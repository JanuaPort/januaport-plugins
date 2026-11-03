"""Private HTTP boundary: auth, routes, validation before any vendor call, caps, key canaries."""

import asyncio
import json
import logging
import re
import unittest
import aiohttp
from aiohttp import web
from aiohttp.test_utils import TestClient, TestServer as AiohttpTestServer
from bing_webmaster import server
from bing_webmaster.adapter import BingAdapter
from bing_webmaster.domain import ReportError
from bing_webmaster.safe_http import SafeAppRunner
from bing_webmaster.server import create_app
from support import BEARER, BING_KEY, BRIDGE, INDEXNOW_KEY, LogCapture, VendorStub, config

PAGE = "https://www.example.test/atas"
SITEMAP = "https://www.example.test/sitemap.xml"
LOG_LINE = re.compile(r"^route=\S+ code=\S+ duration_ms=\d+$")


class SafeTestServer(AiohttpTestServer):
    async def _make_runner(self, **kwargs):
        return SafeAppRunner(self.app, access_log=None, max_line_size=8192, max_field_size=8192, **kwargs)


class RecordingAdapter:
    def __init__(self):
        self.calls = []

    async def run(self, req):
        self.calls.append(req)
        return b'{"state":"ok"}'

    async def close(self):
        pass


class Boundary(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.adapter = RecordingAdapter()
        self.client = TestClient(SafeTestServer(create_app(config(), BRIDGE, self.adapter)))
        await self.client.start_server()

    async def asyncTearDown(self):
        await self.client.close()

    async def test_wrong_or_missing_bridge_key_is_rejected_on_every_route(self):
        routes = [("GET", "/v1/bing/url_info?url=" + PAGE), ("GET", "/v1/bing/sitemaps"), ("GET", "/v1/bing/submission_quota"),
                  ("POST", "/v1/bing/submit_urls"), ("POST", "/v1/bing/submit_sitemap"), ("POST", "/v1/indexnow/submit_urls")]
        for method, path in routes:
            with self.subTest(path):
                missing = await self.client.request(method, path, data=b"{}")
                self.assertEqual(missing.status, 401)
                wrong = await self.client.request(method, path, data=b"{}", headers={"Authorization": "Bearer " + "B" * 44})
                self.assertEqual(wrong.status, 403)
                self.assertEqual(await wrong.json(), {"error": "bridge_forbidden"})
                two = await self.client.request(method, path, data=b"{}", headers=[("Authorization", "Bearer x"), ("Authorization", "Bearer y")])
                self.assertEqual(two.status, 401)
        self.assertEqual(self.adapter.calls, [])

    async def test_unknown_routes_and_methods(self):
        for method, path, status in (
            ("GET", "/v1/bing/GetUrlInfo", 404),
            ("GET", "/v1/bing/delete_site", 404),
            ("POST", "/v1/bing/remove_url", 404),
            ("GET", "/v1/google/url_info", 404),
            ("GET", "/v1/indexnow/url_info", 404),
            ("GET", "/v1/bing", 404),
            ("GET", "/", 404),
            ("POST", "/v1/bing/url_info", 405),
            ("GET", "/v1/bing/submit_urls", 405),
            ("DELETE", "/v1/bing/sitemaps", 405),
        ):
            with self.subTest(path, method=method):
                response = await self.client.request(method, path, headers=BEARER)
                self.assertEqual(response.status, status)
        self.assertEqual(self.adapter.calls, [])

    async def test_local_health_needs_no_key_and_no_vendor(self):
        response = await self.client.get("/healthz")
        self.assertEqual((response.status, await response.json()), (200, {"state": "healthy"}))
        self.assertEqual((await self.client.post("/healthz")).status, 405)
        self.assertEqual(self.adapter.calls, [])

    async def test_negative_inputs_never_reach_the_adapter(self):
        for path in (
            "/v1/bing/url_info",
            "/v1/bing/url_info?url=https://evil.test/",
            "/v1/bing/url_info?url=https://www.example.test:8443/",
            "/v1/bing/url_info?url=https://user@www.example.test/",
            "/v1/bing/url_info?url=https://www.example.test/%23frag",
            "/v1/bing/url_info?url=https://www.example.test/%23frag&url=x",
            "/v1/bing/url_info?url=" + PAGE + "&siteUrl=https://evil.test/",
            "/v1/bing/sitemaps?x=1",
            "/v1/bing/submission_quota?siteUrl=https://evil.test/",
            "/v1/bing/url_info?x=" + "A" * 8200,
        ):
            with self.subTest(path[:60]):
                response = await self.client.get(path, headers=BEARER)
                self.assertEqual(response.status, 400)
                self.assertNotIn("evil", await response.text())
        self.assertEqual(self.adapter.calls, [])

    async def test_get_with_a_body_and_oversized_post_bodies_are_rejected(self):
        response = await self.client.request("GET", "/v1/bing/sitemaps", data=b"{}", headers=BEARER)
        self.assertEqual((response.status, (await response.json())["error"]), (400, "unexpected_body"))
        response = await self.client.post("/v1/bing/submit_urls", data=b'{"urls":"' + b"a" * 9000 + b'"}', headers=BEARER)
        self.assertEqual((response.status, (await response.json())["error"]), (400, "request_too_large"))
        self.assertEqual(self.adapter.calls, [])

    async def test_errors_carry_only_the_fixed_code(self):
        response = await self.client.post("/v1/bing/submit_urls", data=b'{"urls":"https://evil.test/CANARY"}', headers=BEARER)
        self.assertEqual(response.status, 400)
        self.assertEqual(await response.json(), {"error": "invalid_input"})

    async def test_valid_calls_reach_the_adapter_with_parsed_requests(self):
        response = await self.client.get("/v1/bing/url_info?url=" + PAGE, headers=BEARER)
        self.assertEqual(response.status, 200)
        self.assertEqual(self.adapter.calls[0].url, PAGE)
        response = await self.client.post("/v1/bing/submit_urls", data=json.dumps({"urls": PAGE}), headers=BEARER)
        self.assertEqual(response.status, 200)
        self.assertTrue(self.adapter.calls[1].write)

    async def test_parser_rejections_never_echo_raw_input(self):
        with self.assertLogs("bing_webmaster", level="INFO") as logs:
            response = await self.client.get("/v1/bing/sitemaps?SECRET-CANARY=" + "A" * 8200)
        self.assertEqual(response.status, 400)
        self.assertNotIn("SECRET-CANARY", await response.text() + str(logs.output))
        response = await self.client.get("/healthz", headers={"X-Canary": "SECRET-CANARY" + "A" * 8200})
        self.assertEqual(response.status, 400)
        self.assertNotIn("SECRET-CANARY", await response.text())


class Full(unittest.IsolatedAsyncioTestCase):
    """Real adapter against the vendor stub, so 'zero vendor requests' is observable."""

    cfg_over = {}
    budget = 20

    async def asyncSetUp(self):
        self.stub = VendorStub()
        await self.stub.start()
        self.day = "2026-11-02"
        await self.boot()

    async def boot(self, **cfg):
        self.adapter = BingAdapter(
            config(**{**self.cfg_over, **cfg}), BING_KEY, bing_base=self.stub.bing_base, indexnow_url=self.stub.indexnow_url
        )
        app = create_app(config(**{**self.cfg_over, **cfg}), BRIDGE, self.adapter, today=lambda: self.day, budget=self.budget)
        self.client = TestClient(SafeTestServer(app))
        await self.client.start_server()

    async def asyncTearDown(self):
        await self.client.close()
        await self.stub.stop()

    async def post(self, route, body):
        return await self.client.post(route, data=json.dumps(body), headers=BEARER)


class WriteRoutes(Full):
    ROUTES = (
        ("/v1/bing/submit_urls", {"urls": PAGE}, "SubmitUrlBatch"),
        ("/v1/bing/submit_sitemap", {"sitemap_url": SITEMAP}, "SubmitFeed"),
        ("/v1/indexnow/submit_urls", {"urls": PAGE}, "indexnow"),
    )

    async def test_dry_run_validates_but_never_calls_and_never_counts(self):
        for route, body, _ in self.ROUTES:
            with self.subTest(route):
                for _ in range(25):
                    response = await self.post(route, dict(body, dry_run=True))
                    data = await response.json()
                    self.assertEqual((response.status, data["state"], data["dry_run"]), (200, "preview", True))
                bad = await self.post(route, dict(body, dry_run=True, extra=1))
                self.assertEqual(bad.status, 400)
        self.assertEqual(self.stub.calls, [])
        response = await self.post("/v1/bing/submit_sitemap", {"sitemap_url": SITEMAP})
        self.assertEqual((await response.json())["state"], "applied", "previews left the cap untouched")

    async def test_daily_cap_is_twenty_per_route_and_the_21st_has_a_fixed_code(self):
        for route, body, vendor in self.ROUTES:
            with self.subTest(route):
                self.stub.calls.clear()
                for i in range(20):
                    response = await self.post(route, body)
                    self.assertEqual(response.status, 200, i)
                over = await self.post(route, body)
                self.assertEqual((over.status, await over.json()), (429, {"error": "write_cap_reached"}))
                writes = [c for c in self.stub.calls if c["name"] == vendor]
                self.assertEqual(len(writes), 20, "the 21st attempt made no vendor call")

    async def test_the_cap_resets_with_the_utc_day(self):
        for _ in range(20):
            await self.post("/v1/bing/submit_sitemap", {"sitemap_url": SITEMAP})
        self.assertEqual((await self.post("/v1/bing/submit_sitemap", {"sitemap_url": SITEMAP})).status, 429)
        self.day = "2026-11-03"
        self.assertEqual((await self.post("/v1/bing/submit_sitemap", {"sitemap_url": SITEMAP})).status, 200)

    async def test_vendor_failures_and_quota_refusals_count_as_attempts(self):
        self.stub.reply("SubmitFeed", 500, b"")
        for _ in range(20):
            response = await self.post("/v1/bing/submit_sitemap", {"sitemap_url": SITEMAP})
            self.assertEqual((response.status, (await response.json())["error"]), (502, "source_unavailable"))
        self.stub.calls.clear()
        self.stub.reply("SubmitFeed", 200, {"d": None})
        over = await self.post("/v1/bing/submit_sitemap", {"sitemap_url": SITEMAP})
        self.assertEqual(over.status, 429)
        self.assertEqual(self.stub.calls, [])

    async def test_rejected_input_does_not_count(self):
        for _ in range(30):
            response = await self.post("/v1/bing/submit_sitemap", {"sitemap_url": "https://evil.test/sitemap.xml"})
            self.assertEqual(response.status, 400)
        self.assertEqual(self.stub.calls, [])
        self.assertEqual((await self.post("/v1/bing/submit_sitemap", {"sitemap_url": SITEMAP})).status, 200)

    async def test_no_retry_after_a_vendor_error(self):
        self.stub.reply("SubmitFeed", 500, b"")
        await self.post("/v1/bing/submit_sitemap", {"sitemap_url": SITEMAP})
        self.assertEqual(len(self.stub.calls), 1)

    async def test_quota_exceeded_is_reported_without_a_batch_call(self):
        self.stub.reply("GetUrlSubmissionQuota", 200, {"d": {"DailyQuota": 0, "MonthlyQuota": 0}})
        response = await self.post("/v1/bing/submit_urls", {"urls": PAGE})
        self.assertEqual((response.status, await response.json()), (429, {"error": "source_quota_exceeded"}))
        self.assertEqual([c["name"] for c in self.stub.calls], ["GetUrlSubmissionQuota"])

    async def test_foreign_and_malformed_urls_are_rejected_before_any_vendor_call(self):
        for route, key in (("/v1/bing/submit_urls", "urls"), ("/v1/indexnow/submit_urls", "urls"), ("/v1/bing/submit_sitemap", "sitemap_url")):
            for value in ("https://evil.test/a", "http://www.example.test/a", "https://www.example.test:444/a",
                          "https://u@www.example.test/a", "https://www.example.test/a#f", "https://evilexample.test/sitemap.xml"):
                with self.subTest(route, value=value):
                    response = await self.post(route, {key: value})
                    self.assertEqual(response.status, 400)
        self.assertEqual(self.stub.calls, [])


class UnconfiguredIndexNow(Full):
    cfg_over = {"indexnow": None}

    async def test_not_configured_makes_no_call_and_counts_nothing(self):
        for dry in (False, True):
            response = await self.post("/v1/indexnow/submit_urls", {"urls": PAGE, "dry_run": dry})
            self.assertEqual((response.status, await response.json()), (503, {"error": "not_configured"}))
        self.assertEqual(self.stub.calls, [])
        ok = await self.post("/v1/bing/submit_sitemap", {"sitemap_url": SITEMAP})
        self.assertEqual(ok.status, 200)


class LoweredCap(Full):
    cfg_over = {"write_cap_per_day": 3}

    async def test_configured_cap_applies_per_route(self):
        for _ in range(3):
            self.assertEqual((await self.post("/v1/bing/submit_sitemap", {"sitemap_url": SITEMAP})).status, 200)
        self.assertEqual((await self.post("/v1/bing/submit_sitemap", {"sitemap_url": SITEMAP})).status, 429)
        self.assertEqual((await self.post("/v1/bing/submit_urls", {"urls": PAGE})).status, 200)


class ReadRoutes(Full):
    async def test_url_info_host_check_runs_before_the_vendor_call(self):
        for url in ("https://evil.test/", "https://www.example.test:8443/", "https://u@www.example.test/", "https://www.example.test/#x",
                    "http://www.example.test/", "https://evilexample.test/", "https://www.example.test.evil.test/"):
            with self.subTest(url):
                response = await self.client.get("/v1/bing/url_info", params={"url": url}, headers=BEARER)
                self.assertEqual(response.status, 400)
        self.assertEqual(self.stub.calls, [])

    async def test_answers_hold_only_the_fixed_field_list(self):
        response = await self.client.get("/v1/bing/url_info", params={"url": PAGE}, headers=BEARER)
        data = await response.json()
        self.assertEqual(
            sorted(data),
            ["fetched_at", "item", "operation", "schema_version", "source", "state"],
        )
        self.assertEqual(
            sorted(data["item"]),
            ["discovery_date", "document_size", "http_status", "is_page", "last_crawled_date", "url"],
        )
        self.assertNotIn("must-not-appear", json.dumps(data))
        response = await self.client.get("/v1/bing/sitemaps", headers=BEARER)
        data = await response.json()
        self.assertEqual(sorted(data["items"][0]), sorted(["url", "type", "status", "url_count", "file_size", "compressed", "submitted", "last_crawled"]))
        self.assertNotIn("must-not-appear", json.dumps(data))

    async def test_budget_is_enforced(self):
        self.stub.delay = 2
        await self.client.close()
        self.budget = 0.3
        await self.boot()
        response = await self.client.get("/v1/bing/sitemaps", headers=BEARER)
        self.assertEqual((response.status, await response.json()), (504, {"error": "budget_exceeded"}))


class KeyCanary(Full):
    """SEC A1: the Bing key appears in no log line, answer or header on any path."""

    async def asyncSetUp(self):
        server.quiet_libraries()
        await super().asyncSetUp()

    def scenarios(self):
        def echo(request, body):  # a 500 that mirrors the full request URL back
            return web.Response(status=500, text="failed for " + str(request.url) + " " + request.query_string)

        def redirect(request, body):
            return web.Response(status=302, headers={"Location": "https://elsewhere.test/?apikey=" + request.query.get("apikey", "")})

        def bad(request, body):
            return web.json_response({"ErrorCode": 3, "Message": "InvalidApiKey: " + request.query.get("apikey", "")}, status=400)

        def html(request, body):
            return web.Response(text="<html>" + str(request.url) + "</html>", content_type="text/html")

        return {"ok": None, "bing 400": bad, "500 mirror": echo, "3xx": redirect, "html": html}

    async def test_no_path_leaks_the_key(self):
        calls = [
            ("GET", "/v1/bing/url_info", {"params": {"url": PAGE}}),
            ("GET", "/v1/bing/sitemaps", {}),
            ("GET", "/v1/bing/submission_quota", {}),
            ("POST", "/v1/bing/submit_urls", {"data": json.dumps({"urls": PAGE})}),
            ("POST", "/v1/bing/submit_sitemap", {"data": json.dumps({"sitemap_url": SITEMAP})}),
            ("POST", "/v1/indexnow/submit_urls", {"data": json.dumps({"urls": PAGE})}),
        ]
        for name, handler in self.scenarios().items():
            self.stub.handler = handler
            for method, path, kw in calls:
                with self.subTest(name, path=path), LogCapture() as logs:
                    response = await self.client.request(method, path, headers=BEARER, **kw)
                    shown = await response.text() + str(dict(response.headers)) + logs.text
                    self.assertNotIn(BING_KEY, shown)
                    self.assertNotIn("apikey", shown)
                    self.assertNotIn("elsewhere", shown)
                    for line in logs.lines:
                        if line.startswith("route="):
                            self.assertRegex(line, LOG_LINE)

    async def test_timeout_and_connection_failure_do_not_leak(self):
        with LogCapture() as logs:
            self.stub.delay = 2
            await self.client.close()
            self.budget = 0.3
            await self.boot()
            response = await self.client.get("/v1/bing/sitemaps", headers=BEARER)
            self.assertEqual(response.status, 504)
            shown = await response.text() + logs.text
            self.assertNotIn(BING_KEY, shown)
        with LogCapture() as logs:
            await self.stub.stop()
            response = await self.client.get("/v1/bing/sitemaps", headers=BEARER)
            self.assertEqual((response.status, (await response.json())["error"]), (502, "source_unavailable"))
            self.assertNotIn(BING_KEY, await response.text() + logs.text)
            self.assertIn("route=bing/sitemaps code=source_unavailable", logs.text)
        await self.stub.start()

    async def test_success_logs_route_code_and_duration_only(self):
        with LogCapture() as logs:
            response = await self.client.get("/v1/bing/submission_quota", headers=BEARER)
            self.assertEqual(response.status, 200)
        ours = [l for l in logs.lines if l.startswith("route=")]
        self.assertEqual(len(ours), 1)
        self.assertRegex(ours[0], LOG_LINE)
        self.assertIn("code=ok", ours[0])
        self.assertNotIn(BING_KEY, logs.text)
        self.assertNotIn(INDEXNOW_KEY, logs.text)

    async def test_library_loggers_are_quiet_and_exceptions_are_swallowed_into_codes(self):
        for name in server.QUIET_LOGGERS:
            self.assertGreaterEqual(logging.getLogger(name).level, logging.WARNING, name)

        class Boom:
            async def run(self, req):
                raise RuntimeError("https://ssl.bing.com/x?apikey=" + BING_KEY)

            async def close(self):
                pass

        client = TestClient(SafeTestServer(create_app(config(), BRIDGE, Boom())))
        await client.start_server()
        try:
            with LogCapture() as logs:
                response = await client.get("/v1/bing/sitemaps", headers=BEARER)
                self.assertEqual((response.status, await response.json()), (503, {"error": "internal_error"}))
                self.assertNotIn(BING_KEY, logs.text)
        finally:
            await client.close()


if __name__ == "__main__":
    unittest.main()
