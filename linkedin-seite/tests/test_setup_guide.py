"""Signed setup_guide contract, following google-reporting."""
import re
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[1]


@unittest.skipUnless((ROOT/'README.md').exists(), 'plugin files not mounted')
class SetupGuide(unittest.TestCase):
    def guide(self):
        return (ROOT/'SETUP.md').read_bytes().decode('utf-8', errors='strict')

    def test_utf8_size_and_no_relative_links(self):
        raw = (ROOT/'SETUP.md').read_bytes()
        self.assertGreater(len(raw), 0)
        self.assertLessEqual(len(raw), 32*1024)
        for link in re.findall(r'\]\(([^)]+)\)', self.guide()):
            self.assertRegex(link, r'^https://')

    def test_admin_scopes_role_callback_and_pipe(self):
        for phrase in ('rw_organization_admin r_organization_social', 'Seiten-Admin-Rolle',
                       'http://localhost:8765/linkedin/callback', '127.0.0.1',
                       'jnpt credential set --name plugin-linkedin-seite-oauth',
                       'nicht im Container', 'Passwortmanager', '--secret-stdin', '--pkce'):
            self.assertIn(phrase, self.guide())

    def test_lifecycle_and_warning_contract(self):
        for phrase in ('60 Tage', '365 Tage ab Erstzustimmung', '14 Tagen', 'token_abgelaufen',
                       'token_laeuft_ab', 'RAM', 'Tresor zurückschreiben', 'kein Widerruf', '50'):
            self.assertIn(phrase, self.guide())
        readme = (ROOT/'README.md').read_text(encoding='utf-8')
        self.assertIn('SEC-Prüfpunkte (A2)', readme)
        self.assertIn('[SETUP.md](SETUP.md)', readme)

    def test_windows_secret_entry_uses_masked_powershell_input_not_cmd_paste(self):
        # #1237: pasting into getpass under cmd inserts a control character (Ctrl+V)
        guide = self.guide()
        for phrase in ('Windows', 'PowerShell 7', 'Read-Host -MaskInput', 'cmd', 'Steuerzeichen', '--secret-stdin'):
            self.assertIn(phrase, guide)
        self.assertRegex(guide, r'Read-Host -MaskInput [^\n]*\| python [^\n]*zustimmung\.py --client-id APP_CLIENT_ID '
                                r'--secret-stdin \| jnpt credential set --name plugin-linkedin-seite-oauth')
        self.assertNotRegex(guide, r'--client-secret|\$env:|-AsPlainText|ConvertFrom-SecureString')

    def test_stage_b1_write_consent_and_approval_handgriffe(self):
        guide = self.guide()
        for phrase in ('w_organization_social', '--schreiben', 'jnpt credential set --name plugin-linkedin-seite-post-oauth',
                       'redaktion_freigabe', 'linkedin_seite_vorschau', 'SSO', 'hash', '5 schreibende Aktionen',
                       'Lese- und Schreib-Token', 'http://jnpt:8484/mcp', 'jnpt_plugins', '/admin/mcp',
                       'redaktionsplan_get', 'redaktion_freigabe_search', 'redaktion_medien_get_file',
                       'plugin-linkedin-seite-gateway'):
            self.assertIn(phrase, guide)
        self.assertRegex(guide, r'Read-Host -MaskInput [^\n]*\| python [^\n]*zustimmung\.py --client-id APP_CLIENT_ID '
                                r'--secret-stdin --schreiben \| jnpt credential set --name plugin-linkedin-seite-post-oauth')

    def test_readme_names_gateway_route_token_tools_and_live_gates(self):
        readme = (ROOT/'README.md').read_text(encoding='utf-8')
        for phrase in ('http://jnpt:8484/mcp', 'jnpt_plugins', '/admin/mcp', 'redaktionsplan_get',
                       'redaktion_freigabe_search', 'redaktion_medien_get_file', 'plugin-linkedin-seite-gateway',
                       'plugin-linkedin-seite-post-oauth', 'Live-Gate', 'www.linkedin.com', 'updated_via'):
            self.assertIn(phrase, readme)
