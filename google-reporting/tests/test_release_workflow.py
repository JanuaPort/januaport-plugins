"""Guards for the release workflow (JanuaPort/januaport#1138), same rules as agent-runner/script-runner.

Text checks, no YAML dependency (the hash lock has none). Skipped where the repository
root is not mounted (image-only CI step, single-folder container runs).
"""

import re
import unittest
from pathlib import Path

WORKFLOWS = Path(__file__).resolve().parents[2] / ".github" / "workflows"
RELEASE = WORKFLOWS / "google-reporting-release.yml"
IMAGE_REPO = "ghcr.io/januaport/google-reporting"
PINNED = re.compile(r"^[a-z0-9-]+/[A-Za-z0-9._/-]+@[0-9a-f]{40} # v\d+\.\d+\.\d+$")


def read():
    return RELEASE.read_text(encoding="utf-8")


def jobs(text):
    body = re.split(r"^jobs:\n", text, flags=re.M)[1]
    parts = re.split(r"^  ([a-z]+):\n", body, flags=re.M)
    return {parts[i]: parts[i + 1] for i in range(1, len(parts), 2)}


def uses(text):
    return re.findall(r"^\s*(?:- )?uses: (.+)$", text, flags=re.M)


def writes(job):
    return "packages: write" in job


@unittest.skipUnless(WORKFLOWS.is_dir(), "repository workflows not mounted")
class ReleaseWorkflow(unittest.TestCase):
    def test_the_workflow_exists_with_the_five_jobs(self):
        self.assertTrue(RELEASE.is_file())
        self.assertEqual(
            sorted(jobs(read())), ["build", "probes", "publish", "scan", "unit"]
        )

    def test_only_the_version_tag_triggers_it(self):
        text = read()
        on = re.search(r"^on:\n(.*?)^permissions:", text, flags=re.M | re.S).group(1)
        self.assertEqual(
            re.sub(r"\s+", " ", on).strip(),
            'push: tags: - "google-reporting-v*"',
        )
        self.assertIn("IMAGE_REPO: " + IMAGE_REPO + "\n", text)

    def test_tag_schema_is_checked_before_anything_is_built(self):
        unit = jobs(read())["unit"]
        self.assertIn("'^google-reporting-v[0-9]+\\.[0-9]+\\.[0-9]+$'", unit)

    def test_unit_job_installs_the_hash_lock_and_runs_tests_and_mutations(self):
        unit = jobs(read())["unit"]
        self.assertIn("working-directory: google-reporting", unit)
        self.assertIn("pip install --require-hashes -r requirements.lock", unit)
        self.assertIn("python -m unittest discover -s tests -v", unit)
        self.assertIn("python tests/mutations.py", unit)

    def test_permissions_read_by_default_and_write_only_where_ghcr_is_written(self):
        text = read()
        top = re.search(r"^permissions:\n((?:  .+\n)+)", text, flags=re.M).group(1)
        self.assertEqual(top.strip(), "contents: read")
        by_job = jobs(text)
        for name, job in by_job.items():
            self.assertIn("permissions:", job, name)
            self.assertEqual(set(re.findall(r"(\w+): write", job)) - {"packages"}, set(), name)
        self.assertEqual(sorted(n for n, j in by_job.items() if writes(j)), ["build", "publish"])

    def test_no_third_party_action_in_jobs_with_write_access_and_trivy_only_without(self):
        scan = None
        for name, job in jobs(read()).items():
            if writes(job):
                self.assertNotIn("trivy", job.lower(), name)
                for ref in uses(job):
                    self.assertRegex(ref, r"^(actions|docker)/", name)
            elif "trivy" in job.lower():
                scan = job
        self.assertIsNotNone(scan)
        self.assertRegex(scan, r"TRIVY_VERSION: \d+\.\d+\.\d+\n")
        self.assertRegex(scan, r"TRIVY_SHA256: [0-9a-f]{64}\n")
        self.assertIn("sha256sum -c", scan)
        self.assertNotRegex(scan, r"uses: .*trivy")

    def test_every_action_is_pinned_by_commit_sha(self):
        refs = uses(read())
        self.assertTrue(refs)
        for ref in refs:
            self.assertRegex(ref, PINNED)

    def test_pins_match_the_reference_workflows(self):
        reference = (WORKFLOWS / "script-runner-release.yml").read_text(encoding="utf-8")
        known = set(uses(reference))
        for ref in uses(read()):
            if ref.startswith(("actions/setup-python@", "actions/setup-go@")):
                continue
            self.assertIn(ref, known)

    def test_one_amd64_build_by_digest_with_sbom_and_provenance_and_no_tag(self):
        build = jobs(read())["build"]
        self.assertIn("context: google-reporting", build)
        self.assertIn("platforms: linux/amd64", build)
        self.assertIn(
            "outputs: type=image,name=${{ env.IMAGE_REPO }},push-by-digest=true,name-canonical=true,push=true",
            build,
        )
        self.assertIn("sbom: true", build)
        self.assertIn("provenance: mode=max", build)
        self.assertNotRegex(build, r"^\s+tags:", msg="the tag is set only in publish")
        self.assertIn("org.opencontainers.image.licenses=Apache-2.0", build)
        self.assertIn("digest: ${{ steps.build.outputs.digest }}", build)

    def test_probes_check_the_pushed_digest_and_the_config_digest_and_get_no_registry_token(self):
        probes = jobs(read())["probes"]
        self.assertIn("needs: build", probes)
        self.assertIn('ref="${IMAGE_REPO}@${DIGEST}"', probes)
        self.assertIn("docker save", probes)
        self.assertIn('[ "$checked" = "$pushed" ]', probes)
        self.assertLess(probes.index("docker logout ghcr.io"), probes.index("docker run"))
        self.assertNotIn(":latest", probes)

    def test_probes_run_the_tests_and_a_fail_closed_start_in_the_hardened_image(self):
        probes = jobs(read())["probes"]
        for flag in (
            "--network none",
            "--read-only",
            "--user 65532:65532",
            "--cap-drop ALL",
            "--security-opt no-new-privileges",
            "--pids-limit",
        ):
            self.assertIn(flag, probes)
        self.assertIn("unittest discover -s tests", probes)
        self.assertIn("startup_failed", probes)
        self.assertIn("65532:65532", probes)

    def test_scan_is_a_report_not_a_gate(self):
        scan = jobs(read())["scan"]
        self.assertIn("needs: build", scan)
        self.assertIn("--exit-code 0", scan)
        self.assertIn("--scanners vuln", scan)
        self.assertIn("actions/upload-artifact@", scan)

    def test_publish_tags_only_after_green_probes_and_verifies_the_digest(self):
        publish = jobs(read())["publish"]
        self.assertIn("needs: [build, probes]", publish)
        self.assertIn('version="${GITHUB_REF_NAME#google-reporting-v}"', publish)
        self.assertIn(
            'docker buildx imagetools create -t "${IMAGE_REPO}:${version}" "${IMAGE_REPO}@${DIGEST}"',
            publish,
        )
        self.assertIn('[ "$tagged" = "$DIGEST" ]', publish)
        self.assertNotIn(":latest", publish)

    def test_the_ci_workflow_stays_free_of_publishing(self):
        ci = (WORKFLOWS / "google-reporting.yml").read_text(encoding="utf-8")
        self.assertNotIn("packages: write", ci)
        self.assertNotIn("push-by-digest", ci)


if __name__ == "__main__":
    unittest.main()
