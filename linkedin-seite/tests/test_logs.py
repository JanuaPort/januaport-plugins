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
