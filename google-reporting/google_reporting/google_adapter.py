"""Official REST clients; only called inside killable, bounded worker processes."""

import json
from pathlib import Path
import httplib2
import google_auth_httplib2
from google.oauth2 import service_account, _client
from googleapiclient.discovery import build
from google.api_core import exceptions
from google.analytics.data_v1beta import BetaAnalyticsDataClient
from google.analytics.data_v1beta.types import (
    RunReportRequest,
    CheckCompatibilityRequest,
)
from google.analytics.data_v1beta.services.beta_analytics_data.transports.rest import (
    BetaAnalyticsDataRestTransport,
)
from .domain import ReportError, build_query, project, parse_request
from .manage import ManageRequest
from .manage_adapter import ManageRunner


def proto_request(cls, fields):
    from google.protobuf.json_format import ParseDict

    return cls.wrap(ParseDict(fields, cls.pb()()))


SCOPES = {
    "gsc": "https://www.googleapis.com/auth/webmasters.readonly",
    "ga4": "https://www.googleapis.com/auth/analytics.readonly",
}
# A write asks for its own credential object with exactly this scope, never combined with
# the readonly one (JanuaPort/januaport#1148, SEC W1).
WRITE_SCOPES = {
    "gsc": "https://www.googleapis.com/auth/webmasters",
    "ga4": "https://www.googleapis.com/auth/analytics.edit",
}
TOKEN_URI = "https://oauth2.googleapis.com/token"


def load_identity(path):
    try:
        info = json.loads(Path(path).read_text())
        if (
            info.get("type") != "service_account"
            or info.get("token_uri") != TOKEN_URI
            or info.get("universe_domain", "googleapis.com") != "googleapis.com"
        ):
            raise ValueError()
        if (
            info.get("auth_uri", "https://accounts.google.com/o/oauth2/auth")
            != "https://accounts.google.com/o/oauth2/auth"
        ):
            raise ValueError()
        if not isinstance(info.get("client_email"), str) or not info[
            "client_email"
        ].endswith(".iam.gserviceaccount.com"):
            raise ValueError()
        service_account.Credentials.from_service_account_info(
            info, scopes=[SCOPES["gsc"]]
        )
        return info
    except Exception:
        raise ValueError("invalid_identity_file") from None


def safe_error(exc, manage=False):
    status = getattr(getattr(exc, "resp", None), "status", None) or getattr(
        exc, "code", None
    )
    if callable(status):
        status = status()
    if manage and status in (400, 409, 412, 422):
        return ReportError(502, "source_rejected")
    if status in (401, 403) or isinstance(
        exc, (exceptions.Unauthenticated, exceptions.PermissionDenied)
    ):
        return ReportError(502, "source_access_denied")
    if status == 429 or isinstance(exc, exceptions.ResourceExhausted):
        return ReportError(503, "source_quota_limited")
    if status in (408, 504) or isinstance(
        exc, (TimeoutError, exceptions.DeadlineExceeded)
    ):
        return ReportError(504, "source_timeout")
    return ReportError(502, "source_unavailable")


class ScopedCredentials(service_account.Credentials):
    # Pinned google-auth seam: upstream refresh otherwise retries jwt_grant and
    # can perform optional IAM trust-boundary discovery. V1 has neither.
    def before_request(self, request, method, url, headers):
        # Keep the synchronous token contract. Upstream 2.62 otherwise starts an
        # IAM regional-boundary background lookup outside this report's budget.
        if not self.valid:
            self.refresh(request)
        self.apply(headers)

    def refresh(self, request):
        def bounded_request(*args, **kwargs):
            kwargs["timeout"] = 18
            return request(*args, **kwargs)

        self.token, self.expiry, _ = _client.jwt_grant(
            bounded_request,
            TOKEN_URI,
            self._make_authorization_grant_assertion(),
            can_retry=False,
        )


def discovery_client(api, version, cred):
    raw_http = httplib2.Http(timeout=18)
    raw_http.follow_redirects = False
    http = google_auth_httplib2.AuthorizedHttp(
        cred, http=raw_http, max_refresh_attempts=0
    )
    # Bundled discovery avoids runtime discovery downloads and arbitrary service URLs.
    return build(
        api, version, http=http, cache_discovery=False, static_discovery=True
    )


class GoogleAdapter:
    """One cache per worker, distinct scoped credentials for each source; RAM only."""

    def __init__(self, cfg, identity_path):
        self.cfg = cfg
        self.identity_path = identity_path
        self.clients = {}
        self.credentials = {}

    def client(self, source):
        if source not in self.clients:
            info = load_identity(self.identity_path)
            cred = ScopedCredentials.from_service_account_info(
                info, scopes=[SCOPES[source]], always_use_jwt_access=False
            )
            self.credentials[source] = cred
            if source == "gsc":
                self.clients[source] = discovery_client("searchconsole", "v1", cred)
            else:
                transport = BetaAnalyticsDataRestTransport(
                    credentials=cred,
                    host="analyticsdata.googleapis.com",
                    always_use_jwt_access=False,
                )
                # Auth retries and token refresh retries are disabled. Parent deadline kills
                # the process even if an SDK/socket ignores its own deadline.
                transport._session._max_refresh_attempts = 0
                transport._session.max_redirects = 0
                transport._session._auth_request.session.max_redirects = 0
                from requests.adapters import HTTPAdapter

                transport._session._auth_request.session.mount(
                    "https://", HTTPAdapter(max_retries=0)
                )
                self.clients[source] = BetaAnalyticsDataClient(transport=transport)
        return self.clients[source]

    def service(self, label, api, version, scope):
        """One client and one credential object per scope (write scopes never combined)."""
        if label not in self.clients:
            info = load_identity(self.identity_path)
            cred = ScopedCredentials.from_service_account_info(
                info, scopes=[scope], always_use_jwt_access=False
            )
            self.credentials[label] = cred
            self.clients[label] = discovery_client(api, version, cred)
        return self.clients[label]

    def gsc_read(self):
        return self.client("gsc")

    def gsc_write(self):
        return self.service("gsc-write", "searchconsole", "v1", WRITE_SCOPES["gsc"])

    def ga_read(self):
        return self.service("ga4-admin-read", "analyticsadmin", "v1alpha", SCOPES["ga4"])

    def ga_edit(self):
        return self.service("ga4-admin-edit", "analyticsadmin", "v1alpha", WRITE_SCOPES["ga4"])

    def run_manage(self, req):
        try:
            return ManageRunner(self.cfg, self).run(req)
        except ReportError:
            raise
        except Exception as exc:
            # Never propagate Google messages, URLs, inputs or stack traces.
            raise safe_error(exc, manage=True) from None

    def run(self, req):
        if isinstance(req, ManageRequest):
            return self.run_manage(req)
        query = build_query(self.cfg, req)
        if query is None:
            return project(self.cfg, req, {})
        try:
            client = self.client(req.source)
            if req.source == "gsc":
                raw = (
                    client.searchanalytics()
                    .query(siteUrl=self.cfg.gsc_property, body=query)
                    .execute(num_retries=0)
                )
            else:
                if req.report == "get_status":
                    self.check_definitions(client)
                response = client.run_report(
                    request=proto_request(RunReportRequest, query),
                    retry=None,
                    timeout=18,
                )
                raw = type(response).to_dict(
                    response,
                    preserving_proto_field_name=False,
                    always_print_fields_with_no_presence=False,
                    use_integers_for_enums=False,
                )
            return project(self.cfg, req, raw)
        except ReportError:
            raise
        except Exception as exc:
            # Never propagate Google messages, URLs, inputs or stack traces.
            raise safe_error(exc) from None

    def check_definitions(self, client):
        for report in ("traffic_summary", "landing_pages", "key_events"):
            req = parse_request(self.cfg, "ga4", report, [])
            query = build_query(self.cfg, req)
            if query is None:
                continue
            fields = {
                k: query[k]
                for k in ("property", "dimensions", "metrics", "dimensionFilter")
            }
            response = client.check_compatibility(
                request=proto_request(CheckCompatibilityRequest, fields),
                retry=None,
                timeout=18,
            )
            if len(response.dimension_compatibilities) != len(
                query["dimensions"]
            ) or len(response.metric_compatibilities) != len(query["metrics"]):
                raise ReportError(503, "report_definition_unverified")
            for entry in list(response.dimension_compatibilities) + list(
                response.metric_compatibilities
            ):
                if entry.compatibility.name != "COMPATIBLE":
                    raise ReportError(503, "report_definition_incompatible")
