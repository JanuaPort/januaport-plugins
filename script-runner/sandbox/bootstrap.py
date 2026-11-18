# Bootstrap des Skript-Läufers (JanuaPort/januaport#928). Der Wächter startet
#   python -I bootstrap.py <pin-ordner> <einstieg> [<lock-hash>]
# Das Skript liest seine Eingabe als JSON von stdin (auch als
# januaport.input), schreibt seine Ausgabe nach stdout und ruft Werkzeuge mit
# januaport.call(name, arguments). Mit `etl_stream` im Manifest liest es einen
# Rohstrom mit januaport.etl_stream(...) (Vertragsfassung 3, #1215), siehe dort.
# januaport.log goes only to run _meta (M6).
# stderr geht nirgendwohin: automatische Tracebacks bleiben drinnen (M6).
# Bei einer Ausnahme meldet der Bootstrap nur
# den Klassennamen und die Stelle im gepinnten Baum.
import base64
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

_chan = None  # der Kanal zum Wächter (fd 3); _main öffnet ihn
_next_id = 0
_next_etl_id = 0
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


# --- ETL-Rohstrom (Vertragsfassung 3, JanuaPort/januaport#1215) -----------------
#
# Das Skript hat kein Netz. Der vermittler ruft den Kern und reicht den Strom
# stückweise durch (Pull): auf jedes etl_next folgt genau ein etl_chunk oder
# etl_end. Ein Stück ist an beliebiger Bytegrenze geschnitten; die Zeilen
# (NDJSON) setzt dieses SDK über Stückgrenzen hinweg zusammen.

# Das Gateway schreibt Zeilen bis 1 MiB; ein längerer Rest ohne Zeilenumbruch
# ist kein Strom mehr, den das SDK weiter puffert.
_MAX_LINE = 2 << 20
# Gründe des Endsatzes (geschlossene Menge des Gateways) und des vermittlers;
# alles andere wird nie als Text weitergegeben.
_END_GRUENDE = frozenset([
    "deckel", "wandzeit", "widerruf", "laufende", "freigabe", "integration", "shutdown", "fehler",
    "abgebrochen", "grenze", "vertrag", "geschlossen",
])


class StreamEnded(Exception):
    """Der Strom ist nicht mit dem Endsatz `fertig` zu Ende gegangen. Ein
    unvollständiger Datenstand ist KEIN Erfolg: Den Fehler nicht schlucken und
    nicht übernehmen, was bis hierher gelesen wurde.

    Attribute: grund (deckel, wandzeit, widerruf, laufende, freigabe,
    integration, shutdown, fehler vom Gateway; abgebrochen, grenze, vertrag,
    geschlossen vom vermittler), zeilen (gelesene Zeilen), bytes (gelesene Bytes).
    Der Text ist fest und enthält nie Zeileninhalt."""

    def __init__(self, grund, zeilen=0, bytes=0):
        super().__init__("Der ETL-Strom endete vor dem Endsatz fertig: " + grund)
        self.grund = grund
        self.zeilen = zeilen
        self.bytes = bytes


class StreamRefused(Exception):
    """Das Gateway hat den Strom nicht gestartet (zum Beispiel belegt, Deckel
    erreicht, nicht freigegeben). code ist der HTTP-Status (409, 429, 503, ...),
    retry_after die Wartezeit in Sekunden oder None. Das SDK wiederholt nie
    selbst, und das Skript soll es auch nicht in einer Schleife tun: Die
    Wandzeit des Laufs begrenzt jede Wiederholung."""

    def __init__(self, code=None, retry_after=None):
        super().__init__("Der ETL-Strom wurde abgewiesen.")
        self.code = code
        self.retry_after = retry_after


def _end_grund(value):
    return value if isinstance(value, str) and value in _END_GRUENDE else "vertrag"


def etl_stream(integration, query_id, params=None):
    """Liest den Rohstrom einer deklarierten Abfrage und liefert die Zeilen als
    dicts, eine nach der anderen. Es ist ein Iterator: Nie wird der Strom
    gesammelt, gehalten wird höchstens ein Stück (256 KiB) und eine Teilzeile.

        for row in januaport.etl_stream("erp_db", "offene_posten", {"stichtag": "2026-11-18"}):
            ...

    Der Strom endet normal nur beim Endsatz `fertig`; der Endsatz selbst wird
    nicht geliefert. Jedes andere Ende wirft StreamEnded (auch ein fehlender
    Endsatz: StreamEnded("abgebrochen")), eine Abweisung wirft StreamRefused.
    Pro Lauf gibt es genau einen Strom. Vorzeitiges Verlassen (break, close())
    schließt ihn; der Lauf endet dann als error/stream.

    Regeln (E6): Stromzeilen NIE drucken oder loggen, höchstens Zähler: alles,
    was ein Skript ausgibt, steht im Lauf-Ergebnis. Zuerst in ein Zwischenziel
    schreiben und erst NACH `fertig` übernehmen. Nicht in einer Schleife
    wiederholen. Rohdaten haben die Pseudonymisierung des Gateways nicht
    durchlaufen."""
    global _next_etl_id
    _next_etl_id += 1
    sid = _next_etl_id
    msg = {"type": "etl_open", "id": sid, "integration": integration, "query_id": query_id}
    if params is not None:
        msg["params"] = params
    _send(msg)
    buf = b""
    total = 0
    rows = 0
    finished = False  # der Strom ist beim vermittler zu Ende, ein close ist nicht mehr nötig
    try:
        while True:
            _send({"type": "etl_next", "id": sid})
            reply = _recv()
            kind = reply.get("type")
            if reply.get("id") != sid or kind not in ("etl_chunk", "etl_end"):
                raise StreamEnded("vertrag", rows, total)
            if kind == "etl_end":
                finished = True
                status = reply.get("status")
                if status == "abgewiesen":
                    raise StreamRefused(reply.get("code"), reply.get("retry_after_s"))
                if status == "eof":
                    raise StreamEnded("abgebrochen", rows, total)
                raise StreamEnded(_end_grund(status), rows, total)
            data = base64.b64decode(reply.get("data_b64", ""))
            total += len(data)
            buf += data
            pos = 0
            while True:
                nl = buf.find(b"\n", pos)
                if nl < 0:
                    break
                line = buf[pos:nl]
                pos = nl + 1
                if not line.strip():
                    continue
                try:
                    rec = json.loads(line)
                except ValueError:
                    raise StreamEnded("vertrag", rows, total) from None
                kind = rec.get("t") if isinstance(rec, dict) else None
                if kind == "row" and isinstance(rec.get("d"), dict):
                    rows += 1
                    yield rec["d"]
                elif kind == "end" and isinstance(rec.get("grund"), str):
                    finished = True  # das Gateway beendet den Strom von selbst
                    if rec["grund"] == "fertig":
                        return
                    raise StreamEnded(_end_grund(rec["grund"]), rows, total)
                else:
                    raise StreamEnded("vertrag", rows, total)
            buf = buf[pos:]
            if len(buf) > _MAX_LINE:
                raise StreamEnded("grenze", rows, total)
    finally:
        if not finished:
            try:
                _send({"type": "etl_next", "id": sid, "close": True})
                _recv()  # die Antwort gehört zu diesem close, nie zum nächsten call()
            except Exception:
                pass


def _error_at(exc):
    where = None
    tb = exc.__traceback__
    while tb is not None:
        path = os.path.realpath(tb.tb_frame.f_code.co_filename)
        if path.startswith(PIN + os.sep):
            where = "%s:%d" % (os.path.relpath(path, PIN), tb.tb_lineno)
        tb = tb.tb_next
    return where


def _make_module(raw):
    mod = types.ModuleType("januaport")
    mod.input = json.loads(raw) if raw.strip() else {}
    mod.call = call
    mod.log = log
    mod.ToolError = ToolError
    mod.etl_stream = etl_stream
    mod.StreamEnded = StreamEnded
    mod.StreamRefused = StreamRefused
    return mod


def _main():
    global _chan
    _chan = socket.socket(fileno=3)
    sys.dont_write_bytecode = True
    raw = sys.stdin.read()
    sys.stdin = io.StringIO("")
    sys.modules["januaport"] = _make_module(raw)
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


if __name__ == "__main__":
    _main()
