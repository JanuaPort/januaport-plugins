"""Freely named segments per site (JanuaPort/januaport#1379, part B; LEAD c-6883881384).

Synthetic, no network. A site names 1 to 8 segments `[a-z][a-z0-9-]{0,31}`; `all_public` is
reserved and always means every path of the site. `segment` is checked against the segments of
the resolved site: unknown or foreign is invalid_input, never a silent `all_public`.
"""

import json
import re
import unittest
from aiohttp.test_utils import TestClient

from google_reporting.domain import Config, ReportError, Sites, build_query, parse_request, project
from google_reporting.server import create_app
from site_fixtures import JANUA, JANUA_ONLY, TERMIN, januaport_entry, sites_raw, terminagent_entry
from test_boundary import RecordingPool, SafeTestServer
from test_contract import fixture

SHOP = "shop"
KEY = "A" * 32
SITE = "X-Google-Reporting-Site"


def shop_entry():
    """Own host, own pages, own segment names; shares no value with januaport."""
    raw = fixture()
    raw.update(
        name=SHOP,
        gsc_property="sc-domain:shop.test",
        ga4_property="777001",
        hosts=["www.shop.test"],
        segments={"preise": ["/preise"], "start": ["/"]},
        gsc_urls={
            "/preise": ["https://www.shop.test/preise"],
            "/": ["https://www.shop.test/"],
        },
    )
    return raw


def with_segments(segments):
    raw = fixture()
    paths = [p for values in segments.values() if isinstance(values, list) for p in values]
    raw["segments"] = segments
    raw["gsc_urls"] = {p: ["https://www.example.test" + p] for p in paths}
    return raw


def invalid(raw):
    try:
        Config.from_dict(raw)
    except ValueError as exc:
        return str(exc) == "invalid_configuration"
    return False


def named(count):
    return {"s%d" % i: ["/p%d" % i] for i in range(count)}


def code(call):
    with unittest.TestCase().assertRaises(ReportError) as ctx:
        call()
    return ctx.exception.status, ctx.exception.code


class Configuration(unittest.TestCase):
    def test_one_to_eight_segments_load_and_zero_or_nine_do_not(self):
        for count in (1, 2, 8):
            with self.subTest(count=count):
                cfg = Config.from_dict(with_segments(named(count)))
                self.assertEqual(list(cfg.segments), list(named(count)))
        for count in (0, 9, 12):
            with self.subTest(count=count):
                self.assertTrue(invalid(with_segments(named(count))))

    def test_valid_names(self):
        for name in ("a", "atas", "a-b", "a1", "preise-2026", "x" + "0" * 31):
            with self.subTest(name=name):
                cfg = Config.from_dict(with_segments({name: ["/p"]}))
                self.assertEqual(list(cfg.segments), [name])

    def test_invalid_names(self):
        bad = (
            "",
            "A",
            "Atas",
            "1a",
            "-a",
            "a_b",
            "a b",
            "a.b",
            "a/b",
            "x" + "0" * 32,  # 33 characters
            "ü",
            "a\n",
            "all_public",  # reserved, kept fixed for every site
            "all-public-x" + "y" * 40,
            7,
            None,
            ("a",),
        )
        for name in bad:
            with self.subTest(name=name):
                self.assertTrue(invalid(with_segments({name: ["/p"], "ok": ["/q"]})))

    def test_segments_must_be_an_object_of_path_lists(self):
        for value in (["atas"], "atas", None, 7, {"a": "/p"}, {"a": [3]}, {"a": ["/p?x=1"]}):
            with self.subTest(value=value):
                raw = fixture()
                raw["segments"] = value
                self.assertTrue(invalid(raw))

    def test_a_path_in_two_segments_stays_invalid(self):
        raw = with_segments({"a": ["/p"], "b": ["/p"]})
        self.assertTrue(invalid(raw))

    def test_the_old_three_names_still_load_and_are_no_longer_required(self):
        self.assertEqual(list(Config.from_dict(fixture()).segments), ["atas", "gateway", "shared"])
        self.assertEqual(list(Config.from_dict(with_segments({"only": ["/p"]})).segments), ["only"])

    def test_all_paths_of_all_segments_are_still_capped_per_site(self):
        raw = with_segments({"a": ["/p%d" % i for i in range(101)]})
        self.assertTrue(invalid(raw))
        raw = with_segments({"a": ["/p%d" % i for i in range(100)]})
        self.assertEqual(len(Config.from_dict(raw).paths("all_public")), 100)

    def test_gsc_urls_must_still_equal_the_union_of_the_segments(self):
        raw = with_segments({"a": ["/p"], "b": ["/q"]})
        del raw["gsc_urls"]["/q"]
        self.assertTrue(invalid(raw))

    def test_a_site_without_a_segment_named_in_the_list_form_validates_like_a_single_one(self):
        sites = Sites.from_dict(sites_raw(januaport_entry(), shop_entry()))
        self.assertEqual(list(sites.config(JANUA).segments), ["atas", "gateway", "shared"])
        self.assertEqual(list(sites.config(SHOP).segments), ["preise", "start"])


class DuplicateNames(unittest.TestCase):
    """json.loads keeps the last of two equal keys, so the file loader has to refuse them."""

    def text(self, segments_json):
        raw = fixture()
        raw["segments"] = "@@"
        return json.dumps(raw).replace('"@@"', segments_json)

    def test_a_duplicated_segment_name_in_the_file_is_invalid_configuration(self):
        text = self.text('{"a": ["/p"], "a": ["/q"]}')
        with self.assertRaises(ValueError) as ctx:
            Sites.from_json(text)
        self.assertEqual(str(ctx.exception), "invalid_configuration")

    def test_a_duplicated_key_elsewhere_is_refused_too(self):
        text = self.text('{"a": ["/p"]}')[:-1] + ', "ga4_property": "1"}'
        with self.assertRaises(ValueError) as ctx:
            Sites.from_json(text)
        self.assertEqual(str(ctx.exception), "invalid_configuration")

    def test_a_duplicated_key_inside_a_site_entry_is_refused(self):
        text = json.dumps({"sites": [januaport_entry()]})
        text = text.replace('"name": "januaport"', '"name": "januaport", "name": "other"')
        with self.assertRaises(ValueError) as ctx:
            Sites.from_json(text)
        self.assertEqual(str(ctx.exception), "invalid_configuration")

    def test_a_clean_file_loads_in_both_forms(self):
        self.assertFalse(Sites.from_json(json.dumps(fixture())).listed)
        self.assertTrue(Sites.from_json(json.dumps(sites_raw())).listed)

    def test_broken_json_is_invalid_configuration(self):
        for text in ("", "{", "[]", "null", '{"sites": [}'):
            with self.subTest(text=text), self.assertRaises(ValueError) as ctx:
                Sites.from_json(text)
            self.assertEqual(str(ctx.exception), "invalid_configuration")


class Parameter(unittest.TestCase):
    def setUp(self):
        self.sites = Sites.from_dict(sites_raw(januaport_entry(), shop_entry()))
        self.janua, self.shop = self.sites.config(JANUA), self.sites.config(SHOP)

    def parse(self, cfg, pairs, source="gsc", report="page_summary"):
        return parse_request(cfg, source, report, pairs)

    def test_each_site_accepts_its_own_names_and_all_public(self):
        table = (
            (self.janua, "atas", ["/atas"]),
            (self.janua, "gateway", ["/gateway"]),
            (self.janua, "shared", ["/"]),
            (self.janua, "all_public", ["/atas", "/gateway", "/"]),
            (self.shop, "preise", ["/preise"]),
            (self.shop, "start", ["/"]),
            (self.shop, "all_public", ["/preise", "/"]),
        )
        for cfg, name, paths in table:
            for source, report in (("gsc", "page_summary"), ("gsc", "page_compare"), ("ga4", "landing_pages"), ("ga4", "traffic_summary")):
                with self.subTest(site=cfg.site, segment=name, report=report):
                    req = self.parse(cfg, [("segment", name)], source, report)
                    self.assertEqual((req.segment, req.paths), (name, paths))

    def test_without_the_parameter_the_segment_is_all_public(self):
        self.assertEqual(self.parse(self.shop, []).segment, "all_public")
        self.assertEqual(self.parse(self.shop, []).paths, ["/preise", "/"])

    def test_unknown_and_foreign_segments_are_invalid_input_never_all_public(self):
        table = (
            (self.shop, "atas"),  # a segment of another site
            (self.shop, "gateway"),
            (self.shop, "shared"),
            (self.janua, "preise"),
            (self.janua, "start"),
            (self.janua, "nope"),
            (self.shop, "nope"),
            (self.shop, "Preise"),
            (self.shop, "preise "),
            (self.shop, " preise"),
            (self.shop, "all-public"),
            (self.shop, "ALL_PUBLIC"),
            (self.shop, "all_public "),
            (self.shop, ""),
            (self.shop, "preise,start"),
            (self.shop, "x" * 33),
            (self.shop, "ü"),
        )
        for cfg, name in table:
            for source, report in (("gsc", "page_summary"), ("gsc", "page_compare"), ("gsc", "performance_overview"), ("ga4", "landing_pages"), ("ga4", "key_events")):
                with self.subTest(site=cfg.site, segment=name, report=report):
                    self.assertEqual(
                        code(lambda: self.parse(cfg, [("segment", name)], source, report)),
                        (400, "invalid_input"),
                    )

    def test_the_error_carries_no_name(self):
        with self.assertRaises(ReportError) as ctx:
            self.parse(self.shop, [("segment", "geheim-segment")])
        self.assertNotIn("geheim", str(ctx.exception))
        self.assertNotIn("geheim", repr(ctx.exception.__dict__))

    def test_a_page_path_must_lie_inside_the_segment_of_the_site(self):
        self.assertEqual(self.parse(self.shop, [("segment", "preise"), ("page_path", "/preise")]).paths, ["/preise"])
        self.assertEqual(
            code(lambda: self.parse(self.shop, [("segment", "start"), ("page_path", "/preise")])),
            (400, "invalid_input"),
        )

    def test_the_queries_follow_the_segment(self):
        gsc = build_query(self.shop, self.parse(self.shop, [("segment", "preise")]))
        expression = gsc["dimensionFilterGroups"][0]["filters"][0]["expression"]
        self.assertTrue(re.search(expression, "https://www.shop.test/preise"))
        self.assertFalse(re.search(expression, "https://www.shop.test/"))
        ga4 = build_query(self.shop, self.parse(self.shop, [("segment", "start")], "ga4", "landing_pages"))
        values = ga4["dimensionFilter"]["andGroup"]["expressions"][0]["filter"]["inListFilter"]["values"]
        self.assertEqual(values, ["/"])
        self.assertEqual(ga4["property"], "properties/777001")

    def test_the_projection_names_the_segment_of_the_request(self):
        req = self.parse(self.shop, [("segment", "start")])
        out = project(self.shop, req, {"rows": []})
        self.assertEqual((out["scope"]["site"], out["scope"]["segment"]), (SHOP, "start"))
        self.assertEqual(out["scope"]["page_paths"], ["/"])


class Status(unittest.TestCase):
    def test_get_status_names_the_segments_of_the_site_and_nothing_else_about_them(self):
        sites = Sites.from_dict(sites_raw(januaport_entry(), shop_entry(), terminagent_entry()))
        expected = {JANUA: ["atas", "gateway", "shared"], SHOP: ["preise", "start"], TERMIN: ["atas", "gateway", "shared"]}
        for site, names in expected.items():
            cfg = sites.config(site)
            for source in ("gsc", "ga4"):
                with self.subTest(site=site, source=source):
                    req = parse_request(cfg, source, "get_status", [])
                    out = project(cfg, req, {})
                    self.assertEqual(out["segments"], names)
                    text = json.dumps(out)
                    for hidden in ("sc-domain", "123456", "654321", "777001", "/preise", "/atas"):
                        self.assertNotIn(hidden, text)

    def test_the_status_of_a_single_site_names_its_segments(self):
        cfg = Config.from_dict(fixture())
        out = project(cfg, parse_request(cfg, "gsc", "get_status", []), {})
        self.assertEqual(out["segments"], ["atas", "gateway", "shared"])
        self.assertEqual(out["site"], "default")


class Http(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.pool = RecordingPool()
        sites = Sites.from_dict(sites_raw(januaport_entry(), shop_entry()))
        self.client = TestClient(SafeTestServer(create_app(sites, KEY.encode(), self.pool)))
        await self.client.start_server()

    async def asyncTearDown(self):
        await self.client.close()

    async def get(self, path, site):
        return await self.client.get(path, headers={"Authorization": "Bearer " + KEY, SITE: site})

    async def test_unknown_foreign_segment_never_reaches_a_worker_and_is_not_echoed(self):
        for site, segment in ((SHOP, "atas"), (JANUA, "preise"), (SHOP, "geheim-name"), (SHOP, "")):
            for path in ("/v1/gsc/page_summary", "/v1/gsc/page_compare", "/v1/ga4/landing_pages"):
                with self.subTest(site=site, segment=segment, path=path), self.assertLogs("google_reporting", level="INFO") as logs:
                    response = await self.get(path + "?segment=" + segment, site)
                    text = await response.text()
                self.assertEqual((response.status, json.loads(text)), (400, {"error": "invalid_input"}))
                if segment:
                    self.assertNotIn(segment, text)
                    self.assertNotIn(segment, "\n".join(logs.output))
        self.assertEqual(self.pool.calls, [])

    async def test_a_known_segment_reaches_the_pool_with_the_paths_of_its_site(self):
        for path in ("/v1/gsc/page_summary", "/v1/gsc/page_compare", "/v1/ga4/landing_pages"):
            response = await self.get(path + "?segment=preise", SHOP)
            self.assertEqual(response.status, 200)
            call = self.pool.calls[-1]
            self.assertEqual((call.site, call.segment, call.paths), (SHOP, "preise", ["/preise"]))
        response = await self.get("/v1/gsc/page_summary?segment=all_public", SHOP)
        self.assertEqual(self.pool.calls[-1].paths, ["/preise", "/"])

    async def test_januaport_behaves_as_before(self):
        for segment, paths in (("atas", ["/atas"]), ("gateway", ["/gateway"]), ("shared", ["/"])):
            response = await self.get("/v1/gsc/page_summary?segment=" + segment, JANUA)
            self.assertEqual(response.status, 200)
            self.assertEqual(self.pool.calls[-1].paths, paths)
        for value in JANUA_ONLY:
            self.assertNotIn(value, json.dumps(self.pool.calls[-1].paths))


if __name__ == "__main__":
    unittest.main()
