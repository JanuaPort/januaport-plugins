"""#1237, 0.3.1: one consent with all four scopes. A second consent with a different set is not offered.

LinkedIn: "If you request a different scope than the previously granted scope, all the previous
access tokens are invalidated." (authorization-code-flow, Access Token Scopes and Lifetime). The
second grant of 03.11. cost the read path its token, so --gesamt is the only way left.
"""
import contextlib
import inspect
import io
import json
import unittest
from urllib.parse import parse_qs, urlsplit
from unittest.mock import patch
from linkedin_seite import oauth
from linkedin_seite import source_contract as s
from support import GRANT, GRANT_COMMA, GRANT_LIST
import test_consent as helper

consent = getattr(helper, 'consent', None)


@unittest.skipUnless(consent is not None, 'PO tool not shipped in service image')
class GrantConsent(unittest.TestCase):
    # Borrow the loopback browser fixture of the Stufe-A consent tests without re-running them.
    setUp, tearDown = helper.Consent.setUp, helper.Consent.tearDown
    browser, run_consent = helper.Consent.browser, helper.Consent.run_consent

    def cli(self, *flags, stdin='synthetic-secret\n'):
        output, errors = io.StringIO(), io.StringIO()
        original = consent.consent
        with patch('sys.argv', ['z', '--client-id', 'synthetic-client', '--secret-stdin', *flags]), \
             patch('sys.stdin', io.StringIO(stdin)), contextlib.redirect_stdout(output), contextlib.redirect_stderr(errors), \
             patch.object(consent, 'consent', lambda *a, **kw: original(
                 *a, **kw, opener=self.browser, transport=helper.LocalTransport(lambda: None, self.fake.port))):
            try:
                code = consent.main()
            except SystemExit as exc:
                code = exc.code
        return code, output.getvalue(), errors.getvalue()

    def test_one_authorization_requests_exactly_the_four_scopes(self):
        out = self.run_consent()
        self.assertEqual(len(self.seen), 1)
        self.assertEqual(self.seen[0][s.SCOPE], [GRANT])
        self.assertEqual(parse_qs(urlsplit(consent.authorization('c', 's')).query)[s.SCOPE], [GRANT])
        self.assertEqual(out[s.SCOPE], GRANT)

    def test_vault_json_is_what_the_service_loads(self):
        out = self.run_consent()
        self.assertEqual(oauth.validate_identity(dict(out))[s.CLIENT_SECRET], 'synthetic-secret')
        self.assertEqual(oauth.scope_set(out[s.SCOPE]), set(GRANT_LIST))

    def test_separator_and_order_of_the_answer_are_tolerated_the_json_is_normalised(self):
        for answer in (GRANT_COMMA, GRANT, ', '.join(reversed(GRANT_LIST))):
            with self.subTest(answer=answer):
                self.fake.token_body[s.SCOPE] = answer
                self.assertEqual(self.run_consent()[s.SCOPE], GRANT)

    def test_a_different_set_in_the_answer_ends_the_consent_without_a_vault_json(self):
        for answer in ('r_organization_social,rw_organization_admin', 'w_organization_social,w_organization_social_feed',
                       ','.join(GRANT_LIST[:3]), GRANT_COMMA + ',r_ads', '', GRANT_COMMA.replace(',', ';')):
            with self.subTest(answer=answer):
                self.fake.token_body[s.SCOPE] = answer
                code, output, errors = self.cli('--gesamt')
                self.assertEqual((code, output), (1, ''))
                self.assertIn('zustimmung_fehlgeschlagen', errors)

    def test_an_answer_without_scope_ends_the_consent_without_a_vault_json(self):
        # The JSON states the grant. A set nobody confirmed must not be written down as proof.
        del self.fake.token_body[s.SCOPE]
        code, output, errors = self.cli('--gesamt')
        self.assertEqual((code, output), (1, ''))
        self.assertIn('zustimmung_fehlgeschlagen', errors)

    def test_cli_writes_the_json_and_names_the_one_place(self):
        code, output, errors = self.cli('--gesamt')
        self.assertEqual(code, 0)
        self.assertEqual(json.loads(output)[s.SCOPE], GRANT)
        self.assertIn('plugin-linkedin-seite-oauth', errors)
        self.assertNotIn('post-oauth', errors)
        for scope in GRANT_LIST:
            self.assertIn(scope, errors)

    def test_without_gesamt_nothing_happens(self):
        # The old read-only line must not silently ask for four scopes or for two: it stops.
        code, output, errors = self.cli()
        self.assertEqual((code, output, errors), (2, '', 'ungueltige_argumente\n'))
        self.assertEqual(self.seen, [])
        self.assertEqual(self.fake.calls, [])

    def test_schreiben_is_gone_and_cannot_create_a_second_grant(self):
        code, output, errors = self.cli('--schreiben')
        self.assertEqual((code, output, errors), (2, '', 'ungueltige_argumente\n'))
        code, output, errors = self.cli('--schreiben', '--gesamt')
        self.assertEqual((code, output, errors), (2, '', 'ungueltige_argumente\n'))
        self.assertEqual(self.seen, [])

    def test_the_script_offers_no_scope_choice(self):
        # Signature only: calling the old consent() would start the real browser and wait five minutes.
        for function in (consent.consent, consent.authorization):
            self.assertNotIn('scopes', inspect.signature(function).parameters)


if __name__ == '__main__':
    unittest.main()
