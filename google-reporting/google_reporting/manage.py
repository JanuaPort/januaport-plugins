"""Write/manage handles of JanuaPort/januaport#1148: fixed routes, validation, builders.

Pure: no HTTP, no Google import. Every rejection is a fixed code without an echo of the
input. Site and property never come from a parameter, only from the operator config.
"""

from dataclasses import dataclass
from datetime import date, datetime, timedelta, timezone
import hashlib
import json
import re
from .domain import ReportError, public_path

GET_OPS = {
    "gsc": {"list_sitemaps", "get_sitemap", "inspect_url"},
    "ga4": {"list_annotations", "list_channel_groups"},
}
POST_OPS = {
    "gsc": {"submit_sitemap"},
    "ga4": {
        "create_annotation",
        "update_annotation",
        "create_channel_group",
        "update_channel_group",
    },
}
MANAGED_PREFIX = "[JP] "
COLORS = ("PURPLE", "BROWN", "BLUE", "GREEN", "RED", "CYAN")
DEFAULT_COLOR = "BLUE"
TITLE_MAX = 60  # Google Analytics help: including the managed prefix
DESCRIPTION_MAX = 150
MAX_HOSTS = 20
MAX_BODY = 8192
EARLIEST_DATE = date(2020, 1, 1)
LATEST_DAYS_AHEAD = 366
# Google default channel group (Analytics help "Default channel group"). A custom group
# classifies every session, so these keep the usual picture next to the AI channel.
STANDARD_CHANNELS = (
    "Direct",
    "Cross-network",
    "Paid Search",
    "Paid Social",
    "Paid Video",
    "Paid Shopping",
    "Paid Other",
    "Display",
    "Affiliates",
    "Organic Search",
    "Organic Social",
    "Organic Video",
    "Organic Shopping",
    "Email",
    "SMS",
    "Audio",
    "Mobile Push Notifications",
    "AI Assistant",
    "Referral",
)
ANNOTATION_KEYS = {
    "name",
    "title",
    "description",
    "color",
    "systemGenerated",
    "annotationDate",
    "annotationDateRange",
}
GROUP_KEYS = {
    "name",
    "displayName",
    "description",
    "groupingRule",
    "systemDefined",
    "primary",
}
GOOGLE_COLORS = set(COLORS) | {"COLOR_UNSPECIFIED", "ORANGE"}
# Change line: values of these fields have a fixed type and may be printed; free text
# is reduced to length and hash (SEC P2/P4).
FIXED_FIELDS = (
    "annotation_date",
    "color",
    "source_hosts",
    "sitemap_url",
    "registered",
    "last_submitted",
    "channel_group_name",
    "rule_count",
    "primary",
)
FREE_TEXT_FIELDS = ("title", "description")


@dataclass(frozen=True)
class ManageRequest:
    source: str
    operation: str
    params: dict
    dry_run: bool = False

    @property
    def write(self):
        return self.operation in POST_OPS[self.source]


def invalid():
    return ReportError(400, "invalid_input")


def now_utc():
    return datetime.now(timezone.utc)


def timestamp(now=None):
    return now or now_utc().strftime("%Y-%m-%dT%H:%M:%SZ")


# --- validation -------------------------------------------------------------------


def sitemap_url(cfg, value):
    match = (
        re.fullmatch(r"https://([a-z0-9]+(?:[.-][a-z0-9]+)*)(/[A-Za-z0-9/_~.\-]*\.xml)", value)
        if isinstance(value, str) and len(value) <= 512
        else None
    )
    if not match:
        raise invalid()
    host, path = match.groups()
    if host not in cfg.hosts or not public_path(path):
        raise invalid()
    if cfg.gsc_property.startswith("https://") and not value.startswith(cfg.gsc_property):
        raise invalid()
    return value


def resource_id(value):
    if not isinstance(value, str) or not re.fullmatch(r"[A-Za-z0-9_-]{1,64}", value):
        raise invalid()
    return value


def annotation_date(value, now=None):
    if not isinstance(value, str) or not re.fullmatch(r"[0-9]{4}-[0-9]{2}-[0-9]{2}", value):
        raise invalid()
    try:
        day = date.fromisoformat(value)
    except ValueError:
        raise invalid() from None
    latest = (now or now_utc()).date() + timedelta(days=LATEST_DAYS_AHEAD)
    if not EARLIEST_DATE <= day <= latest:
        raise invalid()
    return value


def personal_data(text):
    """Email, phone number or IBAN, the patterns of the gateway privacy scan (SEC P2)."""
    if re.search(r"\b[A-Za-z0-9][A-Za-z0-9._%+\-]*@[A-Za-z0-9][A-Za-z0-9.\-]*\.[A-Za-z]{2,}\b", text):
        return True
    for match in re.finditer(
        r"\+[0-9]{1,3}(?:[ /\-]?\(0\))?(?:[ /\-]?[0-9]{2,9}){1,3}\b|\b0[0-9]{1,4}[ /\-][0-9]{3,9}\b",
        text,
    ):
        if 7 <= sum(c.isdigit() for c in match.group()) <= 15:
            return True
    for match in re.finditer(r"\b[A-Z]{2}[0-9]{2}[A-Z0-9]{11,30}\b", text):
        if valid_iban(match.group()):
            return True
    return False


def valid_iban(iban):
    moved = iban[4:] + iban[:4]
    digits = "".join(str(int(c, 36)) for c in moved)
    return int(digits) % 97 == 1


def free_text(value, limit, required):
    if (
        not isinstance(value, str)
        or len(value) > limit
        or (required and not value)
        or any(ord(c) < 32 or ord(c) == 127 for c in value)
    ):
        raise invalid()
    if personal_data(value):
        raise ReportError(400, "personal_data_rejected")
    return value


def color(value):
    if value not in COLORS:
        raise invalid()
    return value


def source_hosts(value):
    if not isinstance(value, str):
        raise invalid()
    hosts = [h.strip() for h in value.split(",")]
    if (
        not 1 <= len(hosts) <= MAX_HOSTS
        or len(set(hosts)) != len(hosts)
        or not all(
            len(h) <= 253 and re.fullmatch(r"[a-z0-9]+(?:[.-][a-z0-9]+)*", h) and "." in h
            for h in hosts
        )
    ):
        raise invalid()
    return hosts


def annotation_fields(body, now, required):
    params = {}
    if "date" in body:
        params["date"] = annotation_date(body["date"], now)
    if "title" in body:
        title = free_text(body["title"], TITLE_MAX - len(MANAGED_PREFIX), True)
        params["title"] = MANAGED_PREFIX + title
    if "description" in body:
        params["description"] = free_text(body["description"], DESCRIPTION_MAX, False)
    if "color" in body:
        params["color"] = color(body["color"])
    elif required:
        params["color"] = DEFAULT_COLOR
    return params


class DuplicateKey(ValueError):
    pass


def unique_object(pairs):
    keys = [k for k, _ in pairs]
    if len(set(keys)) != len(keys):
        raise DuplicateKey()
    return dict(pairs)


def json_object(raw):
    try:
        body = json.loads(raw.decode("utf-8"), object_pairs_hook=unique_object)
    except (UnicodeDecodeError, ValueError, RecursionError):
        raise invalid() from None
    if not isinstance(body, dict):
        raise invalid()
    return body


def parse_get(cfg, source, op, pairs, now=None):
    if op not in GET_OPS.get(source, ()):
        raise ReportError(404, "route_not_found")
    allowed = {"get_sitemap": {"sitemap_url"}, "inspect_url": {"page_path"}}.get(op, set())
    args = {}
    for key, value in pairs:
        if key not in allowed or key in args or not isinstance(value, str):
            raise invalid()
        args[key] = value
    if op == "get_sitemap":
        if "sitemap_url" not in args:
            raise invalid()
        return ManageRequest(source, op, {"sitemap_url": sitemap_url(cfg, args["sitemap_url"])})
    if op == "inspect_url":
        path = args.get("page_path")
        if path is None or path not in cfg.gsc_urls:
            raise invalid()
        return ManageRequest(source, op, {"page_path": path, "url": cfg.gsc_urls[path][0]})
    return ManageRequest(source, op, {})


def parse_post(cfg, source, op, raw, now=None):
    if op not in POST_OPS.get(source, ()):
        raise ReportError(404, "route_not_found")
    body = json_object(raw)
    allowed = {
        "submit_sitemap": {"sitemap_url"},
        "create_annotation": {"date", "title", "description", "color"},
        "update_annotation": {"annotation_id", "date", "title", "description", "color"},
        "create_channel_group": {"source_hosts"},
        "update_channel_group": {"channel_group_id", "source_hosts"},
    }[op]
    if not set(body) <= allowed | {"dry_run"}:
        raise invalid()
    dry_run = body.pop("dry_run", False)
    if type(dry_run) is not bool:
        raise invalid()
    if op == "submit_sitemap":
        if "sitemap_url" not in body:
            raise invalid()
        params = {"sitemap_url": sitemap_url(cfg, body["sitemap_url"])}
    elif op == "create_annotation":
        if not {"date", "title"} <= set(body):
            raise invalid()
        params = annotation_fields(body, now, required=True)
    elif op == "update_annotation":
        if "annotation_id" not in body or len(body) < 2:
            raise invalid()
        params = {"annotation_id": resource_id(body["annotation_id"])}
        params.update(annotation_fields({k: v for k, v in body.items() if k != "annotation_id"}, now, required=False))
    elif op == "create_channel_group":
        if "source_hosts" not in body:
            raise invalid()
        params = {"source_hosts": source_hosts(body["source_hosts"])}
    else:
        if not {"channel_group_id", "source_hosts"} <= set(body):
            raise invalid()
        params = {
            "channel_group_id": resource_id(body["channel_group_id"]),
            "source_hosts": source_hosts(body["source_hosts"]),
        }
    return ManageRequest(source, op, params, dry_run)


# --- Google bodies -----------------------------------------------------------------


def date_parts(value):
    year, month, day = (int(p) for p in value.split("-"))
    return {"year": year, "month": month, "day": day}


def annotation_body(params):
    body = {}
    for key in ("title", "color", "description"):
        if key in params:
            body[key] = params[key]
    if "date" in params:
        body["annotationDate"] = date_parts(params["date"])
    return body


def host_filter(field, expression):
    return {
        "andGroup": {
            "filterExpressions": [
                {"orGroup": {"filterExpressions": [{"filter": dict(fieldName=field, **expression)}]}}
            ]
        }
    }


def channel_group_rules(cfg, hosts):
    ai = {
        "displayName": cfg.ga4_channel_group_name,
        "expression": host_filter("eachScopeSource", {"inListFilter": {"values": list(hosts)}}),
    }
    standard = [
        {
            "displayName": name,
            "expression": host_filter(
                "eachScopeDefaultChannelGroup",
                {"stringFilter": {"matchType": "EXACT", "value": name}},
            ),
        }
        for name in STANDARD_CHANNELS
    ]
    return [ai] + standard


def channel_group_body(cfg, hosts):
    return {
        "displayName": cfg.ga4_channel_group_name,
        "description": "Managed by JanuaPort: AI assistant sources, then the default channels.",
        "primary": False,
        "groupingRule": channel_group_rules(cfg, hosts),
    }


def hosts_from_rule(rule):
    try:
        leaf = rule["expression"]["andGroup"]["filterExpressions"][0]["orGroup"][
            "filterExpressions"
        ][0]["filter"]
        values = leaf["inListFilter"]["values"]
        if (
            leaf["fieldName"] == "eachScopeSource"
            and isinstance(values, list)
            and all(isinstance(v, str) for v in values)
        ):
            return values
    except (KeyError, TypeError, IndexError):
        pass
    return None


# --- projection of Google answers --------------------------------------------------


def shape_error():
    return ReportError(502, "unexpected_source_shape")


def trailing_id(name, parent):
    if isinstance(name, str) and name.startswith(parent + "/"):
        return resource_id_or_none(name[len(parent) + 1 :])
    return None


def resource_id_or_none(value):
    return value if isinstance(value, str) and re.fullmatch(r"[A-Za-z0-9_-]{1,64}", value) else None


def google_date(value):
    try:
        return date(int(value["year"]), int(value["month"]), int(value["day"])).isoformat()
    except (KeyError, TypeError, ValueError):
        return None


def text_or_none(value, limit=512):
    if isinstance(value, str) and len(value) <= limit and not any(ord(c) < 32 for c in value):
        return value
    return None


def project_annotation(data, parent, strict):
    """Strict before a write: an unknown v1alpha field stops it (SEC H3)."""
    if not isinstance(data, dict) or (strict and set(data) - ANNOTATION_KEYS):
        raise shape_error()
    rng = data.get("annotationDateRange")
    item = {
        "id": trailing_id(data.get("name"), parent + "/reportingDataAnnotations"),
        "date": google_date(data.get("annotationDate")),
        "date_range": {
            "start": google_date(rng.get("startDate")),
            "end": google_date(rng.get("endDate")),
        }
        if isinstance(rng, dict)
        else None,
        "title": text_or_none(data.get("title"), 200),
        "description": text_or_none(data.get("description"), 1000),
        "color": data.get("color") if data.get("color") in GOOGLE_COLORS else None,
        "system_generated": data.get("systemGenerated") is True,
    }
    if strict and (item["id"] is None or item["title"] is None or item["color"] is None):
        raise shape_error()
    item["managed"] = (
        not item["system_generated"]
        and item["date"] is not None
        and isinstance(item["title"], str)
        and item["title"].startswith(MANAGED_PREFIX)
    )
    return item


def project_group(cfg, data, parent, strict):
    if not isinstance(data, dict) or (strict and set(data) - GROUP_KEYS):
        raise shape_error()
    rules = data.get("groupingRule")
    if rules is None:
        rules = []
    if not isinstance(rules, list) or (strict and not all(isinstance(r, dict) for r in rules)):
        raise shape_error()
    name = text_or_none(data.get("displayName"), 200)
    system = data.get("systemDefined") is True
    item = {
        "id": trailing_id(data.get("name"), parent + "/channelGroups"),
        "display_name": name,
        "description": text_or_none(data.get("description"), 1000),
        "primary": data.get("primary") is True,
        "system_defined": system,
        "managed": name == cfg.ga4_channel_group_name and not system,
        "source_hosts": hosts_from_rule(rules[0]) if rules and isinstance(rules[0], dict) else None,
        "channels": [
            r["displayName"]
            for r in rules[:50]
            if isinstance(r, dict) and text_or_none(r.get("displayName"), 200)
        ],
        "rule_count": len(rules),
    }
    if strict and (item["id"] is None or name is None):
        raise shape_error()
    return item


def integer(value):
    try:
        number = int(value)
    except (TypeError, ValueError):
        return None
    return number if 0 <= number <= 10**12 else None


def project_sitemap(data):
    if not isinstance(data, dict):
        raise shape_error()
    contents = data.get("contents")
    submitted = None
    if isinstance(contents, list):
        counts = [integer(c.get("submitted")) for c in contents if isinstance(c, dict)]
        submitted = sum(n for n in counts if n is not None)
    return {
        "path": text_or_none(data.get("path")),
        "last_submitted": text_or_none(data.get("lastSubmitted"), 64),
        "last_downloaded": text_or_none(data.get("lastDownloaded"), 64),
        "is_pending": data.get("isPending") if isinstance(data.get("isPending"), bool) else None,
        "is_sitemaps_index": data.get("isSitemapsIndex")
        if isinstance(data.get("isSitemapsIndex"), bool)
        else None,
        "type": text_or_none(data.get("type"), 64),
        "warnings": integer(data.get("warnings")),
        "errors": integer(data.get("errors")),
        "submitted": submitted,
    }


def project_inspection(data, page_path, url):
    if not isinstance(data, dict):
        raise shape_error()
    status = data.get("inspectionResult", {})
    status = status.get("indexStatusResult", {}) if isinstance(status, dict) else {}
    if not isinstance(status, dict):
        raise shape_error()
    sitemaps = status.get("sitemap", [])
    sitemaps = [s for s in sitemaps if text_or_none(s) and s.startswith("https://")][:10] if isinstance(sitemaps, list) else []
    return {
        "page_path": page_path,
        "inspected_url": url,
        "verdict": text_or_none(status.get("verdict"), 64),
        "coverage_state": text_or_none(status.get("coverageState")),
        "robots_txt_state": text_or_none(status.get("robotsTxtState"), 64),
        "indexing_state": text_or_none(status.get("indexingState"), 64),
        "page_fetch_state": text_or_none(status.get("pageFetchState"), 64),
        "last_crawl_time": text_or_none(status.get("lastCrawlTime"), 64),
        "google_canonical": text_or_none(status.get("googleCanonical")),
        "user_canonical": text_or_none(status.get("userCanonical")),
        "crawled_as": text_or_none(status.get("crawledAs"), 64),
        "sitemaps": sitemaps,
        "listed_in_sitemap": bool(sitemaps),
    }


def envelope(source, operation, state, **fields):
    out = {"schema_version": "1.0", "source": source, "operation": operation, "state": state}
    out.update(fields)
    out["fetched_at"] = now_utc().isoformat().replace("+00:00", "Z")
    return out


# --- change line (SEC P2/P4) -------------------------------------------------------


def line_value(field, value):
    if field in FREE_TEXT_FIELDS:
        if isinstance(value, str):
            digest = hashlib.sha256(value.encode()).hexdigest()[:12]
            return {"len": len(value), "sha256": digest}
        return None
    if value is None or isinstance(value, (bool, int)):
        return value
    if isinstance(value, str):
        return value if len(value) <= 512 else None
    if isinstance(value, list) and len(value) <= MAX_HOSTS and all(isinstance(v, str) for v in value):
        return value
    return None


def line_state(state):
    if not isinstance(state, dict):
        return "null"
    kept = {
        field: line_value(field, state[field])
        for field in FIXED_FIELDS + FREE_TEXT_FIELDS
        if field in state
    }
    return json.dumps(kept, ensure_ascii=True, separators=(",", ":"), sort_keys=True)


def line_target(value):
    return value if isinstance(value, str) and re.fullmatch(r"[A-Za-z0-9:/._~\-]{1,512}", value) else "-"


def change_line(source, operation, payload, now=None):
    fields = payload.get("changed_fields", [])
    names = [f for f in fields if isinstance(f, str) and re.fullmatch(r"[a-z_]{1,40}", f)]
    return "change ts=%s source=%s op=%s target=%s fields=%s before=%s after=%s" % (
        timestamp(now),
        source,
        operation,
        line_target(payload.get("target")),
        ",".join(names),
        line_state(payload.get("before")),
        line_state(payload.get("after")),
    )
