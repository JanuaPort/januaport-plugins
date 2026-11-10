"""Stufe B3 (#1237): the DRAFT core template linkedin-seite-community (CONNECTOR ports it to the core).

SEC c-6747087768 S2 (anlass as enum in the template, exact values) and S3 (daily_cap counts attempts,
the description says so). The core repository is read-only here; this guards the handed-over draft.
Text checks only: the service image has no YAML parser and needs none.
"""
from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parents[1]
DRAFT = ROOT / 'entwurf' / 'linkedin-seite-community.yaml'


@unittest.skipUnless(DRAFT.exists(), 'draft not mounted')
class TemplateDraft(unittest.TestCase):
    def setUp(self):
        self.text = DRAFT.read_text(encoding='utf-8')

    def tool(self, name):
        match = re.search(r'^  - name: %s\n(.*?)(?=^  - name: |\Z)' % name, self.text, re.M | re.S)
        self.assertIsNotNone(match, name)
        return match.group(1)

    def test_head_route_timeout_and_audit(self):
        for line in ('name: linkedin-seite-community', 'base_url: http://linkedin-seite:8092', 'credential: linkedin-seite',
                     'timeout: 120s', 'redact_params: [text]'):
            self.assertRegex(self.text, r'(?m)^\s*' + re.escape(line) + r'$')
        self.assertIn('Audit depth MUST be metadaten', self.text)
        self.assertEqual(re.findall(r'^  - name: (\w+)$', self.text, re.M), ['comments', 'tagesliste', 'reply'])

    def test_reads_are_get_routes_without_write(self):
        for name, path in (('comments', '/v1/linkedin-kommentare/comments'), ('tagesliste', '/v1/linkedin-kommentare/tagesliste')):
            block = self.tool(name)
            self.assertIn('method: GET', block)
            self.assertIn('path: ' + path, block)
            self.assertNotIn('write:', block)
            self.assertNotIn('daily_cap', block)

    def test_reply_is_a_write_with_daily_cap_8_attempts_and_without_freigabe(self):
        block = self.tool('reply')
        self.assertIn('    write: true\n', block)
        self.assertIn('    daily_cap: 8\n', block)
        self.assertNotIn('freigabe', self.text.replace('freigabe_anfordern', ''))
        self.assertIn('Versuche', block)
        self.assertIn('method: POST', block)
        self.assertIn('path: /v1/linkedin-kommentare/reply', block)
        self.assertIn('"linkedin-seite-community:reply"', block)

    def test_anlass_is_the_exact_enum(self):
        block = self.tool('reply')
        self.assertEqual(re.findall(r'enum: \[([^\]]*)\]', block), ['dank, sachfrage_zum_post, gespraech_anbieten'])
        for param in ('post_urn', 'kommentar_urn', 'text', 'anlass'):
            self.assertRegex(block, r'- name: %s\n        type: string\n        required: true' % param)

    def test_activation_rule_names_the_three_rights_and_the_daily_list_elsewhere(self):
        for phrase in ('linkedin-seite-community:comments', 'linkedin-seite-community:reply', 'linkedin-seite:posts',
                       'linkedin-seite-community:tagesliste', 'no Redaktionsplan'):
            self.assertIn(phrase, self.text)


if __name__ == '__main__':
    unittest.main()
