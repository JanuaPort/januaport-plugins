"""Synthetic contract tests for #1130; no production identity or network."""

import datetime as dt
import unittest
from google_reporting.domain import (
    Config,
    ReportError,
    parse_request,
    build_query,
    project,
)


def fixture():
    return dict(
        definition_version="synthetic-v1",
        gsc_property="sc-domain:example.test",
        ga4_property="123456",
        ga4_time_zone="Europe/Berlin",
        hosts=["www.example.test"],
        segments={"atas": ["/atas"], "gateway": ["/gateway"], "shared": ["/"]},
        gsc_urls={
            "/atas": ["https://www.example.test/atas"],
            "/gateway": ["https://www.example.test/gateway"],
            "/": ["https://www.example.test/"],
        },
        query_enabled=False,
        query_min_impressions=10,
        ga4_min_group_size=10,
        key_events=None,
    )


class Contract(unittest.TestCase):
    def setUp(self):
        self.config = Config.from_dict(fixture())

    def test_inclusive_source_timezone_range(self):
        instant = dt.datetime(2026, 3, 30, 0, 30, tzinfo=dt.timezone.utc)
        req = parse_request(self.config, "gsc", "performance_overview", [], instant)
        self.assertEqual((req.start_date, req.end_date), ("2026-02-27", "2026-03-26"))
        req = parse_request(
            self.config, "ga4", "traffic_summary", [("period_days", "90")], instant
        )
        self.assertEqual((req.start_date, req.end_date), ("2025-12-28", "2026-03-27"))

    def test_end_date_after_the_last_final_day_has_its_own_fixed_code(self):
        # The data ends three days before today in the source zone: GSC in
        # America/Los_Angeles, GA4 in the property zone (#1150). A late but well-formed
        # end_date is not malformed input, so the caller learns the reason.
        instant = dt.datetime(2026, 3, 30, 0, 30, tzinfo=dt.timezone.utc)
        for source, report, latest in (
            ("gsc", "performance_overview", "2026-03-26"),
            ("ga4", "traffic_summary", "2026-03-27"),
        ):
            day = dt.date.fromisoformat(latest)
            ok = parse_request(self.config, source, report, [("end_date", latest)], instant)
            self.assertEqual(ok.end_date, latest)
            for late in (day + dt.timedelta(days=1), day + dt.timedelta(days=400)):
                with self.subTest(source=source, end_date=late.isoformat()):
                    with self.assertRaises(ReportError) as ctx:
                        parse_request(
                            self.config,
                            source,
                            report,
                            [("end_date", late.isoformat())],
                            instant,
                        )
                    self.assertEqual(
                        (ctx.exception.status, ctx.exception.code),
                        (400, "end_date_not_available"),
                    )
                    self.assertNotIn(late.isoformat(), str(ctx.exception))

    def test_malformed_end_date_stays_invalid_input(self):
        for value in ("2026-02-30", "yesterday", "2026-3-1", ""):
            with self.subTest(value=value), self.assertRaises(ReportError) as ctx:
                parse_request(self.config, "gsc", "performance_overview", [("end_date", value)])
            self.assertEqual(
                (ctx.exception.status, ctx.exception.code), (400, "invalid_input")
            )

    def test_invalid_input_before_google(self):
        cases = [
            [("property_id", "canary")],
            [("period_days", "29")],
            [("limit", "101")],
            [("segment", "other")],
            [("segment", "atas"), ("page_path", "/gateway")],
            [("page_path", "https://example.test")],
            [("end_date", "2026-02-30")],
            [("limit", "1"), ("limit", "2")],
            [("end_date", "2999-01-01")],
        ]
        for pairs in cases:
            with self.subTest(pairs=pairs), self.assertRaises(ReportError):
                parse_request(self.config, "gsc", "query_summary", pairs)

    def test_fail_closed_config(self):
        for key, value in [
            ("segments", {"atas": ["/private?email=x"], "gateway": [], "shared": []}),
            ("ga4_property", "properties/123"),
            ("ga4_time_zone", "not-a-zone"),
            ("ga4_min_group_size", 0),
            ("query_enabled", "yes"),
            ("unexpected", True),
        ]:
            raw = fixture()
            raw[key] = value
            with self.subTest(key=key), self.assertRaises(ValueError):
                Config.from_dict(raw)
        raw = fixture()
        raw["segments"]["gateway"] = ["/atas"]
        with self.assertRaises(ValueError):
            Config.from_dict(raw)

    def test_fixed_query_and_escaped_allowlist(self):
        req = parse_request(self.config, "gsc", "page_summary", [("segment", "atas")])
        query = build_query(self.config, req)
        self.assertEqual(
            (query["type"], query["dataState"], query["aggregationType"]),
            ("web", "final", "auto"),
        )
        self.assertEqual(query["dimensions"], ["page"])
        import re

        expression = query["dimensionFilterGroups"][0]["filters"][0]["expression"]
        self.assertTrue(re.search(expression, "https://www.example.test/atas"))
        self.assertFalse(
            re.search(expression, "https://www.example.test/atas?person=synthetic")
        )
        self.assertFalse(re.search(expression, "https://wwwXexampleXtest/atas"))
        self.assertIn(
            "^(", query["dimensionFilterGroups"][0]["filters"][0]["expression"]
        )
        req = parse_request(self.config, "ga4", "landing_pages", [])
        query = build_query(self.config, req)
        self.assertEqual(query["dimensions"], [{"name": "landingPage"}])
        self.assertNotIn("landingPagePlusQueryString", str(query))
        self.assertIn("hostName", str(query))
        self.assertTrue(query["returnPropertyQuota"])

    def test_query_disabled_and_key_events_not_configured(self):
        req = parse_request(self.config, "gsc", "query_summary", [])
        with self.assertRaises(ReportError):
            build_query(self.config, req)
        req = parse_request(self.config, "ga4", "key_events", [])
        self.assertEqual(project(self.config, req, {})["state"], "not_configured")

    def test_projection_zero_empty_threshold_and_untrusted_metadata(self):
        req = parse_request(self.config, "ga4", "landing_pages", [])
        raw = {
            "rows": [
                {
                    "dimensionValues": [{"value": "/atas"}],
                    "metricValues": [{"value": "0"}, {"value": "0"}],
                }
            ],
            "rowCount": 2,
            "metadata": {
                "subjectToThresholding": True,
                "dataLossFromOtherRow": True,
                "untrusted": "secret-canary",
            },
        }
        out = project(self.config, req, raw)
        self.assertEqual(out["rows"], [])
        self.assertEqual(out["state"], "partial")
        self.assertNotIn("secret-canary", str(out))
        self.assertEqual(project(self.config, req, {})["state"], "no_rows")
        req = parse_request(self.config, "ga4", "traffic_summary", [])
        raw["rows"][0]["dimensionValues"][0]["value"] = "Direct"
        self.assertEqual(project(self.config, req, raw)["rows"][0]["sessions"], 0)

    def test_gsc_suppression_and_path_allowlist(self):
        cfg = fixture()
        cfg["query_enabled"] = True
        cfg = Config.from_dict(cfg)
        req = parse_request(cfg, "gsc", "query_summary", [])
        raw = {
            "rows": [
                {
                    "keys": ["synthetic query"],
                    "clicks": 1,
                    "impressions": 9,
                    "ctr": 0.1,
                    "position": 2,
                }
            ]
        }
        self.assertEqual(project(cfg, req, raw)["rows"], [])
        raw["rows"][0]["impressions"] = 10
        self.assertEqual(len(project(cfg, req, raw)["rows"]), 1)
        req = parse_request(cfg, "gsc", "page_summary", [])
        raw["rows"][0]["keys"] = ["https://www.example.test/private?canary=1"]
        self.assertEqual(project(cfg, req, raw)["rows"], [])
