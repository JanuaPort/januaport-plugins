"""Stufe B3 (#1237): HTTP boundary, spawned community jobs, queue, spacing, lock and log canary.

Binding: cut c-6747016143 (B3/B4: top level only, queue with 60 s spacing, 10 minute lock per comment),
SEC c-6747087768 (S1 no Redaktionsplan, S3 no counter in the plugin), T1 c-6759978021.
"""
import ast
import asyncio
import inspect
import json
from pathlib import Path
import secrets
import subprocess
import sys
import time
import unittest
import urllib.error
import urllib.request
from aiohttp.test_utils import TestClient, TestServer
from linkedin_seite import community_pool as cp
from linkedin_seite import oauth
from linkedin_seite import server
from linkedin_seite import source_contract as s
from linkedin_seite.community_adapter import CommunityAdapter
from linkedin_seite.safe_http import SafeAppRunner
from support import Fake, read_pool
from support_kommentare import (DRITTE, DRITTER, HEADLINE, NAME, ORG, FakeCommunity, JobCommunityAdapter, community_pool,
                                config, identity)
from support_post import LocalWriteTransport

KEY = 'SYNTHETIC-' + 'c' * 32
AUTH = {'Authorization': 'Bearer ' + KEY}
ROOT = Path(__file__).resolve().parents[1]


class SafeTestServer(TestServer):
    async def _make_runner(self, **kwargs):
        return SafeAppRunner(self.app, access_log=None, max_line_size=8192, max_field_size=8192, **kwargs)


class Clock:
    def __init__(self):
        self.offset = 0.0

    def __call__(self):
        return time.monotonic() + self.offset


class ReadThatWrites(CommunityAdapter):
    """A read job that asks for a write anyway: the server process refuses it."""

    def __init__(self, port, cfg, tokens, link):
        super().__init__(cfg, tokens, link, transport_factory=lambda p: LocalWriteTransport(p, port))

    def comments(self, req):
        self.link.write_request()
        raise AssertionError('a read job got a write permit')


class SlowJob(JobCommunityAdapter):
    def reply(self, req):
        result = super().reply(req)
        time.sleep(3)
        return result


class Service(unittest.IsolatedAsyncioTestCase):
    abstand = 0.0

    async def asyncSetUp(self):
        self.li, self.read_fake = FakeCommunity(), Fake()
        self.cfg = config()
        self.data = identity(self.li.port)
        self.tokens = oauth.Tokens(self.data)
        self.day = ['2026-11-10']
        self.clock = Clock()
        self.pool = read_pool(self.cfg, self.tokens, self.read_fake.port, budget=10)
        self.community = self.community_pool()
        self.client = TestClient(SafeTestServer(server.create_app(self.cfg, KEY.encode(), self.pool, None, self.community)))
        await self.client.start_server()
        self.post = self.li.post()

    def community_pool(self, **kwargs):
        options = {'today': lambda: self.day[0], 'abstand': self.abstand, 'monotonic': self.clock, **kwargs}
        return community_pool(self.cfg, self.tokens, self.li.port, **options)

    async def asyncTearDown(self):
        await self.client.close()
        for fake in (self.li, self.read_fake):
            await asyncio.to_thread(fake.close)

    async def get(self, route, **params):
        reply = await self.client.get('/v1/linkedin-kommentare/' + route, params=params, headers=AUTH)
        return reply.status, await reply.json()

    async def reply(self, comment, text='Danke für Ihre Rückmeldung.', anlass='dank', headers=AUTH, raw=None):
        data = raw if raw is not None else json.dumps({'post_urn': self.post, 'kommentar_urn': comment, 'text': text,
                                                       'anlass': anlass})
        reply = await self.client.post('/v1/linkedin-kommentare/reply', data=data, headers=headers)
        return reply.status, await reply.json()


class Boundary(Service):
    async def test_bridge_methods_routes_and_body_before_any_call(self):
        comment = self.li.comment(self.post)
        for headers, status in (({}, 401), ({'Authorization': 'Bearer wrong'}, 403),
                                ([('Authorization', AUTH['Authorization'])] * 2, 401)):
            for method, route in (('GET', 'comments?post_urn=' + self.post), ('GET', 'tagesliste'), ('POST', 'reply')):
                reply = await self.client.request(method, '/v1/linkedin-kommentare/' + route, headers=headers, data=b'{}' if method == 'POST' else None)
                self.assertEqual(reply.status, status)
                self.assertNotIn('nicht_ausgefuehrt', await reply.json())
        for method, route, status in (('POST', 'comments', 405), ('GET', 'reply', 405), ('POST', 'tagesliste', 405),
                                      ('DELETE', 'reply', 405), ('GET', 'delete', 404), ('POST', 'reaction', 404),
                                      ('GET', 'comments/x', 404), ('GET', 'profile', 404), ('POST', 'edit', 404)):
            with self.subTest(method=method, route=route):
                reply = await self.client.request(method, '/v1/linkedin-kommentare/' + route, headers=AUTH)
                self.assertEqual(reply.status, status)
        reply = await self.client.get('/v1/linkedin-kommentare/comments', params={'post_urn': self.post}, headers=AUTH, data=b'x')
        self.assertEqual((reply.status, await reply.json()), (400, {'error': 'unexpected_body'}))
        status, body = await self.get('comments', post_urn=self.post, seit='2026-11-01')
        self.assertEqual((status, body), (400, {'error': 'invalid_input'}))
        self.assertEqual(self.li.calls, [])
        self.assertIsNotNone(comment)

    async def test_reply_refusals_after_the_bridge_are_200_nicht_ausgefuehrt(self):
        comment = self.li.comment(self.post)
        cases = ((dict(raw=b'{'), 'invalid_input'), (dict(raw=b'{}'), 'invalid_input'),
                 (dict(anlass='beschwerde'), 'anlass_ungueltig'), (dict(text='Mehr auf januaport.ai'), 'text_link'),
                 (dict(text='Danke @Max'), 'text_erwaehnung'), (dict(text='Nur 10 €'), 'text_preis'),
                 (dict(text='x' * 601), 'text_laenge'), (dict(raw=b'x' * 9000), 'request_too_large'))
        for kwargs, code in cases:
            with self.subTest(code=code):
                self.assertEqual(await self.reply(comment, **kwargs), (200, {'error': code, 'nicht_ausgefuehrt': True}))
        self.assertEqual(self.li.calls, [])

    async def test_comments_reply_and_daily_list_through_spawned_jobs(self):
        comment = self.li.comment(self.post)
        status, body = await self.get('comments', post_urn=self.post)
        self.assertEqual((status, [c['kommentar_urn'] for c in body['kommentare']]), (200, [comment]))
        status, body = await self.reply(comment)
        self.assertEqual((status, body['ausgefuehrt'], body['kommentar_urn']), (200, True, comment))
        self.assertNotIn('nicht_ausgefuehrt', body)
        status, body = await self.get('tagesliste')
        self.assertEqual((status, len(body['antworten']), body['offen']), (200, 1, []))
        status, body = await self.get('comments', post_urn=self.post)
        self.assertEqual(body['kommentare'], [])

    async def test_refusals_found_at_linkedin_are_200_nicht_ausgefuehrt(self):
        own = self.li.comment(self.post, actor=ORG)
        self.assertEqual(await self.reply(own), (200, {'error': 'von_uns', 'nicht_ausgefuehrt': True}))
        nested = self.li.comment(self.post, parent=own)
        self.assertEqual(await self.reply(nested), (200, {'error': 'ebene', 'nicht_ausgefuehrt': True}))
        self.assertEqual(self.li.replies_posted(), [])

    async def test_without_a_community_pool_nothing_runs(self):
        client = TestClient(SafeTestServer(server.create_app(self.cfg, KEY.encode(), self.pool, None)))
        await client.start_server()
        try:
            reply = await client.post('/v1/linkedin-kommentare/reply', headers=AUTH, data=json.dumps(
                {'post_urn': self.post, 'kommentar_urn': self.li.comment(self.post), 'text': 'Danke', 'anlass': 'dank'}))
            self.assertEqual((reply.status, await reply.json()),
                             (200, {'error': 'kommentare_nicht_eingerichtet', 'nicht_ausgefuehrt': True}))
            reply = await client.get('/v1/linkedin-kommentare/tagesliste', headers=AUTH)
            self.assertEqual((reply.status, await reply.json()), (503, {'error': 'kommentare_nicht_eingerichtet'}))
        finally:
            await client.close()
        self.assertEqual(self.li.calls, [])

    async def test_the_stage_a_and_b1_routes_do_not_answer_community_names(self):
        for path in ('/v1/linkedin/comments', '/v1/linkedin/reply', '/v1/linkedin-post/reply', '/v1/linkedin-post/comments',
                     '/v1/linkedin-post/tagesliste'):
            reply = await self.client.get(path, headers=AUTH)
            self.assertEqual(reply.status, 404, path)

    async def test_a_read_job_never_gets_a_write_permit(self):
        community = self.community_pool(adapter=ReadThatWrites)
        client = TestClient(SafeTestServer(server.create_app(self.cfg, KEY.encode(), self.pool, None, community)))
        await client.start_server()
        try:
            reply = await client.get('/v1/linkedin-kommentare/comments', params={'post_urn': self.post}, headers=AUTH)
            self.assertEqual((reply.status, await reply.json()), (500, {'error': 'lesen_schreibt'}))
        finally:
            await client.close()
        self.assertEqual(self.li.replies_posted(), [])


class Queue(Service):
    abstand = 1.5

    def test_the_fixed_values(self):
        self.assertEqual((cp.REPLY_ABSTAND, cp.REPLY_SPERRE, cp.REPLY_WARTEND_MAX), (60, 600, 1))
        self.assertEqual(cp.COMMUNITY_HTTP_PER_DAY, 60)
        self.assertEqual(inspect.signature(cp.CommunityPool).parameters['abstand'].default, 60)
        self.assertEqual(inspect.signature(cp.CommunityPool).parameters['sperre'].default, 600)

    async def test_replies_run_one_after_another_with_spacing_and_a_short_queue(self):
        a, b, c = (self.li.comment(self.post, actor=actor) for actor in (DRITTER, DRITTE, 'urn:li:person:Dritter3'))
        results = await asyncio.gather(self.reply(a), self.reply(b), self.reply(c))
        codes = sorted(repr((r[1].get('error'), r[1].get('ausgefuehrt'))) for r in results)
        self.assertEqual(codes, sorted(repr(c) for c in [(None, True), (None, True), ('antwort_warteschlange_voll', None)]))
        refused = [r for r in results if r[1].get('error')][0]
        self.assertEqual(refused, (200, {'error': 'antwort_warteschlange_voll', 'nicht_ausgefuehrt': True}))
        first, second = sorted(call['t'] for call in self.li.replies_posted())
        self.assertGreaterEqual(second - first, 1.5)

    async def test_one_comment_is_locked_while_in_flight_and_ten_minutes_after_sending(self):
        comment = self.li.comment(self.post)
        results = await asyncio.gather(self.reply(comment), self.reply(comment))
        self.assertIn((200, {'error': 'gesperrt', 'nicht_ausgefuehrt': True}), results)
        self.assertEqual(len(self.li.replies_posted()), 1)
        before = len(self.li.calls)
        self.assertEqual(await self.reply(comment), (200, {'error': 'gesperrt', 'nicht_ausgefuehrt': True}))
        self.assertEqual(len(self.li.calls), before)  # the lock answers without asking LinkedIn
        self.clock.offset += 601
        self.assertEqual(await self.reply(comment), (200, {'error': 'wiederholt', 'nicht_ausgefuehrt': True}))
        self.assertEqual(len(self.li.replies_posted()), 1)

    async def test_a_refusal_before_sending_neither_locks_nor_delays(self):
        comment = self.li.comment(self.post, actor=ORG)
        started = time.monotonic()
        for _ in range(2):
            self.assertEqual(await self.reply(comment), (200, {'error': 'von_uns', 'nicht_ausgefuehrt': True}))
        self.assertLess(time.monotonic() - started, 1.5 + 8)
        self.assertEqual(self.community.locks, {})

    async def test_a_job_over_its_budget_after_sending_is_unclear_and_locks(self):
        community = self.community_pool(adapter=SlowJob, reply_budget=1.0)
        client = TestClient(SafeTestServer(server.create_app(self.cfg, KEY.encode(), self.pool, None, community)))
        await client.start_server()
        comment = self.li.comment(self.post)
        data = json.dumps({'post_urn': self.post, 'kommentar_urn': comment, 'text': 'Danke', 'anlass': 'dank'})
        try:
            reply = await client.post('/v1/linkedin-kommentare/reply', headers=AUTH, data=data)
            self.assertEqual((reply.status, await reply.json()), (504, {'error': 'antwort_zeit_ueberschritten'}))
            reply = await client.post('/v1/linkedin-kommentare/reply', headers=AUTH, data=data)
            self.assertEqual((reply.status, await reply.json()), (200, {'error': 'gesperrt', 'nicht_ausgefuehrt': True}))
        finally:
            await client.close()
        self.assertEqual(len(self.li.replies_posted()), 1)


class NoPluginCounter(Service):
    """S3: the cap lives in the core (daily_cap 8 attempts). The plugin counts no replies, no per-post brake."""

    async def test_six_replies_under_one_post_all_go_out(self):
        comments = [self.li.comment(self.post, actor='urn:li:person:Verfasser%d' % i) for i in range(6)]
        for comment in comments:
            status, body = await self.reply(comment)
            self.assertEqual((status, body.get('ausgefuehrt')), (200, True), body)
        self.assertEqual(len(self.li.replies_posted()), 6)

    def test_no_reply_counter_or_brake_constant_in_the_plugin(self):
        source = (ROOT / 'linkedin_seite' / 'community_pool.py').read_text(encoding='utf-8')
        for word in ('REPLIES_PER_DAY', 'ANTWORTEN_PRO_TAG', 'JE_POST', 'PER_POST', 'DAILY_CAP', 'tageslimit_antwort',
                     'schreib_tageslimit'):
            self.assertNotIn(word, source)

    async def test_the_http_budget_of_the_path_protects_linkedin_not_the_reply_count(self):
        comment = self.li.comment(self.post)
        self.community.http[self.day[0]] = cp.COMMUNITY_HTTP_PER_DAY
        self.assertEqual(await self.get('comments', post_urn=self.post), (429, {'error': 'kommentare_http_tageslimit'}))
        self.assertEqual(await self.reply(comment),
                         (200, {'error': 'kommentare_http_tageslimit', 'nicht_ausgefuehrt': True}))
        self.assertEqual(self.li.calls, [])
        self.day[0] = '2026-11-11'
        self.assertEqual((await self.reply(comment))[1]['ausgefuehrt'], True)


class NoRedaktionsplan(unittest.TestCase):
    """S1: the reply token holds no Redaktionsplan; the community route has no gateway access at all."""

    def test_community_modules_import_neither_gateway_nor_editorial_plan(self):
        for name in ('kommentare.py', 'community_adapter.py', 'community_pool.py'):
            tree = ast.parse((ROOT / 'linkedin_seite' / name).read_text(encoding='utf-8'))
            imported = set()
            for node in ast.walk(tree):
                if isinstance(node, ast.ImportFrom):
                    imported.add(node.module or '')
                    imported.update(alias.name for alias in node.names)
                elif isinstance(node, ast.Import):
                    imported.update(alias.name for alias in node.names)
            for forbidden in ('gateway', 'GatewayClient', 'redaktion', 'WriteConfig', 'post_pool'):
                self.assertNotIn(forbidden, imported, name)

    def test_the_pool_takes_no_gateway_token(self):
        self.assertFalse([p for p in inspect.signature(cp.CommunityPool).parameters if 'gateway' in p])
        self.assertFalse([p for p in inspect.signature(CommunityAdapter).parameters if 'gateway' in p])


class LogCanary(unittest.TestCase):
    """K4: the plugin log holds codes and URNs only; comment text, reply text and people never."""

    def test_real_service_log_has_no_comment_text_reply_text_or_person(self):
        li = FakeCommunity()
        self.addCleanup(li.close)
        post = li.post()
        comment = li.comment(post, text='KOMMENTARCANARY bitte antworten')
        own = li.comment(post, actor=ORG, text='EIGENCANARY')
        bridge = secrets.token_urlsafe(40)
        data = identity(li.port)
        proc = subprocess.Popen([sys.executable, '-u', str(Path(__file__).with_name('wire_server_kommentare.py'))],
                                stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        try:
            proc.stdin.write(json.dumps({'oauth': data, 'linkedin_port': li.port, 'bridge': bridge}) + '\n')
            proc.stdin.flush()
            port = int(proc.stdout.readline())
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
            base = 'http://127.0.0.1:%d/v1/linkedin-kommentare/' % port
            auth = {'Authorization': 'Bearer ' + bridge}
            with opener.open(urllib.request.Request(base + 'comments?post_urn=' + post, headers=auth), timeout=20) as reply:
                self.assertIn('KOMMENTARCANARY', reply.read().decode())
            for target, expected in ((comment, 'ausgefuehrt'), (own, 'nicht_ausgefuehrt')):
                request = urllib.request.Request(base + 'reply', method='POST', headers={**auth, 'Content-Type': 'application/json'},
                                                 data=json.dumps({'post_urn': post, 'kommentar_urn': target, 'anlass': 'dank',
                                                                  'text': 'ANTWORTCANARY danke'}).encode())
                with opener.open(request, timeout=20) as reply:
                    self.assertIn(expected, json.loads(reply.read()))
            with opener.open(urllib.request.Request(base + 'tagesliste', headers=auth), timeout=20) as reply:
                answers = json.loads(reply.read())['antworten']
        finally:
            proc.terminate()
            output, _ = proc.communicate(timeout=10)
        self.assertEqual(len(answers), 1)
        self.assertIn('route=linkedin-kommentare/reply code=ok', output)
        self.assertIn('spur kommentar=%s post=%s code=ok anlass=dank' % (comment, post), output)
        self.assertIn('route=linkedin-kommentare/reply code=von_uns', output)
        for canary in ('KOMMENTARCANARY', 'EIGENCANARY', 'ANTWORTCANARY', 'transport-canary', NAME, HEADLINE, 'NAMECANARY',
                       'HEADLINECANARY', 'urn:li:person', DRITTER, data[s.ACCESS_TOKEN], data[s.CLIENT_SECRET], bridge):
            self.assertNotIn(canary, output)


if __name__ == '__main__':
    unittest.main()
