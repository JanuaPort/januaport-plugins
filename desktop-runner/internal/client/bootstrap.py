# Desktop-Läufer: private inherited pipes, length-prefixed JSON, no sockets.
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
    body = json.dumps(value).encode("utf-8")
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
