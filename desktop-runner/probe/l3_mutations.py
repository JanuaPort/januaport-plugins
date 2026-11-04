"""L3 gates, exclusively in l3-test (golang:1.26.10, /workspace).

Default: compile each mutant, run the Linux gate, restore exact bytes. Native
mutants are cross-compiled only, explicitly NOT counted as rejected. LEAD can
run their native gates on Windows with --native. Never runs a GUI or sends data.
"""
from pathlib import Path
import os
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]


def edit(file, old, new, count=1):
    return (file, old, new, count)


def p(file, old, new, count=1):
    return edit("internal/pipe/" + file, old, new, count)


def c(file, old, new, count=1):
    return edit("internal/client/" + file, old, new, count)


CASES = [
    ("dacl-app-packages", "pipe", "TestPipeDACLStruktur", [p("security.go", 'D:P(A;;0x12008b;;;', 'D:P(A;;GR;;;AC)(A;;0x12008b;;;')]),
    ("medium-label", "pipe", "TestPipeDACLStruktur", [p("security.go", 'S:(ML;;NWNR;;;ME)', '')]),
    ("generic-write", "pipe", "TestPipeDACLStruktur", [p("security.go", '0x12008b', '0x12019f', 2)]),
    ("remote-flag", "pipe", "TestPipeDACLStruktur", [p("transport_windows.go", '|windows.PIPE_REJECT_REMOTE_CLIENTS', '')]),
    ("first-instance", "pipe", "TestPipeDACLStruktur", [p("transport_windows.go", '|windows.FILE_FLAG_FIRST_PIPE_INSTANCE', '')]),
    ("server-folder", "pipe", "TestClientPrueftServerbild", [p("security.go", '!sameFolder(own.Image, server.Image, "desktop-runner.exe")', 'false')]),
    ("server-user-sid", "pipe", "TestClientPrueftServerbild", [p("security.go", 'own.UserSID != server.UserSID', 'false')]),
    ("server-publisher", "pipe", "TestClientPrueftServerbild", [p("security.go", 'own.Publisher != server.Publisher', 'false')]),
    ("unsigned-peers", "pipe", "TestClientPrueftServerbild", [p("security.go", 'own.Publisher == ""', 'false')]),
    ("squatting-log", "client", "TestPipeBesetztStartetOhnePipe", [c("pipe.go", 'e.logCode(LogPipeFault, 1)', 'e.logCode(LogRefused, 1)')]),
    ("squatting-retry", "client", "TestPipeBesetztStartetOhnePipe", [c("pipe.go", 'e.logCode(LogPipeFault, 1)', 'listen(); e.logCode(LogPipeFault, 1)')]),
    ("frame-size", "pipe", "TestPipeBefehleGeschlossen", [p("schema.go", 'n > MaxFrame', 'n > MaxFrame*2')]),
    ("json-size", "pipe", "TestPipeBefehleGeschlossen", [p("schema.go", 'len(b) > MaxFrame ||', 'len(b) > MaxFrame*2 ||')]),
    ("additional-fields", "pipe", "TestPipeBefehleGeschlossen", [p("schema.go", '!exactKeys(raw, req, opt)', '!exactKeys(raw, req, opt) && false')]),
    ("duplicate-keys", "pipe", "TestPipeBefehleGeschlossen", [p("schema.go", '!ok || seen[key]', '!ok || seen[key] && false')]),
    ("pause-minutes", "pipe", "TestPipeBefehleGeschlossen", [p("schema.go", '(*m.Minuten == 0 || *m.Minuten == 15 || *m.Minuten == 60)', 'true')]),
    ("answer-range", "pipe", "TestPipeBefehleGeschlossen", [p("schema.go", '*m.Wahl >= 0 && *m.Wahl < 4', 'true')]),
    ("question-text", "pipe", "TestPipeServerSchema", [p("schema.go", 'bounded(m.Text, 500)', 'bounded(m.Text, 501)')]),
    ("question-options", "pipe", "TestPipeServerSchema", [p("schema.go", 'len(m.Optionen) >= 2 && len(m.Optionen) <= 4', 'true')]),
    ("question-option-text", "pipe", "TestPipeServerSchema", [p("schema.go", 'bounded(s, 40)', 'bounded(s, 41)')]),
    ("question-deadline-range", "pipe", "TestPipeServerSchema", [p("schema.go", 'm.FristS >= 5 && m.FristS <= 3600', 'true')]),
    ("question-duplicate", "pipe", "TestFrageNurLokal", [p("questions.go", 'if h.pending != nil {\n\t\th.mu.Unlock()', 'if false {\n\t\th.mu.Unlock()')]),
    ("question-wrong-id", "pipe", "TestFrageNurLokal", [p("questions.go", 'h.pending.id != id', 'false')]),
    ("question-choice", "pipe", "TestFrageNurLokal", [p("questions.go", 'wahl >= h.pending.count', 'false')]),
    ("question-timeout", "pipe", "TestFrageFristUndDisconnect", [p("questions.go", 'time.Duration(q.FristS) * time.Second', 'time.Duration(q.FristS) * time.Hour', 2)]),
    ("question-late-answer", "pipe", "TestFrageAntwortNachFristUndDoppelt", [p("questions.go", '!time.Now().Before(h.pending.deadline)', 'false')]),
    ("question-removal-capacity", "pipe", "TestFrageQueueReserviertEntfernung", [p("questions.go", 'if len(events) > cap(events)-2 {', 'if false {')]),
    ("halt-question", "client", "TestPipeHaltSchliesstFrage", [c("pipe.go", 'e.Questions.Cancel()', '// mutation: question survives halt')]),
    ("credential-in-panel", "client", "TestPipeOhneTraeger", [c("pipe.go", 'if _, err := json.Marshal(m); err != nil {', 'if len(m.Setups)>0 { m.Setups[0].Label=e.Credential.Token }; if _, err := json.Marshal(m); err != nil {')]),
    ("setup-halt", "client", "TestSetupStartNurAusManifest", [c("pipe.go", 'e.haltedLocked(time.Now()) ||', 'false ||', 3)]),
    ("setup-offline", "client", "TestSetupStartNurAusManifest", [c("pipe.go", '!e.panelOnline', 'false', 2)]),
    ("setup-busy", "client", "TestSetupStartNurAusManifest", [c("pipe.go", 'if !e.runMu.TryLock() {\n\t\te.admission.Unlock()\n\t\treturn ErrProtocol', 'if !e.runMu.TryLock() {\n\t\te.admission.Unlock()\n\t\treturn nil')]),
    ("setup-reservation", "client", "TestSetupStartNurAusManifest", [c("pipe.go", 'if e.Container != nil || !e.admission.TryLock() {\n\t\treturn ErrProtocol', 'if e.Container != nil || !e.admission.TryLock() {\n\t\treturn nil')]),
    ("setup-unknown", "client", "TestSetupStartNurAusManifest", [c("pipe.go", 's.Name == setup &&', 'true &&')]),
    ("setup-free-input", "client", "TestSetupStartNurAusManifest", [c("pipe.go", 's.Name == setup && len(s.Frei) == 0', 's.Name == setup')]),
    ("setup-signature", "client", "TestSetupStartNurAusManifest", [c("pipe.go", 'm, err := VerifyManifest(b.Manifest.Envelope, e.Credential.Pubkey, e.Credential.DeviceID, 0)', 'm, err := ParseManifest(b.Manifest.Envelope.Manifest)')]),
    ("setup-contract", "client", "TestSetupStartNurAusManifest", [c("pipe.go", 'if err != nil || m.Contract != WindowsContract {', 'if err != nil {')]),
    ("setup-account", "client", "TestSetupStartNurAusManifest", [c("pipe.go", 'if err = startRefusal(session, b.Config); err != nil {', 'if err = startRefusal(session, b.Config); err != nil && false {')]),
    ("setup-transition", "client", "TestSetupVeralteteFreigabeStartetNicht", [c("pipe.go", 'version != e.haltVersion', '(version != e.haltVersion && false)', 2), c("pipe.go", 'p.haltVersion == e.haltVersion', '(p.haltVersion == e.haltVersion || true)')]),
    ("setup-manifest-withdrawal", "client", "TestSetupVeralteteFreigabeStartetNicht", [c("pipe.go", 'string(current.Manifest) != string(b.Manifest.Envelope.Manifest) || current.Sig != b.Manifest.Envelope.Sig', 'string(current.Manifest) != string(b.Manifest.Envelope.Manifest) && current.Sig != b.Manifest.Envelope.Sig'), c("pipe.go", 'p.envelope.Sig == e.bundle.Manifest.Envelope.Sig', '(p.envelope.Sig == e.bundle.Manifest.Envelope.Sig || true)')]),
    ("setup-final-spawn", "client", "TestSetupFreigabeDirektVorSpawn", [c("engine.go", 'if !e.panelPermitValidLocked(ctx) {', 'if !e.panelPermitValidLocked(ctx) && false {')]),
    ("setup-claim-gate", "client", "TestSetupSperrtUndBrichtClaimAb", [c("halt.go", 'if e.panelPending {', 'if false {')]),
    ("setup-claim-cancel", "client", "TestSetupSperrtUndBrichtClaimAb", [c("pipe.go", 'e.claimCancel()', '// mutation: keep claim polling')]),
    ("setup-claim-generation", "client", "TestSetupSperrtUndBrichtClaimAb", [c("halt.go", 'panelVersion != e.panelGeneration', '(panelVersion != e.panelGeneration && false)')]),
    ("status-too-large", "client", "TestPipeStatusPasstNichtWirdStoerung", [c("pipe.go", 'if _, err := json.Marshal(m); err != nil {', 'if _, err := json.Marshal(m); err != nil && false {')]),
    ("setup-expiry", "client", "TestSetupVeralteteFreigabeStartetNicht", [c("pipe.go", '!time.Now().Before(expires)', 'time.Now().Before(expires) && false')]),
    ("admission-other-starts", "client", "TestSetupSperrtAndereStartwege", [c("engine.go", 'if !e.admission.TryLock() {\n\t\t\treturn nil', 'if !e.admission.TryLock() {\n\t\t\treturn e.report(Result{RunKey:"unexpected",End:"internal"})', 2)]),
    ("script-raw-frames", "client", "TestSkriptRoheTestfunktionenBleibenGesperrt", [c("engine.go", 'default:\n\t\t\t\texitResult(finish(r, "internal", ""))', 'default:\n\t\t\t\texitResult(finish(r, "ok", ""))')]),
    ("h3-handle-list", "client", "TestPipeKindHandleListe", [edit("internal/platform/process_windows.go", 'attrs.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), uintptr(len(handles))*unsafe.Sizeof(handles[0]))', 'error(nil)')]),
    ("hygiene-extra-listener", "client", "TestKeineListenerDownloaderOderPip", [p("transport_windows.go", 'h, err := windows.'+'CreateNamedPipe(', 'h, err := windows.'+'CreateNamedPipe('), p("transport_windows.go", '\tctx, cancel := context.WithCancel(context.Background())', '\t_, _ = windows.'+'CreateNamedPipe(p, 0, 0, 1, 0, 0, 0, &sa)\n\tctx, cancel := context.WithCancel(context.Background())')]),
]

NATIVE = [
    ("native-foreign-account", "pipe", "TestWindowsPipeFremdesKonto", [p("security.go", 'D:P(A;;0x12008b;;;', 'D:P(A;;GR;;;WD)(A;;0x12008b;;;')]),
    ("native-low-label", "pipe", "TestWindowsPipeLowILReadWrite", [p("security.go", 'S:(ML;;NWNR;;;ME)', '')]),
    ("native-create-instance", "pipe", "TestWindowsPipeClientDarfKeineInstanzAnlegen", [p("security.go", '0x12008b', '0x12019f', 2)]),
    ("native-create-instance-bit", "pipe", "TestWindowsPipeClientDarfKeineInstanzAnlegen", [p("security.go", '0x12008b', '0x12008f', 2)]),
    ("native-create-instance-child", "pipe", "TestWindowsPipeClientDarfKeineInstanzAnlegen/standard_process", [p("security.go", '0x12008b', '0x12008f', 2)]),
    ("native-helper-token", "pipe", "TestWindowsPipeClientDarfKeineInstanzAnlegen/standard_process", [p("transport_windows_test.go", 'Token: syscall.Token(token), HideWindow: true', 'Token: syscall.Token(0), HideWindow: true')]),
    ("native-helper-start", "pipe", "TestWindowsPipeClientDarfKeineInstanzAnlegen/standard_process", [p("transport_windows_test.go", 'cmd := exec.CommandContext(ctx, exe, "-test.run=^TestWindowsPipeRightsHelper$"', 'cmd := exec.CommandContext(ctx, exe+".missing", "-test.run=^TestWindowsPipeRightsHelper$"')]),
    ("native-restricting-helper-start", "pipe", "TestWindowsPipeRestrictingSIDsVerweigert", [p("transport_windows_test.go", 'cmd := exec.CommandContext(ctx, exe, "-test.run=^TestWindowsPipeRightsHelper$"', 'cmd := exec.CommandContext(ctx, exe+".missing", "-test.run=^TestWindowsPipeRightsHelper$"')]),
    ("native-restricting-token", "pipe", "TestWindowsPipeRestrictingSIDsVerweigert", [p("transport_windows_test.go", 'if count := pipeRestrictingSIDCount(t, token); count != 0 {', 'if count := pipeRestrictingSIDCount(t, token); count != 0 && false {')]),
    ("native-client-path", "pipe", "TestWindowsServerPrueftClientbildUndReconnect", [p("transport_windows.go", '!sameFolder(own, p.Image, "jnpt-bedienfeld.exe")', '!sameFolder(own, p.Image, "jnpt-bedienfeld.exe") && false')]),
    ("native-server-check", "pipe", "TestWindowsClientPrueftEchtenServer", [p("transport_windows.go", 'return verifyServer(own, server)', '_ = own; _ = server; return nil'), p("transport_windows.go", 'processPeer(pid, true)', 'processPeer(pid, false)'), p("transport_windows.go", 'processPeer(windows.GetCurrentProcessId(), true)', 'processPeer(windows.GetCurrentProcessId(), false)')]),
    ("native-h3", "platform", "TestWindowsKindOhneBedienfeldPipe", [p("transport_windows.go", 'InheritHandle: 0', 'InheritHandle: 1'), edit("internal/platform/process_windows.go", 'attrs.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), uintptr(len(handles))*unsafe.Sizeof(handles[0]))', 'error(nil)')]),
    ("native-h3-python", "platform", "TestWindowsPythonKindOhneBedienfeldPipe", [p("transport_windows.go", 'InheritHandle: 0', 'InheritHandle: 1'), edit("internal/platform/process_windows.go", 'attrs.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), uintptr(len(handles))*unsafe.Sizeof(handles[0]))', 'error(nil)')]),
]


def command(args, timeout=120, cwd=ROOT):
    result = subprocess.run(args, cwd=cwd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=timeout)
    return result.returncode, result.stdout.decode(errors="replace")


def run(case, native=False, execute_native=False):
    label, package, test, changes = case
    if execute_native and label in {"native-create-instance-child", "native-helper-token", "native-helper-start", "native-restricting-helper-start", "native-restricting-token"} and os.environ.get("JNPT_NATIVE_TOKEN_PROBES") != "1":
        print(f"{label}: SKIPPED (opt-in: Hilfsprozess mit gefiltertem Token braucht interaktive Sitzung (JNPT_NATIVE_TOKEN_PROBES=1))", flush=True)
        return
    # Go splits -run at slashes before compiling each subtest expression.
    pattern = "/".join("^(" + part + ")$" for part in test.split("/"))
    originals = {}
    try:
        for file, old, new, expected in changes:
            path = ROOT / file
            originals.setdefault(path, path.read_bytes())
            source = path.read_text(encoding="utf-8")
            if source.count(old) != expected:
                raise RuntimeError(f"{label}: anchor count {source.count(old)} != {expected}: {old}")
            path.write_bytes(source.replace(old, new).encode("utf-8"))
        if native or any(file.endswith("_windows.go") for file, *_ in changes):
            if execute_native:
                code, output = command(["go", "test", "-c", "-o", os.devnull, "./internal/" + package])
            else:
                code, output = command(["docker", "exec", "-e", "GOOS=windows", "-e", "CGO_ENABLED=0", "l3-test", "go", "test", "-c", "-o", "/tmp/l3-native-mutant.exe", "./internal/" + package])
            if code:
                raise RuntimeError(f"{label}: Windows compile failure is NOT a rejection\n{output}")
        if native and not execute_native:
            print(f"{label}: COMPILED ONLY ({test}); native result pending", flush=True)
            return
        if execute_native:
            args = ["go", "test", "-count=1", "-timeout=35s", "-run=" + pattern, "./internal/" + package]
            code, output = command(args)
        else:
            code, output = command(["docker", "exec", "l3-test", "go", "test", "-c", "-o", "/tmp/l3-mutant", "./internal/" + package])
            if code:
                raise RuntimeError(f"{label}: compile failure is NOT a rejection\n{output}")
            code, output = command(["docker", "exec", "-w", "/workspace/desktop-runner/internal/" + package, "l3-test", "/tmp/l3-mutant", "-test.run=" + pattern, "-test.timeout=20s"])
        if code == 0 or not any("--- FAIL: " + name in output for name in test.split("|")):
            raise RuntimeError(f"{label}: survived or infrastructure failed\n{output}")
        print(f"{label}: RED ({test})", flush=True)
    finally:
        for path, original in originals.items():
            path.write_bytes(original)
            if path.read_bytes() != original:
                raise RuntimeError(f"{label}: exact restoration failed")


def main():
    native = "--native" in sys.argv
    if native and sys.platform != "win32":
        raise RuntimeError("--native requires Windows")
    selected = set(sys.argv[1:]) - {"--native"}
    known = {c[0] for c in CASES + NATIVE}
    if selected - known:
        raise RuntimeError(f"unknown cases: {selected - known}")
    for case in ([] if native else CASES) + NATIVE:
        if not selected or case[0] in selected:
            run(case, case in NATIVE, native)
    print("Source bytes restored. Native compilation is not native proof.", flush=True)


if __name__ == "__main__":
    main()
