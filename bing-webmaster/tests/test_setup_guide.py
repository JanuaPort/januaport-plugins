"""SETUP.md becomes the signed catalog field setup_guide (JanuaPort/januaport#1137).

The core rejects the whole catalog for a guide over 32 KiB or with invalid UTF-8,
so the limit is checked here. The guide is read without the repository around it,
so it must not point to files beside it. Skipped where SETUP.md is not mounted.
"""

import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SETUP = ROOT / "SETUP.md"
MAX_BYTES = 32 * 1024


@unittest.skipUnless((ROOT / "README.md").exists(), "plugin files not mounted")
class SetupGuide(unittest.TestCase):
    def guide(self):
        return SETUP.read_bytes().decode("utf-8", errors="strict")

    def test_fits_catalog_field(self):
        raw = SETUP.read_bytes()
        self.assertGreater(len(raw), 0)
        self.assertLessEqual(len(raw), MAX_BYTES)
        raw.decode("utf-8", errors="strict")

    def test_has_no_relative_links(self):
        for target in re.findall(r"\]\(([^)]+)\)", self.guide()):
            self.assertRegex(target, r"^https://", target)

    def test_names_the_bing_key_place_and_the_human_secret_path(self):
        guide = self.guide()
        self.assertIn("plugin-bing-webmaster-bing", guide)
        self.assertIn("API-Zugriff", guide)
        self.assertIn("Passwortmanager", guide)
        self.assertIn("Basis-Einrichtung", guide)

    def test_names_the_scopes_and_the_daily_cap(self):
        guide = self.guide()
        for scope in (
            "`bing-webmaster`",
            "`bing-webmaster-manage`",
            "`bing-webmaster-manage:submit_urls`",
            "`bing-webmaster-manage:submit_sitemap`",
            "`bing-webmaster-manage:indexnow_submit_urls`",
            "`admin_set_user_scopes`",
            "`admin_list_users`",
        ):
            self.assertIn(scope, guide)
        self.assertIn("20", guide)
        self.assertIn("dry_run", guide)

    def test_states_the_limits_as_facts(self):
        guide = self.guide()
        self.assertIn("AI Performance", guide)
        self.assertIn("keine API", guide)
        self.assertIn("keine Indexierung", guide)

    def test_explains_that_the_indexnow_key_is_public(self):
        guide = self.guide()
        self.assertIn("IndexNow", guide)
        self.assertRegex(guide, r"öffentlich")
        self.assertIn("not_configured", guide)

    def test_readme_points_to_guide_instead_of_copy(self):
        readme = (ROOT / "README.md").read_text(encoding="utf-8")
        self.assertIn("SETUP.md", readme)


if __name__ == "__main__":
    unittest.main()
