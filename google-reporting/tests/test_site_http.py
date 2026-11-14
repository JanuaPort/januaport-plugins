"""Site header, HTTP matrix and per-site write cap (JanuaPort/januaport#1379, part A).

Synthetic, no Google. The header names the site after the Bridge key check and before any
parsing; an unresolved site reaches neither the pool nor a counter.
"""

import json
import tempfile
import time
import unittest
from pathlib import Path
from aiohttp.test_utils import TestClient
from multidict import CIMultiDict

from google_reporting.domain import ReportError, Sites
from google_reporting.manage import ManageRequest
from google_reporting.server import create_app
from google_reporting.workers import WRITE_CAP_PER_DAY, WorkerPool
from site_fixtures import JANUA, TERMIN, januaport_entry, sites_raw, terminagent_entry
from test_boundary import SafeTestServer
from test_contract import fixture
from test_manage_http import APPLIED, CountingAdapter, Pool, write_req

KEY = "A" * 32
SITE = "X-Google-Reporting-Site"
REPORT = ("GET", "/v1/gsc/page_summary")
MANAGE = ("GET", "/v1/ga4/list_annotations")
WRITE = ("POST", "/v1/ga4/create_annotation")
ANNOTATION = {"date": "2026-11-14", "title": "Copy ATAS-12"}


def headers(site=None):
    base = CIMultiDict({"Authorization": "Bearer " + KEY})
    if site is None:
        return base
    for value in [site] if isinstance(site, str) else site:
        base.add(SITE, value)
    return base


async def call(client, request, site=None):
    method, path = request
    if method == "POST":
        return await client.post(path, json=ANNOTATION, headers=headers(site))
    return await client.get(path, headers=headers(site))


class Matrix(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.pools, self.clients = {}, {}
        for form, raw in (
            ("single", fixture()),
            ("list-1", sites_raw(januaport_entry())),
            ("list-2", sites_raw()),
        ):
            self.pools[form] = Pool()
            app = create_app(Sites.from_dict(raw), KEY.encode(), self.pools[form])
            self.clients[form] = TestClient(SafeTestServer(app))
            await self.clients[form].start_server()

    async def asyncTearDown(self):
        for client in self.clients.values():
            await client.close()

    async def test_header_by_form_by_request_kind(self):
        required, unknown = "site_required", "site_unknown"
        table = [
            # label, header, single, list-1, list-2
            ("missing", None, "default", required, required),
            ("empty", "", unknown, unknown, unknown),
            ("twice", [JANUA, JANUA], unknown, unknown, unknown),
            ("twice, different", [JANUA, TERMIN], unknown, unknown, unknown),
            ("unknown", "nope-site", unknown, unknown, unknown),
            ("capital letter", "Terminagent", unknown, unknown, unknown),
            ("inner blank", "termin agent", unknown, unknown, unknown),
            ("default", "default", "default", unknown, unknown),
            ("januaport", JANUA, unknown, JANUA, JANUA),
            ("terminagent", TERMIN, unknown, unknown, TERMIN),
        ]
        for label, header, *expected in table:
            for (form, client), want in zip(self.clients.items(), expected):
                for request in (REPORT, MANAGE, WRITE):
                    with self.subTest(label=label, form=form, request=request[1]):
                        pool = self.pools[form]
                        before = len(pool.calls)
                        response = await call(client, request, header)
                        if want.startswith("site_"):
                            self.assertEqual(response.status, 400)
                            self.assertEqual(await response.json(), {"error": want})
                            self.assertEqual(len(pool.calls), before)
                        else:
                            self.assertEqual(response.status, 200)
                            self.assertEqual(len(pool.calls), before + 1)
                            self.assertEqual(pool.calls[-1].site, want)

    async def test_the_error_never_echoes_the_header_value(self):
        client = self.clients["list-2"]
        for value in ("canary-site", "CANARY", "canary site", "c" * 40):
            with self.subTest(value=value), self.assertLogs("google_reporting", level="INFO") as logs:
                response = await call(client, REPORT, value)
                text = await response.text()
            self.assertEqual(response.status, 400)
            self.assertNotIn(value, text)
            self.assertNotIn(value.lower(), text.lower())
            self.assertNotIn(value, "\n".join(logs.output))

    async def test_requests_reach_the_pool_with_the_own_site_config(self):
        client, pool = self.clients["list-2"], self.pools["list-2"]
        await call(client, REPORT, TERMIN)
        await call(client, REPORT, JANUA)
        await client.get("/v1/gsc/inspect_url?page_path=/preise", headers=headers(TERMIN))
        self.assertEqual([c.site for c in pool.calls], [TERMIN, JANUA, TERMIN])
        self.assertEqual(pool.calls[0].paths, ["/", "/preise"])
        self.assertEqual(pool.calls[1].paths, ["/atas", "/gateway", "/"])
        self.assertEqual(pool.calls[2].params["url"], "https://www.termin.test/preise")
        # The page of the other site is not a page of this one: no pool call.
        response = await client.get("/v1/gsc/inspect_url?page_path=/atas", headers=headers(TERMIN))
        self.assertEqual((response.status, await response.json()), (400, {"error": "invalid_input"}))
        response = await client.get(
            "/v1/gsc/get_sitemap?sitemap_url=https://www.example.test/sitemap.xml", headers=headers(TERMIN)
        )
        self.assertEqual(response.status, 400)
        response = await client.post(
            "/v1/gsc/submit_sitemap",
            json={"sitemap_url": "https://www.example.test/sitemap.xml"},
            headers=headers(TERMIN),
        )
        self.assertEqual(response.status, 400)
        self.assertEqual(len(pool.calls), 3)

    async def test_the_site_is_never_a_parameter(self):
        client, pool = self.clients["list-2"], self.pools["list-2"]
        response = await client.get("/v1/gsc/page_summary?site=" + TERMIN, headers=headers(JANUA))
        self.assertEqual((response.status, await response.json()), (400, {"error": "invalid_input"}))
        response = await client.get("/v1/gsc/page_summary?site=" + TERMIN, headers=headers())
        self.assertEqual((response.status, await response.json()), (400, {"error": "site_required"}))
        response = await client.post(
            "/v1/ga4/create_annotation", json=dict(ANNOTATION, site=TERMIN), headers=headers(JANUA)
        )
        self.assertEqual((response.status, await response.json()), (400, {"error": "invalid_input"}))
        self.assertEqual(pool.calls, [])

    async def test_the_bridge_key_comes_before_the_site(self):
        client = self.clients["list-2"]
        for request in (REPORT, MANAGE, WRITE):
            method, path = request
            first = await client.request(method, path, headers=CIMultiDict({SITE: "nope-site"}))
            self.assertEqual((first.status, await first.json()), (401, {"error": "bridge_unauthorized"}))
            wrong = CIMultiDict({"Authorization": "Bearer " + "B" * 32, SITE: "nope-site"})
            second = await client.request(method, path, headers=wrong)
            self.assertEqual((second.status, await second.json()), (403, {"error": "bridge_forbidden"}))
        self.assertEqual(self.pools["list-2"].calls, [])

    async def test_health_needs_no_site(self):
        response = await self.clients["list-2"].get("/healthz")
        self.assertEqual(response.status, 200)

    async def test_log_line_carries_the_resolved_site_and_dashes_without_one(self):
        client = self.clients["list-2"]
        with self.assertLogs("google_reporting", level="INFO") as logs:
            await call(client, REPORT, TERMIN)
            await call(client, REPORT, "nope-site")
            await call(client, REPORT, None)
        lines = [line for line in logs.output if "route=" in line]
        self.assertRegex(lines[0], r"route=gsc/page_summary site=terminagent code=ok duration_ms=\d+")
        self.assertRegex(lines[1], r"route=gsc/page_summary site=- code=site_unknown duration_ms=\d+")
        self.assertRegex(lines[2], r"route=gsc/page_summary site=- code=site_required duration_ms=\d+")

    async def test_change_line_carries_the_site(self):
        pool = self.pools["list-2"]
        pool.payload = APPLIED
        with self.assertLogs("google_reporting", level="INFO") as logs:
            response = await call(self.clients["list-2"], WRITE, TERMIN)
        self.assertEqual(response.status, 200)
        changes = [line for line in logs.output if "change ts=" in line]
        self.assertEqual(len(changes), 1, logs.output)
        self.assertRegex(
            changes[0],
            r"change ts=\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ site=terminagent source=ga4 op=create_annotation target=456 ",
        )

    async def test_single_form_change_line_says_default(self):
        pool = self.pools["single"]
        pool.payload = APPLIED
        with self.assertLogs("google_reporting", level="INFO") as logs:
            await call(self.clients["single"], WRITE)
        self.assertTrue(any(" site=default source=ga4 " in line for line in logs.output), logs.output)

    async def test_switches_are_per_site(self):
        raw = sites_raw(januaport_entry(), dict(terminagent_entry(), query_enabled=True))
        pool = Pool()
        client = TestClient(SafeTestServer(create_app(Sites.from_dict(raw), KEY.encode(), pool)))
        await client.start_server()
        try:
            denied = await client.get("/v1/gsc/query_summary", headers=headers(JANUA))
            self.assertEqual((denied.status, await denied.json()), (503, {"error": "query_not_approved"}))
            allowed = await client.get("/v1/gsc/query_summary", headers=headers(TERMIN))
            self.assertEqual(allowed.status, 200)
            events = await client.get("/v1/ga4/key_events", headers=headers(TERMIN))
            self.assertEqual((await events.json())["state"], "not_configured")
            self.assertEqual((await events.json())["scope"]["site"], TERMIN)
        finally:
            await client.close()

    async def test_status_fallback_names_the_site(self):
        class Unavailable(Pool):
            async def run(self, req):
                raise ReportError(502, "source_access_denied")

        client = TestClient(SafeTestServer(create_app(Sites.from_dict(sites_raw()), KEY.encode(), Unavailable())))
        await client.start_server()
        try:
            status = await client.get("/v1/gsc/get_status", headers=headers(TERMIN))
            body = await status.json()
            self.assertEqual((status.status, body["state"], body["site"]), (200, "access_denied", TERMIN))
            self.assertNotIn("654321", json.dumps(body))
        finally:
            await client.close()


class Cap(unittest.IsolatedAsyncioTestCase):
    """SEC D1: resolve the site first, then count; the key is (site, source, day)."""

    DAY = "2026-11-14"

    async def asyncSetUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.path = self.temp.name + "/attempts"
        self.sites = Sites.from_dict(sites_raw())
        self.pool = WorkerPool(
            self.sites, self.path, budget=3, adapter_factory=CountingAdapter, today=lambda: self.DAY
        )
        self.client = TestClient(SafeTestServer(create_app(self.sites, KEY.encode(), self.pool)))
        await self.client.start_server()

    async def asyncTearDown(self):
        await self.client.close()
        await self.pool.close()

    def attempts(self):
        path = Path(self.path)
        return len(path.read_text().split()) if path.exists() else 0

    async def test_the_cap_is_per_source_and_site_and_unresolved_sites_count_nowhere(self):
        self.assertEqual(WRITE_CAP_PER_DAY, 20)
        # A vague or foreign header must not eat the quota of a real site.
        for value in ["nope-site"] * 12 + ["Januaport"] * 6 + ["termin agent"] * 7:
            response = await call(self.client, WRITE, value)
            self.assertEqual((response.status, await response.json()), (400, {"error": "site_unknown"}))
        for _ in range(3):
            response = await call(self.client, WRITE, None)
            self.assertEqual((response.status, await response.json()), (400, {"error": "site_required"}))
        self.assertEqual(self.pool.write_attempts, {})
        self.assertEqual(self.attempts(), 0)
        # 20 on A are fine, the 21st is refused without a Google call.
        for i in range(WRITE_CAP_PER_DAY):
            response = await call(self.client, WRITE, JANUA)
            self.assertEqual(response.status, 200, i)
        self.assertEqual(self.attempts(), 20)
        refused = await call(self.client, WRITE, JANUA)
        self.assertEqual((refused.status, await refused.json()), (429, {"error": "write_cap_reached"}))
        self.assertEqual(self.attempts(), 20)
        # B is independent: its first attempt runs.
        allowed = await call(self.client, WRITE, TERMIN)
        self.assertEqual(allowed.status, 200)
        self.assertEqual(self.attempts(), 21)
        self.assertEqual(
            self.pool.write_attempts,
            {(JANUA, "ga4", self.DAY): 20, (TERMIN, "ga4", self.DAY): 1},
        )
        # Unresolved headers still count nowhere, also now that A is full.
        for value in ("nope-site", None):
            await call(self.client, WRITE, value)
        self.assertEqual(self.attempts(), 21)
        self.assertEqual(sum(self.pool.write_attempts.values()), 21)

    async def test_the_cap_of_one_site_does_not_lock_the_other(self):
        for _ in range(WRITE_CAP_PER_DAY):
            await call(self.client, WRITE, TERMIN)
        refused = await call(self.client, WRITE, TERMIN)
        self.assertEqual(refused.status, 429)
        for _ in range(3):
            self.assertEqual((await call(self.client, WRITE, JANUA)).status, 200)

    async def test_previews_count_for_no_site(self):
        for site in (JANUA, TERMIN):
            for _ in range(25):
                self.pool.rate["ga4"].clear()
                await self.pool.run(write_req(self.sites.config(site), dry_run=True))
        self.assertEqual(self.pool.write_attempts, {})

    async def test_the_next_day_starts_at_zero_for_each_site(self):
        await self.pool.run(write_req(self.sites.config(JANUA)))
        await self.pool.run(write_req(self.sites.config(TERMIN)))
        self.DAY = "2026-11-15"
        await self.pool.run(write_req(self.sites.config(JANUA)))
        self.assertEqual(self.pool.write_attempts, {(JANUA, "ga4", "2026-11-15"): 1})

    async def test_the_minute_limit_stays_shared_per_source(self):
        # SEC D3: one Google project quota, so 30 per minute and source in total.
        reports = [
            ManageRequest("ga4", "list_annotations", {}, site=site) for site in (JANUA, TERMIN)
        ]
        self.pool.rate["ga4"].extend([time.monotonic()] * 30)
        for req in reports:
            with self.assertRaises(ReportError) as ctx:
                await self.pool.run(req)
            self.assertEqual((ctx.exception.status, ctx.exception.code), (429, "source_rate_limited"))


if __name__ == "__main__":
    unittest.main()
