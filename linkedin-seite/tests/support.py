"""Synthetic HTTP LinkedIn server; production transport with loopback-only test seam."""
from datetime import datetime, timedelta, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import re
import secrets
import threading
import time
import urllib.request
from urllib.parse import urlsplit, parse_qs
from linkedin_seite import source_contract as s
from linkedin_seite.domain import Config
from linkedin_seite.oauth import Transport
from linkedin_seite.linkedin_adapter import LinkedInAdapter

ORG = 'urn:li:organization:123456'
CFG = Config.from_dict({'organization': ORG, 'linkedin_version': '202610'})


def identity(port=123, days=60, refresh=False):
    now = datetime.now(timezone.utc)
    out = {s.CLIENT_ID: str(port), s.CLIENT_SECRET: secrets.token_urlsafe(32),
           s.ACCESS_TOKEN: secrets.token_urlsafe(32),
           'access_expires_at': (now + timedelta(days=days)).isoformat()}
    if refresh:
        out.update({s.REFRESH_TOKEN: secrets.token_urlsafe(32),
                    'refresh_expires_at': (now + timedelta(days=365)).isoformat()})
    return out


class LocalTransport(Transport):
    def __init__(self, permit, port):
        super().__init__(permit)
        self.port = port

    def _open(self, request):
        url = urlsplit(request.full_url)
        local = urllib.request.Request('http://127.0.0.1:%d%s?%s' % (self.port, url.path, url.query),
                                       data=request.data, headers=dict(request.headers), method=request.method)
        return super()._open(local)


class LocalAdapter(LinkedInAdapter):
    def __init__(self, cfg, data, permit):
        super().__init__(cfg, data, permit, lambda p: LocalTransport(p, int(data[s.CLIENT_ID])))


class Fake:
    def __init__(self):
        self.calls = []
        self.status = 200
        self.body = None
        self.delay = 0
        self.location = '/leak'
        self.token_body = {s.ACCESS_TOKEN: secrets.token_urlsafe(32), s.EXPIRES_IN: 60*86400}
        outer = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_GET(self):
                self.answer()

            def do_POST(self):
                self.answer()

            def answer(self):
                parsed = urlsplit(self.path)
                params = parse_qs(parsed.query)
                body = self.rfile.read(int(self.headers.get('Content-Length', 0)))
                outer.calls.append((self.command, parsed.path, params, dict(self.headers), body))
                time.sleep(outer.delay)
                data = outer.token_body if parsed.path == '/oauth/v2/accessToken' else outer.default(parsed.path, params)
                if outer.body is not None:
                    data = outer.body(parsed.path, params, data) if callable(outer.body) else outer.body
                raw = data if isinstance(data, bytes) else json.dumps(data).encode()
                try:
                    self.send_response(outer.status)
                    self.send_header('Content-Type', 'application/json')
                    self.send_header('Content-Length', str(len(raw)))
                    self.send_header('Location', outer.location)
                    self.end_headers()
                    self.wfile.write(raw)
                except (BrokenPipeError, ConnectionResetError):
                    pass

        self.server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        self.server.daemon_threads = True
        self.port = self.server.server_address[1]
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    def close(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()

    def default(self, endpoint, params):
        if endpoint.startswith('/rest/' + s.NETWORK + '/'):
            return {s.NETWORK_SIZE: 10, 'person': 'PRIVATE-CANARY'}
        if endpoint == '/rest/posts':
            return {s.ELEMENTS: [{s.ID: 'urn:li:share:789', s.AUTHOR: ORG,
                                 s.CREATED: 1793145600000, s.TEXT: 'Synthetischer Beitrag ' * 12,
                                 s.LIFECYCLE: s.PUBLISHED, 'person': 'PRIVATE-CANARY'}]}
        aggregate = {s.SHARE_STATS: {s.IMPRESSION: 10, s.CLICK: 2, s.REACTION: 3, s.COMMENT: 4, s.SHARE: 5}}
        org_key = s.ENTITY
        if endpoint == '/rest/' + s.PAGE:
            aggregate = {s.PAGE_STATS: {s.VIEWS: {s.ALL_VIEWS: {s.PAGE_VIEWS: 6}}}}
            org_key = s.ORGANIZATION
        if endpoint == '/rest/' + s.FOLLOWERS:
            aggregate = {s.FOLLOWER_GAIN: {s.ORGANIC_GAIN: 8, s.PAID_GAIN: 2}}
        row = {org_key: ORG, **aggregate, 'followerCountsByCountry': [{'member': 'PRIVATE-CANARY'}],
               'followerCountsBySeniority': 'PRIVATE-CANARY', 'name': 'PRIVATE-CANARY'}
        for param, field in ((s.SHARE_IDS, s.SHARE_ID), (s.UGC_IDS, s.UGC_ID)):
            if param in params:
                row[field] = params[param][0][5:-1]
        if s.INTERVALS not in params:
            return {s.ELEMENTS: [row]}
        start, end = map(int, re.search(r'start:(\d+),end:(\d+)', params[s.INTERVALS][0]).groups())
        return {s.ELEMENTS: [{**row, s.INTERVAL: {s.START: tick, s.END: tick+86400000}}
                              for tick in range(start, end, 86400000)]}
