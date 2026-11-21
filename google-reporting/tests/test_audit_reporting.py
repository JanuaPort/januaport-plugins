"""Regression probes for #1433, #1437 and #1438, with synthetic SDK wire data."""

import ast
import json
from pathlib import Path
import re
import unittest
from unittest.mock import patch
from urllib.parse import urlsplit

from aiohttp.test_utils import TestClient
import requests

from google_reporting.domain import ReportError, Sites, encode_response, parse_request
from google_reporting.server import create_app
from test_boundary import SafeTestServer
import test_google_adapter as wire


ROOT = Path(__file__).resolve().parents[1]
DEFINITION_CODES = (
    "report_definition_unverified",
    "report_definition_incompatible",
    "source_timezone_mismatch",
)


class AdapterPool:
    def __init__(self, adapter):
        self.adapter = adapter

    async def run(self, req):
        return encode_response(self.adapter.run(req))

    async def close(self):
        pass


class StatusHTTP(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.wire = wire.Adapter()
        self.wire.setUp()
        self.client = TestClient(SafeTestServer(create_app(
            Sites.single(self.wire.cfg), b"A" * 32, AdapterPool(self.wire.adapter)
        )))
        await self.client.start_server()
        self.addAsyncCleanup(self.client.close)

    async def get(self, source, report="get_status", query="", key="A"):
        return await self.client.get(
            f"/v1/{source}/{report}" + query,
            headers={"Authorization": "Bearer " + key * 32},
        )

    async def test_status_code_matrix_for_both_sources(self):
        # Some definition errors currently arise only in GA4. The HTTP contract
        # deliberately applies the same mapping to both fixed status routes.
        class FailingPool:
            async def run(inner, req):
                raise ReportError(status, code)

        with patch.object(AdapterPool, "run", FailingPool.run):
            for source in ("gsc", "ga4"):
                for status, code, state in (
                    *((503, code, "definition_invalid") for code in DEFINITION_CODES),
                    (502, "source_access_denied", "access_denied"),
                    (503, "source_quota_limited", "quota_limited"),
                    (503, "configuration_invalid", "not_configured"),
                    (502, "source_unavailable", "unavailable"),
                    (504, "source_timeout", "unavailable"),
                ):
                    with self.subTest(source=source, code=code):
                        response = await self.get(source)
                        body = await response.json()
                        self.assertEqual(response.status, 200)
                        self.assertEqual((body["state"], body.get("code")), (state, code))
                        self.assertEqual(body["site"], "default")
                        self.assertEqual(body["definition_version"], self.wire.cfg.definition_version)
                        self.assertEqual(body["source"], source)

    async def test_actual_adapter_definition_failures_at_http_boundary(self):
        original_send = self.wire.send
        scenario = ""

        def send(session, prepared, **kwargs):
            if ":checkCompatibility" not in prepared.url:
                return original_send(session, prepared, **kwargs)
            self.wire.calls.append((prepared.url, prepared.method, prepared.body, {}, kwargs))
            fields = json.loads(prepared.body)
            data = {}
            for kind in ("dimension", "metric"):
                data[kind + "Compatibilities"] = [
                    {kind + "Metadata": {"apiName": field["name"]},
                     "compatibility": "INCOMPATIBLE" if scenario == DEFINITION_CODES[1] else "COMPATIBLE"}
                    for field in fields[kind + "s"]
                ]
            if scenario == DEFINITION_CODES[0]:
                data["dimensionCompatibilities"] = []
            response = requests.Response()
            response.request = prepared
            response.status_code = 200
            response._content = json.dumps(data).encode()
            return response

        a, _, c = self.wire.patches()
        with a, c, patch.object(requests.Session, "send", send):
            for scenario in DEFINITION_CODES:
                with self.subTest(code=scenario):
                    self.wire.calls.clear()
                    self.wire.body = {"metadata": {"timeZone": (
                        "UTC" if scenario == "source_timezone_mismatch" else "Europe/Berlin"
                    )}}
                    response = await self.get("ga4")
                    body = await response.json()
                    self.assertEqual(response.status, 200)
                    self.assertEqual((body["state"], body.get("code")), ("definition_invalid", scenario))
                    reports = [url for url, *_ in self.wire.calls if ":runReport" in url]
                    # Failed compatibility must block the report call itself.
                    self.assertEqual(len(reports), int(scenario == "source_timezone_mismatch"))

            # The same timezone failure on a normal report remains an HTTP error.
            response = await self.get("ga4", "traffic_summary")
            self.assertEqual(response.status, 503)
            self.assertEqual(await response.json(), {"error": "source_timezone_mismatch"})

    async def test_actual_google_error_text_is_discarded(self):
        a, b, c = self.wire.patches()
        with a, b, c:
            self.wire.status = 403
            with self.assertLogs("google_reporting", level="INFO") as logs:
                for source in ("gsc", "ga4"):
                    response = await self.get(source)
                    body = await response.json()
                    self.assertEqual(response.status, 200)
                    self.assertEqual((body["state"], body.get("code")), ("access_denied", "source_access_denied"))
                    self.assertEqual(set(body), {
                        "schema_version", "source", "report", "state", "code", "checked_at",
                        "definition_version", "site",
                    })
                    self.assertNotIn("SECRET-CANARY", json.dumps(body))
            self.assertNotIn("SECRET-CANARY", str(logs.output))
            self.assertTrue(any("analyticsdata." in url for url, *_ in self.wire.calls))
            self.assertTrue(any("searchconsole." in url for url, *_ in self.wire.calls))

    async def test_bridge_then_parameters_before_actual_adapter(self):
        a, b, c = self.wire.patches()
        with a, b, c:
            for source, report in (("gsc", "page_summary"), ("ga4", "traffic_summary")):
                response = await self.get(source, report, "?unknown=SYNTHETIC-CANARY", key="B")
                self.assertEqual((response.status, await response.json()), (403, {"error": "bridge_forbidden"}))
                response = await self.get(source, report, "?unknown=SYNTHETIC-CANARY")
                self.assertEqual((response.status, await response.json()), (400, {"error": "invalid_input"}))
            self.assertEqual(self.wire.adapter.clients, {})
            self.assertEqual(self.wire.calls, [])


class Channels(unittest.TestCase):
    def test_standard_channels_survive_actual_adapter_projection(self):
        from google_reporting.manage import STANDARD_CHANNELS

        helper = wire.Adapter()
        helper.setUp()
        a, b, c = helper.patches()
        with a, b, c:
            for channel in (*STANDARD_CHANNELS, "(other)"):
                with self.subTest(channel=channel):
                    helper.body["rows"][0]["dimensionValues"][0]["value"] = channel
                    result = helper.adapter.run(parse_request(helper.cfg, "ga4", "traffic_summary", []))
                    self.assertEqual(result["rows"], [{
                        "channel": channel, "sessions": 42, "engaged_sessions": 31,
                    }])

    def test_unknown_channel_is_suppressed_by_actual_adapter(self):
        helper = wire.Adapter()
        helper.setUp()
        a, b, c = helper.patches()
        with a, b, c:
            helper.body["rows"][0]["dimensionValues"][0]["value"] = "SYNTHETIC-UNKNOWN-CHANNEL"
            result = helper.adapter.run(parse_request(helper.cfg, "ga4", "traffic_summary", []))
        self.assertEqual(result["rows"], [])
        self.assertNotIn("SYNTHETIC-UNKNOWN-CHANNEL", json.dumps(result))


@unittest.skipUnless((ROOT / "README.md").exists(), "README not mounted in image-only probe")
class Egress(unittest.TestCase):
    def test_readme_lists_all_adapter_api_hosts(self):
        tree = ast.parse((ROOT / "google_reporting/google_adapter.py").read_text(encoding="utf-8"))
        hosts = set()
        for node in ast.walk(tree):
            if isinstance(node, ast.Call):
                if (isinstance(node.func, ast.Attribute) and node.func.attr == "service"
                        and isinstance(node.func.value, ast.Name) and node.func.value.id == "self"):
                    hosts.add(node.args[1].value + ".googleapis.com")
                elif isinstance(node.func, ast.Name) and node.func.id == "discovery_client" and isinstance(node.args[0], ast.Constant):
                    hosts.add(node.args[0].value + ".googleapis.com")
                elif isinstance(node.func, ast.Name) and node.func.id == "BetaAnalyticsDataRestTransport":
                    hosts.update(k.value.value for k in node.keywords if k.arg == "host")
            elif isinstance(node, ast.Assign) and any(isinstance(t, ast.Name) and t.id == "TOKEN_URI" for t in node.targets):
                hosts.add(urlsplit(node.value.value).hostname)
        self.assertTrue(hosts, "adapter host extraction must find APIs")
        readme = (ROOT / "README.md").read_text(encoding="utf-8")
        egress = readme.split("Initiale Ziele:", 1)[1].split("Kein TLS-/Proxy-Bypass.", 1)[0]
        documented = set(re.findall(r"\b([a-z0-9.-]+\.googleapis\.com):443\b", egress))
        self.assertEqual(hosts - documented, set(), "API host missing from README egress list")
