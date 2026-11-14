"""Fixed reports and fail-closed validation; no HTTP or Google dependency."""

from dataclasses import dataclass
from datetime import date, datetime, timedelta, timezone
from zoneinfo import ZoneInfo
import json
import math
import re
from urllib.parse import urlsplit

MAX_RESPONSE = 512 * 1024
MAX_REGEX = 4096
MAX_PATHS = 100
MAX_SITES = 5
DEFAULT_SITE = "default"
SITE_HEADER = "X-Google-Reporting-Site"
SITE_NAME = re.compile(r"[a-z][a-z0-9-]{0,31}")
SEGMENT_NAME = re.compile(r"[a-z][a-z0-9-]{0,31}")
ALL_PUBLIC = "all_public"
MAX_SEGMENTS = 8
MAX_PERIOD = 90
PERIOD_DAYS = re.compile(r"[1-9][0-9]?")
REPORTS = {
    "gsc": {
        "get_status",
        "performance_overview",
        "query_summary",
        "page_summary",
        "page_compare",
    },
    "ga4": {"get_status", "traffic_summary", "landing_pages", "key_events"},
}


class ReportError(Exception):
    def __init__(self, status, code):
        self.status, self.code = status, code
        super().__init__(code)


def require(ok):
    if not ok:
        raise ValueError("invalid_configuration")


def public_path(path):
    return (
        isinstance(path, str)
        and len(path) <= 512
        and bool(re.fullmatch(r"/[A-Za-z0-9/_~.\-]*", path))
        and "//" not in path
        and all(p not in (".", "..") for p in path.split("/"))
    )


@dataclass(frozen=True)
class Config:
    definition_version: str
    gsc_property: str
    ga4_property: str
    ga4_time_zone: str
    hosts: list
    segments: dict
    gsc_urls: dict
    query_enabled: bool = False
    query_min_impressions: int = 10
    ga4_min_group_size: int = 10
    key_events: dict | None = None
    ga4_channel_group_name: str = "KI-Assistenten"
    site: str = DEFAULT_SITE

    @classmethod
    def from_dict(cls, raw, site=DEFAULT_SITE):
        """One site's configuration; `site` is never part of the raw values (a stray key fails)."""
        try:
            cfg = cls(**raw, site=site)
            require(bool(re.fullmatch(r"[A-Za-z0-9._-]{1,64}", cfg.definition_version)))
            require(
                isinstance(cfg.hosts, list)
                and 1 <= len(cfg.hosts) <= 5
                and len(set(cfg.hosts)) == len(cfg.hosts)
            )
            require(
                all(
                    isinstance(h, str)
                    and re.fullmatch(r"[a-z0-9]+(?:[.-][a-z0-9]+)*", h)
                    and "." in h
                    for h in cfg.hosts
                )
            )
            require(
                isinstance(cfg.gsc_property, str)
                and (
                    cfg.gsc_property.startswith("sc-domain:")
                    or cfg.gsc_property.startswith("https://")
                )
                and len(cfg.gsc_property) <= 512
            )
            if cfg.gsc_property.startswith("https://"):
                u = urlsplit(cfg.gsc_property)
                require(
                    u.hostname in cfg.hosts
                    and not u.query
                    and not u.fragment
                    and not u.username
                    and not u.port
                )
            else:
                domain = cfg.gsc_property.removeprefix("sc-domain:")
                require(all(h == domain or h.endswith("." + domain) for h in cfg.hosts))
            require(
                isinstance(cfg.ga4_property, str)
                and bool(re.fullmatch(r"[1-9][0-9]{0,19}", cfg.ga4_property))
            )
            ZoneInfo(cfg.ga4_time_zone)
            require(type(cfg.query_enabled) is bool)
            require(
                isinstance(cfg.ga4_channel_group_name, str)
                and bool(re.fullmatch(r"[^\x00-\x1f\x7f]{1,80}", cfg.ga4_channel_group_name))
            )
            require(
                type(cfg.query_min_impressions) is int
                and 10 <= cfg.query_min_impressions <= 1000000
            )
            require(
                type(cfg.ga4_min_group_size) is int
                and 1 <= cfg.ga4_min_group_size <= 1000000
            )
            require(
                isinstance(cfg.segments, dict)
                and 1 <= len(cfg.segments) <= MAX_SEGMENTS
                # The pattern has no underscore, so the reserved all_public cannot be a name.
                and all(
                    isinstance(name, str) and SEGMENT_NAME.fullmatch(name)
                    for name in cfg.segments
                )
            )
            paths = []
            for group in cfg.segments.values():
                require(isinstance(group, list) and all(public_path(p) for p in group))
                paths.extend(group)
            require(1 <= len(paths) <= MAX_PATHS and len(set(paths)) == len(paths))
            require(set(cfg.gsc_urls) == set(paths))
            urls = []
            for p, values in cfg.gsc_urls.items():
                require(isinstance(values, list) and 1 <= len(values) <= len(cfg.hosts))
                for value in values:
                    u = urlsplit(value)
                    require(
                        u.scheme == "https"
                        and u.hostname in cfg.hosts
                        and u.netloc == u.hostname
                        and u.path == p
                        and not u.query
                        and not u.fragment
                    )
                    if cfg.gsc_property.startswith("https://"):
                        require(value.startswith(cfg.gsc_property))
                    urls.append(value)
            require(len(urls) == len(set(urls)))
            require(len(regex_for(urls)) <= MAX_REGEX)
            if cfg.key_events is not None:
                require(
                    set(cfg.key_events)
                    == {
                        "names",
                        "meaning",
                        "valid_from",
                        "valid_to",
                        "consent_coverage",
                    }
                )
                events = cfg.key_events
                require(
                    isinstance(events["names"], list)
                    and 1 <= len(events["names"]) <= 20
                    and len(set(events["names"])) == len(events["names"])
                )
                require(
                    all(
                        isinstance(e, str)
                        and re.fullmatch(r"[A-Za-z][A-Za-z0-9_]{0,39}", e)
                        for e in events["names"]
                    )
                )
                require(
                    all(
                        isinstance(events[k], str) and 1 <= len(events[k]) <= 512
                        for k in ("meaning", "consent_coverage")
                    )
                )
                require(iso_date(events["valid_from"]) <= iso_date(events["valid_to"]))
            return cfg
        except (TypeError, KeyError, ValueError, AttributeError, OverflowError):
            raise ValueError("invalid_configuration") from None

    def paths(self, segment):
        return (
            [p for values in self.segments.values() for p in values]
            if segment == ALL_PUBLIC
            else self.segments[segment]
        )


@dataclass(frozen=True)
class Sites:
    """The configured sites and the one place that turns the header into a site (#1379).

    Single form (a plain config) is the site `default`; the list form `sites` names each site
    and always needs the header, also with one entry. Nothing here falls back to a first site.
    """

    listed: bool
    by_name: dict

    @classmethod
    def single(cls, cfg):
        return cls(False, {cfg.site: cfg})

    @classmethod
    def from_json(cls, text):
        """The configuration file: an object key that appears twice is refused, because
        json.loads would silently keep the last one (a doubled segment or site setting)."""
        try:
            return cls.from_dict(json.loads(text, object_pairs_hook=unique_keys))
        except ValueError:
            raise ValueError("invalid_configuration") from None

    @classmethod
    def from_dict(cls, raw):
        try:
            if not isinstance(raw, dict) or "sites" not in raw:
                return cls.single(Config.from_dict(raw))
            require(set(raw) == {"sites"})
            entries = raw["sites"]
            require(isinstance(entries, list) and 1 <= len(entries) <= MAX_SITES)
            by_name = {}
            for entry in entries:
                require(isinstance(entry, dict) and isinstance(entry.get("name"), str))
                name = entry["name"]
                require(
                    bool(SITE_NAME.fullmatch(name))
                    and name != DEFAULT_SITE
                    and name not in by_name
                )
                by_name[name] = Config.from_dict(
                    {k: v for k, v in entry.items() if k != "name"}, site=name
                )
            configs = list(by_name.values())
            for values in (
                [h for c in configs for h in c.hosts],
                [c.gsc_property for c in configs],
                [c.ga4_property for c in configs],
            ):
                require(len(set(values)) == len(values))
            return cls(True, by_name)
        except (TypeError, KeyError, ValueError, AttributeError):
            raise ValueError("invalid_configuration") from None

    def config(self, name):
        cfg = self.by_name.get(name)
        if cfg is None:
            raise ReportError(503, "configuration_invalid")
        return cfg

    def resolve(self, values):
        """The site named by the header values: exact bytes, exactly one value, never an echo."""
        if not values:
            if not self.listed:
                return self.by_name[DEFAULT_SITE]
            raise ReportError(400, "site_required")
        name = values[0] if len(values) == 1 else ""
        cfg = self.by_name.get(name) if SITE_NAME.fullmatch(name) else None
        if cfg is None:
            raise ReportError(400, "site_unknown")
        return cfg


def unique_keys(pairs):
    keys = [k for k, _ in pairs]
    if len(set(keys)) != len(keys):
        raise ValueError("duplicate_key")
    return dict(pairs)


def iso_date(value):
    if not isinstance(value, str) or not re.fullmatch(r"\d{4}-\d{2}-\d{2}", value):
        raise ValueError()
    return date.fromisoformat(value)


def final_date(value, latest):
    """A date the source has final data for: well-formed but later is its own fixed code (#1150)."""
    day = iso_date(value)
    if day > latest:
        raise ReportError(400, "end_date_not_available")
    return day


def regex_for(urls):
    return "^(" + "|".join(re.escape(u) for u in urls) + ")$"


@dataclass(frozen=True)
class Request:
    source: str
    report: str
    start_date: str
    end_date: str
    time_zone: str
    segment: str
    paths: list
    page_path: str | None
    limit: int
    site: str = DEFAULT_SITE
    compare_start_date: str | None = None
    compare_end_date: str | None = None

    @property
    def source_calls(self):
        """Calls to the source: page_compare asks once per period, both count against the rate."""
        return 2 if self.report == "page_compare" else 1


def parse_request(cfg, source, report, pairs, now=None):
    if source not in REPORTS or report not in REPORTS[source]:
        raise ReportError(404, "route_not_found")
    allowed = (
        set() if report == "get_status" else {"period_days", "end_date", "segment"}
    )
    if report in (
        "query_summary",
        "page_summary",
        "page_compare",
        "traffic_summary",
        "landing_pages",
        "key_events",
    ):
        allowed.add("limit")
    if source == "gsc" and report != "get_status":
        allowed.add("page_path")
    if report == "page_compare":
        allowed.add("compare_end_date")
    args = {}
    for k, v in pairs:
        if k not in allowed or k in args or not isinstance(v, str):
            raise ReportError(400, "invalid_input")
        args[k] = v
    try:
        period = args.get("period_days", "28")
        require(bool(PERIOD_DAYS.fullmatch(period)) and int(period) <= MAX_PERIOD)
        days = int(period)
        zone = "America/Los_Angeles" if source == "gsc" else cfg.ga4_time_zone
        latest = (now or datetime.now(timezone.utc)).astimezone(
            ZoneInfo(zone)
        ).date() - timedelta(days=3)
        end = final_date(args["end_date"], latest) if "end_date" in args else latest
        start = end - timedelta(days=days - 1)
        compare_start = compare_end = None
        if report == "page_compare":
            compare_end = (
                final_date(args["compare_end_date"], latest)
                if "compare_end_date" in args
                else end - timedelta(days=days)
            )
            # Period B has the length of A and lies completely before it.
            require(compare_end < start)
            compare_start = compare_end - timedelta(days=days - 1)
        segment = args.get("segment", ALL_PUBLIC)
        require(
            segment == ALL_PUBLIC
            or (bool(SEGMENT_NAME.fullmatch(segment)) and segment in cfg.segments)
        )
        paths = cfg.paths(segment)
        require(bool(paths))
        page = args.get("page_path")
        require(page is None or page in paths)
        raw_limit = args.get("limit", "25")
        require(bool(re.fullmatch(r"[1-9][0-9]{0,2}", raw_limit)))
        limit = int(raw_limit)
        require(1 <= limit <= 100)
        return Request(
            source,
            report,
            start.isoformat(),
            end.isoformat(),
            zone,
            segment,
            [page] if page else paths,
            page,
            limit,
            cfg.site,
            compare_start.isoformat() if compare_start else None,
            compare_end.isoformat() if compare_end else None,
        )
    except (ValueError, OverflowError):
        raise ReportError(400, "invalid_input") from None


def build_query(cfg, req, compare=False):
    """The source query of a report; `compare` asks page_compare for period B instead of A."""
    if req.source == "gsc":
        if req.report == "query_summary" and not cfg.query_enabled:
            raise ReportError(503, "query_not_approved")
        urls = [u for p in req.paths for u in cfg.gsc_urls[p]]
        expression = regex_for(urls)
        if len(expression) > MAX_REGEX:
            raise ReportError(503, "configuration_invalid")
        dimension = {
            "performance_overview": "date",
            "query_summary": "query",
            "page_summary": "page",
            "page_compare": "page",
            "get_status": "date",
        }[req.report]
        start, end = (
            (req.compare_start_date, req.compare_end_date)
            if compare
            else (req.start_date, req.end_date)
        )
        return dict(
            startDate=start,
            endDate=end,
            type="web",
            dataState="final",
            aggregationType="auto",
            dimensions=[dimension],
            # A comparison asks for every approved URL in both periods: a page that ranks below
            # the output limit in one period must not look "unknown" in the other.
            rowLimit=1
            if req.report == "get_status"
            else 90
            if dimension == "date"
            else len(urls)
            if req.report == "page_compare"
            else req.limit,
            dimensionFilterGroups=[
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
        )
    dim = {
        "traffic_summary": "sessionDefaultChannelGroup",
        "landing_pages": "landingPage",
        "key_events": "eventName",
        "get_status": "sessionDefaultChannelGroup",
    }[req.report]
    filters = [
        {
            "filter": {
                "fieldName": "landingPage",
                "inListFilter": {"values": req.paths, "caseSensitive": True},
            }
        },
        {
            "filter": {
                "fieldName": "hostName",
                "inListFilter": {"values": cfg.hosts, "caseSensitive": False},
            }
        },
    ]
    metrics = ["sessions", "engagedSessions"]
    if req.report == "key_events":
        if cfg.key_events is None:
            return None
        if (
            req.start_date < cfg.key_events["valid_from"]
            or req.end_date > cfg.key_events["valid_to"]
        ):
            return None
        filters.append(
            {
                "filter": {
                    "fieldName": "eventName",
                    "inListFilter": {
                        "values": cfg.key_events["names"],
                        "caseSensitive": True,
                    },
                }
            }
        )
        metrics = ["keyEvents"]
    return dict(
        property="properties/" + cfg.ga4_property,
        dateRanges=[{"startDate": req.start_date, "endDate": req.end_date}],
        dimensions=[{"name": dim}],
        metrics=[{"name": m} for m in metrics],
        dimensionFilter={"andGroup": {"expressions": filters}},
        limit=1 if req.report == "get_status" else req.limit,
        orderBys=[{"metric": {"metricName": metrics[0]}, "desc": True}],
        returnPropertyQuota=True,
        keepEmptyRows=False,
    )


def number(value, integer=False):
    if isinstance(value, bool):
        raise ValueError()
    n = float(value)
    if not math.isfinite(n) or n < 0 or n > 1e18:
        raise ValueError()
    if integer and n != int(n):
        raise ValueError()
    return int(n) if integer else n


COMPARE_FIELDS = ("clicks", "impressions", "ctr", "position")
AGGREGATIONS = ("byPage", "byProperty", "auto")


def period_rows(cfg, req, raw):
    """The Search Analytics answer of one period: the row of each approved path.

    A path with two rows (two approved URLs of it) has no single CTR or position, so it counts
    as ambiguous and is never averaged. Rows of unapproved URLs are suppressed.
    """
    if not isinstance(raw, dict) or not isinstance(raw.get("rows", []), list):
        raise ValueError()
    found, ambiguous, suppressed = {}, set(), 0
    for row in raw.get("rows", []):
        keys = row["keys"]
        require(isinstance(keys, list) and len(keys) == 1 and isinstance(keys[0], str))
        item = {k: number(row[k]) for k in COMPARE_FIELDS}
        if item["ctr"] > 1:
            raise ValueError()
        matching = [p for p in req.paths if keys[0] in cfg.gsc_urls[p]]
        if not matching:
            suppressed += 1
        elif matching[0] in found or matching[0] in ambiguous:
            ambiguous.add(matching[0])
            found.pop(matching[0], None)
        else:
            found[matching[0]] = item
    return found, ambiguous, suppressed, len(raw.get("rows", []))


def compare_row(path, first, second):
    """Both periods and A - B per field; a missing period is null, never 0."""
    empty = dict.fromkeys(COMPARE_FIELDS)
    known = first is not None and second is not None
    return {
        "page_path": path,
        "a": first or dict(empty),
        "b": second or dict(empty),
        "delta": {k: round(first[k] - second[k], 10) for k in COMPARE_FIELDS}
        if known
        else dict(empty),
    }


def project_compare(cfg, req, raw, output):
    """page_compare: one row per approved page that has a source row in A or in B."""
    try:
        require(isinstance(raw, dict))
        a, a_ambiguous, a_suppressed, a_count = period_rows(cfg, req, raw["a"])
        b, b_ambiguous, b_suppressed, b_count = period_rows(cfg, req, raw["b"])
    except (KeyError, TypeError, ValueError, IndexError, OverflowError):
        raise ReportError(502, "invalid_source_response") from None
    listed = [p for p in req.paths if p in a or p in b or p in a_ambiguous or p in b_ambiguous]
    # Highest clicks in A first, then the path; a page without a value in A comes last.
    listed.sort(key=lambda p: (p not in a, -a[p]["clicks"] if p in a else 0, p))
    output["compare_range"] = {
        "start_date": req.compare_start_date,
        "end_date": req.compare_end_date,
        "time_zone": req.time_zone,
    }
    output["rows"] = [compare_row(p, a.get(p), b.get(p)) for p in listed[: req.limit]]
    quality = output["quality"]
    quality["limit_reached"] = len(listed) > req.limit
    quality["returned_rows"] = len(output["rows"])
    quality["source_returned_rows_a"] = a_count
    quality["source_returned_rows_b"] = b_count
    aggregations = {
        raw[period].get("responseAggregationType") for period in ("a", "b")
    }
    quality["aggregation_type"] = (
        aggregations.pop() if len(aggregations) == 1 and aggregations <= set(AGGREGATIONS) else "unknown"
    )
    notes = quality["notes"]
    notes.append("bounded_top_rows_not_total")
    shown = listed[: req.limit]
    if any(p not in a and p not in a_ambiguous for p in shown):
        notes.append("page_without_rows_in_period_a")
    if any(p not in b and p not in b_ambiguous for p in shown):
        notes.append("page_without_rows_in_period_b")
    if a_ambiguous or b_ambiguous:
        notes.append("page_rows_ambiguous")
    if a_suppressed or b_suppressed:
        notes.append("small_or_unapproved_groups_suppressed")
    # Every note after the first names a view that is not whole.
    partial = quality["limit_reached"] or len(notes) > 1
    output["state"] = "partial" if partial else "ok" if output["rows"] else "no_rows"
    return output


def project(cfg, req, raw):
    output = {
        "schema_version": "1.0",
        "source": req.source,
        "report": req.report,
        "state": "ok",
        "range": {
            "start_date": req.start_date,
            "end_date": req.end_date,
            "time_zone": req.time_zone,
        },
        "scope": {
            "site": cfg.site,
            "segment": req.segment,
            "definition_version": cfg.definition_version,
            "page_paths": req.paths,
        },
        "rows": [],
        "quality": {"coverage": "unknown", "limit_reached": False, "notes": []},
        "fetched_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
    }
    if req.report == "page_compare":
        return project_compare(cfg, req, raw, output)
    if req.source == "ga4" and raw.get("metadata", {}).get("timeZone") not in (
        None,
        cfg.ga4_time_zone,
    ):
        raise ReportError(503, "source_timezone_mismatch")
    if req.report == "get_status":
        return {
            "schema_version": "1.0",
            "source": req.source,
            "report": "get_status",
            "state": "ready",
            "checked_at": output["fetched_at"],
            "definition_version": cfg.definition_version,
            "site": cfg.site,
            "hosts": list(cfg.hosts),
            "segments": list(cfg.segments),
            "report_definitions": "validated" if req.source == "ga4" else "fixed",
            "key_events": "configured" if cfg.key_events else "not_configured",
            "query_reports": "enabled" if cfg.query_enabled else "disabled_pending_sec",
        }
    if (
        req.source == "ga4"
        and req.report == "key_events"
        and build_query(cfg, req) is None
    ):
        output["state"] = "not_configured"
        return output
    rows = raw.get("rows", [])
    if not isinstance(rows, list):
        raise ReportError(502, "invalid_source_response")
    cap = 90 if req.report == "performance_overview" else req.limit
    suppressed = 0
    try:
        for row in rows[:cap]:
            if req.source == "gsc":
                keys = row.get("keys", [])
                require(
                    isinstance(keys, list)
                    and len(keys) == 1
                    and isinstance(keys[0], str)
                )
                value = keys[0]
                item = {
                    k: number(row[k])
                    for k in ("clicks", "impressions", "ctr", "position")
                }
                if item["ctr"] > 1:
                    raise ValueError()
                if req.report == "query_summary":
                    if item["impressions"] < cfg.query_min_impressions:
                        suppressed += 1
                        continue
                    if len(value) > 1024 or any(ord(c) < 32 for c in value):
                        suppressed += 1
                        continue
                    item["query"] = value
                elif req.report == "page_summary":
                    matching = [p for p in req.paths if value in cfg.gsc_urls[p]]
                    if not matching:
                        suppressed += 1
                        continue
                    item["page_path"] = matching[0]
                else:
                    day = iso_date(value)
                    if not req.start_date <= day.isoformat() <= req.end_date:
                        raise ValueError()
                    item["date"] = value
            else:
                values = row["dimensionValues"]
                require(len(values) == 1)
                value = values[0]["value"]
                require(
                    isinstance(value, str)
                    and len(value) <= 256
                    and not any(ord(c) < 32 for c in value)
                )
                metrics = row["metricValues"]
                if req.report == "key_events":
                    require(len(metrics) == 1)
                    count = number(metrics[0]["value"])
                    if (
                        value not in cfg.key_events["names"]
                        or count < cfg.ga4_min_group_size
                    ):
                        suppressed += 1
                        continue
                    item = {"event_name": value, "key_events": count}
                else:
                    require(len(metrics) == 2)
                    item = {
                        "sessions": number(metrics[0]["value"], True),
                        "engaged_sessions": number(metrics[1]["value"], True),
                    }
                    if req.report == "landing_pages":
                        if (
                            value not in req.paths
                            or item["sessions"] < cfg.ga4_min_group_size
                        ):
                            suppressed += 1
                            continue
                        item["page_path"] = value
                    else:
                        channels = {
                            "Direct",
                            "Organic Search",
                            "Paid Search",
                            "Organic Social",
                            "Paid Social",
                            "Referral",
                            "Email",
                            "Affiliates",
                            "Display",
                            "Cross-network",
                            "Organic Shopping",
                            "Paid Shopping",
                            "Organic Video",
                            "Paid Video",
                            "Audio",
                            "SMS",
                            "Mobile Push Notifications",
                            "Unassigned",
                            "(other)",
                        }
                        if value not in channels:
                            suppressed += 1
                            continue
                        item["channel"] = value
            output["rows"].append(item)
        quality = output["quality"]
        quality["limit_reached"] = len(rows) >= cap
        quality["returned_rows"] = len(output["rows"])
        quality["source_returned_rows"] = len(rows)
        if req.source == "gsc":
            quality["notes"].append("bounded_top_rows_not_total")
            if (
                req.report == "performance_overview"
                and len(output["rows"])
                < (iso_date(req.end_date) - iso_date(req.start_date)).days + 1
            ):
                quality["notes"].append("missing_days_not_collection_start")
            agg = raw.get("responseAggregationType")
            quality["aggregation_type"] = (
                agg if agg in ("byPage", "byProperty", "auto") else "unknown"
            )
        else:
            if "rowCount" in raw:
                quality["source_row_count"] = number(raw["rowCount"], True)
            meta = raw.get("metadata", {})
            if meta.get("timeZone") and meta["timeZone"] != cfg.ga4_time_zone:
                raise ReportError(503, "source_timezone_mismatch")
            quality["source_time_zone_verified"] = (
                meta.get("timeZone") == cfg.ga4_time_zone
            )
            quality["empty_reason_present"] = bool(meta.get("emptyReason"))
            if "dataTruncationReasons" in meta:
                quality["data_truncation_present"] = bool(meta["dataTruncationReasons"])
            for name in ("subjectToThresholding", "dataLossFromOtherRow"):
                if name in meta and type(meta[name]) is bool:
                    quality[name] = meta[name]
            if "samplingMetadatas" in meta:
                quality["sampling"] = [
                    {
                        "samples_read_count": number(s["samplesReadCount"], True),
                        "sampling_space_size": number(s["samplingSpaceSize"], True),
                    }
                    for s in meta["samplingMetadatas"][:4]
                ]
            restrictions = meta.get("schemaRestrictionResponse", {}).get(
                "activeMetricRestrictions", []
            )
            quality["metric_restrictions"] = [
                {
                    "metric_name": r["metricName"],
                    "types": [
                        v
                        for v in r.get("restrictedMetricTypes", [])
                        if v in ("REVENUE_DATA", "COST_DATA")
                    ],
                }
                for r in restrictions
                if r.get("metricName") in ("sessions", "engagedSessions", "keyEvents")
            ]
            quality["other_row_returned"] = any(
                r.get("channel") == "(other)" for r in output["rows"]
            )
            quota = raw.get("propertyQuota", {})
            quality["quota"] = {
                k: {n: number(v[n], True) for n in ("consumed", "remaining") if n in v}
                for k, v in quota.items()
                if k
                in (
                    "tokensPerDay",
                    "tokensPerHour",
                    "concurrentRequests",
                    "serverErrorsPerProjectPerHour",
                    "potentiallyThresholdedRequestsPerHour",
                    "tokensPerProjectPerHour",
                )
            }
            if req.report == "key_events":
                output["measurement"] = {
                    k: cfg.key_events[k]
                    for k in ("meaning", "valid_from", "valid_to", "consent_coverage")
                }
        if suppressed:
            quality["notes"].append("small_or_unapproved_groups_suppressed")
        partial = (
            suppressed
            or quality["limit_reached"]
            or any(
                quality.get(k)
                for k in (
                    "subjectToThresholding",
                    "dataLossFromOtherRow",
                    "sampling",
                    "metric_restrictions",
                    "other_row_returned",
                    "data_truncation_present",
                )
            )
        )
        if quality.get("source_row_count", 0) > len(rows):
            partial = True
        output["state"] = (
            "partial" if partial else "ok" if output["rows"] else "no_rows"
        )
        return output
    except (KeyError, TypeError, ValueError, IndexError, OverflowError):
        raise ReportError(502, "invalid_source_response") from None


def encode_response(value):
    data = json.dumps(
        value, ensure_ascii=True, allow_nan=False, separators=(",", ":")
    ).encode()
    if len(data) > MAX_RESPONSE:
        raise ReportError(502, "response_too_large")
    return data
