"""Pure rules: configuration, URL validation, Bing date parsing, projections, envelopes.

Nothing here talks to the network or logs. Every rejection is a ReportError with a fixed
code; the offending value is never part of the error.
"""

from dataclasses import dataclass, field
from datetime import datetime, timezone
import json
import re
from urllib.parse import unquote_to_bytes

SCHEMA_VERSION = "1.0"
MAX_URL = 512
MAX_URLS = 100
MAX_BODY = 8192
MAX_RESPONSE = 512 * 1024
MAX_FEEDS = 100
MAX_HOSTS = 5
WRITE_CAP_MAX = 20

GET_OPS = {"bing": ("url_info", "sitemaps", "submission_quota")}
POST_OPS = {"bing": ("submit_urls", "submit_sitemap"), "indexnow": ("submit_urls",)}

HOST = re.compile(r"(?=.{1,253}\Z)[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+")
# Exact shape of an acceptable URL: lowercase host without port or userinfo, path without
# query, fragment, "@", ":" or ",". Anything else is rejected, never repaired.
URL = re.compile(r"https://([a-z0-9.-]{1,253})((?:/[A-Za-z0-9\-._~!$&'()*+;=%]*)*)")
PERCENT = re.compile(r"%(?![0-9A-Fa-f]{2})")
INDEXNOW_KEY = re.compile(r"[A-Za-z0-9-]{8,128}")
BING_DATE = re.compile(r"/Date\((-?\d{1,15})(?:[+-]\d{4})?\)/")
VENDOR_URL = re.compile(r"[\x21-\x7e]{1,512}")
VENDOR_LABEL = re.compile(r"[A-Za-z0-9 _.-]{0,64}")


class ReportError(Exception):
    def __init__(self, status, code):
        super().__init__(code)
        self.status = status
        self.code = code


def invalid():
    return ReportError(400, "invalid_input")


def shape_error():
    return ReportError(502, "unexpected_source_shape")


@dataclass(frozen=True)
class IndexNow:
    host: str
    key: str = field(repr=False)
    key_location: str = field(repr=False)


@dataclass(frozen=True)
class Config:
    site_url: str
    hosts: tuple
    write_cap: int
    indexnow: IndexNow | None

    @classmethod
    def from_dict(cls, raw):
        """Fail closed: every deviation is a ValueError without the offending value."""
        if not isinstance(raw, dict) or set(raw) - {"site_url", "hosts", "write_cap_per_day", "indexnow"}:
            raise ValueError("configuration_invalid")
        hosts = raw.get("hosts")
        if (
            not isinstance(hosts, list)
            or not 1 <= len(hosts) <= MAX_HOSTS
            or len(set(map(str, hosts))) != len(hosts)
            or not all(isinstance(h, str) and HOST.fullmatch(h) for h in hosts)
        ):
            raise ValueError("configuration_invalid")
        hosts = tuple(hosts)
        site = split_url(raw.get("site_url"))
        if site is None or site[0] not in hosts or site[1] not in ("", "/"):
            raise ValueError("configuration_invalid")
        cap = raw.get("write_cap_per_day", WRITE_CAP_MAX)
        if type(cap) is not int or not 1 <= cap <= WRITE_CAP_MAX:
            raise ValueError("configuration_invalid")
        return cls(raw["site_url"], hosts, cap, cls._indexnow(raw.get("indexnow"), hosts))

    @staticmethod
    def _indexnow(raw, hosts):
        if raw is None:
            return None
        if not isinstance(raw, dict) or set(raw) != {"key", "key_location"}:
            raise ValueError("configuration_invalid")
        key = raw["key"]
        if not isinstance(key, str) or not INDEXNOW_KEY.fullmatch(key):
            raise ValueError("configuration_invalid")
        where = split_url(raw["key_location"])
        if where is None or where[0] not in hosts or where[1] != "/" + key + ".txt":
            raise ValueError("configuration_invalid")
        return IndexNow(where[0], key, raw["key_location"])


@dataclass(frozen=True)
class Request:
    source: str
    operation: str
    write: bool = False
    dry_run: bool = False
    url: str | None = None
    urls: tuple = ()


def split_url(value):
    """(host, path) of an https URL in the accepted shape, else None."""
    if not isinstance(value, str) or len(value) > MAX_URL or not value.isascii():
        return None
    match = URL.fullmatch(value)
    return match.groups() if match else None


def validate_url(cfg, value, *, sitemap=False):
    parts = split_url(value)
    if parts is None:
        raise invalid()
    host, path = parts
    if host not in cfg.hosts or "//" in path or PERCENT.search(path):
        raise invalid()
    decoded = unquote_to_bytes(path)
    if any(b < 0x20 or b == 0x7F for b in decoded) or b".." in decoded.split(b"/") or b"\\" in decoded:
        raise invalid()
    if sitemap and not (path.startswith("/") and path.endswith(".xml") and len(path) > len("/.xml")):
        raise invalid()
    return value


def _no_duplicates(pairs):
    out = {}
    for key, value in pairs:
        if key in out:
            raise ValueError("duplicate_key")
        out[key] = value
    return out


def _reject_constant(_name):
    raise ValueError("constant")


def json_object(raw):
    if len(raw) > MAX_BODY:
        raise ReportError(400, "request_too_large")
    try:
        data = json.loads(raw.decode("utf-8"), object_pairs_hook=_no_duplicates, parse_constant=_reject_constant)
    except ValueError:
        raise invalid() from None
    if not isinstance(data, dict):
        raise invalid()
    return data


def parse_get(cfg, source, operation, items):
    if operation not in GET_OPS.get(source, ()):
        raise ReportError(404, "route_not_found")
    if operation != "url_info":
        if items:
            raise invalid()
        return Request(source, operation)
    if len(items) != 1 or items[0][0] != "url":
        raise invalid()
    return Request(source, operation, url=validate_url(cfg, items[0][1]))


def _dry_run(body):
    value = body.get("dry_run", False)
    if type(value) is not bool:
        raise invalid()
    return value


def _url_list(cfg, source, text):
    if not isinstance(text, str) or not text:
        raise invalid()
    parts = text.split(",")
    if len(parts) > MAX_URLS:
        raise ReportError(400, "too_many_urls")
    urls = tuple(dict.fromkeys(validate_url(cfg, part) for part in parts))
    if source == "indexnow" and cfg.indexnow is not None:
        if any(split_url(u)[0] != cfg.indexnow.host for u in urls):
            raise invalid()
    return urls


def parse_post(cfg, source, operation, raw):
    if operation not in POST_OPS.get(source, ()):
        raise ReportError(404, "route_not_found")
    body = json_object(raw)
    if operation == "submit_sitemap":
        if set(body) - {"sitemap_url", "dry_run"} or "sitemap_url" not in body:
            raise invalid()
        url = validate_url(cfg, body["sitemap_url"], sitemap=True)
        return Request(source, operation, write=True, dry_run=_dry_run(body), url=url)
    if set(body) - {"urls", "dry_run"} or "urls" not in body:
        raise invalid()
    urls = _url_list(cfg, source, body["urls"])
    return Request(source, operation, write=True, dry_run=_dry_run(body), urls=urls)


def parse_bing_date(value):
    """`/Date(ms±hhmm)/` to ISO UTC. The offset is display-only; ms is already UTC.

    Values at or before the epoch are Bing's "never" and become None.
    """
    if value is None:
        return None
    match = BING_DATE.fullmatch(value) if isinstance(value, str) else None
    if match is None:
        raise shape_error()
    millis = int(match.group(1))
    if millis <= 0:
        return None
    try:
        moment = datetime.fromtimestamp(millis // 1000, tz=timezone.utc)
    except (OverflowError, OSError, ValueError):
        raise shape_error() from None
    return moment.strftime("%Y-%m-%dT%H:%M:%SZ")


def _count(value):
    if type(value) is not int or value < 0:
        raise shape_error()
    return value


def _flag(value):
    if type(value) is not bool:
        raise shape_error()
    return value


def _text(value, pattern):
    if not isinstance(value, str) or not pattern.fullmatch(value):
        raise shape_error()
    return value


def _record(raw, names):
    if not isinstance(raw, dict) or any(name not in raw for name in names):
        raise shape_error()
    return raw


def _payload(raw):
    if not isinstance(raw, dict) or "d" not in raw:
        raise shape_error()
    return raw["d"]


def project_url_info(url, raw):
    data = _payload(raw)
    if data is None:
        return {"state": "no_data", "item": None}
    data = _record(data, ("DiscoveryDate", "DocumentSize", "HttpStatus", "IsPage", "LastCrawledDate"))
    return {
        "state": "ok",
        "item": {
            "url": url,
            "discovery_date": parse_bing_date(data["DiscoveryDate"]),
            "last_crawled_date": parse_bing_date(data["LastCrawledDate"]),
            "http_status": _count(data["HttpStatus"]),
            "document_size": _count(data["DocumentSize"]),
            "is_page": _flag(data["IsPage"]),
        },
    }


def _feed(raw):
    data = _record(raw, ("Url", "Type", "Status", "UrlCount", "FileSize", "Compressed", "Submitted", "LastCrawled"))
    return {
        "url": _text(data["Url"], VENDOR_URL),
        "type": _text(data["Type"], VENDOR_LABEL),
        "status": _text(data["Status"], VENDOR_LABEL),
        "url_count": _count(data["UrlCount"]),
        "file_size": _count(data["FileSize"]),
        "compressed": _flag(data["Compressed"]),
        "submitted": parse_bing_date(data["Submitted"]),
        "last_crawled": parse_bing_date(data["LastCrawled"]),
    }


def project_feeds(raw):
    data = _payload(raw)
    if not isinstance(data, list):
        raise shape_error()
    items = [_feed(entry) for entry in data[:MAX_FEEDS]]
    return {
        "state": "ok" if items else "no_rows",
        "items": items,
        "quality": {"truncated": len(data) > MAX_FEEDS, "returned_items": len(items)},
    }


def project_quota(raw):
    data = _record(_payload(raw), ("DailyQuota", "MonthlyQuota"))
    return {"state": "ok", "item": {"daily_quota": _count(data["DailyQuota"]), "monthly_quota": _count(data["MonthlyQuota"])}}


def _utc_now():
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def envelope(source, operation, state, *, now=_utc_now, **fields):
    return {"schema_version": SCHEMA_VERSION, "source": source, "operation": operation, "state": state, **fields, "fetched_at": now()}


def write_envelope(req, state, *, quota_before=None, vendor_status=None, now=_utc_now):
    return envelope(
        req.source,
        req.operation,
        state,
        now=now,
        dry_run=req.dry_run,
        target=req.url,
        submitted_count=len(req.urls) if req.urls else 1,
        urls=list(req.urls),
        quota_before=quota_before,
        vendor_status=vendor_status,
    )


def encode_response(payload):
    data = json.dumps(payload, separators=(",", ":"), sort_keys=True).encode("utf-8")
    if len(data) > MAX_RESPONSE:
        raise ReportError(502, "response_too_large")
    return data
