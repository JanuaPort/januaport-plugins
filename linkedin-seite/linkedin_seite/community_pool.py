"""Stufe B3 (#1237, 0.4.0): spawned community jobs, the reply queue and the lock per comment.

One spawned process per call, like the write jobs: token over the pipe, permit before every LinkedIn
request, logging silenced in the job. Reply jobs run one after another in an in-process queue, the
next send at least 60 seconds after the previous one (cut B4); at most one call waits. A comment is
locked while a call for it is in flight and for ten minutes after its send attempt, RAM only.

There is NO reply counter here (SEC S3): the core's daily_cap on reply counts attempts. The only
number this pool counts is LinkedIn HTTP attempts of the community path per UTC day, like the read
(50) and write (45) paths: it protects the member's LinkedIn quota and the other two paths, it says
nothing about how many replies go out. A refused call never waits and never locks.
"""
import asyncio
from collections import deque
from datetime import datetime, timezone
import json
import logging
import multiprocessing
import threading
import time
from . import kommentare as k
from .domain import ReportError, encode_response
from .oauth import TokenClient, Transport

LOG = logging.getLogger('linkedin_seite')

COMMUNITY_HTTP_PER_DAY = 60   # read 50 + write 45 + this 60 > 100 per member and day in Development Tier, see README
REPLY_ABSTAND = 60          # seconds between two send attempts
REPLY_SPERRE = 600          # seconds a comment stays locked after its send attempt
REPLY_WARTEND_MAX = 1       # calls waiting behind the running one; the core waits at most 120 s
REPLY_BUDGET = 25           # one reply job: 25 + 60 + 25 s stays below the template timeout of 120 s
LESEN_BUDGET = 30
TAGESLISTE_BUDGET = 60
LESEN_PRO_MINUTE = 10


def nicht_ausgefuehrt(status, code):
    exc = ReportError(status, code)
    exc.nicht_ausgefuehrt = True
    return exc


class PipeLink:
    """Job-process side: ask the server process before every request and before the send."""

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

    def write_request(self):
        self.ask('write')


def job_main(conn, cfg, req, adapter_factory=None):
    logging.disable(logging.CRITICAL)  # no library or request logging inside a job: comment text lives here
    from .community_adapter import CommunityAdapter
    factory = adapter_factory or CommunityAdapter
    link = PipeLink(conn)
    try:
        adapter = factory(cfg, TokenClient(link.ask), link)
        message = ('ok', encode_response(adapter.run(req)))
    except ReportError as exc:
        message = ('error', exc.status, exc.code)
    except Exception:
        message = ('error', 502, 'source_unavailable')
    try:
        conn.send(message)
    except (EOFError, BrokenPipeError, OSError):
        pass
    finally:
        conn.close()


class Job:
    def __init__(self, req, proc, conn, loop, budget):
        self.req, self.proc, self.conn, self.budget = req, proc, conn, budget
        self.future = loop.create_future()
        self.write_started = False  # IPC acknowledgement precedes the one possible provider write
        self.task = None

    @property
    def antwort(self):
        return isinstance(self.req, k.ReplyRequest)


class CommunityPool:
    def __init__(self, cfg, tokens, *, adapter_factory=None, today=None, transport_factory=Transport,
                 abstand=REPLY_ABSTAND, sperre=REPLY_SPERRE, monotonic=time.monotonic, reply_budget=REPLY_BUDGET,
                 lesen_budget=LESEN_BUDGET, tagesliste_budget=TAGESLISTE_BUDGET):
        self.cfg, self.tokens = cfg, tokens
        # Own transport: a renewal this path needed counts against this path's HTTP cap.
        self.transport = transport_factory(self._permit_http)
        self.factory = adapter_factory
        self.context = multiprocessing.get_context('spawn')
        self.today = today or (lambda: datetime.now(timezone.utc).date().isoformat())
        self.http = {}           # UTC day -> LinkedIn HTTP attempts of the community path
        self.counter_lock = threading.Lock()
        self.abstand, self.sperre, self.monotonic = abstand, sperre, monotonic
        self.budgets = {'antwort': reply_budget, 'lesen': lesen_budget, 'tagesliste': tagesliste_budget}
        self.locks = {}          # kommentar_urn -> monotonic time of its send attempt
        self.inflight = set()    # kommentar_urn of calls queued or running
        self.last_send = None
        self.reihe = asyncio.Lock()
        self.wartend = 0
        self.lesen = None
        self.lesen_rate = deque()
        self.jobs = set()
        self.closed = False

    def warning(self):
        return self.tokens.warning()

    def _permit_http(self):
        with self.counter_lock:
            day = self.today()
            for key in [key for key in self.http if key != day]:
                del self.http[key]
            if self.http.get(day, 0) >= COMMUNITY_HTTP_PER_DAY:
                raise ReportError(429, 'kommentare_http_tageslimit')
            self.http[day] = self.http.get(day, 0) + 1

    # --- entry points -------------------------------------------------------------------------

    async def run(self, req):
        if self.closed:
            raise nicht_ausgefuehrt(503, 'service_stopping')
        if isinstance(req, k.ReplyRequest):
            return await self._antwort(req)
        return await self._lesen(req)

    async def _lesen(self, req):
        started = time.monotonic()
        while self.lesen_rate and started - self.lesen_rate[0] >= 60:
            self.lesen_rate.popleft()
        if len(self.lesen_rate) >= LESEN_PRO_MINUTE:
            raise ReportError(429, 'lesen_rate_limited')
        if self.lesen is not None and not self.lesen.future.done():
            raise ReportError(429, 'lesen_belegt')
        self.lesen_rate.append(started)
        budget = self.budgets['tagesliste' if isinstance(req, k.TageslisteRequest) else 'lesen']
        self.lesen = job = self._start(req, budget)
        try:
            return await asyncio.shield(job.future)
        except asyncio.CancelledError:
            job.task.cancel()  # a read dies with its request
            raise

    async def _antwort(self, req):
        key = req.kommentar_urn
        now = self.monotonic()
        for urn in [urn for urn, sent in self.locks.items() if now - sent >= self.sperre]:
            del self.locks[urn]
        if key in self.inflight or key in self.locks:
            raise nicht_ausgefuehrt(429, 'gesperrt')
        if self.reihe.locked() and self.wartend >= REPLY_WARTEND_MAX:
            raise nicht_ausgefuehrt(429, 'antwort_warteschlange_voll')
        self.inflight.add(key)
        job = None
        try:
            self.wartend += 1
            try:
                await self.reihe.acquire()  # FIFO; a cancel while waiting sends nothing
            finally:
                self.wartend -= 1
            try:
                if self.last_send is not None:
                    wait = self.last_send + self.abstand - self.monotonic()
                    if wait > 0:
                        await asyncio.sleep(wait)
                if self.closed:
                    raise nicht_ausgefuehrt(503, 'service_stopping')
                job = self._start(req, self.budgets['antwort'])
            except BaseException:
                self.reihe.release()
                raise
        finally:
            if job is None:
                self.inflight.discard(key)
        # From here the job owns the queue and the in-flight mark: its supervisor releases both. A cancelled
        # request does not stop it, so a send attempt always completes its read back and its lock.
        return await asyncio.shield(job.future)

    # --- job lifecycle ------------------------------------------------------------------------

    def _start(self, req, budget):
        parent, child = self.context.Pipe()
        proc = self.context.Process(target=job_main, args=(child, self.cfg, req, self.factory), daemon=True)
        proc.start()
        child.close()
        job = Job(req, proc, parent, asyncio.get_running_loop(), budget)
        job.task = asyncio.create_task(self._supervise(job))
        self.jobs.add(job)
        return job

    async def _send_token(self, job):
        # In a thread: a due renewal is HTTP, and the server process must keep serving meanwhile.
        try:
            reply = ('token', await asyncio.to_thread(self.tokens.access, self.transport))
        except ReportError as exc:
            reply = ('error', exc.status, exc.code)
        except Exception:
            reply = ('error', 502, 'source_unavailable')
        job.conn.send(reply)

    def _reply(self, job, message):
        kind = message[0]
        if kind not in ('permit', 'write'):
            return message  # final message
        try:
            if kind == 'permit':
                self._permit_http()
            else:
                if not job.antwort:
                    raise ReportError(500, 'lesen_schreibt')  # a read job never reaches a write
                now = self.monotonic()
                job.write_started = True
                self.locks[job.req.kommentar_urn] = now
                self.last_send = now
            job.conn.send(('granted',))
        except ReportError as exc:
            job.conn.send(('error', exc.status, exc.code))
        return None

    async def _supervise(self, job):
        loop = asyncio.get_running_loop()
        deadline = loop.time() + job.budget
        try:
            outcome = None
            while outcome is None:
                while not job.conn.poll():
                    if not job.proc.is_alive() and not job.conn.poll():
                        raise ReportError(502, 'worker_failed')
                    if loop.time() > deadline:
                        raise ReportError(504, 'antwort_zeit_ueberschritten' if job.antwort else 'budget_exceeded')
                    await asyncio.sleep(0.01)
                message = job.conn.recv()
                if message[0] == 'token':
                    await self._send_token(job)
                    continue
                outcome = self._reply(job, message)
            if outcome[0] != 'ok':
                raise ReportError(outcome[1], outcome[2])
            self._trace(job, 'ok', json.loads(outcome[1]))
            job.future.set_result(outcome[1])
        except (EOFError, BrokenPipeError, OSError, IndexError, TypeError, ValueError):
            self._finish_error(job, ReportError(502, 'worker_failed'))
        except ReportError as exc:
            self._finish_error(job, exc)
        except asyncio.CancelledError:
            self._finish_error(job, ReportError(503, 'service_stopping'))
            raise
        finally:
            self._kill(job)
            self.jobs.discard(job)
            if job.antwort:
                self.inflight.discard(job.req.kommentar_urn)
                self.reihe.release()

    def _finish_error(self, job, exc):
        if job.antwort:
            exc.nicht_ausgefuehrt = not job.write_started
        self._trace(job, exc.code, {})
        if not job.future.done():
            job.future.set_exception(exc)
            job.future.exception()  # consumed here; run() re-raises it if somebody waits

    def _trace(self, job, code, payload):
        """One line per finished reply job: URNs, code and anlass only. Never text, never a person."""
        if not job.antwort:
            return
        antwort = payload.get('antwort_urn')
        if not (isinstance(antwort, str) and k.KOMMENTAR_URN.fullmatch(antwort)):
            antwort = '-'
        anlass = job.req.anlass if job.req.anlass in k.ANLAESSE else '-'
        LOG.info('spur kommentar=%s post=%s code=%s anlass=%s antwort=%s doppelt=%d', job.req.kommentar_urn,
                 job.req.post_urn, code, anlass, antwort, 1 if payload.get('doppelt') is True else 0)

    def _kill(self, job):
        try:
            job.conn.close()
        except OSError:
            pass
        if job.proc.is_alive():
            job.proc.kill()
        job.proc.join(timeout=0.5)

    async def close(self):
        self.closed = True
        for job in list(self.jobs):
            if job.task is not None and not job.task.done():
                job.task.cancel()
                await asyncio.gather(job.task, return_exceptions=True)
