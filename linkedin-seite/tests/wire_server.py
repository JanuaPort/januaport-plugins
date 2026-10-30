"""Real subprocess service for log-canary probes; synthetic secrets arrive over stdin."""
import asyncio
import json
import logging
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from aiohttp import web
from linkedin_seite.safe_http import SafeAppRunner
from linkedin_seite.server import create_app
from linkedin_seite.workers import WorkerPool
from linkedin_seite.linkedin_adapter import LinkedInAdapter
from support import CFG, LocalTransport


class NoisyTransport(LocalTransport):
    def _open(self, request):
        # Exercise the library-log suppression at the real worker/HTTP seam.
        logging.warning('transport-canary=%s', request.get_header('Authorization'))
        return super()._open(request)


class NoisyAdapter(LinkedInAdapter):
    def __init__(self, cfg, data, permit):
        super().__init__(cfg, data, permit, lambda p: NoisyTransport(p, int(data['client_id'])))


async def serve(data):
    pool = WorkerPool(CFG, data['oauth'], adapter_factory=NoisyAdapter)
    runner = SafeAppRunner(create_app(CFG, data['bridge'].encode(), pool), access_log=None)
    await runner.setup()
    site = web.TCPSite(runner, '127.0.0.1', 0)
    await site.start()
    print(site._server.sockets[0].getsockname()[1], flush=True)
    try:
        await asyncio.Event().wait()
    finally:
        await runner.cleanup()


if __name__ == '__main__':
    logging.basicConfig(level=logging.INFO, format='%(message)s')
    asyncio.run(serve(json.loads(sys.stdin.readline())))
