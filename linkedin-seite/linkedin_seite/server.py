"""Private HTTP boundary. Local health never contacts LinkedIn."""

import asyncio
import hmac
import ipaddress
import json
import logging
import os
from pathlib import Path
import time
from aiohttp import web
from .domain import Config, ReportError, parse_request, encode_response, strict_json, REPORTS
from .workers import WorkerPool

LOG = logging.getLogger("linkedin_seite")

# Directory mounts of the gateway key places (rotation-safe, unlike file mounts):
# plugin-linkedin-seite = the one Bridge key, plugin-linkedin-seite-oauth
# = the OAuth JSON. Both are read once at start.
CONFIG_PATH = "/etc/linkedin-seite/config.json"
BRIDGE_KEY_PATH = "/run/secrets/jnpt/runtime-key"
IDENTITY_PATH = "/run/secrets/oauth/runtime-key"


def read_key(path):
    value = Path(path).read_bytes().strip()
    if not 32 <= len(value) <= 256 or any(c < 33 or c > 126 for c in value):
        raise ValueError("invalid_bridge_key")
    return value


def load_runtime_files(bridge_key_path=BRIDGE_KEY_PATH, identity_path=IDENTITY_PATH):
    from .oauth import load_identity

    load_identity(identity_path)
    return read_key(bridge_key_path)


def create_app(cfg, key, pool):
    @web.middleware
    async def warnings(request, handler):
        response = await handler(request)
        extra = pool.warning()
        if extra and response.body:
            data = json.loads(response.body)
            data.update(extra)
            response.body = encode_response(data)
        return response

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
            if len(parts) != 4 or parts[1:3] != ["v1", "linkedin"] or parts[3] not in REPORTS:
                raise ReportError(404, "route_not_found")
            source, report = parts[2:]
            route = source + "/" + report
            if request.method != "GET":
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
            if request.can_read_body:
                raise ReportError(400, "unexpected_body")
            req = parse_request(report, list(request.query.items()))
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
            return web.Response(body=payload, content_type="application/json")
        except ReportError as exc:
            code = exc.code
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

    app = web.Application(middlewares=[warnings, errors], client_max_size=8192)

    async def fallback(request):
        return web.Response(status=404)

    app.router.add_route("*", "/{tail:.*}", fallback)

    async def cleanup(app):
        await pool.close()

    app.on_cleanup.append(cleanup)
    return app


def bind_ip(value):
    ip = ipaddress.ip_address(value)
    if ip.version != 4 or not ip.is_private or ip.is_unspecified or ip.is_loopback:
        raise ValueError("invalid_bind_ip")
    return str(ip)


def serve():
    logging.basicConfig(level=logging.INFO, format="%(message)s")
    # Library/access logs may embed full URLs or response bodies.
    for name in ("aiohttp", "urllib3"):
        logging.getLogger(name).setLevel(logging.CRITICAL)
    cfg = Config.from_dict(strict_json(Path(CONFIG_PATH).read_bytes()))
    key = load_runtime_files()
    # Exact plugin-network IPv4 is provisioned by the operator, never 0.0.0.0.
    bind = bind_ip(os.environ.get("LINKEDIN_SEITE_BIND_IP", ""))
    from .oauth import load_identity
    pool = WorkerPool(cfg, load_identity(IDENTITY_PATH))
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
