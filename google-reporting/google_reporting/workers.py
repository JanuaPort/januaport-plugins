"""Two persistent isolated workers; cancellation terminates actual token/API work."""

import asyncio
from collections import deque
import multiprocessing
import logging
import time
from .domain import ReportError, encode_response

BUDGET_SECONDS = 20
WORKERS = 2
RATE_PER_MINUTE = 30
MAX_QUEUED = 4


def worker_main(conn, cfg, identity_path, adapter_factory=None):
    logging.disable(logging.CRITICAL)
    # No Python logging or inherited request/server objects in a spawned worker.
    if adapter_factory is None:
        from .google_adapter import GoogleAdapter

        adapter_factory = GoogleAdapter
    adapter = adapter_factory(cfg, identity_path)
    try:
        while True:
            req = conn.recv()
            try:
                payload = encode_response(adapter.run(req))
                conn.send(("ok", payload))
            except ReportError as exc:
                conn.send(("error", exc.status, exc.code))
            except Exception:
                conn.send(("error", 502, "source_unavailable"))
    except (EOFError, BrokenPipeError):
        pass
    finally:
        conn.close()


class WorkerPool:
    def __init__(
        self, cfg, identity_path, *, budget=BUDGET_SECONDS, adapter_factory=None
    ):
        self.cfg, self.identity_path = cfg, identity_path
        self.budget = budget
        self.factory = adapter_factory
        self.context = multiprocessing.get_context("spawn")
        self.slots = [None] * WORKERS
        self.available = asyncio.Queue(maxsize=WORKERS)
        for i in range(WORKERS):
            self.available.put_nowait(i)
        self.rate = {"gsc": deque(), "ga4": deque()}
        self.pending = 0
        self.closed = False

    def _start(self, index):
        parent, child = self.context.Pipe()
        proc = self.context.Process(
            target=worker_main,
            args=(child, self.cfg, self.identity_path, self.factory),
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
        ticks = self.rate[req.source]
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
                while not conn.poll():
                    if not proc.is_alive():
                        raise ReportError(502, "worker_failed")
                    await asyncio.sleep(0.01)
                result = conn.recv()
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
