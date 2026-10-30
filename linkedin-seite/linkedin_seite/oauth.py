"""Fixed HTTPS transport and RAM-only OAuth lifecycle. No provider diagnostics."""
from datetime import datetime, timedelta, timezone
from pathlib import Path
import urllib.request
import urllib.error
from urllib.parse import urlencode
from . import source_contract as s
from .domain import ReportError, strict_json

ACCESS_DAYS = 60
REFRESH_DAYS = 365
WARNING_DAYS = 14
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


def load_identity(path):
    with Path(path).open('rb') as handle:
        raw = handle.read(64 * 1024 + 1)
    if len(raw) > 64 * 1024:
        raise ValueError('invalid_identity')
    return validate_identity(strict_json(raw))


def warning(data, now=None):
    now = now or datetime.now(timezone.utc)
    expiry = timestamp(data.get('refresh_expires_at', data['access_expires_at']))
    if 'refresh_expires_at' not in data and now >= expiry:
        return {}  # expired access is the fixed token_abgelaufen error, not a forecast
    if now >= expiry - timedelta(days=WARNING_DAYS):
        return {'warnung': 'token_laeuft_ab', 'ablaufdatum': expiry.date().isoformat()}
    return {}


def token_result(raw, now, original_refresh_expiry=None):
    try:
        if not isinstance(raw, dict):
            raise ValueError()
        if s.SCOPE in raw and (not isinstance(raw[s.SCOPE], str) or set(raw[s.SCOPE].split()) != set(s.SCOPES)):
            raise ValueError()
        if raw.get(s.TOKEN_TYPE, 'Bearer').lower() != 'bearer':
            raise ValueError()
        seconds = raw[s.EXPIRES_IN]
        if type(seconds) is not int or not 0 < seconds <= ACCESS_DAYS * 86400:
            raise ValueError()
        result = {s.ACCESS_TOKEN: secret(raw[s.ACCESS_TOKEN]),
                  'access_expires_at': (now + timedelta(seconds=seconds)).isoformat()}
        if s.REFRESH_TOKEN in raw:
            seconds = raw[s.REFRESH_EXPIRES_IN]
            if type(seconds) is not int or not 0 < seconds <= REFRESH_DAYS * 86400:
                raise ValueError()
            expiry = now + timedelta(seconds=seconds)
            if original_refresh_expiry is not None:
                expiry = min(expiry, original_refresh_expiry)  # never extend original consent
            result.update({s.REFRESH_TOKEN: secret(raw[s.REFRESH_TOKEN]), 'refresh_expires_at': expiry.isoformat()})
        return result
    except (ValueError, KeyError, TypeError, AttributeError):
        raise ReportError(502, 'unexpected_source_shape') from None


class Tokens:
    def __init__(self, data, transport, now=None):
        self.data = validate_identity(data)
        self.transport = transport
        self.now = now or (lambda: datetime.now(timezone.utc))

    def access(self):
        now = self.now()
        expiry = timestamp(self.data['access_expires_at'])
        refresh = self.data.get(s.REFRESH_TOKEN)
        refresh_expiry = timestamp(self.data['refresh_expires_at']) if refresh else None
        if refresh and now >= expiry - timedelta(minutes=5) and now < refresh_expiry:
            raw = self.transport.token({s.GRANT_TYPE: s.REFRESH_TOKEN, s.REFRESH_TOKEN: refresh,
                                       s.CLIENT_ID: self.data[s.CLIENT_ID], s.CLIENT_SECRET: self.data[s.CLIENT_SECRET]})
            update = token_result(raw, now, refresh_expiry)
            self.data.update(update)  # RAM only; source file remains untouched
            expiry = timestamp(self.data['access_expires_at'])
        if now >= expiry:
            raise ReportError(503, 'token_abgelaufen')
        return self.data[s.ACCESS_TOKEN]
