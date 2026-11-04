"""#1237, 0.3.1: one token state for reading and writing, renewed only in the server process.

Before 0.3.1 each of the two workers held its own copy, every write job started again from the
vault state, and a killed worker took a running renewal with it. Worker and job processes now ask
the server process over their pipe and never see the refresh token or the client secret.
Real spawned processes, real pipes and real local HTTP; only the target host is local.
"""
import asyncio
from datetime import datetime, timedelta, timezone
from functools import partial
import json
from pathlib import Path
import pickle
import tempfile
import threading
import time
import unittest
from urllib.parse import parse_qs
from linkedin_seite import server
from linkedin_seite import source_contract as s
from linkedin_seite.domain import ReportError
from linkedin_seite.oauth import Tokens, timestamp
from linkedin_seite.post_pool import WRITE_HTTP_PER_DAY, WritePool
from linkedin_seite.workers import DAILY_CAP, WorkerPool
from support import Fake, GRANT_COMMA, LocalTransport, identity
from support_post import GATEWAY_TOKEN, PreviewAsksForToken, config
from test_post_server import AUTH, Service

TOKEN_PATH = '/oauth/v2/accessToken'
RENEWED = 'RENEWED-ACCESS'


class SlowDay(str):
    """A day key whose hash yields the GIL, between the read and the write of a counter."""

    def __hash__(self):
        time.sleep(.001)
        return super().__hash__()


DAY = SlowDay('2026-11-04')


def past():
    return (datetime.now(timezone.utc) - timedelta(seconds=1)).isoformat()


def future():
    return (datetime.now(timezone.utc) + timedelta(days=30)).isoformat()


class OneState(unittest.TestCase):
    def setUp(self):
        self.fake = Fake()
        self.addCleanup(self.fake.close)
        self.fake.token_body = {s.ACCESS_TOKEN: RENEWED, s.EXPIRES_IN: 60 * 86400}
        self.data = identity(self.fake.port, days=-1, refresh=True)
        self.tokens = Tokens(self.data)

    def transport(self, counter=None):
        counter = [] if counter is None else counter
        return LocalTransport(lambda: counter.append(1), self.fake.port)

    def exchanges(self):
        return [call for call in self.fake.calls if call[1] == TOKEN_PATH]

    def test_concurrent_callers_renew_once(self):
        self.fake.delay = .2
        barrier = threading.Barrier(8)
        results = []

        def call():
            transport = self.transport()
            barrier.wait()
            results.append(self.tokens.access(transport))
        threads = [threading.Thread(target=call) for _ in range(8)]
        for thread in threads:
            thread.start()
        for thread in threads:
            thread.join(timeout=5)
        self.assertEqual(results, [RENEWED] * 8)
        self.assertEqual(len(self.exchanges()), 1)

    def test_each_caller_pays_on_its_own_transport(self):
        read, write = [], []
        self.assertEqual(self.tokens.access(self.transport(read)), RENEWED)
        self.assertEqual((len(read), len(write)), (1, 0))
        self.tokens.access(self.transport(write))  # still fresh: nobody pays
        self.assertEqual((len(read), len(write)), (1, 0))
        self.tokens.data['access_expires_at'] = past()
        self.tokens.access(self.transport(write))
        self.assertEqual((len(read), len(write)), (1, 1))

    def test_a_renewal_is_published_in_one_step(self):
        # warning() reads without the lock: it must see the old or the new state, never half of it.
        before = self.tokens.data
        snapshot = dict(before)
        self.tokens.access(self.transport())
        self.assertEqual(before, snapshot)
        self.assertIsNot(self.tokens.data, before)

    def test_a_failed_renewal_changes_nothing_and_the_next_caller_may_try(self):
        self.fake.status = 500
        with self.assertRaises(ReportError):
            self.tokens.access(self.transport())
        self.assertEqual(self.tokens.data[s.ACCESS_TOKEN], self.data[s.ACCESS_TOKEN])
        self.fake.status = 200
        self.assertEqual(self.tokens.access(self.transport()), RENEWED)
        self.assertEqual(len(self.exchanges()), 2)

    def hammer(self, permit):
        # A renewal counts from a thread while the event loop counts for the workers.
        threads = [threading.Thread(target=lambda: [permit() for _ in range(5)]) for _ in range(8)]
        for thread in threads:
            thread.start()
        for thread in threads:
            thread.join(timeout=10)

    def test_read_counter_is_exact_under_concurrent_permits(self):
        pool = WorkerPool(config(1), self.tokens, today=lambda: DAY)
        self.hammer(pool._permit)
        self.assertEqual(pool.attempts[DAY], 40)

    def test_write_counter_is_exact_under_concurrent_permits(self):
        pool = WritePool(config(1), self.tokens, GATEWAY_TOKEN, today=lambda: DAY)
        self.hammer(pool._permit_http)
        self.assertEqual(pool.http[DAY], 40)

    def test_serve_wires_one_state_into_both_pools(self):
        with tempfile.TemporaryDirectory() as temp:
            ident, token = Path(temp, 'oauth'), Path(temp, 'token')
            ident.write_text(json.dumps(identity()), encoding='utf-8')
            token.write_bytes(GATEWAY_TOKEN.encode())
            pool, writes = server.build_pools(config(1), ident, token)
            self.assertIs(pool.tokens, writes.tokens)
            token.unlink()
            pool, writes = server.build_pools(config(1), ident, token)
            self.assertIsNone(writes)
            self.assertIsInstance(pool.tokens, Tokens)


class Recording:
    """The real spawn context; keeps what is handed to a child process (the pipe end excepted)."""

    def __init__(self, context):
        self.context, self.handed = context, []

    def Pipe(self):
        return self.context.Pipe()

    def Process(self, **kwargs):
        self.handed.append(pickle.dumps((kwargs['args'][1:], kwargs.get('kwargs'))))
        return self.context.Process(**kwargs)


class SharedState(Service):
    access_days, with_refresh = -1, True

    async def asyncSetUp(self):
        await super().asyncSetUp()
        self.read_fake.token_body = self.li.token_body = {s.ACCESS_TOKEN: RENEWED, s.EXPIRES_IN: 60 * 86400,
                                                          s.SCOPE: GRANT_COMMA}

    def presented(self):
        """Every refresh token LinkedIn was shown, on either path."""
        seen = [parse_qs(call[4].decode())[s.REFRESH_TOKEN][0] for call in self.read_fake.calls if call[1] == TOKEN_PATH]
        return seen + [parse_qs(call['body'].decode())[s.REFRESH_TOKEN][0]
                       for call in self.li.calls if call['path'] == TOKEN_PATH]

    async def read(self):
        reply = await self.client.get('/v1/linkedin/page_statistics', headers=AUTH)
        return reply.status, await reply.json()

    async def post(self, text='x'):
        rid = self.plan(post_text=text)
        await self.approve(rid)
        return await self.write_call('create_post', rid)

    async def test_parallel_read_and_write_renew_once(self):
        def slow(call):
            if call['path'] == TOKEN_PATH:
                time.sleep(.5)
        self.li.override = slow
        self.read_fake.delay = .5
        (read_status, _), (write_status, body) = await asyncio.gather(self.read(), self.post())
        self.assertEqual((read_status, write_status, body['ausgefuehrt']), (200, 200, True))
        self.assertEqual(len(self.presented()), 1)
        reads = [call for call in self.read_fake.calls if call[1].startswith('/rest/')]
        self.assertTrue(reads)
        self.assertEqual({call[3]['Authorization'] for call in reads}, {'Bearer ' + RENEWED})
        self.assertEqual({call['headers']['authorization'] for call in self.li.api()}, {'Bearer ' + RENEWED})

    async def test_the_next_write_job_finds_the_renewed_token(self):
        for text in ('eins', 'zwei'):
            status, body = await self.post(text)
            self.assertEqual((status, body['ausgefuehrt']), (200, True))
        self.assertEqual(len(self.presented()), 1)

    async def test_a_rotated_refresh_token_is_the_one_the_other_path_presents(self):
        first = self.tokens.data[s.REFRESH_TOKEN]
        self.read_fake.token_body.update({s.REFRESH_TOKEN: 'ROTATED', s.REFRESH_EXPIRES_IN: 365 * 86400})
        self.assertEqual((await self.read())[0], 200)
        self.tokens.data['access_expires_at'] = past()
        status, body = await self.post()
        self.assertEqual((status, body['ausgefuehrt']), (200, True))
        self.assertEqual(self.presented(), [first, 'ROTATED'])

    async def test_a_worker_killed_during_a_renewal_does_not_lose_the_result(self):
        self.tokens.data['access_expires_at'] = future()
        await asyncio.gather(self.read(), self.read())  # both workers up: the budget measures only the renewal
        self.tokens.data['access_expires_at'] = past()
        self.pool.budget = .3
        self.read_fake.delay = 1
        self.assertEqual(await self.read(), (504, {'error': 'budget_exceeded'}))
        for _ in range(300):
            if self.tokens.data[s.ACCESS_TOKEN] == RENEWED:
                break
            await asyncio.sleep(.01)
        self.read_fake.delay = 0
        self.pool.budget = 10
        self.assertEqual((await self.read())[0], 200)
        self.assertEqual(len(self.presented()), 1)

    async def test_the_server_stays_responsive_during_a_renewal(self):
        gaps, done = [], asyncio.Event()

        async def heartbeat():
            last = time.monotonic()
            while not done.is_set():
                await asyncio.sleep(.02)
                now = time.monotonic()
                gaps.append(now - last)
                last = now
        beat = asyncio.create_task(heartbeat())
        self.read_fake.delay = 1.5
        request = asyncio.create_task(self.read())
        while not self.presented():
            await asyncio.sleep(.01)
        self.read_fake.delay = 0
        self.assertEqual((await request)[0], 200)
        done.set()
        await beat
        self.assertLess(max(gaps), .7)

    async def test_only_the_path_that_renews_pays_for_the_renewal(self):
        self.assertEqual((await self.read())[0], 200)
        self.assertEqual(sum(self.pool.attempts.values()), len(self.read_fake.calls))
        self.assertEqual(self.writes.http, {})
        spent = dict(self.pool.attempts)
        self.tokens.data['access_expires_at'] = past()
        self.assertEqual((await self.post())[0], 200)
        self.assertEqual(self.pool.attempts, spent)
        self.assertEqual(sum(self.writes.http.values()), len(self.li.calls))
        self.assertEqual(len(self.presented()), 2)

    async def test_a_spent_read_quota_stops_only_the_read_path(self):
        self.pool.attempts[self.pool.today()] = DAILY_CAP
        self.assertEqual(await self.read(), (429, {'error': 'daily_cap_reached'}))
        self.assertEqual(self.presented(), [])
        status, body = await self.post()
        self.assertEqual((status, body['ausgefuehrt']), (200, True))
        self.assertEqual(len(self.presented()), 1)

    async def test_a_spent_write_quota_stops_only_the_write_path(self):
        self.writes.http[self.day[0]] = WRITE_HTTP_PER_DAY
        status, body = await self.post()
        self.assertEqual((status, body), (200, {'error': 'schreib_http_tageslimit', 'nicht_ausgefuehrt': True}))
        self.assertEqual(self.li.calls, [])
        self.assertEqual((await self.read())[0], 200)
        self.assertEqual(len(self.presented()), 1)

    async def test_no_secret_is_handed_to_a_worker_or_a_job(self):
        read_context, write_context = Recording(self.pool.context), Recording(self.writes.context)
        self.pool.context, self.writes.context = read_context, write_context
        self.assertEqual((await self.read())[0], 200)
        self.assertEqual((await self.post())[0], 200)
        handed = read_context.handed + write_context.handed
        self.assertGreaterEqual(len(handed), 3)  # worker, preview job, write job
        for blob in handed:
            for field in (s.REFRESH_TOKEN, s.CLIENT_SECRET, s.ACCESS_TOKEN):
                self.assertNotIn(self.data[field].encode(), blob, field)

    async def test_a_preview_cannot_fetch_a_token(self):
        self.writes.factory = partial(PreviewAsksForToken, self.li.port)
        self.assertEqual(await self.vorschau(self.plan(post_text='x')), (500, {'error': 'vorschau_schreibt'}))
        self.assertEqual(self.presented(), [])

    async def test_both_warnings_read_the_one_state(self):
        original = (datetime.now(timezone.utc) + timedelta(days=5)).isoformat()
        self.tokens.data['refresh_expires_at'] = original
        self.assertEqual(self.pool.warning()['ablaufdatum'], self.writes.warning()['ablaufdatum_schreiben'])
        self.read_fake.token_body.update({s.REFRESH_TOKEN: 'ROTATED', s.REFRESH_EXPIRES_IN: 2 * 86400})
        self.assertEqual((await self.read())[0], 200)
        shortened = timestamp(self.tokens.data['refresh_expires_at'])
        self.assertLess(shortened, timestamp(original))  # the provider may shorten, never extend
        self.assertEqual(self.pool.warning()['ablaufdatum'], shortened.date().isoformat())
        self.assertEqual(self.writes.warning()['ablaufdatum_schreiben'], shortened.date().isoformat())

    async def test_the_read_path_still_sends_only_get_to_the_api(self):
        self.assertEqual((await self.read())[0], 200)
        api = [call for call in self.read_fake.calls if call[1] != TOKEN_PATH]
        self.assertTrue(api)
        self.assertEqual({call[0] for call in api}, {'GET'})


if __name__ == '__main__':
    unittest.main()
