"""Stufe B1 (#1237): pure contract of the write side — config, entry, poll, formats, hash, SSO."""
import hashlib
import json
import unittest
from linkedin_seite import redaktion as r
from linkedin_seite import source_contract as s
from linkedin_seite.domain import Config, ReportError
from support_post import ORG, PNG, PDF, mp4, record, freigabe


def code(callable_, *args, **kwargs):
    try:
        callable_(*args, **kwargs)
    except ReportError as exc:
        return exc.code
    except ValueError as exc:
        return str(exc)
    return None


GOOD = {'gateway_url': 'http://jnpt:8484/mcp', 'medien_ablage': 'redaktion-medien'}


class WriteConfiguration(unittest.TestCase):
    def test_gateway_url_only_mcp_on_internal_host_with_port(self):
        cfg = r.WriteConfig.from_dict(GOOD)
        self.assertEqual(cfg.gateway_url, 'http://jnpt:8484/mcp')
        self.assertEqual((cfg.redaktionsplan, cfg.freigabe, cfg.max_medium_bytes),
                         ('redaktionsplan', 'redaktion_freigabe', 32*1024*1024))
        for url in ('http://jnpt:8484/admin/mcp', 'http://jnpt:8484/mcp/', 'http://jnpt:8484/mcp/v/redaktion',
                    'http://jnpt:8484/MCP', 'http://jnpt:8484/mcp?x=1', 'http://jnpt:8484/mcp#x', 'http://jnpt/mcp',
                    'http://user:pw@jnpt:8484/mcp', 'http://jnpt@evil:8484/mcp', 'ftp://jnpt:8484/mcp',
                    'http://jnpt:0/mcp', 'http://jnpt:65536/mcp', 'http://JNPT:8484/mcp', 'http://jnpt:8484',
                    'http://jnpt:8484/', '', None, 8484, 'http://jnpt:8484/mcp\n', 'http://jnpt:8484/mcp/../admin/mcp',
                    'http://jnpt:8484//mcp', 'http://jnpt:8484/%6dcp'):
            with self.subTest(url=url), self.assertRaises(ValueError):
                r.WriteConfig.from_dict({**GOOD, 'gateway_url': url})
        self.assertEqual(r.WriteConfig.from_dict({**GOOD, 'gateway_url': 'https://gateway.example.test:443/mcp'}).gateway_url,
                         'https://gateway.example.test:443/mcp')

    def test_closed_tool_names_follow_the_core_qualification(self):
        cfg = r.WriteConfig.from_dict(GOOD)
        self.assertEqual(cfg.tools, {'eintrag': 'redaktionsplan_get', 'freigabe': 'redaktion_freigabe_search',
                                     'datei': 'redaktion_medien_get_file'})
        for field, value in (('redaktionsplan', 'Redaktionsplan'), ('freigabe', '../x'), ('medien_ablage', ''),
                             ('medien_ablage', 'a'*64), ('freigabe', 'redaktionsplan'), ('medien_ablage', 'redaktionsplan')):
            with self.subTest(field=field, value=value), self.assertRaises(ValueError):
                r.WriteConfig.from_dict({**GOOD, field: value})

    def test_size_limit_default_and_maximum_32_mib_unknown_keys_rejected(self):
        self.assertEqual(r.MAX_MEDIUM_BYTES, 32*1024*1024)
        self.assertEqual(r.WriteConfig.from_dict({**GOOD, 'max_medium_bytes': 1024}).max_medium_bytes, 1024)
        for value in (32*1024*1024 + 1, 1023, 0, -1, True, '1024', 1.5):
            with self.subTest(value=value), self.assertRaises(ValueError):
                r.WriteConfig.from_dict({**GOOD, 'max_medium_bytes': value})
        for extra in ({'token': 'x'}, {'author': ORG}, {'endpoint': 'x'}, {'scope': 'x'}):
            with self.assertRaises(ValueError):
                r.WriteConfig.from_dict({**GOOD, **extra})
        for missing in ('gateway_url', 'medien_ablage'):
            with self.assertRaises(ValueError):
                r.WriteConfig.from_dict({k: v for k, v in GOOD.items() if k != missing})

    def test_stage_a_config_unchanged_and_write_block_optional(self):
        plain = Config.from_dict({'organization': ORG, 'linkedin_version': '202610'})
        self.assertIsNone(plain.schreiben)
        full = Config.from_dict({'organization': ORG, 'linkedin_version': '202610', 'schreiben': GOOD})
        self.assertEqual(full.schreiben.gateway_url, 'http://jnpt:8484/mcp')
        for bad in ('x', [], None, {'gateway_url': 'http://jnpt:8484/admin/mcp', 'medien_ablage': 'm'}):
            with self.assertRaises(ValueError):
                Config.from_dict({'organization': ORG, 'schreiben': bad})


class Requests(unittest.TestCase):
    def test_write_routes_take_only_the_kartei_id(self):
        req = r.parse_write('create_post', b'{"eintrag_id":"00000000000000ab"}')
        self.assertEqual((req.aktion, req.eintrag_id, req.vorschau), ('posten', '00000000000000ab', False))
        self.assertEqual(r.AKTIONEN, {'create_post': 'posten', 'edit_post': 'bearbeiten',
                                      'delete_post': 'loeschen', 'create_comment': 'kommentar'})
        for raw in (b'', b'[]', b'{}', b'{"eintrag_id":"00000000000000ab","post_text":"x"}',
                    b'{"eintrag_id":"00000000000000ab","datei":"a.png"}', b'{"eintrag_id":"00000000000000ab","post_urn":"urn:li:share:1"}',
                    b'{"eintrag_id":"00000000000000ab","eintrag_id":"00000000000000ac"}', b'{"eintrag_id":"../x"}',
                    b'{"eintrag_id":"00000000000000AB"}', b'{"eintrag_id":171}', b'{"eintrag_id":"00000000000000ab"', b'\xff'):
            with self.subTest(raw=raw):
                self.assertEqual(code(r.parse_write, 'create_post', raw), 'invalid_input')
        self.assertEqual(code(r.parse_write, 'publish', b'{"eintrag_id":"00000000000000ab"}'), 'route_not_found')

    def test_vorschau_query_eintrag_id_and_aktion_only(self):
        req = r.parse_vorschau([('eintrag_id', '00000000000000ab'), ('aktion', 'loeschen')])
        self.assertEqual((req.aktion, req.eintrag_id, req.vorschau), ('loeschen', '00000000000000ab', True))
        for pairs in ([], [('eintrag_id', '00000000000000ab')], [('eintrag_id', '00000000000000ab'), ('aktion', 'reagieren')],
                      [('eintrag_id', '00000000000000ab'), ('aktion', 'posten'), ('aktion', 'posten')],
                      [('eintrag_id', '00000000000000ab'), ('aktion', 'posten'), ('post_text', 'x')]):
            with self.subTest(pairs=pairs):
                self.assertEqual(code(r.parse_vorschau, pairs), 'invalid_input')

    def test_media_names_are_plain_file_names_rejected_before_any_call(self):
        for name in ('bild.png', 'Karussell_11-2026.PDF', 'clip.mp4', 'a.jpeg', 'b.gif'):
            self.assertEqual(r.check_datei(name), name)
        for name in ('', '/etc/passwd', 'ordner/bild.png', 'ordner\\bild.png', '..', '../bild.png', 'a..png',
                     'C:\\bild.png', '\\\\server\\x.png', '.bild.png', 'bild.png\x00', 'bild .png', 'bild.exe',
                     'bild', 'ä.png', 'a'*124 + '.png', None, 7):
            with self.subTest(name=name):
                self.assertEqual(code(r.check_datei, name), 'medium_name_ungueltig')
        self.assertEqual([r.medium_art(n) for n in ('a.png', 'a.JPG', 'a.jpeg', 'a.gif', 'a.pdf', 'a.mp4')],
                         ['bild', 'bild', 'bild', 'bild', 'dokument', 'video'])


def entry(aktion='posten', **fields):
    rec = record('00000000000000ab', fields)
    return r.entry_from_record(rec, '00000000000000ab', aktion)


POLL = json.dumps({'frage': 'Welche Stufe zuerst?', 'antworten': ['Verbinden', 'Bauen', 'Überblick'], 'dauer': 7})


class Entries(unittest.TestCase):
    def test_fields_are_strings_json_lists_and_objects_fail_closed(self):
        e = entry(post_text='Hallo', medien='["a.png","b.png"]', kanal='linkedin_seite', status='bereit')
        self.assertEqual((e.post_text, e.medien), ('Hallo', ('a.png', 'b.png')))
        for medien in ('a.png', '{"a":1}', '[1]', '["a.png","a.png"]', '["../a.png"]', '[]x', '["a.png"', 'null',
                       json.dumps(['b%d.png' % i for i in range(21)])):
            with self.subTest(medien=medien):
                self.assertIn(code(entry, 'posten', post_text='x', medien=medien), ('medien_ungueltig', 'medium_name_ungueltig'))
        for field in ('post_text', 'medien', 'umfrage', 'post_urn', 'kommentar_text'):
            with self.subTest(field=field):
                self.assertEqual(code(entry, 'posten', **{'post_text': 'x', field: 5}), 'eintrag_ungueltig')
        self.assertEqual(entry(post_text='x', medien='[]').medien, ())
        self.assertEqual(entry(post_text='x').medien, ())

    def test_record_must_be_the_requested_one(self):
        rec = record('00000000000000ac', {'post_text': 'x'})
        self.assertEqual(code(r.entry_from_record, rec, '00000000000000ab', 'posten'), 'gateway_antwort_ungueltig')
        for bad in (None, [], {'id': '00000000000000ab'}, {'id': '00000000000000ab', 'fields': []}):
            self.assertEqual(code(r.entry_from_record, bad, '00000000000000ab', 'posten'), 'gateway_antwort_ungueltig')

    def test_per_action_requirements(self):
        self.assertEqual(code(entry, 'posten'), 'post_text_fehlt')
        self.assertEqual(code(entry, 'posten', post_text='x', post_urn='urn:li:share:1'), 'bereits_veroeffentlicht')
        self.assertEqual(code(entry, 'bearbeiten', post_text='x'), 'post_urn_fehlt')
        self.assertEqual(code(entry, 'bearbeiten', post_urn='urn:li:share:1'), 'post_text_fehlt')
        self.assertEqual(code(entry, 'loeschen'), 'post_urn_fehlt')
        self.assertEqual(code(entry, 'kommentar', post_urn='urn:li:share:1'), 'kommentar_text_fehlt')
        for urn in ('urn:li:person:1', 'urn:li:organization:1', 'urn:li:share:x', 'urn:li:share:1/comments',
                    'urn:li:share:1 ', 'urn:li:activity:1', 'urn:li:comment:(urn:li:share:1,2)'):
            with self.subTest(urn=urn):
                self.assertEqual(code(entry, 'loeschen', post_urn=urn), 'post_urn_ungueltig')
        self.assertEqual(entry('loeschen', post_urn='urn:li:ugcPost:7').post_urn, 'urn:li:ugcPost:7')
        self.assertEqual(entry('kommentar', post_urn='urn:li:share:7', kommentar_text='Link: https://januaport.ai').kommentar_text,
                         'Link: https://januaport.ai')

    def test_comment_text_with_a_urn_is_refused_until_little_text_is_proven_for_comments(self):
        # The comment text goes unescaped (Comments API documents no little text): a mention needs a URN.
        for text in ('Danke @[Max](urn:li:person:1)', 'siehe urn:li:organization:1', 'URN:LI:member:1'):
            with self.subTest(text=text):
                self.assertEqual(code(entry, 'kommentar', post_urn='urn:li:share:1', kommentar_text=text), 'kommentar_erwaehnung')
        self.assertEqual(entry('kommentar', post_urn='urn:li:share:1', kommentar_text='Danke (an alle) @team').kommentar_text,
                         'Danke (an alle) @team')

    def test_pseudonymised_fields_are_never_posted(self):
        # SEC Bed. 5: no privacy labels on plan fields; a veil token would leave the house as text.
        self.assertEqual(code(entry, 'posten', post_text='Hallo ‹name-a1b2›'), 'pseudonym_im_feld')
        self.assertEqual(code(entry, 'kommentar', post_urn='urn:li:share:1', kommentar_text='‹ort-1›'), 'pseudonym_im_feld')


class Polls(unittest.TestCase):
    def poll(self, **change):
        value = {'frage': 'Welche Stufe zuerst?', 'antworten': ['Verbinden', 'Bauen'], 'dauer': 3, **change}
        return json.dumps({k: v for k, v in value.items() if v is not None})

    def test_limits_checked_before_sending_nothing_is_shortened(self):
        good = entry(post_text='x', umfrage=self.poll(frage='F'*140, antworten=['A'*30, 'B', 'C', 'D'], dauer=14))
        self.assertEqual(good.umfrage.frage, 'F'*140)
        self.assertEqual(good.umfrage.antworten, ('A'*30, 'B', 'C', 'D'))
        for dauer in (1, 3, 7, 14):
            self.assertEqual(entry(post_text='x', umfrage=self.poll(dauer=dauer)).umfrage.dauer, dauer)
        for change in ({'frage': 'F'*141}, {'frage': ''}, {'antworten': ['A']}, {'antworten': ['A', 'B', 'C', 'D', 'E']},
                       {'antworten': ['A'*31, 'B']}, {'antworten': ['A', '']}, {'antworten': ['A', 'A']},
                       {'antworten': ['A', 2]}, {'antworten': 'A,B'}, {'dauer': 2}, {'dauer': 30}, {'dauer': '7'},
                       {'dauer': True}, {'dauer': 7.0}, {'frage': None}, {'extra': 'x'}):
            with self.subTest(change=change):
                self.assertEqual(code(entry, 'posten', post_text='x', umfrage=self.poll(**change)), 'umfrage_ungueltig')
        for raw in ('[]', '"x"', '{', '{"frage":"a","frage":"b","antworten":["a","b"],"dauer":3}'):
            self.assertEqual(code(entry, 'posten', post_text='x', umfrage=raw), 'umfrage_ungueltig')

    def test_no_media_together_with_a_poll(self):
        e = entry(post_text='x', umfrage=self.poll(), medien='["a.png"]')
        self.assertEqual(code(r.check_format, e, 'posten'), 'umfrage_mit_medien')
        self.assertEqual(r.check_format(entry(post_text='x', umfrage=self.poll()), 'posten'), 'umfrage')
        self.assertEqual(s.POLL_DURATIONS, {1: 'ONE_DAY', 3: 'THREE_DAYS', 7: 'SEVEN_DAYS', 14: 'FOURTEEN_DAYS'})


class Formats(unittest.TestCase):
    def test_supported_combinations(self):
        cases = {'[]': 'text', '["a.png"]': 'bild', '["a.png","b.jpg"]': 'bilder', '["k.pdf"]': 'dokument',
                 '["c.mp4"]': 'video'}
        for medien, expected in cases.items():
            self.assertEqual(r.check_format(entry(post_text='x', medien=medien), 'posten'), expected)
        twenty = json.dumps(['b%d.png' % i for i in range(20)])
        self.assertEqual(r.check_format(entry(post_text='x', medien=twenty), 'posten'), 'bilder')
        for medien in ('["a.png","k.pdf"]', '["k.pdf","l.pdf"]', '["c.mp4","d.mp4"]', '["a.png","c.mp4"]'):
            with self.subTest(medien=medien):
                self.assertEqual(code(r.check_format, entry(post_text='x', medien=medien), 'posten'), 'medien_kombination_ungueltig')

    def test_file_content_must_match_its_extension(self):
        r.check_magic('bild', PNG)
        r.check_magic('dokument', PDF)
        r.check_magic('video', mp4(4096))
        for art, data in (('bild', PDF), ('dokument', PNG), ('video', PNG), ('bild', b''), ('video', b'\x00'*16)):
            with self.subTest(art=art):
                self.assertEqual(code(r.check_magic, art, data), 'medium_format_ungueltig')


class Hash(unittest.TestCase):
    """SEC Bed. 2: SHA-256 over the canonical form. Frozen vectors: a change breaks the approvals."""

    def media(self, *items):
        return [(name, len(data), hashlib.sha256(data).hexdigest()) for name, data in items]

    def test_canonical_form_is_fixed(self):
        e = entry(post_text='Ein Post mit Link: https://januaport.ai', medien='["a.png"]')
        self.assertEqual(r.canonical('posten', e, self.media(('a.png', PNG))),
                         ('{"aktion":"posten","medien":[{"bytes":21,"datei":"a.png","sha256":"%s"}],'
                          '"post_text":"Ein Post mit Link: https://januaport.ai","umfrage":null}'
                          % hashlib.sha256(PNG).hexdigest()).encode())
        self.assertEqual(r.HASH_PREFIX, b'jnpt-linkedin-seite-freigabe/v1\n')

    def test_table_of_frozen_vectors(self):
        poll = json.dumps({'dauer': 7, 'antworten': ['Ja', 'Nein'], 'frage': 'Gut?'})
        table = [
            ('posten', entry(post_text='Hallo'), [], '9c37da07fcf73161aaf1cd2243ee4a4a241d9fc6a792a203d6badb69927a8cf0'),
            ('posten', entry(post_text='Hallo', medien='["a.png"]'), self.media(('a.png', PNG)), '8ee6658f4c73f177be8161ddff97bf6d6f531df6185364e41f3f63b8d5a8b60e'),
            ('posten', entry(post_text='Hallo', umfrage=poll), [], 'e687a631525fce748578f6e91a08f65e2631f43952ca94c9bad89cdd7b6519f4'),
            ('bearbeiten', entry('bearbeiten', post_text='Hallo', post_urn='urn:li:share:7'), [], '0bbf1403d94b00c641bc6f2c256272c5aee3b14470f2e8b5111f80f3547df746'),
            ('loeschen', entry('loeschen', post_urn='urn:li:share:7'), [], 'd528f4f7520c367556b622a25efaf1427f8f90a9b4752f201914aebe48740cd1'),
            ('kommentar', entry('kommentar', post_urn='urn:li:share:7', kommentar_text='Hallo'), [], '2af7aa349b4a6b0051ff60ce8e98d6e9af4fa1ce2e29fc28c6024b6c9662f908'),
        ]
        digests = []
        for aktion, e, media, frozen in table:
            digest = r.freigabe_hash(aktion, e, media)
            self.assertRegex(digest, r'^[0-9a-f]{64}$')
            self.assertEqual(digest, hashlib.sha256(r.HASH_PREFIX + r.canonical(aktion, e, media)).hexdigest())
            if frozen:
                self.assertEqual(digest, frozen)
            digests.append(digest)
        self.assertEqual(len(set(digests)), len(digests))  # action and fields always separate

    def test_every_relevant_change_changes_the_hash_irrelevant_ones_do_not(self):
        base = r.freigabe_hash('posten', entry(post_text='Hallo', medien='["a.png","b.png"]'),
                               self.media(('a.png', PNG), ('b.png', PNG + b'2')))
        changed = [
            r.freigabe_hash('posten', entry(post_text='Hallo!', medien='["a.png","b.png"]'), self.media(('a.png', PNG), ('b.png', PNG + b'2'))),
            r.freigabe_hash('posten', entry(post_text='Hallo', medien='["b.png","a.png"]'), self.media(('b.png', PNG + b'2'), ('a.png', PNG))),
            r.freigabe_hash('posten', entry(post_text='Hallo', medien='["a.png","b.png"]'), self.media(('a.png', PNG), ('b.png', PNG + b'3'))),
            r.freigabe_hash('posten', entry(post_text='Hallo', medien='["a.png","c.png"]'), self.media(('a.png', PNG), ('c.png', PNG + b'2'))),
        ]
        self.assertNotIn(base, changed)
        same = r.freigabe_hash('posten', entry(post_text='Hallo', medien='["a.png", "b.png"]', status='bereit', kanal='x'),
                               self.media(('a.png', PNG), ('b.png', PNG + b'2')))
        self.assertEqual(base, same)
        p1 = json.dumps({'frage': 'Gut?', 'antworten': ['Ja', 'Nein'], 'dauer': 7})
        p2 = json.dumps({'dauer': 7, 'frage': 'Gut?', 'antworten': ['Ja', 'Nein']}, indent=2)
        p3 = json.dumps({'frage': 'Gut?', 'antworten': ['Nein', 'Ja'], 'dauer': 7})
        h = [r.freigabe_hash('posten', entry(post_text='x', umfrage=p), []) for p in (p1, p2, p3)]
        self.assertEqual(h[0], h[1])  # canonical: key order and whitespace of the JSON field
        self.assertNotEqual(h[0], h[2])  # order of the answers counts
        self.assertNotEqual(r.freigabe_hash('loeschen', entry('loeschen', post_urn='urn:li:share:7'), []),
                            r.freigabe_hash('loeschen', entry('loeschen', post_urn='urn:li:share:8'), []))
        self.assertEqual(r.freigabe_hash('loeschen', entry('loeschen', post_urn='urn:li:share:7', post_text='egal'), []),
                         r.freigabe_hash('loeschen', entry('loeschen', post_urn='urn:li:share:7'), []))


class Approval(unittest.TestCase):
    def test_only_an_exact_sso_written_record_approves(self):
        digest = 'a'*64
        ok = freigabe('00000000000000ab', 'posten', digest)
        self.assertTrue(r.freigegeben([ok], '00000000000000ab', 'posten', digest))
        for bad in (freigabe('00000000000000ac', 'posten', digest), freigabe('00000000000000ab', 'bearbeiten', digest),
                    freigabe('00000000000000ab', 'posten', 'b'*64), freigabe('00000000000000ab', 'posten', digest.upper()),
                    freigabe('00000000000000ab', 'posten', digest, via='inb-team-token'),
                    freigabe('00000000000000ab', 'posten', digest, updated_via='inb-team-token'),
                    freigabe('00000000000000ab', 'posten', digest, updated_via=''),
                    {'fields': ok['fields']}, {'via': 'SSO', 'updated_via': 'SSO', 'fields': 'x'}, 'x', None):
            with self.subTest(bad=bad):
                self.assertFalse(r.freigegeben([bad], '00000000000000ab', 'posten', digest))
        self.assertFalse(r.freigegeben([], '00000000000000ab', 'posten', digest))
        self.assertTrue(r.freigegeben([freigabe('00000000000000ab', 'posten', 'b'*64), ok], '00000000000000ab', 'posten', digest))


class LittleText(unittest.TestCase):
    def test_reserved_characters_escaped_mentions_impossible_hashtags_kept(self):
        self.assertEqual(r.little('Hallo (Welt) [x] {y} <z> @team | ~ * _ \\'),
                         'Hallo \\(Welt\\) \\[x\\] \\{y\\} \\<z\\> \\@team \\| \\~ \\* \\_ \\\\')
        self.assertEqual(r.little('@[Max](urn:li:person:1)'), '\\@\\[Max\\]\\(urn:li:person:1\\)')
        self.assertEqual(r.little('#KI und #Mittelstand2026, a#b, # allein, ##x'),
                         '#KI und #Mittelstand2026, a\\#b, \\# allein, \\##x')
        self.assertEqual(r.little('https://januaport.ai/preise?a=1&b=2'), 'https://januaport.ai/preise?a=1&b=2')
        self.assertEqual(set(s.LITTLE_RESERVED), set('|{}@[]()<>#\\*_~'))


if __name__ == '__main__':
    unittest.main()
