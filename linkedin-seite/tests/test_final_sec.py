"""Final SEC corrections: provider paging and actual IPv6 port collision."""
import contextlib
import errno
import io
import socket
import unittest
from unittest.mock import patch
import test_consent as helper
from support import Fake, LocalAdapter, LocalTransport, CFG, identity
from linkedin_seite import source_contract as s
from linkedin_seite.domain import parse_request, ReportError


class Paging(unittest.TestCase):
    def test_single_post_empty_or_missing_links_valid_next_and_bad_shapes_rejected(self):
        fake = Fake()
        self.addCleanup(fake.close)
        adapter = LocalAdapter(CFG, identity(fake.port), lambda: None)
        for empty in (False, True):
            for paging in ({}, {s.LINKS: []}):
                def response(endpoint, params, data):
                    if endpoint == '/rest/'+s.SHARES:
                        if empty:
                            data[s.ELEMENTS] = []
                        data[s.PAGING] = paging
                    return data
                fake.body = response
                post = adapter.run(parse_request('posts', []))['beitraege'][0]
                self.assertEqual(post['reaktionen'], 0 if empty else 3)
                self.assertEqual(post.get('ohne_interaktion', False), empty)
            for paging in (None, [], 'bad', {s.LINKS: None}, {s.LINKS: {}},
                           {s.LINKS: [None]}, {s.LINKS: [{s.REL: s.NEXT}]}):
                fake.body = response
                with self.assertRaises(ReportError) as cm:
                    adapter.run(parse_request('posts', []))
                self.assertEqual((cm.exception.status, cm.exception.code), (502, 'unexpected_source_shape'))


@unittest.skipUnless(helper.SCRIPT.exists(), 'PO tool not shipped in image')
class IPv6Failure(unittest.TestCase):
    def test_actual_ipv6_port_in_use_aborts_before_browser_or_token(self):
        fake = Fake()
        self.addCleanup(fake.close)
        with socket.socket(socket.AF_INET6) as occupied:
            occupied.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_V6ONLY, 1)
            if hasattr(socket, 'SO_EXCLUSIVEADDRUSE'):
                occupied.setsockopt(socket.SOL_SOCKET, socket.SO_EXCLUSIVEADDRUSE, 1)
            else:
                occupied.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            occupied.bind(('::1', 8765))
            occupied.listen(1)
            opened, errors = [], io.StringIO()
            with contextlib.redirect_stderr(errors), self.assertRaises(ValueError):
                helper.consent.consent('synthetic', 'synthetic', opener=lambda url: opened.append(url) or True,
                    transport=LocalTransport(lambda: None, fake.port), timeout=.05)
            self.assertEqual(opened, [])
            self.assertEqual(fake.calls, [])
            self.assertIn('IPv6-Port [::1]:8765 ist belegt; Zustimmung abgebrochen.', errors.getvalue())
            self.assertNotIn('weiter auf', errors.getvalue())
            with self.assertRaises(OSError):
                socket.create_connection(('127.0.0.1', 8765), timeout=.1)

    def test_unknown_ipv6_error_does_not_fall_back(self):
        original = helper.consent.CallbackServer
        class Denied(original):
            def server_bind(self):
                if self.address_family == socket.AF_INET6:
                    raise OSError(errno.EACCES, 'PRIVATE-CANARY')
                super().server_bind()
        errors, opened = io.StringIO(), []
        with patch.object(helper.consent, 'CallbackServer', Denied), contextlib.redirect_stderr(errors), self.assertRaises(ValueError):
            helper.consent.consent('synthetic', 'synthetic', opener=lambda url: opened.append(url) or True, timeout=.05)
        self.assertEqual(opened, [])
        self.assertNotIn('PRIVATE-CANARY', errors.getvalue())
        self.assertNotIn('weiter auf', errors.getvalue())
