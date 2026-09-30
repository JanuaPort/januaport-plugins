# Bootstrap des Skript-Läufers (JanuaPort/januaport#928). Der Wächter startet
#   python -I bootstrap.py <pin-ordner> <einstieg>
# Das Skript liest seine Eingabe als JSON von stdin (auch als
# januaport.input), schreibt seine Ausgabe nach stdout und ruft Werkzeuge mit
# januaport.call(name, arguments). stderr geht nirgendwohin: Tracebacks
# verlassen die Sandbox nie (M6). Bei einer Ausnahme meldet der Bootstrap nur
# den Klassennamen und die Stelle im gepinnten Baum.
import io
import json
import os
import runpy
import socket
import struct
import sys
import types

PIN = os.path.realpath(sys.argv[1])
ENTRY = sys.argv[2]

_chan = socket.socket(fileno=3)
_next_id = 0


def _send(obj):
    body = json.dumps(obj).encode("utf-8")
    _chan.sendall(struct.pack(">I", len(body)) + body)


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
    mod.ToolError = ToolError
    sys.modules["januaport"] = mod
    sys.path[:0] = [PIN, os.path.join(PIN, "vendor")]
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
