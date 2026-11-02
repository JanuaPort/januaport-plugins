"""Stufe B1 (#1237) SEC Bed. 4: the consent script asks for exactly the write scope in its own mode."""
import contextlib
import io
import json
import unittest
from urllib.parse import parse_qs, urlsplit
from unittest.mock import patch
from linkedin_seite import oauth
from linkedin_seite import source_contract as s
import test_consent as helper

consent = getattr(helper, 'consent', None)


@unittest.skipUnless(consent is not None, 'PO tool not shipped in service image')
class WriteConsent(unittest.TestCase):
    # Borrow the loopback browser fixture of the Stufe-A consent tests without re-running them.
    setUp, tearDown = helper.Consent.setUp, helper.Consent.tearDown
    browser, run_consent = helper.Consent.browser, helper.Consent.run_consent

    def test_write_mode_requests_exactly_w_organization_social(self):
        self.fake.token_body.update({s.SCOPE: 'w_organization_social'})
        out = consent.consent('synthetic-client', 'synthetic-secret', opener=self.browser,
                              transport=helper.LocalTransport(lambda: None, self.fake.port), scopes=s.WRITE_SCOPES)
        self.assertEqual(self.seen[0][s.SCOPE], ['w_organization_social'])
        self.assertEqual(out['scope'], 'w_organization_social')
        self.assertEqual(oauth.validate_write_identity(dict(out))[s.CLIENT_SECRET], 'synthetic-secret')
        with self.assertRaises(ValueError):
            oauth.validate_identity(dict(out))  # never usable as the read token

    def test_read_mode_unchanged_and_without_scope_field(self):
        out = self.run_consent()
        self.assertEqual(set(self.seen[0][s.SCOPE][0].split()), {'rw_organization_admin', 'r_organization_social'})
        self.assertNotIn('scope', out)
        with self.assertRaises(ValueError):
            oauth.validate_write_identity(dict(out))

    def test_write_mode_rejects_a_grant_with_read_scopes(self):
        self.fake.token_body.update({s.SCOPE: 'w_organization_social,rw_organization_admin'})
        with self.assertRaises(Exception):
            consent.consent('synthetic-client', 'synthetic-secret', opener=self.browser,
                            transport=helper.LocalTransport(lambda: None, self.fake.port), scopes=s.WRITE_SCOPES)

    def test_cli_flag_and_stderr_names_the_write_place(self):
        self.fake.token_body.update({s.SCOPE: 'w_organization_social'})
        output, errors = io.StringIO(), io.StringIO()
        original = consent.consent
        args = ['z', '--client-id', 'synthetic-client', '--secret-stdin', '--schreiben']
        with patch('sys.argv', args), patch('sys.stdin', io.StringIO('synthetic-secret\n')), \
             contextlib.redirect_stdout(output), contextlib.redirect_stderr(errors), \
             patch.object(consent, 'consent', lambda *a, **kw: original(*a, **kw, opener=self.browser,
                                                                      transport=helper.LocalTransport(lambda: None, self.fake.port))):
            self.assertEqual(consent.main(), 0)
        self.assertEqual(json.loads(output.getvalue())['scope'], 'w_organization_social')
        self.assertIn('plugin-linkedin-seite-post-oauth', errors.getvalue())
        self.assertEqual(parse_qs(urlsplit(consent.authorization('c', 's', scopes=s.WRITE_SCOPES)).query)[s.SCOPE],
                         ['w_organization_social'])


if __name__ == '__main__':
    unittest.main()
