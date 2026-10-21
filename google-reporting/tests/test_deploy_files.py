"""Compose, host descriptor, tmpfiles and unit examples agree on the new key places.

Skipped inside the image-only CI step, where the deploy files are not mounted.
"""

import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DEPLOY = ROOT / "deploy"
BRIDGE = "/run/jnpt/plugins/google-reporting/runtime-key"
GOOGLE = "/run/jnpt/plugins/google-reporting-google/runtime-key"
OLD_NAMES = ("gsc-bridge-key", "ga4-bridge-key", "google-service-account.json")


def text(path):
    return Path(path).read_text(encoding="utf-8")


@unittest.skipUnless((ROOT / "docker-compose.yml").exists(), "deploy files not mounted")
class DeployFiles(unittest.TestCase):
    def test_compose_mounts_two_read_only_directories(self):
        compose = text(ROOT / "docker-compose.yml")
        pairs = re.findall(
            r"source: (/run/jnpt/plugins/\S+)\n\s+target: (/run/secrets/\S+)\n\s+read_only: true",
            compose,
        )
        self.assertEqual(
            sorted(pairs),
            [
                ("/run/jnpt/plugins/google-reporting", "/run/secrets/jnpt"),
                ("/run/jnpt/plugins/google-reporting-google", "/run/secrets/google"),
            ],
        )
        self.assertIn("source: /opt/jnpt/plugins/google-reporting/config.json", compose)

    def test_no_old_file_names_anywhere_in_deploy(self):
        for path in [ROOT / "docker-compose.yml", *DEPLOY.iterdir()]:
            for name in OLD_NAMES:
                self.assertNotIn(name, text(path), str(path))

    def test_descriptor_requires_config_and_both_runtime_keys(self):
        line = [
            l
            for l in text(DEPLOY / "google-reporting.conf.example").splitlines()
            if l.startswith("REQUIRE=")
        ]
        self.assertEqual(len(line), 1)
        self.assertEqual(
            line[0],
            "REQUIRE=/opt/jnpt/plugins/google-reporting/config.json:" + BRIDGE + ":" + GOOGLE,
        )

    def test_tmpfiles_example_has_two_owned_directories(self):
        lines = [
            l
            for l in text(DEPLOY / "jnpt-plugins-google-reporting.conf.example").splitlines()
            if l.startswith("d ")
        ]
        self.assertEqual(
            lines,
            [
                "d /run/jnpt/plugins/google-reporting 0750 65532 65532 -",
                "d /run/jnpt/plugins/google-reporting-google 0750 65532 65532 -",
            ],
        )

    def test_unit_pair_watches_both_keys_and_recreates(self):
        path = re.sub(r"^#.*$", "", text(DEPLOY / "jnpt-google-reporting-key.path"), flags=re.M)
        self.assertEqual(
            re.findall(r"^PathChanged=(.+)$", path, re.M), [BRIDGE, GOOGLE]
        )
        self.assertNotIn("PathModified", path)
        self.assertNotIn("MakeDirectory", path)
        service = text(DEPLOY / "jnpt-google-reporting-key.service")
        self.assertIn(
            "ExecStart=/usr/bin/docker compose -f "
            "/opt/jnpt/plugins/google-reporting/docker-compose.yml up -d --force-recreate",
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
