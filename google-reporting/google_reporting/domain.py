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
REPORTS = {
    "gsc": {"get_status", "performance_overview", "query_summary", "page_summary"},
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

    @classmethod
    def from_dict(cls, raw):
        try:
            cfg = cls(**raw)
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
            require(set(cfg.segments) == {"atas", "gateway", "shared"})
            paths = []
            for group in cfg.segments.values():
                require(isinstance(group, list) and all(public_path(p) for p in group))
                paths.extend(group)
            require(1 <= len(paths) <= 50 and len(set(paths)) == len(paths))
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
            if segment == "all_public"
            else self.segments[segment]
        )


def iso_date(value):
    if not isinstance(value, str) or not re.fullmatch(r"\d{4}-\d{2}-\d{2}", value):
        raise ValueError()
    return date.fromisoformat(value)


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


def parse_request(cfg, source, report, pairs, now=None):
    if source not in REPORTS or report not in REPORTS[source]:
        raise ReportError(404, "route_not_found")
    allowed = (
        set() if report == "get_status" else {"period_days", "end_date", "segment"}
    )
    if report in (
        "query_summary",
        "page_summary",
        "traffic_summary",
        "landing_pages",
        "key_events",
    ):
        allowed.add("limit")
    if source == "gsc" and report != "get_status":
        allowed.add("page_path")
    args = {}
    for k, v in pairs:
        if k not in allowed or k in args or not isinstance(v, str):
            raise ReportError(400, "invalid_input")
        args[k] = v
    try:
        period = args.get("period_days", "28")
        require(period in ("28", "90"))
        zone = "America/Los_Angeles" if source == "gsc" else cfg.ga4_time_zone
        latest = (now or datetime.now(timezone.utc)).astimezone(
            ZoneInfo(zone)
        ).date() - timedelta(days=3)
        end = iso_date(args["end_date"]) if "end_date" in args else latest
        if end > latest:
            # Well-formed but beyond the final data: a fixed code, no date echoed (#1150).
            raise ReportError(400, "end_date_not_available")
        start = end - timedelta(days=int(period) - 1)
        segment = args.get("segment", "all_public")
        require(segment in ("all_public", "atas", "gateway", "shared"))
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
        )
    except (ValueError, OverflowError):
        raise ReportError(400, "invalid_input") from None


def build_query(cfg, req):
    if req.source == "gsc":
        if req.report == "query_summary" and not cfg.query_enabled:
            raise ReportError(503, "query_not_approved")
        expression = regex_for([u for p in req.paths for u in cfg.gsc_urls[p]])
        if len(expression) > MAX_REGEX:
            raise ReportError(503, "configuration_invalid")
        dimension = {
            "performance_overview": "date",
            "query_summary": "query",
            "page_summary": "page",
            "get_status": "date",
        }[req.report]
        return dict(
            startDate=req.start_date,
            endDate=req.end_date,
            type="web",
            dataState="final",
            aggregationType="auto",
            dimensions=[dimension],
            rowLimit=1
            if req.report == "get_status"
            else 90
            if dimension == "date"
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
            "segment": req.segment,
            "definition_version": cfg.definition_version,
            "page_paths": req.paths,
        },
        "rows": [],
        "quality": {"coverage": "unknown", "limit_reached": False, "notes": []},
        "fetched_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
    }
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
