"""K6b-1 mutations: exclusive, in a container with Go 1.26.10 and Python 3 (no desktop, no native run).

Start, for example (the worktree is mounted at /workspace, nothing else runs in it):
  docker run --rm -v <worktree>:/workspace -w /workspace/desktop-runner golang:1.26.10 \
    sh -c 'apt-get update -qq && apt-get install -y -qq python3 >/dev/null && python3 probe/k6b_mutations.py'

Every mutant must COMPILE and turn its named target test red; a compile error is not a rejection.
All touched bytes are restored in `finally` and compared.
"""
from pathlib import Path
import shutil
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]

CSP = "internal/fenster/csp.go"
NAV = "internal/fenster/navigation.go"
SET = "internal/fenster/settings.go"
UMS = "internal/fenster/umschlag.go"
BRU = "internal/fenster/bruecke.go"
FEH = "internal/fenster/fehler.go"
LOK = "internal/fensterio/lokal.go"
GAT = "internal/fensterio/gateway.go"
MAN = "internal/client/manifest.go"
TOK = "internal/client/apptoken.go"
STO = "internal/client/appstore.go"
LOA = "internal/client/appload.go"
COP = "internal/client/copied_apphost.go"


def edit(path, old, new):
    return path, old, new


# (name, package, target test (regexp, anchored by the runner), edits)
CASES = [
    # --- internal/fenster: CSP, Navigation, Nachrichten, Einstellungen, Umschlag, Brücke
    ("csp-lockerung", "fenster", "TestCSPExakt", [edit(CSP, "script-src 'self';", "script-src 'self' 'unsafe-inline';")]),
    ("csp-connect", "fenster", "TestCSPExakt", [edit(CSP, "connect-src 'none'; base-uri", "connect-src *; base-uri")]),
    ("nav-nach-host", "fenster", "TestNavigationAllowed", [edit(NAV, "rest != \"\" && rest[0] != '/' && rest[0] != '?' && rest[0] != '#'", "rest != \"\" && rest[0] != '/' && rest[0] != '?' && rest[0] != '#' && false"), edit(NAV, "u.Host == host && u.User == nil && u.Opaque == \"\"", "true")]),  # zwei Schichten: Präfixregel und Parser
    ("nav-grossschreibung", "fenster", "TestNavigationAllowed", [edit(NAV, "strings.HasPrefix(raw, prefix)", "strings.HasPrefix(strings.ToLower(raw), prefix)")]),
    ("nav-zeichen", "fenster", "TestNavigationAllowed", [edit(NAV, "r <= 0x20 || r >= 0x7f || r == '\\\\'", "false && r == 0")]),
    ("msg-rahmen", "fenster", "TestMessageAllowed", [edit(NAV, "return s.MainFrame && NavigationAllowed(app, s.URL)", "return NavigationAllowed(app, s.URL)")]),
    ("msg-origin", "fenster", "TestMessageAllowed", [edit(NAV, "return s.MainFrame && NavigationAllowed(app, s.URL)", "return s.MainFrame")]),
    ("msg-befehl", "fenster", "TestMessageAllowed", [edit(NAV, "SourceAllowed(app, s) && slices.Contains(befehle, befehl)", "SourceAllowed(app, s)")]),
    ("settings-devtools", "fenster", "TestSettingsSollliste", [edit(SET, "{\"AreDevToolsEnabled\", false}", "{\"AreDevToolsEnabled\", true}")]),
    ("settings-hostobjekte", "fenster", "TestSettingsNurSkriptUndNachrichtenAn", [edit(SET, "{\"AreHostObjectsAllowed\", false}", "{\"AreHostObjectsAllowed\", true}")]),
    ("umschlag-unbekannte-felder", "fenster", "TestUmschlagStrikt", [edit(UMS, "dec := json.NewDecoder(bytes.NewReader(raw))\n\tdec.DisallowUnknownFields()", "dec := json.NewDecoder(bytes.NewReader(raw))")]),
    ("umschlag-grenze-locker", "fenster", "TestUmschlagStrikt", [edit(UMS, "len(raw) > MaxUmschlag", "len(raw) > 2*MaxUmschlag")]),
    ("umschlag-grenze-knapp", "fenster", "TestUmschlagGrenzeGenau256KiB", [edit(UMS, "len(raw) > MaxUmschlag", "len(raw) >= MaxUmschlag")]),
    ("umschlag-doppelte-schluessel", "fenster", "TestUmschlagStrikt", [edit(UMS, "!utf8.Valid(raw) || !einzigartig(raw)", "!utf8.Valid(raw)")]),
    ("umschlag-args-leer", "fenster", "TestUmschlagStrikt", [edit(UMS, "if !leeresObjekt(u.Args) {", "if false {")]),
    ("bruecke-quelle", "fenster", "TestBrueckeUnerlaubteQuelleBleibtStumm", [edit(BRU, "if !SourceAllowed(b.app, src) {", "if false {")]),
    ("bruecke-roher-fehlertext", "fenster", "TestBrueckeAntwortOhneTraeger", [edit(FEH, "\tdefault:\n\t\treturn CodeFehler", "\tdefault:\n\t\treturn Code(err.Error())")]),
    ("bruecke-text-bei-festem-code", "fenster", "TestBrueckeAntwortOhneTraeger", [edit(FEH, "\tcase errors.As(err, &f):\n\t\treturn f.Code", "\tcase errors.As(err, &f):\n\t\treturn Code(err.Error() + \" \" + string(f.Code))")]),
    ("bruecke-ergebnis-ungeprueft", "fenster", "TestBrueckeAntwortOhneTraeger", [edit(BRU, " && json.Valid(t) && !EnthaeltTraeger(t)", " && json.Valid(t)")]),
    ("bruecke-traeger-muster", "fenster", "TestTraegerErkennung", [edit(BRU, "traegerMuster.MatchString(s)", "false")]),
    ("import-os-exec", "fenster", "TestFensterImportWaechter", [edit(NAV, "\t\"net/url\"\n", "\t\"net/url\"\n\t_ \"os/exec\"\n")]),
    ("import-windows", "fenster", "TestFensterImportWaechter", [edit(CSP, "package fenster\n", "package fenster\n\nimport _ \"syscall\"\n")]),
    # --- internal/fensterio: Pipe-Wrapper, Gateway
    ("pipe-fremder-befehl", "fensterio", "TestLokalAnhalten", [edit(LOK, "pipe.Message{Type: \"anhalten\"}", "pipe.Message{Type: \"fortsetzen\"}")]),
    ("pipe-dritte-operation", "fensterio", "TestLokalGenauZweiOperationen", [edit(LOK, "type lokal struct{ exchange Exchange }", "type lokal struct{ exchange Exchange }\n\nfunc (l *lokal) Pause(ctx context.Context) error {\n\t_, err := l.ask(ctx, pipe.Message{Type: \"weiter\"})\n\treturn err\n}")]),
    ("pipe-anhalten-unbestaetigt", "fensterio", "TestLokalAnhalten", [edit(LOK, "if m.Zustand != \"angehalten\" {", "if false && m.Zustand != \"angehalten\" {")]),
    ("pipe-projektion", "fensterio", "TestLokalStatusProjiziertNurZustandBisNetz", [edit(LOK, "return fenster.Status{Zustand: m.Zustand, Bis: m.Bis, Netz: m.Netz}, nil", "return fenster.Status{Zustand: m.Zustand, Bis: m.Bis, Netz: !m.Netz}, nil")]),
    ("gateway-kein-neuholen", "fensterio", "TestGatewayHoltBei401NeuUndWiederholtEinmal", [edit(GAT, "attempt == 0 && unauthorized(err)", "false && unauthorized(err)")]),
    ("gateway-token-nicht-im-ram", "fensterio", "TestGatewayHolTokenEinmalUndSendetNurZweiTraeger", [edit(GAT, "if g.session.IsZero() {", "if true {")]),
    # --- internal/client: Vertrag 5, App-Token, App-Ablage, Ladeprüfung
    ("v5-apps-pflicht", "client", "TestManifestV5StriktAmAdapter", [edit(MAN, "(m.Apps != nil) != (m.Contract == appsContract)", "m.Apps != nil && m.Contract != appsContract")]),
    ("v5-apps-nur-v5", "client", "TestManifestV5StriktAmAdapter", [edit(MAN, "(m.Apps != nil) != (m.Contract == appsContract)", "m.Apps == nil && m.Contract == appsContract")]),
    ("v5-apps-sortierung", "client", "TestManifestV5StriktAmAdapter", [edit(MAN, "(i > 0 && apps[i-1].Name >= a.Name)", "(i > 0 && apps[i-1].Name >= a.Name && false)")]),
    ("v5-apps-pfad", "client", "TestManifestV5StriktAmAdapter", [edit(MAN, "a.Path != \"apps/\"+a.Name", "false")]),
    ("v5-apps-commit", "client", "TestManifestV5StriktAmAdapter", [edit(MAN, "!validID(\"sha256\", a.Commit)", "a.Commit == \"\"")]),
    ("v5-header", "client", "TestWindowsSprichVertrag5", [edit("internal/client/types.go", "const WindowsContract = 5", "const WindowsContract = 4")]),
    ("app-token-praefix", "client", "TestAppTokenAntwortStrikt", [edit(TOK, "!strings.HasPrefix(out.AppToken, appTokenPrefix) || ", "")]),
    ("app-token-druckt", "client", "TestAppSitzungDruckenTraegtKeinToken", [edit(TOK, "func (s AppSession) Format(f fmt.State, _ rune) { _, _ = fmt.Fprint(f, \"AppSession(***)\") }", "func (s AppSession) Format(f fmt.State, _ rune) { _, _ = fmt.Fprint(f, s.token) }")]),
    ("app-token-rpc-text", "client", "TestAppRPCAmHTTPAdapter", [edit(TOK, "return nil, &RPCError{Code: e.Code}", "return nil, fmt.Errorf(\"%w %s\", &RPCError{Code: e.Code}, reply.Error)")]),
    ("app-siegel-beim-oeffnen", "client", "TestAppSiegelVorJedemOeffnen", [edit(STO, "if err := VerifyEnvironment(dir, seal.Files); err != nil {", "if false {")]),
    ("app-csp-literal", "client", "TestAppCSPWirdEingesetzt", [edit(LOA, "out = append(out, meta...)", "out = append(out, strings.Replace(meta, \"connect-src 'none'\", \"connect-src *\", 1)...)")]),
    ("app-csp-reihenfolge", "client", "TestAppCSPWirdEingesetzt", [edit(LOA, "s.headEnd = pos", "s.headEnd = len(b)")]),
    ("app-csp-pflicht", "client", "TestAppCSPJedeHTMLDatei", [edit(COP, "if path.Ext(file) == \".html\" {\n\t\t\t\tvar err error", "if path.Ext(file) == \"index.html\" {\n\t\t\t\tvar err error")]),
    ("app-csp-eigene-meta", "client", "TestAppCSPAbweisungen", [edit(LOA, "if !ok || s.cspMetas != 0 {", "if !ok {"), edit(LOA, "if !parserSiehtLiteral(out) {", "if false {")]),  # zwei Schichten: Vorfilter und Parserprüfung
    ("app-csp-head-pflicht", "client", "TestAppCSPAbweisungen", [edit(LOA, "z.Err() == io.EOF && heads == 1", "z.Err() == io.EOF")]),
    ("app-csp-unsauberes-head", "client", "TestAppCSPAbweisungen", [edit(LOA, "case heads == 0 && t.Data != \"html\":", "case false:"), edit(LOA, "if !parserSiehtLiteral(out) {", "if false {")]),  # zwei Schichten
    ("app-csp-open-pruefung", "client", "TestAppOpenPrueftFuehrendesLiteralTrotzNeuemSiegel", [edit(STO, "if err != nil || !leadingCSP(b) {", "if err != nil || !leadingCSP(b) && false {")]),
    ("app-csp-open-genau-eine", "client", "TestLeadingCSP", [edit(LOA, "s.cspMetas == 1 &&", "s.cspMetas >= 1 &&"), edit(LOA, "&& parserSiehtLiteral(b)", "")]),  # zwei Schichten
    # SEC-R4 F1: Tokenizer-Vorfilter und Parserprüfung sind zwei Schichten; die Einzelmutanten für Text und End-Tag
    # überleben absichtlich, weil die jeweils andere Schicht greift. Rot werden sie nur gemeinsam.
    ("app-csp-parserpruefung", "client", "TestAppCSPParserPruefungIstDieEigentlichePruefung", [edit(LOA, "if !parserSiehtLiteral(out) {", "if false {"), edit(LOA, "&& parserSiehtLiteral(b)", "")]),
    ("app-csp-text-vor-head", "client", "TestAppCSPAbweisungen", [edit(LOA, "if heads == 0 && len(bytes.TrimSpace(raw)) != 0 {", "if false && len(bytes.TrimSpace(raw)) != 0 {"), edit(LOA, "if !parserSiehtLiteral(out) {", "if false {")]),
    ("app-csp-endtag-vor-head", "client", "TestAppCSPAbweisungen", [edit(LOA, "case html.EndTagToken:\n\t\t\tif heads == 0 {", "case html.EndTagToken:\n\t\t\tif false {"), edit(LOA, "if !parserSiehtLiteral(out) {", "if false {")]),
    ("app-modus", "client", "TestAppPaketAblehnungenLegenNichtsAb", [edit(COP, "if mode != \"100644\" {", "if false {")]),
    ("app-geheimnis", "client", "TestAppPaketAblehnungenLegenNichtsAb", [edit(COP, "if secretPattern.Match(content) {", "if false {")]),
    ("app-ueberschreiben", "client", "TestEnsureHoltPaketUeberHTTPNurEinmal", [edit(STO, "if _, err := os.Lstat(s.dir(app)); err == nil {\n\t\treturn ErrHash\n\t}\n\traw, err", "raw, err")]),
]


def go(args, timeout=600):
    exe = shutil.which("go")
    if exe is None:
        raise RuntimeError("go not found: run this script inside the golang container")
    result = subprocess.run([exe, *args], cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=timeout)
    return result.returncode, result.stdout.decode(errors="replace")


def check_anchors():
    bad = 0
    for name, _package, _test, edits in CASES:
        for file, old, _new in edits:
            if (ROOT / file).read_text(encoding="utf-8").count(old) != 1:
                print(f"{name}: anchor not unique/present in {file}: {old!r}")
                bad += 1
    print(f"{len(CASES)} mutations, {bad} bad anchors")
    return bad


def main():
    if sys.argv[1:] == ["--check-anchors"]:
        sys.exit(1 if check_anchors() else 0)
    selected = set(sys.argv[1:])
    if selected - {case[0] for case in CASES}:
        raise RuntimeError("unknown mutation")
    count = 0
    failed = []
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
                    raise RuntimeError(f"{name}: expected exactly one anchor in {file}: {old!r}")
                path.write_bytes(source.replace(old, new, 1).encode("utf-8"))
            code, out = go(["test", "-count=1", "-run", "^" + test + "$", "./internal/" + package])
            if "build failed" in out or "setup failed" in out:
                failed.append(f"{name}: compilation failure is NOT rejection\n{out}")
                continue
            if code == 0 or "--- FAIL: " + test not in out:
                failed.append(f"{name}: survived or infrastructure failed\n{out}")
                continue
            print(f"{name}: RED ({test})", flush=True)
            count += 1
        finally:
            for path, original in originals.items():
                path.write_bytes(original)
                if path.read_bytes() != original:
                    raise RuntimeError("restoration failed")
    print(f"{count} compiled mutants rejected; exact bytes restored.", flush=True)
    if failed:
        print("\n".join(failed))
        sys.exit(1)


if __name__ == "__main__":
    main()
