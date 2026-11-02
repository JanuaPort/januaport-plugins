import json
import unittest
from bing_webmaster import domain
from bing_webmaster.domain import Config, ReportError, Request
from support import config, config_raw, INDEXNOW_KEY, KEY_LOCATION, URL_INFO_OK, FEEDS_OK, QUOTA_OK


def rejects(case, fn, *args, code="invalid_input", status=400, **kw):
    with case.assertRaises(ReportError) as cm:
        fn(*args, **kw)
    case.assertEqual((cm.exception.status, cm.exception.code), (status, code))
    return cm.exception


class ConfigTable(unittest.TestCase):
    def test_valid_configuration(self):
        cfg = config()
        self.assertEqual(cfg.site_url, "https://www.example.test/")
        self.assertEqual(cfg.hosts, ("example.test", "www.example.test"))
        self.assertEqual(cfg.write_cap, 20)
        self.assertEqual(cfg.indexnow.host, "www.example.test")

    def test_indexnow_is_optional(self):
        cfg = Config.from_dict(config_raw(indexnow=None))
        self.assertIsNone(cfg.indexnow)
        raw = config_raw()
        del raw["indexnow"]
        self.assertIsNone(Config.from_dict(raw).indexnow)

    def test_write_cap_can_only_be_lowered(self):
        self.assertEqual(config(write_cap_per_day=5).write_cap, 5)
        self.assertEqual(config(write_cap_per_day=20).write_cap, 20)
        for bad in (0, -1, 21, "20", True, 2.5, None):
            with self.subTest(bad=bad), self.assertRaises(ValueError):
                config(write_cap_per_day=bad)

    def test_invalid_configurations_fail_closed(self):
        bad = {
            "site not https": {"site_url": "http://www.example.test/"},
            "site with path": {"site_url": "https://www.example.test/blog"},
            "site with port": {"site_url": "https://www.example.test:8443/"},
            "site with userinfo": {"site_url": "https://u@www.example.test/"},
            "site host not allowed": {"site_url": "https://other.example.test/"},
            "site missing": {"site_url": None},
            "hosts empty": {"hosts": []},
            "hosts not list": {"hosts": "example.test"},
            "hosts uppercase": {"hosts": ["Example.test"]},
            "hosts with port": {"hosts": ["example.test:443"]},
            "hosts with wildcard": {"hosts": ["*.example.test"]},
            "hosts with scheme": {"hosts": ["https://example.test"]},
            "hosts duplicated": {"hosts": ["example.test", "example.test"]},
            "hosts too many": {"hosts": ["h%d.example.test" % i for i in range(6)]},
            "unknown key": {"api_key": "x"},
            "indexnow key short": {"indexnow": {"key": "short", "key_location": KEY_LOCATION}},
            "indexnow key long": {"indexnow": {"key": "a" * 129, "key_location": "https://www.example.test/" + "a" * 129 + ".txt"}},
            "indexnow key charset": {"indexnow": {"key": "abcdefgh_ij", "key_location": "https://www.example.test/abcdefgh_ij.txt"}},
            "indexnow location http": {"indexnow": {"key": INDEXNOW_KEY, "key_location": KEY_LOCATION.replace("https", "http")}},
            "indexnow location foreign host": {"indexnow": {"key": INDEXNOW_KEY, "key_location": "https://evil.test/" + INDEXNOW_KEY + ".txt"}},
            "indexnow location other file": {"indexnow": {"key": INDEXNOW_KEY, "key_location": "https://www.example.test/other.txt"}},
            "indexnow location query": {"indexnow": {"key": INDEXNOW_KEY, "key_location": KEY_LOCATION + "?x=1"}},
            "indexnow extra field": {"indexnow": {"key": INDEXNOW_KEY, "key_location": KEY_LOCATION, "x": 1}},
            "indexnow not object": {"indexnow": "yes"},
        }
        for name, over in bad.items():
            with self.subTest(name), self.assertRaises(ValueError):
                config(**over)

    def test_not_a_dict_fails(self):
        for bad in (None, [], "x", 1):
            with self.subTest(bad=bad), self.assertRaises(ValueError):
                Config.from_dict(bad)

    def test_repr_hides_the_indexnow_key(self):
        self.assertNotIn(INDEXNOW_KEY, repr(config()))


class UrlValidation(unittest.TestCase):
    OK = (
        "https://www.example.test/",
        "https://www.example.test",
        "https://example.test/atas",
        "https://www.example.test/integrationen/odoo",
        "https://www.example.test/a-b_c.d~e/%C3%A4",
    )
    BAD = {
        "http": "http://www.example.test/",
        "HTTPS scheme": "HTTPS://www.example.test/",
        "scheme relative": "//www.example.test/",
        "foreign host": "https://evil.test/",
        "suffix without dot": "https://evilexample.test/",
        "subdomain of allowed": "https://a.www.example.test/",
        "allowed as subdomain of foreign": "https://www.example.test.evil.test/",
        "uppercase host": "https://WWW.example.test/",
        "userinfo": "https://user@www.example.test/",
        "userinfo password": "https://user:pw@www.example.test/",
        "at sign in path": "https://www.example.test/a@b",
        "at sign trick": "https://evil.test@www.example.test/",
        "port": "https://www.example.test:443/",
        "ipv6": "https://[::1]/",
        "fragment": "https://www.example.test/#x",
        "query": "https://www.example.test/?a=b",
        "backslash": "https://www.example.test\\@evil.test/",
        "space": "https://www.example.test/a b",
        "newline": "https://www.example.test/a\nb",
        "tab": "https://www.example.test/\t",
        "comma": "https://www.example.test/a,b",
        "non ascii": "https://www.example.test/ä",
        "non ascii host": "https://wwü.example.test/",
        "dotdot": "https://www.example.test/a/../b",
        "encoded dotdot": "https://www.example.test/a/%2e%2e/b",
        "encoded control": "https://www.example.test/%00",
        "broken percent": "https://www.example.test/%zz",
        "empty": "",
        "only scheme": "https://",
        "empty host": "https:///x",
        "javascript": "javascript:alert(1)",
        "double slash host": "https://www.example.test//evil.test/",
        "too long": "https://www.example.test/" + "a" * 520,
        "not a string": 5,
        "none": None,
    }

    def test_accepts_exact_allowlisted_https_urls(self):
        cfg = config()
        for url in self.OK:
            with self.subTest(url):
                self.assertEqual(domain.validate_url(cfg, url), url)

    def test_rejects_everything_else_with_a_fixed_code_and_no_echo(self):
        cfg = config()
        for name, url in self.BAD.items():
            with self.subTest(name):
                exc = rejects(self, domain.validate_url, cfg, url)
                self.assertNotIn("evil", str(exc.args) + exc.code)

    def test_sitemap_must_be_an_xml_file(self):
        cfg = config()
        self.assertEqual(
            domain.validate_url(cfg, "https://www.example.test/sitemap.xml", sitemap=True),
            "https://www.example.test/sitemap.xml",
        )
        for bad in (
            "https://www.example.test/sitemap.txt",
            "https://www.example.test/",
            "https://www.example.test/sitemap.xml/",
            "https://www.example.test/sitemap.xml?x=1",
            "https://evil.test/sitemap.xml",
            "http://www.example.test/sitemap.xml",
        ):
            with self.subTest(bad):
                rejects(self, domain.validate_url, cfg, bad, sitemap=True)


class BodyParsing(unittest.TestCase):
    def post(self, source, op, body, cfg=None):
        raw = body if isinstance(body, bytes) else json.dumps(body).encode()
        return domain.parse_post(cfg or config(), source, op, raw)

    def test_submit_urls(self):
        req = self.post("bing", "submit_urls", {"urls": "https://www.example.test/a,https://example.test/b", "dry_run": True})
        self.assertEqual(
            req,
            Request("bing", "submit_urls", write=True, dry_run=True, urls=("https://www.example.test/a", "https://example.test/b")),
        )
        self.assertFalse(self.post("bing", "submit_urls", {"urls": "https://www.example.test/a"}).dry_run)

    def test_duplicates_collapse_in_order(self):
        req = self.post("bing", "submit_urls", {"urls": "https://www.example.test/a,https://www.example.test/b,https://www.example.test/a"})
        self.assertEqual(req.urls, ("https://www.example.test/a", "https://www.example.test/b"))

    def test_at_most_one_hundred_urls(self):
        many = lambda n: ",".join("https://www.example.test/p%d" % i for i in range(n))
        self.assertEqual(len(self.post("bing", "submit_urls", {"urls": many(100)}).urls), 100)
        rejects(self, self.post, "bing", "submit_urls", {"urls": many(101)}, code="too_many_urls")
        rejects(self, self.post, "indexnow", "submit_urls", {"urls": many(101)}, code="too_many_urls")

    def test_submit_sitemap(self):
        req = self.post("bing", "submit_sitemap", {"sitemap_url": "https://www.example.test/sitemap.xml"})
        self.assertEqual(req.url, "https://www.example.test/sitemap.xml")
        self.assertTrue(req.write)

    def test_indexnow_urls_must_share_the_host_of_the_key_file(self):
        ok = self.post("indexnow", "submit_urls", {"urls": "https://www.example.test/a"})
        self.assertEqual(ok.source, "indexnow")
        rejects(self, self.post, "indexnow", "submit_urls", {"urls": "https://example.test/a"})
        rejects(self, self.post, "indexnow", "submit_urls", {"urls": "https://www.example.test/a,https://example.test/b"})

    def test_rejections_before_any_vendor_call(self):
        bad = {
            "not json": b"{",
            "not an object": b"[]",
            "duplicate key": b'{"urls":"https://www.example.test/a","urls":"https://www.example.test/b"}',
            "unknown key": {"urls": "https://www.example.test/a", "site_url": "https://evil.test/"},
            "site override": {"urls": "https://www.example.test/a", "siteUrl": "https://evil.test/"},
            "urls missing": {},
            "urls empty": {"urls": ""},
            "urls empty element": {"urls": "https://www.example.test/a,,https://www.example.test/b"},
            "urls list": {"urls": ["https://www.example.test/a"]},
            "urls foreign": {"urls": "https://www.example.test/a,https://evil.test/b"},
            "dry_run string": {"urls": "https://www.example.test/a", "dry_run": "true"},
            "dry_run int": {"urls": "https://www.example.test/a", "dry_run": 1},
            "not utf8": b'{"urls":"\xff"}',
            "nan": b'{"urls":"https://www.example.test/a","dry_run":NaN}',
        }
        for name, body in bad.items():
            with self.subTest(name):
                rejects(self, self.post, "bing", "submit_urls", body)

    def test_sitemap_rejections(self):
        for body in (
            {},
            {"sitemap_url": "https://evil.test/sitemap.xml"},
            {"sitemap_url": "https://www.example.test/sitemap.xml", "urls": "x"},
            {"sitemap_url": ["https://www.example.test/sitemap.xml"]},
        ):
            with self.subTest(body):
                rejects(self, self.post, "bing", "submit_sitemap", body)

    def test_get_parsing(self):
        cfg = config()
        req = domain.parse_get(cfg, "bing", "url_info", [("url", "https://www.example.test/a")])
        self.assertEqual(req, Request("bing", "url_info", url="https://www.example.test/a"))
        self.assertEqual(domain.parse_get(cfg, "bing", "sitemaps", []).operation, "sitemaps")
        self.assertEqual(domain.parse_get(cfg, "bing", "submission_quota", []).operation, "submission_quota")

    def test_get_rejections(self):
        cfg = config()
        for query in (
            [],
            [("url", "https://evil.test/")],
            [("url", "https://www.example.test:8443/")],
            [("url", "https://user@www.example.test/")],
            [("url", "https://www.example.test/#f")],
            [("url", "https://www.example.test/a"), ("url", "https://www.example.test/b")],
            [("url", "https://www.example.test/a"), ("siteUrl", "https://evil.test/")],
            [("siteUrl", "https://evil.test/")],
        ):
            with self.subTest(query):
                rejects(self, domain.parse_get, cfg, "bing", "url_info", query)
        rejects(self, domain.parse_get, cfg, "bing", "sitemaps", [("siteUrl", "https://evil.test/")])
        rejects(self, domain.parse_get, cfg, "bing", "submission_quota", [("x", "1")])


class BingDates(unittest.TestCase):
    def test_table(self):
        cases = {
            "/Date(1315349995266-0700)/": "2011-09-06T22:59:55Z",
            "/Date(1316213995266-0700)/": "2011-09-16T22:59:55Z",
            "/Date(1315349995266+0200)/": "2011-09-06T22:59:55Z",
            "/Date(1315349995266)/": "2011-09-06T22:59:55Z",
            "/Date(0)/": None,
            "/Date(-62135596800000)/": None,
            "/Date(-1)/": None,
        }
        for raw, want in cases.items():
            with self.subTest(raw):
                self.assertEqual(domain.parse_bing_date(raw), want)

    def test_none_is_none(self):
        self.assertIsNone(domain.parse_bing_date(None))

    def test_malformed_values_are_a_shape_error(self):
        for bad in ("2011-09-06", "/Date(abc)/", "/Date(1)", "Date(1)", "/Date(1e3)/", "/Date(99999999999999999999)/", "/Date(1-07)/", 5, [], {}, True, "/Date(1315349995266-0700)/x"):
            with self.subTest(bad=bad):
                rejects(self, domain.parse_bing_date, bad, code="unexpected_source_shape", status=502)


class Projections(unittest.TestCase):
    def test_url_info_keeps_only_the_positive_list_and_our_own_url(self):
        out = domain.project_url_info("https://www.example.test/a", URL_INFO_OK)
        self.assertEqual(
            out,
            {
                "state": "ok",
                "item": {
                    "url": "https://www.example.test/a",
                    "discovery_date": "2011-09-06T22:59:55Z",
                    "last_crawled_date": "2011-09-16T22:59:55Z",
                    "http_status": 200,
                    "document_size": 2048,
                    "is_page": True,
                },
            },
        )
        self.assertNotIn("must-not-appear", json.dumps(out))

    def test_url_info_without_data(self):
        self.assertEqual(domain.project_url_info("https://www.example.test/a", {"d": None}), {"state": "no_data", "item": None})

    def test_url_info_shape_errors(self):
        good = URL_INFO_OK["d"]
        variants = {
            "no d": {},
            "d is list": {"d": []},
            "http_status string": {"d": dict(good, HttpStatus="200")},
            "http_status bool": {"d": dict(good, HttpStatus=True)},
            "http_status negative": {"d": dict(good, HttpStatus=-1)},
            "size float": {"d": dict(good, DocumentSize=1.5)},
            "is_page int": {"d": dict(good, IsPage=1)},
            "date garbage": {"d": dict(good, DiscoveryDate="yesterday")},
            "missing field": {"d": {k: v for k, v in good.items() if k != "HttpStatus"}},
            "not a dict": [],
        }
        for name, raw in variants.items():
            with self.subTest(name):
                rejects(self, domain.project_url_info, "https://www.example.test/a", raw, code="unexpected_source_shape", status=502)

    def test_feeds_positive_list(self):
        out = domain.project_feeds(FEEDS_OK)
        self.assertEqual(out["state"], "ok")
        self.assertEqual(
            out["items"],
            [
                {
                    "url": "https://www.example.test/sitemap.xml",
                    "type": "Sitemap",
                    "status": "Success",
                    "url_count": 1023,
                    "file_size": 1024,
                    "compressed": False,
                    "submitted": "2011-09-16T22:59:55Z",
                    "last_crawled": "2011-09-11T22:59:55Z",
                }
            ],
        )
        self.assertEqual(out["quality"], {"truncated": False, "returned_items": 1})
        self.assertNotIn("must-not-appear", json.dumps(out))

    def test_feeds_empty_and_capped(self):
        self.assertEqual(domain.project_feeds({"d": []})["state"], "no_rows")
        item = FEEDS_OK["d"][0]
        out = domain.project_feeds({"d": [item] * 150})
        self.assertEqual(len(out["items"]), 100)
        self.assertEqual(out["quality"], {"truncated": True, "returned_items": 100})

    def test_feeds_shape_errors(self):
        item = FEEDS_OK["d"][0]
        for name, raw in {
            "d is dict": {"d": {}},
            "no d": {},
            "item not dict": {"d": ["x"]},
            "url missing": {"d": [{k: v for k, v in item.items() if k != "Url"}]},
            "url_count string": {"d": [dict(item, UrlCount="5")]},
            "status long": {"d": [dict(item, Status="x" * 100)]},
            "status control": {"d": [dict(item, Status="a\nb")]},
            "url not str": {"d": [dict(item, Url=5)]},
            "url too long": {"d": [dict(item, Url="https://x/" + "a" * 600)]},
        }.items():
            with self.subTest(name):
                rejects(self, domain.project_feeds, raw, code="unexpected_source_shape", status=502)

    def test_quota(self):
        self.assertEqual(domain.project_quota(QUOTA_OK), {"state": "ok", "item": {"daily_quota": 500, "monthly_quota": 10000}})
        for bad in ({"d": None}, {"d": {"DailyQuota": "5", "MonthlyQuota": 1}}, {"d": {"DailyQuota": 5}}, {"d": {"DailyQuota": -1, "MonthlyQuota": 1}}, {}):
            with self.subTest(bad):
                rejects(self, domain.project_quota, bad, code="unexpected_source_shape", status=502)

    def test_envelope_and_size_cap(self):
        env = domain.envelope("bing", "url_info", "ok", item=None, now=lambda: "2026-11-02T10:00:00Z")
        self.assertEqual(env["schema_version"], "1.0")
        self.assertEqual((env["source"], env["operation"], env["fetched_at"]), ("bing", "url_info", "2026-11-02T10:00:00Z"))
        with self.assertRaises(ReportError):
            domain.encode_response({"x": "A" * (512 * 1024)})

    def test_write_envelope_has_every_field_in_every_state(self):
        req = Request("bing", "submit_urls", write=True, dry_run=True, urls=("https://www.example.test/a",))
        for state in ("preview", "applied"):
            env = domain.write_envelope(req, state, now=lambda: "t")
            self.assertEqual(
                sorted(env),
                sorted(["schema_version", "source", "operation", "state", "dry_run", "target", "submitted_count", "urls", "quota_before", "vendor_status", "fetched_at"]),
            )
            self.assertEqual(env["submitted_count"], 1)
        sitemap = domain.write_envelope(Request("bing", "submit_sitemap", write=True, url="https://www.example.test/sitemap.xml"), "applied", now=lambda: "t")
        self.assertEqual(sitemap["target"], "https://www.example.test/sitemap.xml")
        self.assertEqual(sitemap["urls"], [])


if __name__ == "__main__":
    unittest.main()
