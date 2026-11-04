"""Stufe B1 (#1237): the write adapter. Runs in its own spawned job process per action.

Order of every action (SEC c-6575359082, c-6575837332):
  1. read the entry and each file ONCE from the gateway (private copy in RAM, size checked),
  2. hash the complete effect, compare with the exact core approval header,
  3. only then count the action and talk to LinkedIn, from the private copy,
  4. wipe the copies on every path.
LinkedIn calls go only to the closed list in source_contract.WRITE_ENDPOINTS plus validated
upload URLs. Author/owner/actor is always the organization of the operator configuration.
"""
import json
import time
import urllib.error
import urllib.request
from urllib.parse import quote
from . import redaktion as r
from . import source_contract as s
from .domain import ReportError, strict_json
from .gateway import GatewayClient
from .oauth import MAX_SOURCE, Transport

API_TIMEOUT = 18
UPLOAD_TIMEOUT = 120


class Erledigt(Exception):
    """The server already holds the result for this eintrag_id + hash: nothing is sent again."""


class WriteTransport(Transport):
    """Same fixed HTTPS transport (no proxy, no redirect, no retry); returns status and headers too."""

    def exchange(self, request, expect_json=True, timeout=API_TIMEOUT, *, before_send=None):
        self.permit()  # server-process grant before every LinkedIn attempt, failures included
        self.timeout = timeout
        if before_send is not None:
            before_send()  # after local quota checks, immediately before possible provider effect
        try:
            with self._open(request) as response:
                raw = response.read(MAX_SOURCE + 1)
                if len(raw) > MAX_SOURCE:
                    raise ReportError(502, 'response_too_large')
                headers = {key.lower(): value for key, value in response.headers.items()}
                data = strict_json(raw) if expect_json else None
                return response.status, headers, data
        except urllib.error.HTTPError as exc:
            status = exc.code
            exc.close()
            code = ('source_umleitung_abgewiesen' if 300 <= status < 400 else
                    'source_access_denied' if status in (401, 403) else
                    'source_quota_limited' if status == 429 else
                    'source_unavailable' if status >= 500 else 'source_rejected')
            raise ReportError(502, code) from None
        except (ValueError, UnicodeError):
            raise ReportError(502, 'unexpected_source_shape') from None
        except (OSError, urllib.error.URLError):
            raise ReportError(502, 'source_unavailable') from None


def shape():
    raise ReportError(502, 'unexpected_source_shape')


def value_of(data, key, pattern=None):
    value = data.get(key) if isinstance(data, dict) else None
    if not isinstance(value, str) or (pattern is not None and not pattern.fullmatch(value)):
        shape()
    return value


class PostAdapter:
    def __init__(self, cfg, tokens, gateway_token, link, *, transport_factory=WriteTransport,
                 gateway_factory=GatewayClient, sleep=time.sleep, monotonic=time.monotonic):
        self.cfg, self.w = cfg, cfg.schreiben
        self.link = link
        self.transport = transport_factory(link.permit_http)
        self.tokens = tokens  # anything with access(): in a job the pipe to the server process
        self.gateway = gateway_factory(self.w, gateway_token)
        self.sleep, self.monotonic = sleep, monotonic
        self.begonnen = False
        self.kopien = []
        self.schreibversuch = False

    # --- reading and approval ---------------------------------------------------------------

    def _laden(self, req):
        entry = r.entry_from_record(self.gateway.eintrag(req.eintrag_id), req.eintrag_id, req.aktion)
        fmt = r.check_format(entry, req.aktion)
        gesamt = 0
        for name in entry.medien:
            medium = self.gateway.datei(name, self.w.max_medium_bytes)
            self.kopien.append(medium)
            gesamt += medium.groesse
            if gesamt > r.MAX_MEDIEN_GESAMT:
                raise ReportError(413, 'medium_zu_gross')
            r.check_magic(medium.art, medium.daten)
        media = list(self.kopien)
        effect = r.wirkung(req.aktion, entry, media, self.cfg.organization)
        return entry, media, fmt, r.wirkung_hash(effect), effect

    def vorschau(self, req):
        try:
            entry, media, fmt, digest, effect = self._laden(req)
            return {'eintrag_id': req.eintrag_id, 'aktion': req.aktion, 'format': fmt,
                    'ziel_urn': effect['ziel_urn'], 'text': effect['text'],
                    'medien': [{'name': m.name, 'sha256': m.sha256, 'datei': m.name,
                                'art': m.art, 'bytes': m.groesse} for m in media],
                    'umfrage': effect['umfrage'],
                    'post_text': entry.post_text if req.aktion in ('posten', 'bearbeiten') else None,
                    'post_urn': entry.post_urn, 'kommentar_text': entry.kommentar_text if req.aktion == 'kommentar' else None,
                    'dokument_titel': media[0].name if fmt == 'dokument' else None,
                    'hash': digest}
        finally:
            self._wipe()

    def run(self, req):
        try:
            entry, media, fmt, digest, effect = self._laden(req)
            if req.freigabe != digest:
                raise ReportError(409, 'nicht_freigegeben')
            if not self.link.report_hash(digest):
                raise Erledigt()
            result = {'aktion': req.aktion, 'eintrag_id': req.eintrag_id, 'hash': digest, 'format': fmt}
            if req.aktion == 'posten':
                result.update(self._posten(entry, media, fmt))
                result['state'] = 'veroeffentlicht'
            elif req.aktion == 'bearbeiten':
                self.api('post_update', urn=entry.post_urn,
                         body={s.PATCH: {s.SET: {s.COMMENTARY: r.little(entry.post_text)}}}, expect_json=False)
                result.update({'state': 'bearbeitet', 'post_urn': entry.post_urn})
            elif req.aktion == 'loeschen':
                self.api('post_delete', urn=entry.post_urn, expect_json=False)
                result.update({'state': 'geloescht', 'post_urn': entry.post_urn})
            else:
                _, headers, _ = self.api('comment_create', urn=entry.post_urn, expect_json=False, body={
                    s.ACTOR: self.cfg.organization, s.OBJECT: entry.post_urn, s.MESSAGE: {s.MESSAGE_TEXT: entry.kommentar_text}})
                result.update({'state': 'kommentiert', 'post_urn': entry.post_urn,
                               'kommentar_id': value_of(headers, s.RESTLI_ID, r.COMMENT_ID)})
            result['bytes'] = sum(m.groesse for m in media)
            result['ausgefuehrt'] = True
            return result
        except ReportError as exc:
            exc.nicht_ausgefuehrt = not self.schreibversuch
            raise
        finally:
            self._wipe()

    def _wipe(self):
        for medium in self.kopien:
            medium.loeschen()

    # --- LinkedIn ----------------------------------------------------------------------------

    def _begin(self):
        if not self.begonnen:
            self.link.begin_action()  # counts once, before the first LinkedIn request of the action
            self.begonnen = True

    def api(self, op, urn=None, body=None, expect_json=True):
        if op not in s.WRITE_ENDPOINTS:
            raise ReportError(404, 'route_not_found')
        method, template = s.WRITE_ENDPOINTS[op]
        path = template.format(urn=quote(urn, safe='')) if '{urn}' in template else template
        self._begin()
        headers = {'Authorization': 'Bearer ' + self.tokens.access(), s.VERSION_HEADER: self.cfg.linkedin_version,
                   s.PROTOCOL_HEADER: s.PROTOCOL_VERSION}
        if op in s.RESTLI_METHODS:
            headers[s.RESTLI_METHOD] = s.RESTLI_METHODS[op]
        data = None
        if body is not None:
            headers['Content-Type'] = 'application/json'
            data = json.dumps(body, ensure_ascii=False, separators=(',', ':')).encode('utf-8')
        request = urllib.request.Request(s.API_ROOT + path, data=data, headers=headers, method=method)
        status, response_headers, data = self.transport.exchange(
            request, expect_json=expect_json, before_send=self._schreiben if method != 'GET' else None)
        expected = 201 if op in ('post_create', 'comment_create') else 204 if op in ('post_update', 'post_delete') else 200
        if status != expected:
            shape()
        return status, response_headers, data

    def upload(self, url, daten, art):
        ziel = r.upload_ziel(url)  # SEC V1: unknown host, http, port, userinfo: abort before the first byte
        self._begin()
        headers = {'Content-Type': 'application/octet-stream'}
        if s.UPLOAD_TOKEN[art]:
            headers['Authorization'] = 'Bearer ' + self.tokens.access()
        request = urllib.request.Request(ziel, data=daten, headers=headers, method=s.UPLOAD_METHOD)
        _, response_headers, _ = self.transport.exchange(
            request, expect_json=False, timeout=UPLOAD_TIMEOUT, before_send=self._schreiben)
        return response_headers

    def _schreiben(self):
        # After the HTTP permit, before sending: includes init, edit, delete and upload.
        self.link.write_request()
        self.schreibversuch = True

    def _asset(self, op, field, medium):
        _, _, data = self.api(op, body={s.INIT_REQUEST: {s.OWNER: self.cfg.organization}})
        value = data.get(s.VALUE) if isinstance(data, dict) else None
        urn = value_of(value, field, r.ASSET_URN[field])
        self.upload(value_of(value, s.UPLOAD_URL), medium.daten, medium.art)
        return urn

    def _video(self, medium):
        _, _, data = self.api('video_init', body={s.INIT_REQUEST: {
            s.OWNER: self.cfg.organization, s.FILE_SIZE: medium.groesse, s.UPLOAD_CAPTIONS: False, s.UPLOAD_THUMBNAIL: False}})
        value = data.get(s.VALUE) if isinstance(data, dict) else None
        urn = value_of(value, s.VIDEO, r.ASSET_URN[s.VIDEO])
        token = value_of(value, s.UPLOAD_TOKEN_FIELD)
        teile = r.video_teile(value.get(s.UPLOAD_INSTRUCTIONS), medium.groesse)  # all parts checked before one PUT
        etags = []
        for url, first, last in teile:
            headers = self.upload(url, memoryview(medium.daten)[first:last + 1], 'video')  # from the one copy
            etag = headers.get(s.ETAG)
            if not isinstance(etag, str) or not 0 < len(etag) <= 1024 or any(not 32 <= ord(c) <= 126 for c in etag):
                shape()
            etags.append(etag)
        self.api('video_finalize', expect_json=False, body={s.FINALIZE_REQUEST: {
            s.VIDEO: urn, s.UPLOAD_TOKEN_FIELD: token, s.UPLOADED_PARTS: etags}})
        deadline = self.monotonic() + s.VIDEO_MAX_WAIT_SECONDS
        while True:  # SEC V3: fixed spacing, fixed maximum, post only on AVAILABLE
            _, _, state = self.api('video_status', urn=urn)
            status = state.get(s.STATUS) if isinstance(state, dict) else None
            if status == s.VIDEO_AVAILABLE:
                return urn
            if status not in s.VIDEO_PENDING:
                raise ReportError(502, 'video_verarbeitung_fehlgeschlagen')
            if self.monotonic() + s.VIDEO_POLL_SECONDS > deadline:
                raise ReportError(502, 'video_nicht_verfuegbar')
            self.sleep(s.VIDEO_POLL_SECONDS)

    def _posten(self, entry, media, fmt):
        out = {}
        if fmt == 'bild':
            content = {s.MEDIA: {'id': self._asset('image_init', s.IMAGE, media[0])}}
        elif fmt == 'bilder':
            content = {s.MULTI_IMAGE: {s.IMAGES: [{'id': self._asset('image_init', s.IMAGE, m)} for m in media]}}
        elif fmt == 'dokument':
            content = {s.MEDIA: {'id': self._asset('document_init', s.DOCUMENT, media[0]), s.TITLE: media[0].name}}
        elif fmt == 'video':
            out['video_urn'] = self._video(media[0])
            content = {s.MEDIA: {'id': out['video_urn']}}
        elif fmt == 'umfrage':
            poll = entry.umfrage
            content = {s.POLL: {s.POLL_QUESTION: poll.frage, s.POLL_OPTIONS: [{s.POLL_TEXT: a} for a in poll.antworten],
                                s.POLL_SETTINGS: {s.POLL_DURATION: s.POLL_DURATIONS[poll.dauer]}}}
        else:
            content = None
        body = {'author': self.cfg.organization, s.COMMENTARY: r.little(entry.post_text), **s.POST_SHELL}
        if content is not None:
            body[s.CONTENT] = content
        _, headers, _ = self.api('post_create', body=body, expect_json=False)
        out['post_urn'] = value_of(headers, s.RESTLI_ID, r.POST_URN)
        return out
