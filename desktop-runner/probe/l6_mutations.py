"""Exclusive, compiled Linux mutations in l6-test. Never runs Windows probes.

The container is populated from a Git archive (no Windows bind mount). Only
container copies change; finally restores their normalized source bytes.
"""
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
INSTALL = "internal/platform/install.go"
RELEASE = "../.github/workflows/desktop-runner-release.yml"
CASES = [
    ("panel-task-missing", INSTALL, "panelFallback, err = InstallWith(panel, panelExe)", "panelFallback, err = false, error(nil)", "platform", "TestInstallBeideOderVorherigerStand"),
    ("panel-task-left-by-uninstall", INSTALL, "errors.Join(UninstallWith(runner), UninstallWith(panel))", "errors.Join(UninstallWith(runner), UninstallWith(runner))", "platform", "TestUninstallBeideAuchBeiFehler"),
    ("panel-with-arguments", INSTALL, 'arguments = ""', 'arguments = "<Arguments>run</Arguments>"', "platform", "TestBedienfeldAufgabeOhneArgumente"),
    ("panel-file-check-removed", INSTALL, "if err != nil || !info.Mode().IsRegular() {", "if false && (err != nil || !info.Mode().IsRegular()) {", "platform", "TestInstallOhneBedienfeldAendertNichts"),
    ("update-rollback-removed", INSTALL, "return false, errors.Join(err, restorePanel(), restoreRunner())", "_ = restorePanel; _ = restoreRunner; return false, err", "platform", "TestInstallBeideOderVorherigerStand"),
    ("panel-missing-in-sums", RELEASE, "$panelHash *jnpt-bedienfeld.exe`n", "", "client", "TestReleaseUndWindowsCI"),
    ("panel-signature-not-verified", RELEASE, "& $signtool.FullName verify /pa release/jnpt-bedienfeld.exe", "# verification removed", "client", "TestReleaseUndWindowsCI"),
    ("panel-signature-error-ignored", RELEASE, "if ($LASTEXITCODE -ne 0) { throw 'panel_signature_invalid' }", "# rejection removed", "client", "TestReleaseUndWindowsCI"),
    ("panel-excluded-from-azure-signing", RELEASE, "files-folder-filter: exe", "files-folder-filter: desktop-runner.exe", "client", "TestReleaseUndWindowsCI"),
    ("panel-missing-in-provenance", RELEASE, "            release/jnpt-bedienfeld.exe", "", "client", "TestReleaseUndWindowsCI"),
    ("panel-build-removed", RELEASE, '          go build -trimpath -ldflags "-s -w -H=windowsgui" -o out/jnpt-bedienfeld.exe ./cmd/bedienfeld', "          # panel build removed", "client", "TestReleaseUndWindowsCI"),
    ("runner-build-console", RELEASE, "-s -w -H=windowsgui -X main.version", "-s -w -X main.version", "client", "TestReleaseUndWindowsCI"),
    ("panel-build-console", RELEASE, '-s -w -H=windowsgui" -o out/jnpt-bedienfeld.exe', '-s -w" -o out/jnpt-bedienfeld.exe', "client", "TestReleaseUndWindowsCI"),
]


def command(args):
    p = subprocess.run(args, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=180)
    return p.returncode, p.stdout.decode(errors="replace")


def copy(data, destination):
    with tempfile.TemporaryDirectory(prefix="l6-mutation-") as directory:
        path = Path(directory) / "source"
        path.write_bytes(data)
        subprocess.run(["docker", "cp", str(path), "l6-test:" + destination], check=True, stdout=subprocess.PIPE)


def main():
    for name, file, old, new, package, test in CASES:
        original = (ROOT / file).read_bytes().replace(b"\r\n", b"\n")
        source = original.decode("utf-8")
        if source.count(old) != 1:
            raise RuntimeError(name + ": expected exactly one anchor")
        destination = "/workspace/" + (".github/workflows/desktop-runner-release.yml" if file == RELEASE else "desktop-runner/" + file)
        try:
            copy(source.replace(old, new, 1).encode("utf-8"), destination)
            code, output = command(["docker", "exec", "l6-test", "go", "test", "-c", "-o", "/tmp/l6-mutant", "./internal/" + package])
            if code:
                raise RuntimeError(name + ": compilation failure is NOT rejection\n" + output)
            code, output = command(["docker", "exec", "-w", "/workspace/desktop-runner/internal/" + package, "l6-test", "/tmp/l6-mutant", "-test.timeout=30s", "-test.run=^" + test + "$"])
            if code == 0 or "--- FAIL: " + test not in output:
                raise RuntimeError(name + ": survived or infrastructure failure\n" + output)
            print(name + ": RED (" + test + ")", flush=True)
        finally:
            copy(original, destination)
            code, restored = command(["docker", "exec", "l6-test", "sha256sum", destination])
            import hashlib
            if code or restored.split()[0] != hashlib.sha256(original).hexdigest():
                raise RuntimeError(name + ": restoration failed")
    print(f"{len(CASES)} compiled mutants rejected; container sources restored; workspace untouched.", flush=True)


if __name__ == "__main__":
    main()
