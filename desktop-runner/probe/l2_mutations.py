"""L2 mutations in l2-test (golang:1.26.10, /workspace bind mount).

Run exclusively: python probe/l2_mutations.py [case ...]
Each mutant must compile and fail its named test; restore exact source bytes.
"""
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
CASES = [
    ("halt-ram-only", "halt.go", 'if err := e.State.Save("halt", h); err != nil {', 'if err := error(nil); err != nil {', "TestHaltUeberstehtNeustart"),
    ("corruption-works", "halt.go", 'if err != nil || !e.halt.valid() {\n\t\te.haltFaultLocked()', 'if err != nil || !e.halt.valid() {\n\t\te.halt = haltState{State: "arbeitet"}', "TestHaltDateiFehltOderDefekt"),
    ("missing-stops", "halt.go", 'if os.IsNotExist(err) {\n\t\te.halt = haltState{State: "arbeitet"}', 'if os.IsNotExist(err) {\n\t\te.halt = haltState{State: "angehalten"}', "TestHaltDateiFehltOderDefekt"),
    ("fault-log", "halt.go", 'e.logCode(LogHaltFault, 1)', 'e.logCode(LogRefused, 1)', "TestHaltDateiFehltOderDefekt"),
    ("halt-no-kill", "halt.go", '_ = e.active.Kill()', '// mutation: no kill', "TestAnhaltenBeendetLaufOhneNetz"),
    ("halt-no-cancel", "halt.go", 'e.activeCancel()', '// mutation: no cancel', "TestAnhaltenBeendetLaufOhneNetz"),
    ("pause-kills", "halt.go", 'if e.halt.State == "angehalten" {', 'if e.halt.State != "arbeitet" {', "TestPauseLaesstLaufendenZuEnde"),
    ("claim-gate", "halt.go", 'if e.haltedLocked(time.Now()) {', 'if false {', "TestPauseLaesstLaufendenZuEnde"),
    ("claim-cancel", "halt.go", 'e.claimCancel()', '// mutation: keep polling', "TestHaltBrichtOffenenClaimAb"),
    ("schedule-gate", "halt.go", 'return e.haltedLocked(time.Now()) || !slot.After(e.haltThrough)', 'return false', "TestHaltDirekteStartwegeGesperrt"),
    ("schedule-admission", "halt.go", 'if e.scheduleHaltedLocked(slot) {', 'if false {', "TestHaltSperrtSlotreservierung"),
    ("adhoc-gate", "engine.go", 'if e.workHalted() {\n\t\treturn nil', 'if false {\n\t\treturn nil', "TestHaltDirekteStartwegeGesperrt"),
    ("execute-early-gate", "engine.go", 'if e.workHalted() {\n\t\treturn finish(r, "cancelled", "")', 'if false {\n\t\treturn finish(r, "cancelled", "")', "TestHaltDirekteStartwegeGesperrt"),
    ("spawn-gate", "engine.go", 'if e.haltedLocked(time.Now()) {', 'if false {', "TestHaltZwischenClaimUndSpawn"),
    ("spawn-stopped-only", "engine.go", 'if e.haltedLocked(time.Now()) {', 'if e.haltedLocked(time.Now()) && e.halt.State == "angehalten" {', "TestHaltZwischenClaimUndSpawn"),
    ("writeframe-cancel-label", "engine.go", 'if runCtx.Err() != nil {\n\t\t\treturn finish(r, "cancelled", "")', 'if runCtx.Err() != nil && false {\n\t\t\treturn finish(r, "cancelled", "")', "TestHaltBeiWriteFrameFehler"),
    ("writeframe-spurious-cancel", "engine.go", 'if runCtx.Err() != nil {\n\t\t\treturn finish(r, "cancelled", "")', 'if runCtx.Err() != nil || true {\n\t\t\treturn finish(r, "cancelled", "")', "TestHaltBeiWriteFrameFehler"),
    ("spawn-check-before-lock", "engine.go", 'e.mu.Lock()\n\t// L2-1: this check and Spawn must remain in the same critical section.\n\tif e.haltedLocked(time.Now()) {', 'e.mu.Lock()\n\tblocked := e.haltedLocked(time.Now())\n\te.mu.Unlock()\n\te.mu.Lock()\n\tif blocked {', "TestHaltZwischenClaimUndSpawn"),
    ("resume-catchup", "halt.go", '|| !slot.After(e.haltThrough)', '|| false', "TestFortsetzenHoltNichtsNach"),
    ("resume-starts", "halt.go", 'return e.setHaltLocked(h, now)', 'if state == "arbeitet" && e.API != nil { _, _ = e.API.Start(context.Background(), Result{RunKey: "demo/daily/2026-11-03T10:00:00Z", ManifestSeq: 1}) }; return e.setHaltLocked(h, now)', "TestHaltBefehleStartenNie"),
    ("resume-account-bypass", "engine.go", 'why = startRefusal(s, config)', 'why = nil', "TestHaltBefehleStartenNie"),
    ("pause-weakens-stop", "halt.go", 'if state == "pausiert" && e.halt.State == "angehalten" {', 'if false {', "TestHaltBefehleStartenNie"),
    ("pause-minutes", "halt.go", 'if minutes != 0 && minutes != 15 && minutes != 60 {', 'if false {', "TestHaltBefehleStartenNie"),
    ("halt-command-schema", "halt.go", 'if !h.valid() || e.Container != nil {', 'if e.Container != nil {', "TestHaltBefehleStartenNie"),
    ("halt-server-boundary", "halt.go", 'if !h.valid() || e.Container != nil {', 'if !h.valid() {', "TestHaltLaesstServerUnveraendert"),
    ("halt-run-sender", "engine.go", 'e.runHalt(ctx)', '_ = ctx', "TestHaltRunSperrtClaimsUndHaeltHeartbeat"),
    ("pause-no-expiry", "halt.go", 'if !now.Before(until) {', 'if !now.Before(until) && false {', "TestPauseEndetAutomatisch"),
    ("save-error-works", "halt.go", 'e.haltFaultLocked()\n\t\treturn err', 'return err', "TestHaltSpeicherfehlerSperrt"),
    ("halt-rate", "halt.go", '< 10*time.Second', '< 0*time.Second', "TestHaltMeldungKoalesziert"),
    ("halt-no-retry", "halt.go", 'if err == nil && version == e.haltVersion {', 'if (err == nil || err != nil) && version == e.haltVersion {', "TestHaltMeldungWiederholtFehler"),
    ("halt-old-ack", "halt.go", 'err == nil && version == e.haltVersion', 'err == nil && version <= e.haltVersion', "TestHaltMeldungNeustartUndBestaetigung"),
    ("halt-restart-rate", "halt.go", 'if !previous.IsZero() && now.Sub(previous) < 10*time.Second {', 'if !previous.IsZero() && now.Sub(previous) < 0*time.Second {', "TestHaltMeldungNeustartUndBestaetigung"),
    ("halt-unreadable-rate", "halt.go", 'err != nil && !os.IsNotExist(err) {', 'err != nil && !os.IsNotExist(err) && false {', "TestHaltMeldungOhneDauerhafteBremseGesperrt"),
    ("halt-unwritten-rate", "halt.go", 'if err := e.State.Save("halt-attempt", now.UTC()); err != nil {', 'if err := e.State.Save("halt-attempt", now.UTC()); err != nil && false {', "TestHaltMeldungOhneDauerhafteBremseGesperrt"),
]


def command(args, timeout=120):
    return subprocess.run(args, cwd=ROOT, stdout=subprocess.PIPE,
                          stderr=subprocess.STDOUT, timeout=timeout)


def main():
    selected = set(sys.argv[1:])
    unknown = selected - {case[0] for case in CASES}
    if unknown:
        raise RuntimeError(f"unknown cases: {unknown}")
    count = 0
    for label, file, old, new, test in CASES:
        if selected and label not in selected:
            continue
        path = ROOT / "internal" / "client" / file
        original = path.read_bytes()
        source = original.decode("utf-8").replace("\r\n", "\n")
        # Both the monotonic in-process gate and persisted restart gate enforce
        # the same limit. Remove both for the rate-limit mutation.
        expected = 2 if label == "halt-rate" else 1
        if source.count(old) != expected:
            raise RuntimeError(f"{label}: expected {expected} anchors, found {source.count(old)}")
        try:
            path.write_bytes(source.replace(old, new).encode("utf-8"))
            built = command(["docker", "exec", "l2-test", "go", "test", "-c", "-o", "/tmp/l2-mutant", "./internal/client"])
            if built.returncode:
                raise RuntimeError(f"{label}: compile failure does not count\n" + built.stdout.decode(errors="replace"))
            tested = command(["docker", "exec", "-w", "/workspace/desktop-runner/internal/client", "l2-test", "/tmp/l2-mutant", "-test.run=^" + test + "$", "-test.timeout=25s"], 40)
            if tested.returncode == 0 or b"--- FAIL: " + test.encode() not in tested.stdout:
                raise RuntimeError(f"{label}: survived or infrastructure failed\n" + tested.stdout.decode(errors="replace"))
            if label == "spawn-stopped-only" and b"--- FAIL: " + test.encode() + b"/pausiert" not in tested.stdout:
                raise RuntimeError("pause must detect the weakened spawn gate\n" + tested.stdout.decode(errors="replace"))
            print(f"{label}: RED ({test})", flush=True)
            count += 1
        finally:
            path.write_bytes(original)
            if path.read_bytes() != original:
                raise RuntimeError(f"{label}: restoration failed")
    print(f"{count} compiled mutants rejected; exact source bytes restored.", flush=True)


if __name__ == "__main__":
    main()
