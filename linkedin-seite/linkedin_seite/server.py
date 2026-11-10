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
from .redaktion import ROUTES as POST_ROUTES, VORSCHAU, FREIGABE_HEADER, parse_freigabe, parse_vorschau, parse_write
from . import kommentare
from .workers import WorkerPool

LOG = logging.getLogger("linkedin_seite")

# Directory mounts of the gateway key places (rotation-safe, unlike file mounts):
# plugin-linkedin-seite = the one Bridge key, plugin-linkedin-seite-oauth
# = the OAuth JSON. Both are read once at start.
CONFIG_PATH = "/etc/linkedin-seite/config.json"
BRIDGE_KEY_PATH = "/run/secrets/jnpt/runtime-key"
IDENTITY_PATH = "/run/secrets/oauth/runtime-key"
# Stufe B1 (#1237): the Beisteller's own gateway token. Reading and writing share the one grant above.
GATEWAY_TOKEN_PATH = "/run/secrets/gateway/runtime-key"
POST_PREFIX = ["v1", "linkedin-post"]
MAX_POST_BODY = 1024
# Stufe B3 (#1237, 0.4.0): own prefix, own core template linkedin-seite-community, three tools.
COMMUNITY_PREFIX = ["v1", "linkedin-kommentare"]
COMMUNITY_ROUTES = {"comments": "GET", "tagesliste": "GET", "reply": "POST"}


def read_key(path):
    value = Path(path).read_bytes().strip()
    if not 32 <= len(value) <= 256 or any(c < 33 or c > 126 for c in value):
        raise ValueError("invalid_bridge_key")
    return value


def load_runtime_files(bridge_key_path=BRIDGE_KEY_PATH, identity_path=IDENTITY_PATH):
    from .oauth import load_identity

    load_identity(identity_path)
    return read_key(bridge_key_path)


def read_gateway_token(path):
    value = Path(path).read_bytes().strip()
    if not 16 <= len(value) <= 1024 or any(c < 33 or c > 126 for c in value):
        raise ValueError("invalid_gateway_token")
    return value.decode("ascii")


def load_write_side(cfg, token_path=GATEWAY_TOKEN_PATH):
    """(gateway token, "an") or (None, fixed reason). Never breaks Stufe A."""
    if cfg.schreiben is None:
        return None, "nicht_konfiguriert"
    try:
        token = read_gateway_token(token_path)
    except FileNotFoundError:
        return None, "gateway_token_fehlt"
    except (OSError, ValueError):
        return None, "gateway_token_ungueltig"
    return token, "an"


def build_pools(cfg, identity_path=IDENTITY_PATH, token_path=GATEWAY_TOKEN_PATH):
    """All pools on ONE Tokens object (#1237): two on one refresh token would race.

    The community pool (B3) takes no gateway token: replies need no editorial plan (SEC S1).
    """
    from .community_pool import CommunityPool
    from .oauth import Tokens, load_identity
    from .post_pool import WritePool

    tokens = Tokens(load_identity(identity_path))
    token, reason = load_write_side(cfg, token_path)
    LOG.info("schreiben=%s", reason)
    return WorkerPool(cfg, tokens), WritePool(cfg, tokens, token) if token else None, CommunityPool(cfg, tokens)


def check_bridge(request, key):
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


async def bounded_body(request, limit=MAX_POST_BODY):
    if request.content_length is not None and request.content_length > limit:
        raise ReportError(400, "request_too_large")
    raw = b""
    async for chunk in request.content.iter_chunked(1024):
        raw += chunk
        if len(raw) > limit:
            raise ReportError(400, "request_too_large")
    return raw


async def until_done(request, coroutine):
    """Run a pool call and cancel it when the client goes away (aiohttp does not cancel handlers)."""
    task = asyncio.create_task(coroutine)
    try:
        while not task.done():
            if request.transport is None or request.transport.is_closing():
                task.cancel()
                raise asyncio.CancelledError()
            await asyncio.wait({task}, timeout=0.025)
        return await task
    finally:
        if not task.done():
            task.cancel()
            try:
                await task
            except asyncio.CancelledError:
                pass


def create_app(cfg, key, pool, write_pool=None, community_pool=None):
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
        write_route = request.path.startswith("/v1/linkedin-post/") and request.method == "POST"
        reply_route = request.path == "/v1/linkedin-kommentare/reply" and request.method == "POST"
        dispatched = False
        header_checked = False
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
            if len(parts) == 4 and parts[1:3] == POST_PREFIX and parts[3] in POST_ROUTES:
                # Stufe B1 (#1237): own prefix, preview GET, the four write tools POST.
                route = "linkedin-post/" + parts[3]
                if request.method != ("GET" if parts[3] == VORSCHAU else "POST"):
                    raise ReportError(405, "method_not_allowed")
                check_bridge(request, key)
                if parts[3] == VORSCHAU:
                    if request.body_exists:
                        raise ReportError(400, "unexpected_body")
                    req = parse_vorschau(list(request.query.items()))
                else:
                    # A valid bridge and header form precede definite local rejections.
                    parse_freigabe(request.headers.getall(FREIGABE_HEADER, []))
                    header_checked = True
                    req = parse_write(parts[3], await bounded_body(request), request.headers.getall(FREIGABE_HEADER, []))
                if write_pool is None:
                    raise ReportError(503, "schreiben_nicht_eingerichtet")
                dispatched = True
                status, payload = await write_pool.run(req)
                extra = write_pool.warning()
                if extra:
                    payload = encode_response({**json.loads(payload), **extra})
                return web.Response(status=status, body=payload, content_type="application/json")
            if len(parts) == 4 and parts[1:3] == COMMUNITY_PREFIX and parts[3] in COMMUNITY_ROUTES:
                # Stufe B3 (#1237): after a valid bridge every definite refusal of reply is HTTP 200
                # with nicht_ausgefuehrt; after dispatch only with the IPC proof of no send attempt.
                route = "linkedin-kommentare/" + parts[3]
                if request.method != COMMUNITY_ROUTES[parts[3]]:
                    raise ReportError(405, "method_not_allowed")
                check_bridge(request, key)
                if parts[3] == "reply":
                    header_checked = True
                    req = kommentare.parse_reply(await bounded_body(request, kommentare.MAX_REPLY_BODY))
                else:
                    if request.body_exists:
                        raise ReportError(400, "unexpected_body")
                    pairs = list(request.query.items())
                    req = (kommentare.parse_comments(pairs) if parts[3] == "comments"
                           else kommentare.parse_tagesliste(pairs))
                if community_pool is None:
                    raise ReportError(503, "kommentare_nicht_eingerichtet")
                dispatched = True
                payload = await until_done(request, community_pool.run(req))
                return web.Response(body=payload, content_type="application/json")
            if len(parts) != 4 or parts[1:3] != ["v1", "linkedin"] or parts[3] not in REPORTS:
                raise ReportError(404, "route_not_found")
            source, report = parts[2:]
            route = source + "/" + report
            if request.method != "GET":
                raise ReportError(405, "method_not_allowed")
            check_bridge(request, key)
            if request.can_read_body:
                raise ReportError(400, "unexpected_body")
            req = parse_request(report, list(request.query.items()))
            payload = await until_done(request, pool.run(req))
            return web.Response(body=payload, content_type="application/json")
        except ReportError as exc:
            code = exc.code
            payload = {"error": code}
            if ((write_route or reply_route) and header_checked and code != 'nicht_freigegeben'
                    and (not dispatched or getattr(exc, 'nicht_ausgefuehrt', False))):
                payload['nicht_ausgefuehrt'] = True
                return web.json_response(payload, status=200)
            return web.json_response(payload, status=exc.status)
        except asyncio.CancelledError:
            code = "client_cancelled"
            raise
        except Exception:
            code = "internal_error"
            payload = {"error": code}
            if (write_route or reply_route) and header_checked and not dispatched:
                payload['nicht_ausgefuehrt'] = True
                return web.json_response(payload, status=200)
            return web.json_response(payload, status=503)
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
        if community_pool is not None:
            await community_pool.close()
        if write_pool is not None:
            await write_pool.close()
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
    pool, write_pool, community_pool = build_pools(cfg)
    from .safe_http import serve_app

    asyncio.run(serve_app(create_app(cfg, key, pool, write_pool, community_pool), bind))


def main():
    try:
        serve()
    except Exception:
        # Startup/configuration failures must not print SDK/input tracebacks.
        LOG.critical("startup_failed")
        raise SystemExit(1) from None


if __name__ == "__main__":
    main()
