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
        ('c, e := r.Cookie("__Host-jnpt_login")', 'c, e := r.Cookie("__Host-jnpt_login")\n if c == nil { c = &http.Cookie{} }'),
        ('if e != nil || !opaque.MatchString(c.Value) {', 'if false {')]),
    ("F2 Sitzungsverdrängung aus", "internal/apphost/apphost.go", [
        ('delete(h.sessions, earliest)', 'if earliest != "" { h.mu.Unlock(); status(w, 503); return }')]),
    ("H2 globaler Mutex beim Abruf", "internal/apphost/apphost.go", [
        ('h.mu.Unlock()\n\n\tnext := h.fetchPackage', '// mutation: keep mutex\n\n\tnext := h.fetchPackage'),
        ('next.checked = h.c.Now()\n\th.mu.Lock()', 'next.checked = h.c.Now()')]),
    ("H2 parallele Abrufe derselben App", "internal/apphost/apphost.go", [
        ('flight := h.flights[app]; flight != nil {', 'flight := h.flights[app]; flight != nil && false {')]),
    ("H2 wartende Anfrage ignoriert Abbruch", "internal/apphost/apphost.go", [
        ('case <-ctx.Done():', 'case <-context.Background().Done():')]),
    ("5 App aus Query", "internal/apphost/apphost.go", [
        ('map[string]string{"app": app, "code":', 'map[string]string{"app": r.URL.Query().Get("app"), "code":')]),
    ("6 festes Max-Age", "internal/apphost/apphost.go", [
        ('"__Host-jnpt_app", result.Token, age,', '"__Host-jnpt_app", result.Token, 300,')]),
    ("6 unsafe-inline", "internal/apphost/apphost.go", [("script-src 'self';", "script-src 'self' 'unsafe-inline';")]),
    ("7 Headerfilter aus", "internal/apphost/apphost.go", [
        ('out.Header.Set("Authorization", "Bearer "+token)', 'out.Header = r.Header.Clone()\n out.Header.Set("Authorization", "Bearer "+token)')]),
    ("10 Apphost im Plugin-Netz", "docker-compose.yml", [
        ("networks: [jnpt_apps]", "networks: [jnpt_apps, plugins]")]),
    ("11 Caddy delete code aus", "../box/apps.caddy", [("delete code", "# mutation: code filter removed")]),
    ("11 Fehlerhandler aus", "../box/apps.caddy", [("handle_errors {", "handle /not-used-for-errors {")]),
    ("C-1 CORS wildcard", "internal/apphost/apphost.go", [
        ("func security(w http.ResponseWriter) {", 'func security(w http.ResponseWriter) {\n w.Header().Set("Access-Control-Allow-Origin", "*")')]),
    ("C-2 Refresh umgeleitet", "internal/apphost/apphost.go", [("content=\"0;url=/\"", "content=\"0;url=/foreign\"")]),
    ("C-3 Verifier zu kurz", "internal/apphost/apphost.go", [("make([]byte, 32)", "make([]byte, 16)")]),
    ("I-1 Shell statt Binary", "docker-compose.yml", [('command: ["apphost"]', 'command: ["sh", "-c", "script-runner apphost"]')]),
    ("I-2 Dialer MPTCP", "internal/apphost/network.go", [("d.SetMultipathTCP(false)", "d.SetMultipathTCP(true)")]),
    ("I-2 Listener MPTCP", "internal/apphost/network.go", [("lc.SetMultipathTCP(false)", "lc.SetMultipathTCP(true)")]),
    ("I-3 Binaryscan aus", "../.github/workflows/script-runner.yml", [("govulncheck -mode binary", "govulncheck -mode source")]),
    ("H-1 Syntaxpruefung aus", "internal/apphost/apphost.go", [('if h.app(domain) == "" {', 'if false {')]),
    ("H-1 Query durchgereicht", "internal/apphost/apphost.go", [
        ('"/internal/apps/tls-ask?domain="+url.QueryEscape(domain)', '"/internal/apps/tls-ask?"+r.URL.RawQuery')]),
]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--docker", action="store_true")
    parser.add_argument("--only", help="mutation name prefix; still runs the full Go package suite")
    args = parser.parse_args()
    for i, (name, file, replacements) in enumerate(MUTATIONS):
        if args.only and not name.startswith(args.only):
            continue
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
            package = "./internal/hygiene" if name.startswith(("10 ", "I-1 ", "I-3 ")) else "./internal/apphost"
            command = ["go", "test", "-count=1", package]
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
            if name.startswith("11 "):
                command = ["pwsh", "-NoProfile", "-File", str(ROOT / "probe/apphost.ps1")]
            result = subprocess.run(command, cwd=ROOT, text=True, capture_output=True, check=False)
            if name.startswith("11 "):
                lines = result.stdout.splitlines()
                for j, line in enumerate(lines):
                    if "--- FAIL:" in line or line.startswith(("FAIL", "ok ")):
                        print("\n".join(lines[max(0, j-1):j+2]), flush=True)
                print(result.stderr, flush=True)
            else:
                print(result.stdout, result.stderr, flush=True)
            if result.returncode == 0 or "--- FAIL:" not in result.stdout or "build failed" in result.stdout:
                raise RuntimeError(f"mutation not killed by an executing test: {name}")
        finally:
            path.write_bytes(original)


if __name__ == "__main__":
    main()
