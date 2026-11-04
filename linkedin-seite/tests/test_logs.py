import json
from pathlib import Path
import secrets
import subprocess
import sys
import unittest
import urllib.request
import urllib.error
from support import Fake, identity
from linkedin_seite import source_contract as s


class Logs(unittest.TestCase):
    def test_real_service_mentions_never_in_response_or_log(self):
        fake = Fake()
        self.addCleanup(fake.close)
        data, bridge = identity(fake.port), secrets.token_urlsafe(40)
        proc = subprocess.Popen([sys.executable, '-u', str(Path(__file__).with_name('wire_server.py'))],
                                stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        try:
            proc.stdin.write(json.dumps({'oauth': data, 'bridge': bridge})+'\n')
            proc.stdin.flush()
            port = int(proc.stdout.readline())
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
            request = urllib.request.Request('http://127.0.0.1:%d/v1/linkedin/posts' % port,
                                             headers={'Authorization': 'Bearer '+bridge})
            for attrs in ('text', 'attributes', 'elsewhere'):
                def response(endpoint, params, raw):
                    if endpoint == '/rest/'+s.POSTS:
                        raw[s.ELEMENTS][0][s.TEXT] = 'PRIVATE-NAME urn:li:member:987654' if attrs == 'text' else 'PRIVATE-NAME'
                        if attrs == 'attributes':
                            raw[s.ELEMENTS][0][s.ATTRIBUTES] = [{'Mention': 'urn:li:person:987654'}]
                        if attrs == 'elsewhere':
                            raw[s.ELEMENTS][0]['futureField'] = {'deep': ['urn:li:member:987654']}
                    return raw
                fake.body = response
                with opener.open(request, timeout=10) as reply:
                    body = reply.read().decode()
                    for canary in ('PRIVATE-NAME', 'urn:li:member:', 'urn:li:person:'):
                        self.assertNotIn(canary, body)
                    self.assertEqual(json.loads(body)['beitraege'][0]['text_ausgelassen'], 'erwaehnung')
        finally:
            proc.terminate()
            output, _ = proc.communicate(timeout=10)
        self.assertIn('code=ok', output)
        for canary in ('PRIVATE-NAME', 'urn:li:member:', 'urn:li:person:', data['access_token'], bridge):
            self.assertNotIn(canary, output)

    def test_a_renewal_in_the_server_process_leaves_no_token_in_response_or_log(self):
        # #1237, 0.3.1: the exchange moved out of the log-silenced worker into the server process.
        fake = Fake()
        self.addCleanup(fake.close)
        fake.token_body = {s.ACCESS_TOKEN: secrets.token_urlsafe(32), s.EXPIRES_IN: 60*86400,
                           s.REFRESH_TOKEN: secrets.token_urlsafe(32), s.REFRESH_EXPIRES_IN: 365*86400}
        data, bridge = identity(fake.port, days=-1, refresh=True), secrets.token_urlsafe(40)
        proc = subprocess.Popen([sys.executable, '-u', str(Path(__file__).with_name('wire_server.py'))],
                                stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        try:
            proc.stdin.write(json.dumps({'oauth': data, 'bridge': bridge})+'\n')
            proc.stdin.flush()
            port = int(proc.stdout.readline())
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
            request = urllib.request.Request('http://127.0.0.1:%d/v1/linkedin/page_statistics' % port,
                                             headers={'Authorization': 'Bearer '+bridge})
            good = fake.token_body
            fake.status, fake.token_body = 500, {'error': data[s.REFRESH_TOKEN]}  # a failed exchange first
            with self.assertRaises(urllib.error.HTTPError) as failed:
                opener.open(request, timeout=10)
            refused = failed.exception.read().decode()
            failed.exception.close()
            fake.status, fake.token_body = 200, good
            with opener.open(request, timeout=10) as reply:
                body = reply.read().decode() + refused
        finally:
            proc.terminate()
            output, _ = proc.communicate(timeout=10)
        self.assertIn('code=source_unavailable', output)
        self.assertIn('code=ok', output)
        self.assertEqual(len([c for c in fake.calls if c[1] == '/oauth/v2/accessToken']), 2)
        for canary in (data[s.ACCESS_TOKEN], data[s.REFRESH_TOKEN], data[s.CLIENT_SECRET], good[s.ACCESS_TOKEN],
                       good[s.REFRESH_TOKEN], bridge):
            self.assertNotIn(canary, output)
            self.assertNotIn(canary, body)

    def test_real_server_worker_library_logs_redact_tokens(self):
        fake = Fake()
        self.addCleanup(fake.close)
        data = identity(fake.port)
        bridge = secrets.token_urlsafe(40)
        proc = subprocess.Popen([sys.executable, '-u', str(Path(__file__).with_name('wire_server.py'))],
                                stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                text=True)
        try:
            proc.stdin.write(json.dumps({'oauth': data, 'bridge': bridge})+'\n')
            proc.stdin.flush()
            port = int(proc.stdout.readline())
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
            req = urllib.request.Request('http://127.0.0.1:%d/v1/linkedin/page_statistics' % port,
                                         headers={'Authorization': 'Bearer '+bridge})
            with opener.open(req, timeout=10) as response:
                self.assertEqual(response.status, 200)
            fake.status = 500
            fake.body = {'error': data['access_token']}
            with self.assertRaises(urllib.error.HTTPError):
                opener.open(req, timeout=10)
        finally:
            proc.terminate()
            output, _ = proc.communicate(timeout=10)
        self.assertIn('code=source_unavailable', output)
        self.assertNotIn(data['access_token'], output)
        self.assertNotIn(data['client_secret'], output)
        self.assertNotIn(bridge, output)
