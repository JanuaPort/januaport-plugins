"""SETUP.md becomes the signed catalog field setup_guide (JanuaPort/januaport#1137).

The core rejects the whole catalog for a guide over 32 KiB or with invalid UTF-8,
so the limit is checked here, before the feed build. The guide is read without
the repository around it, so it must not point to files beside it.
Skipped inside the image-only CI step, where SETUP.md is not mounted.
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

    def test_names_minimal_google_rights(self):
        guide = self.guide()
        for phrase in (
            "keine Rolle",
            "„Uneingeschränkt“",
            "„Bearbeiter“",
            "„Keine Kosten- und Umsatzmesswerte“",
            "Google Search Console API",
            "Google Analytics Data API",
            "Google Analytics Admin API",
        ):
            self.assertIn(phrase, guide)

    def test_names_the_ceiling_of_the_write_rights(self):
        # SEC ceiling V1 (JanuaPort/januaport#1148): GSC at most „Uneingeschränkt“, GA4 at most
        # „Bearbeiter“, never an owner or administrator and no IAM role.
        guide = self.guide()
        self.assertIn("„Inhaber“", guide)
        self.assertIn("„Administrator“", guide)
        self.assertNotIn("„Betrachter“", guide)
        self.assertNotIn("„Eingeschränkt“", guide)
        self.assertIn("keine Rolle", guide)

    def test_names_the_manage_scopes_and_the_daily_cap(self):
        guide = self.guide()
        self.assertIn("`google-search-console-manage`", guide)
        self.assertIn("`google-analytics-manage`", guide)
        self.assertIn("`google-search-console-manage:submit_sitemap`", guide)
        self.assertIn("`google-analytics-manage:create_annotation`", guide)
        self.assertIn("20", guide)

    def test_keeps_key_out_of_files(self):
        guide = self.guide()
        self.assertIn("plugin-google-reporting-google", guide)
        self.assertRegex(guide, r"Datei[^.]*löschen")
        self.assertIn("Passwortmanager", guide)

    def test_names_user_scopes(self):
        guide = self.guide()
        self.assertIn("`google-search-console`", guide)
        self.assertIn("`google-analytics`", guide)
        # Interface First: the admin-MCP path (JanuaPort/januaport#1145) is named, too.
        self.assertIn("`admin_set_user_scopes`", guide)
        self.assertIn("`admin_list_users`", guide)

    def test_has_no_relative_links(self):
        for target in re.findall(r"\]\(([^)]+)\)", self.guide()):
            self.assertRegex(target, r"^https://", target)

    def test_readme_points_to_guide_instead_of_copy(self):
        readme = (ROOT / "README.md").read_text(encoding="utf-8")
        self.assertIn("SETUP.md", readme)
        self.assertNotIn("## Einrichtung der Google-Seite, Schritt für Schritt", readme)


if __name__ == "__main__":
    unittest.main()
