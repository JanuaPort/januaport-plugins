"""Vendor transport for Bing Webmaster (JSON endpoint) and IndexNow.

The Bing API takes its key only as `?apikey=` in the URL. That URL, and with it the key, is
inside the message of nearly every library exception and may be mirrored by an error body.
So the rules here are structural: failures are classified into fixed codes inside the
`except` clause and raised after it (no exception chain stays reachable), the exception text
and the response body of a failure are never read into a message, redirects are not followed,
the environment (proxies) is ignored, and nothing in this module logs.
"""

import asyncio
import json
import aiohttp
from .domain import (
    ReportError,
    encode_response,
    envelope,
    project_feeds,
    project_quota,
    project_url_info,
    shape_error,
    write_envelope,
)

# Documented in Microsoft Learn "bing-webmaster-dotnet" (IWebmasterApi) and indexnow.org.
BING_BASE = "https://ssl.bing.com/webmaster/api.svc/json"
INDEXNOW_URL = "https://api.indexnow.org/indexnow"
JSON_TYPE = "application/json; charset=utf-8"
MAX_VENDOR_BODY = 256 * 1024
BING_INVALID_API_KEY = 3  # ApiFault.ErrorCode for InvalidApiKey (task brief, live to confirm)


def _failure(status, code):
    return ReportError(status, code)


def classify_bing(status, raw):
    """Fixed code for a non-200 Bing answer. The error message of the body is never used."""
    if 300 <= status < 400:
        return _failure(502, "source_redirect")
    if status == 429:
        return _failure(502, "source_quota_limited")
    if status == 400:
        try:
            fault = json.loads(raw.decode("utf-8"))
        except (ValueError, RecursionError):
            fault = None
        if isinstance(fault, dict) and fault.get("ErrorCode") == BING_INVALID_API_KEY:
            return _failure(502, "bing_api_key_invalid")
    if 400 <= status < 500:
        return _failure(502, "source_rejected")
    return _failure(502, "source_unavailable")


INDEXNOW_CODES = {400: "source_rejected", 403: "indexnow_key_invalid", 422: "indexnow_url_rejected", 429: "source_quota_limited"}


def classify_indexnow(status):
    if 300 <= status < 400:
        return _failure(502, "source_redirect")
    return _failure(502, INDEXNOW_CODES.get(status, "source_unavailable"))


def parse_json(raw):
    try:
        return json.loads(raw.decode("utf-8"))
    except (ValueError, RecursionError):
        pass
    raise shape_error()


class BingAdapter:
    def __init__(self, cfg, key, *, bing_base=BING_BASE, indexnow_url=INDEXNOW_URL, request_timeout=10, session=None):
        self._cfg = cfg
        self._key = key
        self._base = bing_base
        self._indexnow_url = indexnow_url
        self._timeout = request_timeout
        self._session = session

    def __repr__(self):
        return "BingAdapter()"

    async def _ensure_session(self):
        if self._session is None:
            self._session = aiohttp.ClientSession(
                trust_env=False,
                cookie_jar=aiohttp.DummyCookieJar(),
                timeout=aiohttp.ClientTimeout(total=self._timeout),
            )
        return self._session

    async def close(self):
        if self._session is not None:
            await self._session.close()
            self._session = None

    async def _send(self, method, url, *, params=None, body=None):
        """(status, body bytes, oversize) or a ReportError with a fixed code; no retry."""
        failure = None
        result = None
        try:
            session = await self._ensure_session()
            options = {"allow_redirects": False, "headers": {"Content-Type": JSON_TYPE, "Accept": "application/json"}}
            if params is not None:
                options["params"] = params
            if body is not None:
                options["data"] = json.dumps(body, separators=(",", ":")).encode("utf-8")
            async with session.request(method, url, **options) as response:
                chunks, size, oversize = [], 0, False
                async for chunk in response.content.iter_chunked(8192):
                    size += len(chunk)
                    if size > MAX_VENDOR_BODY:
                        oversize = True
                        break
                    chunks.append(chunk)
                result = (response.status, b"".join(chunks), oversize)
        except TimeoutError:
            failure = _failure(504, "budget_exceeded")
        except Exception:  # noqa: BLE001 - text of any transport exception can embed the key
            failure = _failure(502, "source_unavailable")
        if failure is not None:
            raise failure
        return result

    async def _bing(self, method, name, *, query=None, body=None):
        params = dict(query or {})
        params["apikey"] = self._key
        if body is None:
            params = {"siteUrl": self._cfg.site_url, **params}
        status, raw, oversize = await self._send(method, self._base + "/" + name, params=params, body=body)
        if status != 200:
            raise classify_bing(status, raw)
        if oversize:
            raise shape_error()
        return parse_json(raw)

    async def _quota(self):
        return project_quota(await self._bing("GET", "GetUrlSubmissionQuota"))["item"]["daily_quota"]

    async def run(self, req):
        handler = getattr(self, "_" + req.source + "_" + req.operation)
        return encode_response(await handler(req))

    async def _bing_url_info(self, req):
        projection = project_url_info(req.url, await self._bing("GET", "GetUrlInfo", query={"url": req.url}))
        return envelope("bing", "url_info", projection.pop("state"), **projection)

    async def _bing_sitemaps(self, req):
        projection = project_feeds(await self._bing("GET", "GetFeeds"))
        return envelope("bing", "sitemaps", projection.pop("state"), **projection)

    async def _bing_submission_quota(self, req):
        projection = project_quota(await self._bing("GET", "GetUrlSubmissionQuota"))
        return envelope("bing", "submission_quota", projection.pop("state"), **projection)

    async def _write_answer(self, name, body):
        if await self._bing("POST", name, body=body) != {"d": None}:
            raise shape_error()

    async def _bing_submit_urls(self, req):
        available = await self._quota()
        if available < len(req.urls):
            raise ReportError(429, "source_quota_exceeded")
        await self._write_answer("SubmitUrlBatch", {"siteUrl": self._cfg.site_url, "urlList": list(req.urls)})
        return write_envelope(req, "applied", quota_before=available)

    async def _bing_submit_sitemap(self, req):
        await self._write_answer("SubmitFeed", {"siteUrl": self._cfg.site_url, "feedUrl": req.url})
        return write_envelope(req, "applied")

    async def _indexnow_submit_urls(self, req):
        settings = self._cfg.indexnow
        if settings is None:
            raise ReportError(503, "not_configured")
        body = {"host": settings.host, "key": settings.key, "keyLocation": settings.key_location, "urlList": list(req.urls)}
        status, _raw, _oversize = await self._send("POST", self._indexnow_url, body=body)
        if status not in (200, 202):
            raise classify_indexnow(status)
        return write_envelope(req, "applied", vendor_status=status)
