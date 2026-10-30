"""Narrow SEC contract: real HTTP and loopback probes, no live credentials."""
import json
import socket
import threading
import unittest
from urllib.parse import parse_qs, urlencode, urlsplit
import test_consent as helper
from support import Fake, LocalAdapter, LocalTransport, CFG, ORG, identity
from linkedin_seite import source_contract as s
from linkedin_seite.domain import parse_request, ReportError
from linkedin_seite.linkedin_adapter import project_shares, path


class StrictSource(unittest.TestCase):
    def setUp(self):
        self.fake = Fake()
        self.addCleanup(self.fake.close)
        self.adapter = LocalAdapter(CFG, identity(self.fake.port), lambda: None)

    def posts(self):
        return self.adapter.run(parse_request('posts', []))

    def test_zero_only_valid_empty_single_post_no_paging_foreign_org_or_id(self):
        valid = self.fake.default('/rest/'+s.SHARES, {s.SHARE_IDS: ['List(urn:li:share:789)']})
        row = valid[s.ELEMENTS][0]
        bad_shapes = [
            {s.ELEMENTS: [], s.PAGING: None},
            {s.ELEMENTS: [], s.PAGING: {s.LINKS: [{s.REL: s.NEXT}]}},
            {s.ELEMENTS: [], s.NEXT: 'CANARY'},
            {s.ELEMENTS: [], s.ENTITY: 'urn:li:organization:999'},
            {s.ELEMENTS: [], s.SHARE_ID: 'urn:li:share:999'},
            {s.ELEMENTS: [{**row, s.SHARE_ID: 'urn:li:share:999'}]},
            {s.ELEMENTS: [{**row, s.UGC_ID: 'urn:li:ugcPost:999'}]},
            {s.ELEMENTS: [{k: v for k, v in row.items() if k != s.SHARE_ID}]},
            {s.ELEMENTS: [row, row]},
            {s.ELEMENTS: [{**row, s.ENTITY: 'urn:li:organization:999'}]},
        ]
        for bad in bad_shapes:
            self.fake.body = lambda endpoint, params, data: bad if endpoint == '/rest/'+s.SHARES else data
            with self.assertRaises(ReportError) as cm:
                self.posts()
            self.assertEqual((cm.exception.status, cm.exception.code), (502, 'unexpected_source_shape'))
        self.fake.body = lambda endpoint, params, data: {s.ELEMENTS: []} if endpoint == '/rest/'+s.SHARES else data
        self.assertIs(self.posts()['beitraege'][0]['ohne_interaktion'], True)
        with self.assertRaises(ReportError):
            self.adapter.run(parse_request('page_statistics', []))

    def test_only_like_count_may_be_negative(self):
        for key in (s.IMPRESSION, s.CLICK, s.COMMENT, s.SHARE):
            def response(endpoint, params, data):
                if endpoint == '/rest/'+s.SHARES:
                    data[s.ELEMENTS][0][s.SHARE_STATS][key] = -1
                return data
            self.fake.body = response
            with self.assertRaises(ReportError) as cm:
                self.posts()
            self.assertEqual((cm.exception.status, cm.exception.code), (502, 'unexpected_source_shape'))

    def test_like_period_sum_before_clamp_real_transport(self):
        # No new public period-post route. Exercise the shared production projection
        # with daily rows obtained via the real fixed GET transport, without a mock.
        req = parse_request('page_statistics', [('period_days', '2')])
        for daily, expected, corrected in (([-3, 5], 2, False), ([-5, 3], 0, True), ([-3, 3], 0, False)):
            def response(endpoint, params, data):
                data = json.loads(json.dumps(data))  # independent daily source objects
                for row, value in zip(data[s.ELEMENTS], daily):
                    row[s.SHARE_STATS][s.REACTION] = value
                return data
            self.fake.body = response
            raw = self.adapter.get(s.SHARES, {s.Q: s.ENTITY, s.ENTITY: ORG,
                s.INTERVALS: s.INTERVAL_FORMAT.format(start=req.start, end=req.end)})
            result = project_shares([path(row, s.SHARE_STATS) for row in raw[s.ELEMENTS]], post=True)
            self.assertEqual(result['reaktionen'], expected)
            self.assertEqual(result.get('korrigiert', False), corrected)
            self.assertEqual(self.fake.calls[-1][0], 'GET')

    def test_person_reference_anywhere_suppresses_text_only(self):
        for urn in ('urn:li:person:987654', 'urn:li:member:987654'):
            for extra in ({'mentions': [{'entity': urn}]}, {'unknownFuture': {'nested': [urn]}}, {urn: 'PRIVATE-NAME'}):
                def response(endpoint, params, data):
                    if endpoint == '/rest/'+s.POSTS:
                        data[s.ELEMENTS][0].update({s.TEXT: 'PRIVATE-NAME', **extra})
                    return data
                self.fake.body = response
                result = self.posts()
                for private in ('PRIVATE-NAME', urn):
                    self.assertNotIn(private, json.dumps(result))
                post = result['beitraege'][0]
                self.assertIsNone(post['text'])
                self.assertEqual(post['text_ausgelassen'], 'erwaehnung')
                self.assertEqual([post[k] for k in ('impressionen', 'klicks', 'reaktionen', 'kommentare', 'shares')], [10, 2, 3, 4, 5])


@unittest.skipUnless(helper.SCRIPT.exists(), 'PO tool not shipped in image')
class FirstRequest(unittest.TestCase):
    def test_invalid_first_request_on_either_socket_closes_both(self):
        fake = Fake()
        self.addCleanup(fake.close)
        for first_host in ('127.0.0.1', '::1'):
            for kind in ('state', 'path'):
                threads, replies = [], []
                def opener(url):
                    state = parse_qs(urlsplit(url).query)[s.STATE][0]
                    def send():
                        target = ('/wrong' if kind == 'path' else '/linkedin/callback')+'?'+urlencode({s.CODE: 'synthetic', s.STATE: 'wrong' if kind == 'state' else state})
                        with socket.create_connection((first_host, 8765), timeout=1) as sock:
                            sock.sendall(('GET '+target+' HTTP/1.1\r\nHost: localhost\r\n\r\n').encode())
                            replies.append(sock.recv(8192))
                        other = '::1' if first_host == '127.0.0.1' else '127.0.0.1'
                        try:
                            with socket.create_connection((other, 8765), timeout=1) as sock:
                                target = '/linkedin/callback?'+urlencode({s.CODE: 'synthetic', s.STATE: state})
                                sock.sendall(('GET '+target+' HTTP/1.1\r\nHost: localhost\r\n\r\n').encode())
                                replies.append(sock.recv(8192))
                        except OSError:
                            pass
                    thread = threading.Thread(target=send)
                    threads.append(thread)
                    thread.start()
                    return True
                try:
                    with self.assertRaises(ValueError):
                        helper.consent.consent('synthetic', 'synthetic', opener=opener,
                            transport=LocalTransport(lambda: None, fake.port), timeout=1)
                finally:
                    for thread in threads:
                        thread.join(2)
                self.assertTrue(replies)
                self.assertFalse(any(b'200' in reply for reply in replies))
                self.assertEqual(fake.calls, [])
                for host in ('127.0.0.1', '::1'):
                    with self.assertRaises(OSError):
                        socket.create_connection((host, 8765), timeout=.1)
