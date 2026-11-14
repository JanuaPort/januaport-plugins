"""The adapter serves each request from its own site (JanuaPort/januaport#1379, part A).

Real discovery clients over a fake HTTP seam (no network). With the header `terminagent`
no Google call may carry a value of januaport, also not for status and the sitemap list.
"""

import json
import unittest
from unittest.mock import patch
from urllib.parse import unquote

import httplib2

from google_reporting.domain import Sites, parse_request
from google_reporting.google_adapter import GoogleAdapter
from google_reporting.manage import parse_get, parse_post
from site_fixtures import JANUA, JANUA_ONLY, TERMIN, sites_raw
from test_google_adapter import WholePropertyClient, answer, synthetic_identity
from test_manage_adapter import Fake

TERMIN_ONLY = ("termin.test", "654321")
SITEMAPS = {
    JANUA: "https://www.example.test/sitemap.xml",
    TERMIN: "https://www.termin.test/sitemap.xml",
}
PAGES = {JANUA: "/atas", TERMIN: "/preise"}


class Base(unittest.TestCase):
    def setUp(self):
        self.sites = Sites.from_dict(sites_raw())
        self.google = Fake()
        patches = (
            patch("google_reporting.google_adapter.load_identity", return_value=synthetic_identity()),
            patch.object(httplib2.Http, "request", lambda http, *a, **kw: self.google.request(http, *a, **kw)),
        )
        for p in patches:
            p.start()
            self.addCleanup(p.stop)
        self.adapter = GoogleAdapter(self.sites, "unused")
        g = self.google
        g.add("GET", "/sitemaps?", {"sitemap": []})
        g.add("GET", "/sitemaps/https://", {"path": "x"})
        g.add("PUT", "/sitemaps/https://", {})
        g.add("POST", "urlInspection/index:inspect", {"inspectionResult": {"indexStatusResult": {"verdict": "PASS"}}})
        g.add("POST", "searchAnalytics/query", {"rows": []})
        g.add("GET", "reportingDataAnnotations?", {"reportingDataAnnotations": []})
        g.add("POST", "reportingDataAnnotations?", {})
        g.add("GET", "channelGroups?", {"channelGroups": []})
        g.add("POST", "channelGroups?", {})

    def config(self, site):
        return self.sites.config(site)

    def get(self, site, source, op, pairs=()):
        return self.adapter.run(parse_get(self.config(site), source, op, list(pairs)))

    def post(self, site, source, op, body):
        return self.adapter.run(parse_post(self.config(site), source, op, json.dumps(body).encode()))

    def every_tool(self, site):
        """One call of every manage tool and one report, all for the same site."""
        cfg = self.config(site)
        return [
            self.get(site, "gsc", "list_sitemaps"),
            self.get(site, "gsc", "get_sitemap", [("sitemap_url", SITEMAPS[site])]),
            self.get(site, "gsc", "inspect_url", [("page_path", PAGES[site])]),
            self.post(site, "gsc", "submit_sitemap", {"sitemap_url": SITEMAPS[site]}),
            self.get(site, "ga4", "list_annotations"),
            self.post(site, "ga4", "create_annotation", {"date": "2026-11-14", "title": "Seite"}),
            self.get(site, "ga4", "list_channel_groups"),
            self.post(site, "ga4", "create_channel_group", {"source_hosts": "chatgpt.com"}),
            self.adapter.run(parse_request(cfg, "gsc", "page_summary", [])),
            self.adapter.run(parse_request(cfg, "gsc", "get_status", [])),
        ]

    def texts(self, calls):
        return [unquote(c["uri"]) + " " + json.dumps(c["body"]) for c in calls]


class Separation(Base):
    def test_no_google_call_of_terminagent_carries_a_value_of_januaport(self):
        results = self.every_tool(TERMIN)
        texts = self.texts(self.google.calls)
        self.assertGreaterEqual(len(texts), 10)
        for text in texts:
            with self.subTest(call=text[:90]):
                self.assertTrue(any(own in text for own in TERMIN_ONLY), text)
                for foreign in JANUA_ONLY:
                    self.assertNotIn(foreign, text)
        self.assertTrue(all(r.get("site") == TERMIN or r.get("scope", {}).get("site") == TERMIN for r in results))

    def test_no_google_call_of_januaport_carries_a_value_of_terminagent(self):
        self.every_tool(JANUA)
        for text in self.texts(self.google.calls):
            with self.subTest(call=text[:90]):
                self.assertTrue(any(own in text for own in ("example.test", "123456")), text)
                for foreign in TERMIN_ONLY:
                    self.assertNotIn(foreign, text)

    def test_alternating_sites_use_shared_clients_but_never_the_other_property(self):
        for site in (JANUA, TERMIN, TERMIN, JANUA, TERMIN):
            before = len(self.google.calls)
            self.get(site, "ga4", "list_annotations")
            self.get(site, "gsc", "list_sitemaps")
            for text in self.texts(self.google.calls[before:]):
                foreign = TERMIN_ONLY if site == JANUA else JANUA_ONLY
                with self.subTest(site=site, call=text[:80]):
                    for value in foreign:
                        self.assertNotIn(value, text)
        # One service account: the readonly clients and credentials are cached once.
        self.assertEqual(len(self.adapter.credentials), 2)

    def test_manage_answers_name_the_site(self):
        for site in (JANUA, TERMIN):
            for out in (
                self.get(site, "gsc", "list_sitemaps"),
                self.get(site, "ga4", "list_annotations"),
                self.post(site, "ga4", "create_annotation", {"date": "2026-11-14", "title": "t", "dry_run": True}),
            ):
                with self.subTest(site=site, operation=out["operation"]):
                    self.assertEqual(out["site"], site)

    def test_writes_and_previews_use_the_property_of_the_site(self):
        self.post(TERMIN, "ga4", "create_annotation", {"date": "2026-11-14", "title": "t"})
        self.post(TERMIN, "gsc", "submit_sitemap", {"sitemap_url": SITEMAPS[TERMIN]})
        writes = self.google.writes()
        self.assertEqual(len(writes), 2)
        self.assertIn("properties/654321/", writes[0]["uri"])
        self.assertIn("sc-domain:termin.test", writes[1]["uri"])


class FakeGa(WholePropertyClient):
    """Every dimension and metric compatible; records the property of each call."""

    def __init__(self):
        super().__init__(lambda d, m: answer(d, m, foreign=False))
        self.properties = []

    def check_compatibility(self, request, **kwargs):
        self.properties.append(request.property)
        return super().check_compatibility(request, **kwargs)

    def run_report(self, request, **kwargs):
        from google.analytics.data_v1beta.types import RunReportResponse

        self.properties.append(request.property)
        return RunReportResponse()


class Ga4Reports(unittest.TestCase):
    def setUp(self):
        self.sites = Sites.from_dict(sites_raw())
        self.adapter = GoogleAdapter(self.sites, "unused")
        self.client = FakeGa()
        self.adapter.clients["ga4"] = self.client

    def test_reports_and_status_ask_the_property_of_their_site(self):
        for site, prop in ((TERMIN, "properties/654321"), (JANUA, "properties/123456"), (TERMIN, "properties/654321")):
            cfg = self.sites.config(site)
            for report in ("traffic_summary", "landing_pages", "get_status"):
                with self.subTest(site=site, report=report):
                    self.client.properties.clear()
                    out = self.adapter.run(parse_request(cfg, "ga4", report, []))
                    self.assertTrue(self.client.properties)
                    self.assertEqual(set(self.client.properties), {prop})
                    if report == "get_status":
                        self.assertEqual((out["state"], out["site"]), ("ready", site))
                    else:
                        self.assertEqual(out["scope"]["site"], site)

    def test_the_compatibility_check_uses_the_pages_of_its_site(self):
        cfg = self.sites.config(TERMIN)
        self.adapter.run(parse_request(cfg, "ga4", "get_status", []))
        self.assertEqual(self.client.properties[0], "properties/654321")

    def test_a_request_for_an_unknown_site_is_a_fixed_error_before_any_client_call(self):
        from google_reporting.domain import ReportError

        req = parse_request(self.sites.config(TERMIN), "ga4", "traffic_summary", [])
        object.__setattr__(req, "site", "missing")
        with self.assertRaises(ReportError) as ctx:
            self.adapter.run(req)
        self.assertEqual((ctx.exception.status, ctx.exception.code), (503, "configuration_invalid"))
        self.assertEqual(self.client.properties, [])


if __name__ == "__main__":
    unittest.main()
