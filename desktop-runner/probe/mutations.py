"""Real negative tests with a guard removed; exact restoration in finally.

Exclusive worktree access required. Output stays in the terminal.
"""
from pathlib import Path
import os
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
CASES = [
    ("package-mime", "client/http.go", 'if res.Header.Get("Content-Type") != expectedType {', 'if res.Header.Get("Content-Type") != expectedType && false {', "client", "TestPaketMedientypAmHTTPAdapter"),
    ("frame-cap", "client/engine.go", "n == 0 || n > 8<<20", "n == 0 || n > 16<<20", "client", "TestRahmenDeckelMitVollstaendigemKoerper"),
    ("wheel-platform", "client/environment.go", "if !matched {", "if !matched && false {", "client", "TestWheelTagsLockUndManifestVerbote"),
    ("yaml-unknown", "client/environment.go", "d.KnownFields(true)", "d.KnownFields(false)", "client", "TestWheelTagsLockUndManifestVerbote"),

    ("sha1dc", "client/copied_contract.go", "if collision {", "if collision && false {", "client", "TestSHA1Collision"),
    ("journal-ack-one", "client/state.go", "if err := send(r); err != nil {", "if err := send(r); err != nil && false {", "client", "TestErgebnisrueckstauUndAtomarerSlot"),
    ("atomic-slot", "client/state.go", 'j.Marker = &Marker{r, "claimed"}', "j.Marker = nil", "client", "TestErgebnisrueckstauUndAtomarerSlot"),

    ("git-extra", "client/copied_contract.go", "if len(used) != len(objects) {", "if false {", "client", "TestPaketObjektmengeModiUndNamen"),
    ("git-mode", "client/copied_contract.go", 'if mode != "40000" && mode != "100644" && mode != "100755" {', 'if false {', "client", "TestPaketObjektmengeModiUndNamen"),
    ("git-name", "client/copied_contract.go", "!ok || !safeName(name)", "!ok", "client", "TestPaketObjektmengeModiUndNamen"),
    ("git-case", "client/copied_contract.go", "if names[fold] {", "if names[fold] && false {", "client", "TestPaketObjektmengeModiUndNamen"),
    ("environment", "client/environment.go", "if e != nil || !slices.Equal(got, want) {", "if e != nil || !slices.Equal(got, want) && false {", "client", "TestUmgebungVorJedemStart"),
    ("python-origin", "client/environment.go", "&& sum(b) == e.SHA256", "", "client", "TestPythonAllowlistUndWheel"),
    ("wheel-lock", "client/environment.go", "&& slices.Contains(e.Hashes, sum(b))", "&& (slices.Contains(e.Hashes, sum(b)) || true)", "client", "TestPythonAllowlistUndWheel"),
    ("zip-path", "client/environment.go", "|| !safePath(rel) ||", "|| false ||", "client", "TestZIPAbweisungAmEntpacker"),
    ("zip-case", "client/environment.go", "ok && (old != value || !isDir)", "ok && (old != value || !isDir) && false", "client", "TestZIPAbweisungAmEntpacker"),
    ("zip-ratio", "client/environment.go", "(unpacked > 1<<20 && unpacked > int64(len(b))*64)", "(unpacked > 1<<20 && unpacked > int64(len(b))*64 && false)", "client", "TestZIPAbweisungAmEntpacker"),
    ("tls", "client/http.go", "MinVersion: tls.VersionTLS12", "MinVersion: tls.VersionTLS12, InsecureSkipVerify:true", "client", "TestPairTLSUndLeererPubkey"),
    ("fingerprint", "client/http.go", "subtle.ConstantTimeCompare(actual[:], expected) != 1", "subtle.ConstantTimeCompare(actual[:], expected) != 1 && false", "client", "TestCAFingerprintProxyUndURL"),
    ("loopback", "client/http.go", "if ip == nil || !ip.IsLoopback() {", "if (ip == nil || !ip.IsLoopback()) && false {", "client", "TestRedirectUndRemoteHTTPAbgewiesen"),
    ("public-key", "client/http.go", "|| len(key) != 32", "|| len(key) != 32 && false", "client", "TestPairTLSUndLeererPubkey"),
    ("redirect", "client/http.go", "CheckRedirect: func(*http.Request, []*http.Request) error { return ErrProtocol }", "CheckRedirect: func(*http.Request, []*http.Request) error { return nil }", "client", "TestRedirectUndRemoteHTTPAbgewiesen"),
    ("unknown-json", "client/types.go", "d.DisallowUnknownFields()", "// unknown fields allowed", "client", "TestStriktesJSONAmAdapter"),
    ("device-binding", "client/manifest.go", "|| m.DeviceID != device", "", "client", "TestManifestSyncAtomarUndOfflineAmAdapter"),
    ("sequence", "client/manifest.go", "|| m.Seq <= minSeq", "", "client", "TestManifestSyncAtomarUndOfflineAmAdapter"),
    ("signature", "client/manifest.go", "!ed25519.Verify(pub, b, sig)", "!ed25519.Verify(pub, b, sig) && false", "client", "TestManifestSyncAtomarUndOfflineAmAdapter"),
    ("offline-tools", "client/manifest.go", "len(p.DeclaredTools) == 0 &&", "", "client", "TestOfflineRegel"),
    ("offline-grace", "client/manifest.go", "now.Sub(last) < time.Duration(grace)*time.Hour", "now.Sub(last) <= time.Duration(grace)*time.Hour", "client", "TestOfflineRegel"),
    ("offline-clock", "client/manifest.go", "&& !now.Before(last)", "", "client", "TestOfflineRegel"),
    ("account", "client/types.go", "&& strings.EqualFold(s.Account, c.ExpectedAccount)", "", "client", "TestKontoUndManipulationVorSpawn"),
    ("session", "client/types.go", "verified && s.Active &&", "verified &&", "client", "TestReadyVerboteneZustaende"),
    ("idle", "client/types.go", "s.Idle >= time.Duration(c.StartPolicy.IdleMinutes)*time.Minute", "s.Idle >= 0", "client", "TestReadyVerboteneZustaende"),
    ("journal-count", "client/state.go", "len(j.Results) <= 200", "len(j.Results) <= 201", "client", "TestJournalDeckelBestaetigungNeustart"),
    ("journal-bytes", "client/state.go", "const journalMax = 16 << 20", "const journalMax = 32 << 20", "client", "TestJournalMisstVerschluesselteBytes"),
    ("journal-ack", "client/state.go", "if e := send(r); e != nil {", "if e := send(r); e != nil && false {", "client", "TestJournalDeckelBestaetigungNeustart"),
    ("interrupted", "client/state.go", 'r.End = "interrupted"', 'r.End = "ok"', "client", "TestJournalDeckelBestaetigungNeustart"),
    ("revoke", "client/state.go", "return os.RemoveAll(s.Root)", "return nil", "client", "TestWiderrufAllesWeg"),
    ("log-code", "client/log.go", "if code < LogStarted || code > LogCompleted {", "if (code < LogStarted || code > LogCompleted) && false {", "client", "TestLoggerDeckelUndFesteCodes"),
    ("autostart", "platform/install.go", "if !a.PolicyDenied(err) {", "if !a.PolicyDenied(err) && false {", "platform", "TestAutostartRueckfallNurBeiRichtlinie"),
    ("suspended", "platform/process_windows.go", "windows.CREATE_SUSPENDED |", "", "platform", "TestWindowsSuspendBisJobZuordnung"),
    ("job-close", "platform/process_windows.go", "limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE", "limits.BasicLimitInformation.LimitFlags = 0", "platform", "TestWindowsJobCloseBeendetEnkel"),
    ("task-privilege", "platform/install_windows.go", "<RunLevel>LeastPrivilege</RunLevel>", "<RunLevel>HighestAvailable</RunLevel>", "platform", "TestWindowsAufgabeOhneAdmin"),
]


def run(cmd, cwd, timeout=120):
    return subprocess.run(cmd, cwd=cwd, stdout=subprocess.PIPE,
                          stderr=subprocess.STDOUT, timeout=timeout)


def main():
    selected = set(sys.argv[1:])
    count = 0
    (ROOT / ".probe").mkdir(exist_ok=True)
    for label, rel, old, new, package, test in CASES:
        if selected and label not in selected:
            continue
        if os.name != "nt" and "windows" in rel:
            continue
        path = ROOT / "internal" / rel
        original = path.read_bytes()
        text = original.decode("utf-8")
        if text.count(old) != 1:
            raise RuntimeError(f"{label}: expected one guard, got {text.count(old)}")
        binary = ROOT / ".probe" / ("mutation-" + package + (".exe" if os.name == "nt" else ""))
        try:
            mutated=text.replace(old,new,1)
            if label == "zip-case":
                mutated=mutated.replace("os.O_CREATE|os.O_EXCL|os.O_WRONLY", "os.O_CREATE|os.O_TRUNC|os.O_WRONLY")
            path.write_text(mutated, encoding="utf-8", newline="")
            built = run(["go", "test", "-c", "-o", str(binary), "./internal/" + package], ROOT)
            if built.returncode:
                raise RuntimeError(label + ": compile failure does not count\n" + built.stdout.decode(errors="replace"))
            tested = run([str(binary), "-test.run=^" + test + "$", "-test.timeout=25s"], ROOT / "internal" / package, 40)
            if tested.returncode == 0 or b"--- FAIL: " + test.encode() not in tested.stdout:
                raise RuntimeError(label + ": mutant survived or infrastructure failed\n" + tested.stdout.decode(errors="replace"))
            print(label + ": RED (guard removed)", flush=True)
            count += 1
        finally:
            path.write_bytes(original)
    print(f"{count} mutations rejected; all source files restored.", flush=True)


if __name__ == "__main__":
    main()
