"""Vendor transport against a loopback stub: wire shapes, fixed error codes, key never leaves."""

import asyncio
import json
import os
import unittest
from unittest import mock
import aiohttp
from aiohttp import web
from yarl import URL
from bing_webmaster import domain
from bing_webmaster.adapter import BingAdapter
from bing_webmaster.domain import ReportError, Request
from support import (
    BING_KEY,
    INDEXNOW_KEY,
    KEY_LOCATION,
    SITE,
    VendorStub,
    config,
)

PAGE = "https://www.example.test/atas"
SITEMAP = "https://www.example.test/sitemap.xml"
SEEN_PREFIX = "/webmaster/api.svc/json/"


def urls_request(source="bing", dry=False, *urls):
    return Request(source, "submit_urls", write=True, dry_run=dry, urls=tuple(urls or (PAGE,)))


class Base(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.stub = VendorStub()
        await self.stub.start()
        self.adapter = self.make()

    def make(self, **kw):
        return BingAdapter(
            config(),
            BING_KEY,
            bing_base=self.stub.bing_base,
            indexnow_url=self.stub.indexnow_url,
            **kw,
        )

    async def asyncTearDown(self):
        await self.adapter.close()
        await self.stub.stop()

    async def run_req(self, req):
        return json.loads(await self.adapter.run(req))

    async def fails(self, req):
        with self.assertRaises(ReportError) as cm:
            await self.adapter.run(req)
        return cm.exception


class Reads(Base):
    async def test_url_info_request_and_projection(self):
        out = await self.run_req(Request("bing", "url_info", url=PAGE))
        (call,) = self.stub.calls
        self.assertEqual((call["method"], call["path"]), ("GET", SEEN_PREFIX + "GetUrlInfo"))
        # Vendor doc shows the url in quotes in the JSON sample only; plain until proven live.
        self.assertEqual(call["query"], {"siteUrl": SITE, "url": PAGE, "apikey": BING_KEY})
        self.assertEqual(call["headers"]["Content-Type"], "application/json; charset=utf-8")
        self.assertEqual((out["source"], out["operation"], out["state"]), ("bing", "url_info", "ok"))
        self.assertEqual(out["item"]["url"], PAGE)
        self.assertEqual(out["item"]["last_crawled_date"], "2011-09-16T22:59:55Z")
        self.assertNotIn("must-not-appear", json.dumps(out))
        self.assertNotIn("TotalChildUrlCount", json.dumps(out))

    async def test_sitemaps_and_quota(self):
        out = await self.run_req(Request("bing", "sitemaps"))
        self.assertEqual(out["items"][0]["url"], SITEMAP)
        out = await self.run_req(Request("bing", "submission_quota"))
        self.assertEqual(out["item"], {"daily_quota": 500, "monthly_quota": 10000})
        self.assertEqual([c["name"] for c in self.stub.calls], ["GetFeeds", "GetUrlSubmissionQuota"])
        for call in self.stub.calls:
            self.assertEqual(call["query"], {"siteUrl": SITE, "apikey": BING_KEY})

    async def test_unexpected_shapes_have_one_fixed_code(self):
        for name, status, body in (
            ("GetUrlInfo", 200, b"<html>maintenance</html>"),
            ("GetUrlInfo", 200, {"d": {"HttpStatus": "x"}}),
            ("GetUrlInfo", 200, b"x" * (300 * 1024)),
            ("GetUrlInfo", 200, b""),
        ):
            with self.subTest(body=str(body)[:20]):
                self.stub.reply(name, status, body)
                exc = await self.fails(Request("bing", "url_info", url=PAGE))
                self.assertEqual((exc.status, exc.code), (502, "unexpected_source_shape"))


class Writes(Base):
    async def test_submit_urls_checks_the_quota_then_posts_the_batch(self):
        urls = (PAGE, "https://example.test/b")
        out = await self.run_req(urls_request("bing", False, *urls))
        self.assertEqual([c["name"] for c in self.stub.calls], ["GetUrlSubmissionQuota", "SubmitUrlBatch"])
        post = self.stub.calls[1]
        self.assertEqual(post["method"], "POST")
        self.assertEqual(post["query"], {"apikey": BING_KEY})
        self.assertEqual(post["headers"]["Content-Type"], "application/json; charset=utf-8")
        self.assertEqual(json.loads(post["body"]), {"siteUrl": SITE, "urlList": list(urls)})
        self.assertEqual(out["state"], "applied")
        self.assertEqual((out["dry_run"], out["submitted_count"], out["quota_before"]), (False, 2, 500))
        self.assertEqual(out["urls"], list(urls))

    async def test_never_submits_above_the_reported_quota(self):
        self.stub.reply("GetUrlSubmissionQuota", 200, {"d": {"DailyQuota": 1, "MonthlyQuota": 100}})
        exc = await self.fails(urls_request("bing", False, PAGE, "https://example.test/b"))
        self.assertEqual((exc.status, exc.code), (429, "source_quota_exceeded"))
        self.assertEqual([c["name"] for c in self.stub.calls], ["GetUrlSubmissionQuota"])

    async def test_a_quota_of_exactly_the_batch_size_is_enough(self):
        self.stub.reply("GetUrlSubmissionQuota", 200, {"d": {"DailyQuota": 2, "MonthlyQuota": 100}})
        out = await self.run_req(urls_request("bing", False, PAGE, "https://example.test/b"))
        self.assertEqual(out["state"], "applied")

    async def test_submit_sitemap(self):
        out = await self.run_req(Request("bing", "submit_sitemap", write=True, url=SITEMAP))
        (call,) = self.stub.calls
        self.assertEqual((call["method"], call["name"]), ("POST", "SubmitFeed"))
        self.assertEqual(json.loads(call["body"]), {"siteUrl": SITE, "feedUrl": SITEMAP})
        self.assertEqual((out["state"], out["target"], out["urls"]), ("applied", SITEMAP, []))

    async def test_a_write_answer_other_than_null_is_a_shape_error(self):
        self.stub.reply("SubmitFeed", 200, {"d": "accepted"})
        exc = await self.fails(Request("bing", "submit_sitemap", write=True, url=SITEMAP))
        self.assertEqual((exc.status, exc.code), (502, "unexpected_source_shape"))

    async def test_indexnow_wire_body_and_no_bing_key(self):
        self.stub.reply("indexnow", 202, b"")
        out = await self.run_req(urls_request("indexnow", False, PAGE))
        (call,) = self.stub.calls
        self.assertEqual((call["method"], call["path"]), ("POST", "/indexnow"))
        self.assertEqual(call["headers"]["Content-Type"], "application/json; charset=utf-8")
        self.assertEqual(
            json.loads(call["body"]),
            {"host": "www.example.test", "key": INDEXNOW_KEY, "keyLocation": KEY_LOCATION, "urlList": [PAGE]},
        )
        self.assertEqual(call["query"], {})
        sent = json.dumps({k: v for k, v in call.items() if k != "body"}, default=str) + call["body"].decode()
        self.assertNotIn(BING_KEY, sent)
        self.assertEqual((out["state"], out["vendor_status"], out["submitted_count"]), ("applied", 202, 1))
        self.assertIsNone(out["quota_before"])

    async def test_indexnow_ok_status(self):
        out = await self.run_req(urls_request("indexnow", False, PAGE))
        self.assertEqual(out["vendor_status"], 200)

    async def test_indexnow_without_configuration_makes_no_call(self):
        adapter = BingAdapter(
            config(indexnow=None), BING_KEY, bing_base=self.stub.bing_base, indexnow_url=self.stub.indexnow_url
        )
        try:
            with self.assertRaises(ReportError) as cm:
                await adapter.run(urls_request("indexnow"))
        finally:
            await adapter.close()
        self.assertEqual((cm.exception.status, cm.exception.code), (503, "not_configured"))
        self.assertEqual(self.stub.calls, [])


class ErrorCodes(Base):
    async def test_bing_http_error_table(self):
        cases = [
            (400, {"ErrorCode": 3, "Message": "InvalidApiKey " + BING_KEY}, 502, "bing_api_key_invalid"),
            (400, {"ErrorCode": 7, "Message": "other " + BING_KEY}, 502, "source_rejected"),
            (400, b"not json " + BING_KEY.encode(), 502, "source_rejected"),
            (401, {}, 502, "source_rejected"),
            (403, {}, 502, "source_rejected"),
            (404, {}, 502, "source_rejected"),
            (429, {}, 502, "source_quota_limited"),
            (500, b"echo " + BING_KEY.encode(), 502, "source_unavailable"),
            (503, {}, 502, "source_unavailable"),
            (302, {}, 502, "source_redirect"),
            (307, {}, 502, "source_redirect"),
        ]
        for status, body, want_status, want_code in cases:
            with self.subTest(status=status, body=str(body)[:30]):
                self.stub.calls.clear()
                self.stub.reply("GetUrlInfo", status, body)
                exc = await self.fails(Request("bing", "url_info", url=PAGE))
                self.assertEqual((exc.status, exc.code), (want_status, want_code))
                self.assertNotIn(BING_KEY, repr(exc) + str(exc))
                self.assertEqual(len(self.stub.calls), 1, "no retry")

    async def test_indexnow_http_error_table(self):
        cases = [
            (400, "source_rejected"),
            (403, "indexnow_key_invalid"),
            (422, "indexnow_url_rejected"),
            (429, "source_quota_limited"),
            (500, "source_unavailable"),
            (302, "source_redirect"),
        ]
        for status, code in cases:
            with self.subTest(status=status):
                self.stub.calls.clear()
                self.stub.reply("indexnow", status, b"body")
                exc = await self.fails(urls_request("indexnow"))
                self.assertEqual((exc.status, exc.code), (502, code))
                self.assertEqual(len(self.stub.calls), 1, "no retry")

    async def test_a_failing_quota_read_stops_the_batch(self):
        self.stub.reply("GetUrlSubmissionQuota", 500, b"")
        exc = await self.fails(urls_request())
        self.assertEqual(exc.code, "source_unavailable")
        self.assertEqual([c["name"] for c in self.stub.calls], ["GetUrlSubmissionQuota"])

    async def test_redirects_are_not_followed(self):
        def handler(request, body):
            return web.Response(status=302, headers={"Location": "/leak?apikey=" + request.query.get("apikey", "")})

        self.stub.handler = handler
        exc = await self.fails(Request("bing", "sitemaps"))
        self.assertEqual(exc.code, "source_redirect")
        self.assertEqual(len(self.stub.calls), 1)

    async def test_timeout_is_a_fixed_code(self):
        self.stub.delay = 1
        adapter = self.make(request_timeout=0.2)
        try:
            with self.assertRaises(ReportError) as cm:
                await adapter.run(Request("bing", "sitemaps"))
        finally:
            await adapter.close()
        self.assertEqual((cm.exception.status, cm.exception.code), (504, "budget_exceeded"))

    async def test_connection_failure_is_a_fixed_code(self):
        await self.stub.stop()
        try:
            exc = await self.fails(Request("bing", "sitemaps"))
            self.assertEqual((exc.status, exc.code), (502, "source_unavailable"))
        finally:
            await self.stub.start()
            self.adapter._base = self.stub.bing_base


class FailingSession:
    """A transport whose exceptions carry the full request URL, as real libraries do."""

    def __init__(self, make_exc):
        self.make_exc = make_exc
        self.closed = False

    def request(self, method, url, **kw):
        raise self.make_exc(url + "?apikey=" + BING_KEY)

    async def close(self):
        self.closed = True


class KeyNeverInExceptions(unittest.IsolatedAsyncioTestCase):
    """SEC A1: library exception texts embed the URL with ?apikey=; only fixed codes leave."""

    FACTORIES = {
        "client error": lambda text: aiohttp.ClientError(text),
        "response error": lambda text: aiohttp.ClientResponseError(
            aiohttp.RequestInfo(url=URL(text), method="GET", headers={}, real_url=URL(text)),
            (),
            message=text,
        ),
        "os error": lambda text: OSError(text),
        "invalid url": lambda text: aiohttp.InvalidURL(text),
        "too many redirects": lambda text: aiohttp.TooManyRedirects(None, (), message=text),
        "value error": lambda text: ValueError(text),
        "runtime error": lambda text: RuntimeError(text),
        "timeout": lambda text: asyncio.TimeoutError(text),
    }
    CODES = {"timeout": (504, "budget_exceeded")}

    async def test_every_transport_exception_becomes_a_fixed_code_without_chain(self):
        for name, factory in self.FACTORIES.items():
            for req in (Request("bing", "sitemaps"), urls_request("indexnow")):
                with self.subTest(name, op=req.operation + req.source):
                    adapter = BingAdapter(config(), BING_KEY, session=FailingSession(factory))
                    with self.assertRaises(ReportError) as cm:
                        await adapter.run(req)
                    exc = cm.exception
                    shown = repr(exc) + str(exc) + repr(exc.args) + exc.code
                    self.assertNotIn(BING_KEY, shown)
                    self.assertNotIn("apikey", shown)
                    self.assertIsNone(exc.__cause__)
                    self.assertIsNone(exc.__context__, "the original exception must not stay reachable")
                    want = self.CODES.get(name, (502, "source_unavailable"))
                    if name in ("value error", "runtime error"):
                        want = (502, "source_unavailable")
                    self.assertEqual((exc.status, exc.code), want)

    async def test_repr_of_the_adapter_hides_the_key(self):
        adapter = BingAdapter(config(), BING_KEY)
        self.assertNotIn(BING_KEY, repr(adapter) + str(adapter))


class Transport(Base):
    async def test_proxy_environment_is_ignored(self):
        env = {"HTTP_PROXY": "http://127.0.0.1:1", "http_proxy": "http://127.0.0.1:1", "HTTPS_PROXY": "http://127.0.0.1:1"}
        with mock.patch.dict(os.environ, env):
            adapter = self.make()
            try:
                out = json.loads(await adapter.run(Request("bing", "submission_quota")))
            finally:
                await adapter.close()
        self.assertEqual(out["state"], "ok")

    async def test_session_does_not_trust_the_environment(self):
        session = await self.adapter._ensure_session()
        self.assertFalse(session.trust_env)

    async def test_vendor_endpoints_are_fixed_constants(self):
        from bing_webmaster import adapter as module

        self.assertEqual(module.BING_BASE, "https://ssl.bing.com/webmaster/api.svc/json")
        self.assertEqual(module.INDEXNOW_URL, "https://api.indexnow.org/indexnow")
        self.assertNotIn("bing_base", config().__dict__)


if __name__ == "__main__":
    unittest.main()
