"""Compose, host descriptor, tmpfiles and unit examples agree on the two key places.

Skipped inside the image-only CI step, where the deploy files are not mounted.
"""

import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DEPLOY = ROOT / "deploy"
BRIDGE = "/run/jnpt/plugins/bing-webmaster/runtime-key"
BING = "/run/jnpt/plugins/bing-webmaster-bing/runtime-key"
EGRESS_HOSTS = ("ssl.bing.com", "api.indexnow.org")


def text(path):
    return Path(path).read_text(encoding="utf-8")


@unittest.skipUnless((ROOT / "docker-compose.yml").exists(), "deploy files not mounted")
class DeployFiles(unittest.TestCase):
    def test_compose_mounts_two_read_only_directories(self):
        compose = text(ROOT / "docker-compose.yml")
        pairs = re.findall(r"source: (/run/jnpt/plugins/\S+)\n\s+target: (/run/secrets/\S+)\n\s+read_only: true", compose)
        self.assertEqual(
            sorted(pairs),
            [
                ("/run/jnpt/plugins/bing-webmaster", "/run/secrets/jnpt"),
                ("/run/jnpt/plugins/bing-webmaster-bing", "/run/secrets/bing"),
            ],
        )
        self.assertIn("source: /opt/jnpt/plugins/bing-webmaster/config.json", compose)

    def test_compose_is_hardened_like_the_sibling_services(self):
        compose = text(ROOT / "docker-compose.yml")
        for line in ('user: "65532:65532"', "read_only: true", "cap_drop: [ALL]", "no-new-privileges:true", "pids_limit:", "mem_limit:"):
            self.assertIn(line, compose)
        self.assertNotIn("ports:", compose)
        self.assertIn("BING_WEBMASTER_BIND_IP", compose)

    def test_descriptor_requires_config_and_both_runtime_keys(self):
        lines = [l for l in text(DEPLOY / "bing-webmaster.conf.example").splitlines() if l.startswith("REQUIRE=")]
        self.assertEqual(lines, ["REQUIRE=/opt/jnpt/plugins/bing-webmaster/config.json:" + BRIDGE + ":" + BING])

    def test_tmpfiles_example_has_two_owned_directories(self):
        lines = [l for l in text(DEPLOY / "jnpt-plugins-bing-webmaster.conf.example").splitlines() if l.startswith("d ")]
        self.assertEqual(
            lines,
            [
                "d /run/jnpt/plugins/bing-webmaster 0750 65532 65532 -",
                "d /run/jnpt/plugins/bing-webmaster-bing 0750 65532 65532 -",
            ],
        )

    def test_unit_pair_watches_both_keys_and_recreates(self):
        path = re.sub(r"^#.*$", "", text(DEPLOY / "jnpt-bing-webmaster-key.path"), flags=re.M)
        self.assertEqual(re.findall(r"^PathChanged=(.+)$", path, re.M), [BRIDGE, BING])
        self.assertNotIn("PathModified", path)
        self.assertNotIn("MakeDirectory", path)
        service = text(DEPLOY / "jnpt-bing-webmaster-key.service")
        self.assertIn(
            "ExecStart=/usr/bin/docker compose -f /opt/jnpt/plugins/bing-webmaster/docker-compose.yml up -d --force-recreate",
            service,
        )
        for hardening in ("NoNewPrivileges=yes", "PrivateTmp=yes", "ProtectSystem=full", "ProtectHome=read-only", "RestrictNamespaces=yes", "UMask=0077"):
            self.assertIn(hardening, service)
        self.assertNotIn("[Install]", re.sub(r"^#.*$", "", service, flags=re.M))

    def test_deploy_files_name_the_two_egress_targets_and_no_others(self):
        readme = text(ROOT / "README.md")
        for host in EGRESS_HOSTS:
            self.assertIn(host + ":443", readme)
        for extra in ("googleapis.com", "bing.com:443", "microsoft.com"):
            self.assertNotIn(extra, text(ROOT / "docker-compose.yml"))
        egress = re.findall(r"\b[a-z0-9-]+(?:\.[a-z0-9-]+)+:443", readme)
        self.assertEqual(sorted(set(egress)), sorted(h + ":443" for h in EGRESS_HOSTS))

    def test_no_secret_value_in_deploy_files(self):
        for path in [ROOT / "docker-compose.yml", ROOT / "config.example.json", *DEPLOY.iterdir()]:
            self.assertNotRegex(text(path), r"\b[0-9a-fA-F]{32,}\b", str(path))


if __name__ == "__main__":
    unittest.main()
