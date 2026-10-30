from datetime import datetime, timezone
import json
from pathlib import Path
import tempfile
import unittest
from linkedin_seite.domain import Config, ReportError, parse_request, strict_json
from linkedin_seite.oauth import load_identity
from linkedin_seite import server
from support import identity


class Contract(unittest.TestCase):
    def test_defaults_utc_completed_days_and_ninety_days(self):
        now = datetime(2026, 10, 29, 23, 0, tzinfo=timezone.utc)
        req = parse_request('followers', [], now)
        self.assertEqual(datetime.fromtimestamp(req.start/1000, timezone.utc).date().isoformat(), '2026-10-21')
        self.assertEqual(datetime.fromtimestamp(req.end/1000, timezone.utc).date().isoformat(), '2026-10-28')
        req = parse_request('followers', [('period_days', '90')], now)
        self.assertEqual(req.end - req.start, 90*86400000)
        self.assertEqual(Config.from_dict({'organization': '123'}).organization, 'urn:li:organization:123')
        self.assertEqual(Config.from_dict({'organization': '123'}).linkedin_version, datetime.now(timezone.utc).strftime('%Y%m'))

    def test_configuration_cannot_set_endpoint_scope_or_secret(self):
        for field in ('base_url', 'token_url', 'scope', 'scopes', 'access_token', 'author', 'proxy'):
            with self.subTest(field=field), self.assertRaises(ValueError):
                Config.from_dict({'organization': '123', field: 'CANARY'})
        for org in (None, 123, 'urn:li:person:123', '../123', '123?x=1', '0', '1'*21):
            with self.assertRaises(ValueError):
                Config.from_dict({'organization': org})
        for version in ('202613', '20261', 202610, '202610\nCANARY'):
            with self.assertRaises(ValueError):
                Config.from_dict({'organization': '123', 'linkedin_version': version})

    def test_post_limit_and_unknown_parameters(self):
        for pairs in ([('limit', '11')], [('limit', '0')], [('limit', '1'), ('limit', '2')], [('period_days', '7')]):
            with self.assertRaises(ReportError):
                parse_request('posts', pairs)
        self.assertEqual(parse_request('posts', [('limit', '1')]).limit, 1)

    def test_runtime_paths_and_missing_invalid_identity(self):
        self.assertEqual(server.load_runtime_files.__defaults__, (server.BRIDGE_KEY_PATH, server.IDENTITY_PATH))
        self.assertEqual(server.IDENTITY_PATH, '/run/secrets/oauth/runtime-key')
        self.assertEqual(server.CONFIG_PATH, '/etc/linkedin-seite/config.json')
        with tempfile.TemporaryDirectory() as temp:
            key, oauth = Path(temp, 'key'), Path(temp, 'oauth')
            with self.assertRaises(OSError):
                server.load_runtime_files(key, oauth)
            oauth.write_text(json.dumps(identity()), encoding='utf-8')
            for raw in (b'short', b'a'*257, b'a'*31+b'\x00', b'a'*31+b' ' + b'a'):
                key.write_bytes(raw)
                with self.assertRaises(ValueError):
                    server.load_runtime_files(key, oauth)
            key.write_bytes(b'synthetic-' + b'a'*32)
            self.assertEqual(server.load_runtime_files(key, oauth), key.read_bytes())
            for bad in ({}, {'token_uri': 'http://example.test'}, {**identity(), 'refresh_token': 'synthetic'},
                        {**identity(), 'access_expires_at': '2999-01-01T00:00:00+00:00'},
                        {**identity(), 'access_expires_at': '2026-10-29'},
                        {**identity(), 'access_token': 'line\nbreak'}):
                oauth.write_text(json.dumps(bad), encoding='utf-8')
                with self.assertRaises(ValueError):
                    server.load_runtime_files(key, oauth)
            oauth.write_bytes(b'x'*(64*1024+1))
            with self.assertRaises(ValueError):
                load_identity(oauth)

    def test_duplicate_json_and_nonfinite_numbers_rejected(self):
        for raw in ('{"organization":"123","organization":"999"}', '{"x": NaN}', '{"x": Infinity}'):
            with self.assertRaises(ValueError):
                strict_json(raw)

    def test_bind_rejects_wildcard_loopback_ipv6_and_public(self):
        for value in ('', '0.0.0.0', '127.0.0.1', '::', '::1', '8.8.8.8', 'not-an-ip'):
            with self.subTest(value=value), self.assertRaises(ValueError):
                server.bind_ip(value)
        self.assertEqual(server.bind_ip('172.30.0.20'), '172.30.0.20')
