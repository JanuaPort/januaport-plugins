"""Fixed single-GET REST adapter. Discard breakdowns before aggregate projection."""
from datetime import datetime, timezone
import re
import urllib.request
from urllib.parse import urlencode, quote
from . import source_contract as s
from .domain import REPORTS, ReportError
from .oauth import Tokens, Transport


def shape():
    raise ReportError(502, 'unexpected_source_shape')


def number(value):
    if type(value) is not int or value < 0:
        shape()
    return value


def sum_counter(values, key, result):
    values = list(values)
    if any(type(value) is not int for value in values):
        shape()
    if key != s.REACTION and any(value < 0 for value in values):
        shape()
    total = sum(values)
    if total < 0:
        result['korrigiert'] = True
    return max(0, total)


def project_shares(rows, post=False):
    fields = {'impressionen': s.IMPRESSION, 'klicks': s.CLICK}
    if post:
        fields.update({'reaktionen': s.REACTION, 'kommentare': s.COMMENT, 'shares': s.SHARE})
    result = {}
    for out, key in fields.items():
        result[out] = sum_counter((path(row, key) for row in rows), key, result)
    if post and not rows:
        result['ohne_interaktion'] = True
    return result


def mentioned(value):
    if isinstance(value, str):
        return any(marker in value.lower() for marker in s.MENTION_MARKERS)
    if isinstance(value, dict):
        return any(mentioned(key) or mentioned(item) for key, item in value.items())
    if isinstance(value, list):
        return any(mentioned(item) for item in value)
    return False


def minimize_posts(raw):
    # Inspect the entire post before discarding metadata, including future fields.
    # Only the suppression decision crosses the minimization boundary.
    rows = []
    for row in elements(raw):
        attributes = row.get(s.ATTRIBUTES, [])
        if not isinstance(attributes, list) or any(not isinstance(a, dict) for a in attributes):
            shape()
        text = path(row, s.TEXT)
        if not isinstance(text, str):
            shape()
        clean = minimize({s.ELEMENTS: [row]}, (s.ID, s.AUTHOR, s.CREATED, s.LIFECYCLE))[0]
        clean[s.TEXT] = None if mentioned(row) else text
        rows.append(clean)
    return rows


def path(data, *keys):
    for key in keys:
        if not isinstance(data, dict) or key not in data:
            shape()
        data = data[key]
    return data


def elements(raw):
    rows = path(raw, s.ELEMENTS)
    if not isinstance(rows, list) or any(not isinstance(r, dict) for r in rows):
        shape()
    return rows


def ensure_complete(raw):
    if not isinstance(raw, dict):
        shape()
    paging = raw.get(s.PAGING, {})
    if not isinstance(paging, dict) or not isinstance(paging.get(s.LINKS, []), list):
        shape()
    if any(not isinstance(link, dict) or link.get(s.REL) == s.NEXT for link in paging.get(s.LINKS, [])):
        shape()  # no partial sum and no following arbitrary links


def minimize(raw, keys):
    # Positive allowlist: demographics and all unknown fields are discarded here,
    # before downstream aggregation, including future breakdown names.
    return [{k: row[k] for k in keys if k in row} for row in elements(raw)]


class LinkedInAdapter:
    def __init__(self, cfg, identity, permit, transport_factory=Transport):
        self.cfg = cfg
        self.transport = transport_factory(permit)
        self.tokens = Tokens(identity, self.transport)

    def get(self, endpoint, params):
        if endpoint not in (s.PAGE, s.FOLLOWERS, s.SHARES, s.POSTS, s.NETWORK):
            raise ReportError(404, 'route_not_found')
        if endpoint == s.NETWORK:
            endpoint += '/' + quote(self.cfg.organization, safe='')
            params = {s.EDGE_TYPE: s.FOLLOWED_BY_MEMBER}
        token = self.tokens.access()
        headers = {'Authorization': 'Bearer ' + token, s.VERSION_HEADER: self.cfg.linkedin_version,
                   s.PROTOCOL_HEADER: s.PROTOCOL_VERSION}
        return self.transport.send(urllib.request.Request(s.API_ROOT + endpoint + '?' + urlencode(params),
                                   headers=headers, method='GET'))

    def stats(self, endpoint, aggregate, req=None, post=None):
        org_key = s.ORGANIZATION if endpoint == s.PAGE else s.ENTITY
        params = {s.Q: org_key, org_key: self.cfg.organization}
        if req:
            params[s.INTERVALS] = s.INTERVAL_FORMAT.format(start=req.start, end=req.end)
        if post:
            params[s.SHARE_IDS if post.startswith('urn:li:share:') else s.UGC_IDS] = 'List(' + post + ')'
        raw = self.get(endpoint, params)
        ensure_complete(raw)
        if post:
            if s.NEXT in raw:
                shape()  # unknown top-level continuation; paging validated above
            if org_key in raw and raw[org_key] != self.cfg.organization:
                shape()
            for field in (s.SHARE_ID, s.UGC_ID):
                if field in raw and raw[field] != post:
                    shape()
        rows = minimize(raw, (org_key, aggregate, s.INTERVAL, s.SHARE_ID, s.UGC_ID))
        if post and len(rows) > 1:
            shape()
        if post and not rows:
            return []  # documented absence means no interaction, only for a post
        if not rows or any(path(row, org_key) != self.cfg.organization for row in rows):
            shape()
        if post and any(path(row, s.SHARE_ID if post.startswith('urn:li:share:') else s.UGC_ID) != post for row in rows):
            shape()
        if post and any(field in row and row[field] != post for row in rows for field in (s.SHARE_ID, s.UGC_ID)):
            shape()
        if req:
            actual = [(number(path(r, s.INTERVAL, s.START)), number(path(r, s.INTERVAL, s.END))) for r in rows]
            expected = [(start, start + 86400000) for start in range(req.start, req.end, 86400000)]
            if sorted(actual) != expected:
                shape()  # gaps, duplicates, overlaps and foreign dates are not zero
        elif not post and len(rows) != 1:
            shape()
        values = [path(row, aggregate) for row in rows]
        # Strip nested breakdowns too, before the numeric SUM projection.
        if aggregate == s.PAGE_STATS:
            return [{s.VIEWS: {s.ALL_VIEWS: {s.PAGE_VIEWS: path(v, s.VIEWS, s.ALL_VIEWS, s.PAGE_VIEWS)}}} for v in values]
        keys = ((s.ORGANIC_GAIN, s.PAID_GAIN) if aggregate == s.FOLLOWER_GAIN else
                (s.IMPRESSION, s.CLICK, s.REACTION, s.COMMENT, s.SHARE) if post else
                (s.IMPRESSION, s.CLICK))
        return [{key: path(value, key) for key in keys} for value in values]

    def share_sums(self, req=None, post=None):
        rows = self.stats(s.SHARES, s.SHARE_STATS, req, post)
        return project_shares(rows, post=post)

    def run(self, req):
        if req.report not in REPORTS:
            raise ReportError(404, 'route_not_found')
        if req.report == 'page_statistics':
            rows = self.stats(s.PAGE, s.PAGE_STATS, req)
            result = self.share_sums(req)
            result['seitenaufrufe'] = sum(number(path(r, s.VIEWS, s.ALL_VIEWS, s.PAGE_VIEWS)) for r in rows)
        elif req.report == 'followers':
            total = self.get(s.NETWORK, {})
            result = {}
            result['gesamt'] = number(path(total, s.NETWORK_SIZE))
            gains = self.stats(s.FOLLOWERS, s.FOLLOWER_GAIN, req)
            result['zuwachs'] = sum(number(path(r, s.ORGANIC_GAIN)) + number(path(r, s.PAID_GAIN)) for r in gains)
        else:
            raw = self.get(s.POSTS, {s.Q: s.AUTHOR, s.AUTHOR: self.cfg.organization,
                                   s.COUNT: req.limit, s.START: 0, s.SORT: s.CREATED_SORT})
            rows = minimize_posts(raw)
            if len(rows) > req.limit:
                shape()
            result, seen, previous = [], set(), None
            for row in rows:
                identifier, text = path(row, s.ID), path(row, s.TEXT)
                if (path(row, s.AUTHOR) != self.cfg.organization or path(row, s.LIFECYCLE) != s.PUBLISHED
                        or not isinstance(identifier, str) or not re.fullmatch(r'urn:li:(?:share|ugcPost):[0-9]{1,30}', identifier)
                        or identifier in seen):
                    shape()
                seen.add(identifier)
                created = number(path(row, s.CREATED))
                if previous is not None and created > previous:
                    shape()
                previous = created
                try:
                    date = datetime.fromtimestamp(created / 1000, timezone.utc).date().isoformat()
                except (ValueError, OSError, OverflowError):
                    shape()
                item = {'datum': date, 'id': identifier}
                if text is None:
                    item.update({'text': None, 'text_ausgelassen': 'erwaehnung'})
                else:
                    item['textanfang'] = text[:120]
                item.update(self.share_sums(post=identifier))
                result.append(item)
        out = {'bericht': req.report, 'summen' if req.report != 'posts' else 'beitraege': result}
        if req.report != 'posts':
            out['zeitraum'] = {'start': datetime.fromtimestamp(req.start/1000, timezone.utc).date().isoformat(),
                               'ende_exklusiv': datetime.fromtimestamp(req.end/1000, timezone.utc).date().isoformat()}
        return out
