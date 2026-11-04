"""SEC c-6608136792: the core consumes definite non-execution only in 2xx bodies."""
from datetime import datetime, timedelta, timezone
import json

from aiohttp.test_utils import TestClient
from linkedin_seite import redaktion as r, server, source_contract as s
from linkedin_seite.post_pool import WRITE_ACTIONS_PER_DAY, WRITE_HTTP_PER_DAY
from support_post import GATEWAY_TOKEN, PNG
from test_post_server import Service, SafeTestServer, AUTH, KEY

HEADER = {'X-Jnpt-Freigabe': 'hash=freigabe:v2:sha256:' + 'a'*64}


class Outcome(Service):
    def definite(self, status, body, code):
        self.assertEqual((status, body), (200, {'error': code, 'nicht_ausgefuehrt': True}))
        self.assertEqual(self.li.writes(), [])
        for secret in (GATEWAY_TOKEN, self.data[s.ACCESS_TOKEN], self.data[s.CLIENT_SECRET]):
            self.assertNotIn(secret, json.dumps(body))

    async def approved_entry(self, action):
        rid = self.plan(post_text='Text', kommentar_text='Kommentar',
                        **({} if action == 'posten' else {'post_urn': 'urn:li:share:7'}))
        await self.approve(rid, action)
        return rid

    async def test_action_daily_cap_is_200_with_nonexecution_for_every_write(self):
        self.writes.actions[self.day[0]] = WRITE_ACTIONS_PER_DAY
        for route, action in r.AKTIONEN.items():
            rid = await self.approved_entry(action)
            self.definite(*await self.write_call(route, rid), 'schreib_tageslimit')
        self.assertEqual(self.li.calls, [])

    async def test_http_daily_cap_before_first_attempt_is_definite_for_every_write(self):
        self.writes.http[self.day[0]] = WRITE_HTTP_PER_DAY
        for route, action in r.AKTIONEN.items():
            rid = await self.approved_entry(action)
            self.definite(*await self.write_call(route, rid), 'schreib_http_tageslimit')
        self.assertEqual(self.li.calls, [])

    async def test_disabled_write_service_is_200_only_after_header_check(self):
        client = TestClient(SafeTestServer(server.create_app(self.cfg, KEY.encode(), self.pool, None)))
        await client.start_server()
        try:
            for route in r.AKTIONEN:
                reply = await client.post('/v1/linkedin-post/' + route,
                                          json={'eintrag_id': '0123456789abcdef'}, headers={**AUTH, **HEADER})
                self.definite(reply.status, await reply.json(), 'schreiben_nicht_eingerichtet')
                reply = await client.post('/v1/linkedin-post/' + route,
                                          json={'eintrag_id': '0123456789abcdef'}, headers=AUTH)
                self.assertEqual((reply.status, await reply.json()), (409, {'error': 'nicht_freigegeben'}))
        finally:
            await client.close()
        self.assertEqual(self.li.calls, [])

    async def test_closed_pool_is_definite_without_starting_a_job(self):
        rid = await self.approved_entry('posten')
        self.writes.closed = True
        self.definite(*await self.write_call('create_post', rid), 'service_stopping')
        self.assertIsNone(self.writes.job)

    async def test_expired_token_is_definite_before_any_linkedin_request(self):
        rid = await self.approved_entry('posten')
        self.tokens.data['access_expires_at'] = (datetime.now(timezone.utc) - timedelta(days=1)).isoformat()
        self.definite(*await self.write_call('create_post', rid), 'token_abgelaufen')
        self.assertEqual(self.li.calls, [])

    async def test_request_validation_with_valid_header_is_definite_for_every_write(self):
        for route in r.AKTIONEN:
            reply = await self.client.post('/v1/linkedin-post/' + route, data=b'{}', headers={**AUTH, **HEADER})
            self.definite(reply.status, await reply.json(), 'invalid_input')
        self.assertEqual(self.gw.calls, [])
        self.assertEqual(self.li.calls, [])

    async def test_entry_validation_and_media_format_and_size_are_definite(self):
        cases = [({'post_text': '‹verboten›'}, PNG, 'pseudonym_im_feld'),
                 ({'post_text': 'x', 'medien': '["a.png"]'}, b'wrong-format', 'medium_format_ungueltig'),
                 ({'post_text': 'x', 'medien': '["a.png"]'}, PNG + b'x'*1024, 'medium_zu_gross')]
        from dataclasses import replace
        self.writes.cfg = replace(self.cfg, schreiben=replace(self.cfg.schreiben, max_medium_bytes=1024))
        for fields, data, code in cases:
            self.gw.files['a.png'] = data
            rid = self.plan(**fields)
            self.definite(*await self.write_call('create_post', rid, headers={**AUTH, **HEADER}), code)
        self.assertEqual(self.li.calls, [])

    async def test_gateway_rejection_before_sending_is_definite(self):
        rid = await self.approved_entry('posten')
        self.gw.status = 401
        self.definite(*await self.write_call('create_post', rid), 'gateway_zugang_verweigert')
        self.assertEqual(self.li.calls, [])

    async def test_refresh_rejection_is_definite_without_a_business_write(self):
        from support_post import write_identity
        rid = await self.approved_entry('posten')
        self.tokens.data = write_identity(self.li.port, days=-1, refresh=True)
        self.li.token_body[s.SCOPE] = 'r_organization_social'
        self.definite(*await self.write_call('create_post', rid), 'unexpected_source_shape')
        self.assertEqual([c['path'] for c in self.li.calls], ['/oauth/v2/accessToken'])

    async def test_invalid_header_and_hash_are_409_without_either_outcome_field(self):
        rid = await self.approved_entry('posten')
        for headers in (AUTH, {**AUTH, 'X-Jnpt-Freigabe': 'wrong'}, {**AUTH, **HEADER}):
            status, body = await self.write_call('create_post', rid, headers=headers)
            self.assertEqual((status, body), (409, {'error': 'nicht_freigegeben'}))
        self.assertEqual(self.li.calls, [])

    async def test_bridge_failure_never_becomes_a_200_nonexecution_result(self):
        rid = self.plan(post_text='x')
        for headers, expected in ((HEADER, 401), ({**HEADER, 'Authorization': 'Bearer wrong'}, 403)):
            status, body = await self.write_call('create_post', rid, headers=headers)
            self.assertEqual(status, expected)
            self.assertNotIn('nicht_ausgefuehrt', body)
            self.assertNotIn('ausgefuehrt', body)
        self.assertEqual(self.gw.calls, [])
        self.assertEqual(self.li.calls, [])

    async def test_preview_error_keeps_error_status_and_has_no_outcome(self):
        rid = self.plan(post_text='‹verboten›')
        status, body = await self.vorschau(rid)
        self.assertEqual((status, body), (422, {'error': 'pseudonym_im_feld'}))
        self.assertEqual(self.li.calls, [])

    async def test_cap_after_upload_initialization_remains_unclear(self):
        rid = self.plan(post_text='x', medien='["a.png"]')
        self.gw.files['a.png'] = PNG
        await self.approve(rid)
        self.writes.http[self.day[0]] = WRITE_HTTP_PER_DAY - 1
        status, body = await self.write_call('create_post', rid)
        self.assertEqual((status, body), (429, {'error': 'schreib_http_tageslimit'}))
        self.assertEqual(len(self.li.writes()), 1)
        self.assertEqual(self.li.calls[0]['query'], 'action=initializeUpload')

    async def test_rejected_first_business_request_still_has_no_outcome_flags(self):
        rid = await self.approved_entry('posten')
        self.li.override = lambda call: (403, {}, b'')
        status, body = await self.write_call('create_post', rid)
        self.assertEqual((status, body), (502, {'error': 'source_access_denied'}))
        self.assertEqual(len(self.li.writes()), 1)
