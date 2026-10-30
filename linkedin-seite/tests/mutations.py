"""Like google-reporting: baseline green, temporary isolated removal, real test red.

Syntax/import errors and timeouts are not evidence of a detected control mutation.
Original source files are never edited; each probe directory is removed on exit.
"""
import ast
import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]
A = 'test_adapter.Adapter.'
B = 'test_boundary.Boundary.'
W = 'test_boundary.Workers.'
C = 'test_consent.Consent.'
D = 'test_contract.Contract.'
N = 'test_acceptance.Manufacturer.'
H = 'test_acceptance.DualLoopback.'
S = 'test_sec_tightening.StrictSource.'
F = 'test_sec_tightening.FirstRequest.'
MUTANTS = [
    ('bridge-key', 'server.py', 'if not authorized:', 'if False:', B+'test_bridge_missing_wrong_duplicate_never_reach_adapter'),
    ('duplicate-auth', 'server.py', 'if len(header) != 1:', 'if not header:', B+'test_bridge_missing_wrong_duplicate_never_reach_adapter'),
    ('get-only', 'server.py', 'if request.method != "GET":\n                raise ReportError(405, "method_not_allowed")\n            header', 'if False:\n                raise ReportError(405, "method_not_allowed")\n            header', B+'test_no_write_method_no_generic_or_person_routes'),
    ('get-body', 'server.py', 'if request.can_read_body:', 'if False:', B+'test_body_rejected_and_local_health_does_not_use_api'),
    ('private-bind', 'server.py', 'if ip.version != 4 or not ip.is_private or ip.is_unspecified or ip.is_loopback:', 'if False:', D+'test_bind_rejects_wildcard_loopback_ipv6_and_public'),
    ('unknown-org-input', 'domain.py', 'k not in allowed or k in args', 'k in args', B+'test_org_paths_parameters_duplicates_dates_rejected'),
    ('duplicate-input', 'domain.py', 'k not in allowed or k in args', 'k not in allowed', B+'test_org_paths_parameters_duplicates_dates_rejected'),
    ('period-cap', 'domain.py', 'int(days) <= 90', 'int(days) <= 99', B+'test_org_paths_parameters_duplicates_dates_rejected'),
    ('future-date', 'domain.py', 'if end.date() > latest:', 'if False:', B+'test_org_paths_parameters_duplicates_dates_rejected'),
    ('post-input-cap', 'domain.py', 'int(limit) <= 10', 'int(limit) <= 99', D+'test_post_limit_and_unknown_parameters'),
    ('scope-set', 'source_contract.py', "SCOPES = ('rw_organization_admin', 'r_organization_social')", "SCOPES = ('r_organization_social',)", A+'test_scopes_pinned_and_expanded_grant_rejected_on_wire'),
    ('expanded-grant', 'oauth.py', 'set(raw[s.SCOPE].split()) != set(s.SCOPES)', 'False', A+'test_scopes_pinned_and_expanded_grant_rejected_on_wire'),
    ('daily-cap', 'workers.py', '>= DAILY_CAP:', '>= 1000000:', W+'test_daily_cap_counts_individual_gets_and_failures_parent_utc'),
    ('count-before-http', 'oauth.py', 'self.permit()  # server-process grant', 'pass  # server-process grant', W+'test_cap_stops_partway_posts_and_counts_token_exchange'),
    ('minute-throttle', 'workers.py', '>= RATE_PER_MINUTE:', '>= 1000000:', W+'test_minute_throttle_and_closed_pool'),
    ('bounded-queue', 'workers.py', '>= WORKERS + MAX_QUEUED:', '>= 1000000:', W+'test_two_workers_four_queue_slots'),
    ('kill-real-work', 'workers.py', 'proc.kill()', 'pass  # work continues', W+'test_timeout_kills_real_http_and_counter_survives'),
    ('minimize-before-projection', 'linkedin_adapter.py', 'return [{k: row[k] for k in keys if k in row} for row in elements(raw)]', 'return elements(raw)', A+'test_no_person_fields_and_breakdowns_before_projection'),
    ('nested-data-minimization', 'linkedin_adapter.py', 'return [{key: path(value, key) for key in keys} for value in values]', 'return values', A+'test_nested_breakdowns_discarded_by_real_adapter_before_sums'),
    ('org-config-wire', 'linkedin_adapter.py', 'params = {s.Q: org_key, org_key: self.cfg.organization}', "params = {s.Q: org_key, org_key: 'urn:li:organization:999'}", A+'test_fixed_get_routes_org_headers_and_sums'),
    ('response-org', 'linkedin_adapter.py', 'if not rows or any(path(row, org_key) != self.cfg.organization for row in rows):', 'if not rows:', A+'test_period_gaps_duplicates_foreign_org_and_next_page_rejected'),
    ('period-coverage', 'linkedin_adapter.py', 'if sorted(actual) != expected:', 'if False:', A+'test_period_gaps_duplicates_foreign_org_and_next_page_rejected'),
    ('no-partial-pagination', 'linkedin_adapter.py', 'if any(not isinstance(link, dict) or link.get(s.REL) == s.NEXT for link in paging.get(s.LINKS, [])):', 'if False:', A+'test_period_gaps_duplicates_foreign_org_and_next_page_rejected'),
    ('numeric-no-null-fallback', 'linkedin_adapter.py', 'def number(value):\n', 'def number(value):\n    if value is None: return 0\n', A+'test_missing_invalid_metrics_no_zero_or_replacement'),
    ('post-author', 'linkedin_adapter.py', 'path(row, s.AUTHOR) != self.cfg.organization', 'False', A+'test_posts_foreign_author_bad_id_unpublished_overlimit'),
    ('member-urn-in-text', 'linkedin_adapter.py', 'None if mentioned(row) else text', 'text', 'test_logs.Logs.test_real_service_mentions_never_in_response_or_log'),
    ('text-length', 'linkedin_adapter.py', 'text[:120]', 'text', A+'test_fixed_get_routes_org_headers_and_sums'),
    ('fixed-endpoints', 'linkedin_adapter.py', 'if endpoint not in (s.PAGE, s.FOLLOWERS, s.SHARES, s.POSTS, s.NETWORK):', 'if False:', A+'test_unknown_operation_and_endpoint_rejected_before_http'),
    ('outbound-get-only', 'linkedin_adapter.py', "headers=headers, method='GET'", "headers=headers, method='POST'", A+'test_fixed_get_routes_org_headers_and_sums'),
    ('source-redirect', 'oauth.py', 'return None\n\n\nclass Transport:', 'return super().redirect_request(*args, **kwargs)\n\n\nclass Transport:', A+'test_errors_redirects_no_retry_no_raw_error'),
    ('source-retry', 'oauth.py', '            status = exc.code', '            try:\n                self._open(request)\n            except Exception:\n                pass\n            status = exc.code', A+'test_errors_redirects_no_retry_no_raw_error'),
    ('post-statistics-id', 'linkedin_adapter.py', "if post and any(path(row, s.SHARE_ID if post.startswith('urn:li:share:') else s.UGC_ID) != post for row in rows):", 'if False:', S+'test_zero_only_valid_empty_single_post_no_paging_foreign_org_or_id'),
    ('post-created-order', 'linkedin_adapter.py', 'if previous is not None and created > previous:', 'if False:', A+'test_post_statistics_id_and_created_order_fail_closed'),
    ('source-byte-cap', 'oauth.py', 'if len(raw) > MAX_SOURCE:', 'if False:', A+'test_oversized_and_malformed_source'),
    ('response-byte-cap', 'domain.py', 'if len(data) > MAX_RESPONSE:', 'if False:', B+'test_output_limit'),
    ('token-expired', 'oauth.py', 'if now >= expiry:', 'if False:', A+'test_expired_without_refresh_never_calls_http'),
    ('refresh-no-extension', 'oauth.py', 'expiry = min(expiry, original_refresh_expiry)', 'expiry = expiry', A+'test_refresh_ram_only_no_extension_and_single_exchange'),
    ('warning', 'oauth.py', 'if now >= expiry - timedelta(days=WARNING_DAYS):', 'if False:', A+'test_token_response_bounds_and_warning_dates'),
    ('token-log-library', 'workers.py', 'logging.disable(logging.CRITICAL)', 'logging.disable(logging.NOTSET)', 'test_logs.Logs.test_real_server_worker_library_logs_redact_tokens'),
    ('token-log-http', 'server.py', 'route = source + "/" + report', 'route = source + "/" + report + key.decode()', B+'test_source_failure_token_never_in_log_or_error'),
    ('parser-redaction', 'safe_http.py', '{"error": code}', '{"error": message or code}', B+'test_parser_limits_path_limit_safe_json_and_no_token_logs'),
    ('consent-state', 'tools/zustimmung.py', "or not hmac.compare_digest(values.get(s.STATE, '').encode(), state.encode())", '', C+'test_wrong_state_duplicate_wrong_path_stop_before_exchange'),
    ('consent-loopback', 'tools/zustimmung.py', "CallbackServer(('127.0.0.1', 8765)", "CallbackServer(('0.0.0.0', 8765)", C+'test_listener_is_loopback_and_state_changes_each_run'),
    ('consent-hard-deadline', 'tools/zustimmung.py', 'timer.start()', 'pass  # deadline removed', C+'test_main_hard_deadline_exits_even_if_transport_hangs'),
    ('consent-no-echo', 'tools/zustimmung.py', "warnings.simplefilter('error', getpass.GetPassWarning)", 'pass  # echo fallback allowed', C+'test_getpass_echo_fallback_is_rejected'),
    ('consent-secret-argument-redaction', 'tools/zustimmung.py', "self.exit(2, 'ungueltige_argumente\\n')", "self.exit(2, message)", C+'test_secret_not_an_argument_stdout_json_only_no_files'),
    ('network-org-config', 'linkedin_adapter.py', "quote(self.cfg.organization, safe='')", "quote('urn:li:organization:999', safe='')", N+'test_network_total_fixed_encoded_org'),
    ('network-org-encoding', 'linkedin_adapter.py', "quote(self.cfg.organization, safe='')", 'self.cfg.organization', N+'test_network_total_fixed_encoded_org'),
    ('network-edge-type', 'linkedin_adapter.py', 'params = {s.EDGE_TYPE: s.FOLLOWED_BY_MEMBER}', 'params = {}', N+'test_network_total_fixed_encoded_org'),
    ('follower-two-day-lag', 'domain.py', "days=2 if report == 'followers' else 1", 'days=1', B+'test_follower_lag_and_org_override_rejected_before_worker'),
    ('missing-post-zero', 'linkedin_adapter.py', 'if post and not rows:\n            return []', 'if False:\n            return []', N+'test_missing_post_statistics_only_documented_empty_shape'),
    ('missing-post-flag', 'linkedin_adapter.py', "result['ohne_interaktion'] = True", 'pass', N+'test_missing_post_statistics_only_documented_empty_shape'),
    ('negative-count-clamp', 'linkedin_adapter.py', 'return max(0, total)', 'return total', N+'test_negative_like_count_clamped_and_marked'),
    ('negative-count-visible', 'linkedin_adapter.py', "result['korrigiert'] = True", 'pass', N+'test_negative_like_count_clamped_and_marked'),
    ('mention-attributes', 'linkedin_adapter.py', 'mentioned(row)', 'mentioned(text)', N+'test_mentions_text_and_attributes_redacted_without_aborting'),
    ('mention-marker', 'source_contract.py', "'memberattributedentity', 'mention', '@['", "'memberattributedentity', '@['", N+'test_mentions_text_and_attributes_redacted_without_aborting'),
    ('attribute-shape', 'linkedin_adapter.py', 'if not isinstance(attributes, list) or any(not isinstance(a, dict) for a in attributes):', 'if False:', N+'test_unknown_attributes_and_negative_dates_are_not_zero'),
    ('negative-date', 'linkedin_adapter.py', 'type(value) is not int or value < 0', 'type(value) is not int', N+'test_unknown_attributes_and_negative_dates_are_not_zero'),
    ('consent-ipv6-loopback', 'tools/zustimmung.py', "CallbackServer(('::1', 8765)", "CallbackServer(('::', 8765)", H+'test_both_loopbacks_one_request_and_close'),
    ('consent-ipv6-fallback', 'tools/zustimmung.py', "except OSError as exc:\n", "except OSError as exc:\n            raise\n", H+'test_ipv6_failure_continues_ipv4_with_notice'),
    ('consent-ipv6-notice', 'tools/zustimmung.py', "print('IPv6 nicht verfuegbar; weiter auf 127.0.0.1.', file=sys.stderr)", 'pass', H+'test_ipv6_failure_continues_ipv4_with_notice'),
    ('consent-one-attempt-even-invalid', 'tools/zustimmung.py', '        ready, _, _ = select.select(servers, [], [], max(0, deadline - time.monotonic()))\n        for server in ready:\n            server._handle_request_noblock()\n            break  # first request consumes both listeners, regardless of validity', '        while not received and time.monotonic() < deadline:\n            ready, _, _ = select.select(servers, [], [], max(0, deadline - time.monotonic()))\n            for server in ready:\n                server._handle_request_noblock()\n                if received: break', F+'test_invalid_first_request_on_either_socket_closes_both'),
    ('consent-only-one', 'tools/zustimmung.py', 'break  # first request consumes both listeners, regardless of validity', 'pass', H+'test_two_valid_callbacks_only_one_exchange'),
    ('post-no-top-level-next', 'linkedin_adapter.py', 'if s.NEXT in raw:', 'if False:', S+'test_zero_only_valid_empty_single_post_no_paging_foreign_org_or_id'),
    ('post-envelope-org', 'linkedin_adapter.py', 'if org_key in raw and raw[org_key] != self.cfg.organization:', 'if False:', S+'test_zero_only_valid_empty_single_post_no_paging_foreign_org_or_id'),
    ('post-envelope-id', 'linkedin_adapter.py', 'if field in raw and raw[field] != post:', 'if False:', S+'test_zero_only_valid_empty_single_post_no_paging_foreign_org_or_id'),
    ('post-second-row', 'linkedin_adapter.py', 'if post and len(rows) > 1:', 'if False:', S+'test_zero_only_valid_empty_single_post_no_paging_foreign_org_or_id'),
    ('post-other-id', 'linkedin_adapter.py', 'if post and any(field in row and row[field] != post for row in rows for field in (s.SHARE_ID, s.UGC_ID)):', 'if False:', S+'test_zero_only_valid_empty_single_post_no_paging_foreign_org_or_id'),
    ('zero-only-single-post', 'linkedin_adapter.py', 'if post and not rows:\n            return []', 'if not rows:\n            return []', S+'test_zero_only_valid_empty_single_post_no_paging_foreign_org_or_id'),
    ('negative-only-likes', 'linkedin_adapter.py', 'if key != s.REACTION and any(value < 0 for value in values):', 'if False:', S+'test_only_like_count_may_be_negative'),
    ('negative-followers', 'linkedin_adapter.py', 'type(value) is not int or value < 0', 'type(value) is not int', N+'test_negative_aggregate_counters_and_invalid_types_rejected'),
    ('likes-sum-before-clamp', 'linkedin_adapter.py', 'total = sum(values)', 'total = sum(max(0, value) for value in values)', S+'test_like_period_sum_before_clamp_real_transport'),
    ('person-reference-entire-object', 'linkedin_adapter.py', 'mentioned(row)', 'mentioned(text) or mentioned(attributes)', 'test_logs.Logs.test_real_service_mentions_never_in_response_or_log'),

    ('post-valid-paging', 'linkedin_adapter.py', 'if s.NEXT in raw:', 'if s.PAGING in raw or s.NEXT in raw:', 'test_final_sec.Paging.test_single_post_empty_or_missing_links_valid_next_and_bad_shapes_rejected'),
    ('paging-shape', 'linkedin_adapter.py', 'if not isinstance(paging, dict) or not isinstance(paging.get(s.LINKS, []), list):', 'if False:', 'test_final_sec.Paging.test_single_post_empty_or_missing_links_valid_next_and_bad_shapes_rejected'),
    ('ipv6-occupied-port', 'tools/zustimmung.py', "            if exc.errno == errno.EADDRINUSE:\n                print('IPv6-Port [::1]:8765 ist belegt; Zustimmung abgebrochen.', file=sys.stderr)\n                raise ValueError('zustimmung_abgewiesen') from None\n            if exc.errno not in (errno.EAFNOSUPPORT, errno.EADDRNOTAVAIL, errno.EPROTONOSUPPORT):\n                print('IPv6-Listener fehlgeschlagen; Zustimmung abgebrochen.', file=sys.stderr)\n                raise ValueError('zustimmung_abgewiesen') from None\n", '', 'test_final_sec.IPv6Failure.test_actual_ipv6_port_in_use_aborts_before_browser_or_token'),
    ('ipv6-only-unavailable-fallback', 'tools/zustimmung.py', 'if exc.errno not in (errno.EAFNOSUPPORT, errno.EADDRNOTAVAIL, errno.EPROTONOSUPPORT):', 'if False:', 'test_final_sec.IPv6Failure.test_unknown_ipv6_error_does_not_fall_back'),

]


def fingerprint():
    return {str(p.relative_to(ROOT)): hashlib.sha256(p.read_bytes()).hexdigest()
            for directory in ('linkedin_seite', 'tools') for p in (ROOT/directory).glob('*.py')}


def main():
    original = fingerprint()
    baseline = subprocess.run([sys.executable, '-m', 'unittest', 'discover', '-s', 'tests'], cwd=ROOT,
                              stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    if baseline.returncode:
        print(baseline.stdout)
        raise SystemExit('baseline failed; mutation results invalid')
    print('baseline: PASS', flush=True)
    failed = []
    for name, module, before, after, test in MUTANTS:
        with tempfile.TemporaryDirectory(prefix='.mutation-', dir=ROOT) as temporary:
            probe = Path(temporary)
            for directory in ('linkedin_seite', 'tests', 'tools'):
                shutil.copytree(ROOT/directory, probe/directory, ignore=shutil.ignore_patterns('__pycache__'))
            path = probe/module if module.startswith('tools/') else probe/'linkedin_seite'/module
            text = path.read_text(encoding='utf-8')
            if text.count(before) != 1:
                raise RuntimeError('mutation anchor changed: '+name)
            changed = text.replace(before, after, 1)
            ast.parse(changed)
            path.write_text(changed, encoding='utf-8')
            env = dict(os.environ, PYTHONPATH=str(probe)+os.pathsep+str(probe/'tests'), PYTHONDONTWRITEBYTECODE='1')
            try:
                run = subprocess.run([sys.executable, '-m', 'unittest', test], cwd=probe, env=env,
                                     stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, timeout=25)
                detected = run.returncode != 0 and 'FAILED (' in run.stdout and not any(
                    bad in run.stdout for bad in ('ImportError:', 'SyntaxError:', 'ModuleNotFoundError:'))
            except subprocess.TimeoutExpired:
                detected = False  # a hang is not a red-test proof
            print(name+': '+('DETECTED' if detected else 'SURVIVED/INVALID'), flush=True)
            if not detected:
                failed.append(name)
                if 'run' in locals():
                    print(run.stdout)
    if fingerprint() != original:
        raise SystemExit('original sources changed')
    if failed:
        raise SystemExit('failed probes: '+','.join(failed))
    print('all %d mutations detected; originals unchanged' % len(MUTANTS), flush=True)


if __name__ == '__main__':
    main()
