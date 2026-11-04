"""L1 gate mutations in the existing l1-test container; restore exact bytes.

Run exclusively, without concurrent tests: python probe/l1_mutations.py [case ...]
The container must mount the repository at /workspace and use golang:1.26.10.
Compilation failure, missing tests and infrastructure failures never count as red.
"""
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
CASES = [
    ("windows-contract", "http.go", "return WindowsContract", "return 1", "TestVertragJeGeraeteart"),
    ("server-contract", "http.go", "a.contract = 2", "a.contract = 4", "TestVertragJeGeraeteart"),
    ("strict-apps", "types.go", "d.DisallowUnknownFields()", "// mutation: accept unknown fields", "TestManifestV4StriktAmAdapter"),
    ("label40", "manifest.go", "!shortSetupText(s.Label, 40)", "false", "TestManifestV4StriktAmAdapter"),
    ("setup-schema", "manifest.go", "!validSetup(setup)", "false", "TestSetupSchemaAmAdapter"),
    ("setup-duplicate", "manifest.go", "setups[setup.Name] ||", "", "TestSetupSchemaAmAdapter"),
    ("pin-grace-schema", "manifest.go", "(p.GraceHours != nil && (m.Contract < 4 || *p.GraceHours < 1 || *p.GraceHours > 72))", "false", "TestManifestV4StriktAmAdapter"),
    ("v3-grace", "manifest.go", "m.Contract < 4 || *p.GraceHours", "false || *p.GraceHours", "TestSetupSchemaAmAdapter"),
    ("old-setup", "manifest.go", "(p.Setups != nil && m.Contract < 3)", "false", "TestSetupSchemaAmAdapter"),
    ("test-device-1298", "types.go", '`json:"test_device,omitempty"`', '`json:"-"`', "TestTestgeraeteSynchronisieren1298"),
    ("screenshot-test-device", "http.go", "!m.TestDevice ||", "", "TestScreenshotNurTestgeraet"),
    ("screenshot-test-pin", "http.go", 'j.Pin != "test_"+c.DeviceID[:8] ||', "", "TestScreenshotNurTestgeraet"),
    ("result-test-device", "http.go", "!m.TestDevice ||", "", "TestErgebnisNurTestauftrag"),
    ("result-test-pin", "http.go", 'j.Pin != "test_"+c.DeviceID[:8] ||', "", "TestErgebnisNurTestauftrag"),
    ("result-normal-route", "http.go", "if len(r.Result) != 0 {", "if false {", "TestErgebnisNurTestauftrag"),
    ("test-signature", "http.go", "err != nil || !m.TestDevice", "err != nil && false || !m.TestDevice", "TestTestadapterVerweigernFalscheBindung"),
    ("test-commit", "http.go", "p.Commit == j.Commit &&", "", "TestTestadapterVerweigernFalscheBindung"),
    ("test-path", "http.go", "p.Path == j.Path &&", "", "TestTestadapterVerweigernFalscheBindung"),
    ("test-algo", "http.go", "p.Algo == j.Algo &&", "", "TestTestadapterVerweigernFalscheBindung"),
    ("test-sequence", "http.go", "j.ManifestSeq > m.Seq", "false", "TestTestadapterVerweigernFalscheBindung"),
    ("test-job-id", "http.go", "!uuidRE.MatchString(j.JobID) || j.ManifestSeq", "false || j.ManifestSeq", "TestTestadapterVerweigernFalscheBindung"),
    ("screenshot-contract", "http.go", "if a.Contract() != WindowsContract || !o.verified(c) || len(png)", "if false || !o.verified(c) || len(png)", "TestTestadapterVerweigernFalscheBindung"),
    ("screenshot-size", "http.go", "len(png) > 4<<20 ||", "", "TestTestadapterVerweigernFalscheBindung"),
    ("screenshot-png", "http.go", "!bytes.HasPrefix(png, []byte{137, 80, 78, 71, 13, 10, 26, 10})", "false", "TestTestadapterVerweigernFalscheBindung"),
    ("result-size", "http.go", "len(r.Result) > 64<<10", "false", "TestTestadapterVerweigernFalscheBindung"),
    ("result-sequence", "http.go", "r.ManifestSeq != o.ManifestSeq ||", "", "TestTestadapterVerweigernFalscheBindung"),
    ("result-commit", "http.go", "r.Commit != o.Commit ||", "", "TestTestadapterVerweigernFalscheBindung"),
    ("result-contract", "http.go", "if a.Contract() != WindowsContract || !o.verified(c) || r.JobID", "if false || !o.verified(c) || r.JobID", "TestTestadapterVerweigernFalscheBindung"),
    ("result-order", "http.go", "!o.verified(c) || r.JobID", "false || r.JobID", "TestErgebnisNurTestauftrag"),
    ("screenshot-order", "http.go", "!o.verified(c) || len(png)", "false || len(png)", "TestScreenshotNurTestgeraet"),
    ("result-job", "http.go", "r.JobID != o.JobID ||", "", "TestTestadapterVerweigernFalscheBindung"),
    ("result-offline", "http.go", "r.RanOffline ||", "", "TestTestadapterVerweigernFalscheBindung"),
    ("result-run-key", "http.go", 'r.RunKey != "" ||', "", "TestTestadapterVerweigernFalscheBindung"),
    ("setup-manifest", "http.go", "if !found {", "if !found && false {", "TestSetupStartVerweigertFremdeFreigaben"),
    ("setup-contract", "http.go", "a.Contract() != WindowsContract || m.Contract != WindowsContract", "false", "TestSetupStartVerweigertFremdeFreigaben"),
    ("setup-input", "http.go", "if !inputOK(frei) {", "if false {", "TestSetupStartVerweigertFremdeFreigaben"),
    ("setup-response-binding", "http.go", 'out.RunKey != "setup/"+pin+"/"+setup+"/"+nonce ||', "", "TestSetupStartVerweigertFremdeFreigaben"),
    ("setup-nonce", "http.go", "nonce := base64.RawURLEncoding.EncodeToString(random[:])", 'nonce := strings.Repeat("a", 22)', "TestSetupUndHaltAmHTTPAdapter"),
    ("setup-sequence", "http.go", 'body, m.Seq, "", (256<<10)+16<<10', 'body, 0, "", (256<<10)+16<<10', "TestSetupUndHaltAmHTTPAdapter"),
    ("halt-validation", "http.go", 'if a.Contract() != WindowsContract || (state != "arbeitet"', 'if false && (a.Contract() != WindowsContract) || (false && state != "arbeitet"', "TestSetupUndHaltAmHTTPAdapter"),
    ("halt-until", "http.go", 'until != "" && (state != "pausiert"', 'false && (state != "pausiert"', "TestSetupUndHaltAmHTTPAdapter"),
    ("halt-contract", "http.go", 'a.Contract() != WindowsContract || (state != "arbeitet"', 'false || (state != "arbeitet"', "TestVertrag4ClaimUndErgebnisOhneServerfelder"),
    ("pin-grace", "manifest.go", "if p.GraceHours != nil {\n\t\tgrace = *p.GraceHours", "if p.GraceHours != nil {\n\t\tgrace = grace", "TestOfflineFristJePinUndUhrMarke"),
    ("clock-persistence", "state.go", 'if err := s.Save("clock", now); err != nil {', 'if err := error(nil); err != nil {', "TestOfflineFristJePinUndUhrMarke"),
    ("clock-rollback", "state.go", "if high.Before(now) {", "if true {", "TestOfflineFristJePinUndUhrMarke"),
    ("clock-monotonic", "state.go", "floor := s.clockHigh.Add(observed.Sub(s.clockSeen))", "floor := s.clockHigh", "TestOfflineFristJePinUndUhrMarke"),
    ("clock-corruption", "state.go", 'err != nil && !os.IsNotExist(err) {\n\t\treturn time.Time{}, err', 'err != nil && !os.IsNotExist(err) && false {\n\t\treturn time.Time{}, err', "TestOfflineFristJePinUndUhrMarke"),
    ("offline-refusal", "engine.go", "if !OfflineAllowed(p, m.GraceHours, b.Manifest.LastSync, now) {", "if !OfflineAllowed(p, m.GraceHours, b.Manifest.LastSync, now) && false {", "TestOfflineFristJePinUndUhrMarke"),
    ("offline-log", "engine.go", "e.logCode(LogOfflineExpired, 1)", "e.logCode(LogRefused, 1)", "TestOfflineFristJePinUndUhrMarke"),
    ("offline-mark", "engine.go", "// Windows K8 also marks refusals for the core's offline counters.\n\t\tif e.Container == nil {", "// mutation\n\t\tif e.Container != nil {", "TestOfflineNachmeldungAmAdapter"),
    ("archive-longest", "manifest.go", "hours = max(hours, grace)", "hours = min(hours, grace)", "TestK8ArchivLaengsteWirksameFrist"),
    ("journal-replay", "engine.go", 'return e.API.ReportTest(ctx, r, e.Credential, order)', 'return e.API.Report(ctx, r)', "TestErgebnisNachNeustartMitSignierterFreigabe"),
    ("claim-desktop-path", "http.go", 'a.Contract() != 2 && (!safePath(j.Path)', 'a.Contract() == 1 && (!safePath(j.Path)', "TestVertrag4ClaimUndErgebnisOhneServerfelder"),
    ("report-desktop-fields", "http.go", 'a.Contract() != 2 && r.MountedSecretNames', 'a.Contract() == 1 && r.MountedSecretNames', "TestVertrag4ClaimUndErgebnisOhneServerfelder"),
    ("legacy-cache-readiness", "engine.go", "if b.Config.Contract != WindowsContract {", "if false {", "TestAlterCacheSperrtClaims"),
    ("cache-upgrade", "engine.go", "!(e.Container == nil && (m.Contract == 1 || m.Contract == 3 || m.Contract == 4))", "true", "TestTestgeraetUpgradeSynchronisiertVertrag4"),
]


def command(args, timeout=120):
    return subprocess.run(args, cwd=ROOT, stdout=subprocess.PIPE,
                          stderr=subprocess.STDOUT, timeout=timeout)


def main():
    selected = set(sys.argv[1:])
    count = 0
    for label, file, old, new, test in CASES:
        if selected and label not in selected:
            continue
        path = ROOT / "internal" / "client" / file
        original = path.read_bytes()
        source = original.decode("utf-8").replace("\r\n", "\n")
        if source.count(old) != 1:
            raise RuntimeError(f"{label}: expected one anchor, found {source.count(old)}")
        try:
            path.write_bytes(source.replace(old, new, 1).encode("utf-8"))
            built = command(["docker", "exec", "l1-test", "go", "test", "-c", "-o", "/tmp/l1-mutant", "./internal/client"])
            if built.returncode:
                raise RuntimeError(f"{label}: compile failure does not count\n" + built.stdout.decode(errors="replace"))
            tested = command(["docker", "exec", "-w", "/workspace/desktop-runner/internal/client", "l1-test", "/tmp/l1-mutant", "-test.run=^" + test + "$", "-test.timeout=25s"], 40)
            if tested.returncode == 0 or b"--- FAIL: " + test.encode() not in tested.stdout:
                raise RuntimeError(f"{label}: survived or infrastructure failed\n" + tested.stdout.decode(errors="replace"))
            print(f"{label}: RED ({test})", flush=True)
            count += 1
        finally:
            path.write_bytes(original)
            if path.read_bytes() != original:
                raise RuntimeError(f"{label}: restoration failed")
    print(f"{count} compiled mutants rejected; exact source bytes restored.", flush=True)


if __name__ == "__main__":
    main()
