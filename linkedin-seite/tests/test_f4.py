"""#1306 F4: frozen core hash, actual HTTP boundary, actual adapters and spawned pool."""
import copy
import hashlib
import json
import asyncio
from pathlib import Path
import unittest

from linkedin_seite import redaktion as r
from support_post import ORG, PNG, PDF, TOOL_GET, TOOL_FILE
from test_post_server import Service, AUTH
from test_f2_preview import effect_of

GOLDEN = Path(__file__).with_name('data') / 'hash_golden.json'


class HashV2(unittest.TestCase):
    def test_every_core_golden_vector(self):
        vectors = json.loads(GOLDEN.read_text(encoding='utf-8'))
        self.assertEqual(len(vectors), 7)
        for vector in vectors:
            with self.subTest(vector=vector['name']):
                self.assertEqual(r.canonical(vector['eingabe']).decode('utf-8'), vector['kanonisch'])
                self.assertEqual(r.wirkung_hash(vector['eingabe']), vector['hash'])

    def test_every_field_and_order_binds_the_hash(self):
        base = {'aktion': 'posten', 'ziel_urn': ORG, 'text': 'Grüße <>&\n"\\',
                'medien': [{'name': 'a.pdf', 'sha256': 'a'*64}, {'name': 'b.pdf', 'sha256': 'b'*64}],
                'umfrage': {'frage': 'Gut?', 'antworten': ['Ja', 'Nein'], 'dauer': 7}}
        paths = [('aktion',), ('ziel_urn',), ('text',), ('medien', 0, 'name'), ('medien', 1, 'name'),
                 ('medien', 0, 'sha256'), ('medien', 1, 'sha256'), ('umfrage', 'frage'),
                 ('umfrage', 'antworten', 0), ('umfrage', 'antworten', 1), ('umfrage', 'dauer')]
        for path in paths:
            changed = copy.deepcopy(base)
            parent = changed
            for key in path[:-1]:
                parent = parent[key]
            value = parent[path[-1]]
            parent[path[-1]] = value + 1 if type(value) is int else value + 'x'
            with self.subTest(path=path):
                self.assertNotEqual(r.wirkung_hash(base), r.wirkung_hash(changed))
        for field in ('medien', 'antworten'):
            changed = copy.deepcopy(base)
            (changed['medien'] if field == 'medien' else changed['umfrage']['antworten']).reverse()
            self.assertNotEqual(r.wirkung_hash(base), r.wirkung_hash(changed))

    def test_unicode_no_normalization_and_integer_duration(self):
        base = {'aktion': 'posten', 'ziel_urn': ORG, 'text': 'é'}
        self.assertNotEqual(r.wirkung_hash(base), r.wirkung_hash(dict(base, text='e\u0301')))
        for bad in (True, 7.0, '7'):
            with self.subTest(duration=bad), self.assertRaises((ValueError, TypeError)):
                r.wirkung_hash(dict(base, umfrage={'frage': 'x', 'antworten': [], 'dauer': bad}))
        with self.assertRaises(UnicodeError):
            r.wirkung_hash(dict(base, text='\ud800'))


class F4(Service):
    async def call_with(self, route, rid, value=None):
        headers = dict(AUTH)
        if value is not None:
            headers['X-Jnpt-Freigabe'] = value
        return await self.write_call(route, rid, headers=headers)

    def flags(self, body, *, success=False, before=False):
        self.assertEqual(body.get('ausgefuehrt'), True if success else None, body)
        self.assertEqual(body.get('nicht_ausgefuehrt'), True if before else None, body)
        if not success:
            self.assertNotIn('ausgefuehrt', body)
        if not before:
            self.assertNotIn('nicht_ausgefuehrt', body)

    async def test_missing_header_is_409_for_every_write(self):
        for route, action in r.AKTIONEN.items():
            rid = self.plan(post_text='x', kommentar_text='c', **({} if action == 'posten' else {'post_urn': 'urn:li:share:7'}))
            status, body = await self.call_with(route, rid)
            self.assertEqual(status, 409, (route, body))
            self.flags(body, before=True)
        self.assertEqual(self.li.calls, [])

    async def test_header_exact_equality_before_any_linkedin_write_for_every_action(self):
        for route, action in r.AKTIONEN.items():
            rid = self.plan(post_text='x', kommentar_text='c', **({} if action == 'posten' else {'post_urn': 'urn:li:share:7'}))
            _, preview = await self.vorschau(rid, action)
            digest = preview['hash']
            for value in ('hash=freigabe:v2:sha256:' + 'f'*64, digest, 'hash=' + digest.upper(),
                          'hash= ' + digest, 'hash=' + digest + ',hash=' + digest,
                          'hash=' + digest.removeprefix('freigabe:v2:sha256:'),
                          'hash=' + digest + ';auftrag_id=0123456789abcdef',
                          'auftrag_id=0123456789abcdef,hash=' + digest):
                status, body = await self.call_with(route, rid, value)
                self.assertEqual(status, 409, (route, value, body))
                self.flags(body, before=True)
            self.assertEqual(self.li.calls, [])

    async def test_action_cannot_be_exchanged_even_with_identical_target_and_text(self):
        rid = self.plan(post_text='same', kommentar_text='same', post_urn='urn:li:share:7')
        _, preview = await self.vorschau(rid, 'bearbeiten')
        status, body = await self.call_with('create_comment', rid, 'hash=' + preview['hash'])
        self.assertEqual(status, 409)
        self.flags(body, before=True)
        self.assertEqual(self.li.calls, [])

    async def test_duplicate_header_rejected(self):
        rid = self.plan(post_text='x')
        _, preview = await self.vorschau(rid)
        header = ('X-Jnpt-Freigabe', 'hash=' + preview['hash'])
        status, body = await self.write_call('create_post', rid, headers=list(AUTH.items()) + [header, header])
        self.assertEqual(status, 409)
        self.flags(body, before=True)
        self.assertEqual(self.li.calls, [])

    async def test_success_for_every_action_and_only_two_gateway_tools(self):
        for route, action in r.AKTIONEN.items():
            rid = self.plan(post_text='x', kommentar_text='c', **({} if action == 'posten' else {'post_urn': 'urn:li:share:7'}))
            _, preview = await self.vorschau(rid, action)
            expected = {'aktion': action, 'medien': [], 'text': 'c' if action == 'kommentar' else '' if action == 'loeschen' else 'x',
                        'umfrage': None, 'ziel_urn': ORG if action == 'posten' else 'urn:li:share:7'}
            self.assertEqual(effect_of(preview), expected)
            self.assertEqual(preview['hash'], r.wirkung_hash(expected))
            self.gw.calls.clear()
            status, body = await self.call_with(route, rid, 'hash=' + preview['hash'])
            self.assertEqual(status, 200, body)
            self.flags(body, success=True)
            self.assertEqual([c[1] for c in self.gw.tool_calls()], [TOOL_GET])

    async def test_stale_hash_for_text_target_comment_media_title_bytes_order_and_poll(self):
        poll = {'frage': 'Gut?', 'antworten': ['Ja', 'Nein'], 'dauer': 7}
        cases = [('posten', {'post_text': 'x'}, {'post_text': 'y'}),
                 ('bearbeiten', {'post_text': 'x', 'post_urn': 'urn:li:share:7'}, {'post_urn': 'urn:li:share:8'}),
                 ('loeschen', {'post_urn': 'urn:li:share:7'}, {'post_urn': 'urn:li:share:8'}),
                 ('kommentar', {'post_urn': 'urn:li:share:7', 'kommentar_text': 'c'}, {'kommentar_text': 'd'}),
                 ('posten', {'post_text': 'x', 'medien': '["a.pdf"]'}, {'medien': '["b.pdf"]'}),
                 ('posten', {'post_text': 'x', 'medien': '["a.png","b.png"]'}, {'medien': '["b.png","a.png"]'})]
        for update in ({'frage': 'Anders?'}, {'antworten': ['Nein', 'Ja']}, {'antworten': ['Ja!', 'Nein']}, {'dauer': 3}):
            cases.append(('posten', {'post_text': 'x', 'umfrage': json.dumps(poll)}, {'umfrage': json.dumps(dict(poll, **update))}))
        self.gw.files.update({'a.pdf': PDF, 'b.pdf': PDF, 'a.png': PNG, 'b.png': PNG + b'2'})
        for action, fields, update in cases:
            rid = self.plan(**fields)
            _, preview = await self.vorschau(rid, action)
            self.gw.plan[rid]['fields'].update(update)
            route = next(k for k, v in r.AKTIONEN.items() if v == action)
            status, body = await self.call_with(route, rid, 'hash=' + preview['hash'])
            self.assertEqual(status, 409, (action, update, body))
            self.flags(body, before=True)
        self.assertEqual(self.li.calls, [])

    async def test_organization_is_bound_to_preview_and_header(self):
        rid = self.plan(post_text='x')
        _, preview = await self.vorschau(rid)
        other = dict(effect_of(preview), ziel_urn='urn:li:organization:999')
        status, body = await self.call_with('create_post', rid, 'hash=' + r.wirkung_hash(other))
        self.assertEqual(status, 409)
        self.flags(body, before=True)
        self.assertEqual(self.li.calls, [])

    async def test_preview_and_effect_use_one_entry_read(self):
        rid = self.plan(post_text='original', medien='["a.pdf"]')
        self.gw.files['a.pdf'] = PDF
        def change_after_read(name, args, result):
            if name == TOOL_GET:
                saved = copy.deepcopy(result)
                self.gw.plan[rid]['fields']['post_text'] = 'changed'
                return saved
            return result
        self.gw.result_hook = change_after_read
        _, preview = await self.vorschau(rid)
        self.assertEqual(preview['post_text'], 'original')
        self.assertEqual(effect_of(preview)['text'], 'original')
        self.assertEqual(effect_of(preview)['medien'], [{'name': 'a.pdf', 'sha256': hashlib.sha256(PDF).hexdigest()}])
        self.assertEqual(preview['dokument_titel'], effect_of(preview)['medien'][0]['name'])
        self.assertEqual(preview['hash'], r.wirkung_hash(effect_of(preview)))
        self.assertEqual([c[1] for c in self.gw.tool_calls()], [TOOL_GET, TOOL_FILE])
        self.assertEqual(self.li.calls, [])

    async def test_errors_after_first_write_have_no_definitive_flags(self):
        for route, action in r.AKTIONEN.items():
            self.li.calls.clear()
            rid = self.plan(post_text='x', kommentar_text='c', **({} if action == 'posten' else {'post_urn': 'urn:li:share:7'}))
            _, preview = await self.vorschau(rid, action)
            self.li.override = lambda call: (500, {}, b'')
            status, body = await self.call_with(route, rid, 'hash=' + preview['hash'])
            self.assertEqual(status, 502, body)
            self.assertTrue(self.li.writes())
            self.flags(body)

    async def test_upload_initialization_already_makes_failure_unclear(self):
        rid = self.plan(post_text='x', medien='["a.png"]')
        self.gw.files['a.png'] = PNG
        _, preview = await self.vorschau(rid)
        self.li.override = lambda call: (500, {}, b'')
        status, body = await self.call_with('create_post', rid, 'hash=' + preview['hash'])
        self.assertEqual(status, 502)
        self.assertEqual(self.li.calls[0]['query'], 'action=initializeUpload')
        self.flags(body)

    async def test_validation_and_media_errors_are_definitely_not_executed(self):
        for fields in ({'post_text': '‹hidden›'}, {'post_text': 'x', 'medien': '["a.png"]'}):
            rid = self.plan(**fields)
            self.gw.files['a.png'] = b'wrong format'
            status, body = await self.call_with('create_post', rid, 'hash=freigabe:v2:sha256:' + 'a'*64)
            self.assertEqual(status, 422)
            self.flags(body, before=True)
        self.assertEqual(self.li.calls, [])

    async def test_accepted_but_unconfirmed_response_is_unclear(self):
        for route, action in r.AKTIONEN.items():
            rid = self.plan(post_text='x', kommentar_text='c', **({} if action == 'posten' else {'post_urn': 'urn:li:share:7'}))
            _, preview = await self.vorschau(rid, action)
            self.li.override = lambda call: (202, {'x-restli-id': 'urn:li:share:7'}, b'')
            status, body = await self.call_with(route, rid, 'hash=' + preview['hash'])
            self.assertEqual(status, 502)
            self.flags(body)

    async def test_worker_exit_before_and_after_first_real_write(self):
        import time
        for after_write in (False, True):
            with self.subTest(after_write=after_write):
                rid = self.plan(post_text='x')
                _, preview = await self.vorschau(rid)
                self.gw.calls.clear()
                self.li.calls.clear()
                if after_write:
                    self.li.override = lambda call: (time.sleep(1), None)[1]
                else:
                    self.gw.result_hook = lambda n, a, result: (time.sleep(1), result)[1]
                task = asyncio.create_task(self.call_with('create_post', rid, 'hash=' + preview['hash']))
                for _ in range(200):
                    if self.li.calls if after_write else self.gw.tool_calls():
                        break
                    await asyncio.sleep(0.01)
                self.assertTrue(self.li.calls if after_write else self.gw.tool_calls())
                self.writes.job.proc.kill()
                status, body = await task
                self.assertEqual(status, 502)
                self.flags(body, before=not after_write)
                if not after_write:
                    self.assertEqual(self.li.calls, [])
                self.gw.result_hook = None
                self.li.override = None

    async def test_cached_success_still_requires_current_matching_header(self):
        rid = self.plan(post_text='x')
        _, preview = await self.vorschau(rid)
        value = 'hash=' + preview['hash']
        status, body = await self.call_with('create_post', rid, value)
        self.assertEqual(status, 200)
        self.flags(body, success=True)
        before = len(self.li.calls)
        for header in (None, 'hash=freigabe:v2:sha256:' + 'f'*64):
            status, body = await self.call_with('create_post', rid, header)
            self.assertEqual(status, 409)
            self.flags(body, before=True)
        self.gw.plan[rid]['fields']['post_text'] = 'changed'
        status, body = await self.call_with('create_post', rid, value)
        self.assertEqual(status, 409)
        self.flags(body, before=True)
        self.assertEqual(len(self.li.calls), before)


if __name__ == '__main__':
    unittest.main()
