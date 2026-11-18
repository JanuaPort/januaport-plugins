"""Tests des ETL-SDK in bootstrap.py (JanuaPort/januaport#1215 P2).

Aufruf: python test_etl_sdk.py  (Standardbibliothek, kein Docker, kein Netz).
bootstrap.py wird als Modul geladen; der Kanal zum Wächter ist ein Fake, der
den vermittler nachstellt (etl_open/etl_next -> etl_chunk/etl_end).
"""
import base64
import gc
import importlib.util
import json
import os
import struct
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))


def load_bootstrap():
    sys.argv = ["bootstrap.py", HERE, "main.py"]
    spec = importlib.util.spec_from_file_location("bootstrap_under_test", os.path.join(HERE, "bootstrap.py"))
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


def end_line(grund, zeilen=0, nbytes=0):
    return ('{"t":"end","grund":"%s","zeilen":%d,"bytes":%d}\n' % (grund, zeilen, nbytes)).encode()


def row_line(d):
    return (json.dumps({"t": "row", "d": d}, ensure_ascii=False) + "\n").encode("utf-8")


class FakeChan:
    """Stellt Wächter und vermittler nach. chunks: Liste von bytes (so geschnitten
    wie geliefert); final: Antwort, wenn die Stücke aufgebraucht sind; replies:
    fertige Antworten statt Stücke."""

    def __init__(self, chunks=(), final=None, replies=None):
        self.chunks = list(chunks)
        self.final = final if final is not None else {"status": "eof"}
        self.replies = replies
        self.sent = []
        self.inbox = bytearray()

    # --- Bootstrap-Seite
    def sendall(self, data):
        (n,) = struct.unpack(">I", data[:4])
        assert len(data) == 4 + n, "genau ein Rahmen je sendall"
        msg = json.loads(data[4:])
        self.sent.append(msg)
        self.answer(msg)

    def recv(self, n):
        out = bytes(self.inbox[:n])
        del self.inbox[:n]
        return out

    # --- vermittler-Seite
    def push(self, obj):
        body = json.dumps(obj).encode()
        self.inbox += struct.pack(">I", len(body)) + body

    def answer(self, msg):
        t = msg["type"]
        if t == "etl_next":
            if msg.get("close"):
                self.push({"type": "etl_end", "id": msg["id"], "status": "geschlossen"})
            elif self.replies is not None:
                if self.replies:
                    self.push(dict(self.replies.pop(0), id=msg["id"]))
            elif self.chunks:
                self.push({"type": "etl_chunk", "id": msg["id"], "data_b64": base64.b64encode(self.chunks.pop(0)).decode()})
            else:
                self.push(dict(self.final, type="etl_end", id=msg["id"]))
        elif t == "tool_call":
            self.push({"type": "tool_result", "id": msg["id"], "result": {"content": [{"type": "text", "text": "{}"}]}})

    def types(self):
        return [(m["type"], bool(m.get("close"))) for m in self.sent]


def cut(data, sizes):
    out, i = [], 0
    for s in sizes:
        out.append(data[i:i + s])
        i += s
    if i < len(data):
        out.append(data[i:])
    return [c for c in out if c]


class EtlSdkTest(unittest.TestCase):
    def setUp(self):
        self.b = load_bootstrap()

    def use(self, chan):
        self.b._chan = chan
        return chan

    def test_module_exports(self):
        mod = self.b._make_module("{}")
        for name in ("etl_stream", "StreamEnded", "StreamRefused", "call", "log", "ToolError", "input"):
            self.assertTrue(hasattr(mod, name), name)

    def test_lines_assembled_across_chunk_boundaries_and_multibyte(self):
        rows = [{"nr": i, "name": "Müller-Straße ä€ %d" % i} for i in range(40)]
        body = b"".join(row_line(r) for r in rows) + end_line("fertig", 40)
        # absichtlich krumme Schnitte, auch mitten in einem Mehrbyte-Zeichen
        chunks = cut(body, [5, 1, 2, 31, 7, 100, 3, 250, 11])
        chan = self.use(FakeChan(chunks))
        got = list(self.b.etl_stream("erp", "q"))
        self.assertEqual(got, rows)
        # nach dem Endsatz fertig: kein weiteres etl_next, kein close
        types = chan.types()
        self.assertEqual(types[0], ("etl_open", False))
        self.assertNotIn(("etl_next", True), types)
        self.assertEqual(types.count(("etl_next", False)), len(chunks))
        self.assertEqual(chan.chunks, [])

    def test_rows_are_dicts_and_end_line_not_yielded(self):
        body = row_line({"a": 1}) + end_line("fertig", 1)
        self.use(FakeChan([body]))
        self.assertEqual(list(self.b.etl_stream("erp", "q", {"x": 1})), [{"a": 1}])

    def test_open_frame_carries_params(self):
        chan = self.use(FakeChan([end_line("fertig")]))
        list(self.b.etl_stream("erp_db", "offene_posten", {"stichtag": "2026-11-18"}))
        opened = chan.sent[0]
        self.assertEqual((opened["type"], opened["integration"], opened["query_id"], opened["params"]),
                         ("etl_open", "erp_db", "offene_posten", {"stichtag": "2026-11-18"}))
        chan2 = self.use(FakeChan([end_line("fertig")]))
        list(self.b.etl_stream("erp_db", "q"))
        self.assertNotIn("params", chan2.sent[0])

    def test_pull_model_one_next_per_chunk_no_prefetch(self):
        chunks = [row_line({"i": i}) * 3 for i in range(5)] + [end_line("fertig")]
        chan = self.use(FakeChan(chunks))
        it = self.b.etl_stream("erp", "q")
        first = next(it)
        self.assertEqual(first, {"i": 0})
        self.assertEqual(chan.types().count(("etl_next", False)), 1)
        next(it)
        next(it)  # noch dasselbe Stück
        self.assertEqual(chan.types().count(("etl_next", False)), 1)
        next(it)  # erst jetzt das zweite Stück
        self.assertEqual(chan.types().count(("etl_next", False)), 2)
        it.close()

    def test_generator_never_collects(self):
        # Ein langer Strom, in kleinen Stücken: gehalten wird höchstens ein Stück.
        line = row_line({"pad": "x" * 200})
        chan = self.use(FakeChan([line * 50] * 200 + [end_line("fertig", 10000)]))
        count = 0
        for _ in self.b.etl_stream("erp", "q"):
            count += 1
            if count == 120:
                # 120 Zeilen = 3 Stücke gezogen, die übrigen 197 nicht vorgeholt
                self.assertLessEqual(chan.types().count(("etl_next", False)), 3)
        self.assertEqual(count, 10000)

    def test_other_end_reasons_raise_stream_ended(self):
        for grund in ("deckel", "wandzeit", "widerruf", "laufende", "freigabe", "integration", "shutdown", "fehler"):
            body = row_line({"a": 1}) + row_line({"a": 2}) + end_line(grund, 2)
            self.use(FakeChan(cut(body, [20])))
            seen = []
            with self.assertRaises(self.b.StreamEnded) as cm:
                for r in self.b.etl_stream("erp", "q"):
                    seen.append(r)
            self.assertEqual(cm.exception.grund, grund)
            self.assertEqual(cm.exception.zeilen, 2)
            self.assertEqual(cm.exception.bytes, len(body))
            self.assertEqual(len(seen), 2)

    def test_missing_end_line_is_abgebrochen(self):
        body = row_line({"a": 1}) + row_line({"a": 2})
        self.use(FakeChan([body]))  # danach etl_end{eof}
        with self.assertRaises(self.b.StreamEnded) as cm:
            list(self.b.etl_stream("erp", "q"))
        self.assertEqual(cm.exception.grund, "abgebrochen")
        self.assertEqual(cm.exception.zeilen, 2)

    def test_partial_last_line_at_eof_is_abgebrochen(self):
        self.use(FakeChan([row_line({"a": 1}) + b'{"t":"end","grund":"fertig"}']))
        with self.assertRaises(self.b.StreamEnded) as cm:
            list(self.b.etl_stream("erp", "q"))
        self.assertEqual(cm.exception.grund, "abgebrochen")

    def test_eof_without_any_data_is_abgebrochen(self):
        self.use(FakeChan([]))
        with self.assertRaises(self.b.StreamEnded) as cm:
            list(self.b.etl_stream("erp", "q"))
        self.assertEqual(cm.exception.grund, "abgebrochen")

    def test_end_status_from_mediator(self):
        for status in ("abgebrochen", "grenze", "vertrag", "geschlossen"):
            self.use(FakeChan([row_line({"a": 1})], final={"status": status}))
            with self.assertRaises(self.b.StreamEnded) as cm:
                list(self.b.etl_stream("erp", "q"))
            self.assertEqual(cm.exception.grund, status)

    def test_refused_carries_code_and_retry_after(self):
        self.use(FakeChan(replies=[{"type": "etl_end", "status": "abgewiesen", "code": 429, "retry_after_s": 12}]))
        with self.assertRaises(self.b.StreamRefused) as cm:
            list(self.b.etl_stream("erp", "q"))
        self.assertEqual((cm.exception.code, cm.exception.retry_after), (429, 12))
        self.use(FakeChan(replies=[{"type": "etl_end", "status": "abgewiesen"}]))
        with self.assertRaises(self.b.StreamRefused) as cm:
            list(self.b.etl_stream("erp", "q"))
        self.assertEqual((cm.exception.code, cm.exception.retry_after), (None, None))

    def test_no_retry_on_refusal(self):
        chan = self.use(FakeChan(replies=[{"type": "etl_end", "status": "abgewiesen", "code": 429, "retry_after_s": 1}]))
        with self.assertRaises(self.b.StreamRefused):
            list(self.b.etl_stream("erp", "q"))
        self.assertEqual(chan.types().count(("etl_open", False)), 1)
        self.assertEqual(chan.types().count(("etl_next", False)), 1)

    def test_close_sends_close_and_consumes_reply(self):
        chunks = [row_line({"i": i}) * 3 for i in range(3)]
        chan = self.use(FakeChan(chunks))
        it = self.b.etl_stream("erp", "q")
        next(it)
        it.close()
        self.assertEqual(chan.types()[-1], ("etl_next", True))
        self.assertEqual(len(chan.inbox), 0, "die Antwort auf close muss gelesen sein")
        # das nächste call() liest seine eigene Antwort, nicht die alte
        res = self.b.call("kartei_find", {})
        self.assertEqual(res["content"][0]["text"], "{}")

    def test_break_out_of_for_loop_closes(self):
        chan = self.use(FakeChan([row_line({"i": i}) * 3 for i in range(3)]))
        for _row in self.b.etl_stream("erp", "q"):
            break
        gc.collect()
        self.assertEqual(chan.types()[-1], ("etl_next", True))

    def test_normal_end_and_refusal_send_no_close(self):
        chan = self.use(FakeChan([end_line("fertig")]))
        list(self.b.etl_stream("erp", "q"))
        self.assertNotIn(("etl_next", True), chan.types())
        chan = self.use(FakeChan(replies=[{"type": "etl_end", "status": "abgewiesen", "code": 409}]))
        with self.assertRaises(self.b.StreamRefused):
            list(self.b.etl_stream("erp", "q"))
        self.assertNotIn(("etl_next", True), chan.types())

    def test_protocol_violations(self):
        cases = {
            "unbekannter Zeilentyp": b'{"t":"cols","d":{}}\n',
            "kein JSON": b"das ist kein json\n",
            "d kein Objekt": b'{"t":"row","d":[1]}\n',
            "Zeile ohne Typ": b'{"d":{"a":1}}\n',
        }
        for name, line in cases.items():
            chan = self.use(FakeChan([line]))
            with self.assertRaises(self.b.StreamEnded, msg=name) as cm:
                list(self.b.etl_stream("erp", "q"))
            self.assertEqual(cm.exception.grund, "vertrag", name)
            # der Strom ist beim vermittler noch offen: das SDK schließt ihn
            self.assertEqual(chan.types()[-1], ("etl_next", True), name)

    def test_overlong_line_is_grenze(self):
        chan = self.use(FakeChan([b"x" * (self.b._MAX_LINE + 1)]))
        with self.assertRaises(self.b.StreamEnded) as cm:
            list(self.b.etl_stream("erp", "q"))
        self.assertEqual(cm.exception.grund, "grenze")
        self.assertEqual(chan.types()[-1], ("etl_next", True))

    def test_unexpected_frame_is_vertrag(self):
        self.use(FakeChan(replies=[{"type": "tool_result", "result": {}}]))
        with self.assertRaises(self.b.StreamEnded) as cm:
            list(self.b.etl_stream("erp", "q"))
        self.assertEqual(cm.exception.grund, "vertrag")

    def test_wrong_id_is_vertrag(self):
        chan = FakeChan()

        def answer(msg):
            if msg["type"] == "etl_next" and not msg.get("close"):
                chan.push({"type": "etl_chunk", "id": 999, "data_b64": ""})
            elif msg["type"] == "etl_next":
                chan.push({"type": "etl_end", "id": msg["id"], "status": "geschlossen"})

        chan.answer = answer
        self.use(chan)
        with self.assertRaises(self.b.StreamEnded) as cm:
            list(self.b.etl_stream("erp", "q"))
        self.assertEqual(cm.exception.grund, "vertrag")

    def test_exception_texts_are_fixed_and_carry_no_row_content(self):
        canary = "ROWCANARY-SDK"
        body = row_line({"geheim": canary}) + end_line("deckel", 1)
        self.use(FakeChan([body]))
        with self.assertRaises(self.b.StreamEnded) as cm:
            list(self.b.etl_stream("erp", "q"))
        self.assertNotIn(canary, str(cm.exception))
        self.assertNotIn(canary, repr(cm.exception))
        self.use(FakeChan(replies=[{"type": "etl_end", "status": "abgewiesen", "code": 429}]))
        with self.assertRaises(self.b.StreamRefused) as cm:
            list(self.b.etl_stream("erp", "q"))
        self.assertNotIn("Traceback", str(cm.exception))

    def test_unknown_end_reason_text_not_echoed(self):
        body = end_line("<script>CANARY</script>", 0)
        self.use(FakeChan([body]))
        with self.assertRaises(self.b.StreamEnded) as cm:
            list(self.b.etl_stream("erp", "q"))
        self.assertNotIn("CANARY", cm.exception.grund)
        self.assertNotIn("CANARY", str(cm.exception))

    def test_connection_loss_propagates_and_close_does_not_mask(self):
        chan = self.use(FakeChan([row_line({"a": 1}) * 2]))
        it = self.b.etl_stream("erp", "q")
        next(it)
        chan.inbox.clear()
        chan.recv = lambda n: b""  # Wächter weg
        it.close()  # darf nicht werfen


if __name__ == "__main__":
    unittest.main(verbosity=2)
