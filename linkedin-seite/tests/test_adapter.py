from dataclasses import replace
from datetime import datetime, timedelta, timezone
import json
import unittest
from urllib.parse import parse_qs, quote
from linkedin_seite import source_contract as s
from linkedin_seite.domain import parse_request, ReportError
from linkedin_seite.linkedin_adapter import minimize
from linkedin_seite.oauth import token_result, warning
from support import Fake, LocalAdapter, CFG, ORG, identity


class Adapter(unittest.TestCase):
    def setUp(self):
        self.fake = Fake()
        self.addCleanup(self.fake.close)
        self.data = identity(self.fake.port)
        self.attempts = 0
        self.adapter = LocalAdapter(CFG, self.data, self.permit)

    def permit(self):
        self.attempts += 1

    def run_report(self, report='page_statistics'):
        return self.adapter.run(parse_request(report, []))

    def test_fixed_get_routes_org_headers_and_sums(self):
        self.assertEqual(self.run_report()['summen'], {'impressionen': 70, 'klicks': 14, 'seitenaufrufe': 42})
        self.assertEqual(self.run_report('followers')['summen'], {'gesamt': 10, 'zuwachs': 70})
        posts = self.run_report('posts')['beitraege']
        self.assertEqual(len(posts), 1)
        self.assertEqual(len(posts[0]['textanfang']), 120)
        self.assertEqual(posts[0]['reaktionen'], 3)
        self.assertEqual(self.attempts, 6)
        for method, endpoint, params, headers, body in self.fake.calls:
            self.assertEqual(method, 'GET')
            self.assertEqual(body, b'')
            lower = {k.lower(): v for k, v in headers.items()}
            self.assertEqual(lower[s.VERSION_HEADER.lower()], '202610')
            self.assertEqual(lower[s.PROTOCOL_HEADER.lower()], '2.0.0')
            self.assertNotIn('x-restli-method', lower)
            self.assertEqual(headers['Authorization'], 'Bearer ' + self.data[s.ACCESS_TOKEN])
            if endpoint.startswith('/rest/' + s.NETWORK + '/'):
                self.assertEqual(endpoint, '/rest/' + s.NETWORK + '/' + quote(ORG, safe=''))
                self.assertEqual(params, {s.EDGE_TYPE: [s.FOLLOWED_BY_MEMBER]})
            else:
                self.assertIn(ORG, str(params))
        self.assertEqual(self.fake.calls[-1][2][s.SHARE_IDS], ['List(urn:li:share:789)'])

    def test_no_person_fields_and_breakdowns_before_projection(self):
        raw = self.fake.default('/rest/' + s.FOLLOWERS, {})
        minimized = minimize(raw, (s.ENTITY, s.FOLLOWER_GAIN))
        self.assertNotIn('PRIVATE-CANARY', json.dumps(minimized))
        for report in ('page_statistics', 'followers', 'posts'):
            self.assertNotIn('PRIVATE-CANARY', json.dumps(self.run_report(report)))
        allowed = ['/rest/'+v for v in (s.PAGE, s.FOLLOWERS, s.SHARES, s.POSTS)]
        allowed.append('/rest/'+s.NETWORK+'/'+quote(ORG, safe=''))
        self.assertTrue(all(c[1] in allowed for c in self.fake.calls))

    def test_nested_breakdowns_discarded_by_real_adapter_before_sums(self):
        def extra(endpoint, params, data):
            data[s.ELEMENTS][0][s.FOLLOWER_GAIN]['futureDemographicBreakdown'] = 'PRIVATE-CANARY'
            return data
        self.fake.body = extra
        rows = self.adapter.stats(s.FOLLOWERS, s.FOLLOWER_GAIN, parse_request('followers', []))
        self.assertEqual(rows, [{s.ORGANIC_GAIN: 8, s.PAID_GAIN: 2}]*7)
        self.assertNotIn('PRIVATE-CANARY', json.dumps(rows))

    def test_missing_invalid_metrics_no_zero_or_replacement(self):
        for bad in ([], 'bad', 1, {}, {s.ELEMENTS: []}, {s.ELEMENTS: 'bad'},
                    {s.ELEMENTS: [{s.ENTITY: ORG, 'followerCountsByCountry': [{'count': 999}]}]}):
            self.fake.body = bad
            with self.assertRaises(ReportError) as cm:
                self.run_report('followers')
            self.assertEqual(cm.exception.code, 'unexpected_source_shape')
        for value in (None, True, '10', 1.5):
            self.fake.body = lambda endpoint, params, data: (
                {s.NETWORK_SIZE: value}
                if s.INTERVALS not in params else data)
            with self.assertRaises(ReportError):
                self.run_report('followers')

    def test_period_gaps_duplicates_foreign_org_and_next_page_rejected(self):
        def change(kind):
            def mutate(endpoint, params, data):
                if kind == 'gap':
                    data[s.ELEMENTS].pop()
                elif kind == 'duplicate':
                    data[s.ELEMENTS].append(data[s.ELEMENTS][0])
                elif kind == 'org':
                    data[s.ELEMENTS][0][s.ORGANIZATION] = 'urn:li:organization:999'
                else:
                    data[s.PAGING] = {s.LINKS: [{s.REL: s.NEXT, 'href': 'http://example.test/leak'}]}
                return data
            return mutate
        for kind in ('gap', 'duplicate', 'org', 'next'):
            self.fake.body = change(kind)
            with self.subTest(kind=kind), self.assertRaises(ReportError) as cm:
                self.run_report()
            self.assertEqual(cm.exception.code, 'unexpected_source_shape')

    def test_posts_foreign_author_bad_id_unpublished_overlimit(self):
        original = self.fake.default('/rest/posts', {})
        for field, value in ((s.AUTHOR, 'urn:li:person:999'), (s.ID, '../../comments'),
                             (s.TEXT, None), (s.LIFECYCLE, 'DRAFT'), (s.CREATED, True)):
            data = json.loads(json.dumps(original))
            data[s.ELEMENTS][0][field] = value
            self.fake.body = data
            before = len(self.fake.calls)
            with self.subTest(field=field), self.assertRaises(ReportError):
                self.run_report('posts')
            self.assertEqual(len(self.fake.calls), before + 1)
        self.fake.body = {s.ELEMENTS: original[s.ELEMENTS]*11}
        with self.assertRaises(ReportError):
            self.run_report('posts')

    def test_unknown_operation_and_endpoint_rejected_before_http(self):
        with self.assertRaises(ReportError):
            self.adapter.run(replace(parse_request('posts', []), report='create_post'))
        with self.assertRaises(ReportError):
            self.adapter.get('comments', {})
        self.assertEqual(self.fake.calls, [])

    def test_errors_redirects_no_retry_no_raw_error(self):
        for status in (301, 302, 307, 308, 400, 401, 403, 429, 500, 504):
            self.fake.status = status
            self.fake.body = {'error': self.data[s.ACCESS_TOKEN]}
            self.fake.calls.clear()
            with self.assertRaises(ReportError) as cm:
                self.run_report()
            self.assertNotIn(self.data[s.ACCESS_TOKEN], str(cm.exception))
            self.assertEqual(len(self.fake.calls), 1)

    def test_oversized_and_malformed_source(self):
        for body in (b'X' * (512*1024+1), b'{', b'{"elements":[],"elements":[]}', b'{"elements":NaN}'):
            self.fake.body = body
            with self.assertRaises(ReportError) as cm:
                self.run_report()
            self.assertEqual(cm.exception.code, 'response_too_large' if len(body) > 512*1024 else 'unexpected_source_shape')

    def test_post_statistics_id_and_created_order_fail_closed(self):
        def wrong_id(endpoint, params, data):
            if endpoint == '/rest/' + s.SHARES:
                data[s.ELEMENTS][0][s.SHARE_ID] = 'urn:li:share:999'
            return data
        self.fake.body = wrong_id
        with self.assertRaises(ReportError):
            self.run_report('posts')
        rows = self.fake.default('/rest/posts', {})[s.ELEMENTS]
        rows.append({**rows[0], s.ID: 'urn:li:share:999', s.CREATED: rows[0][s.CREATED]+1000})
        self.fake.body = lambda endpoint, params, data: {s.ELEMENTS: rows} if endpoint == '/rest/posts' else data
        with self.assertRaises(ReportError):
            self.run_report('posts')

    def test_expired_without_refresh_never_calls_http(self):
        self.adapter.tokens.data['access_expires_at'] = (datetime.now(timezone.utc)-timedelta(seconds=1)).isoformat()
        with self.assertRaises(ReportError) as cm:
            self.run_report()
        self.assertEqual(cm.exception.code, 'token_abgelaufen')
        self.assertEqual(self.fake.calls, [])

    def test_refresh_ram_only_no_extension_and_single_exchange(self):
        self.data = identity(self.fake.port, days=-1, refresh=True)
        self.data['refresh_expires_at'] = (datetime.now(timezone.utc)+timedelta(days=10)).isoformat()
        original = dict(self.data)
        self.fake.token_body.update({s.REFRESH_TOKEN: 'synthetic-rotated', s.REFRESH_EXPIRES_IN: 365*86400})
        self.adapter = LocalAdapter(CFG, self.data, self.permit)
        self.run_report()
        self.run_report()
        self.assertEqual(self.data, original)
        self.assertEqual(self.adapter.tokens.data['refresh_expires_at'], original['refresh_expires_at'])
        tokens = [c for c in self.fake.calls if c[0] == 'POST']
        self.assertEqual(len(tokens), 1)
        self.assertEqual(tokens[0][1], '/oauth/v2/accessToken')
        self.assertEqual(parse_qs(tokens[0][4].decode())[s.GRANT_TYPE], [s.REFRESH_TOKEN])

    def test_refresh_failure_no_retry_and_expired_refresh_no_exchange(self):
        self.adapter = LocalAdapter(CFG, identity(self.fake.port, days=-1, refresh=True), self.permit)
        self.fake.status = 500
        with self.assertRaises(ReportError):
            self.run_report()
        self.assertEqual(len(self.fake.calls), 1)
        self.adapter.tokens.data['refresh_expires_at'] = (datetime.now(timezone.utc)-timedelta(days=1)).isoformat()
        with self.assertRaises(ReportError) as cm:
            self.run_report()
        self.assertEqual(cm.exception.code, 'token_abgelaufen')
        self.assertEqual(len(self.fake.calls), 1)

    def test_scopes_pinned_and_expanded_grant_rejected_on_wire(self):
        self.assertEqual(set(s.SCOPES), {'rw_organization_admin', 'r_organization_social'})
        self.adapter = LocalAdapter(CFG, identity(self.fake.port, days=-1, refresh=True), self.permit)
        self.fake.token_body[s.SCOPE] = ' '.join(s.SCOPES) + ' unexpected'
        with self.assertRaises(ReportError) as cm:
            self.run_report()
        self.assertEqual(cm.exception.code, 'unexpected_source_shape')
        self.assertEqual(len(self.fake.calls), 1)

    def test_token_response_bounds_and_warning_dates(self):
        now = datetime.now(timezone.utc)
        for seconds in (0, -1, True, '123', 60*86400+1):
            with self.assertRaises(ReportError):
                token_result({s.ACCESS_TOKEN: 'synthetic', s.EXPIRES_IN: seconds}, now)
        data = identity(days=14)
        expiry = datetime.fromisoformat(data['access_expires_at'])
        self.assertEqual(warning(data, expiry-timedelta(days=14, seconds=1)), {})
        self.assertEqual(warning(data, expiry-timedelta(days=14)),
                         {'warnung': 'token_laeuft_ab', 'ablaufdatum': expiry.date().isoformat()})
