"""OAuth token response tolerance (JanuaPort/januaport#1237, c-6571493150).

The real LinkedIn consent was rejected twice: `scope` arrives comma separated, and
`refresh_token_expires_in` overshot 365 days by 59 seconds. The contract stays strict where
it protects (exact scope set, bounded lifetimes, never longer than the documented maximum)
and tolerant where the vendor is imprecise (separator, small overshoot that is capped).
"""
from datetime import datetime, timedelta, timezone
import unittest
from linkedin_seite import source_contract as s
from linkedin_seite.domain import ReportError
from linkedin_seite.oauth import ACCESS_DAYS, REFRESH_DAYS, Tokens, token_result, validate_identity
from support import CFG, Fake, GRANT, GRANT_COMMA, GRANT_LIST, LocalAdapter, LocalTransport, identity
from linkedin_seite.domain import parse_request

DAY = 86400
ACCESS_CAP = ACCESS_DAYS * DAY
REFRESH_CAP = REFRESH_DAYS * DAY
TOLERANCE = DAY  # documented bound: reject beyond cap + one day
NOW = datetime(2026, 11, 2, 12, 0, tzinfo=timezone.utc)


def response(**extra):
    body = {s.ACCESS_TOKEN: 'synthetic-access', s.EXPIRES_IN: 3600}
    body.update(extra)
    return body


class ScopeSeparators(unittest.TestCase):
    def accepted(self, scope):
        out = token_result(response(**{s.SCOPE: scope}), NOW)
        self.assertEqual(out[s.ACCESS_TOKEN], 'synthetic-access')

    def rejected(self, scope):
        with self.assertRaises(ReportError) as cm:
            token_result(response(**{s.SCOPE: scope}), NOW)
        self.assertEqual((cm.exception.status, cm.exception.code), (502, 'unexpected_source_shape'))

    def test_comma_separated_as_sent_by_linkedin(self):
        self.accepted(GRANT_COMMA)
        self.accepted(','.join(GRANT_LIST))

    def test_comma_with_spaces_and_mixed_separators(self):
        for scope in (', '.join(GRANT_LIST), ' ,'.join(GRANT_LIST), ' ' + ' , '.join(GRANT_LIST) + ' ',
                      ',' + ',,'.join(GRANT_LIST) + ',', ' '.join(GRANT_LIST), '\t'.join(GRANT_LIST),
                      ',\n'.join(GRANT_LIST)):
            with self.subTest(scope=scope):
                self.accepted(scope)

    def test_exact_set_missing_extra_or_unrelated_still_rejected(self):
        read, write = GRANT_LIST[:2], GRANT_LIST[2:]
        for scope in (read[0], read[1], write[0], write[1], '', ',', ' , ', ','.join(read), ','.join(write),
                      ','.join(GRANT_LIST[:3]), ','.join(GRANT_LIST[1:]),
                      GRANT_COMMA + ',w_member_social', GRANT_COMMA + ',unexpected',
                      ' '.join(GRANT_LIST) + ',w_member_social', GRANT_COMMA + 'x',
                      ''.join(GRANT_LIST), GRANT_COMMA.upper()):
            with self.subTest(scope=scope):
                self.rejected(scope)

    def test_only_comma_and_whitespace_separate(self):
        for separator in (';', '|', '+', '%20'):
            with self.subTest(separator=separator):
                self.rejected(separator.join(GRANT_LIST))

    def test_non_string_fail_closed(self):
        for scope in (None, 5, True, list(GRANT_LIST), GRANT_COMMA.encode(), {GRANT_LIST[0]: 1}):
            with self.subTest(scope=scope):
                self.rejected(scope)

    def test_absent_scope_unchanged_on_the_refresh_path(self):
        # A refresh cannot widen a grant; demanding the field would turn an undocumented omission
        # into an outage on day 55. The set is still exact whenever LinkedIn sends one.
        self.assertIn(s.ACCESS_TOKEN, token_result(response(), NOW))

    def test_consent_answer_must_name_the_scopes_it_grants(self):
        # The consent writes this set into the vault JSON: it has to be answered, not assumed.
        with self.assertRaises(ReportError) as cm:
            token_result(response(), NOW, scope_required=True)
        self.assertEqual(cm.exception.code, 'unexpected_source_shape')
        self.assertIn(s.ACCESS_TOKEN, token_result(response(**{s.SCOPE: GRANT_COMMA}), NOW, scope_required=True))


class LifetimeTolerance(unittest.TestCase):
    def refresh(self, seconds, original=None):
        return token_result(response(**{s.REFRESH_TOKEN: 'synthetic-refresh', s.REFRESH_EXPIRES_IN: seconds}),
                            NOW, original)

    def test_observed_linkedin_overshoot_is_capped_at_exactly_365_days(self):
        out = self.refresh(31536059)  # 59 s over REFRESH_DAYS * 86400, seen live 02.11.2026
        self.assertEqual(out['refresh_expires_at'], (NOW + timedelta(days=365)).isoformat())

    def test_exact_cap_and_shorter_unchanged(self):
        self.assertEqual(self.refresh(REFRESH_CAP)['refresh_expires_at'], (NOW + timedelta(days=365)).isoformat())
        self.assertEqual(self.refresh(REFRESH_CAP - 1)['refresh_expires_at'],
                         (NOW + timedelta(seconds=REFRESH_CAP - 1)).isoformat())
        self.assertEqual(self.refresh(1)['refresh_expires_at'], (NOW + timedelta(seconds=1)).isoformat())

    def test_refresh_boundary_cap_plus_one_day_capped_beyond_rejected(self):
        self.assertEqual(self.refresh(REFRESH_CAP + TOLERANCE)['refresh_expires_at'],
                         (NOW + timedelta(days=365)).isoformat())
        for seconds in (REFRESH_CAP + TOLERANCE + 1, 2 * REFRESH_CAP, 10**12):
            with self.subTest(seconds=seconds), self.assertRaises(ReportError) as cm:
                self.refresh(seconds)
            self.assertEqual((cm.exception.status, cm.exception.code), (502, 'unexpected_source_shape'))

    def test_refresh_invalid_types_and_values_rejected(self):
        for seconds in (0, -1, -REFRESH_CAP, True, False, '31536000', 1.5, None, [31536000], b'1'):
            with self.subTest(seconds=seconds), self.assertRaises(ReportError):
                self.refresh(seconds)
        with self.assertRaises(ReportError):  # refresh token without its lifetime
            token_result(response(**{s.REFRESH_TOKEN: 'synthetic-refresh'}), NOW)

    def test_original_consent_is_never_extended_even_with_overshoot(self):
        original = NOW + timedelta(days=10)
        self.assertEqual(self.refresh(31536059, original)['refresh_expires_at'], original.isoformat())
        self.assertEqual(self.refresh(REFRESH_CAP, original)['refresh_expires_at'], original.isoformat())
        longer = NOW + timedelta(days=400)  # cannot come from a valid identity, still never extends
        self.assertEqual(self.refresh(31536059, longer)['refresh_expires_at'], (NOW + timedelta(days=365)).isoformat())

    def test_access_overshoot_is_capped_at_exactly_60_days(self):
        for seconds in (ACCESS_CAP + 1, ACCESS_CAP + 59, ACCESS_CAP + TOLERANCE):
            with self.subTest(seconds=seconds):
                out = token_result({s.ACCESS_TOKEN: 'synthetic-access', s.EXPIRES_IN: seconds}, NOW)
                self.assertEqual(out['access_expires_at'], (NOW + timedelta(days=60)).isoformat())
        out = token_result({s.ACCESS_TOKEN: 'synthetic-access', s.EXPIRES_IN: ACCESS_CAP}, NOW)
        self.assertEqual(out['access_expires_at'], (NOW + timedelta(days=60)).isoformat())
        out = token_result({s.ACCESS_TOKEN: 'synthetic-access', s.EXPIRES_IN: 3600}, NOW)
        self.assertEqual(out['access_expires_at'], (NOW + timedelta(hours=1)).isoformat())

    def test_access_beyond_tolerance_and_invalid_rejected(self):
        for seconds in (ACCESS_CAP + TOLERANCE + 1, 365 * DAY, 0, -1, True, '3600', 1.5, None):
            with self.subTest(seconds=seconds), self.assertRaises(ReportError) as cm:
                token_result({s.ACCESS_TOKEN: 'synthetic-access', s.EXPIRES_IN: seconds}, NOW)
            self.assertEqual(cm.exception.code, 'unexpected_source_shape')


class RealLinkedInShapeThroughRefreshPath(unittest.TestCase):
    """The shape of the 02.11. consent: comma scope, refresh and access overshoot."""

    def setUp(self):
        self.fake = Fake()
        self.addCleanup(self.fake.close)
        self.attempts = 0
        self.fake.token_body = {s.ACCESS_TOKEN: 'synthetic-new-access', s.EXPIRES_IN: ACCESS_CAP + 59,
                                s.REFRESH_TOKEN: 'synthetic-new-refresh', s.REFRESH_EXPIRES_IN: 31536059,
                                s.SCOPE: GRANT_COMMA, s.TOKEN_TYPE: 'Bearer'}

    def permit(self):
        self.attempts += 1

    def stored(self, refresh_in):
        now = datetime.now(timezone.utc)
        data = identity(self.fake.port, days=-1, refresh=True)
        data['refresh_expires_at'] = (now + refresh_in).isoformat()
        return data

    def test_tokens_access_refreshes_with_comma_scope_and_overshoot(self):
        data = self.stored(timedelta(days=200))
        original_refresh_expiry = data['refresh_expires_at']
        fixed = datetime.now(timezone.utc)
        tokens = Tokens(data, now=lambda: fixed)
        self.assertEqual(tokens.access(LocalTransport(self.permit, self.fake.port)), 'synthetic-new-access')
        self.assertEqual(tokens.data['access_expires_at'], (fixed + timedelta(days=60)).isoformat())
        self.assertEqual(tokens.data[s.REFRESH_TOKEN], 'synthetic-new-refresh')
        self.assertEqual(tokens.data['refresh_expires_at'], original_refresh_expiry)  # never extended
        self.assertEqual(self.attempts, 1)
        self.assertEqual(len([c for c in self.fake.calls if c[0] == 'POST']), 1)

    def test_full_adapter_report_after_refresh_with_real_shape(self):
        data = self.stored(timedelta(days=200))
        adapter = LocalAdapter(CFG, data, self.permit)
        out = adapter.run(parse_request('page_statistics', []))
        self.assertEqual(out['summen'], {'impressionen': 70, 'klicks': 14, 'seitenaufrufe': 42})
        self.assertEqual(adapter.tokens.data[s.ACCESS_TOKEN], 'synthetic-new-access')

    def test_wrong_scope_on_the_refresh_path_still_fails_closed_with_one_call(self):
        self.fake.token_body[s.SCOPE] = GRANT_COMMA + ',w_member_social'
        tokens = Tokens(self.stored(timedelta(days=200)))
        with self.assertRaises(ReportError) as cm:
            tokens.access(LocalTransport(self.permit, self.fake.port))
        self.assertEqual(cm.exception.code, 'unexpected_source_shape')
        self.assertEqual(len(self.fake.calls), 1)

    def test_overshoot_beyond_tolerance_on_the_refresh_path_fails_closed(self):
        self.fake.token_body[s.REFRESH_EXPIRES_IN] = REFRESH_CAP + TOLERANCE + 1
        tokens = Tokens(self.stored(timedelta(days=200)))
        with self.assertRaises(ReportError) as cm:
            tokens.access(LocalTransport(self.permit, self.fake.port))
        self.assertEqual(cm.exception.code, 'unexpected_source_shape')
        self.assertEqual(len(self.fake.calls), 1)


class StoredCredential(unittest.TestCase):
    """The vault JSON of the one grant (#1237, 0.3.1): its `scope` field is the proof, read at start."""

    def shape(self, now, *, refresh_days=365, access_days=60):
        out = {s.CLIENT_ID: 'synthetic-client', s.CLIENT_SECRET: 'synthetic-secret', s.SCOPE: GRANT,
               s.ACCESS_TOKEN: 'synthetic-access', 'access_expires_at': (now + timedelta(days=access_days)).isoformat()}
        if refresh_days is not None:
            out.update({s.REFRESH_TOKEN: 'synthetic-refresh',
                        'refresh_expires_at': (now + timedelta(days=refresh_days)).isoformat()})
        return out

    def test_capped_refresh_and_full_access_window_validate_unchanged(self):
        now = datetime.now(timezone.utc)
        for kwargs in ({}, {'refresh_days': None}, {'refresh_days': 364, 'access_days': 59}):
            data = self.shape(now, **kwargs)
            with self.subTest(kwargs=kwargs):
                self.assertEqual(validate_identity(dict(data), now), data)
                # read later (a restart days after the consent) it is still within the windows
                self.assertEqual(validate_identity(dict(data), now + timedelta(days=3)), data)

    def test_separator_and_order_tolerant_like_the_token_answer(self):
        now = datetime.now(timezone.utc)
        for scope in (GRANT, GRANT_COMMA, ', '.join(reversed(GRANT_LIST))):
            with self.subTest(scope=scope):
                validate_identity(dict(self.shape(now), **{s.SCOPE: scope}), now)

    def test_a_grant_from_before_0_3_1_without_scope_fails_closed(self):
        now = datetime.now(timezone.utc)
        data = self.shape(now)
        del data[s.SCOPE]
        with self.assertRaises(ValueError):
            validate_identity(data, now)

    def test_any_other_scope_set_fails_closed(self):
        now = datetime.now(timezone.utc)
        read, write = GRANT_LIST[:2], GRANT_LIST[2:]
        for scope in (' '.join(read), ' '.join(write), ' '.join(GRANT_LIST[:3]), GRANT + ' w_member_social', GRANT.upper(),
                      GRANT.replace(' ', ';'), '', ',', None, 5, True, list(GRANT_LIST)):
            with self.subTest(scope=scope), self.assertRaises(ValueError):
                validate_identity(dict(self.shape(now), **{s.SCOPE: scope}), now)

    def test_extra_keys_stay_rejected(self):
        now = datetime.now(timezone.utc)
        with self.assertRaises(ValueError):
            validate_identity(dict(self.shape(now), token_uri='http://example.test'), now)


if __name__ == '__main__':
    unittest.main()
