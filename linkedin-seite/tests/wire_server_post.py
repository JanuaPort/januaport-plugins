"""Real subprocess service with read AND write side for log-canary probes (Stufe B1, #1237).

Synthetic secrets and fake ports arrive over stdin; prints the bound port, then serves.
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
from support_post import NoisyPostAdapter, config, write_pool


async def serve(data):
    cfg = config(data['gateway_port'])
    tokens = Tokens(data['oauth'])  # one state, as in production; the read fake's port travels as client_id
    pool = read_pool(cfg, tokens, int(data['oauth']['client_id']))
    writes = write_pool(cfg, tokens, data['linkedin_port'], data['gateway_token'], adapter=NoisyPostAdapter)
    runner = SafeAppRunner(create_app(cfg, data['bridge'].encode(), pool, writes), access_log=None)
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
