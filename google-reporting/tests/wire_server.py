"""Optional cross-repo wire probe. Synthetic identity-free real HTTP/worker service."""

import argparse
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import asyncio
from google_reporting.manage import ManageRequest, envelope
from google_reporting.safe_http import serve_app
from google_reporting.domain import Config, project
from google_reporting.server import create_app
from google_reporting.workers import WorkerPool
from test_contract import fixture


class SyntheticAdapter:
    def __init__(self, cfg, path):
        self.cfg = cfg

    def run(self, req):
        if isinstance(req, ManageRequest):
            # Synthetic answers in the real envelope; no Google call and no state.
            if not req.write:
                return envelope(req.source, req.operation, "no_rows", items=[], quality={"truncated": False, "returned_items": 0})
            return envelope(
                req.source,
                req.operation,
                "preview" if req.dry_run else "applied",
                dry_run=req.dry_run,
                target=None,
                changed_fields=[],
                before=None,
                after={},
            )
        if req.source == "gsc":
            key = (
                req.end_date
                if req.report == "performance_overview"
                else "synthetic public query"
                if req.report == "query_summary"
                else self.cfg.gsc_urls[req.paths[0]][0]
            )
            raw = {
                "rows": [
                    {
                        "keys": [key],
                        "clicks": 2,
                        "impressions": 20,
                        "ctr": 0.1,
                        "position": 3,
                    }
                ]
            }
        else:
            value = req.paths[0] if req.report == "landing_pages" else "Organic Search"
            raw = {
                "rows": [
                    {
                        "dimensionValues": [{"value": value}],
                        "metricValues": [{"value": "20"}, {"value": "15"}],
                    }
                ],
                "metadata": {"timeZone": "Europe/Berlin"},
            }
        return project(self.cfg, req, raw)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, default=18092)
    args = parser.parse_args()
    raw = fixture()
    raw["query_enabled"] = True
    cfg = Config.from_dict(raw)
    pool = WorkerPool(cfg, "unused", adapter_factory=SyntheticAdapter)
    app = create_app(
        cfg,
        b"synthetic-google-reporting-bridge-contract-no-real-key",
        pool,
    )
    asyncio.run(serve_app(app, "127.0.0.1", args.port))


if __name__ == "__main__":
    main()
