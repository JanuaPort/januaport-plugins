"""Critical control probes: each temporary source mutation must fail a real test."""

import os
from pathlib import Path
import shutil
import subprocess
import sys
import time
import tempfile

ROOT = Path(__file__).resolve().parents[1]
H = "test_http."
A = "test_adapter."
D = "test_domain."
MUTANTS = [
    # Boundary: Bridge auth, strict input before any vendor call (SEC A2).
    (
        "bridge-key-auth",
        "server.py",
        "if not authorized:",
        "if False:",
        H + "Boundary.test_wrong_or_missing_bridge_key_is_rejected_on_every_route",
    ),
    (
        "url-host-not-checked",
        "domain.py",
        'if host not in cfg.hosts or "//" in path or PERCENT.search(path):',
        'if "//" in path or PERCENT.search(path):',
        D + "UrlValidation.test_rejects_everything_else_with_a_fixed_code_and_no_echo",
    ),
    (
        "url-host-suffix-match",
        "domain.py",
        "if host not in cfg.hosts or",
        "if not any(host.endswith(h) for h in cfg.hosts) or",
        D + "UrlValidation.test_rejects_everything_else_with_a_fixed_code_and_no_echo",
    ),
    (
        "url-info-without-host-check",
        "domain.py",
        "return Request(source, operation, url=validate_url(cfg, items[0][1]))",
        "return Request(source, operation, url=items[0][1])",
        H + "ReadRoutes.test_url_info_host_check_runs_before_the_vendor_call",
    ),
    (
        "sitemap-any-file",
        "domain.py",
        'if sitemap and not (path.startswith("/") and path.endswith(".xml") and len(path) > len("/.xml")):',
        "if False:",
        D + "UrlValidation.test_sitemap_must_be_an_xml_file",
    ),
    (
        "indexnow-foreign-host-of-key-file",
        "domain.py",
        "if any(split_url(u)[0] != cfg.indexnow.host for u in urls):",
        "if False:",
        D + "BodyParsing.test_indexnow_urls_must_share_the_host_of_the_key_file",
    ),
    (
        "unknown-body-key",
        "domain.py",
        'if set(body) - {"urls", "dry_run"} or "urls" not in body:',
        'if "urls" not in body:',
        D + "BodyParsing.test_rejections_before_any_vendor_call",
    ),
    (
        "duplicate-json-key",
        "domain.py",
        "        if key in out:",
        "        if False:",
        D + "BodyParsing.test_rejections_before_any_vendor_call",
    ),
    (
        "batch-above-hundred",
        "domain.py",
        "if len(parts) > MAX_URLS:",
        "if len(parts) > 10**6:",
        D + "BodyParsing.test_at_most_one_hundred_urls",
    ),
    (
        "response-cap",
        "domain.py",
        "if len(data) > MAX_RESPONSE:",
        "if False:",
        D + "Projections.test_envelope_and_size_cap",
    ),
    # Positive field lists (SEC A2): extra vendor fields must not pass.
    (
        "url-info-extra-vendor-field",
        "domain.py",
        '"is_page": _flag(data["IsPage"]),',
        '"is_page": _flag(data["IsPage"]), "extra": data.get("Url"),',
        D + "Projections.test_url_info_keeps_only_the_positive_list_and_our_own_url",
    ),
    (
        "feed-extra-vendor-field",
        "domain.py",
        '"compressed": _flag(data["Compressed"]),',
        '"compressed": _flag(data["Compressed"]), "extra": data.get("SomethingNew"),',
        D + "Projections.test_feeds_positive_list",
    ),
    (
        "epoch-date-shown-as-date",
        "domain.py",
        "if millis <= 0:",
        "if millis < 0:",
        D + "BingDates.test_table",
    ),
    # The Bing key never leaves (SEC A1).
    (
        "exception-text-in-code",
        "adapter.py",
        '        except Exception:  # noqa: BLE001 - text of any transport exception can embed the key\n            failure = _failure(502, "source_unavailable")',
        '        except Exception as exc:\n            failure = _failure(502, "source_unavailable " + str(exc))',
        A + "KeyNeverInExceptions.test_every_transport_exception_becomes_a_fixed_code_without_chain",
    ),
    (
        "exception-chain-kept",
        "adapter.py",
        '        except Exception:  # noqa: BLE001 - text of any transport exception can embed the key\n            failure = _failure(502, "source_unavailable")',
        '        except Exception:\n            raise _failure(502, "source_unavailable")',
        A + "KeyNeverInExceptions.test_every_transport_exception_becomes_a_fixed_code_without_chain",
    ),
    (
        "exception-text-logged",
        "server.py",
        '        except Exception:\n            # Never the exception text: transport errors embed the URL with the key.\n            code = "internal_error"',
        '        except Exception as exc:\n            code = "internal_error " + str(exc)',
        H + "KeyCanary.test_library_loggers_are_quiet_and_exceptions_are_swallowed_into_codes",
    ),
    (
        "vendor-message-passed-through",
        "adapter.py",
        'return _failure(502, "bing_api_key_invalid")',
        'return _failure(502, "bing_api_key_invalid " + fault.get("Message", ""))',
        A + "ErrorCodes.test_bing_http_error_table",
    ),
    (
        "redirects-followed",
        "adapter.py",
        '"allow_redirects": False',
        '"allow_redirects": True',
        A + "ErrorCodes.test_redirects_are_not_followed",
    ),
    (
        "proxy-from-environment",
        "adapter.py",
        "trust_env=False,",
        "trust_env=True,",
        A + "Transport.test_proxy_environment_is_ignored",
    ),
    (
        "library-loggers-loud",
        "server.py",
        "logging.getLogger(name).setLevel(logging.CRITICAL)",
        "logging.getLogger(name).setLevel(logging.DEBUG)",
        H + "KeyCanary.test_library_loggers_are_quiet_and_exceptions_are_swallowed_into_codes",
    ),
    (
        "bing-key-sent-to-indexnow",
        "adapter.py",
        'body = {"host": settings.host,',
        'body = {"apikey": self._key, "host": settings.host,',
        A + "Writes.test_indexnow_wire_body_and_no_bing_key",
    ),
    (
        "keys-may-be-equal",
        "server.py",
        "if bridge == bing:",
        "if False:",
        "test_runtime_files.RuntimeFiles.test_the_two_keys_must_differ",
    ),
    # Write rules: dry_run, cap, quota, not_configured.
    (
        "write-cap",
        "server.py",
        "if self.attempts.get(key, 0) >= self.limit:",
        "if self.attempts.get(key, 0) >= 10**9:",
        H + "WriteRoutes.test_daily_cap_is_twenty_per_route_and_the_21st_has_a_fixed_code",
    ),
    (
        "cap-shared-between-routes",
        "server.py",
        'cap.count(req.source + "/" + req.operation)',
        'cap.count("all")',
        H + "WriteRoutes.test_daily_cap_is_twenty_per_route_and_the_21st_has_a_fixed_code",
    ),
    (
        "preview-counts-against-cap",
        "server.py",
        '            if req.dry_run:\n                return encode_response(write_envelope(req, "preview"))\n            cap.count(req.source + "/" + req.operation)',
        '            cap.count(req.source + "/" + req.operation)\n            if req.dry_run:\n                return encode_response(write_envelope(req, "preview"))',
        H + "WriteRoutes.test_dry_run_validates_but_never_calls_and_never_counts",
    ),
    (
        "dry-run-calls-the-vendor",
        "server.py",
        "            if req.dry_run:\n",
        "            if False:\n",
        H + "WriteRoutes.test_dry_run_validates_but_never_calls_and_never_counts",
    ),
    (
        "quota-ignored",
        "adapter.py",
        "if available < len(req.urls):",
        "if False:",
        A + "Writes.test_never_submits_above_the_reported_quota",
    ),
    (
        "write-answer-not-checked",
        "adapter.py",
        'if await self._bing("POST", name, body=body) != {"d": None}:',
        'if await self._bing("POST", name, body=body) is None:',
        A + "Writes.test_a_write_answer_other_than_null_is_a_shape_error",
    ),
    (
        "indexnow-unconfigured-not-refused",
        "server.py",
        'if req.source == "indexnow" and cfg.indexnow is None:',
        "if False:",
        H + "UnconfiguredIndexNow.test_not_configured_makes_no_call_and_counts_nothing",
    ),
    (
        "budget-not-enforced",
        "server.py",
        "async with asyncio.timeout(budget):",
        "async with asyncio.timeout(None):",
        H + "ReadRoutes.test_budget_is_enforced",
    ),
    (
        "parser-rejection-redaction",
        "safe_http.py",
        '{"error": code}',
        '{"error": message or code}',
        H + "Boundary.test_parser_rejections_never_echo_raw_input",
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
            for directory in ("bing_webmaster", "tests"):
                shutil.copytree(
                    ROOT / directory,
                    probe / directory,
                    ignore=shutil.ignore_patterns("__pycache__"),
                )
            path = probe / "bing_webmaster" / module
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
                    timeout=60,
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
