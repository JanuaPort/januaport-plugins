"""Two persistent isolated workers; cancellation terminates actual token/API work."""

import asyncio
from collections import deque
import multiprocessing
import logging
from datetime import datetime, timezone
import time
from .domain import ReportError, encode_response

BUDGET_SECONDS = 20
WORKERS = 2
RATE_PER_MINUTE = 30
MAX_QUEUED = 4
DAILY_CAP = 50


def worker_main(conn, cfg, identity, adapter_factory=None):
    logging.disable(logging.CRITICAL)
    # No Python logging or inherited request/server objects in a spawned worker.
    if adapter_factory is None:
        from .linkedin_adapter import LinkedInAdapter

        adapter_factory = LinkedInAdapter
    def permit():
        conn.send(("permit",))
        reply = conn.recv()
        if reply[0] == "error":
            raise ReportError(reply[1], reply[2])
    adapter = adapter_factory(cfg, identity, permit)
    try:
        while True:
            req = conn.recv()
            try:
                payload = encode_response(adapter.run(req))
                result = ("ok", payload)
            except ReportError as exc:
                result = ("error", exc.status, exc.code)
            except Exception:
                result = ("error", 502, "source_unavailable")
            # Only expiry metadata crosses back; refreshed access/refresh tokens
            # stay in the worker. Shortened provider expiry advances the warning.
            expiry = adapter.tokens.data.get('refresh_expires_at')
            if expiry:
                conn.send(("expiry", expiry))
            conn.send(result)
    except (EOFError, BrokenPipeError):
        pass
    finally:
        conn.close()


class WorkerPool:
    def __init__(
        self,
        cfg,
        identity,
        *,
        budget=BUDGET_SECONDS,
        adapter_factory=None,
        today=None,
    ):
        self.cfg, self.identity = cfg, identity
        self.budget = budget
        self.factory = adapter_factory
        self.context = multiprocessing.get_context("spawn")
        self.slots = [None] * WORKERS
        self.available = asyncio.Queue(maxsize=WORKERS)
        for i in range(WORKERS):
            self.available.put_nowait(i)
        self.rate = deque()
        self.pending = 0
        self.closed = False
        # Count each outbound HTTP attempt in the parent, never in disposable workers.
        self.attempts = {}
        self.today = today or (lambda: datetime.now(timezone.utc).date().isoformat())

    def warning(self):
        from .oauth import warning
        return warning(self.identity)

    def _permit(self):
        day = self.today()
        self.attempts = {k: v for k, v in self.attempts.items() if k == day}
        if self.attempts.get(day, 0) >= DAILY_CAP:
            raise ReportError(429, "daily_cap_reached")
        self.attempts[day] = self.attempts.get(day, 0) + 1

    def _start(self, index):
        parent, child = self.context.Pipe()
        proc = self.context.Process(
            target=worker_main,
            args=(child, self.cfg, self.identity, self.factory),
            daemon=True,
        )
        proc.start()
        child.close()
        self.slots[index] = (proc, parent)
        return proc, parent

    def _kill(self, index):
        slot = self.slots[index]
        if slot is None:
            return
        proc, conn = slot
        conn.close()
        if proc.is_alive():
            proc.kill()
        proc.join(timeout=0.25)
        # SIGKILL cannot be ignored; a stuck OS task stays assigned and makes
        # the pool unavailable rather than creating unlimited replacements.
        if proc.is_alive():
            raise ReportError(503, "worker_unavailable")
        proc.close()
        self.slots[index] = None

    async def run(self, req):
        started = time.monotonic()
        if self.closed:
            raise ReportError(503, "service_stopping")
        ticks = self.rate
        while ticks and started - ticks[0] >= 60:
            ticks.popleft()
        if len(ticks) >= RATE_PER_MINUTE:
            raise ReportError(429, "source_rate_limited")
        if self.pending >= WORKERS + MAX_QUEUED:
            raise ReportError(429, "queue_full")
        ticks.append(started)
        self.pending += 1
        index = None
        try:
            async with asyncio.timeout(self.budget):
                index = await self.available.get()
                proc, conn = self.slots[index] or self._start(index)
                conn.send(req)
                while True:
                    while not conn.poll():
                        if not proc.is_alive():
                            raise ReportError(502, "worker_failed")
                        await asyncio.sleep(0.01)
                    message = conn.recv()
                    if message[0] == "expiry":
                        from .oauth import timestamp
                        current = self.identity.get('refresh_expires_at')
                        if current and timestamp(message[1]) < timestamp(current):
                            self.identity['refresh_expires_at'] = message[1]
                        continue
                    if message[0] != "permit":
                        result = message
                        break
                    try:
                        self._permit()
                        conn.send(("granted",))
                    except ReportError as exc:
                        conn.send(("error", exc.status, exc.code))
                if result[0] == "error":
                    raise ReportError(result[1], result[2])
                return result[1]
        except TimeoutError:
            raise ReportError(504, "budget_exceeded") from None
        except (EOFError, BrokenPipeError, OSError):
            raise ReportError(502, "worker_failed") from None
        finally:
            self.pending -= 1
            if index is not None:
                # An in-flight request after task cancellation/timeout must die.
                # Healthy completed workers preserve RAM token/client caches.
                if "result" not in locals():
                    self._kill(index)
                self.available.put_nowait(index)

    async def close(self):
        self.closed = True
        for i in range(WORKERS):
            self._kill(i)
