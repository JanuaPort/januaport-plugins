"""page_compare: two equally long periods per approved page (JanuaPort/januaport#1379, part B).

Synthetic, no network. Pure domain checks first, then the pool, the HTTP boundary and the real
REST adapter seam with two sites.
"""

import datetime as dt
import json
import re
import tempfile
import unittest
from unittest.mock import patch
from urllib.parse import unquote

import httplib2
from aiohttp.test_utils import TestClient

from google_reporting.domain import (
    Config,
    ReportError,
    Sites,
    build_query,
    parse_request,
    project,
)
from google_reporting.google_adapter import GoogleAdapter
from google_reporting.server import create_app
from google_reporting.workers import RATE_PER_MINUTE, WorkerPool
from site_fixtures import JANUA, TERMIN, januaport_entry, sites_raw, terminagent_entry
from test_boundary import RecordingPool, SafeTestServer
from test_contract import fixture
from test_google_adapter import synthetic_identity
from test_manage_adapter import Fake

UTC = dt.timezone.utc
# Los Angeles is still 2026-03-29, so the last final day is 2026-03-26.
NOW = dt.datetime(2026, 3, 30, 0, 30, tzinfo=UTC)
KEY = "A" * 32
BASE = "https://www.example.test"


def code(call):
    with unittest.TestCase().assertRaises(ReportError) as ctx:
        call()
    return ctx.exception.status, ctx.exception.code


def gsc_row(url, clicks, impressions, ctr, position):
    return {
        "keys": [url],
        "clicks": clicks,
        "impressions": impressions,
        "ctr": ctr,
        "position": position,
    }


def metrics(clicks, impressions, ctr, position):
    return {"clicks": clicks, "impressions": impressions, "ctr": ctr, "position": position}


NULLS = metrics(None, None, None, None)


class Ranges(unittest.TestCase):
    """Period A = [end - p + 1, end]; B has the same length and lies completely before A."""

    def setUp(self):
        self.cfg = Config.from_dict(fixture())

    def parse(self, pairs):
        return parse_request(self.cfg, "gsc", "page_compare", pairs, NOW)

    def test_without_compare_end_date_b_directly_precedes_a(self):
        table = (
            # period_days, A start, A end, B start, B end
            ("1", "2026-03-26", "2026-03-26", "2026-03-25", "2026-03-25"),
            ("7", "2026-03-20", "2026-03-26", "2026-03-13", "2026-03-19"),
            ("28", "2026-02-27", "2026-03-26", "2026-01-30", "2026-02-26"),
            ("90", "2025-12-27", "2026-03-26", "2025-09-28", "2025-12-26"),
        )
        for days, a_start, a_end, b_start, b_end in table:
            with self.subTest(period_days=days):
                req = self.parse([("period_days", days)])
                self.assertEqual((req.start_date, req.end_date), (a_start, a_end))
                self.assertEqual((req.compare_start_date, req.compare_end_date), (b_start, b_end))
                self.assertEqual(req.report, "page_compare")

    def test_default_period_is_28_days(self):
        req = self.parse([])
        self.assertEqual((req.start_date, req.end_date), ("2026-02-27", "2026-03-26"))
        self.assertEqual((req.compare_start_date, req.compare_end_date), ("2026-01-30", "2026-02-26"))

    def test_explicit_end_date_moves_a_and_the_default_b(self):
        req = self.parse([("period_days", "7"), ("end_date", "2026-03-10")])
        self.assertEqual((req.start_date, req.end_date), ("2026-03-04", "2026-03-10"))
        self.assertEqual((req.compare_start_date, req.compare_end_date), ("2026-02-25", "2026-03-03"))

    def test_compare_end_date_sets_b_with_the_same_length(self):
        table = (
            ("1", "2026-03-10", "2026-03-10", "2026-03-10"),
            ("7", "2026-03-10", "2026-03-04", "2026-03-10"),
            ("90", "2025-12-20", "2025-09-22", "2025-12-20"),
        )
        for days, ce, b_start, b_end in table:
            with self.subTest(period_days=days):
                req = self.parse([("period_days", days), ("compare_end_date", ce)])
                self.assertEqual((req.compare_start_date, req.compare_end_date), (b_start, b_end))
                length = lambda s, e: (dt.date.fromisoformat(e) - dt.date.fromisoformat(s)).days
                self.assertEqual(
                    length(req.start_date, req.end_date),
                    length(req.compare_start_date, req.compare_end_date),
                )

    def test_b_ending_the_day_before_a_is_the_closest_allowed(self):
        req = self.parse([("period_days", "7"), ("compare_end_date", "2026-03-19")])
        self.assertEqual((req.compare_start_date, req.compare_end_date), ("2026-03-13", "2026-03-19"))

    def test_b_that_touches_a_is_invalid_input(self):
        table = (
            ("7", "2026-03-20"),  # B ends on the first day of A
            ("7", "2026-03-26"),  # same period
            ("7", "2026-03-23"),  # inside A
            ("1", "2026-03-26"),
            ("90", "2025-12-27"),
            ("90", "2026-03-10"),  # B of 90 days ending inside A
        )
        for days, ce in table:
            with self.subTest(period_days=days, compare_end_date=ce):
                self.assertEqual(
                    code(lambda: self.parse([("period_days", days), ("compare_end_date", ce)])),
                    (400, "invalid_input"),
                )

    def test_compare_end_date_after_the_last_final_day_is_not_available(self):
        for ce in ("2026-03-27", "2026-04-30", "2999-01-01"):
            with self.subTest(compare_end_date=ce):
                self.assertEqual(
                    code(lambda: self.parse([("compare_end_date", ce)])),
                    (400, "end_date_not_available"),
                )

    def test_malformed_compare_end_date_is_invalid_input(self):
        for ce in ("2026-02-30", "yesterday", "2026-3-1", "", "20260310"):
            with self.subTest(compare_end_date=ce):
                self.assertEqual(
                    code(lambda: self.parse([("compare_end_date", ce)])),
                    (400, "invalid_input"),
                )

    def test_b_before_the_calendar_is_invalid_input_not_a_crash(self):
        self.assertEqual(
            code(lambda: self.parse([("period_days", "90"), ("compare_end_date", "0001-01-05")])),
            (400, "invalid_input"),
        )

    def test_compare_end_date_belongs_to_page_compare_only(self):
        for source, report in (
            ("gsc", "page_summary"),
            ("gsc", "performance_overview"),
            ("gsc", "query_summary"),
            ("ga4", "traffic_summary"),
        ):
            with self.subTest(report=report):
                self.assertEqual(
                    code(
                        lambda: parse_request(
                            self.cfg, source, report, [("compare_end_date", "2026-03-01")], NOW
                        )
                    ),
                    (400, "invalid_input"),
                )

    def test_there_is_no_page_compare_for_ga4(self):
        self.assertEqual(
            code(lambda: parse_request(self.cfg, "ga4", "page_compare", [], NOW)),
            (404, "route_not_found"),
        )

    def test_same_rules_as_page_summary_for_the_other_parameters(self):
        for pairs in (
            [("period_days", "91")],
            [("period_days", "07")],
            [("limit", "0")],
            [("limit", "101")],
            [("limit", "1"), ("limit", "2")],
            [("segment", "other")],
            [("page_path", "https://www.example.test/atas")],
            [("page_path", "/private")],
            [("segment", "atas"), ("page_path", "/gateway")],
            [("end_date", "2999-01-01")],
            [("property_id", "canary")],
            [("compare_end_date", "2026-02-01"), ("compare_end_date", "2026-02-02")],
        ):
            with self.subTest(pairs=pairs):
                status, _ = code(lambda: self.parse(pairs))
                self.assertEqual(status, 400)

    def test_limit_page_path_and_segment_are_taken_over(self):
        req = self.parse([("limit", "7"), ("segment", "atas"), ("page_path", "/atas")])
        self.assertEqual((req.limit, req.segment, req.paths, req.page_path), (7, "atas", ["/atas"], "/atas"))
        default = self.parse([])
        self.assertEqual((default.limit, default.segment), (25, "all_public"))
        self.assertEqual(default.paths, ["/atas", "/gateway", "/"])

    def test_two_source_calls_are_counted(self):
        self.assertEqual(self.parse([]).source_calls, 2)
        self.assertEqual(parse_request(self.cfg, "gsc", "page_summary", [], NOW).source_calls, 1)
        self.assertEqual(parse_request(self.cfg, "ga4", "traffic_summary", [], NOW).source_calls, 1)


class Queries(unittest.TestCase):
    """Golden: the two Search Analytics bodies of page_compare, one per period."""

    def setUp(self):
        self.cfg = Config.from_dict(fixture())
        self.req = parse_request(
            self.cfg, "gsc", "page_compare", [("period_days", "7")], NOW
        )

    @staticmethod
    def body(start, end, expression, row_limit):
        return {
            "startDate": start,
            "endDate": end,
            "type": "web",
            "dataState": "final",
            "aggregationType": "auto",
            "dimensions": ["page"],
            "rowLimit": row_limit,
            "dimensionFilterGroups": [
                {
                    "groupType": "and",
                    "filters": [
                        {
                            "dimension": "page",
                            "operator": "includingRegex",
                            "expression": expression,
                        }
                    ],
                }
            ],
        }

    def test_golden_both_bodies(self):
        expression = (
            r"^(https://www\.example\.test/atas|https://www\.example\.test/gateway"
            r"|https://www\.example\.test/)$"
        )
        # Both bodies ask for every approved URL, so a page never looks "unknown" only because
        # it ranks below the output limit in the other period.
        self.assertEqual(
            build_query(self.cfg, self.req),
            self.body("2026-03-20", "2026-03-26", expression, 3),
        )
        self.assertEqual(
            build_query(self.cfg, self.req, compare=True),
            self.body("2026-03-13", "2026-03-19", expression, 3),
        )

    def test_the_filter_is_the_one_of_page_summary(self):
        summary = parse_request(self.cfg, "gsc", "page_summary", [("period_days", "7")], NOW)
        wanted = build_query(self.cfg, summary)["dimensionFilterGroups"]
        for compare in (False, True):
            self.assertEqual(build_query(self.cfg, self.req, compare=compare)["dimensionFilterGroups"], wanted)

    def test_segment_and_page_path_narrow_both_bodies(self):
        for pairs, urls in (
            ([("segment", "atas")], [BASE + "/atas"]),
            ([("page_path", "/gateway")], [BASE + "/gateway"]),
        ):
            req = parse_request(self.cfg, "gsc", "page_compare", pairs, NOW)
            for compare in (False, True):
                with self.subTest(pairs=pairs, compare=compare):
                    query = build_query(self.cfg, req, compare=compare)
                    expression = query["dimensionFilterGroups"][0]["filters"][0]["expression"]
                    self.assertEqual(query["rowLimit"], len(urls))
                    for url in (BASE + "/atas", BASE + "/gateway", BASE + "/"):
                        self.assertEqual(bool(re.search(expression, url)), url in urls)

    def test_row_limit_counts_every_url_of_every_approved_host(self):
        raw = fixture()
        raw["hosts"] = ["www.example.test", "example.test"]
        raw["gsc_urls"]["/atas"] = [BASE + "/atas", "https://example.test/atas"]
        cfg = Config.from_dict(raw)
        req = parse_request(cfg, "gsc", "page_compare", [], NOW)
        self.assertEqual(build_query(cfg, req)["rowLimit"], 4)

    def test_page_summary_queries_are_untouched(self):
        summary = parse_request(self.cfg, "gsc", "page_summary", [("limit", "5")], NOW)
        query = build_query(self.cfg, summary)
        self.assertEqual((query["rowLimit"], query["dimensions"]), (5, ["page"]))


def compare(cfg=None, pairs=(), raw_a=None, raw_b=None):
    cfg = cfg or Config.from_dict(fixture())
    req = parse_request(cfg, "gsc", "page_compare", list(pairs), NOW)
    raw = {"a": raw_a if raw_a is not None else {"rows": []}, "b": raw_b if raw_b is not None else {"rows": []}}
    return project(cfg, req, raw)


A_ROWS = {
    "rows": [
        gsc_row(BASE + "/gateway", 5, 50, 0.1, 8),
        gsc_row(BASE + "/atas", 10, 100, 0.123, 3.5),
    ],
    "responseAggregationType": "byPage",
}
B_ROWS = {
    "rows": [
        gsc_row(BASE + "/", 2, 30, 0.0666, 3),
        gsc_row(BASE + "/atas", 8, 80, 0.1, 4.0),
    ],
    "responseAggregationType": "byPage",
}


class Projection(unittest.TestCase):
    def setUp(self):
        self.out = compare(raw_a=A_ROWS, raw_b=B_ROWS)
        self.rows = {r["page_path"]: r for r in self.out["rows"]}

    def test_envelope_carries_both_ranges_and_the_scope(self):
        out = self.out
        self.assertEqual(
            (out["schema_version"], out["source"], out["report"]), ("1.0", "gsc", "page_compare")
        )
        self.assertEqual(
            out["range"],
            {"start_date": "2026-02-27", "end_date": "2026-03-26", "time_zone": "America/Los_Angeles"},
        )
        self.assertEqual(
            out["compare_range"],
            {"start_date": "2026-01-30", "end_date": "2026-02-26", "time_zone": "America/Los_Angeles"},
        )
        self.assertEqual(out["scope"]["site"], "default")
        self.assertEqual(out["scope"]["segment"], "all_public")
        self.assertEqual(out["scope"]["page_paths"], ["/atas", "/gateway", "/"])
        self.assertTrue(out["fetched_at"].endswith("Z"))
        self.assertEqual(out["quality"]["coverage"], "unknown")

    def test_page_in_both_periods_has_values_and_delta(self):
        row = self.rows["/atas"]
        self.assertEqual(row["a"], metrics(10, 100, 0.123, 3.5))
        self.assertEqual(row["b"], metrics(8, 80, 0.1, 4.0))
        self.assertEqual(row["delta"]["clicks"], 2)
        self.assertEqual(row["delta"]["impressions"], 20)
        self.assertAlmostEqual(row["delta"]["ctr"], 0.023, places=9)
        self.assertAlmostEqual(row["delta"]["position"], -0.5, places=9)
        self.assertEqual(set(row), {"page_path", "a", "b", "delta"})

    def test_ctr_and_position_come_from_the_source_row_and_are_not_computed(self):
        # 10 clicks of 100 impressions would be 0.1; the source says 0.123 and stays.
        self.assertEqual(self.rows["/atas"]["a"]["ctr"], 0.123)
        self.assertEqual(self.rows["/atas"]["a"]["position"], 3.5)
        self.assertEqual(self.rows["/"]["b"]["ctr"], 0.0666)

    def test_page_only_in_a_has_null_b_and_null_delta_not_zero(self):
        row = self.rows["/gateway"]
        self.assertEqual(row["a"], metrics(5, 50, 0.1, 8))
        self.assertEqual(row["b"], NULLS)
        self.assertEqual(row["delta"], NULLS)
        for value in row["b"].values():
            self.assertIsNone(value)

    def test_page_only_in_b_has_null_a_and_null_delta_not_zero(self):
        row = self.rows["/"]
        self.assertEqual(row["a"], NULLS)
        self.assertEqual(row["b"], metrics(2, 30, 0.0666, 3))
        self.assertEqual(row["delta"], NULLS)

    def test_a_page_without_any_row_is_not_listed(self):
        out = compare(raw_a={"rows": [gsc_row(BASE + "/atas", 1, 10, 0.1, 2)]})
        self.assertEqual([r["page_path"] for r in out["rows"]], ["/atas"])

    def test_missing_rows_are_named_in_the_quality_notes(self):
        notes = self.out["quality"]["notes"]
        self.assertIn("page_without_rows_in_period_a", notes)
        self.assertIn("page_without_rows_in_period_b", notes)
        self.assertEqual(self.out["state"], "partial")
        both = compare(
            raw_a={"rows": [gsc_row(BASE + "/atas", 1, 10, 0.1, 2)]},
            raw_b={"rows": [gsc_row(BASE + "/atas", 1, 10, 0.1, 2)]},
        )
        self.assertNotIn("page_without_rows_in_period_a", both["quality"]["notes"])
        self.assertNotIn("page_without_rows_in_period_b", both["quality"]["notes"])
        self.assertEqual(both["state"], "ok")

    def test_rows_are_sorted_by_clicks_in_a_then_path_and_missing_a_comes_last(self):
        self.assertEqual([r["page_path"] for r in self.out["rows"]], ["/atas", "/gateway", "/"])
        tie_a = {
            "rows": [
                gsc_row(BASE + "/gateway", 5, 50, 0.1, 8),
                gsc_row(BASE + "/", 5, 40, 0.125, 2),
                gsc_row(BASE + "/atas", 9, 90, 0.1, 3),
            ]
        }
        out = compare(raw_a=tie_a)
        self.assertEqual([r["page_path"] for r in out["rows"]], ["/atas", "/", "/gateway"])
        only_b = compare(
            raw_b={"rows": [gsc_row(BASE + "/gateway", 4, 40, 0.1, 5), gsc_row(BASE + "/", 9, 90, 0.1, 5)]}
        )
        self.assertEqual([r["page_path"] for r in only_b["rows"]], ["/", "/gateway"])

    def test_limit_cuts_the_sorted_rows_and_marks_the_state(self):
        out = compare(pairs=[("limit", "2")], raw_a=A_ROWS, raw_b=B_ROWS)
        self.assertEqual([r["page_path"] for r in out["rows"]], ["/atas", "/gateway"])
        self.assertTrue(out["quality"]["limit_reached"])
        self.assertEqual(out["state"], "partial")
        self.assertEqual(out["quality"]["returned_rows"], 2)
        whole = compare(pairs=[("limit", "3")], raw_a=A_ROWS, raw_b=B_ROWS)
        self.assertFalse(whole["quality"]["limit_reached"])
        self.assertIn("bounded_top_rows_not_total", whole["quality"]["notes"])

    def test_nothing_in_both_periods_is_no_rows(self):
        out = compare()
        self.assertEqual((out["state"], out["rows"]), ("no_rows", []))

    def test_zero_values_stay_zero(self):
        out = compare(
            raw_a={"rows": [gsc_row(BASE + "/atas", 0, 12, 0, 6)]},
            raw_b={"rows": [gsc_row(BASE + "/atas", 3, 12, 0.25, 6)]},
        )
        row = out["rows"][0]
        self.assertEqual(row["a"]["clicks"], 0)
        self.assertEqual(row["delta"]["clicks"], -3)
        self.assertEqual(row["delta"]["position"], 0)

    def test_urls_outside_the_allowlist_are_suppressed(self):
        out = compare(
            raw_a={
                "rows": [
                    gsc_row(BASE + "/atas", 1, 10, 0.1, 2),
                    gsc_row(BASE + "/private?email=x", 9, 90, 0.1, 2),
                ]
            }
        )
        self.assertEqual([r["page_path"] for r in out["rows"]], ["/atas"])
        self.assertIn("small_or_unapproved_groups_suppressed", out["quality"]["notes"])
        self.assertNotIn("private", json.dumps(out))

    def test_page_path_and_segment_narrow_the_rows_to_the_scope(self):
        out = compare(
            pairs=[("segment", "atas")],
            raw_a={"rows": [gsc_row(BASE + "/atas", 1, 10, 0.1, 2), gsc_row(BASE + "/gateway", 1, 10, 0.1, 2)]},
        )
        self.assertEqual([r["page_path"] for r in out["rows"]], ["/atas"])
        self.assertEqual(out["scope"]["page_paths"], ["/atas"])

    def test_two_rows_of_one_path_in_a_period_are_not_averaged(self):
        raw = fixture()
        raw["hosts"] = ["www.example.test", "example.test"]
        raw["gsc_urls"]["/atas"] = [BASE + "/atas", "https://example.test/atas"]
        cfg = Config.from_dict(raw)
        out = compare(
            cfg,
            raw_a={
                "rows": [
                    gsc_row(BASE + "/atas", 6, 60, 0.1, 2),
                    gsc_row("https://example.test/atas", 4, 40, 0.1, 6),
                ]
            },
            raw_b={"rows": [gsc_row(BASE + "/atas", 5, 50, 0.1, 3)]},
        )
        row = out["rows"][0]
        self.assertEqual(row["a"], NULLS)
        self.assertEqual(row["delta"], NULLS)
        self.assertEqual(row["b"], metrics(5, 50, 0.1, 3))
        self.assertIn("page_rows_ambiguous", out["quality"]["notes"])
        self.assertEqual(out["state"], "partial")

    def test_invalid_source_answers_fail_closed(self):
        broken = (
            {"rows": [gsc_row(BASE + "/atas", 1, 10, 1.5, 2)]},  # CTR above 1
            {"rows": [{"keys": [BASE + "/atas"], "clicks": 1}]},  # metrics missing
            {"rows": [gsc_row(BASE + "/atas", -1, 10, 0.1, 2)]},  # negative
            {"rows": [gsc_row(BASE + "/atas", True, 10, 0.1, 2)]},  # bool
            {"rows": [{"keys": [], "clicks": 1, "impressions": 1, "ctr": 0, "position": 1}]},
            {"rows": "nope"},
        )
        for raw in broken:
            for side in ("raw_a", "raw_b"):
                with self.subTest(side=side, raw=str(raw)[:40]):
                    self.assertEqual(
                        code(lambda: compare(**{side: raw})), (502, "invalid_source_response")
                    )

    def test_the_answer_has_only_declared_fields(self):
        raw_a = {"rows": [dict(gsc_row(BASE + "/atas", 1, 10, 0.1, 2), canary="SECRET-CANARY")]}
        out = compare(raw_a=raw_a)
        self.assertNotIn("CANARY", json.dumps(out))


class PoolCost(unittest.IsolatedAsyncioTestCase):
    """Both Google calls count against the 30 per minute of the source."""

    async def asyncSetUp(self):
        self.cfg = Config.from_dict(fixture())
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.pool = WorkerPool(
            Sites.single(self.cfg), self.temp.name + "/x", adapter_factory=EchoAdapter
        )

    async def asyncTearDown(self):
        await self.pool.close()

    async def test_a_comparison_counts_twice_and_the_16th_is_limited(self):
        self.assertEqual(RATE_PER_MINUTE, 30)
        req = parse_request(self.cfg, "gsc", "page_compare", [], NOW)
        for _ in range(RATE_PER_MINUTE // 2):
            await self.pool.run(req)
        with self.assertRaises(ReportError) as ctx:
            await self.pool.run(req)
        self.assertEqual((ctx.exception.status, ctx.exception.code), (429, "source_rate_limited"))

    async def test_a_comparison_does_not_fit_into_a_single_free_slot(self):
        single = parse_request(self.cfg, "gsc", "page_summary", [], NOW)
        for _ in range(RATE_PER_MINUTE - 1):
            await self.pool.run(single)
        two = parse_request(self.cfg, "gsc", "page_compare", [], NOW)
        with self.assertRaises(ReportError) as ctx:
            await self.pool.run(two)
        self.assertEqual(ctx.exception.code, "source_rate_limited")
        # The refused call did not take the free slot.
        await self.pool.run(single)

    async def test_the_other_source_has_its_own_budget(self):
        two = parse_request(self.cfg, "gsc", "page_compare", [], NOW)
        for _ in range(RATE_PER_MINUTE // 2):
            await self.pool.run(two)
        ga4 = parse_request(self.cfg, "ga4", "traffic_summary", [], NOW)
        await self.pool.run(ga4)


class EchoAdapter:
    def __init__(self, sites, path):
        pass

    def run(self, req):
        return {"report": req.report}


class Http(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.pool = RecordingPool()
        app = create_app(
            Sites.from_dict(sites_raw(januaport_entry(), terminagent_entry())),
            KEY.encode(),
            self.pool,
        )
        self.client = TestClient(SafeTestServer(app))
        await self.client.start_server()

    async def asyncTearDown(self):
        await self.client.close()

    async def get(self, path, site=JANUA):
        headers = {"Authorization": "Bearer " + KEY}
        if site:
            headers["X-Google-Reporting-Site"] = site
        return await self.client.get(path, headers=headers)

    async def test_route_serves_gsc_only(self):
        ok = await self.get("/v1/gsc/page_compare?period_days=7")
        self.assertEqual(ok.status, 200)
        self.assertEqual(self.pool.calls[-1].report, "page_compare")
        gone = await self.get("/v1/ga4/page_compare")
        self.assertEqual((gone.status, await gone.json()), (404, {"error": "route_not_found"}))
        self.assertEqual(len(self.pool.calls), 1)

    async def test_invalid_input_never_reaches_the_worker(self):
        for query, error in (
            ("period_days=91", "invalid_input"),
            ("period_days=0", "invalid_input"),
            ("period_days=07", "invalid_input"),
            ("period_days=7&compare_end_date=2999-01-01", "end_date_not_available"),
            ("period_days=7&end_date=2026-03-10&compare_end_date=2026-03-10", "invalid_input"),
            ("compare_end_date=nope", "invalid_input"),
            ("segment=nope", "invalid_input"),
            ("page_path=/preise", "invalid_input"),  # a page of the other site
            ("limit=101", "invalid_input"),
        ):
            with self.subTest(query=query):
                response = await self.get("/v1/gsc/page_compare?" + query)
                self.assertEqual((response.status, await response.json()), (400, {"error": error}))
        self.assertEqual(self.pool.calls, [])

    async def test_the_second_site_sees_only_its_own_pages(self):
        ok = await self.get("/v1/gsc/page_compare?page_path=/preise", site=TERMIN)
        self.assertEqual(ok.status, 200)
        self.assertEqual((self.pool.calls[-1].site, self.pool.calls[-1].paths), (TERMIN, ["/preise"]))
        foreign = await self.get("/v1/gsc/page_compare?page_path=/atas", site=TERMIN)
        self.assertEqual(foreign.status, 400)
        whole = await self.get("/v1/gsc/page_compare", site=TERMIN)
        self.assertEqual(whole.status, 200)
        self.assertEqual(self.pool.calls[-1].paths, ["/", "/preise"])

    async def test_site_header_is_still_required_in_the_list_form(self):
        response = await self.get("/v1/gsc/page_compare", site=None)
        self.assertEqual((response.status, await response.json()), (400, {"error": "site_required"}))
        self.assertEqual(self.pool.calls, [])


class CompareFake(Fake):
    """Answers each Search Analytics call by its start date; everything else like Fake."""

    def __init__(self, by_start, status=None):
        super().__init__()
        self.by_start = by_start
        self.status = status or {}

    def request(self, http, uri, method="GET", body=None, headers=None, **kwargs):
        if "searchAnalytics/query" not in unquote(uri):
            return super().request(http, uri, method, body, headers, **kwargs)
        parsed = json.loads(body)
        self.calls.append({"method": method, "uri": unquote(uri), "raw": uri, "body": parsed, "scope": None})
        status = self.status.get(parsed["startDate"], 200)
        data = (
            self.by_start[parsed["startDate"]]
            if status == 200
            else {"error": {"code": status, "message": "SECRET-CANARY"}}
        )
        return (
            httplib2.Response({"status": str(status), "content-type": "application/json"}),
            json.dumps(data).encode(),
        )


class AdapterCalls(unittest.TestCase):
    def setUp(self):
        self.sites = Sites.from_dict(sites_raw())
        for p in (
            patch("google_reporting.google_adapter.load_identity", return_value=synthetic_identity()),
        ):
            p.start()
            self.addCleanup(p.stop)

    def run_adapter(self, google, site, pairs=()):
        cfg = self.sites.config(site)
        req = parse_request(cfg, "gsc", "page_compare", list(pairs), NOW)
        with patch.object(httplib2.Http, "request", lambda http, *a, **kw: google.request(http, *a, **kw)):
            return GoogleAdapter(self.sites, "unused").run(req), req

    def test_two_searchanalytics_calls_one_per_period_with_the_page_filter(self):
        google = CompareFake(
            {
                "2026-03-20": {"rows": [gsc_row(BASE + "/atas", 10, 100, 0.1, 3)]},
                "2026-03-13": {"rows": [gsc_row(BASE + "/atas", 4, 80, 0.05, 5)]},
            }
        )
        out, _ = self.run_adapter(google, JANUA, [("period_days", "7")])
        calls = [c for c in google.calls if "searchAnalytics/query" in c["uri"]]
        self.assertEqual(len(calls), 2)
        self.assertEqual(
            [(c["body"]["startDate"], c["body"]["endDate"]) for c in calls],
            [("2026-03-20", "2026-03-26"), ("2026-03-13", "2026-03-19")],
        )
        for call in calls:
            self.assertEqual(call["method"], "POST")
            self.assertIn("sites/sc-domain:example.test/searchAnalytics/query", call["uri"])
            self.assertEqual(call["body"]["dimensions"], ["page"])
            self.assertEqual(call["body"]["dataState"], "final")
        row = out["rows"][0]
        self.assertEqual(row["a"]["clicks"], 10)
        self.assertEqual(row["b"]["clicks"], 4)
        self.assertEqual(row["delta"]["clicks"], 6)

    def test_the_second_site_uses_its_own_property_and_pages_for_both_calls(self):
        google = CompareFake(
            {
                "2026-02-27": {"rows": [gsc_row("https://www.termin.test/preise", 3, 30, 0.1, 4)]},
                "2026-01-30": {"rows": []},
            }
        )
        out, _ = self.run_adapter(google, TERMIN)
        calls = [c for c in google.calls if "searchAnalytics/query" in c["uri"]]
        self.assertEqual(len(calls), 2)
        for call in calls:
            text = call["uri"] + json.dumps(call["body"])
            self.assertIn("sc-domain:termin.test", text)
            for foreign in ("example.test", "/atas", "/gateway", "123456"):
                self.assertNotIn(foreign, text)
        self.assertEqual(out["scope"]["site"], TERMIN)
        self.assertEqual(out["rows"][0]["page_path"], "/preise")
        self.assertEqual(out["rows"][0]["b"], NULLS)

    def test_an_error_of_either_call_is_one_fixed_code_without_raw_text(self):
        for failing, status, expected in (
            ("2026-02-27", 500, "source_unavailable"),
            ("2026-01-30", 500, "source_unavailable"),
            ("2026-01-30", 429, "source_quota_limited"),
            ("2026-02-27", 403, "source_access_denied"),
        ):
            with self.subTest(failing=failing, status=status):
                google = CompareFake({"2026-02-27": {"rows": []}, "2026-01-30": {"rows": []}}, {failing: status})
                with self.assertRaises(ReportError) as ctx:
                    self.run_adapter(google, JANUA)
                self.assertEqual(ctx.exception.code, expected)
                self.assertNotIn("CANARY", str(ctx.exception))

    def test_the_status_query_of_page_summary_is_unchanged_by_the_new_route(self):
        google = CompareFake({"2026-02-27": {"rows": []}})
        cfg = self.sites.config(JANUA)
        req = parse_request(cfg, "gsc", "page_summary", [], NOW)
        with patch.object(httplib2.Http, "request", lambda http, *a, **kw: google.request(http, *a, **kw)):
            GoogleAdapter(self.sites, "unused").run(req)
        self.assertEqual(len([c for c in google.calls if "searchAnalytics/query" in c["uri"]]), 1)


class WireProbe(unittest.TestCase):
    """The synthetic adapter of tests/wire_server.py (used by the cross-repo test) knows the tool."""

    def test_the_wire_adapter_answers_a_comparison_with_a_nonzero_delta(self):
        from wire_server import SyntheticAdapter

        sites = Sites.from_dict(sites_raw())
        for site in (JANUA, TERMIN):
            with self.subTest(site=site):
                cfg = sites.config(site)
                req = parse_request(cfg, "gsc", "page_compare", [], NOW)
                out = SyntheticAdapter(sites, "unused").run(req)
                self.assertEqual((out["report"], out["scope"]["site"]), ("page_compare", site))
                self.assertEqual(out["rows"][0]["delta"]["clicks"], 1)
                self.assertEqual(out["compare_range"]["end_date"], "2026-02-26")


if __name__ == "__main__":
    unittest.main()
