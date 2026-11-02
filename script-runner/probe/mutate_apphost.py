"""Run complete Go package suites for temporary K4 mutations; always restore files.

Inside golang:1.26.10, from /repo/script-runner:
  python3 probe/mutate_apphost.py
The official Go image may not have Python; the host can use --docker instead.
"""
import argparse
import pathlib
import subprocess


ROOT = pathlib.Path(__file__).resolve().parents[1]
MUTATIONS = [
    ("1 Verify aus", "internal/gitobjects/objects.go", [
        ("if e := Verify(b, commit, path); e != nil {", "if e := error(nil); e != nil {")]),
    ("2 Bytegrenze >=", "internal/apphost/package.go", [("total > 4<<20", "total >= 4<<20")]),
    ("2 Dateigrenze >=", "internal/apphost/package.go", [("count > 1000", "count >= 1000")]),
    ("3 Suffix aus", "internal/apphost/apphost.go", [
        ("if !strings.HasSuffix(host, suffix) {", "if false {"),
        ("name := strings.TrimSuffix(host, suffix)", 'name := strings.Split(strings.TrimSuffix(host, suffix), ".")[0]')]),
    ("4 Cookie-Bindung aus", "internal/apphost/apphost.go", [
        ('!ok || b.app != app || !h.c.Now().Before(b.expires) || r.Method != "GET"', '(ok && b.app == "mutation-never") || r.Method != "GET"')]),
    ("5 App aus Query", "internal/apphost/apphost.go", [
        ('map[string]string{"app": app, "code":', 'map[string]string{"app": r.URL.Query().Get("app"), "code":')]),
    ("6 festes Max-Age", "internal/apphost/apphost.go", [
        ('"__Host-jnpt_app", result.Token, age,', '"__Host-jnpt_app", result.Token, 300,')]),
    ("6 unsafe-inline", "internal/apphost/apphost.go", [("script-src 'self';", "script-src 'self' 'unsafe-inline';")]),
    ("7 Headerfilter aus", "internal/apphost/apphost.go", [
        ('out.Header.Set("Authorization", "Bearer "+token)', 'out.Header = r.Header.Clone()\n out.Header.Set("Authorization", "Bearer "+token)')]),
]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--docker", action="store_true")
    args = parser.parse_args()
    for i, (name, file, replacements) in enumerate(MUTATIONS):
        path = ROOT / file
        original = path.read_bytes()
        changed = original.decode()
        for before, after in replacements:
            if changed.count(before) != 1:
                raise RuntimeError(f"ambiguous mutation anchor: {name}: {before}")
            changed = changed.replace(before, after, 1)
        print(f"MUTATION {name}", flush=True)
        try:
            path.write_text(changed, encoding="utf-8", newline="")
            command = ["go", "test", "-count=1", "./internal/apphost"]
            if args.docker:
                # Check RAM on Windows before every heavy named container.
                check = subprocess.run(["powershell", "-NoProfile", "-Command",
                    "if ((Get-CimInstance Win32_OperatingSystem).FreePhysicalMemory -le 15GB/1KB) { exit 1 }"], check=False)
                if check.returncode:
                    raise RuntimeError("less than 15 GiB free RAM")
                command = ["docker", "run", "--rm", "--name", f"k4-mutation-{i}",
                    "-v", f"{ROOT.parent.as_posix()}:/repo",
                    "-v", f"{(pathlib.Path.home()/'go/pkg/mod').as_posix()}:/go/pkg/mod",
                    "-w", "/repo/script-runner", "golang:1.26.10"] + command
            result = subprocess.run(command, cwd=ROOT, text=True, capture_output=True, check=False)
            print(result.stdout, result.stderr, flush=True)
            if result.returncode == 0 or "--- FAIL:" not in result.stdout or "build failed" in result.stdout:
                raise RuntimeError(f"mutation not killed by an executing test: {name}")
        finally:
            path.write_bytes(original)


if __name__ == "__main__":
    main()
