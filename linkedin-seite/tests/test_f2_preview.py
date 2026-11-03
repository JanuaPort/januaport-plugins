"""LEAD #1306: actual HTTP/adapter contract against the unchanged F2 example."""
import copy
from dataclasses import replace
import hashlib
import json
from pathlib import Path
from datetime import datetime, timedelta, timezone

from support_post import PDF, TOOL_GET, record
from test_post_server import Service

EXAMPLE = Path(__file__).with_name('data') / 'linkedin_vorschau_beispiel.json'


def effect_of(preview):
    return {'aktion': preview['aktion'],
            'medien': [{'name': m['name'], 'sha256': m['sha256']} for m in preview['medien']],
            'text': preview['text'], 'umfrage': preview['umfrage'], 'ziel_urn': preview['ziel_urn']}


def independent_hash(preview):
    canonical = json.dumps(effect_of(preview), ensure_ascii=False, sort_keys=True, separators=(',', ':'))
    return 'freigabe:v2:sha256:' + hashlib.sha256(('freigabe:v2\n' + canonical).encode('utf-8')).hexdigest()


class F2Preview(Service):
    def schema(self, actual, expected):
        self.assertIs(type(actual), type(expected))
        if isinstance(expected, dict):
            self.assertEqual(set(actual), set(expected))
            for key in expected:
                self.schema(actual[key], expected[key])
        elif isinstance(expected, list):
            self.assertEqual(len(actual), len(expected))
            for value, reference in zip(actual, expected):
                self.schema(value, reference)

    async def test_exact_f2_example_shape_types_and_effect_hash(self):
        example = json.loads(EXAMPLE.read_text(encoding='utf-8'))
        self.assertEqual(independent_hash(example), example['hash'])
        self.cfg = replace(self.cfg, organization=example['ziel_urn'])
        self.writes.cfg = self.cfg
        rid = example['eintrag_id']
        name = example['medien'][0]['datei']
        self.gw.plan[rid] = record(rid, {'post_text': example['post_text'], 'medien': json.dumps([name])})
        # The core example hashes plain synthetic text, not a PDF. Keep real format validation.
        self.gw.files[name] = PDF
        status, preview = await self.vorschau(rid)
        self.assertEqual(status, 200, preview)
        self.schema(preview, example)
        expected = copy.deepcopy(example)
        expected['medien'][0].update(sha256=hashlib.sha256(PDF).hexdigest(), bytes=len(PDF))
        expected['hash'] = independent_hash(expected)
        self.assertEqual(preview, expected)
        self.assertEqual(preview['hash'], independent_hash(preview))
        self.assertEqual([c[1] for c in self.gw.tool_calls()].count(TOOL_GET), 1)
        self.assertEqual(self.li.calls, [])

    async def test_all_actions_have_only_f2_keys_and_direct_effect_fields(self):
        keys = set(json.loads(EXAMPLE.read_text(encoding='utf-8')))
        for action in ('posten', 'bearbeiten', 'loeschen', 'kommentar'):
            fields = {'post_text': 'Post', 'kommentar_text': 'Kommentar'}
            if action != 'posten':
                fields['post_urn'] = 'urn:li:share:7'
            rid = self.plan(**fields)
            status, preview = await self.vorschau(rid, action)
            self.assertEqual(status, 200)
            self.assertEqual(set(preview), keys)
            self.assertEqual(preview['ziel_urn'], self.cfg.organization if action == 'posten' else fields['post_urn'])
            self.assertEqual(preview['text'], 'Kommentar' if action == 'kommentar' else '' if action == 'loeschen' else 'Post')
            self.assertEqual(preview['medien'], [])
            self.assertIsNone(preview['umfrage'])
            self.assertEqual(preview['hash'], independent_hash(preview))
        self.assertEqual(self.li.calls, [])

    async def test_poll_shape_and_hash_are_direct_and_keep_integer_days(self):
        poll = {'frage': 'Welche?', 'antworten': ['A', 'B'], 'dauer': 7}
        rid = self.plan(post_text='Umfrage', umfrage=json.dumps(poll))
        status, preview = await self.vorschau(rid)
        self.assertEqual(status, 200)
        self.assertEqual(set(preview), set(json.loads(EXAMPLE.read_text(encoding='utf-8'))))
        self.schema(preview['umfrage'], poll)
        self.assertEqual(preview['umfrage'], poll)
        self.assertEqual(preview['hash'], independent_hash(preview))
        self.assertEqual(self.li.calls, [])

    async def test_only_the_four_optional_warning_fields_from_f2_are_added(self):
        expiry = (datetime.now(timezone.utc) + timedelta(days=1)).isoformat()
        self.pool.identity['access_expires_at'] = expiry
        self.writes.identity['access_expires_at'] = expiry
        rid = self.plan(post_text='x')
        status, preview = await self.vorschau(rid)
        self.assertEqual(status, 200)
        allowed = {'warnung', 'ablaufdatum', 'warnung_schreiben', 'ablaufdatum_schreiben'}
        self.assertEqual(set(preview), set(json.loads(EXAMPLE.read_text(encoding='utf-8'))) | allowed)
        for key in allowed:
            self.assertIs(type(preview[key]), str)
        self.assertEqual(preview['hash'], independent_hash(preview))
