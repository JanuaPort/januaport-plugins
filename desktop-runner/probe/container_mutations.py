"""Container-Sperren einzeln entfernen; kompilierende rote Tests verlangen.

Exklusiver Worktree. Kein Bericht auf Platte, bytegenaue Wiederherstellung.
Optional: --container NAME führt Go im lokalen Linux-Prüfcontainer aus.
"""
from pathlib import Path
import argparse
import subprocess

ROOT = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser()
parser.add_argument("--container")
parser.add_argument("--only", nargs="+")
args = parser.parse_args()
prefix = (["docker", "exec", "-e", "TMPDIR=/go/tmp", args.container]
          if args.container else [])
cases = [
    ("secret-regular-file", "client/container.go", "!info.Mode().IsRegular()", "(false && !info.Mode().IsRegular())", "client", "TestGeheimnisseNurDeklariertUndOhneSymlink"),
    ("secret-directory-symlink", "client/secrets_linux.go", "os.Lstat(p)", "os.Stat(p)", "client", "TestGeheimnisseNurDeklariertUndOhneSymlink"),
    ("cleanup-pending", "client/container.go", 'strings.TrimSpace(string(b)) == ""', '(strings.TrimSpace(string(b)) == "" || true)', "client", "TestContainerCleanupWartetAufEntfernung"),
    ("mounted-only-after-start", "client/container.go", 'started := err == nil || errors.Is(err, errContainerExit)', 'started := true || err == nil || errors.Is(err, errContainerExit)', "client", "TestKeinGeheimnisMountBeiFehlgeschlagenemStart"),
    ("runner-root", "../server_linux.go", "os.Geteuid() == 0", "(false && os.Geteuid() == 0)", ".", "TestLinuxKonfigurationUndDienst"),
    ("state-root", "../server_linux.go", 'if c.StateDir == "/" {', 'if false && c.StateDir == "/" {', ".", "TestLinuxKonfigurationUndDienst"),
    ("heartbeat-cancel", "client/engine.go", 'if key != "" && slices.Contains(h.Cancel, key) {', 'if false && key != "" && slices.Contains(h.Cancel, key) {', "client", "TestContainerHeartbeatAbbruchAmHTTPAdapter"),
    ("cleanup-next-run", "client/container.go", "if d.blocked.Load() {", "if d.blocked.Load() && false {", "client", "TestContainerBereinigungsfehlerSperrtFolgelauf"),
    ("privileged", "client/container.go", '[]string{"run", "--interactive"', '[]string{"run", "--privileged", "--interactive"', "client", "TestDockerArgumenteGolden"),
    ("network-host", "client/container.go", '[]string{"host", "bridge"', '[]string{"bridge"', "client", "TestDockerVerboteneOptionen/host$"),
    ("token-env", "client/container.go", 'archive, err := jobArchive(input, token)', 'args = append([]string{args[0], "--env", "JNPT_RUN_TOKEN="+token}, args[1:]...)\n archive, err := jobArchive(input, token)', "client", "TestTokenNichtInInspect"),
    ("secret-undeclared", "client/container.go", 'for _, name := range p.Container.SecretNames {', 'for _, name := range allowed {', "client", "TestGeheimnisseNurDeklariertUndOhneSymlink"),
    ("secret-symlink", "client/container.go", 'info, err := os.Lstat(path)', 'info, err := os.Stat(path)', "client", "TestGeheimnisseNurDeklariertUndOhneSymlink"),
    ("digest", "client/container.go", '|| !repoDigestMatches(b, p.Commit)', '|| (false && !repoDigestMatches(b, p.Commit))', "client", "TestDigestVergleichNachPull"),
    ("orphan", "client/container.go", 'if err = d.cleanup(id); err != nil {', 'if err = error(nil); err != nil {', "client", "TestVerwaisteContainerNachNeustart"),
    ("unknown-options", "client/types.go", 'd.DisallowUnknownFields()', '// unknown fields accepted', "client", "TestDockerVerboteneOptionen"),
    ("resource-limits", "client/container.go", 'if c.CPUMillis < 1 ||', 'if false && c.CPUMillis < 1 ||', "client", "TestContainerGrenzenAmExecutor"),
    ("command-length", "client/container.go", 'len(arg) > 1024 ||', 'false && len(arg) > 1024 ||', "client", "TestContainerGrenzenAmExecutor"),
    ("secret-traversal", "client/container.go", '|| strings.Contains(n, "..") ||', '|| false ||', "client", "TestContainerGrenzenAmExecutor"),
    ("secret-duplicate", "client/container.go", '|| seen[n] {', '|| false {', "client", "TestContainerGrenzenAmExecutor"),
    ("old-docker", "client/container.go", '|| n < 41 {', '|| (n < 0 && false) {', "client", "TestRuntimeFehltOderZuAlt"),
    ("old-podman", "client/container.go", '|| n < 4 {', '|| (n < 0 && false) {', "client", "TestRuntimeFehltOderZuAlt"),
    ("container-signature", "client/manifest.go", '!ed25519.Verify(pub, b, sig)', '(false && !ed25519.Verify(pub, b, sig))', "client", "TestVertrag2ManifestGolden"),
    ("contract-downgrade", "client/engine.go", 'if m.Contract != e.API.Contract() || (m.Contract == 2) != (e.Container != nil) {', 'if false {', "client", "TestVertrag2AmHTTPAdapterUndBeideStartwege"),
    ("mounted-names-required", "client/http.go", '(a.Contract() == 2 && (r.MountedSecretNames == nil || *r.MountedSecretNames == nil || !secretNamesValid(*r.MountedSecretNames)))', '(false && a.Contract() == 2 && (r.MountedSecretNames == nil || *r.MountedSecretNames == nil || !secretNamesValid(*r.MountedSecretNames)))', "client", "TestVertrag2VerweigertFremdeArtUndGeheimnisfelder"),
    ("linux-integrity", "platform/security_linux.go", 'if !hmac.Equal(m.Sum(nil), b[:sha256.Size]) {', 'if !hmac.Equal(m.Sum(nil), b[:sha256.Size]) && false {', "platform", "TestLinuxDateirechteJournalUndIntegritaet"),
    ("linux-private-mode", "platform/security_linux.go", '&& info.Mode().Perm() == mode', '&& (info.Mode().Perm() == mode || true)', "platform", "TestLinuxPrivatePfadeVerweigernSymlinkUndFremdeRechte"),
]

# Each independent value guard is also exercised by its actual forbidden input.
for label, guard, test in [
    ("cpu-upper", "c.CPUMillis > 16000", "TestContainerGrenzenAmExecutor"),
    ("ram-lower", "c.MemoryBytes < 1", "TestContainerGrenzenAmExecutor"),
    ("ram-upper", "c.MemoryBytes > 32<<30", "TestContainerGrenzenAmExecutor"),
    ("wall-lower", "c.WallS < 1", "TestContainerGrenzenAmExecutor"),
    ("wall-upper", "c.WallS > 86400", "TestContainerGrenzenAmExecutor"),
    ("command-count", "len(c.Command) > 32", "TestContainerGrenzenAmExecutor"),
    ("command-empty", 'c.Command[0] == ""', "TestContainerGrenzenAmExecutor"),
    ("command-nul", "strings.ContainsRune(arg, 0)", "TestContainerGrenzenAmExecutor"),
    ("command-utf8", "!utf8.ValidString(arg)", "TestContainerGrenzenAmExecutor"),
    ("secret-regex", "!containerSecret.MatchString(n)", "TestContainerGrenzenAmExecutor"),
    ("image-tag", "!imageDigest.MatchString(c.Image)", "TestDockerVerboteneOptionen"),
    ("network-namespace", "!containerNetwork.MatchString(c.Network)", "TestDockerVerboteneOptionen"),
]:
    cases.append((label, "client/container.go", guard, "(false && "+guard+")", "client", test))

for label, old, new in [
    ("job-root", '"65532:65532"', '"0:0"'),
    ("read-only", '"--read-only",', ''),
    ("cap-drop", '"--cap-drop", "ALL",', ''),
    ("no-new-privileges", '"no-new-privileges"', '"no-new-privileges=false"'),
    ("pids-limit", '"--pids-limit", "128"', '"--pids-limit", "0"'),
    ("pull-never", '"--pull", "never"', '"--pull", "always"'),
    ("init", '"--init",', ''),
    ("auto-rm", '"--rm",', ''),
]:
    cases.append((label, "client/container.go", old, new, "client", "TestDockerArgumenteGolden"))
cases.append(("token-mode", "client/container.go", "Mode: 0400", "Mode: 0600", "client", "TestTokenNichtInInspect"))

if args.only:
    available = {case[0] for case in cases}
    if set(args.only) - available:
        raise SystemExit("Unbekannte Mutationsprobe")
    cases = [case for case in cases if case[0] in args.only]

for name, file, old, new, package, test in cases:
    path = ROOT / "internal" / file
    original = path.read_bytes()
    text = original.decode()
    if text.count(old) != 1:
        raise SystemExit(f"{name}: Sperre nicht eindeutig ({text.count(old)})")
    try:
        path.write_bytes(text.replace(old, new, 1).encode())
        result = subprocess.run(prefix + ["go", "test", "-count=1", "-timeout=90s", ("." if package == "." else "./internal/"+package), "-run", "^"+test], cwd=ROOT, capture_output=True, text=True)
        output = result.stdout + result.stderr
        if result.returncode == 0 or "[build failed]" in output or "--- FAIL: " not in output or "panic: test timed out" in output:
            print(output, flush=True)
            raise SystemExit(f"{name}: kein gültiger roter Test")
        print(f"ROT {name}: {test}", flush=True)
    finally:
        path.write_bytes(original)

print(f"{len(cases)} Mutationsproben rot; alle Quellen wiederhergestellt.", flush=True)
