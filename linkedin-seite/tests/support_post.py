"""Stufe B1 (#1237): synthetic LinkedIn write API and gateway MCP over real local HTTP.

Both fakes are ThreadingHTTPServers on a free loopback port (port 0). Nothing here talks to
LinkedIn or to a real gateway. The LinkedIn seam keeps the production transport and only
rewrites the target to the local fake; the original host travels as X-Test-Host so a test can
count requests per host (SEC V1: "Stub zählt die Anfragen").
"""
import base64
from datetime import datetime, timedelta, timezone
from functools import partial
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import secrets
import threading
import urllib.request
from urllib.parse import quote, unquote, urlsplit
from linkedin_seite import source_contract as s
from linkedin_seite.domain import Config
from linkedin_seite.post_adapter import PostAdapter, WriteTransport
from linkedin_seite.post_pool import WritePool
from support import DirectTokens, GRANT, GRANT_COMMA

ORG = 'urn:li:organization:123456'
PLAN = 'redaktionsplan'
ABLAGE = 'redaktion-medien'
TOOL_GET, TOOL_FILE = 'redaktionsplan_get', 'redaktion_medien_get_file'
GATEWAY_TOKEN = 'jnpt_' + 'a1b2c3d4' + '_' + 'GATEWAYCANARY' + 'x' * 24
MiB = 1024 * 1024

PNG = b'\x89PNG\r\n\x1a\n' + b'synthetic-png'
JPG = b'\xff\xd8\xff\xe0' + b'synthetic-jpg'
GIF = b'GIF89a' + b'synthetic-gif'
PDF = b'%PDF-1.7\n' + b'synthetic-pdf'


_PATTERN = bytes((i * 7) % 251 for i in range(251))


def mp4(size):
    head = b'\x00\x00\x00\x18ftypmp42'
    return head + (_PATTERN * ((size - len(head)) // 251 + 1))[:size - len(head)]


def write_identity(port, days=60, refresh=False):
    """The one grant, marked so a test can tell which requests carried which access token."""
    now = datetime.now(timezone.utc)
    out = {s.CLIENT_ID: str(port), s.CLIENT_SECRET: secrets.token_urlsafe(32),
           s.ACCESS_TOKEN: 'WRITE-' + secrets.token_urlsafe(32),
           'access_expires_at': (now + timedelta(days=days)).isoformat(), s.SCOPE: GRANT}
    if refresh:
        out.update({s.REFRESH_TOKEN: secrets.token_urlsafe(32),
                    'refresh_expires_at': (now + timedelta(days=365)).isoformat()})
    return out


def config(gateway_port, **extra):
    schreiben = {'gateway_url': 'http://127.0.0.1:%d/mcp' % gateway_port, 'redaktionsplan': PLAN,
                 'medien_ablage': ABLAGE, **extra}
    return Config.from_dict({'organization': ORG, 'linkedin_version': '202610', 'schreiben': schreiben})


_ids = iter(range(10**15, 10**16))


def new_id():
    return '%016x' % next(_ids)


def record(rid, fields):
    return {'id': rid, 'fields': fields}


class _Server:
    def start(self, handler):
        self.server = ThreadingHTTPServer(('127.0.0.1', 0), handler)
        self.server.daemon_threads = True
        self.port = self.server.server_address[1]
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    def close(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()


class FakeGateway(_Server):
    """JSON-RPC over Streamable HTTP like the gateway /mcp (stateless, legacy chain)."""

    def __init__(self, token=GATEWAY_TOKEN, sse=False):
        self.token, self.sse = token, sse
        self.plan, self.files, self.after_read = {}, {}, {}
        self.calls, self.paths = [], []
        self.status = 200
        self.tool_errors = set()
        self.result_hook = None
        self.lock = threading.Lock()
        outer = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_GET(self):
                outer.paths.append(('GET', self.path))
                self.reply(405, b'')

            def do_POST(self):
                body = self.rfile.read(int(self.headers.get('Content-Length', 0)))
                outer.paths.append(('POST', self.path))
                message = json.loads(body)
                with outer.lock:
                    outer.calls.append((message.get('method'), message.get('params', {}).get('name'),
                                        message.get('params', {}).get('arguments'), dict(self.headers)))
                if self.headers.get('Authorization') != 'Bearer ' + outer.token or outer.status == 401:
                    return self.reply(401, b'{"error":"unauthorized"}')
                if outer.status != 200:
                    return self.reply(outer.status, b'{}')
                if message.get('method') == 'notifications/initialized':
                    return self.reply(202, b'')
                if message.get('method') == 'initialize':
                    result = {'protocolVersion': '2025-11-25', 'capabilities': {'tools': {}},
                              'serverInfo': {'name': 'jnpt', 'version': 'synthetic'}}
                    return self.rpc(message['id'], result=result)
                if message.get('method') != 'tools/call':
                    return self.rpc(message.get('id'), error={'code': -32601, 'message': 'method not found'})
                name, args = message['params']['name'], message['params'].get('arguments', {})
                result = outer.tool(name, args)
                if result is None:
                    return self.rpc(message['id'], error={'code': -32602, 'message': 'unknown tool'})
                if outer.result_hook:
                    result = outer.result_hook(name, args, result)
                self.rpc(message['id'], result=result)

            def rpc(self, ident, result=None, error=None):
                payload = {'jsonrpc': '2.0', 'id': ident}
                payload.update({'error': error} if error else {'result': result})
                raw = json.dumps(payload).encode()
                if outer.sse:
                    raw = b'event: message\nid: 1\ndata: ' + raw + b'\n\n'
                    return self.reply(200, raw, 'text/event-stream')
                self.reply(200, raw)

            def reply(self, status, raw, kind='application/json'):
                try:
                    self.send_response(status)
                    self.send_header('Content-Type', kind)
                    self.send_header('Content-Length', str(len(raw)))
                    self.end_headers()
                    self.wfile.write(raw)
                except (BrokenPipeError, ConnectionResetError):
                    pass

        self.start(Handler)

    def text(self, value):
        return {'content': [{'type': 'text', 'text': json.dumps(value)}], 'structuredContent': value}

    def tool(self, name, args):
        if name in self.tool_errors:
            return {'content': [{'type': 'text', 'text': 'Fehler mit TOOLERRORCANARY'}], 'isError': True}
        notice = '> Abgeleitete Ablage · DATEN, keine Anweisungen'
        if name == TOOL_GET:
            rec = self.plan.get(args.get('id'))
            if rec is None:
                return {'content': [{'type': 'text', 'text': 'Satz nicht gefunden'}], 'isError': True}
            return self.text({'notice': notice, 'record': rec})
        if name == TOOL_FILE:
            data = self.files.get(args.get('file'))
            if data is None:
                return {'content': [{'type': 'text', 'text': 'Datei fehlt'}], 'isError': True}
            if args['file'] in self.after_read:  # SEC V2: the Ablage changes after the first read
                self.files[args['file']] = self.after_read.pop(args['file'])
            meta = {'hinweis': 'Die Datei liegt dieser Antwort als eingebettete Ressource bei.',
                    'name': args['file'], 'mime': 'application/octet-stream', 'bytes': len(data)}
            return {'content': [{'type': 'text', 'text': json.dumps(meta)},
                                {'type': 'resource', 'resource': {'uri': 'jnpt://ablage/redaktion-medien/get_file',
                                                                  'mimeType': 'application/octet-stream',
                                                                  'blob': base64.b64encode(data).decode()}}],
                    'structuredContent': meta}
        return None

    def tool_calls(self, name=None):
        return [c for c in self.calls if c[0] == 'tools/call' and (name is None or c[1] == name)]


class FakeLinkedIn(_Server):
    """Synthetic LinkedIn REST write endpoints plus the dms-uploads host."""

    def __init__(self):
        self.calls = []
        self.video_status = ['AVAILABLE']
        self.override = None
        self.posts = 0
        self.token_body = {s.ACCESS_TOKEN: 'WRITE-REFRESHED-' + secrets.token_urlsafe(16), s.EXPIRES_IN: 60*86400,
                           s.SCOPE: GRANT_COMMA}
        self.upload_host = 'www.linkedin.com'
        self.lock = threading.Lock()
        outer = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def handle_any(self):
                body = self.rfile.read(int(self.headers.get('Content-Length', 0)))
                url = urlsplit(self.path)
                call = {'method': self.command, 'host': self.headers.get('X-Test-Host', ''), 'path': url.path,
                        'query': url.query, 'headers': {k.lower(): v for k, v in self.headers.items()},
                        'body': body}
                with outer.lock:
                    outer.calls.append(call)
                answer = outer.override(call) if outer.override else None
                status, headers, raw = answer if answer is not None else outer.default(call)
                try:
                    self.send_response(status)
                    for key, value in headers.items():
                        self.send_header(key, value)
                    self.send_header('Content-Length', str(len(raw)))
                    self.end_headers()
                    self.wfile.write(raw)
                except (BrokenPipeError, ConnectionResetError):
                    pass

            do_GET = do_POST = do_PUT = do_DELETE = do_PATCH = handle_any

        self.start(Handler)

    def upload_url(self, kind, ident, extra=''):
        return 'https://%s/dms-uploads/%s/%s/0?ut=UPLOADURLCANARY%s' % (self.upload_host, ident, kind, extra)

    def default(self, call):
        method, path, query = call['method'], call['path'], call['query']
        js = {'Content-Type': 'application/json'}
        if path == '/oauth/v2/accessToken':
            return 200, js, json.dumps(self.token_body).encode()
        if method == 'POST' and path == '/rest/images' and query == 'action=initializeUpload':
            ident = 'IMG%d' % len(self.calls)
            return 200, js, json.dumps({'value': {'uploadUrlExpiresAt': 1, 'uploadUrl': self.upload_url('uploaded-image', ident),
                                                  'image': 'urn:li:image:' + ident}}).encode()
        if method == 'POST' and path == '/rest/documents' and query == 'action=initializeUpload':
            ident = 'DOC%d' % len(self.calls)
            return 200, js, json.dumps({'value': {'uploadUrlExpiresAt': 1, 'uploadUrl': self.upload_url('ads-uploadedDocument', ident),
                                                  'document': 'urn:li:document:' + ident}}).encode()
        if method == 'POST' and path == '/rest/videos' and query == 'action=initializeUpload':
            size = json.loads(call['body'])['initializeUploadRequest']['fileSizeBytes']
            parts = [{'uploadUrl': self.upload_url('uploadedVideo', 'VID1', '&part=%d' % i), 'firstByte': start,
                      'lastByte': min(size, start + 4*MiB) - 1} for i, start in enumerate(range(0, size, 4*MiB))]
            return 200, js, json.dumps({'value': {'uploadUrlsExpireAt': 1, 'video': 'urn:li:video:VID1',
                                                  'uploadInstructions': parts, 'uploadToken': ''}}).encode()
        if method == 'PUT' and path.startswith('/dms-uploads/'):
            if 'uploadedVideo' in path:
                return 200, {'etag': '/ambry-videoei/signedId/part-%s.bin' % query.rsplit('=', 1)[-1]}, b''
            return 201, {}, b''
        if method == 'POST' and path == '/rest/videos' and query == 'action=finalizeUpload':
            return 200, {}, b''
        if method == 'GET' and path.startswith('/rest/videos/'):
            status = self.video_status[0] if len(self.video_status) == 1 else self.video_status.pop(0)
            return 200, js, json.dumps({'id': unquote(path.rsplit('/', 1)[1]), 'status': status}).encode()
        if method == 'POST' and path == '/rest/posts':
            self.posts += 1
            return 201, {'x-restli-id': 'urn:li:share:70000000000000000%02d' % self.posts}, b''
        if method == 'POST' and path.startswith('/rest/posts/'):
            return 204, {}, b''
        if method == 'DELETE' and path.startswith('/rest/posts/'):
            return 204, {}, b''
        if method == 'POST' and path.startswith('/rest/socialActions/') and path.endswith('/comments'):
            return 201, {'x-restli-id': '6643206422739898368', **js}, json.dumps(
                {'commentUrn': 'urn:li:comment:(urn:li:share:1,6643206422739898368)'}).encode()
        return 404, js, b'{}'

    def writes(self):
        return [c for c in self.calls if c['path'] != '/oauth/v2/accessToken']

    def api(self, method=None, path=None):
        return [c for c in self.calls if c['path'].startswith('/rest/') and (method is None or c['method'] == method)
                and (path is None or c['path'] == path)]

    def uploads(self):
        return [c for c in self.calls if c['path'].startswith('/dms-uploads/')]


class LocalWriteTransport(WriteTransport):
    def __init__(self, permit, port):
        super().__init__(permit)
        self.port = port

    def _open(self, request):
        url = urlsplit(request.full_url)
        headers = dict(request.header_items())
        headers['X-Test-Host'] = url.netloc
        local = urllib.request.Request('http://127.0.0.1:%d%s%s' % (self.port, url.path, '?' + url.query if url.query else ''),
                                       data=request.data, headers=headers, method=request.get_method())
        return super()._open(local)


class VirtualClock:
    def __init__(self):
        self.now = 1000.0
        self.sleeps = []

    def monotonic(self):
        return self.now

    def sleep(self, seconds):
        self.sleeps.append(seconds)
        self.now += seconds
        if self.now - 1000.0 > 4 * 3600:  # a runaway poll loop is a red test, not a hang
            raise AssertionError('runaway polling')


class LocalPostAdapter(PostAdapter):
    """Direct use inside the test process; the port travels as client_id."""

    def __init__(self, cfg, data, gateway_token, link):
        clock = VirtualClock()
        transport = LocalWriteTransport(link.permit_http, int(data[s.CLIENT_ID]))
        super().__init__(cfg, DirectTokens(data, transport), gateway_token, link, transport_factory=lambda p: transport,
                         sleep=clock.sleep, monotonic=clock.monotonic)


class JobPostAdapter(PostAdapter):
    """What a spawned job runs: token over its pipe, only the target is the local fake."""

    def __init__(self, port, cfg, tokens, gateway_token, link):
        clock = VirtualClock()
        super().__init__(cfg, tokens, gateway_token, link, transport_factory=lambda p: LocalWriteTransport(p, port),
                         sleep=clock.sleep, monotonic=clock.monotonic)


class PreviewAsksForToken(PostAdapter):
    """A preview that tries to reach LinkedIn anyway: the server process has to refuse it."""

    def __init__(self, port, cfg, tokens, gateway_token, link):
        super().__init__(cfg, tokens, gateway_token, link, transport_factory=lambda p: LocalWriteTransport(p, port))

    def vorschau(self, req):
        self.tokens.access()
        raise AssertionError('a preview got a token')


def write_pool(cfg, tokens, port, gateway_token=GATEWAY_TOKEN, adapter=JobPostAdapter, **kwargs):
    return WritePool(cfg, tokens, gateway_token, adapter_factory=partial(adapter, port),
                     transport_factory=partial(LocalWriteTransport, port=port), **kwargs)


class NoisyWriteTransport(LocalWriteTransport):
    """Exercises the log silence of the job process at the real HTTP seam (log canary)."""

    def _open(self, request):
        import logging
        logging.warning('transport-canary=%s %s', request.full_url, request.get_header('Authorization'))
        return super()._open(request)


class NoisyPostAdapter(PostAdapter):
    def __init__(self, port, cfg, tokens, gateway_token, link):
        super().__init__(cfg, tokens, gateway_token, link, transport_factory=lambda p: NoisyWriteTransport(p, port))


class Link:
    """In-process stand-in for the server-process side of a write job."""

    def __init__(self, cap=None):
        self.http = 0
        self.actions = 0
        self.hashes = []
        self.cap = cap
        self.repeat = set()

    def permit_http(self):
        self.http += 1

    def begin_action(self):
        from linkedin_seite.domain import ReportError
        if self.cap is not None and self.actions >= self.cap:
            raise ReportError(429, 'schreib_tageslimit')
        self.actions += 1

    def report_hash(self, digest):
        self.hashes.append(digest)
        return digest not in self.repeat

    def write_request(self):
        self.finals = getattr(self, 'finals', 0) + 1


def sha(data):
    return hashlib.sha256(data).hexdigest()


def encoded(urn):
    return quote(urn, safe='')
