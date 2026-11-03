"""Stufe B1 (#1237): write jobs, brake and trace in the long-running server process.

Every write action runs in its OWN spawned process (private RAM copy of the media, process ends
after the action). The server process holds what a job must not be able to reset:
  * 5 write actions per UTC day over all actions (SEC Bed. 5), counted when the job asks for
    permission right before its first LinkedIn request; a video counts from initializeUpload,
  * 45 LinkedIn HTTP attempts per UTC day for writes (Development Tier 100 per member and day,
    Stufe A keeps its own 50), every attempt asks first,
  * the idempotency map eintrag_id + hash -> result (in-process only, lost on restart),
  * eintrag_id + hash whose non-idempotent final request (create post/comment) went out and then
    failed: the outcome is unknown, the same approval is refused with `ergebnis_unbekannt`.
A job cannot reach LinkedIn without a grant over its pipe: if the server is gone, the pipe is
closed and the job stops before the next request.

At most one write job runs. The HTTP request waits WAIT_SECONDS; a longer job (video) answers
202 `in_arbeit` and keeps running up to JOB_HARD_SECONDS — a client disconnect does not kill it.
A repeat call for the same entry and action reports the state, later the result.
"""
import asyncio
from collections import deque
from datetime import datetime, timezone
import json
import logging
import multiprocessing
import time
from . import redaktion as r
from .domain import ReportError, encode_response

LOG = logging.getLogger('linkedin_seite')

WRITE_ACTIONS_PER_DAY = 5
WRITE_HTTP_PER_DAY = 45
WAIT_SECONDS = 20
VORSCHAU_BUDGET = 20
VORSCHAU_PER_MINUTE = 10
JOB_HARD_SECONDS = 25 * 60


class PipeLink:
    """Job-process side: ask the server process before counting and before every request."""

    def __init__(self, conn):
        self.conn = conn

    def ask(self, *message):
        self.conn.send(message)
        reply = self.conn.recv()
        if reply[0] == 'error':
            raise ReportError(reply[1], reply[2])
        return reply

    def permit_http(self):
        self.ask('permit')

    def begin_action(self):
        self.ask('aktion')

    def report_hash(self, digest):
        return self.ask('hash', digest)[0] == 'weiter'

    def final_request(self):
        self.ask('final')

    def expiry(self, value):
        self.conn.send(('expiry', value))


def job_main(conn, cfg, identity, gateway_token, req, adapter_factory=None):
    logging.disable(logging.CRITICAL)  # no library or request logging inside a job
    from .post_adapter import Erledigt, PostAdapter
    factory = adapter_factory or PostAdapter
    link = PipeLink(conn)
    adapter = None
    try:
        adapter = factory(cfg, identity, gateway_token, link)
        result = adapter.vorschau(req) if req.vorschau else adapter.run(req)
        message = ('ok', encode_response(result))
    except Erledigt:
        message = ('erledigt',)
    except ReportError as exc:
        message = ('error', exc.status, exc.code)
    except Exception:
        message = ('error', 502, 'source_unavailable')
    try:
        expiry = adapter.tokens.data.get('refresh_expires_at') if adapter else None
        if expiry:
            link.expiry(expiry)  # only expiry metadata crosses back, never a token
        conn.send(message)
    except (EOFError, BrokenPipeError, OSError):
        pass
    finally:
        conn.close()


class Job:
    def __init__(self, req, proc, conn, loop):
        self.req, self.proc, self.conn = req, proc, conn
        self.key = (req.eintrag_id, req.aktion)
        self.future = loop.create_future()
        self.digest = None
        self.abgeholt = False
        self.final = False  # the non-idempotent final request was about to be sent
        self.task = None


def in_arbeit(job):
    payload = {'aktion': job.req.aktion, 'eintrag_id': job.req.eintrag_id, 'state': 'in_arbeit'}
    if job.digest:
        payload['hash'] = job.digest
    return 202, encode_response(payload)


class WritePool:
    def __init__(self, cfg, identity, gateway_token, *, adapter_factory=None, today=None, wait=WAIT_SECONDS,
                 hard=JOB_HARD_SECONDS, budget=VORSCHAU_BUDGET):
        self.cfg, self.identity, self.token = cfg, identity, gateway_token
        self.factory = adapter_factory
        self.context = multiprocessing.get_context('spawn')
        self.today = today or (lambda: datetime.now(timezone.utc).date().isoformat())
        self.wait, self.hard, self.budget = wait, hard, budget
        self.actions = {}   # UTC day -> counted write actions (SEC Bed. 5)
        self.http = {}      # UTC day -> LinkedIn HTTP attempts of write jobs
        self.done = {}      # (eintrag_id, hash) -> result payload, in-process idempotency
        self.unbekannt = set()  # (eintrag_id, hash) whose final request went out and then failed
        self.job = None
        self.vorschau_job = None
        self.vorschau_rate = deque()
        self.closed = False

    def warning(self):
        from .oauth import warning
        extra = warning(self.identity)
        return {'warnung_schreiben': extra['warnung'], 'ablaufdatum_schreiben': extra['ablaufdatum']} if extra else {}

    # --- counters, in this process only ---------------------------------------------------

    def _count(self, store, cap, code):
        day = self.today()
        for key in [k for k in store if k != day]:
            del store[key]
        if store.get(day, 0) >= cap:
            raise ReportError(429, code)
        store[day] = store.get(day, 0) + 1

    # --- job lifecycle ------------------------------------------------------------------------

    def _start(self, req):
        parent, child = self.context.Pipe()
        proc = self.context.Process(target=job_main, args=(child, self.cfg, self.identity, self.token, req, self.factory),
                                    daemon=True)
        proc.start()
        child.close()
        job = Job(req, proc, parent, asyncio.get_running_loop())
        job.task = asyncio.create_task(self._supervise(job))
        return job

    def _reply(self, job, message):
        kind = message[0]
        if kind == 'expiry':
            from .oauth import timestamp
            current = self.identity.get('refresh_expires_at')
            if current and timestamp(message[1]) < timestamp(current):
                self.identity['refresh_expires_at'] = message[1]
            return None
        if kind not in ('permit', 'aktion', 'hash', 'final'):
            return message  # final message
        try:
            if job.req.vorschau:
                raise ReportError(500, 'vorschau_schreibt')  # a preview never talks to LinkedIn
            if kind == 'permit':
                self._count(self.http, WRITE_HTTP_PER_DAY, 'schreib_http_tageslimit')
                job.conn.send(('granted',))
            elif kind == 'aktion':
                self._count(self.actions, WRITE_ACTIONS_PER_DAY, 'schreib_tageslimit')
                job.conn.send(('granted',))
            elif kind == 'final':
                job.final = True
                job.conn.send(('granted',))
            else:
                job.digest = message[1] if isinstance(message[1], str) and r.HASH.fullmatch(message[1]) else None
                if job.digest is None:
                    raise ReportError(502, 'worker_failed')
                if (job.req.eintrag_id, job.digest) in self.unbekannt:
                    # A post/comment may exist already; never send it again on the same approval.
                    raise ReportError(409, 'ergebnis_unbekannt')
                job.conn.send(('fertig',) if (job.req.eintrag_id, job.digest) in self.done else ('weiter',))
        except ReportError as exc:
            job.conn.send(('error', exc.status, exc.code))
        return None

    async def _supervise(self, job):
        loop = asyncio.get_running_loop()
        deadline = loop.time() + (self.budget if job.req.vorschau else self.hard)
        outcome = None
        try:
            while outcome is None:
                while not job.conn.poll():
                    if not job.proc.is_alive() and not job.conn.poll():
                        raise ReportError(502, 'worker_failed')
                    if loop.time() > deadline:
                        raise ReportError(504, 'budget_exceeded' if job.req.vorschau else 'schreiben_zeit_ueberschritten')
                    await asyncio.sleep(0.01)
                outcome = self._reply(job, job.conn.recv())
            if outcome[0] == 'ok':
                result = (200, outcome[1])
                if not job.req.vorschau:
                    self.done[(job.req.eintrag_id, json.loads(outcome[1])['hash'])] = outcome[1]
            elif outcome[0] == 'erledigt':
                stored = json.loads(self.done[(job.req.eintrag_id, job.digest)])
                stored['wiederholt'] = True
                result = (200, encode_response(stored))
            else:
                raise ReportError(outcome[1], outcome[2])
            self._trace(job, 'wiederholt' if outcome[0] == 'erledigt' else 'ok', json.loads(result[1]))
            job.future.set_result(result)
        except (EOFError, BrokenPipeError, OSError, KeyError, ValueError, IndexError, TypeError):
            self._finish_error(job, ReportError(502, 'worker_failed'))
        except ReportError as exc:
            self._finish_error(job, exc)
        except asyncio.CancelledError:
            self._finish_error(job, ReportError(503, 'service_stopping'))
            raise
        finally:
            self._kill(job)

    def _finish_error(self, job, exc):
        if job.final and job.digest:
            self.unbekannt.add((job.req.eintrag_id, job.digest))
        self._trace(job, exc.code, {})
        if not job.future.done():
            job.future.set_exception(exc)
            job.future.exception()  # consumed here; run() re-raises it if somebody waits

    def _trace(self, job, code, payload):
        """One line per finished write job: ids, URN, hash and size only (SEC Bed. 5, V3)."""
        if job.req.vorschau:
            return
        urn = payload.get('post_urn') if code in ('ok', 'wiederholt') else None
        if not (isinstance(urn, str) and r.POST_URN.fullmatch(urn)):
            urn = '-'
        size = payload.get('bytes') if type(payload.get('bytes')) is int else 0
        LOG.info('spur aktion=%s eintrag_id=%s hash=%s code=%s urn=%s bytes=%d', job.req.aktion, job.req.eintrag_id,
                 job.digest or '-', code, urn, size)

    def _kill(self, job):
        try:
            job.conn.close()
        except OSError:
            pass
        if job.proc.is_alive():
            job.proc.kill()
        job.proc.join(timeout=0.5)

    # --- entry points -------------------------------------------------------------------------

    async def run(self, req):
        if self.closed:
            raise ReportError(503, 'service_stopping')
        if req.vorschau:
            return await self._vorschau(req)
        key = (req.eintrag_id, req.aktion)
        if self.job is not None and not self.job.future.done():
            if self.job.key == key:
                return in_arbeit(self.job)
            raise ReportError(429, 'schreiben_belegt')
        previous = self.job
        if previous is not None and previous.key == key and not previous.abgeholt:
            previous.abgeholt = True
            failed = previous.future.exception()
            if failed is not None:
                raise ReportError(failed.status, failed.code)  # the outcome after a 202, reported once
            return previous.future.result()  # first delivery of a result that finished after its 202
        self.job = job = self._start(req)
        try:
            result = await asyncio.wait_for(asyncio.shield(job.future), self.wait)
        except TimeoutError:
            return in_arbeit(job)
        except ReportError:
            job.abgeholt = True
            raise
        job.abgeholt = True
        return result

    async def _vorschau(self, req):
        started = time.monotonic()
        while self.vorschau_rate and started - self.vorschau_rate[0] >= 60:
            self.vorschau_rate.popleft()
        if len(self.vorschau_rate) >= VORSCHAU_PER_MINUTE:
            raise ReportError(429, 'vorschau_rate_limited')
        if self.vorschau_job is not None and not self.vorschau_job.future.done():
            raise ReportError(429, 'vorschau_belegt')
        self.vorschau_rate.append(started)
        self.vorschau_job = job = self._start(req)
        try:
            return await asyncio.shield(job.future)
        except asyncio.CancelledError:
            job.task.cancel()  # a preview dies with its request
            raise

    async def close(self):
        self.closed = True
        for job in (self.job, self.vorschau_job):
            if job is not None and job.task is not None and not job.task.done():
                job.task.cancel()
                await asyncio.gather(job.task, return_exceptions=True)
