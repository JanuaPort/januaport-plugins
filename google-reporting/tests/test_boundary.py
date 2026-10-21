import asyncio
import json
import os
from pathlib import Path
import tempfile
import time
import unittest
from aiohttp.test_utils import TestClient, TestServer as AiohttpTestServer
from google_reporting.safe_http import SafeAppRunner
from google_reporting.domain import Config, ReportError, parse_request, encode_response
from google_reporting.server import create_app
from google_reporting.workers import WorkerPool
from test_contract import fixture


class SafeTestServer(AiohttpTestServer):
    async def _make_runner(self, **kwargs):
        return SafeAppRunner(
            self.app, access_log=None, max_line_size=8192, max_field_size=8192, **kwargs
        )


class SlowAdapter:
    def __init__(self, cfg, path):
        self.path = path
        self.calls = 0

    def run(self, req):
        Path(self.path).write_text(str(os.getpid()))
        if req.report == "page_summary":
            time.sleep(10)
        self.calls += 1
        return {"calls": self.calls}


class Workers(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.cfg = Config.from_dict(fixture())
        self.pool = WorkerPool(
            self.cfg, self.temp.name + "/pid", budget=3, adapter_factory=SlowAdapter
        )

    async def asyncTearDown(self):
        await self.pool.close()

    async def test_timeout_kills_work_and_releases_capacity(self):
        req = parse_request(self.cfg, "gsc", "page_summary", [])
        with self.assertRaises(ReportError) as cm:
            await self.pool.run(req)
        self.assertEqual(cm.exception.status, 504)
        pid = int(Path(self.temp.name + "/pid").read_text())
        with self.assertRaises(ProcessLookupError):
            os.kill(pid, 0)
        fast = parse_request(self.cfg, "gsc", "performance_overview", [])
        self.assertEqual(json.loads(await self.pool.run(fast))["calls"], 1)

    async def test_client_cancel_kills_process(self):
        task = asyncio.create_task(
            self.pool.run(parse_request(self.cfg, "gsc", "page_summary", []))
        )
        for _ in range(400):
            if Path(self.temp.name + "/pid").exists():
                break
            await asyncio.sleep(0.01)
        pid = int(Path(self.temp.name + "/pid").read_text())
        task.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await task
        with self.assertRaises(ProcessLookupError):
            os.kill(pid, 0)
        self.assertEqual(self.pool.pending, 0)

    async def test_worker_reuses_ram_and_rate_limit(self):
        req = parse_request(self.cfg, "gsc", "performance_overview", [])
        counts = [json.loads(await self.pool.run(req))["calls"] for _ in range(4)]
        self.assertEqual(counts, [1, 1, 2, 2])
        for _ in range(26):
            await self.pool.run(req)
        with self.assertRaises(ReportError) as cm:
            await self.pool.run(req)
        self.assertEqual(cm.exception.status, 429)

    async def test_global_two_workers_and_bounded_queue(self):
        req = parse_request(self.cfg, "gsc", "page_summary", [])
        jobs = [asyncio.create_task(self.pool.run(req)) for _ in range(6)]
        await asyncio.sleep(0.1)
        self.assertEqual(sum(slot is not None for slot in self.pool.slots), 2)
        with self.assertRaises(ReportError) as cm:
            await self.pool.run(req)
        self.assertEqual(cm.exception.status, 429)
        for task in jobs:
            task.cancel()
        await asyncio.gather(*jobs, return_exceptions=True)


class RecordingPool:
    def __init__(self):
        self.calls = []

    async def run(self, req):
        self.calls.append(req)
        return b'{"state":"ok"}'

    async def close(self):
        pass


class HTTP(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.pool = RecordingPool()
        self.cfg = Config.from_dict(fixture())
        self.client = TestClient(
            SafeTestServer(
                create_app(self.cfg, {"gsc": b"A" * 32, "ga4": b"B" * 32}, self.pool)
            )
        )
        await self.client.start_server()

    async def asyncTearDown(self):
        await self.client.close()

    async def test_negative_inputs_never_reach_worker(self):
        for query in (
            "property_id=CANARY",
            "limit=101",
            "limit=1&limit=2",
            "end_date=2999-01-01",
            "segment=atas&page_path=/gateway",
            "page_path=%2Fprivate%3Femail%3Dx",
            "period_days=29",
            "x=" + ("A" * 8200),
        ):
            with self.subTest(query=query[:50]):
                response = await self.client.get(
                    "/v1/gsc/page_summary?" + query,
                    headers={"Authorization": "Bearer " + "A" * 32},
                )
                self.assertEqual(response.status, 400)
                self.assertNotIn("CANARY", await response.text())
        self.assertEqual(self.pool.calls, [])

    async def test_source_keys_are_not_interchangeable(self):
        for source, key in [("gsc", "B"), ("ga4", "A")]:
            response = await self.client.get(
                "/v1/" + source + "/get_status",
                headers={"Authorization": "Bearer " + key * 32},
            )
            self.assertEqual(response.status, 403)
        self.assertEqual(self.pool.calls, [])

    async def test_unconfigured_key_events_and_query_disabled(self):
        response = await self.client.get(
            "/v1/ga4/key_events", headers={"Authorization": "Bearer " + "B" * 32}
        )
        self.assertEqual((await response.json())["state"], "not_configured")
        response = await self.client.get(
            "/v1/gsc/query_summary", headers={"Authorization": "Bearer " + "A" * 32}
        )
        self.assertEqual(response.status, 503)
        self.assertEqual(self.pool.calls, [])

    async def test_local_health_and_normal_call(self):
        response = await self.client.get("/healthz")
        self.assertEqual(response.status, 200)
        self.assertEqual(self.pool.calls, [])
        response = await self.client.get(
            "/v1/gsc/page_summary?segment=atas",
            headers={"Authorization": "Bearer " + "A" * 32},
        )
        self.assertEqual(response.status, 200)
        self.assertEqual(self.pool.calls[0].paths, ["/atas"])

    async def test_parser_rejections_never_echo_raw_input(self):
        with self.assertLogs("google_reporting", level="INFO") as logs:
            response = await self.client.get(
                "/v1/gsc/page_summary?SECRET-CANARY=" + ("A" * 8200)
            )
        self.assertNotIn("SECRET-CANARY", str(logs.output))
        self.assertEqual(response.status, 400)
        self.assertNotIn("SECRET-CANARY", await response.text())
        self.assertEqual(self.pool.calls, [])
        response = await self.client.get(
            "/healthz", headers={"X-Canary": "SECRET-CANARY" + ("A" * 8200)}
        )
        self.assertEqual(response.status, 400)
        self.assertNotIn("SECRET-CANARY", await response.text())

    def test_output_size_cap(self):
        with self.assertRaises(ReportError):
            encode_response({"x": "A" * (512 * 1024)})


class Disconnect(unittest.IsolatedAsyncioTestCase):
    async def test_http_disconnect_kills_google_worker(self):
        with tempfile.TemporaryDirectory() as temp:
            cfg = Config.from_dict(fixture())
            pool = WorkerPool(
                cfg, temp + "/pid", budget=10, adapter_factory=SlowAdapter
            )
            client = TestClient(
                SafeTestServer(
                    create_app(cfg, {"gsc": b"A" * 32, "ga4": b"B" * 32}, pool)
                )
            )
            await client.start_server()
            try:
                task = asyncio.create_task(
                    client.get(
                        "/v1/gsc/page_summary",
                        headers={"Authorization": "Bearer " + "A" * 32},
                    )
                )
                for _ in range(600):
                    if Path(temp + "/pid").exists():
                        break
                    await asyncio.sleep(0.01)
                pid = int(Path(temp + "/pid").read_text())
                task.cancel()
                await asyncio.gather(task, return_exceptions=True)
                for _ in range(100):
                    if pool.pending == 0:
                        break
                    await asyncio.sleep(0.01)
                self.assertEqual(pool.pending, 0)
                with self.assertRaises(ProcessLookupError):
                    os.kill(pid, 0)
            finally:
                await client.close()

    async def test_status_safe_states_and_report_errors_are_distinct(self):
        class UnavailablePool(RecordingPool):
            async def run(self, req):
                raise ReportError(502, "source_access_denied")

        client = TestClient(
            SafeTestServer(
                create_app(
                    Config.from_dict(fixture()),
                    {"gsc": b"A" * 32, "ga4": b"B" * 32},
                    UnavailablePool(),
                )
            )
        )
        await client.start_server()
        try:
            status = await client.get(
                "/v1/gsc/get_status", headers={"Authorization": "Bearer " + "A" * 32}
            )
            self.assertEqual(status.status, 200)
            self.assertEqual((await status.json())["state"], "access_denied")
            report = await client.get(
                "/v1/gsc/page_summary", headers={"Authorization": "Bearer " + "A" * 32}
            )
            self.assertEqual(report.status, 502)
        finally:
            await client.close()
