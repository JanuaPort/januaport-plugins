"""Private HTTP boundary. Local health never contacts Google."""

import asyncio
from datetime import datetime, timezone
import hmac
import ipaddress
import json
import logging
import os
from pathlib import Path
import time
from aiohttp import web
from .domain import Config, ReportError, parse_request, encode_response, REPORTS
from .manage import (
    GET_OPS,
    MAX_BODY,
    POST_OPS,
    change_line,
    parse_get,
    parse_post,
)
from .workers import WorkerPool

LOG = logging.getLogger("google_reporting")

# Directory mounts of the gateway key places (rotation-safe, unlike file mounts):
# plugin-google-reporting = the one Bridge key, plugin-google-reporting-google
# = the service account JSON. Both are read once at start.
CONFIG_PATH = "/etc/google-reporting/config.json"
BRIDGE_KEY_PATH = "/run/secrets/jnpt/runtime-key"
IDENTITY_PATH = "/run/secrets/google/runtime-key"


def read_key(path):
    value = Path(path).read_bytes().strip()
    if not 32 <= len(value) <= 256 or any(c < 33 or c > 126 for c in value):
        raise ValueError("invalid_bridge_key")
    return value


def load_runtime_files(bridge_key_path=BRIDGE_KEY_PATH, identity_path=IDENTITY_PATH):
    from .google_adapter import load_identity

    load_identity(identity_path)
    return read_key(bridge_key_path)


def route_kind(parts):
    """report (GET, fixed reports), read (GET, manage lists) or write (POST); else None."""
    if len(parts) != 4 or parts[1] != "v1" or parts[2] not in REPORTS:
        return None
    source, op = parts[2:]
    if op in REPORTS[source]:
        return "report"
    if op in GET_OPS[source]:
        return "read"
    return "write" if op in POST_OPS[source] else None


async def bounded_body(request):
    if request.content_length is not None and request.content_length > MAX_BODY:
        raise ReportError(400, "request_too_large")
    raw = await request.content.read(MAX_BODY + 1)
    if len(raw) > MAX_BODY:
        raise ReportError(400, "request_too_large")
    return raw


def log_change(source, operation, payload):
    """One fixed-format line per applied write; previews and reads leave none (SEC P4)."""
    result = json.loads(payload)
    if result.get("state") == "applied":
        LOG.info(change_line(source, operation, result))


def create_app(cfg, key, pool):
    @web.middleware
    async def errors(request, handler):
        started = time.monotonic()
        code = "ok"
        route = "unknown"
        try:
            if len(request.raw_path.encode()) > 8192:
                raise ReportError(400, "request_too_large")
            if len(request.path) > 512:
                raise ReportError(400, "request_too_large")
            if request.path == "/healthz":
                route = "healthz"
                if request.method != "GET":
                    raise ReportError(405, "method_not_allowed")
                return web.Response(
                    body=b'{"state":"healthy"}', content_type="application/json"
                )
            parts = request.path.split("/")
            kind = route_kind(parts)
            if kind is None:
                raise ReportError(404, "route_not_found")
            source, report = parts[2:]
            route = source + "/" + report
            if request.method != ("POST" if kind == "write" else "GET"):
                raise ReportError(405, "method_not_allowed")
            header = request.headers.getall("Authorization", [])
            if len(header) != 1:
                raise ReportError(401, "bridge_unauthorized")
            expected = b"Bearer " + key
            try:
                authorized = hmac.compare_digest(header[0].encode("ascii"), expected)
            except UnicodeError:
                authorized = False
            if not authorized:
                raise ReportError(403, "bridge_forbidden")
            if kind == "write":
                req = parse_post(cfg, source, report, await bounded_body(request))
            elif request.can_read_body:
                raise ReportError(400, "unexpected_body")
            elif kind == "read":
                req = parse_get(cfg, source, report, list(request.query.items()))
            else:
                req = parse_request(cfg, source, report, list(request.query.items()))
            if source == "gsc" and report == "query_summary" and not cfg.query_enabled:
                raise ReportError(503, "query_not_approved")
            if source == "ga4" and report == "key_events" and cfg.key_events is None:
                from .domain import project

                payload = encode_response(project(cfg, req, {}))
            else:
                task = asyncio.create_task(pool.run(req))
                try:
                    while not task.done():
                        if request.transport is None or request.transport.is_closing():
                            task.cancel()
                            raise asyncio.CancelledError()
                        await asyncio.wait({task}, timeout=0.025)
                    payload = await task
                finally:
                    if not task.done():
                        task.cancel()
                        try:
                            await task
                        except asyncio.CancelledError:
                            pass
            if kind == "write":
                log_change(source, report, payload)
            return web.Response(body=payload, content_type="application/json")
        except ReportError as exc:
            code = exc.code
            if route in ("gsc/get_status", "ga4/get_status") and exc.status in (
                502,
                503,
                504,
            ):
                state = {
                    "source_access_denied": "access_denied",
                    "source_quota_limited": "quota_limited",
                    "configuration_invalid": "not_configured",
                }.get(code, "unavailable")
                return web.json_response(
                    {
                        "schema_version": "1.0",
                        "source": route.split("/")[0],
                        "report": "get_status",
                        "state": state,
                        "checked_at": datetime.now(timezone.utc).isoformat(),
                        "definition_version": cfg.definition_version,
                    }
                )
            return web.json_response({"error": code}, status=exc.status)
        except asyncio.CancelledError:
            code = "client_cancelled"
            raise
        except Exception:
            code = "internal_error"
            return web.json_response({"error": code}, status=503)
        finally:
            LOG.info(
                "route=%s code=%s duration_ms=%d",
                route,
                code,
                int((time.monotonic() - started) * 1000),
            )

    app = web.Application(middlewares=[errors], client_max_size=8192)

    async def fallback(request):
        return web.Response(status=404)

    app.router.add_route("*", "/{tail:.*}", fallback)

    async def cleanup(app):
        await pool.close()

    app.on_cleanup.append(cleanup)
    return app


def serve():
    logging.basicConfig(level=logging.INFO, format="%(message)s")
    # Library/access logs may embed full URLs or response bodies.
    for name in ("aiohttp", "google", "googleapiclient", "urllib3", "httplib2"):
        logging.getLogger(name).setLevel(logging.CRITICAL)
    cfg = Config.from_dict(json.loads(Path(CONFIG_PATH).read_text()))
    key = load_runtime_files()
    bind = os.environ.get("GOOGLE_REPORTING_BIND_IP", "")
    # Exact plugin-network IPv4 is provisioned by the operator, never 0.0.0.0.
    ip = ipaddress.ip_address(bind)
    if ip.version != 4 or not ip.is_private or ip.is_unspecified or ip.is_loopback:
        raise ValueError("invalid_bind_ip")
    pool = WorkerPool(cfg, IDENTITY_PATH)
    from .safe_http import serve_app

    asyncio.run(serve_app(create_app(cfg, key, pool), bind))


def main():
    try:
        serve()
    except Exception:
        # Startup/configuration failures must not print SDK/input tracebacks.
        LOG.critical("startup_failed")
        raise SystemExit(1) from None


if __name__ == "__main__":
    main()
