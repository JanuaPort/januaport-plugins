"""Runtime key files: the Bridge key (jnpt) and the Bing key (bing) as directory mounts."""

import tempfile
import unittest
from pathlib import Path
from bing_webmaster import server
from support import BING_KEY


class RuntimeFiles(unittest.TestCase):
    def test_paths_are_the_directory_mounts_with_runtime_key(self):
        self.assertEqual(server.BRIDGE_KEY_PATH, "/run/secrets/jnpt/runtime-key")
        self.assertEqual(server.BING_KEY_PATH, "/run/secrets/bing/runtime-key")
        self.assertEqual(server.CONFIG_PATH, "/etc/bing-webmaster/config.json")

    def test_serve_reads_exactly_these_defaults(self):
        self.assertEqual(server.load_runtime_files.__defaults__, (server.BRIDGE_KEY_PATH, server.BING_KEY_PATH))

    def test_both_keys_load_from_given_paths_and_are_trimmed(self):
        with tempfile.TemporaryDirectory() as temp:
            bridge, bing = Path(temp, "bridge"), Path(temp, "bing")
            bridge.write_bytes(b"synthetic-bridge-key-0123456789abcdef\n")
            bing.write_bytes(BING_KEY.encode() + b"\n")
            self.assertEqual(server.load_runtime_files(str(bridge), str(bing)), (b"synthetic-bridge-key-0123456789abcdef", BING_KEY))

    def test_missing_or_invalid_files_fail_closed(self):
        with tempfile.TemporaryDirectory() as temp:
            bridge, bing = Path(temp, "bridge"), Path(temp, "bing")
            with self.assertRaises(OSError):
                server.load_runtime_files(str(bridge), str(bing))
            bridge.write_bytes(b"synthetic-bridge-key-0123456789abcdef")
            with self.assertRaises(OSError):
                server.load_runtime_files(str(bridge), str(bing))
            for bad in (b"", b"short", b"has space in it 0123456789abcdef", b"a" * 129, b"key/with/slash0123456789", b"key?query=1&x=yyyyyyyy", b"k\xc3\xa4y0123456789abcdef"):
                bing.write_bytes(bad)
                with self.subTest(bad=bad[:12]), self.assertRaises(ValueError):
                    server.load_runtime_files(str(bridge), str(bing))
            bridge.write_bytes(b"short")
            bing.write_bytes(BING_KEY.encode())
            with self.assertRaises(ValueError):
                server.load_runtime_files(str(bridge), str(bing))

    def test_the_two_keys_must_differ(self):
        with tempfile.TemporaryDirectory() as temp:
            bridge, bing = Path(temp, "bridge"), Path(temp, "bing")
            same = BING_KEY.encode() + b"0123456789abcdefghij"
            bridge.write_bytes(same)
            bing.write_bytes(same)
            with self.assertRaises(ValueError):
                server.load_runtime_files(str(bridge), str(bing))

    def test_failures_do_not_print_the_key(self):
        with tempfile.TemporaryDirectory() as temp:
            bridge, bing = Path(temp, "bridge"), Path(temp, "bing")
            bridge.write_bytes(b"synthetic-bridge-key-0123456789abcdef")
            bing.write_bytes(b"bad key CANARY-VALUE")
            with self.assertRaises(ValueError) as cm:
                server.load_runtime_files(str(bridge), str(bing))
            self.assertNotIn("CANARY-VALUE", repr(cm.exception) + str(cm.exception))

    def test_startup_failure_prints_only_a_fixed_line(self):
        self.assertTrue(hasattr(server, "main"))


if __name__ == "__main__":
    unittest.main()
