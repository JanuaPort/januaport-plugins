"""Compose, host descriptor, tmpfiles and unit examples agree on the new key places.

Skipped inside the image-only CI step, where the deploy files are not mounted.
"""

import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DEPLOY = ROOT / "deploy"
BRIDGE = "/run/jnpt/plugins/linkedin-seite/runtime-key"
OAUTH = "/run/jnpt/plugins/linkedin-seite-oauth/runtime-key"
# Stufe B1 (#1237): write OAuth and the Beisteller's own gateway token, optional at start.
POST_OAUTH = "/run/jnpt/plugins/linkedin-seite-post-oauth/runtime-key"
GATEWAY = "/run/jnpt/plugins/linkedin-seite-gateway/runtime-key"
OLD_NAMES = ("gsc-bridge-key", "ga4-bridge-key", "google-service-account.json")


def text(path):
    return Path(path).read_text(encoding="utf-8")


@unittest.skipUnless((ROOT / "docker-compose.yml").exists(), "deploy files not mounted")
class DeployFiles(unittest.TestCase):
    def test_compose_mounts_four_read_only_directories(self):
        compose = text(ROOT / "docker-compose.yml")
        pairs = re.findall(
            r"source: (/run/jnpt/plugins/\S+)\n\s+target: (/run/secrets/\S+)\n\s+read_only: true",
            compose,
        )
        self.assertEqual(
            sorted(pairs),
            [
                ("/run/jnpt/plugins/linkedin-seite", "/run/secrets/jnpt"),
                ("/run/jnpt/plugins/linkedin-seite-gateway", "/run/secrets/gateway"),
                ("/run/jnpt/plugins/linkedin-seite-oauth", "/run/secrets/oauth"),
                ("/run/jnpt/plugins/linkedin-seite-post-oauth", "/run/secrets/post-oauth"),
            ],
        )
        self.assertIn("source: /opt/jnpt/plugins/linkedin-seite/config.json", compose)

    def test_no_old_file_names_anywhere_in_deploy(self):
        for path in [ROOT / "docker-compose.yml", *DEPLOY.iterdir()]:
            for name in OLD_NAMES:
                self.assertNotIn(name, text(path), str(path))

    def test_descriptor_requires_config_and_the_two_read_keys_only(self):
        # The write keys are optional: without them Stufe A runs and the write routes answer 503.
        line = [
            l
            for l in text(DEPLOY / "linkedin-seite.conf.example").splitlines()
            if l.startswith("REQUIRE=")
        ]
        self.assertEqual(len(line), 1)
        self.assertEqual(
            line[0],
            "REQUIRE=/opt/jnpt/plugins/linkedin-seite/config.json:" + BRIDGE + ":" + OAUTH,
        )

    def test_tmpfiles_example_has_four_owned_directories(self):
        lines = [
            l
            for l in text(DEPLOY / "jnpt-plugins-linkedin-seite.conf.example").splitlines()
            if l.startswith("d ")
        ]
        self.assertEqual(
            lines,
            [
                "d /run/jnpt/plugins/linkedin-seite 0750 65532 65532 -",
                "d /run/jnpt/plugins/linkedin-seite-oauth 0750 65532 65532 -",
                "d /run/jnpt/plugins/linkedin-seite-post-oauth 0750 65532 65532 -",
                "d /run/jnpt/plugins/linkedin-seite-gateway 0750 65532 65532 -",
            ],
        )

    def test_unit_pair_watches_all_four_keys_and_recreates(self):
        path = re.sub(r"^#.*$", "", text(DEPLOY / "jnpt-linkedin-seite-key.path"), flags=re.M)
        self.assertEqual(
            re.findall(r"^PathChanged=(.+)$", path, re.M), [BRIDGE, OAUTH, POST_OAUTH, GATEWAY]
        )
        self.assertNotIn("PathModified", path)
        self.assertNotIn("MakeDirectory", path)
        service = text(DEPLOY / "jnpt-linkedin-seite-key.service")
        self.assertIn(
            "ExecStart=/usr/bin/docker compose -f "
            "/opt/jnpt/plugins/linkedin-seite/docker-compose.yml up -d --force-recreate",
            service,
        )
        for hardening in (
            "NoNewPrivileges=yes",
            "PrivateTmp=yes",
            "ProtectSystem=full",
            "ProtectHome=read-only",
            "RestrictNamespaces=yes",
            "UMask=0077",
        ):
            self.assertIn(hardening, service)
        self.assertNotIn("[Install]", re.sub(r"^#.*$", "", service, flags=re.M))


@unittest.skipUnless((ROOT / "config.example.json").exists(), "plugin files not mounted")
class ExampleConfig(unittest.TestCase):
    def test_example_names_the_internal_gateway_mcp_and_loads(self):
        import json
        from linkedin_seite.domain import Config

        raw = json.loads(text(ROOT / "config.example.json"))
        self.assertEqual(raw["schreiben"]["gateway_url"], "http://jnpt:8484/mcp")
        cfg = Config.from_dict(raw)
        self.assertEqual(sorted(cfg.schreiben.tools.values()),
                         ["redaktion_freigabe_search", "redaktion_medien_get_file", "redaktionsplan_get"])
