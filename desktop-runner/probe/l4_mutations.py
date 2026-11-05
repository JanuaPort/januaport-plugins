"""L4 gates; exclusive worktree, golang:1.26.10 in l4-test at /workspace.

Each mutant must compile, then fail its named test. Windows process mutants
are compiled only; --native is for LEAD's Windows CI, never a local GUI run.
All edited bytes are restored in finally. No report files are written.
"""
from pathlib import Path
import os
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
UI = "cmd/bedienfeld/main_windows.go"
MODEL = "internal/panel/model.go"
SCHEMA = "internal/pipe/schema.go"
RUNNER = "internal/client/questions.go"
LINK = "internal/panelio/link.go"

def edit(file, old, new, count=1):
    return file, old, new, count

CASES = [
    ("default-button", "internal/panel", "TestFokusWaechter", [edit(UI, "buttonStyle      = 0x40000000 | 0x10000000", "BS_DEFPUSHBUTTON = 1\n\tbuttonStyle      = 0x40000000 | 0x10000000 | BS_DEFPUSHBUTTON")]),
    ("foreground-api", "internal/panel", "TestFokusWaechter", [edit(UI, "var (", 'var setForeground = user.proc("SetForegroundWindow")\n\nvar (')]),
    ("missing-api-connects", "internal/panel", "TestBedienfeldProcAufloesung", [edit(UI, "if nativeLoadError == nil {", "if true {")]),
    ("must-find-proc", "internal/panel", "TestBedienfeldProcAufloesung", [edit(UI, "var (", 'var unsafeProc = syscall.MustLoadDLL("user32.dll").MustFindProc("ShowWindow")\n\nvar (')]),
    ("activating-dialog", "internal/panel", "TestFokusWaechter", [edit(UI, "WS_EX_NOACTIVATE | 0x00000008", "0x00000008")]),
    ("activating-show", "internal/panel", "TestFokusWaechter", [edit(UI, "showNoActivate   = 4", "showNoActivate   = 5")]),
    ("http-import", "internal/panel", "TestPanelImportWaechter", [edit(UI, "import (", 'import (\n\t_ "net/' + 'http"')]),
    ("direct-net-import", "internal/panel", "TestPanelImportWaechter", [edit(UI, "import (", 'import (\n\t_ "net"')]),
    ("balloon-flag", "internal/panel", "TestFokusWaechter", [edit(UI, "trayFlags        = 1 | 2 | 4", "trayFlags        = 1 | 2 | 4 | 0x10")]),
    # #1240 Konsolenanschluss des GUI-Läufers: run nie, Umleitung unangetastet, Anschluss als Erstes in main().
    ("console-run-attaches", ".", "TestKonsolenplan", [edit("console.go", 'if len(args) > 0 && args[0] == "run" {', "if false {")]),
    ("console-redirect-ignored", ".", "TestKonsolenplan", [edit("console.go", "plan := consolePlan{Stdout: !stdoutValid, Stderr: !stderrValid}", "plan := consolePlan{Stdout: true, Stderr: true}")]),
    ("console-attach-not-first", ".", "TestMainHaengtKonsoleZuerstAn", [edit("main.go", "\tattachConsole()\n", "")]),
    # #1240 Hinweis am Symbol: NIF_INFO nur in notify, fester Text, einmal je neuer Rückfrage.
    ("balloon-in-render", "internal/panel", "TestFokusWaechter", [edit(UI, "Flags: trayFlags,", "Flags: trayFlags | nifInfo,")]),
    ("notice-every-render", "internal/panel", "TestHinweisNurAnEinerStelle", [edit(UI, "if a.tray && a.model.TakeNotice() {", "if a.tray {")]),
    ("notice-question-text", "internal/panel", "TestHinweisNurAnEinerStelle", [edit(UI, "copy(n.Info[:], syscall.StringToUTF16(panel.NoticeText))", "copy(n.Info[:], syscall.StringToUTF16(a.model.View().Dialog.Text))")]),
    ("notice-appends-id", "internal/panel", "TestHinweisNurAnEinerStelle", [edit(UI, "syscall.StringToUTF16(panel.NoticeText)", "syscall.StringToUTF16(panel.NoticeText+a.dialogID)")]),
    ("notice-opens-dialog", "internal/panel", "TestHinweisNurAnEinerStelle", [edit(UI, "\t\ta.notify()\n", "\t\ta.notify()\n\t\ta.apply(a.model.Choose(\"frage\", time.Now()))\n")]),
    # #1240 Symbol nach Autostart/Explorer-Neustart: bei Scheitern die andere Operation (NIM_ADD/NIM_MODIFY) versuchen.
    ("tray-no-second-attempt", "internal/panel", "TestTrayUpdateVersuche", [edit("internal/panel/tray.go", "return attempt(!tray)", "return false")]),
    ("tray-false-after-second", "internal/panel", "TestTrayUpdateVersuche", [edit("internal/panel/tray.go", "return attempt(!tray)", "attempt(!tray)\n\treturn false")]),
    ("tray-render-ignores-update", "internal/panel", "TestRenderNutztTrayUpdate", [edit(UI, "a.tray = panel.TrayUpdate(a.tray, try)", "_ = panel.TrayUpdate\n\ta.tray = a.tray || try(a.tray)")]),
    ("notice-with-sound", "internal/panel", "TestHinweisNurAnEinerStelle", [edit(UI, "noticeInfoFlags = 1 | 0x10", "noticeInfoFlags = 1")]),
    ("notice-text-changed", "internal/panel", "TestHinweisFesterText", [edit(MODEL, 'NoticeText  = "JanuaPort braucht eine Antwort"', 'NoticeText  = "Rückfrage"')]),
    ("notice-not-consumed", "internal/panel", "TestHinweisEinmalJeNeuerFrage", [edit(MODEL, "due := m.noticeFor != nil && m.noticeFor == m.dialog\n\tm.noticeFor = nil", "due := m.noticeFor != nil && m.noticeFor == m.dialog")]),
    ("notice-for-gone-question", "internal/panel", "TestHinweisEinmalJeNeuerFrage", [edit(MODEL, "due := m.noticeFor != nil && m.noticeFor == m.dialog", "due := m.noticeFor != nil")]),
    ("notice-pointer-lost", "internal/panel", "TestHinweisEinmalJeNeuerFrage", [edit(MODEL, "m.noticeFor = m.dialog", "m.noticeFor = &Dialog{}")]),
    ("edit-field", "internal/panel", "TestBedienfeldNurGepruefterDial", [edit(UI, '"STATIC"', '"EDIT"')]),
    ("dial-bypass", "internal/panel", "TestBedienfeldNurGepruefterDial", [edit(UI, "ctx, pipe.Dial,", "ctx, nil,"), edit(UI, '"github.com/januaport/januaport-plugins/desktop-runner/internal/pipe"', '_ "github.com/januaport/januaport-plugins/desktop-runner/internal/pipe"')]),
    ("ui-thread", "internal/panel", "TestBedienfeldNurGepruefterDial", [edit(UI, "runtime.LockOSThread()", "runtime.Gosched()")]),
    ("fault-next-error", "internal/panel", "TestPanelStoerungBleibtBisNeuGeprueftemZustand", [edit(MODEL, 'case "fehler":', 'case "fehler":\n\t\tm.fault = false')]),
    ("fault-without-recheck", "internal/panel", "TestPanelStoerungBleibtBisNeuGeprueftemZustand", [edit(MODEL, "if m.checked {", "if true {")]),
    ("fault-text", "internal/panel", "TestPanelStoerungBleibtBisNeuGeprueftemZustand", [edit(MODEL, "Störung. Anhalten: abmelden oder über den Admin halten lassen.", "Störung.")]),
    ("fault-color", "internal/panel", "TestPanelStoerungBleibtBisNeuGeprueftemZustand", [edit(MODEL, 'Color: "purple"', 'Color: "green"')]),
    ("verification-gate", "internal/panelio", "TestServerpruefungSendetNichtsAmAdapter", [edit(LINK, "if c != nil {", 'if c != nil {\n\t\t\t\tpipe.WriteFrame(c, pipe.Message{Type:"status"})')]),
    ("retry-distance", "internal/panel", "TestPanelServerpruefungSperrtBefehle", [edit(MODEL, "d := 5 * time.Second", "d := time.Second")]),
    ("retry-cap", "internal/panel", "TestPanelServerpruefungSperrtBefehle", [edit(MODEL, "return min(d, 60*time.Second)", "return d")]),
    ("local-time", "internal/panel", "TestPanelZustaende", [edit(MODEL, 'at.Local().Format("15:04")', 'at.Format("15:04")')]),
    ("test-on-normal-device", "internal/panel", "TestPanelZustaende", [edit(MODEL, "if m.status.Test {", "if true {")]),
    ("stale-command", "internal/panelio", "TestVerbindungVerwirftAlteBefehleUndBrichtSchreibenAb", [edit(LINK, "if r.Generation != generation {", "if false {")]),
    ("write-timeout", "internal/panelio", "TestBlockiertesSchreibenTrotzHeartbeatWirdGetrennt", [edit(LINK, "writeTimer.Reset(5 * time.Second)", "writeTimer.Reset(time.Hour)")]),
    ("hidden-dialog-answer", "internal/panel", "TestPanelFrageNurLokal", [edit(MODEL, "|| !m.dialog.Visible", "")]),
    ("second-dialog", "internal/panel", "TestPanelFrageNurLokal", [edit(MODEL, '&& m.dialog == nil {', '&& true {')]),
    ("dialog-deadline", "internal/panel", "TestPanelFrageNurLokal", [edit(MODEL, "!now.Before(m.dialog.Deadline)", "false")]),
    ("dialog-removal", "internal/panel", "TestPanelFrageNurLokal", [edit(MODEL, "m.dialog.ID == v.ID", "false")]),
    ("dialog-halt", "internal/panel", "TestPanelFrageNurLokal", [edit(MODEL, 'if id == "anhalten" {', "if false {")]),
    ("dialog-status-halt", "internal/panel", "TestPanelFrageNurLokal", [edit(MODEL, 'if v.Zustand == "angehalten" {', "if false {")]),
    ("answer-range", "internal/panel", "TestPanelFrageNurLokal", [edit(MODEL, "choice < 0 || choice >= len(m.dialog.Options)", "false")]),
    ("answer-command", "internal/panel", "TestPanelFrageNurLokal", [edit(MODEL, 'Type: "antwort", ID: id', 'Type: "anhalten", ID: id')]),
    ("exit-halts-runner", "internal/panel", "TestPanelMenueBefehleUndSetupEingabe", [edit(MODEL, "return Effects{Quit: true}", 'return Effects{Quit:true, Commands:[]pipe.Message{{Type:"anhalten"}}}')]),
    ("grey-setup-starts", "internal/panel", "TestPanelMenueBefehleUndSetupEingabe", [edit(MODEL, "&& s.Startbar && !s.BrauchtEingabe", "&& true")]),
    ("input-hint-all-grey", "internal/panel", "TestPanelMenueBefehleUndSetupEingabe", [edit(MODEL, "if s.BrauchtEingabe {", "if s.BrauchtEingabe || !s.Startbar {")]),
    ("input-always-false", "internal/client", "TestPanelEingabeAusVerifiziertemManifest", [edit("internal/client/pipe.go", "BrauchtEingabe: len(s.Frei) > 0", "BrauchtEingabe: false")]),
    ("input-unverified", "internal/client", "TestPanelEingabeAusVerifiziertemManifest", [edit("internal/client/pipe.go", "manifest, err := VerifyManifest(b.Manifest.Envelope, e.Credential.Pubkey, e.Credential.DeviceID, 0)", "manifest, err := ParseManifest(b.Manifest.Envelope.Manifest)")]),
    ("title-from-script", "internal/client", "TestH4RoheFragenAmLaeufer", [edit(RUNNER, "q.mu.Lock()", "m.Titel = m.Text\n\tq.mu.Lock()", 2)]),
    ("second-open-question", "internal/client", "TestH4RoheFragenAmLaeufer", [edit(RUNNER, "if q.pending {", "if false {"), edit("internal/pipe/questions.go", "if h.pending != nil {\n\t\th.mu.Unlock()", "if false {\n\t\th.mu.Unlock()")]),
    ("title-length", "internal/pipe", "TestL4SchemaPflichtfelder", [edit(SCHEMA, "bounded(m.Titel, 40)", "bounded(m.Titel, 41)")]),
    ("title-required", "internal/pipe", "TestL4SchemaPflichtfelder", [edit(SCHEMA, 'return "type id titel text optionen frist_s", ""', 'return "type id text optionen frist_s", "titel"'), edit(SCHEMA, "bounded(m.Titel, 40)", '(m.Titel == "" || bounded(m.Titel, 40))')]),
    ("input-required", "internal/pipe", "TestL4SchemaPflichtfelder", [edit(SCHEMA, '"pin setup label startbar braucht_eingabe", ""', '"pin setup label startbar", "braucht_eingabe"')]),
    ("raw-question-text", "internal/client", "TestH4RoheFrageGrenzen", [edit(SCHEMA, "bounded(m.Text, 500)", "bounded(m.Text, 501)")]),
    ("raw-question-options", "internal/client", "TestH4RoheFrageGrenzen", [edit(SCHEMA, "len(m.Optionen) >= 2 && len(m.Optionen) <= 4", "true")]),
    ("raw-question-deadline", "internal/client", "TestH4RoheFrageGrenzen", [edit(SCHEMA, "m.FristS >= 5 && m.FristS <= 3600", "true")]),
    ("raw-option-length", "internal/client", "TestH4RoheFrageGrenzen", [edit(SCHEMA, "bounded(s, 40)", "bounded(s, 41)")]),
    ("script-title-field", "internal/client", "TestH4SkriptTitelAbgewiesen", [edit(SCHEMA, "!ok || seen[key]", "!ok")]),
    ("legacy-raw-script", "internal/client", "TestSkriptRoheTestfunktionenBleibenGesperrt", [edit("internal/client/engine.go", 'default:\n\t\t\t\texitResult(finish(r, "internal", ""))', 'default:\n\t\t\t\texitResult(finish(r, "ok", ""))')]),
]

NATIVE = [
    ("missing-proc-panics", "cmd/bedienfeld", "TestWindowsBedienfeldFehlenderProc", [edit("cmd/bedienfeld/native_windows.go", "if p.err != nil {\n\t\treturn 0, 0, p.err", "if false {\n\t\treturn 0, 0, p.err")]),
    ("inherited-handle", "cmd/bedienfeld", "TestWindowsBedienfeldProzesssonde", [edit("cmd/bedienfeld/process_windows_test.go", "nil, nil, inherit,", "nil, nil, true,")]),
    ("process-argument", "cmd/bedienfeld", "TestWindowsBedienfeldProzesssonde", [edit("cmd/bedienfeld/process_windows_test.go", "windows.StringToUTF16(`\"` + exe + `\"`)", "windows.StringToUTF16(`\"` + exe + `\" jnpt_probe`)" )]),
    ("process-environment", "cmd/bedienfeld", "TestWindowsBedienfeldProzesssonde", [edit("cmd/bedienfeld/process_windows_test.go", "env = append(env, 0)", 'env = append(env, windows.StringToUTF16("jnpt_probe=synthetic")...); env = append(env, 0)')]),
]

def command(args, timeout=150, cwd=ROOT):
    r = subprocess.run(args, cwd=cwd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=timeout)
    return r.returncode, r.stdout.decode(errors="replace")

def run(case, native=False, execute_native=False):
    label, package, test, edits = case
    originals = {}
    try:
        for name, old, new, expected in edits:
            path = ROOT / name
            originals.setdefault(path, path.read_bytes())
            source = path.read_text(encoding="utf-8")
            if source.count(old) != expected:
                raise RuntimeError(f"{label}: anchor count {source.count(old)} != {expected}: {old}")
            path.write_bytes(source.replace(old, new).encode("utf-8"))
        if native or any(name == UI for name, *_ in edits):
            args = ["go", "test", "-c", "-o", os.devnull, "./cmd/bedienfeld"] if execute_native else ["docker", "exec", "-e", "CGO_ENABLED=0", "-e", "GOOS=windows", "l4-test", "go", "test", "-c", "-o", "/tmp/l4-native-mutant.exe", "./cmd/bedienfeld"]
            code, out = command(args)
            if code: raise RuntimeError(f"{label}: Windows compile failure is NOT rejection\n{out}")
        if native and not execute_native:
            print(f"{label}: COMPILED ONLY ({test}); native result pending", flush=True)
            return
        if execute_native:
            code, out = command(["go", "test", "-count=1", "-timeout=60s", "-run=^"+test+"$", "./"+package])
        else:
            code, out = command(["docker", "exec", "l4-test", "go", "test", "-c", "-o", "/tmp/l4-mutant", "./"+package])
            if code: raise RuntimeError(f"{label}: compile failure is NOT rejection\n{out}")
            code, out = command(["docker", "exec", "-w", "/workspace/desktop-runner/"+package, "l4-test", "/tmp/l4-mutant", "-test.timeout=40s", "-test.run=^"+test+"$"])
        if code == 0 or "--- FAIL: "+test not in out:
            raise RuntimeError(f"{label}: survived or infrastructure failed\n{out}")
        print(f"{label}: RED ({test})", flush=True)
    finally:
        for path, original in originals.items():
            path.write_bytes(original)
            if path.read_bytes() != original: raise RuntimeError("restoration failed")

def main():
    native = "--native" in sys.argv
    if native and sys.platform != "win32": raise RuntimeError("--native requires Windows CI")
    selected = set(sys.argv[1:]) - {"--native"}
    known = {c[0] for c in CASES+NATIVE}
    if selected-known: raise RuntimeError(f"unknown cases: {selected-known}")
    for case in ([] if native else CASES)+NATIVE:
        if not selected or case[0] in selected: run(case, case in NATIVE, native)
    print("Source bytes restored. Compilation is not native proof.", flush=True)

if __name__ == "__main__": main()
