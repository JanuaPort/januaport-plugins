"""Write/manage handles of JanuaPort/januaport#1148: pure validation, builders, log line.

No Google, no network. Every rejection is a fixed code without echo of the input.
"""

import json
import re
import unittest
from google_reporting.domain import Config, ReportError
from google_reporting.manage import (
    GET_OPS,
    POST_OPS,
    STANDARD_CHANNELS,
    ManageRequest,
    annotation_body,
    channel_group_body,
    channel_group_rules,
    change_line,
    hosts_from_rule,
    parse_get,
    parse_post,
)
from test_contract import fixture

PROPERTY_FORBIDDEN = ("property", "property_id", "site", "site_url", "primary", "name")


def post(cfg, source, op, body):
    return parse_post(cfg, source, op, json.dumps(body).encode())


def code_of(cfg, source, op, body):
    with unittest.TestCase().assertRaises(ReportError) as cm:
        post(cfg, source, op, body)
    return cm.exception.status, cm.exception.code


class Routes(unittest.TestCase):
    def test_operation_sets_are_exactly_the_issue_list(self):
        self.assertEqual(
            GET_OPS, {"gsc": {"list_sitemaps", "get_sitemap", "inspect_url"}, "ga4": {"list_annotations", "list_channel_groups"}}
        )
        self.assertEqual(
            POST_OPS,
            {
                "gsc": {"submit_sitemap"},
                "ga4": {
                    "create_annotation",
                    "update_annotation",
                    "create_channel_group",
                    "update_channel_group",
                },
            },
        )
        for ops in list(GET_OPS.values()) + list(POST_OPS.values()):
            for op in ops:
                self.assertNotRegex(op, r"delete|archive|remove")


class ConfigName(unittest.TestCase):
    def test_channel_group_name_default_and_fail_closed(self):
        self.assertEqual(Config.from_dict(fixture()).ga4_channel_group_name, "KI-Assistenten")
        raw = fixture()
        raw["ga4_channel_group_name"] = "AI Traffic"
        self.assertEqual(Config.from_dict(raw).ga4_channel_group_name, "AI Traffic")
        for bad in ("", "x" * 81, "line\nbreak", 5, None):
            raw = fixture()
            raw["ga4_channel_group_name"] = bad
            with self.subTest(bad=bad), self.assertRaises(ValueError):
                Config.from_dict(raw)


class SitemapUrl(unittest.TestCase):
    def setUp(self):
        self.cfg = Config.from_dict(fixture())

    def test_valid_url_on_configured_host(self):
        req = post(self.cfg, "gsc", "submit_sitemap", {"sitemap_url": "https://www.example.test/sitemap.xml"})
        self.assertEqual(req.params["sitemap_url"], "https://www.example.test/sitemap.xml")
        self.assertFalse(req.dry_run)
        req = post(self.cfg, "gsc", "submit_sitemap", {"sitemap_url": "https://www.example.test/a/b_c-1.xml", "dry_run": True})
        self.assertTrue(req.dry_run)

    def test_invalid_urls_are_rejected_without_echo(self):
        for url in (
            "http://www.example.test/sitemap.xml",
            "https://evil.example/sitemap.xml",
            "https://example.test/sitemap.xml",
            "https://www.example.test.evil.example/sitemap.xml",
            "https://www.example.test/sitemap.xml?x=1",
            "https://www.example.test/sitemap.xml#frag",
            "https://www.example.test/../sitemap.xml",
            "https://www.example.test/a//sitemap.xml",
            "https://user@www.example.test/sitemap.xml",
            "https://www.example.test:8443/sitemap.xml",
            "https://www.example.test/sitemap.txt",
            "https://www.example.test/sitemap.xml ",
            "https://www.example.test/sitemäp.xml",
            "https://WWW.EXAMPLE.TEST/sitemap.xml",
            "https://www.exämple.test/sitemap.xml",
            "https://www.example.test/" + "a" * 600 + ".xml",
            "https://www.example.test/%2e%2e/sitemap.xml",
            "",
            None,
            5,
        ):
            with self.subTest(url=str(url)[:40]):
                status, code = code_of(self.cfg, "gsc", "submit_sitemap", {"sitemap_url": url})
                self.assertEqual((status, code), (400, "invalid_input"))

    def test_url_prefix_property_requires_the_prefix(self):
        raw = fixture()
        raw["gsc_property"] = "https://www.example.test/"
        cfg = Config.from_dict(raw)
        post(cfg, "gsc", "submit_sitemap", {"sitemap_url": "https://www.example.test/sitemap.xml"})

    def test_get_sitemap_uses_the_same_rule(self):
        req = parse_get(self.cfg, "gsc", "get_sitemap", [("sitemap_url", "https://www.example.test/sitemap.xml")])
        self.assertEqual(req.operation, "get_sitemap")
        with self.assertRaises(ReportError):
            parse_get(self.cfg, "gsc", "get_sitemap", [("sitemap_url", "https://evil.example/x.xml")])
        with self.assertRaises(ReportError):
            parse_get(self.cfg, "gsc", "get_sitemap", [])


class InspectUrl(unittest.TestCase):
    def test_only_allowlisted_paths_map_to_the_configured_url(self):
        cfg = Config.from_dict(fixture())
        req = parse_get(cfg, "gsc", "inspect_url", [("page_path", "/atas")])
        self.assertEqual(req.params, {"page_path": "/atas", "url": "https://www.example.test/atas"})
        for pairs in ([("page_path", "/private")], [("page_path", "https://www.example.test/atas")], [], [("page_path", "/atas"), ("page_path", "/atas")], [("page_path", "/atas"), ("x", "1")]):
            with self.subTest(pairs=pairs), self.assertRaises(ReportError) as cm:
                parse_get(cfg, "gsc", "inspect_url", pairs)
            self.assertEqual(cm.exception.status, 400)


class Annotation(unittest.TestCase):
    def setUp(self):
        self.cfg = Config.from_dict(fixture())

    def create(self, **fields):
        body = {"date": "2026-10-22", "title": "Copy ATAS-12"}
        body.update(fields)
        return post(self.cfg, "ga4", "create_annotation", body)

    def test_title_gets_the_managed_prefix_and_defaults(self):
        req = self.create()
        self.assertEqual(req.params["title"], "[JP] Copy ATAS-12")
        self.assertEqual(req.params["color"], "BLUE")
        self.assertEqual(req.params["date"], "2026-10-22")
        self.assertNotIn("description", req.params)
        self.assertEqual(self.create(title="a" * 55).params["title"], "[JP] " + "a" * 55)

    def test_limits_follow_google_help(self):
        # Title 60 characters including the prefix, description 150.
        self.assertEqual(code_of(self.cfg, "ga4", "create_annotation", {"date": "2026-10-22", "title": "a" * 56}), (400, "invalid_input"))
        self.create(description="d" * 150)
        self.assertEqual(code_of(self.cfg, "ga4", "create_annotation", {"date": "2026-10-22", "title": "t", "description": "d" * 151})[1], "invalid_input")

    def test_date_is_a_single_valid_day(self):
        for bad in ("2026-10-22/2026-10-25", "2026-13-01", "2026-02-30", "22.10.2026", "2026-10-22T10:00:00Z", "", 20261022, None, "2019-12-31", "2099-01-01"):
            with self.subTest(bad=bad):
                self.assertEqual(code_of(self.cfg, "ga4", "create_annotation", {"date": bad, "title": "t"}), (400, "invalid_input"))

    def test_color_from_a_fixed_list(self):
        for good in ("PURPLE", "BROWN", "BLUE", "GREEN", "RED", "CYAN"):
            self.assertEqual(self.create(color=good).params["color"], good)
        for bad in ("ORANGE", "COLOR_UNSPECIFIED", "blue", "", 1):
            with self.subTest(bad=bad):
                self.assertEqual(code_of(self.cfg, "ga4", "create_annotation", {"date": "2026-10-22", "title": "t", "color": bad})[1], "invalid_input")

    def test_control_characters_are_rejected(self):
        for field in ("title", "description"):
            for bad in ("a\nb", "a\x00b", "a\tb"):
                body = {"date": "2026-10-22", "title": "t", field: bad}
                self.assertEqual(code_of(self.cfg, "ga4", "create_annotation", body)[1], "invalid_input")

    def test_personal_data_is_rejected_with_a_fixed_code_and_no_echo(self):
        # SEC P2: email, phone, IBAN in the only free texts that reach the property.
        for field in ("title", "description"):
            for canary in ("max.mustermann@example.test", "+49 30 12345678", "DE89370400440532013000", "0511/1234567"):
                with self.subTest(field=field, canary=canary):
                    body = {"date": "2026-10-22", "title": "t"}
                    body[field] = "Fix fuer " + canary
                    with self.assertRaises(ReportError) as cm:
                        post(self.cfg, "ga4", "create_annotation", body)
                    self.assertEqual((cm.exception.status, cm.exception.code), (400, "personal_data_rejected"))
                    self.assertNotIn(canary, str(cm.exception))
                    self.assertNotIn(canary, repr(cm.exception.args))

    def test_ticket_and_commit_text_is_not_personal_data(self):
        self.create(title="ATAS-1148 /atas", description="Commit 4ffbc7d8, Ticket JanuaPort/januaport#1148, 2026-10-22")

    def test_unknown_and_foreign_fields_are_rejected(self):
        for key in PROPERTY_FORBIDDEN + ("annotation_id", "date_range", "start_date", "system_generated", "delete"):
            with self.subTest(key=key):
                self.assertEqual(code_of(self.cfg, "ga4", "create_annotation", {"date": "2026-10-22", "title": "t", key: "1"}), (400, "invalid_input"))

    def test_update_needs_an_id_and_a_change(self):
        req = post(self.cfg, "ga4", "update_annotation", {"annotation_id": "456", "title": "neu", "color": "RED"})
        self.assertEqual(req.params, {"annotation_id": "456", "title": "[JP] neu", "color": "RED"})
        for body in (
            {"annotation_id": "456"},
            {"title": "x"},
            {"annotation_id": "properties/999/reportingDataAnnotations/1", "title": "x"},
            {"annotation_id": "../1", "title": "x"},
            {"annotation_id": "4/5", "title": "x"},
            {"annotation_id": "", "title": "x"},
            {"annotation_id": "1" * 65, "title": "x"},
            {"annotation_id": 456, "title": "x"},
            {"annotation_id": "456", "date": "2026-10-22/2026-10-23"},
        ):
            with self.subTest(body=body):
                self.assertEqual(code_of(self.cfg, "ga4", "update_annotation", body), (400, "invalid_input"))

    def test_body_builder_creates_google_shape(self):
        body = annotation_body({"date": "2026-10-22", "title": "[JP] x", "color": "BLUE", "description": "d"})
        self.assertEqual(
            body,
            {"title": "[JP] x", "color": "BLUE", "description": "d", "annotationDate": {"year": 2026, "month": 10, "day": 22}},
        )
        self.assertNotIn("annotationDateRange", body)


class ChannelGroup(unittest.TestCase):
    def setUp(self):
        self.cfg = Config.from_dict(fixture())

    def test_hosts_are_a_bounded_hostname_list(self):
        req = post(self.cfg, "ga4", "create_channel_group", {"source_hosts": "chatgpt.com, claude.ai,perplexity.ai"})
        self.assertEqual(req.params["source_hosts"], ["chatgpt.com", "claude.ai", "perplexity.ai"])
        many = ",".join("h%d.example" % i for i in range(20))
        self.assertEqual(len(post(self.cfg, "ga4", "create_channel_group", {"source_hosts": many}).params["source_hosts"]), 20)
        for bad in (
            many + ",h20.example",
            "",
            ",",
            "chatgpt.com,,claude.ai",
            "chatgpt.com,chatgpt.com",
            "https://chatgpt.com",
            "chatgpt.com/path",
            "chat gpt.com",
            "CHATGPT.com",
            "localhost",
            ".chatgpt.com",
            "chatgpt..com",
            "x" * 254 + ".com",
            "chatgpt.com;claude.ai",
            ["chatgpt.com"],
            5,
        ):
            with self.subTest(bad=str(bad)[:30]):
                self.assertEqual(code_of(self.cfg, "ga4", "create_channel_group", {"source_hosts": bad}), (400, "invalid_input"))

    def test_primary_name_rules_and_foreign_keys_are_rejected(self):
        # SEC P1: primary is no parameter at all; no free rules, name or property.
        for key, value in (("primary", True), ("primary", False), ("display_name", "x"), ("grouping_rule", []), ("rules", "x"), ("property", "1"), ("name", "properties/1/channelGroups/2"), ("description", "x")):
            with self.subTest(key=key, value=value):
                body = {"source_hosts": "chatgpt.com", key: value}
                self.assertEqual(code_of(self.cfg, "ga4", "create_channel_group", body), (400, "invalid_input"))

    def test_update_needs_a_numeric_group_id(self):
        req = post(self.cfg, "ga4", "update_channel_group", {"channel_group_id": "789", "source_hosts": "chatgpt.com"})
        self.assertEqual(req.params, {"channel_group_id": "789", "source_hosts": ["chatgpt.com"]})
        for gid in ("", "../1", "7/8", "properties/1/channelGroups/2", "a" * 65, 789, None):
            with self.subTest(gid=gid):
                self.assertEqual(code_of(self.cfg, "ga4", "update_channel_group", {"channel_group_id": gid, "source_hosts": "chatgpt.com"}), (400, "invalid_input"))

    def test_rules_carry_the_standard_channels_and_ai_before_referral(self):
        # SEC P1 / issue: a custom group classifies every session; without the
        # standard channels everything else would be Unassigned.
        hosts = ["chatgpt.com", "claude.ai"]
        rules = channel_group_rules(self.cfg, hosts)
        names = [r["displayName"] for r in rules]
        self.assertEqual(names[0], "KI-Assistenten")
        self.assertEqual(names[1:], list(STANDARD_CHANNELS))
        self.assertLess(names.index("KI-Assistenten"), names.index("Referral"))
        for required in ("Direct", "Organic Search", "Paid Search", "Referral", "Email", "Organic Social", "Paid Social", "Display", "Affiliates"):
            self.assertIn(required, names)
        self.assertLessEqual(len(rules), 50)
        self.assertEqual(len(names), len(set(names)))
        ai = rules[0]["expression"]
        self.assertEqual(
            ai,
            {
                "andGroup": {
                    "filterExpressions": [
                        {
                            "orGroup": {
                                "filterExpressions": [
                                    {"filter": {"fieldName": "eachScopeSource", "inListFilter": {"values": hosts}}}
                                ]
                            }
                        }
                    ]
                }
            },
        )
        referral = rules[names.index("Referral")]["expression"]
        leaf = referral["andGroup"]["filterExpressions"][0]["orGroup"]["filterExpressions"][0]["filter"]
        self.assertEqual(leaf, {"fieldName": "eachScopeDefaultChannelGroup", "stringFilter": {"matchType": "EXACT", "value": "Referral"}})

    def test_create_body_is_never_primary_and_has_the_configured_name(self):
        body = channel_group_body(self.cfg, ["claude.ai"])
        self.assertIs(body["primary"], False)
        self.assertEqual(body["displayName"], "KI-Assistenten")
        self.assertLessEqual(len(body["displayName"]), 80)
        self.assertLessEqual(len(body.get("description", "")), 256)
        self.assertEqual(body["groupingRule"], channel_group_rules(self.cfg, ["claude.ai"]))
        raw = fixture()
        raw["ga4_channel_group_name"] = "AI Traffic"
        self.assertEqual(channel_group_body(Config.from_dict(raw), ["claude.ai"])["displayName"], "AI Traffic")

    def test_hosts_come_back_out_of_our_own_rule_only(self):
        rules = channel_group_rules(self.cfg, ["chatgpt.com", "claude.ai"])
        self.assertEqual(hosts_from_rule(rules[0]), ["chatgpt.com", "claude.ai"])
        self.assertIsNone(hosts_from_rule(rules[1]))
        self.assertIsNone(hosts_from_rule({"displayName": "x", "expression": {"filter": {}}}))
        self.assertIsNone(hosts_from_rule("junk"))


class DryRunAndBody(unittest.TestCase):
    def setUp(self):
        self.cfg = Config.from_dict(fixture())

    def test_dry_run_must_be_a_json_boolean(self):
        base = {"sitemap_url": "https://www.example.test/sitemap.xml"}
        self.assertTrue(post(self.cfg, "gsc", "submit_sitemap", dict(base, dry_run=True)).dry_run)
        self.assertFalse(post(self.cfg, "gsc", "submit_sitemap", dict(base, dry_run=False)).dry_run)
        for bad in ("true", 1, None, "false"):
            with self.subTest(bad=bad):
                self.assertEqual(code_of(self.cfg, "gsc", "submit_sitemap", dict(base, dry_run=bad)), (400, "invalid_input"))

    def test_body_must_be_one_json_object_without_duplicates(self):
        for raw in (b"", b"[]", b"null", b'"x"', b"{", b'{"sitemap_url":"https://www.example.test/a.xml","sitemap_url":"https://www.example.test/b.xml"}', b"\xff\xfe"):
            with self.subTest(raw=raw[:20]), self.assertRaises(ReportError) as cm:
                parse_post(self.cfg, "gsc", "submit_sitemap", raw)
            self.assertEqual((cm.exception.status, cm.exception.code), (400, "invalid_input"))

    def test_unknown_route_is_not_found(self):
        for source, op in (("gsc", "delete_sitemap"), ("ga4", "delete_annotation"), ("ga4", "archive_channel_group"), ("gsc", "list_sitemaps"), ("ga4", "submit_sitemap")):
            with self.subTest(op=op), self.assertRaises(ReportError) as cm:
                parse_post(self.cfg, source, op, b"{}")
            self.assertEqual((cm.exception.status, cm.exception.code), (404, "route_not_found"))
        with self.assertRaises(ReportError) as cm:
            parse_get(self.cfg, "ga4", "create_annotation", [])
        self.assertEqual(cm.exception.status, 404)

    def test_get_routes_take_no_unknown_query(self):
        for source, op in (("gsc", "list_sitemaps"), ("ga4", "list_annotations"), ("ga4", "list_channel_groups")):
            self.assertIsInstance(parse_get(self.cfg, source, op, []), ManageRequest)
            with self.assertRaises(ReportError) as cm:
                parse_get(self.cfg, source, op, [("property_id", "CANARY")])
            self.assertEqual(cm.exception.status, 400)


class ChangeLine(unittest.TestCase):
    PATTERN = re.compile(
        r"^change ts=(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ) source=(gsc|ga4) op=([a-z_]+) target=(\S+) fields=(\S*) before=(\{.*\}|null) after=(\{.*\}|null)$"
    )

    def payload(self):
        return {
            "schema_version": "1.0",
            "source": "ga4",
            "operation": "update_annotation",
            "state": "applied",
            "dry_run": False,
            "target": "456",
            "changed_fields": ["color", "title"],
            "before": {"annotation_date": "2026-10-22", "color": "BLUE", "title": "[JP] alt", "description": "Beschreibung alt"},
            "after": {"annotation_date": "2026-10-22", "color": "RED", "title": "[JP] neu", "description": "Beschreibung neu"},
            "token": "BRIDGE-CANARY-TOKEN",
            "private_key": "KEY-CANARY",
        }

    def test_fixed_format_with_fixed_type_values_only(self):
        line = change_line("ga4", "update_annotation", self.payload(), now="2026-10-22T10:00:00Z")
        match = self.PATTERN.match(line)
        self.assertIsNotNone(match, line)
        self.assertEqual(match.group(1, 2, 3, 4, 5), ("2026-10-22T10:00:00Z", "ga4", "update_annotation", "456", "color,title"))
        after = json.loads(match.group(7))
        self.assertEqual(after["color"], "RED")
        self.assertEqual(after["annotation_date"], "2026-10-22")
        self.assertNotIn("\n", line)

    def test_free_text_appears_as_length_and_hash_only(self):
        line = change_line("ga4", "update_annotation", self.payload(), now="2026-10-22T10:00:00Z")
        for plain in ("[JP] neu", "[JP] alt", "Beschreibung neu", "Beschreibung alt"):
            self.assertNotIn(plain, line)
        after = json.loads(self.PATTERN.match(line).group(7))
        self.assertEqual(after["title"]["len"], len("[JP] neu"))
        self.assertRegex(after["title"]["sha256"], r"^[0-9a-f]{12}$")
        self.assertEqual(after["description"]["len"], len("Beschreibung neu"))

    def test_canary_token_and_unknown_fields_never_appear(self):
        line = change_line("ga4", "update_annotation", self.payload(), now="2026-10-22T10:00:00Z")
        self.assertNotIn("BRIDGE-CANARY-TOKEN", line)
        self.assertNotIn("KEY-CANARY", line)
        payload = self.payload()
        payload["after"]["secret"] = "FIELD-CANARY"
        payload["target"] = "4 5\nchange forged=1"
        line = change_line("ga4", "update_annotation", payload, now="2026-10-22T10:00:00Z")
        self.assertNotIn("FIELD-CANARY", line)
        self.assertEqual(line.count("\n"), 0)
        self.assertNotIn("forged", line)

    def test_hosts_sitemap_and_null_before(self):
        payload = {
            "operation": "create_channel_group",
            "target": "789",
            "changed_fields": ["source_hosts"],
            "before": None,
            "after": {"channel_group_name": "KI-Assistenten", "source_hosts": ["chatgpt.com", "claude.ai"], "rule_count": 20},
        }
        line = change_line("ga4", "create_channel_group", payload, now="2026-10-22T10:00:00Z")
        match = self.PATTERN.match(line)
        self.assertEqual(match.group(6), "null")
        self.assertEqual(json.loads(match.group(7))["source_hosts"], ["chatgpt.com", "claude.ai"])
        payload = {
            "operation": "submit_sitemap",
            "target": "https://www.example.test/sitemap.xml",
            "changed_fields": ["registered"],
            "before": {"registered": False},
            "after": {"registered": True, "sitemap_url": "https://www.example.test/sitemap.xml"},
        }
        line = change_line("gsc", "submit_sitemap", payload, now="2026-10-22T10:00:00Z")
        self.assertIn("https://www.example.test/sitemap.xml", line)
        self.assertIsNotNone(self.PATTERN.match(line), line)


if __name__ == "__main__":
    unittest.main()
