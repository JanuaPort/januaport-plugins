"""LF bytes are shared with core Git fixtures; CI uses frozen SHA-256 pins."""
import hashlib
from pathlib import Path
import subprocess
import unittest

ROOT = Path(__file__).resolve().parents[1]
FIXTURES = (
    ('linkedin_vorschau_beispiel.json', 'internal/connector/testdata/linkedin_vorschau_beispiel.json',
     'c238d1617f0582ed7eaf46901368e04de92c47194f61c2bf9cc45f4bdcb54050'),
    ('hash_golden.json', 'internal/freigabe/testdata/hash_golden.json',
     '566c635e69c43e5341e12293256518663e008dd23e6f9cc68588d2ee3f88dd00'),
)


class FixtureBytes(unittest.TestCase):
    def test_json_fixtures_use_lf(self):
        for name, _, _ in FIXTURES:
            with self.subTest(file=name):
                data = (ROOT/'tests'/'data'/name).read_bytes()
                self.assertNotIn(b'\r', data)
                self.assertTrue(data.endswith(b'\n'))

    def test_frozen_sha256_and_optional_local_core_bytes(self):
        worktree = ROOT.parent.parent/'jpwt-1306f2'
        for name, relative, expected in FIXTURES:
            with self.subTest(file=name):
                local = (ROOT/'tests'/'data'/name).read_bytes()
                self.assertEqual(hashlib.sha256(local).hexdigest(), expected)
                path = worktree/relative
                if path.exists():
                    core = path.read_bytes()
                    if b'\r' in core:
                        # Windows checkout conversion: verify its exact canonical Git bytes.
                        blob = subprocess.run(['git', '-C', str(worktree), 'show', 'HEAD:'+relative],
                                              check=True, stdout=subprocess.PIPE).stdout
                        self.assertEqual(core, blob.replace(b'\n', b'\r\n'))
                        core = blob
                    self.assertEqual(hashlib.sha256(core).hexdigest(), expected)
                    self.assertEqual(local, core)
