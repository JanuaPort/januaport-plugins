"""HTTP boundary and write cap of JanuaPort/januaport#1148, synthetic and without Google."""

import asyncio
import json
import os
from pathlib import Path
import tempfile
import time
import unittest
from aiohttp.test_utils import TestClient
from google_reporting.domain import Config, ReportError
from google_reporting.manage import ManageRequest, parse_post
from google_reporting.server import create_app
from google_reporting.workers import WorkerPool, WRITE_CAP_PER_DAY
from test_boundary import SafeTestServer
from test_contract import fixture

KEY = "A" * 32
AUTH = {"Authorization": "Bearer " + KEY}
APPLIED = {
    "schema_version": "1.0",
    "source": "ga4",
    "operation": "create_annotation",
    "state": "applied",
    "dry_run": False,
    "target": "456",
    "changed_fields": ["annotation_date", "color", "title"],
    "before": None,
    "after": {"annotation_date": "2026-10-22", "color": "BLUE", "title": "[JP] Copy ATAS-12", "description": "Commit 4ffbc7d8"},
    "fetched_at": "2026-10-22T10:00:00Z",
}


class Pool:
    def __init__(self, payload=None):
        self.calls = []
        self.payload = payload or {"state": "ok"}

    async def run(self, req):
        self.calls.append(req)
        return json.dumps(self.payload).encode()

    async def close(self):
        pass


class Routes(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.pool = Pool()
        self.cfg = Config.from_dict(fixture())
        self.client = TestClient(SafeTestServer(create_app(self.cfg, KEY.encode(), self.pool)))
        await self.client.start_server()

    async def asyncTearDown(self):
        await self.client.close()

    async def test_new_get_routes_reach_the_pool_with_a_manage_request(self):
        for path, query in (
            ("/v1/gsc/list_sitemaps", ""),
            ("/v1/gsc/get_sitemap", "?sitemap_url=https://www.example.test/sitemap.xml"),
            ("/v1/gsc/inspect_url", "?page_path=/atas"),
            ("/v1/ga4/list_annotations", ""),
            ("/v1/ga4/list_channel_groups", ""),
        ):
            with self.subTest(path=path):
                response = await self.client.get(path + query, headers=AUTH)
                self.assertEqual(response.status, 200)
        self.assertTrue(all(isinstance(c, ManageRequest) and not c.write for c in self.pool.calls))
        self.assertEqual(len(self.pool.calls), 5)

    async def test_post_routes_reach_the_pool(self):
        body = {"sitemap_url": "https://www.example.test/sitemap.xml", "dry_run": True}
        response = await self.client.post("/v1/gsc/submit_sitemap", json=body, headers=AUTH)
        self.assertEqual(response.status, 200)
        call = self.pool.calls[0]
        self.assertTrue(call.write and call.dry_run)
        self.assertEqual(call.operation, "submit_sitemap")

    async def test_forbidden_routes_do_not_exist(self):
        for method, path in (
            ("POST", "/v1/gsc/delete_sitemap"),
            ("DELETE", "/v1/gsc/submit_sitemap"),
            ("POST", "/v1/ga4/delete_annotation"),
            ("POST", "/v1/ga4/archive_channel_group"),
            ("PUT", "/v1/ga4/create_annotation"),
            ("PATCH", "/v1/ga4/update_annotation"),
            ("POST", "/v1/ga4/update_annotation/456"),
            ("POST", "/v1/ga4/run"),
            ("POST", "/v1/properties/999/create_annotation"),
        ):
            with self.subTest(path=path, method=method):
                response = await self.client.request(method, path, headers=AUTH, data=b"{}")
                self.assertIn(response.status, (404, 405))
        self.assertEqual(self.pool.calls, [])

    async def test_method_is_bound_to_the_route(self):
        response = await self.client.get("/v1/gsc/submit_sitemap", headers=AUTH)
        self.assertEqual(response.status, 405)
        response = await self.client.post("/v1/gsc/list_sitemaps", json={}, headers=AUTH)
        self.assertEqual(response.status, 405)
        self.assertEqual(self.pool.calls, [])

    async def test_post_needs_the_bridge_key(self):
        body = {"date": "2026-10-22", "title": "t"}
        response = await self.client.post("/v1/ga4/create_annotation", json=body)
        self.assertEqual(response.status, 401)
        response = await self.client.post("/v1/ga4/create_annotation", json=body, headers={"Authorization": "Bearer " + "B" * 32})
        self.assertEqual(response.status, 403)
        self.assertEqual(self.pool.calls, [])

    async def test_schema_violations_give_400_before_any_google_call_without_echo(self):
        bad = [
            ("gsc", "submit_sitemap", {"sitemap_url": "https://evil.example/CANARY.xml"}),
            ("gsc", "submit_sitemap", {"sitemap_url": "https://www.example.test/CANARY.xml", "property": "CANARY"}),
            ("ga4", "create_annotation", {"date": "CANARY", "title": "t"}),
            ("ga4", "create_annotation", {"date": "2026-10-22", "title": "a@b.example.com CANARY max.muster@example.test"}),
            ("ga4", "create_channel_group", {"source_hosts": "chatgpt.com", "primary": True, "x": "CANARY"}),
            ("ga4", "update_channel_group", {"channel_group_id": "../CANARY", "source_hosts": "chatgpt.com"}),
            ("ga4", "update_annotation", {"annotation_id": "properties/999/CANARY", "title": "t"}),
        ]
        for source, op, body in bad:
            with self.subTest(op=op, body=str(body)[:40]):
                response = await self.client.post("/v1/%s/%s" % (source, op), json=body, headers=AUTH)
                self.assertEqual(response.status, 400)
                self.assertNotIn("CANARY", await response.text())
        for raw in (b"{", b"[]", b""):
            response = await self.client.post("/v1/ga4/create_annotation", data=raw, headers=AUTH)
            self.assertEqual(response.status, 400)
        self.assertEqual(self.pool.calls, [])

    async def test_get_routes_reject_a_body_and_oversized_post_is_rejected(self):
        response = await self.client.get("/v1/ga4/list_annotations", data=b"{}", headers=AUTH)
        self.assertEqual(response.status, 400)
        response = await self.client.post("/v1/ga4/create_annotation", data=b'{"date":"%s"}' % (b"A" * 9000), headers=AUTH)
        self.assertEqual(response.status, 400)
        self.assertEqual(self.pool.calls, [])

    async def test_change_line_has_a_fixed_format_and_no_free_text_or_key(self):
        self.pool.payload = APPLIED
        body = {"date": "2026-10-22", "title": "Copy ATAS-12", "description": "Commit 4ffbc7d8"}
        with self.assertLogs("google_reporting", level="INFO") as logs:
            response = await self.client.post("/v1/ga4/create_annotation", json=body, headers=AUTH)
        self.assertEqual(response.status, 200)
        lines = logs.output
        changes = [l for l in lines if "change ts=" in l]
        self.assertEqual(len(changes), 1, lines)
        self.assertRegex(changes[0], r"change ts=\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ source=ga4 op=create_annotation target=456 fields=annotation_date,color,title before=null after=\{")
        joined = "\n".join(lines)
        for plain in ("Copy ATAS-12", "Commit 4ffbc7d8", KEY):
            self.assertNotIn(plain, joined)
        # The existing invariant stays: route, code, duration.
        self.assertTrue(any("route=ga4/create_annotation code=ok duration_ms=" in l for l in lines), lines)

    async def test_preview_and_reads_write_no_change_line(self):
        self.pool.payload = dict(APPLIED, state="preview", dry_run=True)
        with self.assertLogs("google_reporting", level="INFO") as logs:
            await self.client.post("/v1/ga4/create_annotation", json={"date": "2026-10-22", "title": "t", "dry_run": True}, headers=AUTH)
            await self.client.get("/v1/ga4/list_annotations", headers=AUTH)
        self.assertFalse(any("change ts=" in l for l in logs.output), logs.output)


class FailingPool(Pool):
    async def run(self, req):
        raise ReportError(409, "annotation_not_managed")


class FixedErrors(unittest.IsolatedAsyncioTestCase):
    async def test_error_code_is_fixed_and_logged_without_change_line(self):
        cfg = Config.from_dict(fixture())
        client = TestClient(SafeTestServer(create_app(cfg, KEY.encode(), FailingPool())))
        await client.start_server()
        try:
            with self.assertLogs("google_reporting", level="INFO") as logs:
                response = await client.post("/v1/ga4/update_annotation", json={"annotation_id": "456", "title": "t"}, headers=AUTH)
            self.assertEqual(response.status, 409)
            self.assertEqual(await response.json(), {"error": "annotation_not_managed"})
            self.assertTrue(any("code=annotation_not_managed" in l for l in logs.output))
            self.assertFalse(any("change ts=" in l for l in logs.output))
        finally:
            await client.close()


class CountingAdapter:
    """Counts real Google attempts in a file: the worker process is not the counter."""

    def __init__(self, cfg, path):
        self.path = path

    def run(self, req):
        with open(self.path, "a") as handle:
            handle.write("x\n")
        if req.operation == "update_annotation":
            time.sleep(10)
        if req.operation == "update_channel_group":
            raise ReportError(502, "source_unavailable")
        return {"operation": req.operation}


def write_req(cfg, source="ga4", op="create_annotation", dry_run=False, **extra):
    body = {"create_annotation": {"date": "2026-10-22", "title": "t"}, "update_annotation": {"annotation_id": "456", "title": "t"}, "update_channel_group": {"channel_group_id": "789", "source_hosts": "claude.ai"}, "submit_sitemap": {"sitemap_url": "https://www.example.test/sitemap.xml"}}[op]
    body = dict(body, **extra)
    if dry_run:
        body["dry_run"] = True
    return parse_post(cfg, source, op, json.dumps(body).encode())


class DailyCap(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.path = self.temp.name + "/attempts"
        self.cfg = Config.from_dict(fixture())
        self.day = ["2026-10-22"]
        self.pool = WorkerPool(self.cfg, self.path, budget=3, adapter_factory=CountingAdapter, today=lambda: self.day[0])

    async def asyncTearDown(self):
        await self.pool.close()

    def attempts(self):
        p = Path(self.path)
        return len(p.read_text().split()) if p.exists() else 0

    async def test_cap_is_twenty_per_source_and_day_and_the_21st_has_a_fixed_code(self):
        self.assertEqual(WRITE_CAP_PER_DAY, 20)
        # Reads share the per-minute limit of the pool, so spread the attempts
        # over time by resetting the minute window between rounds.
        for i in range(20):
            self.pool.rate["ga4"].clear()
            await self.pool.run(write_req(self.cfg))
        self.pool.rate["ga4"].clear()
        with self.assertRaises(ReportError) as cm:
            await self.pool.run(write_req(self.cfg))
        self.assertEqual((cm.exception.status, cm.exception.code), (429, "write_cap_reached"))
        self.assertEqual(self.attempts(), 20)

    async def test_preview_does_not_count(self):
        for _ in range(25):
            self.pool.rate["ga4"].clear()
            await self.pool.run(write_req(self.cfg, dry_run=True))
        self.pool.rate["ga4"].clear()
        await self.pool.run(write_req(self.cfg))
        self.assertEqual(self.attempts(), 26)
        self.assertEqual(self.pool.write_attempts.get(("ga4", "2026-10-22")), 1)

    async def test_failed_attempts_count_and_sources_are_separate(self):
        for _ in range(20):
            self.pool.rate["ga4"].clear()
            with self.assertRaises(ReportError):
                await self.pool.run(write_req(self.cfg, op="update_channel_group"))
        self.pool.rate["ga4"].clear()
        with self.assertRaises(ReportError) as cm:
            await self.pool.run(write_req(self.cfg))
        self.assertEqual(cm.exception.code, "write_cap_reached")
        await self.pool.run(write_req(self.cfg, source="gsc", op="submit_sitemap"))

    async def test_counter_survives_a_killed_worker(self):
        # SEC P3: the counter lives in the long-running server process.
        before = dict(self.pool.write_attempts)
        with self.assertRaises(ReportError) as cm:
            await self.pool.run(write_req(self.cfg, op="update_annotation"))
        self.assertEqual(cm.exception.status, 504)
        pid_alive = any(slot is not None for slot in self.pool.slots)
        self.assertFalse(pid_alive)
        self.assertEqual(self.pool.write_attempts, {("ga4", "2026-10-22"): 1})
        self.assertNotEqual(self.pool.write_attempts, before)
        self.pool.rate["ga4"].clear()
        await self.pool.run(write_req(self.cfg))
        self.assertEqual(self.pool.write_attempts, {("ga4", "2026-10-22"): 2})

    async def test_next_day_starts_at_zero(self):
        for _ in range(20):
            self.pool.rate["ga4"].clear()
            await self.pool.run(write_req(self.cfg))
        self.day[0] = "2026-10-23"
        self.pool.rate["ga4"].clear()
        await self.pool.run(write_req(self.cfg))
        self.assertEqual(self.pool.write_attempts, {("ga4", "2026-10-23"): 1})

    async def test_over_cap_does_not_start_or_kill_a_worker(self):
        for _ in range(20):
            self.pool.rate["ga4"].clear()
            await self.pool.run(write_req(self.cfg))
        started = [slot for slot in self.pool.slots if slot is not None]
        pids = sorted(slot[0].pid for slot in started)
        self.pool.rate["ga4"].clear()
        with self.assertRaises(ReportError):
            await self.pool.run(write_req(self.cfg))
        self.assertEqual(sorted(slot[0].pid for slot in self.pool.slots if slot is not None), pids)


if __name__ == "__main__":
    unittest.main()
