"""Free periods of 1 to 90 days (JanuaPort/januaport#1379, part B); synthetic, no network.

`period_days` is a decimal string without leading zero, 1 through 90, default 28, for every
report of both sources except get_status. The last day stays "source today minus three days".
"""

import datetime as dt
import unittest

from google_reporting.domain import Config, ReportError, parse_request
from test_contract import fixture

UTC = dt.timezone.utc
# 2026-03-30 00:30 UTC: Los Angeles still 03-29 (latest 03-26), Berlin already 03-30 (latest 03-27).
NOW = dt.datetime(2026, 3, 30, 0, 30, tzinfo=UTC)
REPORTS = (
    ("gsc", "performance_overview", "2026-03-26"),
    ("gsc", "query_summary", "2026-03-26"),
    ("gsc", "page_summary", "2026-03-26"),
    ("ga4", "traffic_summary", "2026-03-27"),
    ("ga4", "landing_pages", "2026-03-27"),
    ("ga4", "key_events", "2026-03-27"),
)


def code(call):
    with unittest.TestCase().assertRaises(ReportError) as ctx:
        call()
    return ctx.exception.status, ctx.exception.code


class Periods(unittest.TestCase):
    def setUp(self):
        self.cfg = Config.from_dict(fixture())

    def test_valid_periods_are_inclusive_and_end_at_the_latest_day(self):
        for source, report, latest in REPORTS:
            end = dt.date.fromisoformat(latest)
            for days in (1, 2, 7, 28, 29, 89, 90):
                with self.subTest(source=source, report=report, days=days):
                    req = parse_request(
                        self.cfg, source, report, [("period_days", str(days))], NOW
                    )
                    self.assertEqual(req.end_date, latest)
                    self.assertEqual(
                        req.start_date, (end - dt.timedelta(days=days - 1)).isoformat()
                    )

    def test_default_is_28_days_for_every_report(self):
        for source, report, latest in REPORTS:
            with self.subTest(source=source, report=report):
                req = parse_request(self.cfg, source, report, [], NOW)
                end = dt.date.fromisoformat(latest)
                self.assertEqual(
                    req.start_date, (end - dt.timedelta(days=27)).isoformat()
                )

    def test_invalid_periods_are_invalid_input_for_every_report(self):
        bad = (
            "0",
            "91",
            "100",
            "07",
            "007",
            "7.0",
            "",
            "-1",
            "1e1",
            "+7",
            " 7",
            "7 ",
            "7\n",
            "0x10",
            "1_0",
            "seven",
            "٧",  # Arabic-Indic digit seven
            "７",  # full-width digit seven
            "9" * 40,
        )
        for source, report, _ in REPORTS:
            for value in bad:
                with self.subTest(source=source, report=report, value=value):
                    self.assertEqual(
                        code(
                            lambda: parse_request(
                                self.cfg, source, report, [("period_days", value)], NOW
                            )
                        ),
                        (400, "invalid_input"),
                    )

    def test_get_status_takes_no_period(self):
        for source in ("gsc", "ga4"):
            with self.subTest(source=source):
                self.assertEqual(
                    code(
                        lambda: parse_request(
                            self.cfg, source, "get_status", [("period_days", "7")], NOW
                        )
                    ),
                    (400, "invalid_input"),
                )

    def test_period_given_twice_is_invalid(self):
        pairs = [("period_days", "7"), ("period_days", "7")]
        self.assertEqual(
            code(lambda: parse_request(self.cfg, "gsc", "page_summary", pairs, NOW)),
            (400, "invalid_input"),
        )


class LatestDay(unittest.TestCase):
    """The data ends three days before today in the source zone; the distance stays 3 for GSC."""

    def setUp(self):
        self.cfg = Config.from_dict(fixture())

    def test_exactly_today_minus_three_is_accepted_and_minus_two_is_not(self):
        for source, report, latest in REPORTS:
            day = dt.date.fromisoformat(latest)
            with self.subTest(source=source, report=report):
                ok = parse_request(
                    self.cfg, source, report, [("end_date", day.isoformat())], NOW
                )
                self.assertEqual(ok.end_date, latest)
                for late in (1, 2, 30):
                    value = (day + dt.timedelta(days=late)).isoformat()
                    self.assertEqual(
                        code(
                            lambda: parse_request(
                                self.cfg, source, report, [("end_date", value)], NOW
                            )
                        ),
                        (400, "end_date_not_available"),
                    )

    def test_the_day_change_follows_the_zone_of_the_source(self):
        # Los Angeles midnight of 2026-03-30 is 07:00 UTC; Berlin midnight is 22:00 UTC the day before.
        table = (
            ("gsc", "page_summary", dt.datetime(2026, 3, 30, 6, 59, tzinfo=UTC), "2026-03-26"),
            ("gsc", "page_summary", dt.datetime(2026, 3, 30, 7, 0, tzinfo=UTC), "2026-03-27"),
            ("ga4", "landing_pages", dt.datetime(2026, 3, 29, 21, 59, tzinfo=UTC), "2026-03-26"),
            ("ga4", "landing_pages", dt.datetime(2026, 3, 29, 22, 0, tzinfo=UTC), "2026-03-27"),
        )
        for source, report, now, latest in table:
            with self.subTest(source=source, now=now.isoformat()):
                req = parse_request(self.cfg, source, report, [], now)
                self.assertEqual(req.end_date, latest)
                after = (dt.date.fromisoformat(latest) + dt.timedelta(days=1)).isoformat()
                self.assertEqual(
                    code(
                        lambda: parse_request(
                            self.cfg, source, report, [("end_date", after)], now
                        )
                    ),
                    (400, "end_date_not_available"),
                )

    def test_ga4_takes_the_zone_from_the_configuration(self):
        raw = fixture()
        raw["ga4_time_zone"] = "Pacific/Auckland"
        cfg = Config.from_dict(raw)
        # 2026-03-30 11:30 UTC is already 03-31 in Auckland (UTC+13) but 04:30 on 03-30 in Los Angeles.
        later = dt.datetime(2026, 3, 30, 11, 30, tzinfo=UTC)
        self.assertEqual(
            parse_request(cfg, "ga4", "traffic_summary", [], later).end_date, "2026-03-28"
        )
        self.assertEqual(
            parse_request(cfg, "gsc", "page_summary", [], later).end_date, "2026-03-27"
        )

    def test_a_period_before_the_range_start_of_the_calendar_is_invalid_not_a_crash(self):
        self.assertEqual(
            code(
                lambda: parse_request(
                    self.cfg,
                    "gsc",
                    "page_summary",
                    [("end_date", "0001-01-02"), ("period_days", "90")],
                    NOW,
                )
            ),
            (400, "invalid_input"),
        )


if __name__ == "__main__":
    unittest.main()
