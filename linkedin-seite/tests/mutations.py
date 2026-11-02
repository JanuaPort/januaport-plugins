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
T = 'test_oauth_toleranz.'
# Stufe B1 (#1237): SEC c-6575359082 Bed. 1-5, c-6575837332 V1-V3, poll limits.
PF = 'test_post_adapter.Formats.'
PC = 'test_post_adapter.ClosedEndpoints.'
PA = 'test_post_adapter.Approval.'
PU = 'test_post_adapter.UploadTarget.'
PB = 'test_post_adapter.UploadBytes.'
PV = 'test_post_adapter.VideoStatus.'
PW = 'test_post_adapter.WriteToken.'
DE = 'test_post_domain.Entries.'
DP = 'test_post_domain.Polls.'
DF = 'test_post_domain.Formats.'
DH = 'test_post_domain.Hash.'
DA = 'test_post_domain.Approval.'
DC = 'test_post_domain.WriteConfiguration.'
DR = 'test_post_domain.Requests.'
G = 'test_post_gateway.Gateway.'
SB = 'test_post_server.Boundary.'
SF = 'test_post_server.Flow.'
SG = 'test_post_server.Background.'
ST = 'test_post_server.Tokens.'
SW = 'test_post_server.TokenSeparationOnTheWire.'
SL = 'test_post_server.LogCanary.'
WC = 'test_post_consent.WriteConsent.'
MUTANTS = [
    ('bridge-key', 'server.py', 'if not authorized:', 'if False:', B+'test_bridge_missing_wrong_duplicate_never_reach_adapter'),
    ('duplicate-auth', 'server.py', 'if len(header) != 1:', 'if not header:', B+'test_bridge_missing_wrong_duplicate_never_reach_adapter'),
    ('get-only', 'server.py', 'if request.method != "GET":\n                raise ReportError(405, "method_not_allowed")\n            check_bridge', 'if False:\n                raise ReportError(405, "method_not_allowed")\n            check_bridge', B+'test_no_write_method_no_generic_or_person_routes'),
    ('get-body', 'server.py', 'if request.can_read_body:', 'if False:', B+'test_body_rejected_and_local_health_does_not_use_api'),
    ('private-bind', 'server.py', 'if ip.version != 4 or not ip.is_private or ip.is_unspecified or ip.is_loopback:', 'if False:', D+'test_bind_rejects_wildcard_loopback_ipv6_and_public'),
    ('unknown-org-input', 'domain.py', 'k not in allowed or k in args', 'k in args', B+'test_org_paths_parameters_duplicates_dates_rejected'),
    ('duplicate-input', 'domain.py', 'k not in allowed or k in args', 'k not in allowed', B+'test_org_paths_parameters_duplicates_dates_rejected'),
    ('period-cap', 'domain.py', 'int(days) <= 90', 'int(days) <= 99', B+'test_org_paths_parameters_duplicates_dates_rejected'),
    ('future-date', 'domain.py', 'if end.date() > latest:', 'if False:', B+'test_org_paths_parameters_duplicates_dates_rejected'),
    ('post-input-cap', 'domain.py', 'int(limit) <= 10', 'int(limit) <= 99', D+'test_post_limit_and_unknown_parameters'),
    ('scope-set', 'source_contract.py', "SCOPES = ('rw_organization_admin', 'r_organization_social')", "SCOPES = ('r_organization_social',)", A+'test_scopes_pinned_and_expanded_grant_rejected_on_wire'),
    ('expanded-grant', 'oauth.py', 'scope_set(raw[s.SCOPE]) != set(scopes)', 'False', A+'test_scopes_pinned_and_expanded_grant_rejected_on_wire'),
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

    # JanuaPort/januaport#1237: tolerant where LinkedIn is imprecise, strict where it protects.
    ('scope-comma-separator', 'oauth.py', "re.compile(r'[\\s,]+')", "re.compile(r'\\s+')", T+'ScopeSeparators.test_comma_separated_as_sent_by_linkedin'),
    ('scope-only-comma-and-whitespace', 'oauth.py', "re.compile(r'[\\s,]+')", "re.compile(r'[\\s,;|+]+')", T+'ScopeSeparators.test_only_comma_and_whitespace_separate'),
    ('scope-empty-parts', 'oauth.py', 'for part in SCOPE_SEPARATORS.split(value) if part}', 'for part in SCOPE_SEPARATORS.split(value)}', T+'ScopeSeparators.test_comma_with_spaces_and_mixed_separators'),
    ('scope-exact-set-wire', 'oauth.py', 'scope_set(raw[s.SCOPE]) != set(scopes)', 'not scope_set(raw[s.SCOPE]) >= set(scopes)', T+'RealLinkedInShapeThroughRefreshPath.test_wrong_scope_on_the_refresh_path_still_fails_closed_with_one_call'),
    ('lifetime-cap', 'oauth.py', 'return min(seconds, cap)', 'return seconds', T+'LifetimeTolerance.test_observed_linkedin_overshoot_is_capped_at_exactly_365_days'),
    ('lifetime-no-overshoot-tolerance', 'oauth.py', 'seconds <= cap + OVERSHOOT_TOLERANCE_SECONDS:', 'seconds <= cap:', T+'LifetimeTolerance.test_observed_linkedin_overshoot_is_capped_at_exactly_365_days'),
    ('lifetime-unbounded-overshoot', 'oauth.py', 'seconds <= cap + OVERSHOOT_TOLERANCE_SECONDS:', 'seconds <= cap + 10**9:', T+'LifetimeTolerance.test_refresh_boundary_cap_plus_one_day_capped_beyond_rejected'),
    ('lifetime-positive', 'oauth.py', 'not 0 < seconds <= cap', 'not seconds <= cap', T+'LifetimeTolerance.test_refresh_invalid_types_and_values_rejected'),
    ('lifetime-int-type', 'oauth.py', 'type(seconds) is not int or ', '', T+'LifetimeTolerance.test_refresh_invalid_types_and_values_rejected'),
    ('access-lifetime-capped', 'oauth.py', 'lifetime(raw[s.EXPIRES_IN], ACCESS_DAYS)', 'raw[s.EXPIRES_IN]', T+'LifetimeTolerance.test_access_overshoot_is_capped_at_exactly_60_days'),
    ('refresh-lifetime-capped', 'oauth.py', 'lifetime(raw[s.REFRESH_EXPIRES_IN], REFRESH_DAYS)', 'raw[s.REFRESH_EXPIRES_IN]', T+'LifetimeTolerance.test_observed_linkedin_overshoot_is_capped_at_exactly_365_days'),
    ('restli-structure-raw', 'linkedin_adapter.py', "(value if key in s.RESTLI_STRUCTURED else quote(str(value), safe=''))", "quote(str(value), safe='')", A+'test_restli2_query_keeps_structure_raw_and_encodes_urns'),
    # --- Stufe B1 (#1237) -------------------------------------------------------------------
    # SEC Bed. 1: author/owner/actor only the configured organization, closed method/path list.
    ('b1-author-person', 'post_adapter.py', "body = {'author': self.cfg.organization,", "body = {'author': 'urn:li:person:1',", PF+'test_text_post_with_link_author_is_the_configured_organization'),
    ('b1-owner-person', 'post_adapter.py', 'self.api(op, body={s.INIT_REQUEST: {s.OWNER: self.cfg.organization}})', "self.api(op, body={s.INIT_REQUEST: {s.OWNER: 'urn:li:person:1'}})", PF+'test_single_image_upload_with_token_then_media_post'),
    ('b1-actor-person', 'post_adapter.py', 's.ACTOR: self.cfg.organization', "s.ACTOR: 'urn:li:person:1'", PF+'test_own_comment_as_the_organization'),
    ('b1-foreign-method', 'source_contract.py', "'post_delete': ('DELETE', 'posts/{urn}'),", "'post_delete': ('POST', 'posts/{urn}'),", PF+'test_delete_needs_its_own_approval'),
    ('b1-comment-get', 'source_contract.py', "'video_status': ('GET', 'videos/{urn}'),", "'video_status': ('GET', 'videos/{urn}'), 'comment_list': ('GET', 'socialActions/{urn}/comments'),", PC+'test_comment_get_reactions_and_foreign_operations_refused_before_http'),
    ('b1-closed-list-check', 'post_adapter.py', 'if op not in s.WRITE_ENDPOINTS:', 'if False:', PC+'test_comment_get_reactions_and_foreign_operations_refused_before_http'),
    ('b1-endpoint-table', 'source_contract.py', "'post_update': ('POST', 'posts/{urn}'),", "'post_update': ('PATCH', 'posts/{urn}'),", PC+'test_endpoint_table_is_exactly_the_reviewed_one'),
    ('b1-partial-update-header', 'source_contract.py', "RESTLI_METHODS = {'post_update': 'PARTIAL_UPDATE', 'post_delete': 'DELETE'}", "RESTLI_METHODS = {'post_delete': 'DELETE'}", PF+'test_edit_partial_update_commentary_only'),
    # SEC Bed. 2: approval from the second Kartei, bound to content and bytes, all four actions.
    ('b2-hash-mismatch', 'post_adapter.py', 'if not ok:', 'if False:', PA+'test_hash_mismatch_nothing_reaches_linkedin'),
    ('b2-hash-over-bytes', 'redaktion.py', "'medien': [{'datei': name, 'bytes': size, 'sha256': digest} for name, size, digest in media],", "'medien': [{'datei': name} for name, size, digest in media],", PA+'test_media_bytes_exchanged_after_the_preview'),
    ('b2-approval-field-in-plan', 'post_adapter.py', '        ok = r.freigegeben(saetze, req.eintrag_id, req.aktion, digest)', "        ok = r.freigegeben(saetze, req.eintrag_id, req.aktion, digest) or self.gateway.eintrag(req.eintrag_id)['fields'].get('freigabe') == digest", PA+'test_approval_field_inside_the_plan_is_worthless'),
    ('b2-delete-without-record', 'redaktion.py', '            return True\n    return False', "            return True\n    return aktion == 'loeschen'", PA+'test_delete_without_approval_record_and_with_a_foreign_action'),
    ('b2-sso-only', 'redaktion.py', "\n                and rec.get('via') == SSO and rec.get('updated_via') == SSO):", '):', PA+'test_only_sso_written_approvals_count'),
    ('b2-updated-via-sso', 'redaktion.py', " and rec.get('updated_via') == SSO):", '):', DA+'test_only_an_exact_sso_written_record_approves'),
    ('b2-action-in-hash', 'redaktion.py', "        obj = {'aktion': aktion, 'post_urn': entry.post_urn}", "        obj = {'post_urn': entry.post_urn}", DH+'test_table_of_frozen_vectors'),
    ('b2-poll-order-in-hash', 'redaktion.py', "'antworten': list(umfrage.antworten),", "'antworten': sorted(umfrage.antworten),", DH+'test_every_relevant_change_changes_the_hash_irrelevant_ones_do_not'),
    ('b2-text-in-hash', 'redaktion.py', "        obj = {'aktion': aktion, 'post_urn': entry.post_urn, 'post_text': entry.post_text}", "        obj = {'aktion': aktion, 'post_urn': entry.post_urn}", DH+'test_table_of_frozen_vectors'),
    # SEC Bed. 3: back channel only reading, only the three tools, only /mcp, no path.
    ('b3-closed-tools', 'gateway.py', 'if tool not in self.tools:', 'if False:', G+'test_any_other_tool_is_refused_before_http'),
    ('b3-path-before-call', 'gateway.py', "        r.check_datei(name)  # no path", "        pass  # no path", G+'test_paths_rejected_before_any_call'),
    ('b3-mcp-only', 'redaktion.py', "r'):(?P<port>[1-9][0-9]{0,4})/mcp')", "r'):(?P<port>[1-9][0-9]{0,4})/(?:admin/)?mcp(?:/.*)?')", DC+'test_gateway_url_only_mcp_on_internal_host_with_port'),
    ('b3-plan-path-guard', 'redaktion.py', "    return tuple(check_datei(v) for v in value)", "    return tuple(value)", DE+'test_fields_are_strings_json_lists_and_objects_fail_closed'),
    ('b3-pseudonym-guard', 'redaktion.py', "if any(mark in fields.get(name, '') for mark in PSEUDONYM):", 'if False:', DE+'test_pseudonymised_fields_are_never_posted'),
    # SEC Bed. 4: own write credential, exactly w_organization_social, never exchanged.
    ('b4-write-scope', 'source_contract.py', "WRITE_SCOPES = ('w_organization_social', 'w_organization_social_feed')", "WRITE_SCOPES = ('w_organization_social',)", WC+'test_write_mode_requests_exactly_w_organization_social'),
    ('b4-write-scope-extra', 'source_contract.py', "WRITE_SCOPES = ('w_organization_social', 'w_organization_social_feed')", "WRITE_SCOPES = ('w_organization_social', 'w_organization_social_feed', 'r_organization_social')", WC+'test_write_mode_requests_exactly_w_organization_social'),
    ('b4-write-scope-table', 'source_contract.py', "WRITE_SCOPES = ('w_organization_social', 'w_organization_social_feed')", "WRITE_SCOPES = ('w_organization_social_feed',)", PC+'test_endpoint_table_is_exactly_the_reviewed_one'),
    ('b4-consent-scope-string', 'tools/zustimmung.py', "s.STATE: state, s.SCOPE: ' '.join(scopes)}", "s.STATE: state, s.SCOPE: ' '.join(s.SCOPES)}", WC+'test_write_mode_requests_exactly_w_organization_social'),
    ('b4-write-identity-marker', 'oauth.py', "if not isinstance(data, dict) or not isinstance(data.get(s.SCOPE), str) or scope_set(data[s.SCOPE]) != set(s.WRITE_SCOPES):", 'if not isinstance(data, dict):', ST+'test_write_identity_requires_exactly_the_write_scope_read_identity_refuses_it'),
    ('b4-write-loader', 'server.py', '    from .oauth import load_write_identity\n', '    from .oauth import load_identity as load_write_identity\n', ST+'test_loading_the_write_side_fail_closed_with_fixed_reasons'),
    ('b4-write-place', 'server.py', 'POST_IDENTITY_PATH = "/run/secrets/post-oauth/runtime-key"', 'POST_IDENTITY_PATH = "/run/secrets/oauth/runtime-key"', ST+'test_write_identity_requires_exactly_the_write_scope_read_identity_refuses_it'),
    ('b4-write-refresh-scope', 'post_adapter.py', 'self.tokens = Tokens(identity, self.transport, scopes=s.WRITE_SCOPES)', 'self.tokens = Tokens(identity, self.transport)', PW+'test_expired_write_token_is_refreshed_with_exactly_the_write_scope'),
    ('b4-token-exchange', 'post_pool.py', "args=(child, self.cfg, self.identity, self.token, req, self.factory),", "args=(child, self.cfg, dict(self.identity, access_token='READ-TOKEN-SWAPPED'), self.token, req, self.factory),", SW+'test_reads_carry_the_read_token_writes_the_write_token'),
    # SEC Bed. 5: 5 write actions per UTC day over all actions, idempotency, trace without text.
    ('b5-daily-cap', 'post_pool.py', "self._count(self.actions, WRITE_ACTIONS_PER_DAY, 'schreib_tageslimit')", "self._count(self.actions, 10**6, 'schreib_tageslimit')", SF+'test_five_write_actions_per_utc_day_over_all_actions'),
    ('b5-count-before-request', 'post_adapter.py', 'self.link.begin_action()  # counts once', 'pass  # counts once', PC+'test_action_is_counted_before_the_first_linkedin_request'),
    ('b5-video-counts-once', 'post_adapter.py', 'self.link.begin_action()  # counts once', 'pass  # counts once', SF+'test_aborted_video_counts_as_one_action'),
    ('b5-idempotency', 'post_pool.py', "job.conn.send(('fertig',) if (job.req.eintrag_id, job.digest) in self.done else ('weiter',))", "job.conn.send(('weiter',))", SF+'test_post_then_idempotent_repeat_returns_the_same_urn'),
    ('b5-job-log-silence', 'post_pool.py', 'logging.disable(logging.CRITICAL)  # no library', 'logging.disable(logging.NOTSET)  # no library', SL+'test_real_service_log_has_no_text_file_name_upload_url_or_secret'),
    ('b5-trace-text', 'post_pool.py', "LOG.info('spur aktion=%s eintrag_id=%s hash=%s code=%s urn=%s bytes=%d', job.req.aktion, job.req.eintrag_id,", "LOG.info('spur aktion=%s eintrag_id=%s hash=%s code=%s urn=%s bytes=%d %s', job.req.aktion, job.req.eintrag_id, str(job.req) + str(payload),", SF+'test_post_then_idempotent_repeat_returns_the_same_urn'),
    ('b5-unknown-outcome', 'post_pool.py', 'if (job.req.eintrag_id, job.digest) in self.unbekannt:', 'if False:', SF+'test_unknown_outcome_after_the_final_request_is_never_sent_again'),
    ('b5-final-marker', 'post_adapter.py', 'self.link.final_request()  # from here', 'pass  # from here', SF+'test_unknown_outcome_after_the_final_request_is_never_sent_again'),
    ('b5-one-job', 'post_pool.py', "            raise ReportError(429, 'schreiben_belegt')", '            pass', SG+'test_slow_job_answers_202_then_the_result'),
    ('b5-write-auth', 'server.py', '                check_bridge(request, key)\n                if write_pool is None:', '                pass\n                if write_pool is None:', SB+'test_auth_method_route_and_body_checked_before_any_call'),
    ('b5-only-the-id', 'redaktion.py', "if not isinstance(body, dict) or set(body) != {'eintrag_id'}:", "if not isinstance(body, dict) or 'eintrag_id' not in body:", DR+'test_write_routes_take_only_the_kartei_id'),
    # SEC V1: upload target fixed host, https, no userinfo/port, no redirect, token only where needed.
    ('v1-host', 'redaktion.py', ' or host not in s.UPLOAD_HOSTS', '', PU+'test_foreign_host_http_userinfo_and_port_never_get_a_byte'),
    ('v1-https', 'redaktion.py', "parts.scheme != 'https' or ", '', PU+'test_foreign_host_http_userinfo_and_port_never_get_a_byte'),
    ('v1-userinfo-port', 'redaktion.py', ' or parts.netloc != host', '', PU+'test_table_of_upload_targets'),
    ('v1-path-prefix', 'redaktion.py', '            or parts.fragment or not parts.path.startswith(s.UPLOAD_PATH_PREFIX)):', '            or parts.fragment):', PU+'test_table_of_upload_targets'),
    ('v1-no-redirect', 'oauth.py', 'return None\n\n\nclass Transport:', 'return super().redirect_request(*args, **kwargs)\n\n\nclass Transport:', PU+'test_redirect_of_an_api_answer_is_not_followed_and_carries_no_token'),
    ('v1-video-without-token', 'source_contract.py', "UPLOAD_TOKEN = {'bild': True, 'dokument': True, 'video': False}", "UPLOAD_TOKEN = {'bild': True, 'dokument': True, 'video': True}", PF+'test_video_parts_without_token_finalize_status_then_post'),
    ('v1-check-before-put', 'post_adapter.py', '        ziel = r.upload_ziel(url)  # SEC V1', '        ziel = url  # SEC V1', PU+'test_foreign_host_http_userinfo_and_port_never_get_a_byte'),
    # SEC V2: approval before initializeUpload, one read, gap-free parts, size before reading.
    ('v2-approval-before-upload', 'post_adapter.py', 'if not ok:', 'if False:', PA+'test_hash_mismatch_nothing_reaches_linkedin'),
    ('v2-read-once', 'post_adapter.py', 'memoryview(medium.daten)[first:last + 1]', 'self.gateway.datei(medium.name, self.w.max_medium_bytes).daten[first:last + 1]', PB+'test_file_exchanged_between_hash_and_upload_is_not_read_again'),
    ('v2-parts-gap-overlap', 'redaktion.py', 'or first != expected or ', 'or ', PB+'test_part_list_gaps_overlaps_counts_and_types_abort_without_finalize'),
    ('v2-parts-count', 'redaktion.py', ' or len(instructions) > math.ceil(size / s.VIDEO_PART_BYTES)', '', PB+'test_part_list_gaps_overlaps_counts_and_types_abort_without_finalize'),
    ('v2-parts-cover', 'redaktion.py', '    if expected != size:\n', '    if False:\n', PB+'test_part_list_gaps_overlaps_counts_and_types_abort_without_finalize'),
    ('v2-size-limit', 'gateway.py', '    if size > max_bytes:', '    if False:', PB+'test_oversized_file_rejected_before_any_linkedin_request'),
    ('v2-copy-wiped', 'post_adapter.py', '            medium.loeschen()', '            pass', PB+'test_private_copies_are_wiped_on_success_and_on_every_error'),
    ('v2-magic', 'redaktion.py', "    if not ok:\n        fail(422, 'medium_format_ungueltig')", "    if False:\n        fail(422, 'medium_format_ungueltig')", DF+'test_file_content_must_match_its_extension'),
    # SEC V3: bounded status polling, post only on AVAILABLE.
    ('v3-max-wait', 'post_adapter.py', 'if self.monotonic() + s.VIDEO_POLL_SECONDS > deadline:', 'if False:', PV+'test_never_available_aborts_after_the_maximum_time_without_post'),
    ('v3-available-only', 'post_adapter.py', 'if status == s.VIDEO_AVAILABLE:', "if status in (s.VIDEO_AVAILABLE, 'PROCESSING'):", PV+'test_never_available_aborts_after_the_maximum_time_without_post'),
    ('v3-failed-status', 'post_adapter.py', 'if status not in s.VIDEO_PENDING:', 'if False:', PV+'test_failed_or_unknown_status_aborts'),
    # Poll limits (SEC c-6575837332 (a)): checked before sending, nothing shortened, no media.
    ('poll-question-length', 'redaktion.py', 'not 1 <= len(frage) <= s.POLL_QUESTION_MAX', 'not 1 <= len(frage)', DP+'test_limits_checked_before_sending_nothing_is_shortened'),
    ('poll-option-count', 'redaktion.py', 'not s.POLL_OPTIONS_MIN <= len(antworten) <= s.POLL_OPTIONS_MAX', 'not 1 <= len(antworten)', DP+'test_limits_checked_before_sending_nothing_is_shortened'),
    ('poll-duration', 'redaktion.py', 'dauer not in s.POLL_DURATIONS', 'dauer not in (1, 2, 3, 7, 14, 30)', DP+'test_limits_checked_before_sending_nothing_is_shortened'),
    ('poll-no-media', 'redaktion.py', "        if arten:\n            fail(422, 'umfrage_mit_medien')", "        if False:\n            fail(422, 'umfrage_mit_medien')", DP+'test_no_media_together_with_a_poll'),
    # Text safety: no mention can be formed, comment without URN, no repost of a posted entry.
    ('little-escape', 'redaktion.py', '        elif char in s.LITTLE_RESERVED:', '        elif False:', PF+'test_text_post_with_link_author_is_the_configured_organization'),
    ('comment-urn-refused', 'redaktion.py', "if aktion == 'kommentar' and 'urn:li:' in entry.kommentar_text.lower():", 'if False:', DE+'test_comment_text_with_a_urn_is_refused_until_little_text_is_proven_for_comments'),
    ('posted-entry-not-again', 'redaktion.py', "        if entry.post_urn is not None:\n            fail(409, 'bereits_veroeffentlicht')", "        if False:\n            fail(409, 'bereits_veroeffentlicht')", DE+'test_per_action_requirements'),

    ('secret-control-character', 'tools/zustimmung.py', "if any(unicodedata.category(char) == 'Cc' for char in value):", 'if False:', C+'test_control_character_in_secret_is_rejected_with_german_message_and_nothing_is_sent'),

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
