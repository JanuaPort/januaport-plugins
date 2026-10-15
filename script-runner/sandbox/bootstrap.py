# Bootstrap des Skript-Läufers (JanuaPort/januaport#928). Der Wächter startet
#   python -I bootstrap.py <pin-ordner> <einstieg> [<lock-hash>]
# Das Skript liest seine Eingabe als JSON von stdin (auch als
# januaport.input), schreibt seine Ausgabe nach stdout und ruft Werkzeuge mit
# januaport.call(name, arguments). januaport.log goes only to run _meta (M6).
# stderr geht nirgendwohin: automatische Tracebacks bleiben drinnen (M6).
# Bei einer Ausnahme meldet der Bootstrap nur
# den Klassennamen und die Stelle im gepinnten Baum.
import io
import json
import os
import runpy
import socket
import struct
import sys
import threading
import types

PIN = os.path.realpath(sys.argv[1])
ENTRY = sys.argv[2]
# Bibliotheken (#1041): Lock-Hash aus dem job, vom Wächter als 64 Hex geprüft.
# Das Nur-Lese-Volume liegt unter /libs/<hash>. Es kommt per sys.path.append
# in den Suchpfad, nie über site: keine .pth-Dateien, keine Entry-Points.
LIBS_ROOT = "/libs"
LIBS = sys.argv[3] if len(sys.argv) > 3 else None

_chan = socket.socket(fileno=3)
_next_id = 0
_send_lock = threading.Lock()


def _send(obj):
    body = json.dumps(obj).encode("utf-8")
    with _send_lock:
        _chan.sendall(struct.pack(">I", len(body)) + body)


def log(*parts):
    """Append a line to the run log in _meta; never to daemon logs.

    Like print, parts are separated by a space; a final newline is always added.
    The guard caps the run log at 64 KiB, including its truncation marker.
    """
    text = " ".join(part if isinstance(part, str) else str(part) for part in parts) + "\n"
    # Even escaped Unicode stays well below the frame reader's byte limit.
    for start in range(0, len(text), 8192):
        _send({"type": "log", "data": text[start:start + 8192]})


def _recv_exact(n):
    buf = b""
    while len(buf) < n:
        chunk = _chan.recv(n - len(buf))
        if not chunk:
            raise ConnectionError("Verbindung zum Wächter beendet")
        buf += chunk
    return buf


def _recv():
    (n,) = struct.unpack(">I", _recv_exact(4))
    return json.loads(_recv_exact(n))


class ToolError(Exception):
    """Ein Werkzeug hat mit isError geantwortet."""


def call(name, arguments=None):
    """Ruft ein im Manifest deklariertes JanuaPort-Werkzeug auf und gibt das
    CallToolResult als dict zurück. Wirft ToolError bei isError."""
    global _next_id
    _next_id += 1
    _send({"type": "tool_call", "id": _next_id, "name": name, "arguments": arguments or {}})
    msg = _recv()
    result = msg.get("result") or {}
    if result.get("isError"):
        texts = [c.get("text", "") for c in result.get("content") or [] if isinstance(c, dict)]
        raise ToolError(" ".join(texts) or name)
    return result


def _error_at(exc):
    where = None
    tb = exc.__traceback__
    while tb is not None:
        path = os.path.realpath(tb.tb_frame.f_code.co_filename)
        if path.startswith(PIN + os.sep):
            where = "%s:%d" % (os.path.relpath(path, PIN), tb.tb_lineno)
        tb = tb.tb_next
    return where


def _main():
    sys.dont_write_bytecode = True
    raw = sys.stdin.read()
    sys.stdin = io.StringIO("")
    mod = types.ModuleType("januaport")
    mod.input = json.loads(raw) if raw.strip() else {}
    mod.call = call
    mod.log = log
    mod.ToolError = ToolError
    sys.modules["januaport"] = mod
    sys.path[:0] = [PIN, os.path.join(PIN, "vendor")]
    if LIBS is not None:
        if len(LIBS) != 64 or any(c not in "0123456789abcdef" for c in LIBS):
            os._exit(1)
        sys.path.append(os.path.join(LIBS_ROOT, LIBS))
    code = 0
    try:
        runpy.run_path(os.path.join(PIN, ENTRY), run_name="__main__")
    except SystemExit as e:
        if e.code is None:
            code = 0
        elif isinstance(e.code, int):
            code = e.code
        else:
            code = 1
    except BaseException as e:  # noqa: B036 — jede Ausnahme endet hier, ohne Text
        report = {"type": "error", "error_type": type(e).__name__}
        where = _error_at(e)
        if where:
            report["error_at"] = where
        try:
            _send(report)
        except Exception:
            pass
        code = 1
    try:
        sys.stdout.flush()
    except Exception:
        pass
    os._exit(code)


_main()
