"""Fixed HTTPS transport and RAM-only OAuth lifecycle. No provider diagnostics."""
from datetime import datetime, timedelta, timezone
from pathlib import Path
import re
import urllib.request
import urllib.error
from urllib.parse import urlencode
from . import source_contract as s
from .domain import ReportError, strict_json

ACCESS_DAYS = 60
REFRESH_DAYS = 365
WARNING_DAYS = 14
OVERSHOOT_TOLERANCE_SECONDS = 86400  # documented bound: reject beyond cap + one day
SCOPE_SEPARATORS = re.compile(r'[\s,]+')
MAX_SOURCE = 512 * 1024


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


class Transport:
    def __init__(self, permit):
        self.permit = permit
        self.timeout = 18
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())

    def _open(self, request):
        return self.opener.open(request, timeout=self.timeout)

    def send(self, request):
        self.permit()  # server-process grant before every HTTP attempt, including OAuth
        try:
            with self._open(request) as response:
                raw = response.read(MAX_SOURCE + 1)
                if len(raw) > MAX_SOURCE:
                    raise ReportError(502, 'response_too_large')
                return strict_json(raw)
        except urllib.error.HTTPError as exc:
            status = exc.code
            exc.close()
            code = ('source_access_denied' if status in (401, 403) else
                    'source_quota_limited' if status == 429 else
                    'source_unavailable' if status >= 500 else 'source_rejected')
            raise ReportError(502, code) from None
        except (ValueError, UnicodeError):
            raise ReportError(502, 'unexpected_source_shape') from None
        except (OSError, urllib.error.URLError):
            raise ReportError(502, 'source_unavailable') from None

    def token(self, fields):
        return self.send(urllib.request.Request(s.TOKEN_URL, data=urlencode(fields).encode(),
                         headers={'Content-Type': 'application/x-www-form-urlencoded'}, method='POST'))


def timestamp(value):
    if not isinstance(value, str):
        raise ValueError('invalid_identity')
    dt = datetime.fromisoformat(value)
    if dt.tzinfo is None:
        raise ValueError('invalid_identity')
    return dt.astimezone(timezone.utc)


def secret(value):
    if not isinstance(value, str) or not 1 <= len(value) <= 8192 or any(ord(c) < 33 or ord(c) > 126 for c in value):
        raise ValueError('invalid_identity')
    return value


def validate_identity(data, now=None):
    now = now or datetime.now(timezone.utc)
    required = {s.CLIENT_ID, s.CLIENT_SECRET, s.ACCESS_TOKEN, 'access_expires_at'}
    optional = {s.REFRESH_TOKEN, 'refresh_expires_at'}
    if not isinstance(data, dict) or not required <= data.keys() or set(data) - required - optional:
        raise ValueError('invalid_identity')
    for field in (s.CLIENT_ID, s.CLIENT_SECRET, s.ACCESS_TOKEN):
        secret(data[field])
    if timestamp(data['access_expires_at']) > now + timedelta(days=ACCESS_DAYS, minutes=1):
        raise ValueError('invalid_identity')
    if (s.REFRESH_TOKEN in data) != ('refresh_expires_at' in data):
        raise ValueError('invalid_identity')
    if s.REFRESH_TOKEN in data:
        secret(data[s.REFRESH_TOKEN])
        if timestamp(data['refresh_expires_at']) > now + timedelta(days=REFRESH_DAYS, minutes=1):
            raise ValueError('invalid_identity')
    return dict(data)


def read_identity_file(path):
    with Path(path).open('rb') as handle:
        raw = handle.read(64 * 1024 + 1)
    if len(raw) > 64 * 1024:
        raise ValueError('invalid_identity')
    return strict_json(raw)


def load_identity(path):
    return validate_identity(read_identity_file(path))


def validate_write_identity(data, now=None):
    """Stufe B1 (#1237, SEC Bed. 4): the write identity carries `scope` = exactly the write scope.

    The unchanged Stufe-A check rejects any `scope` field, so a write JSON in the read place fails
    there, and a read JSON (no `scope`) fails here: the two tokens cannot be exchanged unnoticed.
    Returns the identity without the marker, in the shape `Tokens` expects.
    """
    if not isinstance(data, dict) or not isinstance(data.get(s.SCOPE), str) or scope_set(data[s.SCOPE]) != set(s.WRITE_SCOPES):
        raise ValueError('invalid_identity')
    return validate_identity({k: v for k, v in data.items() if k != s.SCOPE}, now)


def load_write_identity(path):
    return validate_write_identity(read_identity_file(path))


def warning(data, now=None):
    now = now or datetime.now(timezone.utc)
    expiry = timestamp(data.get('refresh_expires_at', data['access_expires_at']))
    if 'refresh_expires_at' not in data and now >= expiry:
        return {}  # expired access is the fixed token_abgelaufen error, not a forecast
    if now >= expiry - timedelta(days=WARNING_DAYS):
        return {'warnung': 'token_laeuft_ab', 'ablaufdatum': expiry.date().isoformat()}
    return {}


def scope_set(value):
    """LinkedIn sends the granted scopes comma separated (02.11.2026); whitespace is accepted too.

    Only the separator is tolerant. The caller still compares the set with the pinned scopes
    exactly, so nothing extra and nothing missing passes. Non-strings fail closed.
    """
    if not isinstance(value, str):
        raise ValueError()
    return {part for part in SCOPE_SEPARATORS.split(value) if part}


def lifetime(seconds, days):
    """Validated lifetime in seconds, capped at the documented maximum.

    LinkedIn overshoots slightly (refresh_token_expires_in 31536059 = 59 s over 365 days).
    A small overshoot is capped, never extended; beyond cap + OVERSHOOT_TOLERANCE_SECONDS,
    or for non-int / non-positive values, the response is rejected as unexpected.
    """
    cap = days * 86400
    if type(seconds) is not int or not 0 < seconds <= cap + OVERSHOOT_TOLERANCE_SECONDS:
        raise ValueError()
    return min(seconds, cap)


def token_result(raw, now, original_refresh_expiry=None, scopes=s.SCOPES):
    try:
        if not isinstance(raw, dict):
            raise ValueError()
        if s.SCOPE in raw and scope_set(raw[s.SCOPE]) != set(scopes):
            raise ValueError()
        if raw.get(s.TOKEN_TYPE, 'Bearer').lower() != 'bearer':
            raise ValueError()
        seconds = lifetime(raw[s.EXPIRES_IN], ACCESS_DAYS)
        result = {s.ACCESS_TOKEN: secret(raw[s.ACCESS_TOKEN]),
                  'access_expires_at': (now + timedelta(seconds=seconds)).isoformat()}
        if s.REFRESH_TOKEN in raw:
            seconds = lifetime(raw[s.REFRESH_EXPIRES_IN], REFRESH_DAYS)
            expiry = now + timedelta(seconds=seconds)
            if original_refresh_expiry is not None:
                expiry = min(expiry, original_refresh_expiry)  # never extend original consent
            result.update({s.REFRESH_TOKEN: secret(raw[s.REFRESH_TOKEN]), 'refresh_expires_at': expiry.isoformat()})
        return result
    except (ValueError, KeyError, TypeError, AttributeError):
        raise ReportError(502, 'unexpected_source_shape') from None


class Tokens:
    def __init__(self, data, transport, now=None, scopes=s.SCOPES):
        self.data = validate_identity(data)
        self.transport = transport
        self.now = now or (lambda: datetime.now(timezone.utc))
        self.scopes = scopes  # the read grant, or for Stufe B1 the separate write grant

    def access(self):
        now = self.now()
        expiry = timestamp(self.data['access_expires_at'])
        refresh = self.data.get(s.REFRESH_TOKEN)
        refresh_expiry = timestamp(self.data['refresh_expires_at']) if refresh else None
        if refresh and now >= expiry - timedelta(minutes=5) and now < refresh_expiry:
            raw = self.transport.token({s.GRANT_TYPE: s.REFRESH_TOKEN, s.REFRESH_TOKEN: refresh,
                                       s.CLIENT_ID: self.data[s.CLIENT_ID], s.CLIENT_SECRET: self.data[s.CLIENT_SECRET]})
            update = token_result(raw, now, refresh_expiry, self.scopes)
            self.data.update(update)  # RAM only; source file remains untouched
            expiry = timestamp(self.data['access_expires_at'])
        if now >= expiry:
            raise ReportError(503, 'token_abgelaufen')
        return self.data[s.ACCESS_TOKEN]
