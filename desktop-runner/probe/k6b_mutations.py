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
HTP = "internal/client/http.go"
HST = "internal/fensterio/host.go"
EXC = "internal/fensterio/exchange.go"
RUN = "internal/fensterio/runtime.go"
EVT = "internal/fensterio/events.go"
WVW = "internal/fensterio/webview_windows.go"
EXT = "internal/fenster/extern.go"
RDR = "internal/client/reader.go"
ENG = "internal/client/engine.go"
LOGF = "internal/client/log.go"
APW = "cmd/bedienfeld/apps_windows.go"
MDL = "internal/panel/model.go"
WAIT = "internal/fensterio/wait.go"
WVS = "internal/fensterio/webview_windows.go"
ECH = "internal/edge/chromium.go"
BMN = "cmd/bedienfeld/main_windows.go"
EDD = "internal/edge/doc.go"
EDW = "internal/edge/corewebview2.go"
PTW = "internal/pipe/transport_windows.go"
RSR = "internal/fenster/resource.go"
DGN = "internal/fenster/diag.go"
ENVF = "internal/fensterio/env.go"
ESN = "internal/envsnap/envsnap.go"
APL = "internal/client/appload.go"


def edit(path, old, new):
    return path, old, new


# (name, package, target test (regexp, anchored by the runner), edits)
CASES = [
    # --- internal/fenster: CSP, Navigation, Nachrichten, Einstellungen, Umschlag, Brücke
    ("csp-lockerung", "fenster", "TestCSPExakt", [edit(CSP, "script-src 'self';", "script-src 'self' 'unsafe-inline';")]),
    ("csp-connect", "fenster", "TestCSPExakt", [edit(CSP, "connect-src 'none'; base-uri", "connect-src *; base-uri")]),
    ("nav-nach-host", "fenster", "TestNavigationAllowed", [edit(NAV, "if rest == \"\" || rest[0] != '/' {", "if false {"), edit(NAV, "u.Host == host && u.User == nil && u.Opaque == \"\"", "true")]),  # zwei Schichten: Präfixregel und Parser
    ("nav-grossschreibung", "fenster", "TestNavigationAllowed", [edit(NAV, "strings.HasPrefix(raw, prefix)", "strings.HasPrefix(strings.ToLower(raw), prefix)")]),
    # SEC-R4 F2: nur .html-Seiten
    ("nav-nur-html", "fenster", "TestNavigationAllowed", [edit(NAV, "if !htmlPath(rest) {", "if false {")]),
    ("nav-html-endung-exakt", "fenster", "TestNavigationAllowed", [edit(NAV, "strings.HasSuffix(base, \".html\")", "strings.HasSuffix(strings.ToLower(base), \".html\")")]),
    ("nav-html-kodierte-trenner", "fenster", "TestNavigationAllowed", [edit(NAV, "for _, enc := range []string{\"%2f\", \"%5c\", \"%00\", \"%3f\", \"%23\"} {", "for _, enc := range []string{} {")]),
    ("nav-html-pfad-vor-query", "fenster", "TestNavigationAllowed", [edit(NAV, "path, _, _ := strings.Cut(rest, \"?\")", "path := rest")]),
    ("nav-html-dateiname", "fenster", "TestNavigationAllowed", [edit(NAV, " && len(base) > len(\".html\")", "")]),
    ("nav-start-index", "fenster", "TestStartURL", [edit(NAV, "return \"https://\" + host + \"/index.html\"", "return \"https://\" + host + \"/\"")]),
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
    # SEC-R4 F3: Testlücken
    ("traeger-unescape", "fenster", "TestTraegerErkennungNachUnescaping", [edit(BRU, "\treturn traegerWert(v)\n}", "\treturn false\n}")]),
    ("traeger-schluessel", "fenster", "TestBrueckeAntwortOhneTraeger", [edit(BRU, "if traegerText(k) || traegerWert(e) {", "if k == \"\" || traegerWert(e) {")]),
    ("rpc-methode", "client", "TestAppRPCNurZweiMethoden", [edit(TOK, "(method != \"tools/list\" && method != \"tools/call\")", "false")]),
    ("rpc-id", "client", "TestAppRPCAntwortMussZurFrageGehoeren", [edit(TOK, "|| reply.ID != id {", "{")]),
    ("rpc-version", "client", "TestAppRPCAntwortMussZurFrageGehoeren", [edit(TOK, "reply.JSONRPC != \"2.0\" ||", "")]),
    ("rpc-ergebnis-und-fehler", "client", "TestAppRPCAntwortMussZurFrageGehoeren", [edit(TOK, "if len(reply.Result) != 0 || json.Unmarshal(reply.Error, &e) != nil {", "if json.Unmarshal(reply.Error, &e) != nil {")]),
    ("http-beide-traeger", "client", "TestRequestAppTraegerRegeln", [edit(HTP, "endpoint != \"mcp\" || appToken != \"\" || strings.ContainsAny(runToken", "endpoint != \"mcp\" || strings.ContainsAny(runToken")]),
    ("http-app-endpoint", "client", "TestRequestAppTraegerRegeln", [edit(HTP, "if endpoint != \"mcp\" || !strings.HasPrefix(appToken, appTokenPrefix)", "if !strings.HasPrefix(appToken, appTokenPrefix)")]),
    ("http-app-praefix", "client", "TestRequestAppTraegerRegeln", [edit(HTP, "|| !strings.HasPrefix(appToken, appTokenPrefix) ||", "||")]),
    ("gw-revoked-code", "fensterio", "TestGatewayFehlerabbildungAmFake", [edit(GAT, "case errors.Is(err, client.ErrRevoked):", "case false:")]),
    ("app-siegel-beim-oeffnen", "client", "TestAppSiegelVorJedemOeffnen", [edit(STO, "if err := VerifyEnvironment(dir, seal.Files); err != nil {", "if false {")]),
    ("app-csp-literal", "client", "TestAppCSPWirdEingesetzt", [edit(LOA, "out = append(out, prefix...)", "out = append(out, strings.Replace(prefix, \"connect-src 'none'\", \"connect-src *\", 1)...)")]),
    ("app-csp-reihenfolge", "client", "TestAppCSPWirdEingesetzt", [edit(LOA, "s.headEnd = pos", "s.headEnd = len(b)")]),
    ("app-csp-pflicht", "client", "TestAppCSPJedeHTMLDatei", [edit(COP, "if path.Ext(file) == \".html\" {\n\t\t\t\tvar err error", "if path.Ext(file) == \"index.html\" {\n\t\t\t\tvar err error")]),
    ("app-csp-eigene-meta", "client", "TestAppCSPAbweisungen", [edit(LOA, "if !ok || s.cspMetas != 0 {", "if !ok {"), edit(LOA, "if !parserSiehtLiteral(out) {", "if false {")]),  # zwei Schichten: Vorfilter und Parserprüfung
    ("app-csp-head-pflicht", "client", "TestAppCSPAbweisungen", [edit(LOA, "z.Err() == io.EOF && heads == 1", "z.Err() == io.EOF")]),
    ("app-csp-unsauberes-head", "client", "TestAppCSPAbweisungen", [edit(LOA, "case heads == 0 && t.Data != \"html\":", "case false:"), edit(LOA, "if !parserSiehtLiteral(out) {", "if false {")]),  # zwei Schichten
    ("app-csp-open-pruefung", "client", "TestAppOpenPrueftFuehrendesLiteralTrotzNeuemSiegel", [edit(STO, "if err != nil || !leadingCSP(b) {", "if err != nil || !leadingCSP(b) && false {")]),
    ("app-csp-open-genau-eine", "client", "TestLeadingCSP", [edit(LOA, "s.cspMetas == 1 &&", "s.cspMetas >= 1 &&"), edit(LOA, "&& parserSiehtLiteral(b)", "")]),  # zwei Schichten
    # SEC-R4 F4: Zeichensatz-Trick
    ("app-csp-charset-meta", "client", "TestAppCSPZeichensatzMetaVorn", [edit(LOA, "prefix := fenster.HeadPrefix()", "prefix := fenster.CSPMeta()")]),
    ("app-csp-esc", "client", "TestAppCSPZeichensatzTrick", [edit(LOA, "utf8.Valid(b) && bytes.IndexByte(b, 0x1b) < 0 && bytes.IndexByte(b, 0) < 0", "utf8.Valid(b) && bytes.IndexByte(b, 0) < 0")]),
    ("app-csp-utf8", "client", "TestAppCSPZeichensatzTrick", [edit(LOA, "utf8.Valid(b) && bytes.IndexByte(b, 0x1b) < 0", "(utf8.Valid(b) || true) && bytes.IndexByte(b, 0x1b) < 0")]),
    ("app-csp-nul", "client", "TestAppCSPZeichensatzTrick", [edit(LOA, " && bytes.IndexByte(b, 0) < 0", "")]),
    ("app-csp-open-charset", "client", "TestLeadingCSPBrauchtZuerstDieZeichensatzMeta", [edit(LOA, "bytes.HasPrefix(b[s.headEnd:], []byte(fenster.HeadPrefix()))", "bytes.HasPrefix(b[s.headEnd:], []byte(fenster.CSPMeta()))"), edit(LOA, "return len(first) == 2 && nodeIsCharsetUTF8(first[0]) && first[1].Data == \"meta\" && nodeIsCSPMeta(first[1]) && nodeContent(first[1]) == fenster.CSP", "return len(first) >= 1 && first[0].Data == \"meta\" && nodeIsCSPMeta(first[0]) && nodeContent(first[0]) == fenster.CSP")]),  # zwei Schichten: Präfix und Parser
    ("app-csp-open-esc", "client", "TestAppOpenLehntZeichensatzTrickTrotzNeuemSiegelAb", [edit(LOA, "\tif !textSicher(b) {\n\t\treturn false\n\t}", "")]),
    # SEC-R4 F1: Tokenizer-Vorfilter und Parserprüfung sind zwei Schichten; die Einzelmutanten für Text und End-Tag
    # überleben absichtlich, weil die jeweils andere Schicht greift. Rot werden sie nur gemeinsam.
    ("app-csp-parserpruefung", "client", "TestAppCSPParserPruefungIstDieEigentlichePruefung", [edit(LOA, "if !parserSiehtLiteral(out) {", "if false {"), edit(LOA, "&& parserSiehtLiteral(b)", "")]),
    ("app-csp-text-vor-head", "client", "TestAppCSPAbweisungen", [edit(LOA, "if heads == 0 && len(bytes.TrimSpace(raw)) != 0 {", "if false && len(bytes.TrimSpace(raw)) != 0 {"), edit(LOA, "if !parserSiehtLiteral(out) {", "if false {")]),
    ("app-csp-endtag-vor-head", "client", "TestAppCSPAbweisungen", [edit(LOA, "case html.EndTagToken:\n\t\t\tif heads == 0 {", "case html.EndTagToken:\n\t\t\tif false {"), edit(LOA, "if !parserSiehtLiteral(out) {", "if false {")]),
    ("app-modus", "client", "TestAppPaketAblehnungenLegenNichtsAb", [edit(COP, "if mode != \"100644\" {", "if false {")]),
    ("app-geheimnis", "client", "TestAppPaketAblehnungenLegenNichtsAb", [edit(COP, "if secretPattern.Match(content) {", "if false {")]),
    ("app-ueberschreiben", "client", "TestEnsureHoltPaketUeberHTTPNurEinmal", [edit(STO, "if _, err := os.Lstat(s.dir(app)); err == nil {\n\t\treturn ErrHash\n\t}\n\traw, err", "raw, err")]),
    # --- K6b-2 (SEC-Auflagen B1-B4, native Ereignisse)
    # B1: Halt bleibt bedienbar
    ("host-synchron", "fensterio", "TestHostReceiveBlockiertNie", [edit(HST, "\th.wg.Add(1)\n\tgo func() {", "\th.wg.Add(1)\n\tfunc() {")]),
    ("host-gemeinsame-plaetze", "fensterio", "TestHostAnhaltenBleibtBeiHaengendemGatewayBedienbar", [edit(HST, "b, err := fenster.NewBruecke(cfg.App, &limitedGateway{inner: cfg.Gateway, sem: make(chan struct{}, cfg.MaxGateway)}, &limitedLokal{inner: cfg.Lokal, sem: make(chan struct{}, cfg.MaxLokal)}, hostView{h})", "shared := make(chan struct{}, cfg.MaxGateway)\n\tb, err := fenster.NewBruecke(cfg.App, &limitedGateway{inner: cfg.Gateway, sem: shared}, &limitedLokal{inner: cfg.Lokal, sem: shared}, hostView{h})")]),
    ("host-fail-ohne-abbruch", "fensterio", "TestHostFailBrichtAbUndNimmtNichtsMehrAn", [edit(HST, "\t\th.cancel()\n\t\tif h.onClosed != nil {", "\t\tif h.onClosed != nil {")]),
    ("host-warteschlange-unbegrenzt", "fensterio", "TestHostAntwortschlangeBegrenztUndBlockiertNie", [edit(HST, "if h.closed || len(h.out) >= queueSize {", "if h.closed {")]),
    ("host-zu-viele-gateway", "fensterio", "TestHostReceiveBlockiertNie", [edit(HST, "func (g *limitedGateway) take() bool {\n\tselect {\n\tcase g.sem <- struct{}{}:\n\t\treturn true\n\tdefault:\n\t\treturn false\n\t}", "func (g *limitedGateway) take() bool {\n\treturn true"), edit(HST, "defer func() { <-g.sem }()\n\treturn g.inner.Werkzeuge(ctx)", "return g.inner.Werkzeuge(ctx)"), edit(HST, "defer func() { <-g.sem }()\n\treturn g.inner.Werkzeug(ctx, name, arguments)", "return g.inner.Werkzeug(ctx, name, arguments)")]),  # ohne Grenze und ohne das passende Freigeben, sonst hängt der Aufräumer
    ("exchange-alle-rahmen", "fensterio", "TestPanelExchangeNurZustandUndFehlerBeantworten", [edit(EXC, "if err != nil || (m.Type != \"zustand\" && m.Type != \"fehler\") {", "if err != nil || m.Type == \"\" {")]),
    ("exchange-parallel", "fensterio", "TestPanelExchangeSerialisiertGleichzeitigeBefehle", [edit(EXC, "slot: make(chan struct{}, 1)", "slot: make(chan struct{}, 8)")]),
    ("exchange-reset", "fensterio", "TestPanelExchangeVerbindungsverlustWeckt", [edit(EXC, "\tif w != nil {\n\t\tw.lost()\n\t}", "\t_ = w")]),
    ("lokal-nachfragen", "fensterio", "TestLokalAnhaltenNachVeraltetemRahmen", [edit(LOK, "i < 3 &&", "i < 0 &&")]),
    ("runtime-open-async-synchron", "fensterio", "TestRuntimeOpenAsyncKehrtSofortZurueck", [edit(RUN, "\tgo func() {\n\t\to, err := r.Open(name)\n\t\tdone(o, err)\n\t}()", "\to, err := r.Open(name)\n\tdone(o, err)")]),
    # B3: nur lesen
    ("reader-schreibt", "client", "TestReaderOhneSchreibendeAufrufe", [edit(RDR, "\tinfo, err := os.Lstat(p)\n\tif err != nil {\n\t\treturn nil, err\n\t}", "\tinfo, err := os.Lstat(p)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n\t_ = os.Chmod(p, info.Mode())")]),
    ("reader-openapp-ohne-siegel", "client", "TestReaderOpenAppPrueftSiegel", [edit(RDR, "return openApp(r.root, r, app)", "return filepath.Join(r.root, \"apps\", app.Name, app.Commit), nil")]),
    ("reader-manifest-signatur", "client", "TestReaderFehlerfaelle", [edit(RDR, "m, err := VerifyManifest(bundle.Manifest.Envelope, id.Pubkey, id.DeviceID, 0)", "_ = id\n\tm, err := ParseManifest(bundle.Manifest.Envelope.Manifest)")]),
    ("reader-vertrag", "client", "TestReaderFehlerfaelle", [edit(RDR, "m.Contract != WindowsContract || m.Apps == nil", "m.Apps == nil"), edit(MAN, "(m.Apps != nil) != (m.Contract == appsContract) || ", "")]),  # zwei Schichten: Leser und ParseManifest
    ("reader-apps-liest-token", "client", "TestReaderAppsDekodiertKeinToken", [edit(RDR, "id, err := r.identity()", "c, err := r.Credential()\n\tid := struct {\n\t\tDeviceID string\n\t\tPubkey   []byte\n\t}{c.DeviceID, c.Pubkey}")]),
    ("runtime-widerruf-loescht", "fensterio", "TestRuntimeWiderrufLoeschtNichts", [edit(RUN, "\t\"context\"\n\t\"errors\"\n", "\t\"context\"\n\t\"errors\"\n\t\"os\"\n"), edit(RUN, "func() error { return nil })", "func() error { return os.Remove(filepath.Join(r.cfg.Root, \"credential\")) })")]),
    ("runtime-client-engine", "fensterio", "TestFensterioRuftNurLeserUndAppAPI", [edit(RUN, "var ErrOpen = ", "var _ = client.OpenState\n\nvar ErrOpen = ")]),
    ("sync-legt-apps-ab", "client", "TestSyncLegtAppsAb", [edit(ENG, "\te.ensureApps(ctx, m)\n", "")]),
    ("prepare-ohne-sperre", "client", "TestAppPrepareNachWiderrufLegtNichtsAb", [edit(STO, "return s.state.Use(func() error {", "return (func(f func() error) error { return f() })(func() error {")]),
    ("log-code-app", "client", "TestLogCodeAppStoerung", [edit(LOGF, "code > LogAppFault {", "code > LogPipeFault {")]),
    # B1/B2: UI-Thread und Tür
    ("ui-thread-liest", "fensterio", "TestBedienfeldUIThreadOhneNetzUndBruecke", [edit(APW, "\t\t\ta.model.SetApps(l.Names)", "\t\t\t_, _ = a.rt.Apps()\n\t\t\ta.model.SetApps(l.Names)")]),
    ("bedienfeld-importiert-client", "fensterio", "TestBedienfeldImportiertClientNicht", [edit(APW, "import (\n", "import (\n\t_ \"github.com/januaport/januaport-plugins/desktop-runner/internal/client\"\n")]),
    ("bedienfeld-importiert-platform", "fensterio", "TestPlatformNurHinterDerTuer", [edit(APW, "import (\n", "import (\n\t_ \"github.com/januaport/januaport-plugins/desktop-runner/internal/platform\"\n")]),
    ("fensterio-abbruch", "fensterio", "TestFensterioKeinAbbruchUndKeinPanel", [edit(HST, "\t\th.cancel()", "\t\tos.Exit(1)\n\t\th.cancel()"), edit(HST, "import (\n", "import (\n\t\"os\"\n")]),
    ("panel-importiert-fensterio", "panel", "TestPanelImportWaechter", [edit("internal/panel/model.go", "import (\n", "import (\n\t_ \"github.com/januaport/januaport-plugins/desktop-runner/internal/fensterio\"\n")]),
    # Native Ereignisse (Urteile, Schritte)
    ("nav-alles-erlaubt", "fensterio", "TestNavigationVerdict", [edit(EVT, "\t}\n\treturn NavVerdict{Cancel: true}\n}", "\t}\n\treturn NavVerdict{}\n}")]),
    ("neues-fenster-nicht-abgebrochen", "fensterio", "TestNewWindowVerdictIstImmerAbgebrochen", [edit(EVT, "\tv := NavigationVerdict(app, uri)\n\tv.Cancel = true\n\treturn v", "\treturn NavigationVerdict(app, uri)")]),
    ("download-nicht-abgebrochen", "fensterio", "TestDownloadVerdictBrichtImmerAb", [edit(EVT, "func DownloadVerdict() NavVerdict { return NavVerdict{Cancel: true} }", "func DownloadVerdict() NavVerdict { return NavVerdict{} }")]),
    ("quelle-ignoriert", "fensterio", "TestMessageSource", [edit(EVT, "return fenster.Source{MainFrame: true, URL: url}", "return fenster.Source{MainFrame: true, URL: fenster.StartURL(\"demo\")}")]),
    ("schritte-laufen-weiter", "fensterio", "TestRunStepsHaeltBeimErstenFehlerAn", [edit(EVT, "\t\tif err := s.Run(); err != nil {\n\t\t\treturn &StepError{Step: s.Name}\n\t\t}", "\t\tif err := s.Run(); err != nil {\n\t\t\t_ = err\n\t\t}")]),
    ("schritt-ursache", "fensterio", "TestRunStepsHaeltBeimErstenFehlerAn", [edit(EVT, "type StepError struct{ Step string }", "type StepError struct {\n\tStep  string\n\tCause error\n}\n\nfunc (e *StepError) Unwrap() error { return e.Cause }"), edit(EVT, "return &StepError{Step: s.Name}", "return &StepError{Step: s.Name, Cause: err}")]),
    ("adapter-download-fehlt", "fensterio", "TestAdapterBindetAlleEreignisse", [edit(WVW, "{\"bind DownloadStarting\", func() error {", "{\"bind Download\", func() error {")]),
    ("adapter-neues-fenster-fehlt", "fensterio", "TestAdapterBindetAlleEreignisse", [edit(WVW, "{\"bind NewWindowRequested\", func() error {", "{\"bind NewWindow\", func() error {")]),
    ("adapter-navigation-fehlt", "fensterio", "TestAdapterBindetAlleEreignisse", [edit(WVW, "{\"bind NavigationStarting\", func() error {", "{\"bind Navigation\", func() error {")]),
    ("adapter-nachricht-fehlt", "fensterio", "TestAdapterBindetAlleEreignisse", [edit(WVW, "{\"bind WebMessageReceived\", func() error {", "{\"bind WebMessage\", func() error {")]),
    ("adapter-berechtigung-fehlt", "fensterio", "TestAdapterBindetAlleEreignisse", [edit(WVW, "{\"bind PermissionRequested\", func() error {", "{\"bind Permission\", func() error {")]),
    ("adapter-prozessabsturz-fehlt", "fensterio", "TestAdapterBindetAlleEreignisse", [edit(WVW, "{\"bind ProcessFailed\", func() error {", "{\"bind Process\", func() error {")]),
    ("adapter-setting-fehlt", "fensterio", "TestAdapterWendetAlleSettingsAn", [edit(WVW, "\"IsStatusBarEnabled\":", "\"IsStatusBar\":")]),
    # fenster: externe Links, Layout; panel: App-Menü
    ("ext-userinfo", "fenster", "TestExternalLink", [edit(EXT, "u.User != nil || ", ""), edit(EXT, "authority != u.Host || strings.ContainsAny(authority, \"@%[]\")", "strings.ContainsAny(authority, \"%[]\")"), edit(EXT, "r != '-' {", "r != '-' && r != '@' {")]),  # vier Schichten
    ("ext-http", "fenster", "TestExternalLink", [edit(EXT, "!strings.HasPrefix(raw, \"https://\")", "false"), edit(EXT, "u.Scheme != \"https\" || ", ""), edit(EXT, "rest := strings.TrimPrefix(raw, \"https://\")", "rest := raw[strings.Index(raw, \"://\")+3:]")]),  # drei Schichten
    ("ext-invalid-host", "fenster", "TestDecide", [edit(EXT, "strings.HasSuffix(strings.SplitN(host, \":\", 2)[0], \".invalid\")", "false")]),
    ("ext-zeichen", "fenster", "TestExternalLink", [edit(EXT, "r <= 0x20 || r >= 0x7f || r == '\\\\'", "false && r == 0")]),
    ("layout-leiste", "fenster", "TestLayout", [edit(EXT, "view = Rect{0, bh, w, h - bh}", "view = Rect{0, 0, w, h}")]),
    ("panel-app-unbekannt", "panel", "TestPanelAppNurBekannteNamen", [edit(MDL, "\t\t\tif n == name {", "\t\t\tif n == name || true {")]),
    ("panel-app-validierung", "panel", "TestPanelAppNurBekannteNamen", [edit(MDL, "if appName.MatchString(n) && !seen[n] {", "if !seen[n] {")]),
    # VM-Befund: begrenzter Start, kein Prozessende, COM-Thread
    ("wait-fehler-ignoriert", "fensterio", "TestWaitForBegrenzt", [edit(WAIT, "if err != nil {\n\t\t\treturn err\n\t\t}", "if err != nil && false {\n\t\t\treturn err\n\t\t}")]),
    ("wait-ohne-grenze", "fensterio", "TestWaitForBegrenzt", [edit(WAIT, "deadline := time.Now().Add(timeout)", "deadline := time.Now().Add(timeout + 3*time.Second)")]),
    ("wait-beendet-nicht", "fensterio", "TestWaitForBegrenzt", [edit(WAIT, "\t\tstep()\n", "")]),
    ("start-frist-lang", "fensterio", "TestStartFristBegrenzt", [edit(WAIT, "const startFrist = 20 * time.Second", "const startFrist = 20 * time.Hour")]),
    ("edge-exit", "fensterio", "TestEdgeKopieOhneProzessendeUndOhneUnbegrenzteWartezeit", [edit(ECH, "\te.failure.CompareAndSwap(nil, &err)\n}\n\n// fail", "\te.failure.CompareAndSwap(nil, &err)\n\tos.Exit(1)\n}\n\n// fail"), edit(ECH, '\t"fmt"\n', '\t"fmt"\n\t"os"\n')]),
    ("edge-getmessage", "fensterio", "TestEdgeKopieOhneProzessendeUndOhneUnbegrenzteWartezeit", [edit(ECH, "\te.hwnd = hwnd\n\tbrowserArgs", "\te.hwnd = hwnd\n\t_ = w32.User32GetMessageW\n\tbrowserArgs")]),
    ("edge-echo", "fensterio", "TestEdgeKopieOhneProzessendeUndOhneUnbegrenzteWartezeit", [edit(ECH, "\tif err = e.webview.AddProcessFailed(", "\t_ = e.webview.AddWebMessageReceived(e.webMessageReceived, &token)\n\tif err = e.webview.AddProcessFailed(")]),
    ("edge-fatal", "fensterio", "TestEdgeKopieOhneProzessendeUndOhneUnbegrenzteWartezeit", [edit(ECH, "\t\te.errorCallback(err)\n\t\treturn 0\n\t}\n\tdefer req.Release()", "\t\tlog.Fatal(err)\n\t}\n\tdefer req.Release()")]),
    ("bedienfeld-ohne-com", "fensterio", "TestBedienfeldInitialisiertCOMAufDemUIThread", [edit(BMN, "\tleave, _ := fensterio.EnterUI()\n\tdefer leave()\n", "")]),
    ("bedienfeld-com-im-thread", "fensterio", "TestBedienfeldInitialisiertCOMAufDemUIThread", [edit(BMN, "\tleave, _ := fensterio.EnterUI()\n\tdefer leave()\n", "\tgo func() { _, _ = fensterio.EnterUI() }()\n")]),
    ("adapter-ohne-thread-pruefung", "fensterio", "TestAdapterPruefteThreadUndWartetBegrenzt", [edit(WVS, '"com apartment"', '"com"')]),
    ("adapter-ohne-cancel", "fensterio", "TestAdapterPruefteThreadUndWartetBegrenzt", [edit(WVS, "\t\t\t\ts.chromium.Cancel()\n", "")]),
    ("adapter-ohne-wait", "fensterio", "TestAdapterPruefteThreadUndWartetBegrenzt", [edit(WVS, "err := WaitFor(startFrist, s.pumpOnce,", "err := waitUnbegrenzt(s.pumpOnce,"), edit(WVS, "func (s *Surface) trace(event, detail string) {", "func waitUnbegrenzt(step func(), poll func() (bool, error)) error {\n\tfor {\n\t\tif ok, err := poll(); ok || err != nil {\n\t\t\treturn err\n\t\t}\n\t\tstep()\n\t}\n}\n\nfunc (s *Surface) trace(event, detail string) {")]),
    # SEC-Bedingungen zur Edge-Kopie
    ("edge-panic", "fensterio", "TestEdgeKopieOhnePanicEndlosschleifeUndWarten", [edit(ECH, "func globalErrorHandler(err error) {\n\tif err != nil {", "func globalErrorHandler(err error) {\n\tif err != nil {\n\t\tpanic(err)")]),
    ("edge-endlosschleife", "fensterio", "TestEdgeKopieOhnePanicEndlosschleifeUndWarten", [edit(ECH, "func (e *Chromium) Cancel() { atomic.StoreUintptr(&e.cancelled, 1) }", "func (e *Chromium) Cancel() {\n\tatomic.StoreUintptr(&e.cancelled, 1)\n\tfor {\n\t\tbreak\n\t}\n}")]),
    ("edge-sleep", "fensterio", "TestEdgeKopieOhnePanicEndlosschleifeUndWarten", [edit(ECH, "func (e *Chromium) Cancel() { atomic.StoreUintptr(&e.cancelled, 1) }", "func (e *Chromium) Cancel() {\n\tatomic.StoreUintptr(&e.cancelled, 1)\n\ttime.Sleep(0)\n}")]),
    ("edge-umgebung-ohne-return", "fensterio", "TestEdgeRueckrufeKehrenNachFehlerZurueck", [edit(ECH, "\tif err != nil {\n\t\te.errorCallback(err)\n\t\treturn 0\n\t}\n\treturn 0\n}", "\tif err != nil {\n\t\te.errorCallback(err)\n\t}\n\treturn 0\n}")]),
    ("edge-controller-ohne-return", "fensterio", "TestEdgeRueckrufeKehrenNachFehlerZurueck", [edit(ECH, "syscall.Errno(res)))\n\t\treturn 0\n\t}\n\n\tcontroller.vtbl.AddRef", "syscall.Errno(res)))\n\t}\n\n\tcontroller.vtbl.AddRef")]),
    ("edge-ohne-upstream-hash", "fensterio", "TestEdgeKopieNenntUpstream", [edit(EDD, "11c6e567b916645d5fe6df006681f988ac52c13d", "unbekannt")]),
    ("edge-falsche-pruefsumme", "fensterio", "TestEdgeKopieNenntUpstream", [edit(EDD, "h1:7U3QcDj1PrBPaxJNCui2k1SkWml+Q5kvFUFyTImA6NU=", "h1:alt=")]),
    ("edge-wird-importiert", "panel", "TestEdgeNurVonFensterio", [edit(PTW, '\t"golang.org/x/sys/windows"\n)', '\t"golang.org/x/sys/windows"\n\n\t_ "github.com/januaport/januaport-plugins/desktop-runner/internal/edge"\n)')]),
    # Fremdserver-Befund: Schichten 1 und 2, Diagnose, Umgebung
    ("resource-filter-fehlt", "fensterio", "TestAdapterSperrtRessourcenUndNetz", [edit(WVS, 'return wv2com.AddWebResourceRequestedFilter(s.com(), "*", wv2com.ResourceContextAll)', 'return nil')]),
    ("resource-fremder-host", "fenster", "TestResourceAllowed", [edit(RSR, "if !strings.HasPrefix(raw, prefix) {\n\t\treturn false\n\t}", "if false {\n\t\treturn false\n\t}")]),
    ("resource-dokument-ohne-html", "fenster", "TestResourceAllowed", [edit(RSR, "if document {\n\t\treturn NavigationAllowed(app, raw)\n\t}", "_ = document")]),
    ("resource-elternverzeichnis", "fenster", "TestResourceAllowed", [edit(RSR, 'if seg == ".." {', 'if seg == "..." {')]),
    ("resource-kodierter-trenner", "fenster", "TestResourceAllowed", [edit(RSR, '[]string{"%2f", "%5c", "%00", "%3f", "%23"}', '[]string{"%5c", "%00", "%3f", "%23"}')]),
    ("browserargs-loopback", "fenster", "TestBrowserArguments", [edit(RSR, "--proxy-bypass-list=<-loopback> --host-resolver-rules", "--host-resolver-rules"), edit(RSR, "`--proxy-bypass-list=<-loopback>`, ", "")]),
    ("browserargs-festes-ziel", "fenster", "TestBrowserArguments", [edit(RSR, "--proxy-server=http://0.0.0.0:0 --proxy", "--proxy-server=http://127.0.0.1:1 --proxy"), edit(RSR, "`--proxy-server=http://0.0.0.0:0`", "`--proxy-server=http://127.0.0.1:1`")]),
    ("diag-traeger", "fenster", "TestDiagText", [edit(DGN, "if EnthaeltTraeger([]byte(s)) {", "if false {")]),
    ("verdict-rahmen", "fenster", "TestSourceVerdict", [edit(DGN, "if !s.MainFrame {\n\t\treturn false, \"rahmen\"\n\t}", "")]),
    ("reply-inhalt", "fenster", "TestReplySummary", [edit(DGN, 'return "fehler=" + r.Fehler', 'return "fehler=" + r.Fehler + string(b)'), edit(DGN, 'if r.Ok != nil && *r.Ok {\n\t\treturn "ok"', 'if r.Ok != nil && *r.Ok {\n\t\treturn "ok" + string(b)')]),
    ("env-scrub-fehlt", "fensterio", "TestAdapterPruefteThreadUndWartetBegrenzt", [edit(WVS, '{"umgebung", func() error {', '{"env", func() error {')]),
    ("env-gross-klein", "fensterio", "TestEnvNamenZumEntfernen", [edit(ENVF, 'strings.EqualFold(name[:4], "jnpt")', 'name[:4] == "JNPT"')]),
    # SEC-Urteil Lauf 5: Browserprozess prüfen, WEBVIEW2_*, spekulatives Laden
    ("netzargumente-fehlen", "fensterio", "TestAdapterSperrtRessourcenUndNetz", [edit(WVS, "args = append(args, fenster.NetworkLockArgs()...)", "_ = fenster.NetworkLockArgs")]),
    ("browserpruefung-fehlt", "fensterio", "TestAdapterPruefteThreadUndWartetBegrenzt", [edit(WVS, '{"browser pruefen", func() error {', '{"browser", func() error {')]),
    ("browserpruefung-ohne-kommandozeile", "fensterio", "TestAdapterSperrtRessourcenUndNetz", [edit(WVS, "problems := fenster.BrowserCommandLineProblems(line)", "problems := []string(nil)\n\t\t\t_ = line")]),
    ("webview2-variablen-ignoriert", "fensterio", "TestAdapterSperrtRessourcenUndNetz", [edit(WVS, "if findings := webview2Findings(envsnap.Start(), os.Environ()); len(findings) > 0 {", "if findings := jnptNames(nil); len(findings) > 0 {"), edit(WVS, '\t"github.com/januaport/januaport-plugins/desktop-runner/internal/envsnap"\n', '')]),
    ("webview2-gross-klein", "fensterio", "TestWebView2Eingriffe", [edit(ENVF, 'len(name) >= 9 && strings.EqualFold(name[:9], "webview2_")', 'len(name) >= 9 && name[:9] == "WEBVIEW2_"')]),
    ("webview2-ohne-unterstrich", "fensterio", "TestWebView2Eingriffe", [edit(ENVF, "len(name) >= 9 && strings.EqualFold(name[:9], \"webview2_\")", "len(name) >= 8 && strings.EqualFold(name[:8], \"webview2\")")]),
    ("cmdline-doppelt", "fenster", "TestBrowserCommandLineProblems", [edit(RSR, "case len(got) > 1:", "case false:")]),
    ("cmdline-abweichend", "fenster", "TestBrowserCommandLineProblems", [edit(RSR, "case got[0] != w.value:", "case false:")]),
    ("cmdline-verbotene-argumente", "fenster", "TestBrowserCommandLineProblems", [edit(RSR, '[]string{"--proxy-pac-url", "--no-proxy-server", "--proxy-auto-detect"}', '[]string{"--proxy-pac-url"}')]),
    ("cmdline-anfuehrungszeichen", "fenster", "TestBrowserCommandLineProblems", [edit(RSR, "case c == '\"':\n\t\t\tinQuote = !inQuote", "case c == '\"':\n\t\t\tinQuote = false")]),
    ("lader-prerender", "client", "TestInjectCSPLehntSpekulativesLadenAb", [edit(APL, 'case "prerender", "prefetch", "preconnect", "dns-prefetch":', 'case "prefetch", "preconnect", "dns-prefetch":')]),
    ("lader-preconnect", "client", "TestInjectCSPLehntSpekulativesLadenAb", [edit(APL, 'case "prerender", "prefetch", "preconnect", "dns-prefetch":', 'case "prerender", "prefetch", "dns-prefetch":')]),
    ("lader-grossschreibung", "client", "TestInjectCSPLehntSpekulativesLadenAb", [edit(APL, "strings.Fields(strings.ToLower(a.Val))", "strings.Fields(a.Val)")]),
    ("lader-speculationrules", "client", "TestInjectCSPLehntSpekulativesLadenAb", [edit(APL, 'strings.EqualFold(strings.TrimSpace(a.Val), "speculationrules")', 'false')]),
    ("lader-template", "client", "TestInjectCSPLehntSpekulativesLadenAb", [edit(APL, "\t\tfor c := n.FirstChild; c != nil; c = c.NextSibling {\n\t\t\twalk(c)\n\t\t}\n\t}\n\twalk(doc)\n\treturn found", "\t\tfor c := n.FirstChild; c != nil && n.Data != \"template\"; c = c.NextSibling {\n\t\t\twalk(c)\n\t\t}\n\t}\n\twalk(doc)\n\treturn found")]),
    # Lauf 6: Eingriffe vor und nach dem Lader-Init unterscheiden
    ("eingriff-start-ignoriert", "fensterio", "TestWebView2Eingriffe", [edit(ENVF, "for _, kv := range start {", "for _, kv := range []string(nil) {")]),
    ("eingriff-laufend-ignoriert", "fensterio", "TestWebView2Eingriffe", [edit(ENVF, "for _, kv := range current {", "for _, kv := range []string(nil) {")]),
    ("eingriff-neutral-alles", "fensterio", "TestWebView2Eingriffe", [edit(ENVF, "ok && value == want {", "ok && (value == want || true) {")]),
    ("eingriff-wert-in-meldung", "fensterio", "TestWebView2Eingriffe", [edit(ENVF, 'out = append(out, fmt.Sprintf("%s(laenge=%d,laufend)", name, len(value)))', 'out = append(out, fmt.Sprintf("%s(laenge=%d,laufend)", name, len(value))+value)')]),
    ("eingriff-kanal-neutral", "fensterio", "TestWebView2Eingriffe", [edit(ENVF, '"WEBVIEW2_RELEASE_CHANNEL_PREFERENCE":   "0",', '"WEBVIEW2_RELEASE_CHANNEL_PREFERENCE":   "1",')]),
    ("envsnap-importiert-mehr", "fensterio", "TestEnvsnapLaeuftVorDemLader", [edit(ESN, 'import "os"', 'import (\n\t"os"\n\t"strings"\n)\n\nvar _ = strings.ToUpper')]),
    # Lauf 10: Fokus an die WebView
    ("fokus-bei-setfocus-fehlt", "fensterio", "TestSurfaceGibtDerSeiteDenFokus", [edit(WVS, "case wmSetFocus:\n\t\t\tif s.view != nil {\n\t\t\t\ts.chromium.Focus()", "case wmSetFocus:\n\t\t\tif s.view != nil {\n\t\t\t\t_ = s.chromium")]),
    ("fokus-beim-anzeigen-fehlt", "fensterio", "TestSurfaceGibtDerSeiteDenFokus", [edit(WVS, "pShow.Call(s.hwnd, 1)\n\t\t\ts.chromium.Focus()", "pShow.Call(s.hwnd, 1)")]),
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
