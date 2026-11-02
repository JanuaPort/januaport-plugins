"""Private HTTP boundary. Local health never contacts Bing or IndexNow."""

import asyncio
from datetime import datetime, timezone
import hmac
import ipaddress
import json
import logging
import os
from pathlib import Path
import re
import time
from aiohttp import web
from .adapter import BingAdapter
from .domain import (
    GET_OPS,
    MAX_BODY,
    POST_OPS,
    Config,
    ReportError,
    encode_response,
    parse_get,
    parse_post,
    write_envelope,
)

LOG = logging.getLogger("bing_webmaster")

# Directory mounts of the two gateway key places (rotation-safe, unlike file mounts):
# plugin-bing-webmaster = the Bridge key, plugin-bing-webmaster-bing = the Bing API key.
# Both are read once at start.
CONFIG_PATH = "/etc/bing-webmaster/config.json"
BRIDGE_KEY_PATH = "/run/secrets/jnpt/runtime-key"
BING_KEY_PATH = "/run/secrets/bing/runtime-key"
BUDGET_SECONDS = 20
QUIET_LOGGERS = ("aiohttp", "aiohttp.client", "aiohttp.server", "aiohttp.web", "aiohttp.access", "asyncio", "urllib3")
BING_KEY = re.compile(rb"[A-Za-z0-9_-]{16,128}")


def read_bridge_key(path):
    value = Path(path).read_bytes().strip()
    if not 32 <= len(value) <= 256 or any(c < 33 or c > 126 for c in value):
        raise ValueError("invalid_bridge_key")
    return value


def read_bing_key(path):
    value = Path(path).read_bytes().strip()
    if not BING_KEY.fullmatch(value):
        raise ValueError("invalid_bing_key")
    return value


def load_runtime_files(bridge_key_path=BRIDGE_KEY_PATH, bing_key_path=BING_KEY_PATH):
    bridge = read_bridge_key(bridge_key_path)
    bing = read_bing_key(bing_key_path)
    if bridge == bing:
        raise ValueError("keys_must_differ")
    return bridge, bing.decode("ascii")


def quiet_libraries():
    # Library loggers may carry full URLs (and with them ?apikey=) in messages and tracebacks.
    for name in QUIET_LOGGERS:
        logging.getLogger(name).setLevel(logging.CRITICAL)


def route_kind(parts):
    """read (GET), write (POST) or None for an unknown route."""
    if len(parts) != 4 or parts[1] != "v1":
        return None
    source, operation = parts[2:]
    if operation in GET_OPS.get(source, ()):
        return "read"
    return "write" if operation in POST_OPS.get(source, ()) else None


class WriteCap:
    """Attempts per write route and UTC day, counted in this long-running process."""

    def __init__(self, limit, today):
        self.limit = limit
        self.today = today
        self.attempts = {}

    def count(self, route):
        day = self.today()
        self.attempts = {k: v for k, v in self.attempts.items() if k[1] == day}
        key = (route, day)
        if self.attempts.get(key, 0) >= self.limit:
            raise ReportError(429, "write_cap_reached")
        self.attempts[key] = self.attempts.get(key, 0) + 1


async def bounded_body(request):
    if request.content_length is not None and request.content_length > MAX_BODY:
        raise ReportError(400, "request_too_large")
    raw = b""
    async for chunk in request.content.iter_chunked(4096):
        raw += chunk
        if len(raw) > MAX_BODY:
            raise ReportError(400, "request_too_large")
    return raw


def authorize(request, key):
    header = request.headers.getall("Authorization", [])
    if len(header) != 1:
        raise ReportError(401, "bridge_unauthorized")
    try:
        authorized = hmac.compare_digest(header[0].encode("ascii"), b"Bearer " + key)
    except UnicodeError:
        authorized = False
    if not authorized:
        raise ReportError(403, "bridge_forbidden")


def utc_day():
    return datetime.now(timezone.utc).date().isoformat()


def create_app(cfg, key, adapter, *, today=utc_day, budget=BUDGET_SECONDS):
    cap = WriteCap(cfg.write_cap, today)

    async def execute(req):
        if req.write:
            if req.source == "indexnow" and cfg.indexnow is None:
                raise ReportError(503, "not_configured")
            if req.dry_run:
                return encode_response(write_envelope(req, "preview"))
            cap.count(req.source + "/" + req.operation)
        try:
            async with asyncio.timeout(budget):
                return await adapter.run(req)
        except TimeoutError:
            raise ReportError(504, "budget_exceeded") from None

    @web.middleware
    async def errors(request, handler):
        started = time.monotonic()
        code = "ok"
        route = "unknown"
        try:
            if len(request.raw_path.encode()) > 8192 or len(request.path) > 512:
                raise ReportError(400, "request_too_large")
            if request.path == "/healthz":
                route = "healthz"
                if request.method != "GET":
                    raise ReportError(405, "method_not_allowed")
                return web.Response(body=b'{"state":"healthy"}', content_type="application/json")
            parts = request.path.split("/")
            kind = route_kind(parts)
            if kind is None:
                raise ReportError(404, "route_not_found")
            source, operation = parts[2:]
            route = source + "/" + operation
            if request.method != ("POST" if kind == "write" else "GET"):
                raise ReportError(405, "method_not_allowed")
            authorize(request, key)
            if kind == "write":
                req = parse_post(cfg, source, operation, await bounded_body(request))
            elif request.can_read_body:
                raise ReportError(400, "unexpected_body")
            else:
                req = parse_get(cfg, source, operation, list(request.query.items()))
            return web.Response(body=await execute(req), content_type="application/json")
        except ReportError as exc:
            code = exc.code
            return web.json_response({"error": code}, status=exc.status)
        except asyncio.CancelledError:
            code = "client_cancelled"
            raise
        except Exception:
            # Never the exception text: transport errors embed the URL with the key.
            code = "internal_error"
            return web.json_response({"error": code}, status=503)
        finally:
            LOG.info("route=%s code=%s duration_ms=%d", route, code, int((time.monotonic() - started) * 1000))

    app = web.Application(middlewares=[errors], client_max_size=8192)

    async def fallback(request):
        return web.Response(status=404)

    app.router.add_route("*", "/{tail:.*}", fallback)

    async def cleanup(app):
        await adapter.close()

    app.on_cleanup.append(cleanup)
    return app


def bind_address():
    # Exact plugin-network IPv4 is provisioned by the operator, never 0.0.0.0.
    bind = os.environ.get("BING_WEBMASTER_BIND_IP", "")
    ip = ipaddress.ip_address(bind)
    if ip.version != 4 or not ip.is_private or ip.is_unspecified or ip.is_loopback:
        raise ValueError("invalid_bind_ip")
    return bind


def serve():
    logging.basicConfig(level=logging.INFO, format="%(message)s")
    quiet_libraries()
    cfg = Config.from_dict(json.loads(Path(CONFIG_PATH).read_text()))
    bridge, bing = load_runtime_files()
    bind = bind_address()
    from .safe_http import serve_app

    asyncio.run(serve_app(create_app(cfg, bridge, BingAdapter(cfg, bing)), bind))


def main():
    try:
        serve()
    except Exception:
        # Startup/configuration failures must not print tracebacks or input values.
        LOG.critical("startup_failed")
        raise SystemExit(1) from None


if __name__ == "__main__":
    main()
