"""B1 mutation evidence. Run against a disposable Go container mounting this repo.

python agent-runner/probe/mutations.py jnpt1099-b1-check
B1.1 only: append B1.1 as the optional case filter.
Every source edit is restored in finally. No golden file is modified.
The report goes to <tmp>/agent-runner-mutations/, never into the repo.
"""
from pathlib import Path
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]
CONTAINER = sys.argv[1]
A = "internal/anthropic/adapter.go"
C = "internal/runner/contract.go"
G = "internal/runner/gateway.go"
L = "internal/runner/loop.go"
S = "internal/runner/server.go"
W = "../.github/workflows/agent-runner-release.yml"

# label, Go test regex, edits (path, original, replacement)
CASES = [
    ("model im Body", "TestThinkingAndCache|TestLoopGoldenThinkingAndLog", [(A, 'type Request struct {', 'type Request struct {\n Model string `json:"model"`')]),
    ("stream im Body", "TestThinkingAndCache|TestLoopGoldenThinkingAndLog", [(A, 'type Request struct {', 'type Request struct {\n Stream bool `json:"stream"`')]),
    ("Anbietertext durchreichen", "TestGatewayErrorsFixed", [
        (G, 'Class  string `json:"class"`', 'Class  string `json:"class"`\n Message string `json:"message"`'),
        (G, 'return response, "error", envelope.Error.Class', 'return response, "error", envelope.Error.Message'),
        (C, 'text = fixedMessage(end, detail)', 'text = detail')]),
    ("Inhalt im Lauf-Protokoll", "TestLoopGoldenThinkingAndLog", [(L, 'r.log.Write(record)', '_, _ = r.cfg.Log.Write([]byte(a.Input)); r.log.Write(record)')]),
    ("Compose Ausgangsnetz external", "TestCompose$", [("docker-compose.yml", 'networks: [plugins]', 'networks: [plugins, external]'), ("docker-compose.yml", 'networks:\n  plugins:', 'networks:\n  external: {}\n  plugins:')]),
    ("Top-Level Allowlist entfernt", "TestForbiddenModelPayloads", [(A, 'messages system tools tool_choice max_tokens temperature top_p top_k stop_sequences thinking metadata', 'messages system tools tool_choice max_tokens temperature top_p top_k stop_sequences thinking metadata model stream')]),
    ("Erzwungene Werkzeugwahl", "TestForbiddenModelPayloads", [(A, 'if choice, ok := m["tool_choice"]; ok {', 'if choice, ok := m["tool_choice"]; ok && false {')]),
    ("Anbieter-Werkzeuge", "TestForbiddenModelPayloads", [(A, '!only(tool, "name description input_schema cache_control") || str(tool["name"]) == "" || len(tool["input_schema"]) == 0', 'len(tool) == 0')]),
    ("Nachrichten-Zusatzfelder", "TestForbiddenModelPayloads", [(A, '!only(msg, "role content")', 'false')]),
    ("Unbekannte Inhaltsbloecke", "TestForbiddenModelPayloads|TestUnsafeResponseAndUnknownToolAtAdapter", [(A, 'default:\n\t\t\treturn errPayload', 'default:\n\t\t\tcontinue')]),
    ("Inhaltsblock-Zusatzfelder", "TestForbiddenModelPayloads", [(A, 'if !only(b, fields) {', 'if !only(b, fields) && false {')]),
    ("URL statt base64", "TestForbiddenModelPayloads", [(A, 'str(source["type"]) != "base64"', 'false')]),
    ("Ungueltiges base64", "TestForbiddenModelPayloads", [(A, 'e != nil || str(source["data"]) == ""', 'false && (e != nil || str(source["data"]) == "")')]),
    ("Verschachteltes tool_result", "TestForbiddenModelPayloads", [(A, 'str(b["tool_use_id"]) == "" || ValidateContent(b["content"]) != nil', 'false')]),
    ("URL-Bild-Platzhalter entfernt", "TestToolResultSanitization|TestURLImageToolResultAtAdapter", [(A, 'var projected any = map[string]string{"type": "text", "text": Placeholder}', 'var projected any = b')]),
    ("Senderpruefung entfernt", "TestModelWireGuards", [(G, 'e != nil || anthropic.Validate(b) != nil', 'e != nil')]),
    ("runtime-key Sperre entfernt", "TestAuthHeadersAndPathsAtAdapter", [(S, 'if key == "" || len(auth) != 1 || subtle.ConstantTimeCompare([]byte(auth[0]), []byte("Bearer "+key)) != 1 {', 'if false && (key == "" || len(auth) != 1 || subtle.ConstantTimeCompare([]byte(auth[0]), []byte("Bearer "+key)) != 1) {')]),
    ("Lauf-Header Sperre entfernt", "TestAuthHeadersAndPathsAtAdapter", [(C, 'if !singleHeader(h, "X-Jnpt-Run-Token") || !singleHeader(h, "X-Jnpt-Run-Id") || h.Get("X-Jnpt-Run-Id") != a.Run.ID {', 'if false {')]),
    ("Gateway-Pfade und Vertragskennung", "TestAuthHeadersAndPathsAtAdapter", [(C, 'if a.Contract != 1 || a.Gateway.ModelPath != ModelPath || a.Gateway.MCPPath != MCPPath {', 'if false {')]),
    ("Unbekannte Argumente", "TestMalformedArgumentsAtAdapter", [(C, 'd.DisallowUnknownFields()', '// guard removed')]),
    ("Ungueltige Grenzen", "TestAuthHeadersAndPathsAtAdapter", [(C, 'if a.Limits.Steps <= 0 || a.Limits.ToolCalls < 0 || a.Limits.WallS <= 0', 'if false && (a.Limits.Steps <= 0 || a.Limits.ToolCalls < 0 || a.Limits.WallS <= 0'), (C, 'a.Run.Revision <= 0 {', 'a.Run.Revision <= 0) {')]),
    ("Schrittgrenze", "TestLimitsAtAdapter/steps", [(L, 'record.Steps < a.Limits.Steps', 'record.Steps < a.Limits.Steps+1')]),
    ("Werkzeuggrenze", "TestLimitsAtAdapter/tool_calls", [(L, 'record.ToolCalls >= a.Limits.ToolCalls', 'record.ToolCalls > a.Limits.ToolCalls')]),
    ("Context-Abbruch abgekoppelt", "TestCancellationAndWallAtAdapter/false", [(L, 'context.WithTimeout(parent,', 'context.WithTimeout(context.WithoutCancel(parent),')]),
    ("Eigene Wandzeit ignoriert", "TestAdvertisedWallCapEnforcedAtAdapter", [(L, 'min(a.Limits.WallS, r.cfg.MaxWallS)', 'a.Limits.WallS')]),
    ("Request-Wandzeit ignoriert", "TestCancellationAndWallAtAdapter/true", [(L, 'min(a.Limits.WallS, r.cfg.MaxWallS)', 'r.cfg.MaxWallS')]),
    ("Entzug ignoriert", "TestRevocationAtBothGateways", [(G, 'b.revoked.Store(true)', 'b.revoked.Store(false)')]),
    ("Weiterleitung erlaubt", "TestNoRedirectsOrProxyAtEitherGateway", [(G, 'return http.ErrUseLastResponse', 'return nil')]),
    ("Umgebungsproxy erlaubt", "TestNoRedirectsOrProxyAtEitherGateway", [(S, 'tr.Proxy = nil', 'tr.Proxy = http.ProxyFromEnvironment')]),
    ("Werkzeug-Allowlist entfernt", "TestUnsafeResponseAndUnknownToolAtAdapter", [(L, '!allowed[b.Name] || ids[b.ID]', 'ids[b.ID]')]),
    ("Doppelte Werkzeug-ID", "TestToolListAndMultiToolGuards", [(L, '!allowed[b.Name] || ids[b.ID]', '!allowed[b.Name]')]),
    ("Doppelte Listeneintraege", "TestToolListAndMultiToolGuards", [(L, '!toolName.MatchString(t.Name) || allowed[t.Name] || len(tools) >= 1024', '!toolName.MatchString(t.Name) || len(tools) >= 1024')]),
    ("Werkzeugnamen ungeprueft", "TestToolListAndMultiToolGuards", [(L, '!toolName.MatchString(t.Name) || allowed[t.Name]', 'allowed[t.Name]')]),
    ("Werkzeugliste unbegrenzt", "TestToolListSizeLimit", [(L, 'len(tools) >= 1024', 'len(tools) >= 2048')]),
    ("Cursor-Schleife", "TestToolListAndMultiToolGuards", [(L, 'seen[cursor] || len(seen) >= 1024', 'false')]),
    ("Schluesseldatei unbegrenzt", "TestConfigAndKeyFailClosed", [(S, 'io.LimitReader(f, 4097)', 'io.LimitReader(f, 8193)'), (S, 'len(b) > 4096', 'len(b) > 8192')]),
    ("Schluessel mit Zeilenumbruch", "TestConfigAndKeyFailClosed", [(S, 'strings.ContainsAny(key, "\\r\\n \\t")', 'false')]),
    ("Gateway-Konfiguration ungeprueft", "TestConfigAndKeyFailClosed", [(S, 'if e != nil || u.Host == ""', 'if false && (e != nil || u.Host == ""'), (S, '(u.Path != "" && u.Path != "/") {', '(u.Path != "" && u.Path != "/")) {')]),
    ("Wandzeit-Konfiguration ungeprueft", "TestConfigAndKeyFailClosed", [(S, 'cfg.MaxWallS < 1 || cfg.MaxWallS > DefaultMaxWallS', 'false')]),
    ("Daten hinter JSON", "TestMalformedArgumentsAtAdapter", [(S, 'e != nil || !json.Valid(body)', 'e != nil || (false && !json.Valid(body))')]),
    ("Antwortgroesse aufgeweicht", "TestResponseBody4MiBLimitAtAdapter", [(G, 'remaining: maxBody', 'remaining: maxBody * 2')]),
    ("Anfragegroesse aufgeweicht", "TestSizeBoundsAndMalformedModelAtAdapter", [(S, 'http.MaxBytesReader(w, req.Body, maxBody)', 'http.MaxBytesReader(w, req.Body, maxBody * 2)'), (S, 'MaxRequestBodyBytes: maxBody', 'MaxRequestBodyBytes: maxBody * 2'), (C, 'len(raw) > maxBody', 'len(raw) > maxBody * 2')]),
    ("Docker root", "TestDockerNonRootStatic", [("Dockerfile", 'USER 65532:65532', 'USER 0:0')]),
    ("Compose schreibbares Root-FS", "TestCompose$", [("docker-compose.yml", 'read_only: true', 'read_only: false')]),
    ("Thinking veraendert", "TestLoopGoldenThinkingAndLog", [(L, 'Content: response.Content', 'Content: json.RawMessage(`[{"type":"text","text":"replaced"}]`)')]),
    ("Cache entfernt", "TestThinkingAndCache", [(A, 'tools[len(tools)-1].CacheControl = map[string]string{"type": "ephemeral"}', 'tools[len(tools)-1].CacheControl = nil')]),
    ("SDK-Cleanup vom Lauf geloest", "TestCleanupWithinWallAtAdapter", [(L, 'scope: ctx', 'scope: nil')]),
    ("B1.1 H1 Close vor cancel", "TestCancelBeforeSessionCloseAtAdapter", [(L, 'cancel(); _ = session.Close()', '_ = session.Close(); cancel()')]),
    ("B1.1 H2 Antwortdeckel angehoben", "TestResponseBody4MiBLimitAtAdapter", [(G, 'remaining: maxBody', 'remaining: maxBody * 2')]),
    ("B1.1 H3 alter rejected-Satz", "TestRejectedMessageAtAdapter|TestAllResultGoldens", [(C, 'Der Modell-Durchgang hat die Anfrage des Agenten-Läufers abgewiesen.', 'Werkzeuge und Funktionen des Anbieters (Websuche, Abruf, Code-Ausführung, Container, MCP-Server) sind im Modell-Durchgang gesperrt.')]),
    ("B1.1 H4 Medien-Positivliste entfernt", "TestImageMediaTypesAtAdapter", [(A, 'if str(b["type"]) == "image" {', 'if false {')]),
    ("B1.1 H4 Projektionspruefung entfernt", "TestImageMediaTypesAtAdapter", [(A, 'if ValidateContent(encoded) == nil {', 'if true { _ = encoded')]),
    ("B1.1 H4 Senderpruefung entfernt", "TestImageMediaTypesAtAdapter", [(G, 'e != nil || anthropic.Validate(b) != nil', 'e != nil')]),
    ("B1.1 H4 Antwortpruefung entfernt", "TestImageMediaTypesAtAdapter", [(G, 'res.StatusCode != 200 || anthropic.ValidateContent(envelope.Response.Content) != nil', 'res.StatusCode != 200')]),
    ("B1.1 H4 Verschachtelungspruefung entfernt", "TestImageMediaTypesAtAdapter", [(A, 'str(b["tool_use_id"]) == "" || ValidateContent(b["content"]) != nil', 'str(b["tool_use_id"]) == ""')]),
    ("B1.1 H5 Sondenzustand nicht ignoriert", "TestSmokeStateIgnored", [(".gitignore", '/probe/.smoke/', '')]),
    ("B1.1 Release Branch-Ausloeser", "TestReleaseWorkflowOnlyOnTag", [(W, '    tags:', '    branches: [main]\n    tags:')]),
    ("B1.1 Release Tag-Schema gelockert", "TestReleaseTagSchema", [(W, '^agent-runner-v[0-9]+', 'agent-runner-v[0-9]+')]),
    ("B1.1 Release ungepinnte Aktion", "TestReleaseWorkflowUsesPinnedBySHA", [(W, 'actions/checkout@11d5960a326750d5838078e36cf38b85af677262', 'actions/checkout@v4')]),
    ("B1.1 Release anderer SHA-Pin", "TestReleaseWorkflowUsesPinnedBySHA", [(W, 'actions/checkout@11d5960a326750d5838078e36cf38b85af677262', 'actions/checkout@' + '0' * 40)]),
    ("B1.1 Release C1 Vergleich entfernt", "TestReleaseWorkflowOrderAndC1", [(W, '[ "$checked" = "$pushed" ] ||', '[ true ] ||')]),
    ("B1.1 Release C2 Drittanbieter mit Schreibrecht", "TestReleaseWorkflowC2NoThirdPartyWithWrite", [(W, 'docker/setup-buildx-action@', 'thirdparty/setup-buildx-action@')]),
    ("B1.1 Release Schreibrecht fuer scan", "TestReleaseWorkflowPermissions|TestReleaseWorkflowC2NoThirdPartyWithWrite", [(W, 'name: Trivy-Bericht (kein Gate)\n    needs: build\n    runs-on: ubuntu-latest\n    permissions:\n      contents: read\n      packages: read', 'name: Trivy-Bericht (kein Gate)\n    needs: build\n    runs-on: ubuntu-latest\n    permissions:\n      contents: read\n      packages: write')]),
    ("B1.1 Release Build mit Tag", "TestReleaseWorkflowBuildPushByDigest", [(W, '          platforms: linux/amd64', '          tags: ghcr.io/januaport/agent-runner:0.1.0\n          platforms: linux/amd64')]),
    ("B1.1 Release Sonden mit Tag statt Digest", "TestReleaseWorkflowOrderAndC1", [(W, '${env:IMAGE_REPO}@${env:DIGEST}', '${env:IMAGE_REPO}:0.1.0')]),
    ("B1.1 Release Publish ohne Sonden", "TestReleaseWorkflowOrderAndC1", [(W, 'needs: [build, probes]', 'needs: [build]')]),
    ("B1.1 Release Tag-Digest-Vergleich entfernt", "TestReleaseWorkflowOrderAndC1", [(W, '[ "$tagged" = "$DIGEST" ] ||', '[ true ] ||')]),
    ("B1.1 Release zweite Plattform", "TestReleaseWorkflowBuildPushByDigest", [(W, 'platforms: linux/amd64', 'platforms: linux/amd64,linux/arm64')]),
    ("B1.1 Release weiterer Build", "TestReleaseWorkflowBuildPushByDigest", [(W, '    steps:', '    steps:\n      - uses: docker/build-push-action@10e90e3645eae34f1e60eeb005ba3a3d33f178e8')]),
    ("B1.1 Release Scan als Gate", "TestReleaseWorkflowOrderAndC1", [(W, '--exit-code 0', '--exit-code 1')]),
]

def main():
    report = []
    failed = False
    selected = CASES if len(sys.argv) < 3 else [case for case in CASES if sys.argv[2] in case[0]]
    for label, test, edits in selected:
        originals = {ROOT / name: (ROOT / name).read_bytes() for name, _, _ in edits}
        try:
            for name, old, new in edits:
                path = ROOT / name
                body = path.read_text(encoding="utf-8")
                if old not in body:
                    raise RuntimeError(f"mutation anchor missing: {label}: {old}")
                path.write_text(body.replace(old, new, 1), encoding="utf-8", newline="\n")
            proc = subprocess.run(["docker", "exec", CONTAINER, "go", "test", "./internal/...", "-run", test, "-count=1", "-timeout=8s"], text=True, encoding="utf-8", errors="replace", capture_output=True, timeout=30)
            output = proc.stdout + proc.stderr
            killed = proc.returncode != 0 and ("--- FAIL:" in output or "test timed out" in output) and "build failed" not in output
            if label.startswith("B1.1"):
                killed &= "test timed out" not in output
            line = f"{'ROT' if killed else 'UNBELEGT'} | {label} | {test} | exit={proc.returncode}"
            print(line, flush=True)
            excerpt = output if len(output) <= 8000 else output[:8000] + "\n[Ausgabe fuer den Bericht auf 8000 Zeichen gekuerzt]\n"
            report.append(line + "\n" + excerpt)
            failed |= not killed
        finally:
            for path, body in originals.items():
                path.write_bytes(body)
    target = "mutations.txt" if len(sys.argv) < 3 else "mutations-additional.txt"
    out = Path(tempfile.gettempdir()) / "agent-runner-mutations"
    out.mkdir(parents=True, exist_ok=True)
    (out / target).write_text("\n".join(report), encoding="utf-8", newline="\n")
    return int(failed)

if __name__ == "__main__":
    raise SystemExit(main())
