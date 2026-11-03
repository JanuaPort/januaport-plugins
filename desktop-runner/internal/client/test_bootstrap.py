"""Bootstrap helper tests; no user script, network or desktop is executed."""
import ast
import io
import json
from pathlib import Path
import struct
import types
import unittest
from unittest.mock import patch


class BootstrapTests(unittest.TestCase):
    def setUp(self):
        tree = ast.parse(Path(__file__).with_name("bootstrap.py").read_text(encoding="utf-8"))
        tree.body.pop()  # main() is covered through the real Go process adapter.
        self.ns = {"__file__": str(Path(__file__).with_name("bootstrap.py"))}
        with patch("sys.stdin", types.SimpleNamespace(buffer=io.BytesIO())), patch("sys.stdout", types.SimpleNamespace(buffer=io.BytesIO())):
            exec(compile(tree, "bootstrap.py", "exec"), self.ns)

    def reply(self, value):
        body = json.dumps(value).encode()
        self.ns["_reader"] = io.BytesIO(struct.pack(">I", len(body)) + body)

    def test_frage_fehler_und_null(self):
        for response in ({"type": "fehler", "code": "abgewiesen"}, {"type": "antwort", "id": "2", "wahl": None}):
            self.reply(response)
            self.assertIsNone(self.ns["frage"]("Weiter?", ["Ja", "Nein"], 5))

    def test_screenshot_fehler(self):
        self.reply({"type": "fehler", "code": "abgewiesen"})
        self.assertIsNone(self.ns["screenshot"]())

    def test_ergebnis_json(self):
        for value in (object(), float("nan"), float("inf")):
            with self.assertRaises((TypeError, ValueError)):
                self.ns["ergebnis"](value)
        self.assertEqual(self.ns["_writer"].getvalue(), b"")

    def test_januaport_call(self):
        self.reply({"type": "tool_result", "id": 1, "result": {"content": []}})
        self.assertEqual(self.ns["call"]("demo"), {"content": []})


if __name__ == "__main__":
    unittest.main()
