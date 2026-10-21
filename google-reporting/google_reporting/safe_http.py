"""Pinned aiohttp adapter: parser errors occur before application middleware."""

import asyncio
import logging
import signal
from aiohttp import web
from aiohttp.web_protocol import RequestHandler
from aiohttp.web_server import Server

LOG = logging.getLogger("google_reporting")


class SafeRequestHandler(RequestHandler):
    def handle_error(self, request, status=500, exc=None, message=None):
        # aiohttp's default interpolates raw request bytes into 400 responses and
        # exception logs. Never pass its diagnostic text through this boundary.
        code = "invalid_http_request" if status < 500 else "internal_error"
        LOG.info("route=unknown code=%s duration_ms=0", code)
        response = web.json_response({"error": code}, status=status)
        response.force_close()
        return response


class SafeServer(Server):
    def __call__(self):
        # Deliberately fail closed on an incompatible pinned aiohttp signature.
        return SafeRequestHandler(self, loop=self._loop, **self._kwargs)


class SafeAppRunner(web.AppRunner):
    async def _make_server(self):
        original = await super()._make_server()
        return SafeServer(
            original.request_handler,
            request_factory=original.request_factory,
            handler_cancellation=original.handler_cancellation,
            loop=original._loop,
            **original._kwargs,
        )


async def serve_app(app, bind, port=8092):
    runner = SafeAppRunner(
        app,
        access_log=None,
        handler_cancellation=True,
        max_line_size=8192,
        max_field_size=8192,
    )
    await runner.setup()
    stop = asyncio.Event()
    loop = asyncio.get_running_loop()
    for signum in (signal.SIGTERM, signal.SIGINT):
        loop.add_signal_handler(signum, stop.set)
    try:
        await web.TCPSite(runner, bind, port).start()
        await stop.wait()
    finally:
        await runner.cleanup()
        for signum in (signal.SIGTERM, signal.SIGINT):
            loop.remove_signal_handler(signum)
