"""Path cap of the operator configuration (JanuaPort/januaport#1176); synthetic, no network."""

import re
import unittest

from google_reporting.domain import MAX_REGEX, Config, build_query, parse_request, regex_for

from test_contract import fixture


def config_with(paths):
    raw = fixture()
    third = len(paths) // 3
    raw["segments"] = {
        "atas": paths[:third],
        "gateway": paths[third : 2 * third],
        "shared": paths[2 * third :],
    }
    raw["gsc_urls"] = {p: ["https://www.example.test" + p] for p in paths}
    return raw


def short_paths(n):
    return ["/p%d" % i for i in range(n)]


class PathCap(unittest.TestCase):
    def test_hundred_exact_paths_load(self):
        cfg = Config.from_dict(config_with(short_paths(100)))
        self.assertEqual(sum(len(cfg.paths(s)) for s in ("atas", "gateway", "shared")), 100)

    def test_hundred_and_one_paths_rejected(self):
        raw = config_with(short_paths(101))
        urls = [u for values in raw["gsc_urls"].values() for u in values]
        # The cap itself rejects, not the regex limit.
        self.assertLessEqual(len(regex_for(urls)), MAX_REGEX)
        with self.assertRaises(ValueError):
            Config.from_dict(raw)

    def test_regex_limit_stays_fail_closed(self):
        long_paths = ["/integrationen/%s-%s" % ("x" * 90, i) for i in range(60)]
        raw = config_with(long_paths)
        urls = [u for values in raw["gsc_urls"].values() for u in values]
        self.assertGreater(len(regex_for(urls)), MAX_REGEX)
        with self.assertRaises(ValueError):
            Config.from_dict(raw)

    def test_segment_filter_lists_every_path_exactly(self):
        paths = short_paths(100)
        cfg = Config.from_dict(config_with(paths))
        req = parse_request(cfg, "gsc", "page_summary", [("segment", "shared")])
        expression = build_query(cfg, req)["dimensionFilterGroups"][0]["filters"][0]["expression"]
        for p in cfg.paths("shared"):
            self.assertTrue(re.search(expression, "https://www.example.test" + p))
        # Exact paths only: no prefix match on a configured path.
        self.assertFalse(re.search(expression, "https://www.example.test/p66/sub"))


if __name__ == "__main__":
    unittest.main()
