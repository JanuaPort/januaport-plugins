"""Real subprocess service with the community side for log-canary probes (Stufe B3, #1237).

Synthetic secrets and the fake port arrive over stdin; prints the bound port, then serves.
The job adapter logs on purpose at the HTTP seam: job processes must stay silent.
"""
import asyncio
import json
import logging
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from aiohttp import web
from linkedin_seite.oauth import Tokens
from linkedin_seite.safe_http import SafeAppRunner
from linkedin_seite.server import create_app
from support import read_pool
from support_kommentare import NOW, community_pool, config
from support_post import LocalWriteTransport
from linkedin_seite.community_adapter import CommunityAdapter


class NoisyTransport(LocalWriteTransport):
    def _open(self, request):
        logging.warning('transport-canary=%s %s %s', request.full_url, request.get_header('Authorization'), request.data)
        return super()._open(request)


class NoisyCommunityAdapter(CommunityAdapter):
    def __init__(self, port, cfg, tokens, link):
        super().__init__(cfg, tokens, link, transport_factory=lambda p: NoisyTransport(p, port), now=lambda: NOW)


async def serve(data):
    cfg = config()
    tokens = Tokens(data['oauth'])
    pool = read_pool(cfg, tokens, data['linkedin_port'])
    community = community_pool(cfg, tokens, data['linkedin_port'], adapter=NoisyCommunityAdapter, abstand=0)
    runner = SafeAppRunner(create_app(cfg, data['bridge'].encode(), pool, None, community), access_log=None)
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
