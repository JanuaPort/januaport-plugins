"""Critical control probes: each temporary source mutation must fail a real test."""

import os
from pathlib import Path
import shutil
import subprocess
import sys
import time
import tempfile

ROOT = Path(__file__).resolve().parents[1]
MUTANTS = [
    (
        "source-key-auth",
        "server.py",
        "if not authorized:",
        "if False:",
        "test_boundary.HTTP.test_source_keys_are_not_interchangeable",
    ),
    (
        "unknown-input",
        "domain.py",
        "k not in allowed or k in args",
        "k in args",
        "test_boundary.HTTP.test_negative_inputs_never_reach_worker",
    ),
    (
        "gsc-anchor",
        "domain.py",
        'return "^(" +',
        'return "(" +',
        "test_contract.Contract.test_fixed_query_and_escaped_allowlist",
    ),
    (
        "token-retry",
        "google_adapter.py",
        "can_retry=False",
        "can_retry=True",
        "test_google_adapter.Adapter.test_429_401_and_token_failure_have_no_retries_or_raw_errors",
    ),
    (
        "actual-work-kill",
        "workers.py",
        "proc.kill()",
        "pass  # mutant: work continues",
        "test_boundary.Workers.test_timeout_kills_work_and_releases_capacity",
    ),
    (
        "response-cap",
        "domain.py",
        "if len(data) > MAX_RESPONSE:",
        "if False:",
        "test_boundary.HTTP.test_output_size_cap",
    ),
    (
        "parser-rejection-redaction",
        "safe_http.py",
        '{"error": code}',
        '{"error": message or code}',
        "test_boundary.HTTP.test_parser_rejections_never_echo_raw_input",
    ),
]


def main():
    start = time.monotonic()
    baseline = subprocess.run(
        [sys.executable, "-m", "unittest", "discover", "-s", "tests"],
        cwd=ROOT,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
    )
    if baseline.returncode:
        raise SystemExit("baseline failed; mutation results would be invalid")
    print("baseline: PASS", flush=True)
    failed = []
    for name, module, before, after, test in MUTANTS:
        with tempfile.TemporaryDirectory(prefix=".mutation-", dir=ROOT) as temporary:
            probe = Path(temporary)
            for directory in ("google_reporting", "tests"):
                shutil.copytree(
                    ROOT / directory,
                    probe / directory,
                    ignore=shutil.ignore_patterns("__pycache__"),
                )
            path = probe / "google_reporting" / module
            original = path.read_text()
            if original.count(before) != 1:
                raise RuntimeError("mutation anchor changed: " + name)
            path.write_text(original.replace(before, after, 1))
            env = dict(
                os.environ,
                PYTHONPATH=str(probe) + os.pathsep + str(probe / "tests"),
                PYTHONDONTWRITEBYTECODE="1",
            )
            try:
                run = subprocess.run(
                    [sys.executable, "-m", "unittest", test],
                    cwd=probe,
                    env=env,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.STDOUT,
                    timeout=20,
                )
                detected = run.returncode != 0
            except subprocess.TimeoutExpired:
                detected = True
            print(name + ": " + ("DETECTED" if detected else "SURVIVED"), flush=True)
            if not detected:
                failed.append(name)
    print("duration_seconds=" + str(round(time.monotonic() - start, 3)), flush=True)
    if failed:
        raise SystemExit("surviving mutations: " + ",".join(failed))


if __name__ == "__main__":
    main()
