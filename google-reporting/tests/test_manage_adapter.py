"""Real official discovery clients and the credential layer, without Google or network.

Pins JanuaPort/januaport#1148: one credential object per scope (never combined),
the scope set per operation, fixed routes/bodies, strict v1alpha handling, no
retries, no raw Google text.
"""

import base64
import json
import unittest
from unittest.mock import patch
from urllib.parse import parse_qs, unquote
import httplib2
from google_reporting.domain import Config, ReportError
from google_reporting.google_adapter import GoogleAdapter
from google_reporting.manage import STANDARD_CHANNELS, channel_group_rules, parse_get, parse_post
from test_contract import fixture
from test_google_adapter import synthetic_identity

GSC_READ = "https://www.googleapis.com/auth/webmasters.readonly"
GSC_WRITE = "https://www.googleapis.com/auth/webmasters"
GA_READ = "https://www.googleapis.com/auth/analytics.readonly"
GA_EDIT = "https://www.googleapis.com/auth/analytics.edit"
SITEMAP = "https://www.example.test/sitemap.xml"
ANNOTATION = {
    "name": "properties/123456/reportingDataAnnotations/456",
    "title": "[JP] Copy ATAS-12",
    "description": "Commit 4ffbc7d8",
    "color": "BLUE",
    "annotationDate": {"year": 2026, "month": 10, "day": 22},
    "systemGenerated": False,
}
HUMAN_ANNOTATION = dict(ANNOTATION, title="Kampagne Herbst")


def group(cfg, hosts=("chatgpt.com",), name="KI-Assistenten", **extra):
    data = {
        "name": "properties/123456/channelGroups/789",
        "displayName": name,
        "groupingRule": channel_group_rules(cfg, list(hosts)),
        "primary": False,
        "systemDefined": False,
    }
    data.update(extra)
    return data


class Fake:
    def __init__(self):
        self.routes = []
        self.calls = []
        self.token_scopes = []
        self.tokens = {}

    def add(self, method, needle, data=None, status=200):
        self.routes.append((method, needle, data, status))

    def request(self, http, uri, method="GET", body=None, headers=None, **kwargs):
        if uri == "https://oauth2.googleapis.com/token":
            assertion = parse_qs(body if isinstance(body, str) else body.decode())["assertion"][0]
            payload = assertion.split(".")[1]
            claims = json.loads(base64.urlsafe_b64decode(payload + "=" * (-len(payload) % 4)))
            token = "tok%d" % len(self.tokens)
            self.tokens[token] = claims["scope"]
            self.token_scopes.append(claims["scope"])
            data = {"access_token": token, "expires_in": 3600, "token_type": "Bearer"}
            return httplib2.Response({"status": "200", "content-type": "application/json"}), json.dumps(data).encode()
        auth = (headers or {}).get("authorization", "")
        scope = self.tokens.get(auth.replace("Bearer ", ""))
        parsed = json.loads(body) if body else None
        self.calls.append({"method": method, "uri": unquote(uri), "raw": uri, "body": parsed, "scope": scope})
        for route_method, needle, data, status in self.routes:
            if route_method == method and needle in unquote(uri):
                if status != 200:
                    data = {"error": {"code": status, "message": "SECRET-CANARY"}}
                return httplib2.Response({"status": str(status), "content-type": "application/json"}), json.dumps(data if data is not None else {}).encode()
        data = {"error": {"code": 404, "message": "SECRET-CANARY"}}
        return httplib2.Response({"status": "404", "content-type": "application/json"}), json.dumps(data).encode()

    def api_calls(self):
        return [c for c in self.calls]

    def writes(self):
        return [c for c in self.calls if c["method"] in ("POST", "PUT", "PATCH", "DELETE") and "urlInspection" not in c["uri"]]


class Base(unittest.TestCase):
    def setUp(self):
        self.cfg = Config.from_dict(fixture())
        self.google = Fake()
        self.identity = synthetic_identity()
        patches = (
            patch("google_reporting.google_adapter.load_identity", return_value=self.identity),
            patch.object(httplib2.Http, "request", lambda http, *a, **kw: self.google.request(http, *a, **kw)),
        )
        for p in patches:
            p.start()
            self.addCleanup(p.stop)
        self.adapter = GoogleAdapter(self.cfg, "unused")

    def get(self, source, op, pairs=()):
        return self.adapter.run(parse_get(self.cfg, source, op, list(pairs)))

    def post(self, source, op, body):
        return self.adapter.run(parse_post(self.cfg, source, op, json.dumps(body).encode()))


class ScopePins(Base):
    """W1: each operation asks Google for exactly this scope set, one scope per credential."""

    def scopes(self):
        return set(self.google.token_scopes)

    def prepare(self):
        g = self.google
        g.add("GET", "/sitemaps?", {"sitemap": [{"path": SITEMAP, "isPending": False}]})
        g.add("GET", "/sitemaps/" + SITEMAP, {"path": SITEMAP, "isPending": False}, 200)
        g.add("PUT", "/sitemaps/" + SITEMAP, {})
        g.add("POST", "urlInspection/index:inspect", {"inspectionResult": {"indexStatusResult": {"verdict": "PASS"}}})
        g.add("GET", "reportingDataAnnotations?", {"reportingDataAnnotations": [ANNOTATION]})
        g.add("GET", "reportingDataAnnotations/456", ANNOTATION)
        g.add("POST", "reportingDataAnnotations?", ANNOTATION)
        g.add("PATCH", "reportingDataAnnotations/456", ANNOTATION)
        g.add("GET", "channelGroups?", {"channelGroups": []})
        g.add("GET", "channelGroups/789", group(self.cfg))
        g.add("POST", "channelGroups?", group(self.cfg))
        g.add("PATCH", "channelGroups/789", group(self.cfg))

    def test_scope_set_per_operation(self):
        self.prepare()
        cases = [
            ("get", "gsc", "list_sitemaps", [], {GSC_READ}),
            ("get", "gsc", "get_sitemap", [("sitemap_url", SITEMAP)], {GSC_READ}),
            ("get", "gsc", "inspect_url", [("page_path", "/atas")], {GSC_READ}),
            ("post", "gsc", "submit_sitemap", {"sitemap_url": SITEMAP, "dry_run": True}, {GSC_READ}),
            ("post", "gsc", "submit_sitemap", {"sitemap_url": SITEMAP}, {GSC_READ, GSC_WRITE}),
            ("get", "ga4", "list_annotations", [], {GA_READ}),
            ("get", "ga4", "list_channel_groups", [], {GA_READ}),
            ("post", "ga4", "create_annotation", {"date": "2026-10-22", "title": "t", "dry_run": True}, set()),
            ("post", "ga4", "create_annotation", {"date": "2026-10-22", "title": "t"}, {GA_EDIT}),
            ("post", "ga4", "update_annotation", {"annotation_id": "456", "title": "t", "dry_run": True}, {GA_READ}),
            ("post", "ga4", "update_annotation", {"annotation_id": "456", "title": "t"}, {GA_READ, GA_EDIT}),
            ("post", "ga4", "create_channel_group", {"source_hosts": "claude.ai", "dry_run": True}, {GA_READ}),
            ("post", "ga4", "create_channel_group", {"source_hosts": "claude.ai"}, {GA_READ, GA_EDIT}),
            ("post", "ga4", "update_channel_group", {"channel_group_id": "789", "source_hosts": "claude.ai", "dry_run": True}, {GA_READ}),
            ("post", "ga4", "update_channel_group", {"channel_group_id": "789", "source_hosts": "claude.ai"}, {GA_READ, GA_EDIT}),
        ]
        for kind, source, op, arg, expected in cases:
            with self.subTest(op=op, arg=str(arg)[:60]):
                self.google.token_scopes.clear()
                self.google.tokens.clear()
                self.adapter = GoogleAdapter(self.cfg, "unused")
                (self.get if kind == "get" else self.post)(source, op, arg)
                self.assertEqual(self.scopes(), expected)
                for scope in self.google.token_scopes:
                    self.assertNotIn(" ", scope, "scopes are never combined in one credential")

    def test_reads_never_carry_a_write_scope_on_the_wire(self):
        self.prepare()
        self.get("gsc", "list_sitemaps")
        self.get("ga4", "list_annotations")
        self.get("ga4", "list_channel_groups")
        self.assertTrue(self.google.calls)
        self.assertTrue(all(c["scope"] in (GSC_READ, GA_READ) for c in self.google.calls))

    def test_write_calls_use_the_write_credential_and_reads_stay_readonly(self):
        self.prepare()
        self.post("ga4", "update_annotation", {"annotation_id": "456", "title": "neu"})
        by_method = {c["method"]: c["scope"] for c in self.google.calls}
        self.assertEqual(by_method["GET"], GA_READ)
        self.assertEqual(by_method["PATCH"], GA_EDIT)

    def test_existing_report_credentials_are_unchanged(self):
        from google_reporting.google_adapter import SCOPES

        self.assertEqual(SCOPES, {"gsc": GSC_READ, "ga4": GA_READ})


class Sitemaps(Base):
    def test_list_and_get_projection_has_only_known_fields(self):
        self.google.add(
            "GET",
            "/sitemaps?",
            {"sitemap": [{"path": SITEMAP, "lastSubmitted": "2026-10-20T10:00:00Z", "isPending": False, "isSitemapsIndex": False, "type": "sitemap", "warnings": "1", "errors": "0", "contents": [{"type": "web", "submitted": "12", "indexed": "9"}], "secret": "SECRET-CANARY"}]},
        )
        out = self.get("gsc", "list_sitemaps")
        self.assertEqual(out["operation"], "list_sitemaps")
        item = out["items"][0]
        self.assertEqual(item["path"], SITEMAP)
        self.assertEqual(item["last_submitted"], "2026-10-20T10:00:00Z")
        self.assertIs(item["is_pending"], False)
        self.assertEqual(item["submitted"], 12)
        self.assertNotIn("SECRET-CANARY", json.dumps(out))
        self.assertEqual(self.google.calls[0]["method"], "GET")
        self.assertIn("sc-domain:example.test", self.google.calls[0]["uri"])

    def test_get_sitemap_not_registered_has_a_fixed_code(self):
        with self.assertRaises(ReportError) as cm:
            self.get("gsc", "get_sitemap", [("sitemap_url", SITEMAP)])
        self.assertEqual((cm.exception.status, cm.exception.code), (404, "sitemap_not_found"))
        self.assertNotIn("CANARY", str(cm.exception))

    def test_submit_puts_without_body_and_reports_before_and_after(self):
        self.google.add("PUT", "/sitemaps/" + SITEMAP, {})
        out = self.post("gsc", "submit_sitemap", {"sitemap_url": SITEMAP})
        put = [c for c in self.google.calls if c["method"] == "PUT"]
        self.assertEqual(len(put), 1)
        self.assertIsNone(put[0]["body"])
        self.assertIn("sc-domain:example.test", put[0]["uri"])
        self.assertEqual(put[0]["scope"], GSC_WRITE)
        self.assertEqual((out["state"], out["dry_run"], out["target"]), ("applied", False, SITEMAP))
        self.assertEqual(out["before"], {"registered": False})
        self.assertEqual(out["after"]["registered"], True)
        self.assertEqual(out["after"]["sitemap_url"], SITEMAP)
        self.assertEqual(out["changed_fields"], ["registered"])

    def test_submit_of_a_registered_sitemap_shows_the_previous_state(self):
        self.google.add("GET", "/sitemaps/" + SITEMAP, {"path": SITEMAP, "lastSubmitted": "2026-10-01T00:00:00Z", "isPending": False})
        self.google.add("PUT", "/sitemaps/" + SITEMAP, {})
        out = self.post("gsc", "submit_sitemap", {"sitemap_url": SITEMAP})
        self.assertEqual(out["before"], {"registered": True, "last_submitted": "2026-10-01T00:00:00Z"})

    def test_dry_run_never_calls_put(self):
        out = self.post("gsc", "submit_sitemap", {"sitemap_url": SITEMAP, "dry_run": True})
        self.assertEqual(out["state"], "preview")
        self.assertTrue(out["dry_run"])
        self.assertEqual(self.google.writes(), [])
        self.assertEqual(out["after"]["registered"], True)

    def test_failed_put_is_not_retried_and_has_no_raw_text(self):
        for status, code in ((403, "source_access_denied"), (429, "source_quota_limited"), (500, "source_unavailable")):
            self.google.calls.clear()
            self.google.routes.clear()
            self.google.add("PUT", "/sitemaps/" + SITEMAP, status=status)
            with self.assertRaises(ReportError) as cm:
                self.post("gsc", "submit_sitemap", {"sitemap_url": SITEMAP})
            self.assertEqual(cm.exception.code, code)
            self.assertNotIn("CANARY", str(cm.exception))
            self.assertEqual(len([c for c in self.google.calls if c["method"] == "PUT"]), 1)

    def test_inspect_projects_known_index_fields_and_the_referring_sitemaps(self):
        self.google.add(
            "POST",
            "urlInspection/index:inspect",
            {
                "inspectionResult": {
                    "inspectionResultLink": "https://search.google.com/search-console/inspect?resource_id=SECRET-CANARY",
                    "indexStatusResult": {
                        "verdict": "PASS",
                        "coverageState": "Submitted and indexed",
                        "robotsTxtState": "ALLOWED",
                        "indexingState": "INDEXING_ALLOWED",
                        "pageFetchState": "SUCCESSFUL",
                        "lastCrawlTime": "2026-10-21T08:00:00Z",
                        "googleCanonical": "https://www.example.test/atas",
                        "userCanonical": "https://www.example.test/atas",
                        "crawledAs": "MOBILE",
                        "sitemap": [SITEMAP],
                        "referringUrls": ["https://other.example/SECRET-CANARY"],
                    },
                }
            },
        )
        out = self.get("gsc", "inspect_url", [("page_path", "/atas")])
        call = self.google.calls[0]
        self.assertEqual(call["body"], {"inspectionUrl": "https://www.example.test/atas", "siteUrl": "sc-domain:example.test", "languageCode": "en-US"})
        self.assertEqual(call["scope"], GSC_READ)
        item = out["items"][0]
        self.assertEqual(item["page_path"], "/atas")
        self.assertEqual(item["verdict"], "PASS")
        self.assertEqual(item["coverage_state"], "Submitted and indexed")
        self.assertEqual(item["sitemaps"], [SITEMAP])
        self.assertIs(item["listed_in_sitemap"], True)
        self.assertNotIn("CANARY", json.dumps(out))

    def test_inspect_without_a_sitemap_says_so(self):
        self.google.add("POST", "urlInspection/index:inspect", {"inspectionResult": {"indexStatusResult": {"verdict": "NEUTRAL", "coverageState": "Discovered"}}})
        item = self.get("gsc", "inspect_url", [("page_path", "/atas")])["items"][0]
        self.assertEqual(item["sitemaps"], [])
        self.assertIs(item["listed_in_sitemap"], False)


class Annotations(Base):
    def test_list_projects_known_fields_and_marks_managed(self):
        self.google.add("GET", "reportingDataAnnotations?", {"reportingDataAnnotations": [ANNOTATION, HUMAN_ANNOTATION, dict(ANNOTATION, name="properties/123456/reportingDataAnnotations/9", systemGenerated=True, title="[JP] x", annotationDate=None, annotationDateRange={"startDate": {"year": 2026, "month": 10, "day": 1}, "endDate": {"year": 2026, "month": 10, "day": 3}})]})
        out = self.get("ga4", "list_annotations")
        first, second, third = out["items"]
        self.assertEqual((first["id"], first["date"], first["managed"]), ("456", "2026-10-22", True))
        self.assertEqual(first["title"], "[JP] Copy ATAS-12")
        self.assertIs(second["managed"], False)
        self.assertIs(third["managed"], False, "system generated annotations are never managed")
        self.assertEqual(third["date_range"], {"start": "2026-10-01", "end": "2026-10-03"})
        call = self.google.calls[0]
        self.assertIn("properties/123456/reportingDataAnnotations", call["uri"])
        self.assertEqual(call["scope"], GA_READ)

    def test_list_marks_truncation_when_google_has_more_pages(self):
        self.google.add("GET", "reportingDataAnnotations?", {"reportingDataAnnotations": [ANNOTATION], "nextPageToken": "SECRET-CANARY"})
        out = self.get("ga4", "list_annotations")
        self.assertIs(out["quality"]["truncated"], True)
        self.assertNotIn("CANARY", json.dumps(out))

    def test_create_posts_the_fixed_body_to_the_configured_property_only(self):
        self.google.add("POST", "reportingDataAnnotations?", dict(ANNOTATION))
        out = self.post("ga4", "create_annotation", {"date": "2026-10-22", "title": "Copy ATAS-12", "description": "Commit 4ffbc7d8"})
        (call,) = self.google.writes()
        self.assertEqual(call["method"], "POST")
        self.assertTrue(call["uri"].startswith("https://analyticsadmin.googleapis.com/v1alpha/properties/123456/reportingDataAnnotations"))
        self.assertEqual(call["body"], {"title": "[JP] Copy ATAS-12", "color": "BLUE", "description": "Commit 4ffbc7d8", "annotationDate": {"year": 2026, "month": 10, "day": 22}})
        self.assertEqual(call["scope"], GA_EDIT)
        self.assertEqual((out["state"], out["target"], out["before"]), ("applied", "456", None))
        self.assertEqual(out["after"]["title"], "[JP] Copy ATAS-12")
        self.assertEqual(out["after"]["annotation_date"], "2026-10-22")
        self.assertEqual(out["changed_fields"], ["annotation_date", "color", "description", "title"])

    def test_create_dry_run_never_writes(self):
        out = self.post("ga4", "create_annotation", {"date": "2026-10-22", "title": "t", "dry_run": True})
        self.assertEqual(self.google.calls, [])
        self.assertEqual((out["state"], out["dry_run"], out["target"], out["before"]), ("preview", True, None, None))
        self.assertEqual(out["after"]["title"], "[JP] t")

    def test_update_reads_first_and_patches_only_the_changed_fields(self):
        self.google.add("GET", "reportingDataAnnotations/456", ANNOTATION)
        self.google.add("PATCH", "reportingDataAnnotations/456", dict(ANNOTATION, title="[JP] neu", color="RED"))
        out = self.post("ga4", "update_annotation", {"annotation_id": "456", "title": "neu", "color": "RED"})
        self.assertEqual([c["method"] for c in self.google.calls], ["GET", "PATCH"])
        patch_call = self.google.calls[1]
        self.assertIn("properties/123456/reportingDataAnnotations/456", patch_call["uri"])
        self.assertIn("updateMask=color,title", patch_call["uri"])
        self.assertEqual(patch_call["body"], {"title": "[JP] neu", "color": "RED"})
        self.assertEqual(out["changed_fields"], ["color", "title"])
        self.assertEqual(out["before"]["title"], "[JP] Copy ATAS-12")
        self.assertEqual(out["after"]["title"], "[JP] neu")
        self.assertEqual(out["target"], "456")

    def test_update_of_a_human_or_system_annotation_is_refused_before_any_write(self):
        for existing in (HUMAN_ANNOTATION, dict(ANNOTATION, systemGenerated=True)):
            self.google.calls.clear()
            self.google.routes.clear()
            self.google.add("GET", "reportingDataAnnotations/456", existing)
            with self.assertRaises(ReportError) as cm:
                self.post("ga4", "update_annotation", {"annotation_id": "456", "title": "neu"})
            self.assertEqual((cm.exception.status, cm.exception.code), (409, "annotation_not_managed"))
            self.assertEqual(self.google.writes(), [])

    def test_update_of_a_missing_annotation_has_a_fixed_code(self):
        with self.assertRaises(ReportError) as cm:
            self.post("ga4", "update_annotation", {"annotation_id": "456", "title": "neu"})
        self.assertEqual((cm.exception.status, cm.exception.code), (404, "annotation_not_found"))
        self.assertEqual(self.google.writes(), [])

    def test_unknown_v1alpha_field_fails_closed_before_the_write(self):
        # H3: v1alpha may change shape; an unknown field in what we would overwrite stops the write.
        self.google.add("GET", "reportingDataAnnotations/456", dict(ANNOTATION, brandNewField="x"))
        with self.assertRaises(ReportError) as cm:
            self.post("ga4", "update_annotation", {"annotation_id": "456", "title": "neu"})
        self.assertEqual((cm.exception.status, cm.exception.code), (502, "unexpected_source_shape"))
        self.assertEqual(self.google.writes(), [])

    def test_update_without_a_difference_writes_nothing(self):
        self.google.add("GET", "reportingDataAnnotations/456", ANNOTATION)
        out = self.post("ga4", "update_annotation", {"annotation_id": "456", "title": "Copy ATAS-12"})
        self.assertEqual(self.google.writes(), [])
        self.assertEqual((out["state"], out["changed_fields"]), ("unchanged", []))

    def test_update_dry_run_shows_before_after_without_patch(self):
        self.google.add("GET", "reportingDataAnnotations/456", ANNOTATION)
        out = self.post("ga4", "update_annotation", {"annotation_id": "456", "color": "GREEN", "dry_run": True})
        self.assertEqual(self.google.writes(), [])
        self.assertEqual((out["state"], out["changed_fields"]), ("preview", ["color"]))
        self.assertEqual((out["before"]["color"], out["after"]["color"]), ("BLUE", "GREEN"))

    def test_errors_are_not_retried_and_carry_no_raw_text(self):
        for status, code in ((401, "source_access_denied"), (403, "source_access_denied"), (429, "source_quota_limited"), (503, "source_unavailable"), (400, "source_rejected")):
            self.google.calls.clear()
            self.google.routes.clear()
            self.google.add("POST", "reportingDataAnnotations?", status=status)
            with self.assertRaises(ReportError) as cm:
                self.post("ga4", "create_annotation", {"date": "2026-10-22", "title": "t"})
            self.assertEqual(cm.exception.code, code)
            self.assertNotIn("CANARY", str(cm.exception))
            self.assertEqual(len(self.google.writes()), 1)


class ChannelGroups(Base):
    def test_list_projects_groups_with_managed_flag_hosts_and_channels(self):
        default = {"name": "properties/123456/channelGroups/1", "displayName": "Default Channel Group", "systemDefined": True, "primary": True, "groupingRule": []}
        self.google.add("GET", "channelGroups?", {"channelGroups": [default, group(self.cfg, hosts=("chatgpt.com", "claude.ai"))]})
        out = self.get("ga4", "list_channel_groups")
        system, ours = out["items"]
        self.assertEqual((system["id"], system["system_defined"], system["managed"], system["primary"]), ("1", True, False, True))
        self.assertEqual((ours["id"], ours["managed"], ours["primary"]), ("789", True, False))
        self.assertEqual(ours["source_hosts"], ["chatgpt.com", "claude.ai"])
        self.assertEqual(ours["channels"], ["KI-Assistenten"] + list(STANDARD_CHANNELS))

    def test_create_sends_the_built_group_never_primary(self):
        self.google.add("GET", "channelGroups?", {"channelGroups": []})
        self.google.add("POST", "channelGroups?", group(self.cfg, hosts=("chatgpt.com", "claude.ai")))
        out = self.post("ga4", "create_channel_group", {"source_hosts": "chatgpt.com,claude.ai"})
        (call,) = self.google.writes()
        self.assertTrue(call["uri"].startswith("https://analyticsadmin.googleapis.com/v1alpha/properties/123456/channelGroups"))
        self.assertIs(call["body"]["primary"], False)
        self.assertEqual(call["body"]["displayName"], "KI-Assistenten")
        names = [r["displayName"] for r in call["body"]["groupingRule"]]
        self.assertEqual(names, ["KI-Assistenten"] + list(STANDARD_CHANNELS))
        self.assertLess(names.index("KI-Assistenten"), names.index("Referral"))
        self.assertEqual(call["scope"], GA_EDIT)
        self.assertEqual((out["state"], out["target"], out["before"]), ("applied", "789", None))
        self.assertEqual(out["after"]["source_hosts"], ["chatgpt.com", "claude.ai"])
        self.assertIs(out["after"].get("primary", False), False)

    def test_create_refuses_a_second_group_with_the_configured_name(self):
        self.google.add("GET", "channelGroups?", {"channelGroups": [group(self.cfg)]})
        with self.assertRaises(ReportError) as cm:
            self.post("ga4", "create_channel_group", {"source_hosts": "claude.ai"})
        self.assertEqual((cm.exception.status, cm.exception.code), (409, "channel_group_exists"))
        self.assertEqual(self.google.writes(), [])

    def test_create_dry_run_never_writes(self):
        self.google.add("GET", "channelGroups?", {"channelGroups": []})
        out = self.post("ga4", "create_channel_group", {"source_hosts": "claude.ai", "dry_run": True})
        self.assertEqual(self.google.writes(), [])
        self.assertEqual((out["state"], out["target"]), ("preview", None))
        self.assertEqual(out["after"]["rule_count"], 1 + len(STANDARD_CHANNELS))

    def test_update_patches_only_the_rules_and_never_primary(self):
        self.google.add("GET", "channelGroups/789", group(self.cfg, hosts=("chatgpt.com",)))
        self.google.add("PATCH", "channelGroups/789", group(self.cfg, hosts=("chatgpt.com", "claude.ai")))
        out = self.post("ga4", "update_channel_group", {"channel_group_id": "789", "source_hosts": "chatgpt.com,claude.ai"})
        patch_call = self.google.writes()[0]
        self.assertEqual(patch_call["method"], "PATCH")
        self.assertIn("properties/123456/channelGroups/789", patch_call["uri"])
        self.assertIn("updateMask=grouping_rule", patch_call["uri"])
        self.assertEqual(set(patch_call["body"]), {"groupingRule"})
        self.assertEqual(patch_call["scope"], GA_EDIT)
        self.assertEqual(out["before"]["source_hosts"], ["chatgpt.com"])
        self.assertEqual(out["after"]["source_hosts"], ["chatgpt.com", "claude.ai"])
        self.assertEqual(out["changed_fields"], ["source_hosts"])

    def test_update_of_a_foreign_or_system_group_is_refused_before_any_write(self):
        # SEC P1: only the group with the configured name; a human's group stays untouched.
        for existing in (group(self.cfg, name="Meine Gruppe"), group(self.cfg, systemDefined=True), group(self.cfg, name="Default Channel Group", primary=True, systemDefined=True)):
            self.google.calls.clear()
            self.google.routes.clear()
            self.google.add("GET", "channelGroups/789", existing)
            with self.assertRaises(ReportError) as cm:
                self.post("ga4", "update_channel_group", {"channel_group_id": "789", "source_hosts": "claude.ai"})
            self.assertEqual((cm.exception.status, cm.exception.code), (409, "channel_group_not_managed"))
            self.assertEqual(self.google.writes(), [])

    def test_update_of_a_missing_group_has_a_fixed_code(self):
        with self.assertRaises(ReportError) as cm:
            self.post("ga4", "update_channel_group", {"channel_group_id": "789", "source_hosts": "claude.ai"})
        self.assertEqual((cm.exception.status, cm.exception.code), (404, "channel_group_not_found"))
        self.assertEqual(self.google.writes(), [])

    def test_update_of_a_group_that_became_primary_is_left_alone(self):
        # A human made it primary in the meantime: we neither unset nor reuse it silently.
        self.google.add("GET", "channelGroups/789", group(self.cfg, primary=True))
        self.google.add("PATCH", "channelGroups/789", group(self.cfg, primary=True))
        out = self.post("ga4", "update_channel_group", {"channel_group_id": "789", "source_hosts": "claude.ai"})
        self.assertEqual(set(self.google.writes()[0]["body"]), {"groupingRule"})
        self.assertIs(out["before"]["primary"], True)

    def test_unknown_v1alpha_field_fails_closed_before_the_write(self):
        self.google.add("GET", "channelGroups/789", group(self.cfg, brandNewField=1))
        with self.assertRaises(ReportError) as cm:
            self.post("ga4", "update_channel_group", {"channel_group_id": "789", "source_hosts": "claude.ai"})
        self.assertEqual((cm.exception.status, cm.exception.code), (502, "unexpected_source_shape"))
        self.assertEqual(self.google.writes(), [])

    def test_update_dry_run_shows_before_after_without_patch(self):
        self.google.add("GET", "channelGroups/789", group(self.cfg, hosts=("chatgpt.com",)))
        out = self.post("ga4", "update_channel_group", {"channel_group_id": "789", "source_hosts": "claude.ai", "dry_run": True})
        self.assertEqual(self.google.writes(), [])
        self.assertEqual((out["state"], out["before"]["source_hosts"], out["after"]["source_hosts"]), ("preview", ["chatgpt.com"], ["claude.ai"]))

    def test_update_without_a_difference_writes_nothing(self):
        self.google.add("GET", "channelGroups/789", group(self.cfg, hosts=("chatgpt.com",)))
        out = self.post("ga4", "update_channel_group", {"channel_group_id": "789", "source_hosts": "chatgpt.com"})
        self.assertEqual(self.google.writes(), [])
        self.assertEqual((out["state"], out["changed_fields"]), ("unchanged", []))

    def test_google_errors_are_fixed_codes_without_retry(self):
        self.google.add("GET", "channelGroups?", {"channelGroups": []})
        self.google.add("POST", "channelGroups?", status=429)
        with self.assertRaises(ReportError) as cm:
            self.post("ga4", "create_channel_group", {"source_hosts": "claude.ai"})
        self.assertEqual(cm.exception.code, "source_quota_limited")
        self.assertEqual(len(self.google.writes()), 1)


if __name__ == "__main__":
    unittest.main()
