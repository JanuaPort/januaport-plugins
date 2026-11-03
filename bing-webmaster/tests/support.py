"""Shared synthetic fixtures: configuration, a loopback vendor stub and a log capture.

Only synthetic values. The Bing key below is a loud canary so that a leak in any log,
answer or exception text is recognisable by plain substring search.
"""

import asyncio
import json
import logging
from aiohttp import web
from bing_webmaster.domain import Config

BRIDGE = b"synthetic-bing-webmaster-bridge-key-0123456789"
BEARER = {"Authorization": "Bearer " + BRIDGE.decode()}
LEAK_CANARY = "CANARYbingKEYsynthetic0123456789abcd"
BING_KEY = LEAK_CANARY
INDEXNOW_KEY = "synthetic-indexnow-key-0123456789"
SITE = "https://www.example.test/"
HOSTS = ["example.test", "www.example.test"]
KEY_LOCATION = "https://www.example.test/" + INDEXNOW_KEY + ".txt"


def config_raw(**over):
    raw = {
        "site_url": SITE,
        "hosts": list(HOSTS),
        "indexnow": {"key": INDEXNOW_KEY, "key_location": KEY_LOCATION},
    }
    raw.update(over)
    return raw


def config(**over):
    return Config.from_dict(config_raw(**over))


URL_INFO_OK = {
    "d": {
        "__type": "UrlInfo:#Microsoft.Bing.Webmaster.Api",
        "AnchorCount": 50,
        "DiscoveryDate": "/Date(1315349995266-0700)/",
        "DocumentSize": 2048,
        "HttpStatus": 200,
        "IsPage": True,
        "LastCrawledDate": "/Date(1316213995266-0700)/",
        "TotalChildUrlCount": 100,
        "Url": "https://vendor-field.example.test/must-not-appear",
        "SomethingNew": "must-not-appear",
    }
}
FEEDS_OK = {
    "d": [
        {
            "__type": "Feed:#Microsoft.Bing.Webmaster.Api",
            "Compressed": False,
            "FileSize": 1024,
            "LastCrawled": "/Date(1315781995040-0700)/",
            "Status": "Success",
            "Submitted": "/Date(1316213995040-0700)/",
            "Type": "Sitemap",
            "Url": "https://www.example.test/sitemap.xml",
            "UrlCount": 1023,
            "SomethingNew": "must-not-appear",
        }
    ]
}
QUOTA_OK = {
    "d": {
        "__type": "UrlSubmissionQuota:#Microsoft.Bing.Webmaster.Api",
        "DailyQuota": 500,
        "MonthlyQuota": 10000,
    }
}
NULL_OK = {"d": None}


class VendorStub:
    """Loopback stand-in for ssl.bing.com and api.indexnow.org; counts every request."""

    def __init__(self):
        self.calls = []
        self.handler = None  # callable(request, body) -> web.Response, optional
        self.replies = {}  # method name -> (status, body) overrides
        self.delay = 0
        self.runner = None
        self.port = None

    @property
    def bing_base(self):
        return "http://127.0.0.1:%d/webmaster/api.svc/json" % self.port

    @property
    def indexnow_url(self):
        return "http://127.0.0.1:%d/indexnow" % self.port

    def reply(self, name, status, body):
        self.replies[name] = (status, body)

    async def _serve(self, request):
        body = await request.read()
        name = request.path.rsplit("/", 1)[-1]
        self.calls.append(
            {
                "method": request.method,
                "path": request.path,
                "name": name,
                "query": dict(request.query),
                "raw_query": request.query_string,
                "headers": dict(request.headers),
                "body": body,
            }
        )
        if self.delay:
            await asyncio.sleep(self.delay)
        if self.handler is not None:
            return self.handler(request, body)
        status, payload = self.replies.get(name, self.default(name))
        raw = payload if isinstance(payload, bytes) else json.dumps(payload).encode()
        return web.Response(status=status, body=raw, content_type="application/json")

    def default(self, name):
        return {
            "GetUrlInfo": (200, URL_INFO_OK),
            "GetFeeds": (200, FEEDS_OK),
            "GetUrlSubmissionQuota": (200, QUOTA_OK),
            "SubmitUrlBatch": (200, NULL_OK),
            "SubmitFeed": (200, NULL_OK),
            "indexnow": (200, b""),
        }[name]

    async def start(self):
        app = web.Application()
        app.router.add_route("*", "/{tail:.*}", self._serve)
        self.runner = web.AppRunner(app, access_log=None)
        await self.runner.setup()
        site = web.TCPSite(self.runner, "127.0.0.1", 0)
        await site.start()
        self.port = site._server.sockets[0].getsockname()[1]

    async def stop(self):
        await self.runner.cleanup()


class LogCapture(logging.Handler):
    """All records of the whole process, rendered with their exception text."""

    def __init__(self):
        super().__init__(logging.DEBUG)
        self.lines = []

    def emit(self, record):
        text = record.getMessage()
        if record.exc_info:
            text += " " + repr(record.exc_info[1])
        self.lines.append(text)

    def __enter__(self):
        self.root = logging.getLogger()
        self.before = self.root.level
        self.root.addHandler(self)
        self.root.setLevel(logging.DEBUG)
        return self

    def __exit__(self, *exc):
        self.root.removeHandler(self)
        self.root.setLevel(self.before)

    @property
    def text(self):
        return "\n".join(self.lines)
