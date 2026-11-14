"""Runtime key files: one Bridge key (jnpt) and the Google key (google) as directories."""

import tempfile
import unittest
from pathlib import Path
from google_reporting import server
from test_google_adapter import synthetic_identity
import json


class RuntimeFiles(unittest.TestCase):
    def test_paths_are_the_directory_mounts_with_runtime_key(self):
        self.assertEqual(server.BRIDGE_KEY_PATH, "/run/secrets/jnpt/runtime-key")
        self.assertEqual(server.IDENTITY_PATH, "/run/secrets/google/runtime-key")
        self.assertEqual(server.CONFIG_PATH, "/etc/google-reporting/config.json")

    def test_serve_reads_exactly_these_defaults(self):
        self.assertEqual(
            server.load_runtime_files.__defaults__,
            (server.BRIDGE_KEY_PATH, server.IDENTITY_PATH),
        )

    def test_one_bridge_key_and_identity_load_from_given_paths(self):
        with tempfile.TemporaryDirectory() as temp:
            key = Path(temp, "key")
            identity = Path(temp, "identity")
            key.write_bytes(b"synthetic-bridge-key-0123456789abcdef\n")
            identity.write_text(json.dumps(synthetic_identity()))
            self.assertEqual(
                server.load_runtime_files(str(key), str(identity)),
                b"synthetic-bridge-key-0123456789abcdef",
            )

    def test_missing_or_invalid_files_fail_closed(self):
        with tempfile.TemporaryDirectory() as temp:
            key = Path(temp, "key")
            identity = Path(temp, "identity")
            identity.write_text(json.dumps(synthetic_identity()))
            with self.assertRaises(OSError):
                server.load_runtime_files(str(key), str(identity))
            key.write_bytes(b"short")
            with self.assertRaises(ValueError):
                server.load_runtime_files(str(key), str(identity))
            key.write_bytes(b"synthetic-bridge-key-0123456789abcdef")
            identity.write_text("{}")
            with self.assertRaises(ValueError):
                server.load_runtime_files(str(key), str(identity))

    def test_bridge_keys_must_differ_is_gone(self):
        self.assertNotIn("bridge_keys_must_differ", Path(server.__file__).read_text())


ROOT = Path(__file__).resolve().parents[1]


@unittest.skipUnless((ROOT / "README.md").exists(), "plugin files not mounted")
class ConfigExamples(unittest.TestCase):
    """The example files are the documented forms: single (default) and list (#1379)."""

    def load(self, name):
        from google_reporting.domain import Sites

        return Sites.from_json((ROOT / name).read_text(encoding="utf-8"))

    def test_single_example_is_the_site_default(self):
        sites = self.load("config.example.json")
        self.assertFalse(sites.listed)
        self.assertEqual(list(sites.by_name), ["default"])

    def test_sites_example_is_the_list_form_with_two_sites(self):
        sites = self.load("config.example.sites.json")
        self.assertTrue(sites.listed)
        self.assertEqual(len(sites.by_name), 2)
        self.assertNotIn("default", sites.by_name)
        # Each site names its own segments (#1379 part B); the first keeps the three classic ones.
        self.assertEqual(list(sites.by_name["meine-site"].segments), ["atas", "gateway", "shared"])
        self.assertEqual(list(sites.by_name["zweite-site"].segments), ["start", "preise"])
