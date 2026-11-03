# Desktop-Läufer: private inherited pipes and length-prefixed JSON only.
# Independently implemented Windows counterpart to script-runner/sandbox/bootstrap.py.
import io
import json
import os
import runpy
import struct
import sys
import threading
import types

_reader = sys.stdin.buffer
_writer = sys.stdout.buffer
_lock = threading.Lock()
_seq = 0


def _read_exact(n):
    result = bytearray()
    while len(result) < n:
        chunk = _reader.read(n - len(result))
        if not chunk:
            raise EOFError()
        result.extend(chunk)
    return result


def _read():
    n, = struct.unpack(">I", _read_exact(4))
    if n == 0 or n > 8 * 1024 * 1024:
        raise ValueError()
    return json.loads(_read_exact(n))


def _write(value):
    body = json.dumps(value, allow_nan=False).encode("utf-8")
    if len(body) > 8 * 1024 * 1024:
        raise ValueError()
    _writer.write(struct.pack(">I", len(body)) + body)
    _writer.flush()


class ToolError(Exception):
    pass


def call(name, arguments=None):
    global _seq
    with _lock:
        _seq += 1
        _write({"type": "tool_call", "id": _seq, "name": name,
                "arguments": {} if arguments is None else arguments})
        response = _read()
        if response.get("id") != _seq or response.get("type") != "tool_result":
            raise ValueError()
        result = response["result"]
        if result.get("isError"):
            raise ToolError()
        return result


def log(*parts):
    # The desktop wire permits log; this client deliberately sends an empty log.
    pass


def frage(text, optionen, frist_s):
    global _seq
    with _lock:
        _seq += 1
        ident = str(_seq)
        _write({"type": "frage", "id": ident, "text": text,
                "optionen": optionen, "frist_s": frist_s})
        response = _read()
        if response.get("type") == "fehler":
            return None
        if response.get("type") != "antwort" or response.get("id") != ident:
            raise ValueError("Ungueltige Antwort")
        return response.get("wahl")


def ergebnis(wert):
    with _lock:
        _write({"type": "ergebnis", "wert": wert})
        response = _read()
        if response.get("type") != "ergebnis" or response.get("ok") is not True:
            raise ValueError("Ergebnis abgewiesen")


def screenshot():
    with _lock:
        _write({"type": "screenshot"})
        response = _read()
        if response.get("type") == "fehler":
            return None
        if response.get("type") != "screenshot" or response.get("ok") is not True:
            raise ValueError("Ungueltige Antwort")
        return True


def main():
    request = _read()
    pin = os.path.realpath(os.path.join(os.path.dirname(__file__), "..", "pin"))
    sys.stdin = io.StringIO(json.dumps(request["input"]))
    sys.stdout = open(os.devnull, "w", encoding="utf-8")
    sys.stderr = open(os.devnull, "w", encoding="utf-8")
    mod = types.ModuleType("januaport")
    mod.input = request["input"]
    mod.call, mod.log, mod.ToolError = call, log, ToolError
    sys.modules["januaport"] = mod
    helper = types.ModuleType("jnpt")
    helper.frage, helper.ergebnis, helper.screenshot = frage, ergebnis, screenshot
    sys.modules["jnpt"] = helper
    sys.path[:0] = [pin, os.path.join(pin, "vendor")]
    code = 0
    try:
        runpy.run_path(os.path.join(pin, request["entry"]), run_name="__main__")
    except SystemExit as exc:
        code = 0 if exc.code is None or exc.code == 0 else 1
    except BaseException:
        code = 1
    _write({"type": "exited", "code": code})
    os._exit(code)


main()
