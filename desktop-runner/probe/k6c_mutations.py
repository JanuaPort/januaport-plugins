"""K6c: kompilierende rote Mutanten; exklusiv, bytegenaue Wiederherstellung.

Auf Windows: python probe/k6c_mutations.py (Go-HTTP-Tests, keine GUI/Sonde).
Linux-Ziele laufen im benannten Container k6cp3-test. Kein anderer Container
wird gestartet, gestoppt oder verändert. Native WebView-Sonden gehören BOX.
"""
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]

# name, file, old, new, package, test, windows-only standard Go adapter
CASES = [
    ("pkce", "anmeldung/anmeldung.go", "sha256.Sum256([]byte(verifier))", 'sha256.Sum256([]byte("constant"))', "anmeldung", "TestPKCEUndState", False),
    ("state", "anmeldung/anmeldung.go", "subtle.ConstantTimeCompare([]byte(q.Get(\"state\")), []byte(f.State)) != 1", "subtle.ConstantTimeCompare([]byte(q.Get(\"state\")), []byte(f.State)) == -1", "fensterio", "TestLoopbackNurEinmal", True),
    ("host", "anmeldung/anmeldung.go", "host != wantHost", "false", "fensterio", "TestLoopbackNurEinmal", True),
    ("query-doppelt", "anmeldung/anmeldung.go", 'len(q["state"]) != 1', 'len(q["state"]) == 0', "anmeldung", "TestRueckrufStrikt", False),
    ("freie-fehler", "anmeldung/anmeldung.go", 'case "kein_zugriff", "platz_gesperrt", "abgebrochen":', 'case "kein_zugriff", "platz_gesperrt", "abgebrochen", "freier-text":', "anmeldung", "TestRueckrufStrikt", False),
    ("name-laenge", "anmeldung/anmeldung.go", "n == 128", "n == 129", "anmeldung", "TestNameUndAuthorizeURL", False),
    ("name-steuer", "anmeldung/anmeldung.go", "unicode.IsControl(r) ||", "false ||", "client", "TestPCSessionStrikt", False),
    ("name-bidi", "anmeldung/anmeldung.go", "unicode.Is(unicode.Cf, r)", "(unicode.Is(unicode.Cf, r) && false)", "client", "TestPCSessionStrikt", False),
    ("authorize", "anmeldung/anmeldung.go", "return ok", "return ok || true", "client", "TestPCSessionStrikt", False),
    ("wechsel-default", "client/applogin.go", "out.Changed == nil || *out.Changed", "out.Changed != nil && *out.Changed", "client", "TestPCSessionStrikt", False),
    ("namensvergleich", "fensterio/login.go", "r.person.Changed || !l.signed {", "(r.person.Changed && r.person.Name != l.name) || !l.signed {", "fensterio", "TestPersonenwechselVorFreigabe", False),
    ("kein-loeschen", "fensterio/login.go", "v.ClearBrowsingData(finish)", "func() error { finish(true); return nil }()", "fensterio", "TestPersonenwechselVorFreigabe", False),
    ("loeschfehler", "fensterio/login.go", "if !ok {", "if false {", "fensterio", "TestLoginBereinigungsfehler", False),
    ("abmelden-token", "fensterio/login.go", "l.gateway.Forget()\n\tif l.host", "// vergessen entfernt\n\tif l.host", "fensterio", "TestPersonenwechselVorFreigabe", False),
    ("login-suspend", "fensterio/login.go", "l.host.Suspend()", "// Suspend entfernt", "fensterio", "TestLoginVerwirftLaufendeAntwort", False),
    ("login-pump-generation", "fensterio/login.go", "if !l.current(r.generation) {", "if false {", "fensterio", "TestLoginPumpVerwirftAlteGenerationNachAbmelden", False),
    ("host-suspend-out", "fensterio/host.go", "h.epoch++\n\th.out = nil", "h.epoch++", "fensterio", "TestLoginVerwirftWartendeAntwort", False),
    ("session-zeitablauf", "fensterio/login_windows.go", 'he.Class == "kein_zugriff"', 'he.Class == "unbekannt"', "fensterio", "TestLoginVomKnopfBisSessionAmAdapter", True),
    ("konto-abgelaufen", "fensterio/login.go", "l.signed && l.gateway.personalExpired()", "false && l.gateway.personalExpired()", "fensterio", "TestLoginSitzungsablaufEntferntKontoAmAdapter", False),
    ("unsigniert-neuladen", "fensterio/login.go", "r.person.Changed || !l.signed", "r.person.Changed", "fensterio", "TestLoginGleichePersonOhneLokaleSitzungLaedtNeu", False),
    ("alte-antwort", "fensterio/host.go", "v.epoch != h.epoch ||", "", "fensterio", "TestPersonenwechselVerwirftAlteBrueckenantwort", False),
    ("person-required", "fensterio/gateway.go", 'he.Status == 409 && he.Class == "person_required"', 'he.Status == 409 && he.Class == "unknown"', "fensterio", "TestGatewayPersonRequiredAmAdapter", False),
    ("frist", "fensterio/loopback_windows.go", "5 * time.Minute", "6 * time.Minute", "fensterio", "TestLoginNativVerdrahtet", False),
    ("falsch-verbraucht", "fensterio/loopback_windows.go", "w.WriteHeader(400)", "_ = listener.Close(); w.WriteHeader(400)", "fensterio", "TestLoopbackNurEinmal", True),
    ("header-frist", "fensterio/loopback_windows.go", "ReadHeaderTimeout: 5 * time.Second,", "", "client", "TestKeineListenerDownloaderOderPip", False),
    ("header-groesse", "fensterio/loopback_windows.go", "MaxHeaderBytes: 4096,", "", "client", "TestKeineListenerDownloaderOderPip", False),
    ("http-client", "fensterio/loopback_windows.go", "const loginLifetime", 'func verboten() { _, _ = http.' + 'Get("https://invalid.example/") }\n\nconst loginLifetime', "client", "TestKeineListenerDownloaderOderPip", False),
    ("zweiter-listener", "fensterio/loopback_windows.go", "const loginLifetime", 'func verboten() { l,_ := net.' + 'Listen("tcp4", "127.0.0.1:0"); if l!=nil { l.Close() } }\n\nconst loginLifetime', "client", "TestKeineListenerDownloaderOderPip", False),
    ("shell-pruefung", "fensterio/webview_windows.go", "!ok {\n\t\treturn ErrWebView\n\t}\n\treturn windows.ShellExecute", "!ok && false {\n\t\treturn ErrWebView\n\t}\n\treturn windows.ShellExecute", "fensterio", "TestLoginBrowserPruefungVorShell", False),
]

def command(package, test, windows):
    args = ["go", "test", "./internal/" + package, "-run", "^" + test, "-count=1", "-timeout=30s"]
    return args if windows else ["docker", "exec", "k6cp3-test", *args]

def main():
    selected = set(sys.argv[1:])
    for name, file, old, new, package, test, windows in CASES:
        if selected and name not in selected:
            continue
        path = ROOT / "internal" / file
        original = path.read_bytes()
        text = original.decode("utf-8")
        if old not in text:
            raise SystemExit("Anker fehlt: " + name)
        baseline=subprocess.run(command(package,test,windows),cwd=ROOT,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True,timeout=90)
        if baseline.returncode:
            print(baseline.stdout)
            raise SystemExit("Ausgangstest nicht gruen: "+name)
        try:
            path.write_bytes(text.replace(old, new, 1).encode("utf-8"))
            if file.endswith("_windows.go"):
                output = ROOT / ".probe" / "k6cp3-mutant.test.exe"
                output.parent.mkdir(exist_ok=True)
                built = subprocess.run(["go", "test", "-c", "-o", str(output), "./internal/fensterio"], cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, timeout=90)
                if built.returncode:
                    print(built.stdout)
                    raise SystemExit("Windows-Mutant kompiliert nicht: " + name)
            p = subprocess.run(command(package, test, windows), cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, timeout=90)
            if p.returncode == 0 or "[build failed]" in p.stdout or "--- FAIL:" not in p.stdout:
                print(p.stdout)
                raise SystemExit("Mutant nicht kompilierend rot: " + name)
            print("ROT " + name + ": " + test, flush=True)
        finally:
            path.write_bytes(original)
            assert path.read_bytes() == original

def pin_mutations():
    path = ROOT / "internal/fensterio/loopback_windows.go"
    original = path.read_bytes()
    moved = path.with_name("k6cp3_wrong_windows.go")
    assert not moved.exists()
    for name in ["null-listener", "falsche-datei"]:
        if len(sys.argv)>1 and name not in sys.argv[1:]:
            continue
        baseline=subprocess.run(command("client","TestKeineListenerDownloaderOderPip",False),cwd=ROOT,capture_output=True,text=True,timeout=90)
        if baseline.returncode:
            raise SystemExit("Ausgangstest nicht gruen: "+name+"\n"+baseline.stdout+baseline.stderr)
        try:
            if name=="null-listener":
                text=original.decode("utf-8").replace('"net"', 'transport "net"').replace('net.', 'transport.')
                path.write_bytes(text.encode("utf-8"))
            else:
                path.rename(moved)
            output=ROOT / ".probe/k6cp3-mutant.test.exe"
            output.parent.mkdir(exist_ok=True)
            built=subprocess.run(["go","test","-c","-o",str(output),"./internal/fensterio"],cwd=ROOT,capture_output=True,text=True,timeout=90)
            if built.returncode:
                raise SystemExit("Mutant kompiliert nicht: "+name+"\n"+built.stdout+built.stderr)
            p=subprocess.run(command("client","TestKeineListenerDownloaderOderPip",False),cwd=ROOT,capture_output=True,text=True,timeout=90)
            if p.returncode==0 or "--- FAIL:" not in p.stdout:
                raise SystemExit("Hygiene nicht rot: "+name+"\n"+p.stdout+p.stderr)
            print("ROT "+name+": TestKeineListenerDownloaderOderPip",flush=True)
        finally:
            if moved.exists():
                moved.rename(path)
            path.write_bytes(original)
            assert path.read_bytes()==original

if __name__ == "__main__":
    main()
    pin_mutations()
