"""Stufe B1 (#1237): HTTP boundary, write pool and trace (SEC Bed. 4 and 5, V3 counting)."""
import asyncio
import json
from pathlib import Path
import re
import secrets
import subprocess
import sys
import tempfile
import unittest
import urllib.error
import urllib.request
from unittest.mock import patch
from aiohttp.test_utils import TestClient, TestServer
from linkedin_seite import oauth
from linkedin_seite import redaktion as r
from linkedin_seite import server
from linkedin_seite import source_contract as s
from linkedin_seite.domain import Config, ReportError
from linkedin_seite.post_pool import WritePool, WRITE_ACTIONS_PER_DAY
from linkedin_seite.safe_http import SafeAppRunner
from linkedin_seite.workers import WorkerPool
from support import Fake, GRANT, GRANT_COMMA, GRANT_LIST, LocalAdapter, identity
from support_post import (FakeGateway, FakeLinkedIn, GATEWAY_TOKEN, LocalPostAdapter, PNG, MiB, config,
                          mp4, new_id, record, write_identity)

KEY = 'SYNTHETIC-' + 'b' * 32
AUTH = {'Authorization': 'Bearer ' + KEY}


class SafeTestServer(TestServer):
    async def _make_runner(self, **kwargs):
        return SafeAppRunner(self.app, access_log=None, max_line_size=8192, max_field_size=8192, **kwargs)


class Service(unittest.IsolatedAsyncioTestCase):

    async def asyncSetUp(self):
        self.read_fake = Fake()
        self.li = FakeLinkedIn()
        self.gw = FakeGateway()
        self.cfg = config(self.gw.port)
        self.read = identity(self.read_fake.port)
        self.write = write_identity(self.li.port)
        self.day = ['2026-11-02']
        self.approvals = {}
        self.pool = WorkerPool(self.cfg, self.read, adapter_factory=LocalAdapter, budget=10)
        self.writes = self.write_pool()
        self.client = TestClient(SafeTestServer(server.create_app(self.cfg, KEY.encode(), self.pool, self.writes)))
        await self.client.start_server()

    def write_pool(self, **kwargs):
        return WritePool(self.cfg, self.write, GATEWAY_TOKEN, adapter_factory=LocalPostAdapter,
                         today=lambda: self.day[0], **kwargs)

    async def asyncTearDown(self):
        await self.client.close()
        for fake in (self.read_fake, self.li, self.gw):
            await asyncio.to_thread(fake.close)

    def plan(self, **fields):
        rid = new_id()
        self.gw.plan[rid] = record(rid, fields)
        return rid

    async def vorschau(self, rid, aktion='posten'):
        reply = await self.client.get('/v1/linkedin-post/vorschau', params={'eintrag_id': rid, 'aktion': aktion}, headers=AUTH)
        return reply.status, await reply.json()

    async def approve(self, rid, aktion='posten'):
        status, body = await self.vorschau(rid, aktion)
        self.assertEqual(status, 200, body)
        self.approvals[(rid, aktion)] = body['hash']
        return body['hash']

    async def write_call(self, route, rid, headers=None):
        if headers is None:
            headers = dict(AUTH)
            digest = self.approvals.get((rid, r.AKTIONEN[route]))
            if digest:
                headers['X-Jnpt-Freigabe'] = 'hash=' + digest
        reply = await self.client.post('/v1/linkedin-post/' + route, data=json.dumps({'eintrag_id': rid}), headers=headers)
        return reply.status, await reply.json()


class Boundary(Service):
    async def test_auth_method_route_and_body_checked_before_any_call(self):
        rid = self.plan(post_text='x')
        for headers, status in (({}, 401), ({'Authorization': 'Bearer wrong'}, 403),
                                ([('Authorization', AUTH['Authorization'])] * 2, 401)):
            reply = await self.client.post('/v1/linkedin-post/create_post', data=json.dumps({'eintrag_id': rid}), headers=headers)
            self.assertEqual(reply.status, status)
        for method, path, status in (('GET', 'create_post', 405), ('PUT', 'create_post', 405), ('DELETE', 'delete_post', 405),
                                     ('POST', 'vorschau', 405), ('POST', 'like', 404), ('POST', 'create_post/x', 404),
                                     ('POST', 'reply_comment', 404), ('GET', 'comments', 404)):
            with self.subTest(method=method, path=path):
                reply = await self.client.request(method, '/v1/linkedin-post/' + path, headers=AUTH, data=b'{}')
                self.assertEqual(reply.status, status)
        for body in (b'', b'{}', json.dumps({'eintrag_id': rid, 'post_text': 'anderer Text'}).encode(),
                     json.dumps({'eintrag_id': rid, 'datei': 'x.png'}).encode(), b'x' * 2048):
            reply = await self.client.post('/v1/linkedin-post/create_post', data=body, headers={**AUTH, 'X-Jnpt-Freigabe': 'hash=freigabe:v2:sha256:' + 'a'*64})
            self.assertEqual(reply.status, 200)
            self.assertIs((await reply.json())['nicht_ausgefuehrt'], True)
        reply = await self.client.get('/v1/linkedin-post/vorschau', params={'eintrag_id': rid, 'aktion': 'posten'},
                                      headers=AUTH, data=b'{}')
        self.assertEqual(reply.status, 400)
        self.assertEqual(self.gw.calls, [])
        self.assertEqual(self.li.calls, [])

    async def test_stage_a_routes_unchanged_next_to_the_write_routes(self):
        reply = await self.client.get('/v1/linkedin/page_statistics', headers=AUTH)
        self.assertEqual(reply.status, 200)
        for path in ('create_post', 'vorschau', 'delete_post'):
            reply = await self.client.get('/v1/linkedin/' + path, headers=AUTH)
            self.assertEqual(reply.status, 404)
        reply = await self.client.post('/v1/linkedin/page_statistics', headers=AUTH)
        self.assertEqual(reply.status, 405)
        self.assertEqual(self.li.calls, [])

    async def test_write_disabled_without_configuration_or_credentials(self):
        client = TestClient(SafeTestServer(server.create_app(self.cfg, KEY.encode(), self.pool, None)))
        await client.start_server()
        try:
            for method, path in (('POST', 'create_post'), ('GET', 'vorschau?eintrag_id=00000000000000ab&aktion=posten')):
                reply = await client.request(method, '/v1/linkedin-post/' + path, headers={**AUTH, 'X-Jnpt-Freigabe': 'hash=freigabe:v2:sha256:' + 'a'*64},
                                             data=json.dumps({'eintrag_id': '00000000000000ab'}) if method == 'POST' else None)
                self.assertEqual((reply.status, await reply.json()), (200 if method == 'POST' else 503, {'error': 'schreiben_nicht_eingerichtet', **({'nicht_ausgefuehrt': True} if method == 'POST' else {})}))
            reply = await client.post('/v1/linkedin-post/create_post', data=b'{}')
            self.assertEqual(reply.status, 401)
        finally:
            await client.close()


class Flow(Service):
    async def test_post_then_idempotent_repeat_returns_the_same_urn(self):
        rid = self.plan(post_text='Hallo (Welt)', medien='["a.png"]')
        self.gw.files['a.png'] = PNG
        digest = await self.approve(rid)
        with self.assertLogs('linkedin_seite', level='INFO') as logs:
            status, first = await self.write_call('create_post', rid)
        self.assertEqual(status, 200, first)
        self.assertIs(first['ausgefuehrt'], True)
        self.assertNotIn('nicht_ausgefuehrt', first)
        self.assertEqual((first['post_urn'], first['hash']), ('urn:li:share:7000000000000000001', digest))
        status, again = await self.write_call('create_post', rid)
        self.assertIs(again['ausgefuehrt'], True)
        self.assertNotIn('nicht_ausgefuehrt', again)
        self.assertEqual(again['hash'], digest)
        self.assertEqual((status, again['post_urn'], again['wiederholt']), (200, first['post_urn'], True))
        self.assertEqual(len(self.li.api('POST', '/rest/posts')), 1)
        self.assertEqual(sum(self.writes.actions.values()), 1)
        spur = [line for line in logs.output if 'spur ' in line]
        self.assertEqual(len(spur), 1)
        self.assertRegex(spur[0], r'spur aktion=posten eintrag_id=%s hash=%s code=ok urn=urn:li:share:7000000000000000001 bytes=%d$'
                         % (rid, digest, len(PNG)))
        for canary in ('Hallo', 'a.png', 'UPLOADURLCANARY', 'dms-uploads', GATEWAY_TOKEN, self.write[s.ACCESS_TOKEN]):
            self.assertNotIn(canary, '\n'.join(logs.output))

    async def test_unknown_outcome_after_the_write_is_returned_to_the_core(self):
        self.li.override = lambda call: (201, {}, b'') if call['path'] == '/rest/posts' else None
        rid = self.plan(post_text='x')
        await self.approve(rid)
        status, body = await self.write_call('create_post', rid)
        self.assertEqual((status, body), (502, {'error': 'unexpected_source_shape'}))
        self.assertEqual(len(self.li.api('POST', '/rest/posts')), 1)
        # The durable unknown-result lock belongs to the core, not this RAM pool.

    async def test_failure_before_the_final_request_may_be_retried(self):
        self.li.override = lambda call: (500, {}, b'') if call['method'] == 'PUT' else None
        rid = self.plan(post_text='x', medien='["a.png"]')
        self.gw.files['a.png'] = PNG
        await self.approve(rid)
        status, body = await self.write_call('create_post', rid)
        self.assertEqual((status, body), (502, {'error': 'source_unavailable'}))
        self.li.override = None
        status, body = await self.write_call('create_post', rid)
        self.assertEqual((status, body['state']), (200, 'veroeffentlicht'))
        self.assertEqual(len(self.li.api('POST', '/rest/posts')), 1)

    async def test_not_approved_is_409_and_counts_nothing(self):
        rid = self.plan(post_text='x')
        status, body = await self.write_call('create_post', rid)
        self.assertEqual((status, body), (409, {'error': 'nicht_freigegeben'}))
        self.assertEqual((self.li.calls, sum(self.writes.actions.values())), ([], 0))

    async def test_five_write_actions_per_utc_day_over_all_actions(self):
        self.assertEqual(WRITE_ACTIONS_PER_DAY, 5)
        jobs = []
        for i in range(3):
            rid = self.plan(post_text='Post %d' % i)
            await self.approve(rid)
            jobs.append(('create_post', rid))
        rid = self.plan(post_text='x', post_urn='urn:li:share:9')
        await self.approve(rid, 'loeschen')
        jobs.append(('delete_post', rid))
        rid = self.plan(post_urn='urn:li:share:9', kommentar_text='Link')
        await self.approve(rid, 'kommentar')
        jobs.append(('create_comment', rid))
        for route, rid in jobs:
            status, body = await self.write_call(route, rid)
            self.assertEqual(status, 200, body)
        rid = self.plan(post_text='zu viel', post_urn='urn:li:share:9')
        await self.approve(rid, 'bearbeiten')
        before = len(self.li.calls)
        status, body = await self.write_call('edit_post', rid)
        self.assertEqual((status, body), (200, {'error': 'schreib_tageslimit', 'nicht_ausgefuehrt': True}))
        self.assertEqual(len(self.li.calls), before)
        self.day[0] = '2026-11-03'
        status, body = await self.write_call('edit_post', rid)
        self.assertEqual((status, body['state']), (200, 'bearbeitet'))

    async def test_aborted_video_counts_as_one_action(self):
        rid = self.plan(post_text='Clip', medien='["clip.mp4"]')
        self.gw.files['clip.mp4'] = mp4(8192)
        await self.approve(rid)
        self.li.video_status = ['PROCESSING']
        status, body = await self.write_call('create_post', rid)
        self.assertEqual((status, body), (502, {'error': 'video_nicht_verfuegbar'}))
        self.assertEqual(self.writes.actions, {self.day[0]: 1})
        self.assertEqual(self.li.api('POST', '/rest/posts'), [])

    async def test_preview_never_counts_and_never_calls_linkedin(self):
        rid = self.plan(post_text='x', medien='["clip.mp4"]')
        self.gw.files['clip.mp4'] = mp4(8192)
        for _ in range(3):
            status, body = await self.vorschau(rid)
            self.assertEqual(status, 200)
        self.assertEqual(body['medien'][0]['bytes'], 8192)
        self.assertEqual((self.li.calls, self.writes.actions), ([], {}))


class Background(Service):
    async def test_slow_job_waits_for_result_and_rejects_concurrent_requests(self):
        import time
        def slow(call):
            if call['method'] == 'PUT':
                time.sleep(1)
            return None
        self.li.override = slow
        rid = self.plan(post_text='Clip', medien='["clip.mp4"]')
        self.gw.files['clip.mp4'] = mp4(8192)
        await self.approve(rid)
        task = asyncio.create_task(self.write_call('create_post', rid))
        for _ in range(200):
            if self.li.uploads():
                break
            await asyncio.sleep(0.02)
        self.assertFalse(task.done())
        status, body = await self.write_call('create_post', rid)
        self.assertEqual((status, body), (200, {'error': 'schreiben_belegt', 'nicht_ausgefuehrt': True}))
        status, body = await task
        self.assertEqual((status, body['ausgefuehrt']), (200, True))
        self.assertEqual(len(self.li.api('POST', '/rest/posts')), 1)


class Tokens(unittest.TestCase):
    """#1237, 0.3.1: one grant in one place; the old second place has no effect any more."""

    def test_identity_requires_exactly_the_four_scopes(self):
        good = identity()
        self.assertEqual(oauth.validate_identity(dict(good)), good)
        self.assertEqual(oauth.validate_identity(dict(good, scope=GRANT_COMMA))[s.SCOPE], GRANT_COMMA)
        without = {key: value for key, value in good.items() if key != s.SCOPE}
        for bad in (without, dict(good, scope='rw_organization_admin r_organization_social'),
                    dict(good, scope='w_organization_social w_organization_social_feed'),
                    dict(good, scope=' '.join(GRANT_LIST[:3])), dict(good, scope=GRANT + ' r_ads'),
                    dict(good, scope=''), dict(good, scope=list(GRANT_LIST))):
            with self.subTest(bad=bad.get(s.SCOPE)), self.assertRaises(ValueError):
                oauth.validate_identity(bad)
        self.assertEqual(server.GATEWAY_TOKEN_PATH, '/run/secrets/gateway/runtime-key')
        self.assertEqual(server.IDENTITY_PATH, '/run/secrets/oauth/runtime-key')

    def test_the_second_place_is_gone_from_the_service(self):
        self.assertFalse(hasattr(server, 'POST_IDENTITY_PATH'))
        self.assertFalse(hasattr(oauth, 'load_write_identity'))
        self.assertFalse(hasattr(oauth, 'validate_write_identity'))
        for path in Path(oauth.__file__).parent.glob('*.py'):
            self.assertNotIn('post-oauth', path.read_text(encoding='utf-8'), path.name)

    def test_a_leftover_file_in_the_old_place_has_no_effect(self):
        # A grant still lying in plugin-linkedin-seite-post-oauth: the write side opens the gateway
        # token and nothing else, whether that file is valid, broken or a second grant.
        real, opened = Path.open, []

        def spy(path, *args, **kwargs):
            opened.append(str(path))
            return real(path, *args, **kwargs)
        with tempfile.TemporaryDirectory() as temp:
            token = Path(temp, 'token')
            token.write_bytes(GATEWAY_TOKEN.encode())
            leftover = Path(temp, 'post-oauth', 'runtime-key')
            leftover.parent.mkdir()
            leftover.write_text(json.dumps(write_identity(1)), encoding='utf-8')
            with patch.object(Path, 'open', spy):
                self.assertEqual(server.load_write_side(config(1), token), (GATEWAY_TOKEN, 'an'))
        self.assertEqual(opened, [str(token)])

    def test_loading_the_write_side_fail_closed_with_fixed_reasons(self):
        cfg = config(1)
        with tempfile.TemporaryDirectory() as temp:
            token = Path(temp, 'token')
            self.assertEqual(server.load_write_side(cfg, token), (None, 'gateway_token_fehlt'))
            for raw in (b'short', b'with space ' * 4, b'x' * 2000):
                token.write_bytes(raw)
                self.assertEqual(server.load_write_side(cfg, token), (None, 'gateway_token_ungueltig'))
            token.write_bytes(GATEWAY_TOKEN.encode() + b'\n')
            self.assertEqual(server.load_write_side(cfg, token), (GATEWAY_TOKEN, 'an'))
        plain = Config.from_dict({'organization': '123'})
        self.assertEqual(server.load_write_side(plain, token), (None, 'nicht_konfiguriert'))

    def test_refresh_must_return_the_one_grant(self):
        li = FakeLinkedIn()
        self.addCleanup(li.close)
        from support_post import LocalWriteTransport
        data = write_identity(li.port, days=-1, refresh=True)
        tokens = oauth.Tokens(data, LocalWriteTransport(lambda: None, li.port))
        self.assertTrue(tokens.access().startswith('WRITE-REFRESHED-'))
        for answer in ('r_organization_social,rw_organization_admin', 'w_organization_social,w_organization_social_feed'):
            li.token_body[s.SCOPE] = answer
            tokens = oauth.Tokens(write_identity(li.port, days=-1, refresh=True), LocalWriteTransport(lambda: None, li.port))
            with self.subTest(answer=answer), self.assertRaises(ReportError):
                tokens.access()


class TokenSeparationOnTheWire(Service):
    async def test_reads_carry_the_read_token_writes_the_write_token(self):
        await self.client.get('/v1/linkedin/page_statistics', headers=AUTH)
        rid = self.plan(post_text='x')
        await self.approve(rid)
        status, _ = await self.write_call('create_post', rid)
        self.assertEqual(status, 200)
        self.assertTrue(self.read_fake.calls)
        self.assertTrue(all(c[3]['Authorization'] == 'Bearer ' + self.read[s.ACCESS_TOKEN] for c in self.read_fake.calls))
        api = [c for c in self.li.calls if c['host'] == 'api.linkedin.com']
        self.assertTrue(api)
        self.assertTrue(all(c['headers']['authorization'] == 'Bearer ' + self.write[s.ACCESS_TOKEN] for c in api))


class LogCanary(unittest.TestCase):
    """SEC Bed. 5 / V3: log holds only ids, URNs, hash and size — never text, file name or upload URL."""

    def test_real_service_log_has_no_text_file_name_upload_url_or_secret(self):
        li, gw, read_fake = FakeLinkedIn(), FakeGateway(), Fake()
        for fake in (li, gw, read_fake):
            self.addCleanup(fake.close)
        rid = new_id()
        gw.plan[rid] = record(rid, {'post_text': 'TEXTCANARY mit Inhalt', 'medien': '["dateinamecanary.png"]'})
        gw.files['dateinamecanary.png'] = PNG
        bridge = secrets.token_urlsafe(40)
        read, write = identity(read_fake.port), write_identity(li.port)
        proc = subprocess.Popen([sys.executable, '-u', str(Path(__file__).with_name('wire_server_post.py'))],
                                stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        try:
            proc.stdin.write(json.dumps({'oauth': read, 'write': write, 'bridge': bridge, 'gateway_port': gw.port,
                                         'gateway_token': GATEWAY_TOKEN}) + '\n')
            proc.stdin.flush()
            port = int(proc.stdout.readline())
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
            base = 'http://127.0.0.1:%d/v1/linkedin-post/' % port
            auth = {'Authorization': 'Bearer ' + bridge}
            with opener.open(urllib.request.Request(base + 'vorschau?eintrag_id=%s&aktion=posten' % rid, headers=auth), timeout=20) as reply:
                digest = json.loads(reply.read())['hash']
            auth['X-Jnpt-Freigabe'] = 'hash=' + digest
            request = urllib.request.Request(base + 'create_post', data=json.dumps({'eintrag_id': rid}).encode(),
                                             headers={**auth, 'Content-Type': 'application/json'}, method='POST')
            with opener.open(request, timeout=20) as reply:
                urn = json.loads(reply.read())['post_urn']
            li.override = lambda call: (500, {}, b'{"message":"TEXTCANARY UPLOADURLCANARY"}') if call['method'] == 'PUT' else None
            rid2 = new_id()
            gw.plan[rid2] = record(rid2, {'post_text': 'TEXTCANARY zwei', 'medien': '["dateinamecanary.png"]'})
            with opener.open(urllib.request.Request(base + 'vorschau?eintrag_id=%s&aktion=posten' % rid2, headers=auth), timeout=20) as reply:
                auth['X-Jnpt-Freigabe'] = 'hash=' + json.loads(reply.read())['hash']
            request = urllib.request.Request(base + 'create_post', data=json.dumps({'eintrag_id': rid2}).encode(),
                                             headers=auth, method='POST')
            with self.assertRaises(urllib.error.HTTPError):
                opener.open(request, timeout=20)
        finally:
            proc.terminate()
            output, _ = proc.communicate(timeout=10)
        self.assertIn('spur aktion=posten eintrag_id=%s hash=%s code=ok urn=%s' % (rid, digest, urn), output)
        self.assertIn('spur aktion=posten eintrag_id=%s' % rid2, output)
        self.assertIn('code=source_unavailable', output)
        for canary in ('TEXTCANARY', 'dateinamecanary', 'UPLOADURLCANARY', 'dms-uploads', 'uploadUrl', GATEWAY_TOKEN,
                       write[s.ACCESS_TOKEN], write[s.CLIENT_SECRET], read[s.ACCESS_TOKEN], bridge):
            self.assertNotIn(canary, output)
        for line in output.splitlines():
            self.assertRegex(line, r'^(route=\S+ code=\S+ duration_ms=\d+|spur aktion=\S+ eintrag_id=[0-9a-f]{16} hash=(?:freigabe:v2:sha256:[0-9a-f]{64}|-) '
                                   r'code=\S+ urn=\S+ bytes=\d+|schreiben=\S+)$')


if __name__ == '__main__':
    unittest.main()
