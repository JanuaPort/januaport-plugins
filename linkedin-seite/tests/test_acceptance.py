"""October 2026 manufacturer corrections, exercised over real local HTTP."""
import contextlib
import errno
from datetime import datetime, timezone
import io
import json
from pathlib import Path
import socket
import threading
import unittest
from urllib.parse import parse_qs, quote, urlencode, urlsplit
from unittest.mock import patch
import test_consent as helper
from support import Fake, LocalAdapter, LocalTransport, CFG, ORG, identity
from linkedin_seite.domain import parse_request, ReportError
from linkedin_seite import source_contract as s


class Manufacturer(unittest.TestCase):
    def setUp(self):
        self.fake = Fake()
        self.addCleanup(self.fake.close)
        self.adapter = LocalAdapter(CFG, identity(self.fake.port), lambda: None)

    def report(self, report='posts'):
        return self.adapter.run(parse_request(report, []))

    def test_network_total_fixed_encoded_org(self):
        self.assertEqual(set(s.SCOPES), {'rw_organization_admin', 'r_organization_social'})
        def response(endpoint, params, data):
            if endpoint.startswith('/rest/networkSizes/'):
                return {'firstDegreeSize': 123, 'person': 'PRIVATE-CANARY'}
            if endpoint == '/rest/' + s.FOLLOWERS and s.INTERVALS not in params:
                return {s.ELEMENTS: []}
            return data
        self.fake.body = response
        self.assertEqual(self.report('followers')['summen'], {'gesamt': 123, 'zuwachs': 70})
        call = self.fake.calls[0]
        self.assertEqual(call[:3], ('GET', '/rest/networkSizes/' + quote(ORG, safe=''),
                                    {'edgeType': ['COMPANY_FOLLOWED_BY_MEMBER']}))
        self.assertEqual(len(self.fake.calls), 2)
        self.adapter.get(s.NETWORK, {s.ENTITY: 'urn:li:organization:999', s.EDGE_TYPE: 'other'})
        self.assertEqual(self.fake.calls[-1][:3], call[:3])
        for endpoint in ('networkSizes/urn:li:organization:999', '../networkSizes/999'):
            with self.assertRaises(ReportError):
                self.adapter.get(endpoint, {})
        self.assertEqual(len(self.fake.calls), 3)

    def test_followers_two_day_lag_no_clamping(self):
        now = datetime(2026, 10, 29, 0, 1, tzinfo=timezone.utc)
        req = parse_request('followers', [], now)
        self.assertEqual(datetime.fromtimestamp(req.end/1000, timezone.utc).date().isoformat(), '2026-10-28')
        for end in ('2026-10-28', '2026-10-29'):
            with self.assertRaises(ReportError) as cm:
                parse_request('followers', [('end_date', end)], now)
            self.assertEqual(cm.exception.code, 'end_date_not_available')
        self.assertEqual(parse_request('followers', [('end_date', '2026-10-27')], now), req)

    def test_missing_post_statistics_only_documented_empty_shape(self):
        self.fake.body = lambda endpoint, params, data: {s.ELEMENTS: []} if endpoint == '/rest/'+s.SHARES else data
        post = self.report()['beitraege'][0]
        self.assertIs(post['ohne_interaktion'], True)
        for key in ('impressionen', 'klicks', 'reaktionen', 'kommentare', 'shares'):
            self.assertEqual(post[key], 0)
        for bad in ({}, {s.ELEMENTS: None}, {s.ELEMENTS: [{}]},
                    {s.ELEMENTS: [], s.PAGING: {s.LINKS: [{s.REL: s.NEXT}]}}):
            self.fake.body = lambda endpoint, params, data: bad if endpoint == '/rest/'+s.SHARES else data
            with self.assertRaises(ReportError) as cm:
                self.report()
            self.assertEqual(cm.exception.code, 'unexpected_source_shape')

    def test_negative_like_count_clamped_and_marked(self):
        fields = ((s.REACTION, 'reaktionen'),)
        for source, output in fields:
            def response(endpoint, params, data):
                if endpoint == '/rest/'+s.SHARES:
                    data[s.ELEMENTS][0][s.SHARE_STATS][source] = -7
                return data
            self.fake.body = response
            post = self.report()['beitraege'][0]
            self.assertEqual(post[output], 0)
            self.assertIs(post['korrigiert'], True)

    def test_mentions_text_and_attributes_redacted_without_aborting(self):
        for changes in (
            {s.TEXT: 'PRIVATE-NAME urn:li:member:987654'},
            {s.TEXT: 'PRIVATE-NAME urn:li:person:987654'},
            {s.TEXT: 'Hello @[PRIVATE-NAME](urn:li:person:987654)'},
            {s.TEXT: 'PRIVATE-NAME', 'attributes': [{'value': {'com.linkedin.common.MemberAttributedEntity': {'member': 'urn:li:member:987654'}}}]},
            {s.TEXT: 'PRIVATE-NAME', 'attributes': [{'type': 'Mention', 'name': 'PRIVATE-NAME'}]},
        ):
            def response(endpoint, params, data):
                if endpoint == '/rest/'+s.POSTS:
                    data[s.ELEMENTS][0].update(changes)
                return data
            self.fake.body = response
            logs = io.StringIO()
            with contextlib.redirect_stdout(logs), contextlib.redirect_stderr(logs):
                result = self.report()
            serialized = json.dumps(result) + logs.getvalue()
            for private in ('PRIVATE-NAME', 'urn:li:member:', 'urn:li:person:'):
                self.assertNotIn(private, serialized)
            post = result['beitraege'][0]
            self.assertIsNone(post['text'])
            self.assertEqual(post['text_ausgelassen'], 'erwaehnung')
            self.assertNotIn('textanfang', post)
            self.assertEqual(post['reaktionen'], 3)

    def test_negative_aggregate_counters_and_invalid_types_rejected(self):
        for endpoint, keys, report in (
            (s.NETWORK, (s.NETWORK_SIZE,), 'followers'),
            (s.FOLLOWERS, (s.FOLLOWER_GAIN, s.ORGANIC_GAIN), 'followers'),
            (s.FOLLOWERS, (s.FOLLOWER_GAIN, s.PAID_GAIN), 'followers'),
            (s.PAGE, (s.PAGE_STATS, s.VIEWS, s.ALL_VIEWS, s.PAGE_VIEWS), 'page_statistics'),
            (s.SHARES, (s.SHARE_STATS, s.IMPRESSION), 'page_statistics'),
            (s.SHARES, (s.SHARE_STATS, s.CLICK), 'page_statistics'),
        ):
            for value in (-5, None, True, '1', 1.5):
                def response(url, params, data):
                    if url == '/rest/'+endpoint or url.startswith('/rest/'+endpoint+'/'):
                        rows = [data] if endpoint == s.NETWORK else data[s.ELEMENTS]
                        for row in rows:
                            target = row
                            for key in keys[:-1]:
                                target = target[key]
                            target[keys[-1]] = value
                    return data
                self.fake.body = response
                with self.assertRaises(ReportError) as cm:
                    self.report(report)
                self.assertEqual((cm.exception.status, cm.exception.code), (502, 'unexpected_source_shape'))

    def test_unknown_attributes_and_negative_dates_are_not_zero(self):
        for field, value in ((s.ATTRIBUTES, {}), (s.ATTRIBUTES, [None]), (s.CREATED, -1)):
            def response(endpoint, params, data):
                if endpoint == '/rest/'+s.POSTS:
                    data[s.ELEMENTS][0][field] = value
                return data
            self.fake.body = response
            with self.assertRaises(ReportError) as cm:
                self.report()
            self.assertEqual(cm.exception.code, 'unexpected_source_shape')

    def test_normal_posts_have_no_correction_or_suppression_flags(self):
        post = self.report()['beitraege'][0]
        self.assertEqual(len(post['textanfang']), 120)
        for field in ('korrigiert', 'ohne_interaktion', 'text', 'text_ausgelassen'):
            self.assertNotIn(field, post)


@unittest.skipUnless(helper.SCRIPT.exists(), 'PO tool not shipped in service image')
class DualLoopback(unittest.TestCase):
    def test_two_valid_callbacks_only_one_exchange(self):
        fake = Fake()
        self.addCleanup(fake.close)
        sockets = []
        def opener(url):
            state = parse_qs(urlsplit(url).query)['state'][0]
            for host in ('::1', '127.0.0.1'):
                sock = socket.create_connection((host, 8765), timeout=2)
                sockets.append(sock)
                target = '/linkedin/callback?' + urlencode({'code': host, 'state': state})
                sock.sendall(('GET '+target+' HTTP/1.1\r\nHost: localhost\r\n\r\n').encode())
            return True
        try:
            helper.consent.consent('synthetic', 'synthetic', opener=opener,
                transport=LocalTransport(lambda: None, fake.port), timeout=2)
            replies = []
            for sock in sockets:
                try:
                    replies.append(sock.recv(8192))
                except ConnectionResetError:
                    replies.append(b'')
            self.assertEqual(sum(b'200' in reply for reply in replies), 1)
            self.assertEqual(len(fake.calls), 1)
        finally:
            for sock in sockets:
                sock.close()

    def test_both_loopbacks_one_request_and_close(self):
        self.exercise()

    def exercise(self, ipv6_failure=False):
        fake = Fake()
        self.addCleanup(fake.close)
        replies, failures, threads, addresses = [], [], [], []
        original = helper.consent.CallbackServer
        class InspectServer(original):
            def server_bind(self):
                if ipv6_failure and self.address_family == socket.AF_INET6:
                    raise OSError(ipv6_failure, 'synthetic IPv6 unavailable')
                super().server_bind()
                addresses.append(self.socket.getsockname()[:2])
        def opener(url):
            state = parse_qs(urlsplit(url).query)['state'][0]
            self.assertEqual(parse_qs(urlsplit(url).query)['redirect_uri'], ['http://localhost:8765/linkedin/callback'])
            def requests():
                try:
                    for host, value in [('127.0.0.1' if ipv6_failure else '::1', state)]:
                        with socket.create_connection((host, 8765), timeout=1) as sock:
                            target = '/linkedin/callback?' + urlencode({'code': 'synthetic-code', 'state': value})
                            sock.sendall(('GET '+target+' HTTP/1.1\r\nHost: localhost\r\n\r\n').encode())
                            reply = b''
                            while chunk := sock.recv(8192):
                                reply += chunk
                            replies.append(reply)
                except Exception as exc:
                    failures.append(type(exc).__name__)
            thread = threading.Thread(target=requests)
            threads.append(thread)
            thread.start()
            return True
        errors = io.StringIO()
        try:
            with patch.object(helper.consent, 'CallbackServer', InspectServer), contextlib.redirect_stderr(errors):
                result = helper.consent.consent('synthetic', 'synthetic', opener=opener,
                    transport=LocalTransport(lambda: None, fake.port), timeout=2)
        finally:
            for thread in threads:
                thread.join(3)
        self.assertFalse(failures)
        self.assertIn(s.ACCESS_TOKEN, result)
        self.assertEqual(len(fake.calls), 1)
        self.assertIn(b'200', replies[0])
        self.assertEqual(set(addresses), {('127.0.0.1', 8765)} if ipv6_failure else {('127.0.0.1', 8765), ('::1', 8765)})
        self.assertEqual(errors.getvalue(), 'IPv6 nicht verfuegbar; weiter auf 127.0.0.1.\n' if ipv6_failure else '')
        for host, port in addresses:
            with self.assertRaises(OSError):
                socket.create_connection((host, port), timeout=.1)

    def test_ipv6_failure_continues_ipv4_with_notice(self):
        for code in (errno.EAFNOSUPPORT, errno.EADDRNOTAVAIL, errno.EPROTONOSUPPORT):
            self.exercise(ipv6_failure=code)


class Documentation(unittest.TestCase):
    def test_stdout_direct_pipe_only(self):
        root = Path(__file__).resolve().parents[1]
        if not (root/'README.md').exists():
            self.skipTest('documentation not shipped in image')
        for name in ('README.md', 'SETUP.md'):
            text = (root/name).read_text(encoding='utf-8')
            self.assertIn('Die Ausgabe auf stdout nie in eine Datei umleiten, nur direkt per Pipe an `jnpt credential set`.', text)
