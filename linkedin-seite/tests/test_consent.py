import contextlib
import importlib.util
import io
from pathlib import Path
import socket
import subprocess
import sys
import threading
import time
import unittest
from urllib.parse import urlsplit, parse_qs, urlencode
from unittest.mock import patch
from linkedin_seite import source_contract as s
from support import Fake, LocalTransport

SCRIPT = Path(__file__).resolve().parents[1] / 'tools' / 'zustimmung.py'
if SCRIPT.exists():
    spec = importlib.util.spec_from_file_location('zustimmung', SCRIPT)
    consent = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(consent)


@unittest.skipUnless(SCRIPT.exists(), 'PO tool not shipped in service image')
class Consent(unittest.TestCase):
    def setUp(self):
        self.fake = Fake()
        self.addCleanup(self.fake.close)
        self.seen = []
        self.reply = []
        self.threads = []

    def tearDown(self):
        for thread in self.threads:
            thread.join(timeout=3)

    def browser(self, url, kind='ok'):
        params = parse_qs(urlsplit(url).query)
        self.seen.append(params)
        state = params[s.STATE][0] if kind != 'wrong-state' else 'CANARY'
        target = '/linkedin/callback?' + urlencode({s.CODE: 'synthetic-code', s.STATE: state})
        if kind == 'duplicate':
            target += '&code=other'
        if kind == 'wrong-path':
            target = '/else?' + target.split('?')[1]
        def request():
            with socket.create_connection(('127.0.0.1', 8765), timeout=2) as sock:
                sock.sendall(('GET '+target+' HTTP/1.1\r\nHost: localhost:8765\r\n\r\n').encode())
                out = b''
                while chunk := sock.recv(8192):
                    out += chunk
                self.reply.append(out)
        thread = threading.Thread(target=request)
        thread.start()
        self.threads.append(thread)
        return True

    def run_consent(self, **kwargs):
        return consent.consent('synthetic-client', 'synthetic-secret',
                               opener=self.browser, transport=LocalTransport(lambda: None, self.fake.port), **kwargs)

    def test_exact_scopes_state_redirect_pkce_and_one_callback(self):
        out = self.run_consent(pkce=True)
        self.assertIn(s.ACCESS_TOKEN, out)
        params = self.seen[0]
        self.assertEqual(set(params[s.SCOPE][0].split()), {'rw_organization_admin', 'r_organization_social'})
        self.assertEqual(params[s.REDIRECT], ['http://localhost:8765/linkedin/callback'])
        self.assertGreaterEqual(len(params[s.STATE][0]), 40)
        self.assertEqual(params[s.CODE_CHALLENGE_METHOD], ['S256'])
        token = parse_qs(self.fake.calls[0][4].decode())
        self.assertIn(s.CODE_VERIFIER, token)
        self.assertEqual(token[s.CODE], ['synthetic-code'])
        with self.assertRaises(OSError):
            socket.create_connection(('127.0.0.1', 8765), timeout=.2)

    def test_no_pkce_default_and_optional_refresh(self):
        self.fake.token_body.update({s.REFRESH_TOKEN: 'synthetic-refresh', s.REFRESH_EXPIRES_IN: 365*86400})
        out = self.run_consent()
        self.assertIn(s.REFRESH_TOKEN, out)
        self.assertNotIn(s.CODE_CHALLENGE, self.seen[0])
        self.assertNotIn(s.CODE_VERIFIER, parse_qs(self.fake.calls[0][4].decode()))

    def test_wrong_state_duplicate_wrong_path_stop_before_exchange(self):
        for kind in ('wrong-state', 'duplicate', 'wrong-path'):
            with self.subTest(kind=kind), self.assertRaises(ValueError):
                consent.consent('synthetic-client', 'synthetic-secret',
                                opener=lambda url: self.browser(url, kind),
                                transport=LocalTransport(lambda: None, self.fake.port), timeout=.2)
        self.assertEqual(self.fake.calls, [])
        self.assertTrue(all(b'400' in reply and b'CANARY' not in reply for reply in self.reply))

    def test_timeout_no_callback_no_exchange(self):
        started = time.monotonic()
        with self.assertRaises(ValueError):
            consent.consent('synthetic', 'synthetic', opener=lambda _: True, timeout=.05,
                            transport=LocalTransport(lambda: None, self.fake.port))
        self.assertLess(time.monotonic()-started, 1)
        self.assertEqual(self.fake.calls, [])
        self.assertEqual(consent.TIMEOUT, 300)

    def test_secret_not_an_argument_stdout_json_only_no_files(self):
        args = ['zustimmung.py', '--client-id', 'synthetic-client', '--secret-stdin']
        output, errors = io.StringIO(), io.StringIO()
        original = consent.consent
        with patch('sys.argv', args), patch('sys.stdin', io.StringIO('synthetic-secret\n')), \
             contextlib.redirect_stdout(output), contextlib.redirect_stderr(errors), \
             patch.object(consent, 'consent', lambda *a, **kw: original(*a, **kw, opener=self.browser, transport=LocalTransport(lambda: None, self.fake.port))), \
             patch('builtins.open', side_effect=AssertionError('file write/read forbidden')):
            self.assertEqual(consent.main(), 0)
        import json
        self.assertEqual(json.loads(output.getvalue())[s.CLIENT_SECRET], 'synthetic-secret')
        self.assertEqual(errors.getvalue(), 'Refresh-Token: nein\n')
        rejected = io.StringIO()
        with patch('sys.argv', args + ['--client-secret', 'CANARY']), contextlib.redirect_stderr(rejected), self.assertRaises(SystemExit):
            consent.main()
        self.assertNotIn('CANARY', rejected.getvalue())

    def test_callback_rejects_errors_empty_code_and_absolute_url(self):
        for target in ('/linkedin/callback?error=denied&state=x', '/linkedin/callback?code=&state=x',
                       'http://example.test/linkedin/callback?code=c&state=x'):
            with self.assertRaises(ValueError):
                consent.callback(target, 'x')

    def test_listener_is_loopback_and_state_changes_each_run(self):
        original = consent.CallbackServer
        addresses = []
        class InspectServer(original):
            def __init__(self, *args):
                super().__init__(*args)
                addresses.append(self.socket.getsockname()[:2])
        with patch.object(consent, 'CallbackServer', InspectServer):
            self.run_consent()
            self.run_consent()
        self.assertEqual(addresses, [('127.0.0.1', 8765), ('::1', 8765)]*2)
        self.assertNotEqual(self.seen[0][s.STATE], self.seen[1][s.STATE])

    def test_main_hard_deadline_exits_even_if_transport_hangs(self):
        code = "import sys,time; sys.path.insert(0,sys.argv[1]); import zustimmung as z; z.TIMEOUT=.1; z.consent=lambda *a: time.sleep(10); sys.argv=['z','--client-id','synthetic','--secret-stdin']; raise SystemExit(z.main())"
        result = subprocess.run([sys.executable, '-c', code, str(SCRIPT.parent)],
                                input='synthetic-secret\n', capture_output=True, text=True, timeout=3)
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stdout, '')
        self.assertEqual(result.stderr, 'zustimmung_timeout\n')

    def test_getpass_echo_fallback_is_rejected(self):
        def unsafe(*args):
            import warnings
            warnings.warn('unsafe fallback', consent.getpass.GetPassWarning)
            raise AssertionError('must stop before echo fallback')
        errors, output = io.StringIO(), io.StringIO()
        with patch('sys.argv', ['z','--client-id','synthetic']), patch.object(consent.getpass, 'getpass', unsafe), \
             contextlib.redirect_stderr(errors), contextlib.redirect_stdout(output):
            self.assertEqual(consent.main(), 1)
        self.assertEqual(output.getvalue(), '')
        self.assertEqual(errors.getvalue(), 'zustimmung_fehlgeschlagen\n')
