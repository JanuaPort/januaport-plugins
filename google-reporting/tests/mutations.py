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
        "bridge-key-auth",
        "server.py",
        "if not authorized:",
        "if False:",
        "test_boundary.HTTP.test_wrong_or_missing_bridge_key_is_rejected_for_both_sources",
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
    # JanuaPort/januaport#1148: scope separation, cap, SEC P1-P4, H1-H3.
    (
        "scope-combined-in-one-credential",
        "google_adapter.py",
        "info, scopes=[scope], always_use_jwt_access=False",
        "info, scopes=[scope, WRITE_SCOPES['gsc']], always_use_jwt_access=False",
        "test_manage_adapter.ScopePins.test_scope_set_per_operation",
    ),
    (
        "read-uses-write-scope",
        "google_adapter.py",
        '"v1alpha", SCOPES["ga4"])',
        '"v1alpha", WRITE_SCOPES["ga4"])',
        "test_manage_adapter.ScopePins.test_reads_never_carry_a_write_scope_on_the_wire",
    ),
    (
        "write-cap",
        "workers.py",
        ">= WRITE_CAP_PER_DAY",
        ">= 10**9",
        "test_manage_http.DailyCap.test_cap_is_twenty_per_source_and_day_and_the_21st_has_a_fixed_code",
    ),
    (
        "preview-counts-against-cap",
        "workers.py",
        'if getattr(req, "write", False) and not req.dry_run:',
        'if getattr(req, "write", False):',
        "test_manage_http.DailyCap.test_preview_does_not_count",
    ),
    (
        "channel-group-primary",
        "manage.py",
        '"primary": False,',
        '"primary": True,',
        "test_manage_domain.ChannelGroup.test_create_body_is_never_primary_and_has_the_configured_name",
    ),
    (
        "foreign-channel-group-patched",
        "manage_adapter.py",
        'raise ReportError(409, "channel_group_not_managed")',
        "pass",
        "test_manage_adapter.ChannelGroups.test_update_of_a_foreign_or_system_group_is_refused_before_any_write",
    ),
    (
        "channel-group-patch-mask",
        "manage_adapter.py",
        'updateMask="grouping_rule"',
        'updateMask="*"',
        "test_manage_adapter.ChannelGroups.test_update_patches_only_the_rules_and_never_primary",
    ),
    (
        "ai-channel-behind-referral",
        "manage.py",
        "return [ai] + standard",
        "return standard + [ai]",
        "test_manage_domain.ChannelGroup.test_rules_carry_the_standard_channels_and_ai_before_referral",
    ),
    (
        "privacy-scan-bypassed",
        "manage.py",
        "if personal_data(value):",
        "if False:",
        "test_manage_domain.Annotation.test_personal_data_is_rejected_with_a_fixed_code_and_no_echo",
    ),
    (
        "managed-prefix-dropped",
        "manage.py",
        'params["title"] = MANAGED_PREFIX + title',
        'params["title"] = title',
        "test_manage_domain.Annotation.test_title_gets_the_managed_prefix_and_defaults",
    ),
    (
        "human-annotation-patched",
        "manage_adapter.py",
        'raise ReportError(409, "annotation_not_managed")',
        "pass",
        "test_manage_adapter.Annotations.test_update_of_a_human_or_system_annotation_is_refused_before_any_write",
    ),
    (
        "sitemap-foreign-host",
        "manage.py",
        "if host not in cfg.hosts or not public_path(path):",
        "if not public_path(path):",
        "test_manage_domain.SitemapUrl.test_invalid_urls_are_rejected_without_echo",
    ),
    (
        "dry-run-submits",
        "manage_adapter.py",
        "if not req.dry_run:\n            self.adapter.gsc_write()",
        "if True:\n            self.adapter.gsc_write()",
        "test_manage_adapter.Sitemaps.test_dry_run_never_calls_put",
    ),
    (
        "change-line-prints-free-text",
        "manage.py",
        "if field in FREE_TEXT_FIELDS:",
        "if False:",
        "test_manage_domain.ChangeLine.test_free_text_appears_as_length_and_hash_only",
    ),
    (
        "change-line-for-previews",
        "server.py",
        'if result.get("state") == "applied":',
        "if True:",
        "test_manage_http.Routes.test_preview_and_reads_write_no_change_line",
    ),
    (
        "unknown-v1alpha-field-tolerated",
        "manage.py",
        "if not isinstance(data, dict) or (strict and set(data) - ANNOTATION_KEYS):",
        "if not isinstance(data, dict):",
        "test_manage_adapter.Annotations.test_unknown_v1alpha_field_fails_closed_before_the_write",
    ),
    # JanuaPort/januaport#1150: only the asked apiNames decide in checkCompatibility.
    (
        "compatibility-checks-foreign-entries",
        "google_adapter.py",
        "        if name in requested:\n            states.setdefault(name, []).append(state)",
        "        states.setdefault(name, []).append(state)",
        "test_google_adapter.Compatibilities.test_table",
    ),
    (
        "compatibility-missing-name-tolerated",
        "google_adapter.py",
        "    if any(name not in states for name in requested):\n        raise ReportError(503, \"report_definition_unverified\")\n",
        "",
        "test_google_adapter.Compatibilities.test_table",
    ),
    (
        "compatibility-length-check-back",
        "google_adapter.py",
        "    states = {}\n",
        "    if len(answered) != len(requested):\n        raise ReportError(503, \"report_definition_unverified\")\n    states = {}\n",
        "test_google_adapter.Adapter.test_status_is_ready_on_a_whole_property_answer_actual_rest",
    ),
    (
        "late-end-date-accepted",
        "domain.py",
        "        if end > latest:",
        "        if False:",
        "test_contract.Contract.test_end_date_after_the_last_final_day_has_its_own_fixed_code",
    ),
    (
        "late-end-date-code-generic",
        "domain.py",
        '"end_date_not_available"',
        '"invalid_input"',
        "test_boundary.HTTP.test_late_end_date_reports_a_fixed_code_without_echo",
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
