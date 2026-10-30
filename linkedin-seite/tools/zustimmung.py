#!/usr/bin/env python3
"""Run on the PO workstation; stdout is exclusively the vault JSON."""
import argparse
import base64
from contextlib import ExitStack
from datetime import datetime, timezone
import errno
import getpass
import hashlib
from http.server import HTTPServer, BaseHTTPRequestHandler
import hmac
import os
from pathlib import Path
import secrets
import select
import socket
import sys
import threading
import time
from urllib.parse import urlencode, urlsplit, parse_qsl
import webbrowser
import warnings

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from linkedin_seite import source_contract as s
from linkedin_seite.oauth import Transport, secret, token_result
from linkedin_seite.domain import encode_response

TIMEOUT = 300


def authorization(client_id, state, verifier=None):
    params = {s.RESPONSE_TYPE: s.CODE, s.CLIENT_ID: client_id, s.REDIRECT: s.REDIRECT_URI,
              s.STATE: state, s.SCOPE: ' '.join(s.SCOPES)}
    if verifier:
        params[s.CODE_CHALLENGE] = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).rstrip(b'=').decode()
        params[s.CODE_CHALLENGE_METHOD] = s.S256
    return s.AUTHORIZE_URL + '?' + urlencode(params)


def callback(target, state):
    url = urlsplit(target)
    pairs = parse_qsl(url.query, keep_blank_values=True, strict_parsing=True)
    values = dict(pairs)
    if (url.scheme or url.netloc or url.fragment or url.path != '/linkedin/callback'
            or len(values) != len(pairs) or set(values) != {s.CODE, s.STATE}
            or not hmac.compare_digest(values.get(s.STATE, '').encode(), state.encode())):
        raise ValueError('zustimmung_abgewiesen')
    return secret(values[s.CODE])


class CallbackServer(HTTPServer):
    def __init__(self, address, handler, family=socket.AF_INET):
        self.address_family = family
        super().__init__(address, handler)

    def server_bind(self):
        if hasattr(socket, 'SO_EXCLUSIVEADDRUSE'):
            self.allow_reuse_address = False
            self.socket.setsockopt(socket.SOL_SOCKET, socket.SO_EXCLUSIVEADDRUSE, 1)
        if self.address_family == socket.AF_INET6:
            self.socket.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_V6ONLY, 1)
        super().server_bind()

    def get_request(self):
        sock, addr = super().get_request()
        sock.settimeout(max(0.001, self.deadline - time.monotonic()))
        return sock, addr

    def handle_error(self, *args):
        pass  # no request bytes, codes, state or tracebacks


def consent(client_id, client_secret, pkce=False, *, opener=webbrowser.open, transport=None, timeout=TIMEOUT):
    deadline = time.monotonic() + timeout
    secret(client_id)
    secret(client_secret)
    state = secrets.token_urlsafe(32)
    verifier = secrets.token_urlsafe(64) if pkce else None
    received = []

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def send_error(self, code, message=None, explain=None):
            self.send_response(code)
            self.send_header('Connection', 'close')
            self.end_headers()
            self.wfile.write(b'zustimmung_abgewiesen')

        def do_GET(self):
            try:
                received.append(callback(self.path, state))
                self.send_response(200)
                self.send_header('Connection', 'close')
                self.end_headers()
                self.wfile.write(b'Zustimmung empfangen. Dieses Fenster schliessen.')
            except (ValueError, UnicodeError):
                self.send_error(400)
            self.close_connection = True

    # Two explicit loopbacks, one shared attempt, including invalid requests.
    with ExitStack() as stack:
        servers = [stack.enter_context(CallbackServer(('127.0.0.1', 8765), Handler))]
        try:
            servers.append(stack.enter_context(CallbackServer(('::1', 8765), Handler, socket.AF_INET6)))
        except OSError as exc:
            if exc.errno == errno.EADDRINUSE:
                print('IPv6-Port [::1]:8765 ist belegt; Zustimmung abgebrochen.', file=sys.stderr)
                raise ValueError('zustimmung_abgewiesen') from None
            if exc.errno not in (errno.EAFNOSUPPORT, errno.EADDRNOTAVAIL, errno.EPROTONOSUPPORT):
                print('IPv6-Listener fehlgeschlagen; Zustimmung abgebrochen.', file=sys.stderr)
                raise ValueError('zustimmung_abgewiesen') from None
            print('IPv6 nicht verfuegbar; weiter auf 127.0.0.1.', file=sys.stderr)
        for server in servers:
            server.deadline = deadline
        if not opener(authorization(client_id, state, verifier)):
            raise ValueError('browser_start_failed')
        ready, _, _ = select.select(servers, [], [], max(0, deadline - time.monotonic()))
        for server in ready:
            server._handle_request_noblock()
            break  # first request consumes both listeners, regardless of validity
    if len(received) != 1:
        raise ValueError('zustimmung_abgewiesen')
    fields = {s.GRANT_TYPE: s.AUTH_CODE, s.CODE: received[0], s.REDIRECT: s.REDIRECT_URI,
              s.CLIENT_ID: client_id, s.CLIENT_SECRET: client_secret}
    if verifier:
        fields[s.CODE_VERIFIER] = verifier
    transport = transport or Transport(lambda: None)
    remaining = deadline - time.monotonic()
    if remaining <= 0:
        raise ValueError('zustimmung_abgewiesen')
    transport.timeout = min(18, remaining)
    raw = transport.token(fields)
    result = token_result(raw, datetime.now(timezone.utc))
    result.update({s.CLIENT_ID: client_id, s.CLIENT_SECRET: client_secret})
    return result


def main():
    class SafeParser(argparse.ArgumentParser):
        def error(self, message):
            self.exit(2, 'ungueltige_argumente\n')

    parser = SafeParser(description='LinkedIn-Zustimmung am PO-Rechner; stdout direkt in den Tresor pipen.')
    parser.add_argument('--client-id', required=True)
    parser.add_argument('--secret-stdin', action='store_true')
    parser.add_argument('--pkce', action='store_true', help='Nur nach belegter S256-Unterstützung der LinkedIn-App verwenden.')
    args = parser.parse_args()
    timer = None
    try:
        with warnings.catch_warnings():
            warnings.simplefilter('error', getpass.GetPassWarning)
            value = sys.stdin.readline(8194).rstrip('\r\n') if args.secret_stdin else getpass.getpass('Client-Secret: ')
        def expired():
            # Hard wall-clock deadline also covers slow headers/token responses.
            # Only this standalone helper process exits; stdout is still empty.
            os.write(2, b'zustimmung_timeout\n')
            os._exit(1)
        timer = threading.Timer(TIMEOUT, expired)
        timer.daemon = True
        timer.start()
        result = consent(args.client_id, value, args.pkce)
        print('Refresh-Token: ' + ('ja' if s.REFRESH_TOKEN in result else 'nein'), file=sys.stderr)
        print(encode_response(result).decode())
    except (Exception, KeyboardInterrupt):
        print('zustimmung_fehlgeschlagen', file=sys.stderr)
        return 1
    finally:
        if timer:
            timer.cancel()
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
