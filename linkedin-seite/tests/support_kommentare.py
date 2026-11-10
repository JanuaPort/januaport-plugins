"""Stufe B3 (#1237): synthetic LinkedIn Posts/Comments API for comments, replies and the daily list.

A ThreadingHTTPServer on a free loopback port. Nothing here talks to LinkedIn. The production
transport is kept; only the target is rewritten to the fake (as in support_post). Every comment
element carries the canaries a real answer may carry: author name and headline (`actor~`), the
person URN of the author, of likers and of the acting admin (`agent`). None of them may ever leave
the plugin. Comment URNs follow the documented form urn:li:comment:(urn:li:activity:<n>,<n>).
"""
from datetime import datetime, timezone
from functools import partial
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
import secrets
import threading
import time
from urllib.parse import parse_qs, quote, unquote, urlsplit
from linkedin_seite import source_contract as s
from linkedin_seite.community_adapter import CommunityAdapter
from linkedin_seite.community_pool import CommunityPool
from linkedin_seite.domain import Config
from support import DirectTokens, GRANT
from support_post import LocalWriteTransport

ORG = 'urn:li:organization:123456'
FREMDE_ORG = 'urn:li:organization:999'
DRITTER = 'urn:li:person:PersonCanaryA1'      # a third party; must never be output
DRITTE = 'urn:li:person:PersonCanaryB2'
EIGENE_PERSON = 'urn:li:person:EigenePersonCanary'
NAME = 'NAMECANARY Mustermann'
HEADLINE = 'HEADLINECANARY bei Firma'
# The test clock: the real time at the first import, handed to spawned jobs over the environment, so the
# service's own clock (daily list window) and the fakes agree in every process.
NOW = datetime.fromtimestamp(int(os.environ.setdefault('LINKEDIN_KOMMENTARE_TEST_NOW',
                                                       str(int(datetime.now(timezone.utc).timestamp())))), timezone.utc)
NOW_MS = int(NOW.timestamp() * 1000)
DAY_MS = 86400000
_ids = iter(range(7100000000000000000, 7200000000000000000, 7))


def new_id():
    return str(next(_ids))


def ms(days_ago, hours=0):
    return NOW_MS - int(days_ago * DAY_MS) - int(hours * 3600000)


def identity(port, days=60):
    from datetime import timedelta
    return {s.CLIENT_ID: str(port), s.CLIENT_SECRET: secrets.token_urlsafe(32),
            s.ACCESS_TOKEN: 'KOMM-' + secrets.token_urlsafe(32),
            'access_expires_at': (datetime.now(timezone.utc) + timedelta(days=days)).isoformat(), s.SCOPE: GRANT}


def config(eigene=(EIGENE_PERSON,)):
    raw = {'organization': ORG, 'linkedin_version': '202610'}
    if eigene is not None:
        raw['kommentare'] = {'eigene_personen': list(eigene)}
    return Config.from_dict(raw)


def kurn(activity, cid):
    return 'urn:li:comment:(urn:li:activity:%s,%s)' % (activity, cid)


class FakeCommunity:
    """Posts, top-level comments per post and replies per comment, as LinkedIn returns them."""

    def __init__(self):
        self.posts, self.threads, self.comments, self.replies = {}, {}, {}, {}
        self.calls = []
        self.override = None        # call -> (status, headers, raw) or None
        self.summary = True         # include commentsSummary in comment elements
        self.on_reply = None        # hook(parent_urn, body) after a created reply
        self.list_total = None      # paging.total override for comment lists
        self.lock = threading.Lock()
        outer = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def handle_any(self):
                body = self.rfile.read(int(self.headers.get('Content-Length', 0)))
                url = urlsplit(self.path)
                call = {'method': self.command, 'host': self.headers.get('X-Test-Host', ''), 'path': url.path,
                        'query': parse_qs(url.query), 'raw_query': url.query,
                        'headers': {k.lower(): v for k, v in self.headers.items()}, 'body': body,
                        't': time.monotonic()}
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

        self.server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        self.server.daemon_threads = True
        self.port = self.server.server_address[1]
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    def close(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()

    # --- building state --------------------------------------------------------------------

    def post(self, days_ago=1, author=ORG, state='PUBLISHED', kind='share'):
        urn = 'urn:li:%s:%s' % (kind, new_id())
        self.posts[urn] = {'id': urn, 'author': author, 'createdAt': ms(days_ago), 'publishedAt': ms(days_ago),
                           'lifecycleState': state, 'commentary': 'Eigener Beitragstext', 'visibility': 'PUBLIC'}
        self.threads[urn] = new_id()
        self.comments[urn] = []
        return urn

    def element(self, activity, actor, text, created, modified=None, parent=None, cid=None, **extra):
        cid = cid or new_id()
        urn = kurn(activity, cid)
        element = {'actor': actor, 'commentUrn': urn, 'id': cid,
                   'created': {'actor': actor, 'time': created},
                   'lastModified': {'actor': actor, 'time': modified if modified is not None else created},
                   'likesSummary': {'aggregatedTotalLikes': 1, 'likedByCurrentUser': False, 'totalLikes': 1,
                                    'selectedLikes': ['urn:li:like:(urn:li:person:LikeCanary9,%s)' % urn]},
                   'message': {'attributes': [], 'text': text}, 'object': 'urn:li:activity:' + activity,
                   'actor~': {'localizedFirstName': NAME, 'localizedHeadline': HEADLINE, 'vanityName': 'vanitycanary'}}
        if actor == ORG:
            element['agent'] = 'urn:li:person:AgentCanary7'
            element['created']['impersonator'] = 'urn:li:person:AgentCanary7'
        if parent is not None:
            element['parentComment'] = parent
        element.update(extra)
        return element

    def comment(self, post_urn, actor=DRITTER, text='Toller Beitrag! TEXTCANARY', days_ago=0.5, modified_days_ago=None,
                **extra):
        modified = None if modified_days_ago is None else ms(modified_days_ago)
        element = self.element(self.threads[post_urn], actor, text, ms(days_ago), modified, **extra)
        self.comments[post_urn].append(element)
        self.replies.setdefault(element['commentUrn'], [])
        return element['commentUrn']

    def reply(self, comment_urn, actor=ORG, text='Danke für Ihr Feedback.', days_ago=0.25):
        activity = comment_urn.split('activity:')[1].split(',')[0]
        element = self.element(activity, actor, text, ms(days_ago), parent=comment_urn)
        self.replies.setdefault(comment_urn, []).append(element)
        return element['commentUrn']

    def find(self, comment_urn):
        for elements in self.comments.values():
            for element in elements:
                if element['commentUrn'] == comment_urn:
                    return element
        return None

    # --- answering ---------------------------------------------------------------------------

    def with_summary(self, element):
        out = dict(element)
        if self.summary:
            count = len(self.replies.get(element['commentUrn'], []))
            out['commentsSummary'] = {'totalFirstLevelComments': count, 'aggregatedTotalComments': count,
                                      'selectedComments': [r['commentUrn'] for r in self.replies.get(element['commentUrn'], [])[:1]]}
        return out

    def default(self, call):
        js = {'Content-Type': 'application/json'}
        method, path = call['method'], call['path']
        if path == '/oauth/v2/accessToken':
            return 200, js, json.dumps({s.ACCESS_TOKEN: 'KOMM-REFRESHED', s.EXPIRES_IN: 60*86400, s.SCOPE: GRANT}).encode()
        if method == 'GET' and path.startswith('/rest/posts/'):
            post = self.posts.get(unquote(path[len('/rest/posts/'):]))
            return (200, js, json.dumps(post).encode()) if post else (404, js, b'{"status":404}')
        if method == 'GET' and path == '/rest/posts':
            count = int(call['query'].get('count', ['10'])[0])
            rows = sorted(self.posts.values(), key=lambda p: -p['createdAt'])
            rows = [p for p in rows if p['author'] == call['query'].get('author', [''])[0]][:count]
            return 200, js, json.dumps({'elements': rows, 'paging': {'start': 0, 'count': count, 'links': []}}).encode()
        if path.startswith('/rest/socialActions/') and path.endswith('/comments'):
            target = unquote(path[len('/rest/socialActions/'):-len('/comments')])
            if method == 'GET':
                if target in self.comments:
                    rows = [self.with_summary(e) for e in self.comments[target]]
                elif target in self.replies:
                    rows = [dict(e) for e in self.replies[target]]
                else:
                    return 404, js, b'{"status":404}'
                total = self.list_total if self.list_total is not None else len(rows)
                return 200, js, json.dumps({'elements': rows, 'paging': {'start': 0, 'count': 100, 'total': total,
                                                                         'links': []}}).encode()
            if method == 'POST':
                body = json.loads(call['body'])
                if target not in self.replies:
                    return 404, js, b'{"status":404}'
                activity = target.split('activity:')[1].split(',')[0]
                element = self.element(activity, body['actor'], body['message']['text'], NOW_MS - 1000, parent=target)
                self.replies[target].append(element)
                if self.on_reply:
                    self.on_reply(target, body)
                return 201, {**js, 'x-restli-id': element['id']}, json.dumps(element).encode()
        return 404, js, b'{}'

    # --- inspection --------------------------------------------------------------------------

    def api(self, method=None, prefix=''):
        return [c for c in self.calls if c['path'].startswith('/rest/' + prefix) and (method is None or c['method'] == method)]

    def replies_posted(self):
        return [c for c in self.api('POST', 'socialActions/')]


class LocalCommunityAdapter(CommunityAdapter):
    """Direct use inside the test process; the port travels as client_id."""

    def __init__(self, cfg, data, link, now=NOW):
        transport = LocalWriteTransport(link.permit_http, int(data[s.CLIENT_ID]))
        super().__init__(cfg, DirectTokens(data, transport), link, transport_factory=lambda p: transport,
                         now=lambda: now)


class JobCommunityAdapter(CommunityAdapter):
    """What a spawned community job runs: token over its pipe, only the target is the local fake."""

    def __init__(self, port, cfg, tokens, link):
        super().__init__(cfg, tokens, link, transport_factory=lambda p: LocalWriteTransport(p, port), now=lambda: NOW)


class Link:
    """In-process stand-in for the server-process side of a community job."""

    def __init__(self, http_cap=None):
        self.http = 0
        self.writes = 0
        self.http_cap = http_cap

    def permit_http(self):
        from linkedin_seite.domain import ReportError
        if self.http_cap is not None and self.http >= self.http_cap:
            raise ReportError(429, 'kommentare_http_tageslimit')
        self.http += 1

    def write_request(self):
        self.writes += 1


def community_pool(cfg, tokens, port, adapter=JobCommunityAdapter, **kwargs):
    return CommunityPool(cfg, tokens, adapter_factory=partial(adapter, port),
                         transport_factory=partial(LocalWriteTransport, port=port), **kwargs)


def encoded(urn):
    return quote(urn, safe='')
