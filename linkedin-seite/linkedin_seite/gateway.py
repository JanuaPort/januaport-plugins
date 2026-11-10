"""Stufe B1 (#1237) W2: the Beisteller's own read-only MCP client to the gateway /mcp.

JSON-RPC over Streamable HTTP, the legacy chain initialize -> notifications/initialized ->
tools/call, like kartei-rpa/JanuaPort.Kartei.psm1 (Mcp-Protocol-Version is not set; the
gateway derives it from the handshake). The token is the Beisteller's own credential
(Hausmuster #887) and only ever goes to the configured gateway URL. The client calls
EXACTLY two tools (SEC c-6575359082 Bed. 3); any other name is refused before HTTP.
"""
import base64
import binascii
import hashlib
import json
import math
import urllib.error
import urllib.request
from . import redaktion as r
from .domain import ReportError, strict_json
from .oauth import NoRedirect

PROTOCOL_VERSION = '2025-11-25'
CLIENT_INFO = {'name': 'januaport-linkedin-seite', 'version': '0.4.0'}
SESSION_HEADER = 'Mcp-Session-Id'
SMALL_ANSWER = 1024 * 1024
TIMEOUT = 15
FILE_TIMEOUT = 60


def fail(status, code):
    raise ReportError(status, code)


def zu_gross(size, max_bytes):
    if size > max_bytes:
        fail(413, 'medium_zu_gross')


class Medium:
    """The ONE private copy of a file (SEC V2): read once, hashed, uploaded from here, then wiped."""

    def __init__(self, name, daten):
        self.name = name
        self.art = r.medium_art(name)
        self.daten = daten
        self.groesse = len(daten)
        self.sha256 = hashlib.sha256(daten).hexdigest()
        self.geloescht = False

    def loeschen(self):
        self.daten[:] = bytes(len(self.daten))  # overwrite in place (same length), then drop
        self.daten = bytearray()
        self.geloescht = True


class GatewayClient:
    def __init__(self, wcfg, token):
        self.wcfg = wcfg
        self.token = token
        names = wcfg.tools
        self.tool_eintrag, self.tool_datei = names['eintrag'], names['datei']
        self.tools = frozenset(names.values())
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
        self.session = None
        self.ready = False
        self.next_id = 0

    def _post(self, message, limit, timeout):
        headers = {'Authorization': 'Bearer ' + self.token, 'Accept': 'application/json, text/event-stream',
                   'Content-Type': 'application/json'}
        if self.session:
            headers[SESSION_HEADER] = self.session
        body = json.dumps(message, ensure_ascii=False, separators=(',', ':')).encode('utf-8')
        request = urllib.request.Request(self.wcfg.gateway_url, data=body, headers=headers, method='POST')
        try:
            with self._open(request, timeout) as response:
                raw = response.read(limit + 1)
                if len(raw) > limit:  # stop reading: a file answer beyond the size limit is never parsed
                    fail(*((413, 'medium_zu_gross') if limit > SMALL_ANSWER else (502, 'gateway_antwort_ungueltig')))
                session = response.headers.get(SESSION_HEADER)
                if session and all(33 <= ord(c) <= 126 for c in session) and len(session) <= 256:
                    self.session = session
                return response.status, response.headers.get('Content-Type', ''), raw
        except urllib.error.HTTPError as exc:
            status = exc.code
            exc.close()
            fail(502, 'gateway_zugang_verweigert' if status in (401, 403) else 'gateway_abgewiesen')
        except (urllib.error.URLError, OSError, ValueError):
            fail(502, 'gateway_nicht_erreichbar')

    def _open(self, request, timeout):
        return self.opener.open(request, timeout=timeout)

    def _rpc(self, method, params, limit=SMALL_ANSWER, timeout=TIMEOUT):
        self.next_id += 1
        ident = self.next_id
        status, kind, raw = self._post({'jsonrpc': '2.0', 'id': ident, 'method': method, 'params': params}, limit, timeout)
        message = self._message(kind, raw, ident)
        if 'error' in message:
            fail(502, 'gateway_abgewiesen')
        if not isinstance(message.get('result'), dict):
            fail(502, 'gateway_antwort_ungueltig')
        return message['result']

    def _message(self, kind, raw, ident):
        try:
            if kind.split(';')[0].strip().lower() == 'text/event-stream':
                candidates = []
                for event in raw.decode('utf-8').replace('\r\n', '\n').split('\n\n'):
                    data = '\n'.join(line[5:].lstrip(' ') for line in event.split('\n') if line.startswith('data:'))
                    if data:
                        candidates.append(strict_json(data))
            else:
                candidates = [strict_json(raw)]
        except (ValueError, UnicodeError):
            fail(502, 'gateway_antwort_ungueltig')
        found = [m for m in candidates if isinstance(m, dict) and m.get('id') == ident and m.get('jsonrpc') == '2.0']
        if len(found) != 1:
            fail(502, 'gateway_antwort_ungueltig')
        return found[0]

    def _initialize(self):
        if self.ready:
            return
        self._rpc('initialize', {'protocolVersion': PROTOCOL_VERSION, 'capabilities': {}, 'clientInfo': CLIENT_INFO})
        status, _, _ = self._post({'jsonrpc': '2.0', 'method': 'notifications/initialized'}, SMALL_ANSWER, TIMEOUT)
        if status not in (200, 202, 204):
            fail(502, 'gateway_antwort_ungueltig')
        self.ready = True

    def call(self, tool, arguments, limit, timeout=TIMEOUT):
        if tool not in self.tools:
            fail(502, 'gateway_werkzeug_nicht_erlaubt')  # closed list, before any HTTP
        self._initialize()
        return self._rpc('tools/call', {'name': tool, 'arguments': arguments}, limit, timeout)

    def _structured(self, result, key):
        if result.get('isError') is True:
            return None
        payload = result.get('structuredContent')
        if not isinstance(payload, dict) or key not in payload:
            fail(502, 'gateway_antwort_ungueltig')
        return payload[key]

    def eintrag(self, rid):
        record = self._structured(self.call(self.tool_eintrag, {'id': rid}, SMALL_ANSWER), 'record')
        if record is None:
            fail(404, 'eintrag_nicht_gefunden')
        return record

    def datei(self, name, max_bytes):
        r.check_datei(name)  # no path, no '..', nothing absolute: rejected before any call
        # Answer cap from the size limit: base64 grows by 4/3, plus a little JSON around it.
        limit = 4 * math.ceil(max_bytes / 3) + SMALL_ANSWER
        result = self.call(self.tool_datei, {'file': name}, limit, FILE_TIMEOUT)
        if result.get('isError') is True:
            fail(502, 'gateway_werkzeug_fehler')
        meta = result.get('structuredContent')
        content = result.get('content')
        if not isinstance(meta, dict) or meta.get('name') != name or type(meta.get('bytes')) is not int:
            fail(502, 'gateway_antwort_ungueltig')
        zu_gross(meta['bytes'], max_bytes)  # announced size, before decoding
        blobs = [c.get('resource') for c in content if isinstance(c, dict) and c.get('type') == 'resource'] \
            if isinstance(content, list) else []
        if len(blobs) != 1 or not isinstance(blobs[0], dict) or not isinstance(blobs[0].get('blob'), str):
            fail(502, 'gateway_antwort_ungueltig')
        try:
            daten = bytearray(base64.b64decode(blobs[0]['blob'], validate=True))
        except (binascii.Error, ValueError):
            fail(502, 'gateway_antwort_ungueltig')
        zu_gross(len(daten), max_bytes)
        if len(daten) != meta['bytes']:
            fail(502, 'gateway_antwort_ungueltig')
        return Medium(name, daten)
