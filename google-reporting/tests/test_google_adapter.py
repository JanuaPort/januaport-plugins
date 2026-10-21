"""Real official adapters + HTTP seam interceptors, no Google/network/identity."""

from datetime import datetime, timedelta
import json
import tempfile
import unittest
from unittest.mock import patch
from cryptography.hazmat.primitives.asymmetric import rsa
from cryptography.hazmat.primitives import serialization
import httplib2
import requests
from google_reporting.domain import Config, ReportError, parse_request
from google_reporting.google_adapter import GoogleAdapter, SCOPES, load_identity
from test_contract import fixture


def synthetic_identity():
    # Ephemeral synthetic signing key, never persisted or committed.
    key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    return {
        "type": "service_account",
        "token_uri": "https://oauth2.googleapis.com/token",
        "client_email": "synthetic@synthetic-project.iam.gserviceaccount.com",  # gitleaks:allow synthetic ephemeral test identity
        "private_key": key.private_bytes(
            serialization.Encoding.PEM,
            serialization.PrivateFormat.PKCS8,
            serialization.NoEncryption(),
        ).decode(),
    }


class Adapter(unittest.TestCase):
    def setUp(self):
        self.cfg = Config.from_dict(fixture())
        self.identity = synthetic_identity()
        self.calls = []
        self.status = 200
        self.token_status = 200
        self.body = {
            "rows": [
                {
                    "dimensionValues": [{"value": "Organic Search"}],
                    "metricValues": [{"value": "42"}, {"value": "31"}],
                }
            ],
            "rowCount": 1,
            "metadata": {"timeZone": "Europe/Berlin", "subjectToThresholding": True},
            "propertyQuota": {"tokensPerHour": {"consumed": 1, "remaining": 99}},
        }
        self.adapter = GoogleAdapter(self.cfg, "unused")

    def send(self, session, prepared, **kwargs):
        self.calls.append(
            (
                prepared.url,
                prepared.method,
                prepared.body,
                dict(prepared.headers),
                kwargs,
            )
        )
        response = requests.Response()
        response.request = prepared
        response.headers["Content-Type"] = "application/json"
        if prepared.url == "https://oauth2.googleapis.com/token":
            response.status_code = self.token_status
            data = (
                {
                    "access_token": "synthetic-ephemeral-token",
                    "expires_in": 3600,
                    "token_type": "Bearer",
                }
                if self.token_status == 200
                else {
                    "error": "temporarily_unavailable",
                    "error_description": "SECRET-CANARY",
                }
            )
        else:
            response.status_code = self.status
            data = (
                self.body
                if self.status == 200
                else {"error": {"code": self.status, "message": "SECRET-CANARY"}}
            )
        response._content = json.dumps(data).encode()
        return response

    def httplib(self, http, uri, method="GET", body=None, headers=None, **kwargs):
        self.calls.append((uri, method, body, headers, kwargs))
        if uri == "https://oauth2.googleapis.com/token":
            data = {
                "access_token": "synthetic-ephemeral-token",
                "expires_in": 3600,
                "token_type": "Bearer",
            }
            status = self.token_status
        else:
            status = self.status
            req = parse_request(self.cfg, "gsc", "performance_overview", [])
            data = {
                "rows": [
                    {
                        "keys": [req.end_date],
                        "clicks": 0,
                        "impressions": 12,
                        "ctr": 0,
                        "position": 2,
                    }
                ],
                "responseAggregationType": "byPage",
            }
        if status != 200:
            data = {"error": {"code": status, "message": "SECRET-CANARY"}}
        return httplib2.Response(
            {"status": str(status), "content-type": "application/json"}
        ), json.dumps(data).encode()

    def patches(self):
        outer = self
        return (
            patch(
                "google_reporting.google_adapter.load_identity",
                return_value=self.identity,
            ),
            patch.object(
                requests.Session,
                "send",
                lambda session, p, **kw: outer.send(session, p, **kw),
            ),
            patch.object(
                httplib2.Http,
                "request",
                lambda http, *a, **kw: outer.httplib(http, *a, **kw),
            ),
        )

    def test_real_rest_serialization_projection_cache_and_refresh(self):
        a, b, c = self.patches()
        with a, b, c:
            req = parse_request(self.cfg, "ga4", "traffic_summary", [])
            result = self.adapter.run(req)
            self.assertEqual(
                result["rows"],
                [{"channel": "Organic Search", "sessions": 42, "engaged_sessions": 31}],
            )
            self.assertTrue(result["quality"]["subjectToThresholding"])
            self.assertEqual(
                result["quality"]["quota"]["tokensPerHour"]["remaining"], 99
            )
            self.adapter.run(req)
            self.assertEqual(sum(url.endswith("/token") for url, *_ in self.calls), 1)
            self.adapter.credentials["ga4"].expiry = datetime.now(
                __import__("datetime").timezone.utc
            ).replace(tzinfo=None) - timedelta(seconds=1)
            self.adapter.run(req)
            self.assertEqual(sum(url.endswith("/token") for url, *_ in self.calls), 2)
            api = [call for call in self.calls if "analyticsdata." in call[0]][0]
            self.assertTrue(
                api[0].startswith(
                    "https://analyticsdata.googleapis.com/v1beta/properties/123456:runReport?%24alt="
                )
            )
            self.assertEqual(api[1], "POST")
            body = json.loads(api[2])
            self.assertEqual(
                body["dimensions"], [{"name": "sessionDefaultChannelGroup"}]
            )
            self.assertTrue(body["returnPropertyQuota"])
            self.assertIn("hostName", str(body))
            self.assertEqual(api[4]["timeout"], 18)
            cred = self.adapter.credentials["ga4"]
            self.assertEqual(list(cred.scopes), [SCOPES["ga4"]])
            self.assertFalse(cred._always_use_jwt_access)
            self.assertIsNone(cred._jwt_credentials)

    def test_429_401_and_token_failure_have_no_retries_or_raw_errors(self):
        a, b, c = self.patches()
        with a, b, c:
            req = parse_request(self.cfg, "ga4", "traffic_summary", [])
            for status in (401, 403, 429, 500, 504):
                self.status = status
                self.calls = []
                with self.assertRaises(ReportError) as cm:
                    self.adapter.run(req)
                self.assertNotIn("CANARY", str(cm.exception))
                self.assertEqual(
                    sum("analyticsdata." in url for url, *_ in self.calls), 1
                )
            self.adapter.credentials["ga4"].expiry = datetime.now(
                __import__("datetime").timezone.utc
            ).replace(tzinfo=None) - timedelta(seconds=1)
            self.token_status = 503
            self.calls = []
            with self.assertRaises(ReportError):
                self.adapter.run(req)
            self.assertEqual(len(self.calls), 1)

    def test_actual_gsc_wire_readonly_scope_and_no_retry(self):
        a, b, c = self.patches()
        with a, b, c:
            req = parse_request(self.cfg, "gsc", "performance_overview", [])
            self.assertEqual(self.adapter.run(req)["rows"][0]["clicks"], 0)
            api = [c for c in self.calls if "webmasters" in c[0]][0]
            self.assertEqual(api[1], "POST")
            body = json.loads(api[2])
            self.assertEqual(body["dataState"], "final")
            self.assertEqual(body["aggregationType"], "auto")
            self.assertIn("includingRegex", api[2])
            self.assertEqual(
                list(self.adapter.credentials["gsc"].scopes), [SCOPES["gsc"]]
            )
            self.status = 429
            self.calls = []
            with self.assertRaises(ReportError):
                self.adapter.run(req)
            self.assertEqual(len(self.calls), 1)

    def test_metadata_absence_unknown_timezone_mismatch_and_empty_reason(self):
        a, b, c = self.patches()
        with a, b, c:
            req = parse_request(self.cfg, "ga4", "traffic_summary", [])
            self.body = {}
            out = self.adapter.run(req)
            self.assertNotIn("subjectToThresholding", out["quality"])
            self.assertFalse(out["quality"]["source_time_zone_verified"])
            self.body = {"metadata": {"timeZone": "UTC"}}
            with self.assertRaises(ReportError) as cm:
                self.adapter.run(req)
            self.assertEqual(cm.exception.code, "source_timezone_mismatch")
            self.body = {"metadata": {"emptyReason": "SECRET-CANARY"}}
            out = self.adapter.run(req)
            self.assertTrue(out["quality"]["empty_reason_present"])
            self.assertNotIn("CANARY", str(out))

    def test_invalid_identity_metadata_cannot_select_token_endpoint(self):
        with tempfile.NamedTemporaryFile(mode="w+") as file:
            for field, value in [
                ("token_uri", "http://169.254.169.254/metadata"),
                ("universe_domain", "attacker.test"),
                ("type", "external_account"),
            ]:
                data = dict(self.identity)
                data[field] = value
                file.seek(0)
                json.dump(data, file)
                file.truncate()
                file.flush()
                with self.assertRaises(ValueError):
                    load_identity(file.name)

    def test_adapter_rejects_unknown_report_before_client_construction(self):
        from dataclasses import replace

        req = replace(
            parse_request(self.cfg, "ga4", "traffic_summary", []), report="unknown"
        )
        with self.assertRaises((ReportError, KeyError)):
            self.adapter.run(req)
        self.assertEqual(self.adapter.clients, {})

    def test_key_event_contract_and_compatibility_status_actual_rest(self):
        cfg = fixture()
        cfg["key_events"] = {
            "names": ["synthetic_event"],
            "meaning": "Existing synthetic goal, not qualified lead",
            "valid_from": "2020-01-01",
            "valid_to": "2999-12-31",
            "consent_coverage": "unknown",
        }
        self.cfg = Config.from_dict(cfg)
        self.adapter = GoogleAdapter(self.cfg, "unused")
        a, b, c = self.patches()
        original = self.send

        def send(session, prepared, **kwargs):
            if ":checkCompatibility" in prepared.url:
                self.calls.append(
                    (
                        prepared.url,
                        prepared.method,
                        prepared.body,
                        dict(prepared.headers),
                        kwargs,
                    )
                )
                fields = json.loads(prepared.body)
                data = {
                    "dimensionCompatibilities": [
                        {
                            "dimensionMetadata": {"apiName": d["name"]},
                            "compatibility": "COMPATIBLE",
                        }
                        for d in fields["dimensions"]
                    ],
                    "metricCompatibilities": [
                        {
                            "metricMetadata": {"apiName": m["name"]},
                            "compatibility": "COMPATIBLE",
                        }
                        for m in fields["metrics"]
                    ],
                }
                response = requests.Response()
                response.request = prepared
                response.status_code = 200
                response._content = json.dumps(data).encode()
                return response
            return original(session, prepared, **kwargs)

        with (
            a,
            c,
            patch.object(
                requests.Session,
                "send",
                lambda session, p, **kw: send(session, p, **kw),
            ),
        ):
            out = self.adapter.run(parse_request(self.cfg, "ga4", "get_status", []))
            self.assertEqual(out["state"], "ready")
            self.assertEqual(
                sum(":checkCompatibility" in url for url, *_ in self.calls), 3
            )
            self.calls = []
            self.body = {
                "rows": [
                    {
                        "dimensionValues": [{"value": "synthetic_event"}],
                        "metricValues": [{"value": "10"}],
                    }
                ]
            }
            out = self.adapter.run(parse_request(self.cfg, "ga4", "key_events", []))
            self.assertEqual(
                out["rows"], [{"event_name": "synthetic_event", "key_events": 10.0}]
            )
            self.assertIn("meaning", out["measurement"])
            api = json.loads(self.calls[0][2])
            self.assertIn("synthetic_event", str(api))
            self.assertEqual(api["metrics"], [{"name": "keyEvents"}])

    def test_token_redirects_and_transport_retry_adapters_are_disabled(self):
        from requests.adapters import BaseAdapter

        class Redirect(BaseAdapter):
            def __init__(self):
                self.calls = []

            def close(self):
                pass

            def send(self, request, **kwargs):
                self.calls.append(request.url)
                response = requests.Response()
                response.request = request
                response.status_code = 307
                response.headers["Location"] = "https://unapproved.example.test/collect"
                response._content = b""
                return response

        with patch(
            "google_reporting.google_adapter.load_identity", return_value=self.identity
        ):
            client = self.adapter.client("ga4")
            session = client.transport._session
            self.assertEqual(
                session._auth_request.session.get_adapter("https://").max_retries.total,
                0,
            )
            redirects = Redirect()
            session._auth_request.session.mount("https://", redirects)
            with self.assertRaises(ReportError):
                self.adapter.run(parse_request(self.cfg, "ga4", "traffic_summary", []))
            self.assertEqual(redirects.calls, ["https://oauth2.googleapis.com/token"])

    def test_actual_metadata_sampling_truncation_and_restrictions_are_safe(self):
        a, b, c = self.patches()
        with a, b, c:
            self.body["metadata"].update(
                {
                    "samplingMetadatas": [
                        {"samplesReadCount": "3", "samplingSpaceSize": "10"}
                    ],
                    "dataTruncationReasons": [
                        {"dataTruncationMessage": "SECRET-CANARY"}
                    ],
                    "schemaRestrictionResponse": {
                        "activeMetricRestrictions": [
                            {
                                "metricName": "sessions",
                                "restrictedMetricTypes": ["REVENUE_DATA"],
                            }
                        ]
                    },
                }
            )
            out = self.adapter.run(
                parse_request(self.cfg, "ga4", "traffic_summary", [])
            )
            self.assertTrue(out["quality"]["data_truncation_present"])
            self.assertEqual(out["quality"]["sampling"][0]["samples_read_count"], 3)
            self.assertEqual(
                out["quality"]["metric_restrictions"][0]["types"], ["REVENUE_DATA"]
            )
            self.assertEqual(out["state"], "partial")
            self.assertNotIn("SECRET-CANARY", str(out))


class SlowTokenAdapter(GoogleAdapter):
    """Actual signing/token transport path blocked at its last HTTP boundary."""

    def run(self, req):
        import os
        import time
        from pathlib import Path

        identity = synthetic_identity()
        marker = self.identity_path

        def blocked(session, request, **kwargs):
            if request.url != "https://oauth2.googleapis.com/token":
                raise AssertionError("unexpected auth target")
            Path(marker).write_text(str(os.getpid()))
            time.sleep(10)
            raise AssertionError("cancelled token work survived")

        with (
            patch(
                "google_reporting.google_adapter.load_identity", return_value=identity
            ),
            patch.object(requests.Session, "send", blocked),
        ):
            return super().run(req)


class SlowTokenCancellation(unittest.IsolatedAsyncioTestCase):
    async def test_actual_google_token_work_is_terminated_on_deadline(self):
        import os
        from pathlib import Path
        from google_reporting.workers import WorkerPool

        with tempfile.TemporaryDirectory() as temp:
            cfg = Config.from_dict(fixture())
            pool = WorkerPool(
                cfg, temp + "/pid", budget=5, adapter_factory=SlowTokenAdapter
            )
            try:
                with self.assertRaises(ReportError) as cm:
                    await pool.run(parse_request(cfg, "ga4", "traffic_summary", []))
                self.assertEqual(cm.exception.code, "budget_exceeded")
                pid = int(Path(temp + "/pid").read_text())
                with self.assertRaises(ProcessLookupError):
                    os.kill(pid, 0)
                self.assertEqual(pool.pending, 0)
            finally:
                await pool.close()
