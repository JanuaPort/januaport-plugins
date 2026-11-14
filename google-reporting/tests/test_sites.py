"""Several sites in one service (JanuaPort/januaport#1379, part A); synthetic, no network.

Configuration, site resolution and the separation of two sites at the pure level.
"""

import json
import unittest

from google_reporting.domain import (
    Config,
    ReportError,
    Sites,
    build_query,
    parse_request,
    project,
)
from google_reporting.manage import ManageRequest, parse_get, parse_post
from site_fixtures import JANUA, JANUA_ONLY, TERMIN, januaport_entry, sites_raw, terminagent_entry
from test_contract import fixture


def invalid(raw):
    try:
        Sites.from_dict(raw)
    except ValueError as exc:
        return str(exc) == "invalid_configuration"
    return False


def many_paths(host, n):
    paths = ["/p%d" % i for i in range(n)]
    third = n // 3
    return dict(
        segments={
            "atas": paths[:third],
            "gateway": paths[third : 2 * third],
            "shared": paths[2 * third :],
        },
        gsc_urls={p: ["https://%s%s" % (host, p)] for p in paths},
    )


class Configuration(unittest.TestCase):
    def test_single_form_is_the_site_default_and_stays_readable(self):
        sites = Sites.from_dict(fixture())
        self.assertFalse(sites.listed)
        self.assertEqual(list(sites.by_name), ["default"])
        self.assertEqual(sites.by_name["default"].site, "default")
        self.assertEqual(Config.from_dict(fixture()).site, "default")

    def test_list_form_keeps_the_names_in_order(self):
        sites = Sites.from_dict(sites_raw())
        self.assertTrue(sites.listed)
        self.assertEqual(list(sites.by_name), [JANUA, TERMIN])
        self.assertEqual([c.site for c in sites.by_name.values()], [JANUA, TERMIN])
        self.assertEqual(sites.by_name[TERMIN].ga4_property, "654321")

    def test_list_with_one_entry_is_still_the_list_form(self):
        sites = Sites.from_dict(sites_raw(januaport_entry()))
        self.assertTrue(sites.listed)
        self.assertEqual(list(sites.by_name), [JANUA])

    def test_single_helper_wraps_one_config(self):
        sites = Sites.single(Config.from_dict(fixture()))
        self.assertFalse(sites.listed)
        self.assertEqual(sites.config("default").hosts, ["www.example.test"])

    def numbered(self, i):
        entry = januaport_entry()
        entry.update(
            name="site%d" % i,
            hosts=["www.s%d.test" % i],
            gsc_property="sc-domain:s%d.test" % i,
            ga4_property=str(1000 + i),
            segments={"atas": [], "gateway": [], "shared": ["/"]},
            gsc_urls={"/": ["https://www.s%d.test/" % i]},
        )
        return entry

    def test_one_to_five_sites_load_and_zero_or_six_do_not(self):
        for n in (1, 2, 5):
            with self.subTest(n=n):
                raw = sites_raw(*[self.numbered(i) for i in range(n)])
                self.assertEqual(len(Sites.from_dict(raw).by_name), n)
        self.assertTrue(invalid({"sites": []}))
        self.assertTrue(invalid(sites_raw(*[self.numbered(i) for i in range(6)])))

    def test_invalid_shapes(self):
        base = januaport_entry()
        cases = {
            "sites is not a list": {"sites": {"januaport": base}},
            "sites is a string": {"sites": "januaport"},
            "entry is not an object": {"sites": ["januaport"]},
            "entry without name": {"sites": [{k: v for k, v in base.items() if k != "name"}]},
            "entry with its own site key": {"sites": [dict(base, site="januaport")]},
            "entry with an unknown key": {"sites": [dict(base, unexpected=True)]},
            "mixed form: single keys beside sites": dict(fixture(), sites=[base]),
            "mixed form: one extra key beside sites": dict(sites_raw(), hosts=["www.example.test"]),
            "single form with a name": dict(fixture(), name="januaport"),
            "single form with a site key": dict(fixture(), site="default"),
            "entry broken by the existing rules": {"sites": [dict(base, ga4_property="properties/123")]},
            "second entry broken by the existing rules": {
                "sites": [base, dict(terminagent_entry(), ga4_time_zone="nope")]
            },
        }
        for label, raw in cases.items():
            with self.subTest(label):
                self.assertTrue(invalid(raw))

    def test_names(self):
        for name in ("a", "terminagent", "a-b", "a1", "x" + "0" * 31):
            with self.subTest(valid=name):
                entry = dict(januaport_entry(), name=name)
                self.assertEqual(list(Sites.from_dict({"sites": [entry]}).by_name), [name])
        bad = ("", "A", "Januaport", "1a", "-a", "a_b", "a b", "a.b", "x" * 33, "ü", "default", None, 7, ["a"])
        for name in bad:
            with self.subTest(invalid=name):
                self.assertTrue(invalid({"sites": [dict(januaport_entry(), name=name)]}))

    def test_default_is_forbidden_in_the_list_form(self):
        self.assertTrue(invalid({"sites": [dict(januaport_entry(), name="default")]}))
        self.assertTrue(invalid(sites_raw(dict(januaport_entry(), name="default"), terminagent_entry())))

    def test_duplicate_name(self):
        other = dict(terminagent_entry(), name=JANUA)
        self.assertTrue(invalid(sites_raw(januaport_entry(), other)))

    def test_hosts_must_be_disjoint(self):
        # A second site that really shares the host of the first.
        shared = dict(
            januaport_entry(),
            name="shop",
            gsc_property="sc-domain:www.example.test",
            ga4_property="777777",
        )
        self.assertTrue(invalid(sites_raw(januaport_entry(), shared)))
        # Own host plus one host of the other site.
        other = dict(
            terminagent_entry(),
            hosts=["www.termin.test", "www.example.test"],
            gsc_property="sc-domain:test",
            gsc_urls={
                "/": ["https://www.example.test/", "https://www.termin.test/"],
                "/preise": ["https://www.termin.test/preise"],
            },
        )
        self.assertTrue(invalid(sites_raw(januaport_entry(), other)))

    def test_properties_are_unique_per_site(self):
        # Disjoint hosts, so only the property rule can reject these.
        same_ga4 = dict(terminagent_entry(), ga4_property="123456")
        self.assertTrue(invalid(sites_raw(januaport_entry(), same_ga4)))
        shop = dict(
            terminagent_entry(),
            hosts=["shop.example.test"],
            gsc_property="sc-domain:example.test",
            gsc_urls={
                "/": ["https://shop.example.test/"],
                "/preise": ["https://shop.example.test/preise"],
            },
        )
        self.assertTrue(invalid(sites_raw(januaport_entry(), shop)))
        # The same entry with a free GSC property is fine: the rule is the property.
        free = dict(shop, gsc_property="https://shop.example.test/")
        self.assertEqual(len(Sites.from_dict(sites_raw(januaport_entry(), free)).by_name), 2)

    def test_path_and_regex_caps_apply_per_site(self):
        hundred = dict(terminagent_entry(), **many_paths("www.termin.test", 99))
        full = dict(januaport_entry(), **many_paths("www.example.test", 99))
        self.assertEqual(len(Sites.from_dict(sites_raw(full, hundred)).by_name), 2)
        over = dict(terminagent_entry(), **many_paths("www.termin.test", 102))
        self.assertTrue(invalid(sites_raw(januaport_entry(), over)))
        long_paths = ["/integrationen/%s-%s" % ("x" * 90, i) for i in range(60)]
        third = len(long_paths) // 3
        long_entry = dict(
            terminagent_entry(),
            segments={
                "atas": long_paths[:third],
                "gateway": long_paths[third : 2 * third],
                "shared": long_paths[2 * third :],
            },
            gsc_urls={p: ["https://www.termin.test" + p] for p in long_paths},
        )
        self.assertTrue(invalid(sites_raw(januaport_entry(), long_entry)))

    def test_each_site_validates_with_its_own_values(self):
        termin = dict(terminagent_entry(), query_enabled=True)
        sites = Sites.from_dict(sites_raw(januaport_entry(), termin))
        self.assertFalse(sites.config(JANUA).query_enabled)
        self.assertTrue(sites.config(TERMIN).query_enabled)


class Resolution(unittest.TestCase):
    """The header names the site; nothing else does. No fallback to a first site, no echo."""

    def forms(self):
        return {
            "single": Sites.from_dict(fixture()),
            "list-1": Sites.from_dict(sites_raw(januaport_entry())),
            "list-2": Sites.from_dict(sites_raw()),
        }

    def outcome(self, sites, values):
        try:
            return sites.resolve(values).site
        except ReportError as exc:
            self.assertEqual(exc.status, 400)
            for value in values:
                if value:
                    self.assertNotIn(value, exc.code)
                    self.assertNotIn(value, str(exc))
            return exc.code

    def test_table(self):
        required, unknown = "site_required", "site_unknown"
        table = [
            # label, header values, single, list-1, list-2
            ("missing", [], "default", required, required),
            ("empty", [""], unknown, unknown, unknown),
            ("twice, same", [JANUA, JANUA], unknown, unknown, unknown),
            ("twice, different", [JANUA, TERMIN], unknown, unknown, unknown),
            ("unknown name", ["nope"], unknown, unknown, unknown),
            ("capital letter", ["Terminagent"], unknown, unknown, unknown),
            ("capital januaport", ["Januaport"], unknown, unknown, unknown),
            ("leading blank", [" terminagent"], unknown, unknown, unknown),
            ("trailing blank", ["terminagent "], unknown, unknown, unknown),
            ("inner blank", ["termin agent"], unknown, unknown, unknown),
            ("trailing newline", ["terminagent\n"], unknown, unknown, unknown),
            ("tab", ["terminagent\t"], unknown, unknown, unknown),
            ("non-ascii", ["terminägent"], unknown, unknown, unknown),
            ("too long", ["a" * 33], unknown, unknown, unknown),
            ("default", ["default"], "default", unknown, unknown),
            ("januaport", [JANUA], unknown, JANUA, JANUA),
            ("terminagent", [TERMIN], unknown, unknown, TERMIN),
        ]
        forms = self.forms()
        for label, values, *expected in table:
            for (form, sites), want in zip(forms.items(), expected):
                with self.subTest(label=label, form=form):
                    self.assertEqual(self.outcome(sites, values), want)

    def test_a_resolved_site_is_its_own_config_never_a_neighbour(self):
        sites = Sites.from_dict(sites_raw())
        self.assertIs(sites.resolve([JANUA]), sites.config(JANUA))
        self.assertIs(sites.resolve([TERMIN]), sites.config(TERMIN))
        self.assertEqual(sites.resolve([TERMIN]).hosts, ["www.termin.test"])

    def test_unknown_config_name_is_a_fixed_configuration_error(self):
        with self.assertRaises(ReportError) as ctx:
            Sites.from_dict(sites_raw()).config("missing")
        self.assertEqual((ctx.exception.status, ctx.exception.code), (503, "configuration_invalid"))


class Separation(unittest.TestCase):
    """One request touches property, hosts and paths of exactly one site."""

    def setUp(self):
        sites = Sites.from_dict(sites_raw())
        self.janua, self.termin = sites.config(JANUA), sites.config(TERMIN)

    def test_requests_carry_their_site(self):
        for cfg in (self.janua, self.termin):
            for source, report in (("gsc", "page_summary"), ("ga4", "traffic_summary"), ("gsc", "get_status")):
                with self.subTest(site=cfg.site, report=report):
                    self.assertEqual(parse_request(cfg, source, report, []).site, cfg.site)
        self.assertEqual(parse_get(self.termin, "gsc", "list_sitemaps", []).site, TERMIN)
        body = json.dumps({"date": "2026-11-14", "title": "t"}).encode()
        post = parse_post(self.termin, "ga4", "create_annotation", body)
        self.assertEqual((post.site, post.write), (TERMIN, True))

    def test_report_queries_of_terminagent_contain_nothing_of_januaport(self):
        for source, report in (
            ("gsc", "page_summary"),
            ("gsc", "performance_overview"),
            ("gsc", "get_status"),
            ("ga4", "traffic_summary"),
            ("ga4", "landing_pages"),
            ("ga4", "get_status"),
        ):
            query = build_query(self.termin, parse_request(self.termin, source, report, []))
            text = json.dumps(query)
            with self.subTest(report=report):
                for foreign in JANUA_ONLY:
                    self.assertNotIn(foreign, text)
        gsc = build_query(self.termin, parse_request(self.termin, "gsc", "page_summary", []))
        self.assertIn("www.termin.test", json.dumps(gsc))
        ga4 = build_query(self.termin, parse_request(self.termin, "ga4", "traffic_summary", []))
        self.assertEqual(ga4["property"], "properties/654321")

    def test_a_page_of_the_other_site_is_invalid_input_for_every_tool_with_a_page_or_url(self):
        def code(call):
            with self.assertRaises(ReportError) as ctx:
                call()
            return (ctx.exception.status, ctx.exception.code)

        foreign = "https://www.example.test/sitemap.xml"
        cases = {
            "page_summary page_path": lambda: parse_request(
                self.termin, "gsc", "page_summary", [("page_path", "/atas")]
            ),
            "performance_overview page_path": lambda: parse_request(
                self.termin, "gsc", "performance_overview", [("page_path", "/gateway")]
            ),
            "segment without pages": lambda: parse_request(
                self.termin, "gsc", "page_summary", [("segment", "atas")]
            ),
            "inspect_url page_path": lambda: parse_get(
                self.termin, "gsc", "inspect_url", [("page_path", "/atas")]
            ),
            "get_sitemap host": lambda: parse_get(
                self.termin, "gsc", "get_sitemap", [("sitemap_url", foreign)]
            ),
            "submit_sitemap host": lambda: parse_post(
                self.termin, "gsc", "submit_sitemap", json.dumps({"sitemap_url": foreign}).encode()
            ),
            "submit_sitemap host in dry run": lambda: parse_post(
                self.termin,
                "gsc",
                "submit_sitemap",
                json.dumps({"sitemap_url": foreign, "dry_run": True}).encode(),
            ),
        }
        for label, call in cases.items():
            with self.subTest(label):
                self.assertEqual(code(call), (400, "invalid_input"))
        # The reverse: the own host and the own page work.
        inspect = parse_get(self.termin, "gsc", "inspect_url", [("page_path", "/preise")])
        self.assertEqual(inspect.params["url"], "https://www.termin.test/preise")
        own = "https://www.termin.test/sitemap.xml"
        parse_get(self.termin, "gsc", "get_sitemap", [("sitemap_url", own)])
        parse_post(self.termin, "gsc", "submit_sitemap", json.dumps({"sitemap_url": own}).encode())
        # januaport does not accept the page of terminagent either.
        with self.assertRaises(ReportError):
            parse_get(self.janua, "gsc", "inspect_url", [("page_path", "/preise")])

    def test_site_is_never_a_parameter(self):
        pairs = [("site", TERMIN)]
        for source, report in (("gsc", "page_summary"), ("ga4", "traffic_summary"), ("gsc", "get_status")):
            with self.subTest(report=report), self.assertRaises(ReportError) as ctx:
                parse_request(self.janua, source, report, pairs)
            self.assertEqual(ctx.exception.code, "invalid_input")
        with self.assertRaises(ReportError):
            parse_get(self.janua, "gsc", "list_sitemaps", pairs)
        body = json.dumps({"date": "2026-11-14", "title": "t", "site": TERMIN}).encode()
        with self.assertRaises(ReportError):
            parse_post(self.janua, "ga4", "create_annotation", body)

    def test_reports_say_which_site_they_belong_to(self):
        req = parse_request(self.termin, "gsc", "page_summary", [])
        out = project(self.termin, req, {"rows": []})
        self.assertEqual(out["scope"]["site"], TERMIN)
        self.assertEqual(out["scope"]["page_paths"], ["/", "/preise"])
        ga4 = project(self.janua, parse_request(self.janua, "ga4", "traffic_summary", []), {"rows": []})
        self.assertEqual(ga4["scope"]["site"], JANUA)

    def test_status_names_site_and_hosts_but_no_property(self):
        for source in ("gsc", "ga4"):
            out = project(self.termin, parse_request(self.termin, source, "get_status", []), {})
            with self.subTest(source=source):
                self.assertEqual(out["site"], TERMIN)
                self.assertEqual(out["hosts"], ["www.termin.test"])
                text = json.dumps(out)
                for hidden in ("654321", "sc-domain", "properties/"):
                    self.assertNotIn(hidden, text)

    def test_single_form_reports_site_default(self):
        cfg = Sites.from_dict(fixture()).config("default")
        out = project(cfg, parse_request(cfg, "gsc", "page_summary", []), {"rows": []})
        self.assertEqual(out["scope"]["site"], "default")
        self.assertIsInstance(parse_get(cfg, "gsc", "list_sitemaps", []), ManageRequest)


if __name__ == "__main__":
    unittest.main()
