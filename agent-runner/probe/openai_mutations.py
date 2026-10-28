"""#1193/#1228 mutation evidence; exclusive worktree access required.

python agent-runner/probe/openai_mutations.py [running-go-container]
The container must mount the whole checkout and use agent-runner as its workdir.
Without a container argument, use local Go. Restore every file byte-for-byte.
Reports go to the system temp directory, never into the repository.
"""
from pathlib import Path
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]
A = "internal/openai/adapter.go"
C = "internal/runner/contract.go"
G = "internal/runner/gateway.go"
H = "internal/runner/conversation.go"
L = "internal/runner/loop.go"

# Each guard mutation keeps the code compilable, so only behavioral failures count.
CASES = [
    ("#1228 Altes Token-Feld wieder erlaubt", "TestCompletionTokenField|TestOpenAIOutgoingGuardsAtAdapter", A,
     '"messages tools max_completion_tokens"', '"messages tools max_completion_tokens max_tokens"'),
    ("#1228 OpenAI-Anfrage nutzt altes Feld", "TestCompletionTokenField|TestTokenBudgetAtAdapter", A,
     '`json:"max_completion_tokens"`', '`json:"max_tokens"`'),
    ("#1228 OpenAI-Deckel 1024 entfernt", "TestTokenBudgetAtAdapter", H,
     'projected, min(1024, a.Limits.TokensOut)', 'projected, a.Limits.TokensOut'),
    ("#1228 OpenAI ignoriert niedrigeres Budget", "TestTokenBudgetAtAdapter", H,
     'projected, min(1024, a.Limits.TokensOut)', 'projected, 1024'),
    ("#1228 Anthropic-Deckel 1024 entfernt", "TestTokenBudgetAtAdapter", H,
     'tools, min(1024, a.Limits.TokensOut)', 'tools, a.Limits.TokensOut'),
    ("#1228 Anthropic ignoriert niedrigeres Budget", "TestTokenBudgetAtAdapter", H,
     'tools, min(1024, a.Limits.TokensOut)', 'tools, 1024'),
    ("#1228 Wiederholung nach Anbieterfehler", "TestOpenAIUnknownCompletionTokensNoRetryAtAdapter", G,
     'return response, end, detail\n\t}\n\tresponse, e = openai.ParseResponse(raw)',
     'if end == "error" && detail == "provider" {\n\t\t\t_, _, _ = r.modelBody(ctx, c, bytes.Replace(b, []byte("max_completion_tokens"), []byte("max_tokens"), 1))\n\t\t}\n\t\treturn response, end, detail\n\t}\n\tresponse, e = openai.ParseResponse(raw)'),

    ("H1 developer wieder erlaubt", "TestRunnerVocabularyRejected|TestOpenAIOutgoingGuardsAtAdapter", A, 'case "system", "user", "assistant":', 'case "system", "developer", "user", "assistant":'),
    ("H2 content_filter wieder Fehler", "TestOpenAIFinishDetailsAtAdapter", H, 'case "content_filter":\n\t\treturn turn{}, "limit", "content_filter"', ''),
    ("H2 Log-Detail fehlt", "TestOpenAIFinishDetailsAtAdapter", L, 'record.Detail = detail', '// detail removed'),
    ("model in Anfrage", "TestRequestAndGolden", A, 'type Request struct {', 'type Request struct {\n Model string `json:"model"`'),
    ("stream in Anfrage", "TestRequestAndGolden", A, 'type Request struct {', 'type Request struct {\n Stream bool `json:"stream"`'),
    ("tool_call_id fehlt", "TestOpenAILoopTable", A, 'ToolCallID: id', 'ToolCallID: ""'),
    ("Default openai statt anthropic", "TestProtocolDefaultAndUnknownAtAdapter|TestLegacyAnthropicRequestBytes", H, 'a.Model.Protocol == "openai"', 'a.Model.Protocol == "" || a.Model.Protocol == "openai"'),
    ("Unbekanntes Protokoll still anthropic", "TestProtocolDefaultAndUnknownAtAdapter|TestInvalidProtocolValuesAtAdapter", C, '(value != "anthropic" && value != "openai")', 'false'),
    ("Senderpruefung entfernt", "TestOpenAIOutgoingGuardsAtAdapter", G, 'e != nil || openai.Validate(b) != nil', 'e != nil'),
    ("Antwortpruefung entfernt", "TestOpenAIUnsafeResponsesAtAdapter", A, 'if validateMessage(b) != nil {', 'if false {'),
    ("Leere choices ungeprueft", "TestResponseTable", A, 'len(envelope.Choices) == 0', 'false'),
    ("Antwortrolle ungeprueft", "TestOpenAIUnsafeResponsesAtAdapter", A, 'str(m["role"]) != "assistant"', 'false'),
    ("Top-Level-Allowlist entfernt", "TestForbiddenRequests|TestRunnerVocabularyRejected", A, '!only(m, "messages tools max_completion_tokens")', 'false'),
    ("Erzwungene Werkzeugwahl", "TestForbiddenRequests|TestRunnerVocabularyRejected", A, '"messages tools max_completion_tokens"', '"messages tools max_completion_tokens tool_choice"'),
    ("Nachrichten-Zusatzfelder", "TestForbiddenRequests", A, '!only(m, "role content tool_calls tool_call_id")', 'false'),
    ("Unbekannte Rolle", "TestForbiddenRequests", A, 'default:\n\t\treturn errPayload', 'default:\n\t\t// guard removed'),
    ("Fehlende Werkzeugkorrelation akzeptiert", "TestForbiddenRequests|TestOpenAIOutgoingGuardsAtAdapter", A, 'str(m["tool_call_id"]) == ""', 'false'),
    ("Nichttext-Inhalt akzeptiert", "TestForbiddenRequests|TestResponseTable", A, 'if json.Unmarshal(content, &text) != nil {', 'if json.Unmarshal(content, &text) != nil && false {'),
    ("Leere Nachrichten akzeptiert", "TestForbiddenRequests", A, 'len(messages) == 0', 'false'),
    ("Null-Werkzeugliste akzeptiert", "TestForbiddenRequests", A, 'tools == nil', 'false'),
    ("Null-Aufrufliste akzeptiert", "TestForbiddenRequests", A, 'calls == nil', 'false'),
    ("Werkzeug-Formpruefung entfernt", "TestForbiddenRequests", A,
     'if !ok || !only(t, "type function") || str(t["type"]) != "function" || !fnOK || !only(fn, "name description parameters") || str(fn["name"]) == "" || !schemaOK {',
     'if false && (!ok || !only(t, "type function") || str(t["type"]) != "function" || !fnOK || !only(fn, "name description parameters") || str(fn["name"]) == "" || !schemaOK) {'),
    ("Funktionsaufruf-Formpruefung entfernt", "TestForbiddenRequests|TestResponseTable", A,
     'if !ok || !only(c, "id type function") || str(c["id"]) == "" || str(c["type"]) != "function" || !fnOK || !only(fn, "name arguments") || str(fn["name"]) == "" || string(fn["arguments"]) == "null" || json.Unmarshal(fn["arguments"], &args) != nil {',
     'if false && (!ok || !only(c, "id type function") || str(c["id"]) == "" || str(c["type"]) != "function" || !fnOK || !only(fn, "name arguments") || str(fn["name"]) == "" || string(fn["arguments"]) == "null" || json.Unmarshal(fn["arguments"], &args) != nil) {'),
    ("Nichttext-Ergebnis durchgereicht", "TestToolResultTextProjection", A, '} else {\n\t\t\tparts = append(parts, Placeholder)', '} else {\n\t\t\tparts = append(parts, string(raw))'),
    ("Ungueltige Argumente ans Gateway", "TestOpenAILoopTable", L, 'json.Unmarshal(b.Input, &input) != nil || input == nil', 'false && (json.Unmarshal(b.Input, &input) != nil || input == nil)'),
    ("Werkzeugfreigabe entfernt", "TestOpenAIToolAuthorizationAndLimits", L, '!allowed[b.Name] || ids[b.ID]', 'ids[b.ID]'),
    ("Doppelte Aufruf-ID erlaubt", "TestOpenAIToolAuthorizationAndLimits", L, '!allowed[b.Name] || ids[b.ID]', '!allowed[b.Name]'),
    ("Aufrufgrenze entfernt", "TestOpenAIToolAuthorizationAndLimits", L, 'record.ToolCalls >= a.Limits.ToolCalls', 'false'),
    ("Laenge wird Erfolg", "TestOpenAILoopTable", H, 'case "length":\n\t\tout.Stop = "max_tokens"', 'case "length":\n\t\tout.Stop = "end_turn"'),
    ("Unbekannter Stop-Grund akzeptiert", "TestOpenAIUnsafeResponsesAtAdapter", L, 'response.Stop != "tool_use" || len(response.Calls) == 0', 'false'),
]


def main():
    command = ["go"] if len(sys.argv) == 1 else ["docker", "exec", sys.argv[1], "go"]
    report = []
    failed = False
    for label, test, name, old, new in CASES:
        path = ROOT / name
        original = path.read_bytes()
        try:
            body = original.decode("utf-8")
            if body.count(old) != 1:
                raise RuntimeError(f"mutation anchor not unique: {label}")
            path.write_text(body.replace(old, new, 1), encoding="utf-8", newline="\n")
            proc = subprocess.run(command + ["test", "./internal/...", "-run", test, "-count=1", "-timeout=15s"],
                                  cwd=ROOT, text=True, encoding="utf-8", errors="replace", capture_output=True, timeout=60)
            output = proc.stdout + proc.stderr
            killed = proc.returncode != 0 and "--- FAIL:" in output and "build failed" not in output and "test timed out" not in output
            line = f"{'ROT' if killed else 'UNBELEGT'} | {label} | {test}"
            print(line, flush=True)
            report.append(line + "\n" + output)
            failed |= not killed
        finally:
            path.write_bytes(original)
    out = Path(tempfile.gettempdir()) / "agent-runner-mutations"
    out.mkdir(parents=True, exist_ok=True)
    (out / "openai.txt").write_text("\n".join(report), encoding="utf-8", newline="\n")
    return int(failed)


if __name__ == "__main__":
    raise SystemExit(main())
