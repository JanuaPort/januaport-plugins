import asyncio
from datetime import datetime, timedelta, timezone
import json
import unittest
from aiohttp.test_utils import TestClient, TestServer
from linkedin_seite.domain import ReportError, parse_request, encode_response
from linkedin_seite.safe_http import SafeAppRunner
from linkedin_seite.server import create_app
from linkedin_seite.workers import WorkerPool, DAILY_CAP
from support import Fake, LocalAdapter, CFG, identity


class SafeTestServer(TestServer):
    async def _make_runner(self, **kwargs):
        return SafeAppRunner(self.app, access_log=None, max_line_size=8192, max_field_size=8192, **kwargs)


class Boundary(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.fake = Fake()
        self.data = identity(self.fake.port)
        self.pool = WorkerPool(CFG, self.data, adapter_factory=LocalAdapter, budget=5)
        self.key = 'SYNTHETIC-' + 'a'*32
        self.auth = {'Authorization': 'Bearer ' + self.key}
        self.client = TestClient(SafeTestServer(create_app(CFG, self.key.encode(), self.pool)))
        await self.client.start_server()

    async def asyncTearDown(self):
        await self.client.close()
        await asyncio.to_thread(self.fake.close)

    async def get(self, query='', **kwargs):
        return await self.client.get('/v1/linkedin/page_statistics' + query, headers=kwargs.pop('headers', self.auth), **kwargs)

    async def test_bridge_missing_wrong_duplicate_never_reach_adapter(self):
        for headers, expected in (({}, 401), ({'Authorization': 'Bearer wrong'}, 403),
                                  ([('Authorization', self.auth['Authorization'])]*2, 401)):
            response = await self.get(headers=headers)
            self.assertEqual(response.status, expected)
        self.assertEqual(self.fake.calls, [])
        self.assertEqual((await self.get()).status, 200)

    async def test_org_paths_parameters_duplicates_dates_rejected(self):
        for query in ('organization=urn:li:organization:999', 'organizationalEntity=999', 'author=999',
                      'organization_id=999', 'path=posts', 'url=http://example.test', 'scope=x',
                      'period_days=91', 'period_days=0', 'period_days=-1', 'period_days=7&period_days=8',
                      'period_days=1.0', 'end_date=2999-01-01', 'end_date=2026-02-30', 'end_date=2026-1-1',
                      'limit=11', 'country=de', 'member=123'):
            with self.subTest(query=query):
                response = await self.get('?' + query)
                self.assertEqual(response.status, 400)
        self.assertEqual(self.fake.calls, [])

    async def test_follower_lag_and_org_override_rejected_before_worker(self):
        yesterday = (datetime.now(timezone.utc)-timedelta(days=1)).date().isoformat()
        for query in ('end_date='+yesterday, 'organization=urn:li:organization:999', 'edgeType=other', 'path=networkSizes'):
            reply = await self.client.get('/v1/linkedin/followers?'+query, headers=self.auth)
            self.assertEqual(reply.status, 400)
            self.assertEqual(await reply.json(), {'error': 'end_date_not_available' if query.startswith('end_date=') else 'invalid_input'})
        self.assertEqual(self.fake.calls, [])

    async def test_no_write_method_no_generic_or_person_routes(self):
        for method in ('POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS', 'HEAD'):
            response = await self.client.request(method, '/v1/linkedin/page_statistics', headers=self.auth)
            self.assertEqual(response.status, 405)
        for path in ('create_post', 'delete_post', 'comments', 'reactions', 'members', 'run', 'posts/123', '../rest/posts'):
            response = await self.client.get('/v1/linkedin/' + path, headers=self.auth)
            self.assertEqual(response.status, 404)
        self.assertEqual(self.fake.calls, [])

    async def test_body_rejected_and_local_health_does_not_use_api(self):
        response = await self.get(data=b'{}')
        self.assertEqual(response.status, 400)
        self.assertEqual((await self.client.get('/healthz')).status, 200)
        self.assertEqual(self.fake.calls, [])

    async def test_parser_limits_path_limit_safe_json_and_no_token_logs(self):
        secret = self.data['access_token']
        with self.assertLogs('linkedin_seite', level='INFO') as logs:
            for query in ('?CANARY=' + 'A'*8200, '?access_token=' + secret):
                response = await self.get(query)
                self.assertEqual(response.status, 400)
                text = await response.text()
                self.assertNotIn(secret, text)
                self.assertNotIn('CANARY', text)
                if len(query) > 8192:
                    self.assertEqual(json.loads(text), {'error': 'invalid_http_request'})
            response = await self.get(headers={'X-Canary': secret + 'A'*8200})
            self.assertEqual(response.status, 400)
            self.assertEqual(await response.json(), {'error': 'invalid_http_request'})
            response = await self.client.get('/' + 'X'*513)
            self.assertEqual(response.status, 400)
        self.assertNotIn(secret, str(logs.output))
        self.assertNotIn('CANARY', str(logs.output))

    async def test_source_failure_token_never_in_log_or_error(self):
        secret = self.data['access_token']
        self.fake.status = 500
        self.fake.body = {'error': secret}
        with self.assertLogs('linkedin_seite', level='INFO') as logs:
            response = await self.get()
        self.assertEqual(await response.json(), {'error': 'source_unavailable'})
        self.assertNotIn(secret, str(logs.output))
        self.assertNotIn(self.key, str(logs.output))

    async def test_warning_on_success_error_and_health_expiry_code(self):
        self.data['access_expires_at'] = (datetime.now(timezone.utc)+timedelta(days=10)).isoformat()
        for url in ('/v1/linkedin/page_statistics', '/healthz', '/unknown'):
            response = await self.client.get(url, headers=self.auth)
            body = await response.json()
            self.assertEqual(body['warnung'], 'token_laeuft_ab')
            self.assertRegex(body['ablaufdatum'], r'^\d{4}-\d\d-\d\d$')
        await self.pool.close()
        self.pool.closed = False
        self.data['access_expires_at'] = (datetime.now(timezone.utc)-timedelta(seconds=1)).isoformat()
        response = await self.get()
        self.assertEqual(await response.json(), {'error': 'token_abgelaufen'})

    async def test_http_disconnect_kills_real_adapter(self):
        self.fake.delay = 10
        task = asyncio.create_task(self.get())
        for _ in range(300):
            if self.fake.calls:
                break
            await asyncio.sleep(.01)
        self.assertTrue(self.fake.calls)
        task.cancel()
        await asyncio.gather(task, return_exceptions=True)
        for _ in range(200):
            if self.pool.pending == 0:
                break
            await asyncio.sleep(.01)
        self.assertEqual(self.pool.pending, 0)
        self.assertTrue(all(slot is None for slot in self.pool.slots))
        self.assertEqual(sum(self.pool.attempts.values()), 1)

    def test_output_limit(self):
        with self.assertRaises(ReportError):
            encode_response({'x': 'a'*(512*1024)})


class Workers(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.fake = Fake()
        self.day = ['2026-10-29']
        self.pool = WorkerPool(CFG, identity(self.fake.port), adapter_factory=LocalAdapter,
                               budget=3, today=lambda: self.day[0])
        self.req = parse_request('page_statistics', [])

    async def asyncTearDown(self):
        await self.pool.close()
        await asyncio.to_thread(self.fake.close)

    async def test_daily_cap_counts_individual_gets_and_failures_parent_utc(self):
        self.assertEqual(DAILY_CAP, 50)
        for _ in range(24):
            self.pool.rate.clear()
            await self.pool.run(self.req)
        self.fake.status = 500
        for _ in range(2):
            self.pool.rate.clear()
            with self.assertRaises(ReportError):
                await self.pool.run(self.req)
        self.pool.rate.clear()
        with self.assertRaises(ReportError) as cm:
            await self.pool.run(self.req)
        self.assertEqual(cm.exception.code, 'daily_cap_reached')
        self.assertEqual(len(self.fake.calls), 50)
        self.assertEqual(self.pool.attempts, {self.day[0]: 50})
        self.day[0] = '2026-10-30'
        self.fake.status = 200
        await self.pool.run(self.req)
        self.assertEqual(self.pool.attempts, {self.day[0]: 2})

    async def test_cap_stops_partway_posts_and_counts_token_exchange(self):
        self.pool.attempts[self.day[0]] = 49
        with self.assertRaises(ReportError) as cm:
            await self.pool.run(parse_request('posts', []))
        self.assertEqual(cm.exception.code, 'daily_cap_reached')
        self.assertEqual(len(self.fake.calls), 1)
        await self.pool.close()
        self.pool = WorkerPool(CFG, identity(self.fake.port, days=-1, refresh=True), adapter_factory=LocalAdapter)
        await self.pool.run(self.req)
        self.assertEqual(sum(self.pool.attempts.values()), 3)

    async def test_timeout_kills_real_http_and_counter_survives(self):
        self.fake.delay = 10
        with self.assertRaises(ReportError) as cm:
            await self.pool.run(self.req)
        self.assertEqual(cm.exception.code, 'budget_exceeded')
        self.assertEqual(len(self.fake.calls), 1)
        self.assertTrue(all(slot is None for slot in self.pool.slots))
        self.assertEqual(sum(self.pool.attempts.values()), 1)
        self.fake.delay = 0
        await self.pool.run(self.req)
        self.assertEqual(sum(self.pool.attempts.values()), 3)

    async def test_minute_throttle_and_closed_pool(self):
        self.fake.status = 500
        for _ in range(30):
            with self.assertRaises(ReportError):
                await self.pool.run(self.req)
        with self.assertRaises(ReportError) as cm:
            await self.pool.run(self.req)
        self.assertEqual(cm.exception.code, 'source_rate_limited')
        self.assertEqual(len(self.fake.calls), 30)
        await self.pool.close()
        with self.assertRaises(ReportError) as cm:
            await self.pool.run(self.req)
        self.assertEqual(cm.exception.code, 'service_stopping')

    async def test_two_workers_four_queue_slots(self):
        self.fake.delay = 10
        tasks = [asyncio.create_task(self.pool.run(self.req)) for _ in range(6)]
        await asyncio.sleep(.1)
        self.assertEqual(sum(slot is not None for slot in self.pool.slots), 2)
        with self.assertRaises(ReportError) as cm:
            await self.pool.run(self.req)
        self.assertEqual(cm.exception.code, 'queue_full')
        for task in tasks:
            task.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)
