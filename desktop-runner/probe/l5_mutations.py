"""Exclusive L5 mutations in l5-test; no native execution or desktop capture."""
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
HELPERS = "internal/client/helpers.go"
HTTP = "internal/client/http.go"

def edit(path, old, new):
    return path, old, new

CASES = [
    ("test-device", "client", "TestScreenshotNurTestgeraet", [edit(HTTP, "!m.TestDevice ||", "")]),
    ("test-pin", "client", "TestScreenshotNurTestgeraet", [edit(HTTP, 'j.Pin != "test_"+c.DeviceID[:8] ||', "")]),
    ("capture-authorization", "client", "TestScreenshotNurTestgeraet", [edit(HELPERS, "!h.allowed ||", "")]),
    ("result-authorization", "client", "TestErgebnisNurTestauftrag", [edit(HELPERS, "if h.allowed {", "if true {")]),
    ("capture-count", "client", "TestL5ScreenshotGrenzen", [edit(HELPERS, "h.captures >= limit ||", "limit < 0 ||")]),
    ("result-size", "client", "TestL5ErgebnisNullUndGrenzen", [edit(HELPERS, " || len(v.Wert) > 64<<10", ""), edit(HELPERS, " || len(wire) > 64<<10", "")]),
    ("result-escaping", "client", "TestL5ErgebnisNullUndGrenzen", [edit(HELPERS, " || len(wire) > 64<<10", "")]),
    # Both layers guard the same bytes; remove both to observe an actual upload.
    ("png-size", "client", "TestL5ScreenshotGrenzen", [edit(HELPERS, "len(png) > 4<<20 ||", ""), edit(HTTP, "len(png) > 4<<20 ||", "")]),
    ("png-signature", "client", "TestL5ScreenshotGrenzen", [edit(HELPERS, "!bytes.HasPrefix(png, []byte{137, 80, 78, 71, 13, 10, 26, 10})", "false"), edit(HTTP, "!bytes.HasPrefix(png, []byte{137, 80, 78, 71, 13, 10, 26, 10})", "false")]),
    ("null-result", "client", "TestL5ErgebnisNullUndGrenzen", [edit(HELPERS, "h.result = nil", "h.result = wire")]),
    ("first-result-wins", "client", "TestErgebnisNurTestauftrag", [edit(HELPERS, "if h.allowed {", "if h.allowed && h.result == nil {")]),
    ("result-unknown-field", "client", "TestL5RahmenStrikt", [edit(HELPERS, "d.DisallowUnknownFields()", "// removed")]),
    ("result-duplicates", "client", "TestL5RahmenStrikt", [edit(HELPERS, "if _, err := canonical(raw); err != nil {", "if !json.Valid(raw) {")]),
    ("screenshot-schema", "client", "TestL5RahmenStrikt", [edit(HELPERS, "if strict(raw, &v) != nil {", "if json.Unmarshal(raw, &v) != nil {")]),
    ("job-sequence", "client", "TestScreenshotNurTestgeraet", [edit(HELPERS, " && r.ManifestSeq == o.ManifestSeq", "")]),
    ("offline", "client", "TestScreenshotNurTestgeraet", [edit(HELPERS, "r.RanOffline ||", "")]),
    ("setup", "client", "TestScreenshotNurTestgeraet", [edit(HELPERS, 'r.RunKey != "" ||', "")]),
    ("signature", "client", "TestScreenshotNurTestgeraet", [edit(HELPERS, "o.verified(e.Credential) &&", "")]),
    ("automatic-capture", "client", "TestL5ScreenshotGrenzen", [edit("internal/client/engine.go", "_ = helpers.screenshot(captureCtx, true)", "_ = captureCtx")]),
    ("question-no-panel", "client", "TestFrageNurLokal", [edit("internal/pipe/questions.go", "if h.events == nil {\n\t\th.mu.Unlock()\n\t\treturn nil, nil", "if h.events == nil {\n\t\th.mu.Unlock()\n\t\tchoice := 0\n\t\treturn &choice, nil")]),
    ("question-deadline", "client", "TestFrageNurLokal", [edit("internal/pipe/questions.go", "time.NewTimer(time.Duration(q.FristS) * time.Second)", "time.NewTimer(time.Millisecond)")]),
    ("python-network", "client", "TestKeineListenerDownloaderOderPip", [edit("internal/client/bootstrap.py", "import io", "import io\nimport socket")]),
    ("png-buffer", "platform", "TestScreenshotPNGBufferGrenze", [edit("internal/platform/screenshot.go", "if len(p) > 4<<20-b.Len() {", "if false {")]),
]

def command(args):
    result = subprocess.run(args, cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=120)
    return result.returncode, result.stdout.decode(errors="replace")

def main():
    selected = set(sys.argv[1:])
    if selected - {case[0] for case in CASES}:
        raise RuntimeError("unknown mutation")
    count = 0
    for name, package, test, edits in CASES:
        if selected and name not in selected:
            continue
        originals = {}
        try:
            for file, old, new in edits:
                path = ROOT / file
                originals.setdefault(path, path.read_bytes())
                source = path.read_text(encoding="utf-8")
                if source.count(old) != 1:
                    raise RuntimeError(f"{name}: expected one anchor: {old}")
                path.write_bytes(source.replace(old, new, 1).encode("utf-8"))
            code, out = command(["docker", "exec", "l5-test", "go", "test", "-c", "-o", "/tmp/l5-mutant", "./internal/"+package])
            if code:
                raise RuntimeError(f"{name}: compilation failure is NOT rejection\n{out}")
            code, out = command(["docker", "exec", "-w", "/workspace/desktop-runner/internal/"+package, "l5-test", "/tmp/l5-mutant", "-test.timeout=30s", "-test.run=^"+test+"$"])
            if code == 0 or "--- FAIL: "+test not in out:
                raise RuntimeError(f"{name}: survived or infrastructure failed\n{out}")
            print(f"{name}: RED ({test})", flush=True)
            count += 1
        finally:
            for path, original in originals.items():
                path.write_bytes(original)
                if path.read_bytes() != original:
                    raise RuntimeError("restoration failed")
    print(f"{count} compiled mutants rejected; exact bytes restored.", flush=True)

if __name__ == "__main__":
    main()
