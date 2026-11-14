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
        "    if day > latest:",
        "    if False:",
        "test_contract.Contract.test_end_date_after_the_last_final_day_has_its_own_fixed_code",
    ),
    (
        "late-end-date-code-generic",
        "domain.py",
        '"end_date_not_available"',
        '"invalid_input"',
        "test_boundary.HTTP.test_late_end_date_reports_a_fixed_code_without_echo",
    ),
    # JanuaPort/januaport#1379: several sites, the header names the site, nothing falls back.
    (
        "site-falls-back-to-the-first",
        "domain.py",
        "cfg = self.by_name.get(name) if SITE_NAME.fullmatch(name) else None",
        "cfg = self.by_name.get(name) or next(iter(self.by_name.values()))",
        "test_sites.Resolution.test_table",
    ),
    (
        "list-form-with-one-site-needs-no-header",
        "domain.py",
        "            if not self.listed:",
        "            if not self.listed or len(self.by_name) == 1:",
        "test_sites.Resolution.test_table",
    ),
    (
        "site-header-lowercased",
        "domain.py",
        'name = values[0] if len(values) == 1 else ""',
        'name = values[0].lower() if len(values) == 1 else ""',
        "test_sites.Resolution.test_table",
    ),
    (
        "site-header-stripped",
        "domain.py",
        'name = values[0] if len(values) == 1 else ""',
        'name = values[0].strip() if len(values) == 1 else ""',
        "test_sites.Resolution.test_table",
    ),
    (
        "adapter-uses-the-first-site-config",
        "google_adapter.py",
        "return self.sites.config(req.site)",
        "return next(iter(self.sites.by_name.values()))",
        "test_site_adapter.Separation.test_no_google_call_of_terminagent_carries_a_value_of_januaport",
    ),
    (
        "write-cap-key-without-site",
        "workers.py",
        "key = (req.site, req.source, day)",
        "key = (req.source, day)",
        "test_site_http.Cap.test_the_cap_is_per_source_and_site_and_unresolved_sites_count_nowhere",
    ),
    (
        "count-before-the-site-is-resolved",
        "server.py",
        "            cfg = sites.resolve(request.headers.getall(SITE_HEADER, []))",
        "            first = (next(iter(sites.by_name)), source, pool.today())\n"
        "            pool.write_attempts[first] = pool.write_attempts.get(first, 0) + (kind == 'write')\n"
        "            cfg = sites.resolve(request.headers.getall(SITE_HEADER, []))",
        "test_site_http.Cap.test_the_cap_is_per_source_and_site_and_unresolved_sites_count_nowhere",
    ),
    (
        "site-name-in-the-error",
        "server.py",
        'return web.json_response({"error": code}, status=exc.status)',
        'return web.json_response({"error": code + ":" + request.headers.get(SITE_HEADER, "")}, status=exc.status)',
        "test_site_http.Matrix.test_the_error_never_echoes_the_header_value",
    ),
    # JanuaPort/januaport#1379 part B: free periods, page_compare, named segments.
    (
        "missing-page-shown-as-zero",
        "domain.py",
        "    empty = dict.fromkeys(COMPARE_FIELDS)\n",
        "    empty = dict.fromkeys(COMPARE_FIELDS, 0)\n",
        "test_page_compare.Projection.test_page_only_in_a_has_null_b_and_null_delta_not_zero",
    ),
    (
        "ctr-computed-from-clicks-and-impressions",
        "domain.py",
        "        item = {k: number(row[k]) for k in COMPARE_FIELDS}\n",
        "        item = {k: number(row[k]) for k in COMPARE_FIELDS}\n"
        '        item["ctr"] = item["clicks"] / item["impressions"] if item["impressions"] else 0\n',
        "test_page_compare.Projection.test_ctr_and_position_come_from_the_source_row_and_are_not_computed",
    ),
    (
        "b-overlapping-a-accepted",
        "domain.py",
        "            require(compare_end < start)\n",
        "            require(True)\n",
        "test_page_compare.Ranges.test_b_that_touches_a_is_invalid_input",
    ),
    (
        "period-91-accepted",
        "domain.py",
        "MAX_PERIOD = 90",
        "MAX_PERIOD = 99",
        "test_periods.Periods.test_invalid_periods_are_invalid_input_for_every_report",
    ),
    (
        "gsc-distance-two-days",
        "domain.py",
        ").date() - timedelta(days=3)",
        ").date() - timedelta(days=2)",
        "test_periods.LatestDay.test_exactly_today_minus_three_is_accepted_and_minus_two_is_not",
    ),
    (
        "unknown-segment-falls-back-to-all-public",
        "domain.py",
        "        require(\n"
        "            segment == ALL_PUBLIC\n"
        "            or (bool(SEGMENT_NAME.fullmatch(segment)) and segment in cfg.segments)\n"
        "        )\n",
        "        require(bool(SEGMENT_NAME.fullmatch(segment)) or segment == ALL_PUBLIC)\n"
        "        if segment not in cfg.segments:\n"
        "            segment = ALL_PUBLIC\n",
        "test_segments.Parameter.test_unknown_and_foreign_segments_are_invalid_input_never_all_public",
    ),
    (
        "nine-segments-accepted",
        "domain.py",
        "1 <= len(cfg.segments) <= MAX_SEGMENTS",
        "1 <= len(cfg.segments) <= 99",
        "test_segments.Configuration.test_one_to_eight_segments_load_and_zero_or_nine_do_not",
    ),
    (
        "duplicate-config-keys-accepted",
        "domain.py",
        "    if len(set(keys)) != len(keys):\n",
        "    if False:\n",
        "test_segments.DuplicateNames.test_a_duplicated_segment_name_in_the_file_is_invalid_configuration",
    ),
    (
        "comparison-counted-once",
        "workers.py",
        '        calls = getattr(req, "source_calls", 1)\n',
        "        calls = 1\n",
        "test_page_compare.PoolCost.test_a_comparison_counts_twice_and_the_16th_is_limited",
    ),
    (
        "second-call-asks-period-a-again",
        "google_adapter.py",
        "build_query(cfg, req, compare=True)",
        "build_query(cfg, req)",
        "test_page_compare.AdapterCalls.test_two_searchanalytics_calls_one_per_period_with_the_page_filter",
    ),
    (
        "comparison-query-limited-to-the-output-limit",
        "domain.py",
        "            else len(urls)\n",
        "            else req.limit\n",
        "test_page_compare.Queries.test_golden_both_bodies",
    ),
    (
        "delta-sign-flipped",
        "domain.py",
        "round(first[k] - second[k], 10)",
        "round(second[k] - first[k], 10)",
        "test_page_compare.Projection.test_page_in_both_periods_has_values_and_delta",
    ),
    (
        "ambiguous-path-keeps-one-row",
        "domain.py",
        "            found.pop(matching[0], None)\n",
        "            found[matching[0]] = item\n",
        "test_page_compare.Projection.test_two_rows_of_one_path_in_a_period_are_not_averaged",
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
