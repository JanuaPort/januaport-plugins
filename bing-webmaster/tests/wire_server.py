"""Optional cross-repo wire probe: the real HTTP service and the real adapter against a vendor stub.

No Bing or IndexNow request leaves the machine; the stub answers on loopback with the
documented shapes. The Bridge key below is synthetic.
"""

import argparse
import asyncio
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from bing_webmaster.adapter import BingAdapter
from bing_webmaster.safe_http import serve_app
from bing_webmaster.server import create_app
from support import BING_KEY, VendorStub, config

WIRE_BRIDGE_KEY = b"synthetic-bing-webmaster-bridge-contract-no-real-key"


async def serve(port):
    stub = VendorStub()
    await stub.start()
    adapter = BingAdapter(config(), BING_KEY, bing_base=stub.bing_base, indexnow_url=stub.indexnow_url)
    await serve_app(create_app(config(), WIRE_BRIDGE_KEY, adapter), "127.0.0.1", port)
    await stub.stop()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, default=18093)
    args = parser.parse_args()
    asyncio.run(serve(args.port))


if __name__ == "__main__":
    main()
