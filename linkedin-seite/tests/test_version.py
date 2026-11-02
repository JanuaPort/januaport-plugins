"""Version consistency (CLAUDE.md: the version stands in the contract head, README head and CHANGELOG)."""
from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parents[1]


def triple(text):
    return tuple(int(part) for part in text.split('.'))


@unittest.skipUnless((ROOT/'README.md').exists(), 'plugin files not mounted')
class Version(unittest.TestCase):
    def read(self, name):
        return (ROOT/name).read_text(encoding='utf-8')

    def versions(self):
        contract = re.match(r'# linkedin-seite (\d+\.\d+\.\d+) — Modulvertrag', self.read('CLAUDE.md'))
        readme = re.match(r'# LinkedIn-Seite (\d+\.\d+\.\d+) — ', self.read('README.md'))
        changelog = re.search(r'^## (\d+\.\d+\.\d+) — \d{4}-\d{2}-\d{2}$', self.read('CHANGELOG.md'), re.M)
        for found in (contract, readme, changelog):
            self.assertIsNotNone(found)
        return contract.group(1), readme.group(1), changelog.group(1)

    def test_contract_readme_and_newest_changelog_entry_agree(self):
        contract, readme, changelog = self.versions()
        self.assertEqual(contract, readme)
        self.assertEqual(readme, changelog)

    def test_oauth_tolerance_fix_ships_from_0_1_1_with_its_changelog_entry(self):
        self.assertGreaterEqual(triple(self.versions()[2]), (0, 1, 1))
        section = re.search(r'^## 0\.1\.1 — .*?(?=^## |\Z)', self.read('CHANGELOG.md'), re.M | re.S)
        self.assertIsNotNone(section)
        self.assertIn('JanuaPort/januaport#1237', section.group(0))


if __name__ == '__main__':
    unittest.main()
