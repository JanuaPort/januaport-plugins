"""Fixed input contract and bounded JSON, following google-reporting."""
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
import json
import re

MAX_RESPONSE = 512 * 1024
REPORTS = frozenset({'page_statistics', 'followers', 'posts'})


class ReportError(Exception):
    def __init__(self, status, code):
        super().__init__(code)
        self.status, self.code = status, code


def strict_json(raw):
    def pairs(items):
        result = {}
        for k, v in items:
            if k in result:
                raise ValueError('duplicate_key')
            result[k] = v
        return result
    return json.loads(raw, object_pairs_hook=pairs,
                      parse_constant=lambda _: (_ for _ in ()).throw(ValueError('invalid_number')))


@dataclass(frozen=True)
class Config:
    organization: str
    linkedin_version: str

    @classmethod
    def from_dict(cls, raw):
        if not isinstance(raw, dict) or set(raw) - {'organization', 'linkedin_version'}:
            raise ValueError('configuration_invalid')
        org = raw.get('organization')
        if not isinstance(org, str) or not re.fullmatch(r'(?:urn:li:organization:)?[1-9][0-9]{0,19}', org):
            raise ValueError('configuration_invalid')
        if not org.startswith('urn:'):
            org = 'urn:li:organization:' + org
        version = raw.get('linkedin_version', datetime.now(timezone.utc).strftime('%Y%m'))
        if not isinstance(version, str) or not re.fullmatch(r'20[0-9]{2}(?:0[1-9]|1[0-2])', version):
            raise ValueError('configuration_invalid')
        return cls(org, version)


@dataclass(frozen=True)
class Request:
    report: str
    start: int
    end: int
    limit: int = 10


def parse_request(report, pairs, now=None):
    if report not in REPORTS:
        raise ReportError(404, 'route_not_found')
    allowed = {'limit'} if report == 'posts' else {'period_days', 'end_date'}
    args = {}
    for k, v in pairs:
        if k not in allowed or k in args:
            raise ReportError(400, 'invalid_input')
        args[k] = v
    try:
        days, limit = args.get('period_days', '7'), args.get('limit', '10')
        if not re.fullmatch(r'[1-9][0-9]?', days) or not 1 <= int(days) <= 90:
            raise ValueError()
        if not re.fullmatch(r'[1-9][0-9]?', limit) or not 1 <= int(limit) <= 10:
            raise ValueError()
        today = (now or datetime.now(timezone.utc)).astimezone(timezone.utc).date()
        latest = today - timedelta(days=2 if report == 'followers' else 1)
        end_date = args.get('end_date', latest.isoformat())
        if not re.fullmatch(r'\d{4}-\d\d-\d\d', end_date):
            raise ValueError()
        end = datetime.fromisoformat(end_date).replace(tzinfo=timezone.utc)
        if end.date() > latest:
            raise ReportError(400, 'end_date_not_available')
        end += timedelta(days=1)
        start = end - timedelta(days=int(days))
        return Request(report, int(start.timestamp()*1000), int(end.timestamp()*1000), int(limit))
    except (ValueError, TypeError, OverflowError):
        raise ReportError(400, 'invalid_input') from None


def encode_response(value):
    data = json.dumps(value, ensure_ascii=False, allow_nan=False, separators=(',', ':')).encode()
    if len(data) > MAX_RESPONSE:
        raise ReportError(502, 'response_too_large')
    return data
