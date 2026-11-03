"""Stufe B1 (#1237): real write adapter against the local LinkedIn and gateway fakes.

Covers SEC c-6575359082 Bed. 1 and 2 and c-6575837332 V1–V3 on the actual code path.
"""
import json
import unittest
from linkedin_seite import redaktion as r
from linkedin_seite import source_contract as s
from linkedin_seite.domain import ReportError
from linkedin_seite.gateway import GatewayClient
from support_post import (FakeGateway, FakeLinkedIn, GATEWAY_TOKEN, LocalPostAdapter, Link, ORG, PNG, JPG, PDF, MiB,
                          TOOL_FILE, config, encoded, mp4, new_id, record, write_identity)

TEXT = 'Neu: drei Stufen (Verbinden, Bauen) – mehr unter https://januaport.ai #KI'
ESCAPED = 'Neu: drei Stufen \\(Verbinden, Bauen\\) – mehr unter https://januaport.ai #KI'


class Base(unittest.TestCase):
    def setUp(self):
        self.li = FakeLinkedIn()
        self.gw = FakeGateway()
        self.addCleanup(self.li.close)
        self.addCleanup(self.gw.close)
        self.cfg = config(self.gw.port)
        self.data = write_identity(self.li.port)
        self.link = Link()
        self.copies = []
        self.approvals = {}

    def adapter(self, link=None, cfg=None):
        adapter = LocalPostAdapter(cfg or self.cfg, self.data, GATEWAY_TOKEN, link or self.link)
        copies, original = self.copies, adapter.gateway.datei

        def datei(name, limit):
            medium = original(name, limit)
            copies.append(medium)
            return medium
        adapter.gateway.datei = datei
        return adapter

    def plan(self, **fields):
        rid = new_id()
        self.gw.plan[rid] = record(rid, fields)
        return rid

    def vorschau(self, rid, aktion='posten'):
        return self.adapter(Link()).vorschau(r.WriteRequest(aktion, rid, True))

    def approve(self, rid, aktion='posten'):
        digest = self.vorschau(rid, aktion)['hash']
        self.approvals[(rid, aktion)] = digest
        return digest

    def act(self, rid, aktion='posten', **kwargs):
        return self.adapter(**kwargs).run(r.WriteRequest(aktion, rid, freigabe=self.approvals.get((rid, aktion))))

    def fails(self, rid, aktion='posten', **kwargs):
        with self.assertRaises(ReportError) as cm:
            self.act(rid, aktion, **kwargs)
        return cm.exception.code

    def body(self, call):
        return json.loads(call['body'])

    def assert_no_person(self):
        for call in self.li.calls:
            self.assertNotIn(b'urn:li:person', call['body'])
            self.assertNotIn(b'urn:li:member', call['body'])

    def assert_headers(self, call):
        h = call['headers']
        self.assertEqual(h['authorization'], 'Bearer ' + self.data[s.ACCESS_TOKEN])
        self.assertEqual(h['linkedin-version'], '202610')
        self.assertEqual(h['x-restli-protocol-version'], '2.0.0')
        self.assertEqual(call['host'], 'api.linkedin.com')


POST_SHELL = {'author': ORG, 'visibility': 'PUBLIC',
              'distribution': {'feedDistribution': 'MAIN_FEED', 'targetEntities': [], 'thirdPartyDistributionChannels': []},
              'lifecycleState': 'PUBLISHED', 'isReshareDisabledByAuthor': False}


class Formats(Base):
    def test_text_post_with_link_author_is_the_configured_organization(self):
        rid = self.plan(post_text=TEXT)
        digest = self.approve(rid)
        result = self.act(rid)
        self.assertEqual(result, {'aktion': 'posten', 'eintrag_id': rid, 'hash': digest, 'format': 'text',
                                  'state': 'veroeffentlicht', 'post_urn': 'urn:li:share:7000000000000000001', 'bytes': 0, 'ausgefuehrt': True})
        calls = self.li.writes()
        self.assertEqual([(c['method'], c['path']) for c in calls], [('POST', '/rest/posts')])
        self.assertEqual(self.body(calls[0]), {**POST_SHELL, 'commentary': ESCAPED})
        self.assert_headers(calls[0])
        self.assertEqual(calls[0]['headers']['content-type'], 'application/json')
        self.assertNotIn('x-restli-method', calls[0]['headers'])
        self.assertEqual((self.link.actions, self.link.http), (1, 1))
        self.assert_no_person()

    def test_single_image_upload_with_token_then_media_post(self):
        rid = self.plan(post_text='Bild', medien='["a.png"]')
        self.gw.files['a.png'] = PNG
        self.approve(rid)
        result = self.act(rid)
        self.assertEqual((result['format'], result['bytes']), ('bild', len(PNG)))
        init, put, post = self.li.writes()
        self.assertEqual((init['method'], init['path'], init['query']), ('POST', '/rest/images', 'action=initializeUpload'))
        self.assertEqual(self.body(init), {'initializeUploadRequest': {'owner': ORG}})
        self.assert_headers(init)
        self.assertEqual((put['method'], put['host'], put['body']), ('PUT', 'www.linkedin.com', PNG))
        self.assertEqual(put['headers']['authorization'], 'Bearer ' + self.data[s.ACCESS_TOKEN])
        image = 'urn:li:image:IMG1'
        self.assertEqual(self.body(post), {**POST_SHELL, 'commentary': 'Bild', 'content': {'media': {'id': image}}})
        self.assertEqual(self.link.actions, 1)
        self.assert_no_person()

    def test_multiple_images_keep_their_order(self):
        rid = self.plan(post_text='Bilder', medien='["z.jpg","a.png"]')
        self.gw.files.update({'z.jpg': JPG, 'a.png': PNG})
        self.approve(rid)
        self.assertEqual(self.act(rid)['format'], 'bilder')
        calls = self.li.writes()
        self.assertEqual([c['body'] for c in calls if c['method'] == 'PUT'], [JPG, PNG])
        post = calls[-1]
        self.assertEqual(self.body(post)['content'], {'multiImage': {'images': [{'id': 'urn:li:image:IMG1'}, {'id': 'urn:li:image:IMG3'}]}})

    def test_pdf_document_carries_its_file_name_as_title(self):
        rid = self.plan(post_text='Karussell', medien='["karussell-11-2026.pdf"]')
        self.gw.files['karussell-11-2026.pdf'] = PDF
        self.approve(rid)
        self.assertEqual(self.act(rid)['format'], 'dokument')
        init, put, post = self.li.writes()
        self.assertEqual((init['path'], init['query']), ('/rest/documents', 'action=initializeUpload'))
        self.assertEqual(self.body(init), {'initializeUploadRequest': {'owner': ORG}})
        self.assertEqual(put['headers']['authorization'], 'Bearer ' + self.data[s.ACCESS_TOKEN])
        self.assertEqual(self.body(post)['content'], {'media': {'id': 'urn:li:document:DOC1', 'title': 'karussell-11-2026.pdf'}})

    def test_video_parts_without_token_finalize_status_then_post(self):
        data = mp4(9 * MiB)
        rid = self.plan(post_text='Clip', medien='["clip.mp4"]')
        self.gw.files['clip.mp4'] = data
        self.approve(rid)
        self.li.video_status = ['PROCESSING', 'WAITING_UPLOAD', 'AVAILABLE']
        adapter = self.adapter()
        result = adapter.run(r.WriteRequest('posten', rid, freigabe=self.approvals.get((rid, 'posten'))))
        self.assertEqual((result['format'], result['video_urn']), ('video', 'urn:li:video:VID1'))
        calls = self.li.writes()
        init = calls[0]
        self.assertEqual((init['path'], init['query']), ('/rest/videos', 'action=initializeUpload'))
        self.assertEqual(self.body(init), {'initializeUploadRequest': {'owner': ORG, 'fileSizeBytes': len(data),
                                                                       'uploadCaptions': False, 'uploadThumbnail': False}})
        puts = [c for c in calls if c['method'] == 'PUT']
        self.assertEqual(len(puts), 3)
        self.assertEqual(b''.join(p['body'] for p in puts), data)
        for put in puts:
            self.assertNotIn('authorization', put['headers'])  # SEC V1: no token where LinkedIn does not want it
            self.assertEqual(put['headers']['content-type'], 'application/octet-stream')
            self.assertEqual(put['host'], 'www.linkedin.com')
        finalize = calls[4]
        self.assertEqual((finalize['path'], finalize['query']), ('/rest/videos', 'action=finalizeUpload'))
        self.assertEqual(self.body(finalize), {'finalizeUploadRequest': {'video': 'urn:li:video:VID1', 'uploadToken': '',
                         'uploadedPartIds': ['/ambry-videoei/signedId/part-%d.bin' % i for i in range(3)]}})
        status = [c for c in calls if c['method'] == 'GET']
        self.assertEqual([c['path'] for c in status], ['/rest/videos/' + encoded('urn:li:video:VID1')] * 3)
        self.assertEqual(self.body(calls[-1])['content'], {'media': {'id': 'urn:li:video:VID1'}})
        self.assertEqual(adapter.sleep.__self__.sleeps, [s.VIDEO_POLL_SECONDS] * 2)
        self.assertEqual(self.link.actions, 1)
        self.assertEqual(self.link.http, len(calls))

    def test_poll_post_exact_limits_no_upload(self):
        poll = json.dumps({'frage': 'Welche Stufe zuerst?', 'antworten': ['Verbinden', 'Bauen (bald)'], 'dauer': 7})
        rid = self.plan(post_text='Umfrage (kurz)', umfrage=poll)
        self.approve(rid)
        self.assertEqual(self.act(rid)['format'], 'umfrage')
        (post,) = self.li.writes()
        self.assertEqual(self.body(post), {**POST_SHELL, 'commentary': 'Umfrage \\(kurz\\)', 'content': {'poll': {
            'question': 'Welche Stufe zuerst?', 'options': [{'text': 'Verbinden'}, {'text': 'Bauen (bald)'}],
            'settings': {'duration': 'SEVEN_DAYS'}}}})

    def test_edit_partial_update_commentary_only(self):
        rid = self.plan(post_text='Korrigiert (v2)', post_urn='urn:li:ugcPost:42', medien='["a.png"]')
        digest = self.approve(rid, 'bearbeiten')
        result = self.act(rid, 'bearbeiten')
        self.assertEqual(result, {'aktion': 'bearbeiten', 'eintrag_id': rid, 'hash': digest, 'format': 'bearbeiten',
                                  'state': 'bearbeitet', 'post_urn': 'urn:li:ugcPost:42', 'bytes': 0, 'ausgefuehrt': True})
        (call,) = self.li.writes()
        self.assertEqual((call['method'], call['path']), ('POST', '/rest/posts/' + encoded('urn:li:ugcPost:42')))
        self.assertEqual(call['headers']['x-restli-method'], 'PARTIAL_UPDATE')
        self.assertEqual(self.body(call), {'patch': {'$set': {'commentary': 'Korrigiert \\(v2\\)'}}})
        self.assert_headers(call)
        self.assertEqual(self.gw.tool_calls(TOOL_FILE), [])

    def test_delete_needs_its_own_approval(self):
        rid = self.plan(post_urn='urn:li:share:42')
        self.approve(rid, 'loeschen')
        self.assertEqual(self.act(rid, 'loeschen')['state'], 'geloescht')
        (call,) = self.li.writes()
        self.assertEqual((call['method'], call['path']), ('DELETE', '/rest/posts/' + encoded('urn:li:share:42')))
        self.assertEqual(call['headers']['x-restli-method'], 'DELETE')
        self.assertEqual(call['body'], b'')

    def test_own_comment_as_the_organization(self):
        rid = self.plan(post_urn='urn:li:share:42', kommentar_text='Mehr dazu: https://januaport.ai (Preise)')
        self.approve(rid, 'kommentar')
        result = self.act(rid, 'kommentar')
        self.assertEqual((result['state'], result['kommentar_id']), ('kommentiert', '6643206422739898368'))
        (call,) = self.li.writes()
        self.assertEqual((call['method'], call['path']),
                         ('POST', '/rest/socialActions/' + encoded('urn:li:share:42') + '/comments'))
        self.assertEqual(self.body(call), {'actor': ORG, 'object': 'urn:li:share:42',
                                           'message': {'text': 'Mehr dazu: https://januaport.ai (Preise)'}})
        self.assert_no_person()


class WriteToken(Base):
    def test_expired_write_token_is_refreshed_with_exactly_the_write_scope(self):
        self.data = write_identity(self.li.port, days=-1, refresh=True)
        rid = self.plan(post_text='x')
        self.approve(rid)
        self.act(rid)
        token, post = self.li.calls[0], self.li.calls[-1]
        self.assertEqual(token['path'], '/oauth/v2/accessToken')
        self.assertTrue(post['headers']['authorization'].startswith('Bearer WRITE-REFRESHED-'))
        self.li.token_body[s.SCOPE] = 'r_organization_social,rw_organization_admin'  # a read grant answer
        rid = self.plan(post_text='y')
        self.approve(rid)
        self.assertEqual(self.fails(rid), 'unexpected_source_shape')
        self.assertEqual(self.li.api('POST', '/rest/posts')[1:], [])


class ClosedEndpoints(Base):
    """SEC Bed. 1: closed list of method and path; nothing else, before HTTP."""

    def test_endpoint_table_is_exactly_the_reviewed_one(self):
        self.assertEqual(s.WRITE_ENDPOINTS, {
            'post_create': ('POST', 'posts'), 'post_update': ('POST', 'posts/{urn}'),
            'post_delete': ('DELETE', 'posts/{urn}'), 'comment_create': ('POST', 'socialActions/{urn}/comments'),
            'image_init': ('POST', 'images?action=initializeUpload'),
            'document_init': ('POST', 'documents?action=initializeUpload'),
            'video_init': ('POST', 'videos?action=initializeUpload'),
            'video_finalize': ('POST', 'videos?action=finalizeUpload'), 'video_status': ('GET', 'videos/{urn}')})
        self.assertEqual(s.UPLOAD_HOSTS, ('www.linkedin.com',))
        self.assertEqual(s.UPLOAD_TOKEN, {'bild': True, 'dokument': True, 'video': False})
        self.assertEqual(s.WRITE_SCOPES, ('w_organization_social', 'w_organization_social_feed'))
        self.assertEqual(s.VIDEO_PART_BYTES, 4 * 1024 * 1024)

    def test_comment_get_reactions_and_foreign_operations_refused_before_http(self):
        adapter = self.adapter()
        for op in ('comment_list', 'comment_get', 'comment_delete', 'reaction_create', 'post_get', 'video_delete',
                   'video_caption', 'video_thumbnail', 'organization_update', 'post_patch'):
            with self.subTest(op=op), self.assertRaises(ReportError) as cm:
                adapter.api(op, urn='urn:li:share:1')
            self.assertEqual(cm.exception.code, 'route_not_found')
        self.assertEqual(self.li.calls, [])
        self.assertEqual(self.link.actions, 0)

    def test_action_is_counted_before_the_first_linkedin_request(self):
        rid = self.plan(post_text='x', medien='["a.png"]')
        self.gw.files['a.png'] = PNG
        self.approve(rid)
        self.assertEqual(self.fails(rid, link=Link(cap=0)), 'schreib_tageslimit')
        self.assertEqual(self.li.calls, [])


class Approval(Base):
    """SEC Bed. 2: exact core header approval, bound to content and bytes, for all four actions."""

    def test_missing_header_rejected_at_the_actual_adapter(self):
        for action in r.AKTION_WERTE:
            rid = self.plan(post_text='x', kommentar_text='c',
                            **({} if action == 'posten' else {'post_urn': 'urn:li:share:7'}))
            with self.subTest(action=action), self.assertRaises(ReportError) as cm:
                self.adapter().run(r.WriteRequest(action, rid))
            self.assertEqual(cm.exception.status, 409)
            self.assertTrue(cm.exception.nicht_ausgefuehrt)
        self.assertEqual(self.li.calls, [])

    def test_hash_mismatch_nothing_reaches_linkedin(self):
        rid = self.plan(post_text='x', medien='["clip.mp4"]')
        self.gw.files['clip.mp4'] = mp4(8192)
        self.approvals[(rid, 'posten')] = 'freigabe:v2:sha256:' + 'b'*64
        self.assertEqual(self.fails(rid), 'nicht_freigegeben')
        self.assertEqual((self.li.calls, self.link.actions), ([], 0))

    def test_media_bytes_exchanged_after_the_preview(self):
        rid = self.plan(post_text='x', medien='["a.png"]')
        self.gw.files['a.png'] = PNG
        self.approve(rid)
        self.gw.files['a.png'] = PNG + b'anderes Bild'
        self.assertEqual(self.fails(rid), 'nicht_freigegeben')
        self.assertEqual(self.li.calls, [])

    def test_text_changed_after_the_preview(self):
        rid = self.plan(post_text='x')
        self.approve(rid)
        self.gw.plan[rid]['fields']['post_text'] = 'y'
        self.assertEqual(self.fails(rid), 'nicht_freigegeben')
        self.assertEqual(self.li.calls, [])

    def test_approval_field_inside_the_plan_is_worthless(self):
        rid = self.plan(post_text='x')
        digest = self.vorschau(rid)['hash']
        self.gw.plan[rid]['fields'].update({'freigabe': digest, 'hash': digest, 'status': 'freigegeben'})
        self.assertEqual(self.vorschau(rid)['hash'], digest)
        self.assertEqual(self.fails(rid), 'nicht_freigegeben')
        self.assertEqual(self.li.calls, [])

    def test_delete_without_approval_record_and_with_a_foreign_action(self):
        rid = self.plan(post_text='x', post_urn='urn:li:share:42')
        self.assertEqual(self.fails(rid, 'loeschen'), 'nicht_freigegeben')
        self.approvals[(rid, 'loeschen')] = self.vorschau(rid, 'bearbeiten')['hash']
        self.assertEqual(self.fails(rid, 'loeschen'), 'nicht_freigegeben')
        self.approve(rid, 'bearbeiten')
        self.assertEqual(self.fails(rid, 'loeschen'), 'nicht_freigegeben')
        self.assertEqual(self.li.calls, [])

    def test_preview_reads_shows_and_hashes_but_never_writes(self):
        rid = self.plan(post_text='Hallo (x)', medien='["a.png","b.png"]')
        self.gw.files.update({'a.png': PNG, 'b.png': PNG + b'2'})
        preview = self.vorschau(rid)
        self.assertEqual(preview['post_text'], 'Hallo (x)')
        self.assertEqual(preview['format'], 'bilder')
        self.assertEqual(preview['medien'], [{'name': 'a.png', 'datei': 'a.png', 'art': 'bild', 'bytes': len(PNG), 'sha256': r.sha256_hex(PNG)},
                                             {'name': 'b.png', 'datei': 'b.png', 'art': 'bild', 'bytes': len(PNG) + 1,
                                              'sha256': r.sha256_hex(PNG + b'2')}])
        self.assertNotIn('freigegeben', preview)
        self.assertEqual(self.li.calls, [])
        self.approve(rid)
        self.assertEqual(self.vorschau(rid)['text'], 'Hallo (x)')
        self.assertEqual(self.li.calls, [])


class UploadTarget(Base):
    """SEC V1: upload URL only to a fixed host, no redirect, token only where needed."""

    def test_table_of_upload_targets(self):
        good = 'https://www.linkedin.com/dms-uploads/C4E10AQ/uploaded-image/0?ca=vector_ads&cn=uploads&sync=0&v=beta&ut=x'
        self.assertEqual(r.upload_ziel(good), good)
        for url in ('http://www.linkedin.com/dms-uploads/x', 'https://evil.example/dms-uploads/x',
                    'https://www.linkedin.com.evil.example/dms-uploads/x', 'https://evilwww.linkedin.com/dms-uploads/x',
                    'https://x.www.linkedin.com/dms-uploads/x', 'https://www.linkedin.com:443/dms-uploads/x',
                    'https://www.linkedin.com:8443/dms-uploads/x', 'https://~@www.linkedin.com/dms-uploads/x',
                    'https://www.linkedin.com@evil.test/dms-uploads/x', 'https://WWW.LINKEDIN.COM/dms-uploads/x',
                    'https://www.linkedin.com/other/x', 'https://www.linkedin.com/dms-uploads/x#f',
                    'https://www.linkedin.com/dms-uploads/x y', '//www.linkedin.com/dms-uploads/x', '', None, 5,
                    'https://api.linkedin.com/dms-uploads/x', 'https://www.linkedin.com/dms-uploads/x\n'):
            with self.subTest(url=url), self.assertRaises(ReportError) as cm:
                r.upload_ziel(url)
            self.assertEqual(cm.exception.code, 'upload_ziel_abgewiesen')

    def check_target(self, url, entry_files):
        def override(call):
            if call['method'] == 'POST' and call['query'] == 'action=initializeUpload':
                answer = self.li.default(call)
                body = json.loads(answer[2])
                if 'uploadInstructions' in body['value']:
                    body['value']['uploadInstructions'][0]['uploadUrl'] = url
                else:
                    body['value']['uploadUrl'] = url
                return answer[0], answer[1], json.dumps(body).encode()
            return None
        self.li.override = override
        name, data = entry_files
        rid = self.plan(post_text='x', medien=json.dumps([name]))
        self.gw.files[name] = data
        self.approve(rid)
        code = self.fails(rid)
        self.assertEqual(code, 'upload_ziel_abgewiesen')
        self.assertEqual([c for c in self.li.calls if c['method'] == 'PUT'], [])
        self.assertTrue(all(c['host'] == 'api.linkedin.com' for c in self.li.calls))
        self.assertEqual(self.li.api('POST', '/rest/posts'), [])
        self.assertEqual([c for c in self.li.calls if c['query'] == 'action=finalizeUpload'], [])

    def test_foreign_host_http_userinfo_and_port_never_get_a_byte(self):
        for url in ('https://evil.example/dms-uploads/x?ut=TOKENTARGET', 'http://www.linkedin.com/dms-uploads/x',
                    'https://www.linkedin.com@evil.test/dms-uploads/x', 'https://www.linkedin.com:8443/dms-uploads/x'):
            for files in (('a.png', PNG), ('k.pdf', PDF), ('c.mp4', mp4(8192))):
                with self.subTest(url=url, kind=files[0]):
                    self.li.calls.clear()
                    self.check_target(url, files)

    def test_redirect_of_the_upload_url_is_not_followed(self):
        steal = 'http://127.0.0.1:%d/steal' % self.li.port  # stays on the local fake, even if followed
        def override(call):
            if call['method'] == 'PUT':
                return 307, {'Location': steal}, b''
            return None
        self.li.override = override
        for name, data in (('a.png', PNG), ('c.mp4', mp4(8192))):
            with self.subTest(kind=name):
                self.li.calls.clear()
                rid = self.plan(post_text='x', medien=json.dumps([name]))
                self.gw.files[name] = data
                self.approve(rid)
                self.assertEqual(self.fails(rid), 'source_umleitung_abgewiesen')
                self.assertEqual(len([c for c in self.li.calls if c['method'] == 'PUT']), 1)
                self.assertEqual([c for c in self.li.calls if c['path'] == '/steal'], [])
                self.assertEqual(self.li.api('POST', '/rest/posts'), [])
                self.assertEqual([c for c in self.li.calls if c['query'] == 'action=finalizeUpload'], [])

    def test_redirect_of_an_api_answer_is_not_followed_and_carries_no_token(self):
        steal = 'http://127.0.0.1:%d/steal' % self.li.port
        self.li.override = lambda call: (307, {'Location': steal}, b'') if call['method'] == 'GET' else None
        rid = self.plan(post_text='x', medien='["c.mp4"]')
        self.gw.files['c.mp4'] = mp4(8192)
        self.approve(rid)
        self.assertEqual(self.fails(rid), 'source_umleitung_abgewiesen')
        self.assertEqual([c for c in self.li.calls if c['path'] == '/steal'], [])
        self.assertEqual(self.li.api('POST', '/rest/posts'), [])


class UploadBytes(Base):
    """SEC V2: hash over exactly the uploaded bytes, read once, approval before initializeUpload."""

    def test_file_exchanged_between_hash_and_upload_is_not_read_again(self):
        data = mp4(5 * MiB)
        rid = self.plan(post_text='x', medien='["clip.mp4"]')
        self.gw.files['clip.mp4'] = data
        self.approve(rid)
        self.gw.after_read['clip.mp4'] = mp4(5 * MiB)[::-1]  # the Ablage is swapped right after the first read
        self.gw.calls.clear()
        self.act(rid)
        self.assertEqual(len(self.gw.tool_calls(TOOL_FILE)), 1)
        self.assertEqual(b''.join(c['body'] for c in self.li.calls if c['method'] == 'PUT'), data)

    def test_part_list_gaps_overlaps_counts_and_types_abort_without_finalize(self):
        size = 9 * MiB
        def parts(*ranges):
            return [{'uploadUrl': self.li.upload_url('uploadedVideo', 'VID1', '&part=%d' % i), 'firstByte': a, 'lastByte': b}
                    for i, (a, b) in enumerate(ranges)]
        four = 4 * MiB
        cases = {
            'gap': parts((0, four - 1), (four + 1, size - 1)),
            'overlap': parts((0, four - 1), (four - 1, size - 1)),
            'short': parts((0, four - 1), (four, size - 2)),
            'beyond': parts((0, four - 1), (four, size)),
            'too-many': parts(*[(i * MiB, (i + 1) * MiB - 1) for i in range(9)]),
            'reversed': parts((four, size - 1), (0, four - 1)),
            'string': [{'uploadUrl': self.li.upload_url('uploadedVideo', 'VID1'), 'firstByte': '0', 'lastByte': size - 1}],
            'bool': [{'uploadUrl': self.li.upload_url('uploadedVideo', 'VID1'), 'firstByte': False, 'lastByte': size - 1}],
            'empty': [],
        }
        for kind, instructions in cases.items():
            with self.subTest(kind=kind):
                self.li.calls.clear()
                def override(call, instructions=instructions):
                    if call['query'] == 'action=initializeUpload':
                        return 200, {'Content-Type': 'application/json'}, json.dumps({'value': {
                            'video': 'urn:li:video:VID1', 'uploadToken': '', 'uploadInstructions': instructions}}).encode()
                    return None
                self.li.override = override
                rid = self.plan(post_text='x', medien='["clip.mp4"]')
                self.gw.files['clip.mp4'] = mp4(size)
                self.approve(rid)
                link = Link()
                self.assertIn(self.fails(rid, link=link), ('upload_teile_ungueltig', 'unexpected_source_shape'))
                self.assertEqual([c for c in self.li.calls if c['method'] == 'PUT'], [])
                self.assertEqual([c for c in self.li.calls if c['query'] == 'action=finalizeUpload'], [])
                self.assertEqual(self.li.api('POST', '/rest/posts'), [])
                self.assertEqual(link.actions, 1)  # initializeUpload was sent: it counts

    def test_oversized_file_rejected_before_any_linkedin_request(self):
        cfg = config(self.gw.port, max_medium_bytes=4096)
        for name, data in (('c.mp4', mp4(4097)), ('a.png', PNG + b'x' * 5000)):
            with self.subTest(kind=name):
                rid = self.plan(post_text='x', medien=json.dumps([name]))
                self.gw.files[name] = data
                with self.assertRaises(ReportError) as cm:
                    self.adapter(cfg=cfg).vorschau(r.WriteRequest('posten', rid, True))
                self.assertEqual(cm.exception.code, 'medium_zu_gross')
                self.assertEqual(self.fails(rid, cfg=cfg), 'medium_zu_gross')
        self.assertEqual(self.li.calls, [])

    def test_private_copies_are_wiped_on_success_and_on_every_error(self):
        rid = self.plan(post_text='x', medien='["a.png","b.png"]')
        self.gw.files.update({'a.png': PNG, 'b.png': PNG + b'2'})
        self.approve(rid)
        self.copies.clear()
        self.act(rid)
        self.assertEqual(len(self.copies), 2)
        self.assertTrue(all(c.daten == bytearray() and c.geloescht for c in self.copies))
        self.copies.clear()
        self.li.override = lambda call: (500, {}, b'') if call['method'] == 'PUT' else None
        self.approvals.clear()
        self.approve(rid)
        self.copies.clear()
        self.assertEqual(self.fails(rid), 'source_unavailable')
        self.assertTrue(self.copies and all(c.daten == bytearray() and c.geloescht for c in self.copies))
        self.copies.clear()
        self.approvals.clear()
        self.assertEqual(self.fails(rid), 'nicht_freigegeben')
        self.assertTrue(self.copies and all(c.geloescht for c in self.copies))


class VideoStatus(Base):
    """SEC V3: bounded status polling, post only on AVAILABLE, the action counts once."""

    def video_entry(self):
        rid = self.plan(post_text='Clip', medien='["clip.mp4"]')
        self.gw.files['clip.mp4'] = mp4(8192)
        self.approve(rid)
        return rid

    def test_never_available_aborts_after_the_maximum_time_without_post(self):
        rid = self.video_entry()
        self.li.video_status = ['PROCESSING']
        adapter = self.adapter()
        with self.assertRaises(ReportError) as cm:
            adapter.run(r.WriteRequest('posten', rid, freigabe=self.approvals.get((rid, 'posten'))))
        self.assertEqual(cm.exception.code, 'video_nicht_verfuegbar')
        sleeps = adapter.sleep.__self__.sleeps
        self.assertTrue(sleeps and set(sleeps) == {s.VIDEO_POLL_SECONDS})
        self.assertLessEqual(sum(sleeps), s.VIDEO_MAX_WAIT_SECONDS)
        polls = self.li.api('GET')
        self.assertLessEqual(len(polls), s.VIDEO_MAX_WAIT_SECONDS // s.VIDEO_POLL_SECONDS + 1)
        self.assertEqual(self.li.api('POST', '/rest/posts'), [])
        self.assertEqual(self.link.actions, 1)
        self.assertEqual((s.VIDEO_POLL_SECONDS, s.VIDEO_MAX_WAIT_SECONDS), (30, 15 * 60))

    def test_failed_or_unknown_status_aborts(self):
        for status in ('PROCESSING_FAILED', 'DELETED', None):
            with self.subTest(status=status):
                self.li.calls.clear()
                rid = self.video_entry()
                self.li.video_status = [status]
                self.assertIn(self.fails(rid), ('video_verarbeitung_fehlgeschlagen',))
                self.assertEqual(self.li.api('POST', '/rest/posts'), [])


if __name__ == '__main__':
    unittest.main()
