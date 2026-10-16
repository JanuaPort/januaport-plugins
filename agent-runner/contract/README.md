# Draht-Vertrag — agent-runner B1

Maßgeblich: `cmd/jnpt/testdata/agent-runner-contract/` des Kerns, geliefert
für JanuaPort/januaport#1099. Die sieben JSON-Dateien und SHA256SUMS wurden
byte-identisch nach `golden/` kopiert. `.gitattributes` deaktiviert für diese
Dateien die Zeilenenden-Konvertierung. Der Test pinnt auch den Hash der
SHA256SUMS; fachliche Änderungen erfolgen im Code, nicht in diesen Dateien.

## Eingang und Meldung

MCP `tools/list`: genau ein Werkzeug `run_agent`; Ergebnis-Metadaten
`_meta["januaport.ai/runner"] = {contract: 1, max_wall_s: 300}` im Standard.
Die gemeldete ganzzahlige Wandzeit wird selbst eingehalten, der Kern nimmt
`min(Meldung, Freigabe)`. Die Kennung 1 ist unabhängig vom script-runner 2.

MCP `tools/call`: `name: run_agent`, arguments aus `run_agent_request.json`,
`Authorization: Bearer <runtime-key>` und die beiden Golden-Lauf-Header.
Das Lauf-Token steht ausschließlich in `X-Jnpt-Run-Token`; die ID muss mit
`arguments.run.id` übereinstimmen. Header werden auf Eindeutigkeit, Länge
und druckbare ASCII-Form geprüft. Die Beispielwerte `<lauf-token>` und
`<lauf-id>` bleiben gültige Testwerte; dieser Runner verlangt nicht das
UUID-/Token-Präfix des script-runners.

Unbekannte Argumentfelder, falsche Vertragskennung/Pfade, fehlende oder
mehrfache Lauf-Header, unpassende ID und ungültige Grenzen ergeben einen
festen Protokollfehler vor dem Lauf. Werkzeugaufrufe können auf 0 begrenzt
sein, Schritte, Wandzeit und Token-Budgets sind positiv. Körpergrenze 4 MiB.

## Modell und Werkzeuge

Die Operator-Adresse stammt aus `JNPT_GATEWAY_URL`; Anfrage-Pfade müssen
exakt `/api/plugin/agent-runner/mcp` und `/api/plugin/agent-runner/model`
sein. Beide Wege verwenden Bearer Lauf-Token. Es gibt keine Umleitung oder
Umgebungsproxy-Nutzung. Der runtime-key bleibt auf Weg A.

`model_request.json` ist eine akzeptierte Messages-Anfrage und wird im Test
verlustfrei dekodiert/enkodiert. Die produktive Anfrage ergänzt die explizit
geforderten Cache-Marken: System als Textblock mit `cache_control`, Marke
am letzten Werkzeug. Das Golden bleibt als gültiges Beispiel unverändert.

Oberste zulässige Felder: `messages`, `system`, `tools`, `tool_choice`,
`max_tokens`, `temperature`, `top_p`, `top_k`, `stop_sequences`, `thinking`,
`metadata`. Der Erzeuger setzt davon messages/system/tools/max_tokens;
**kein model, stream oder tool_choice**. Nachrichten haben nur role/content.
Erlaubte Blöcke: text, image, document, tool_use, tool_result, thinking,
redacted_thinking. Bilder/Dokumente benötigen base64-Quellen. Anbieter-
Werkzeuge werden abgewiesen. Signed thinking wird nicht umgeschrieben.

Werkzeugdefinitionen stammen von `tools/list`; nur deren Namen werden
aufgerufen. MCP-Text und base64-Bilder werden in `tool_result` projiziert,
strukturierte Ergebnisse als JSON-Text angehängt. Nicht darstellbare
Blöcke einschließlich URL-Bildern werden zu:

> [Werkzeug-Inhalt in diesem Modell-Durchgang nicht darstellbar.]

Modell-Antworten verwenden den Umschlag aus `model_response_ok.json`.
Die usage-Werte werden nicht lokal summiert. Alle assistant-Blöcke werden
angehängt; Werkzeugergebnisse folgen als eine neue user-Nachricht.

## Ende und neue Referenzdateien zur Übernahme

Erfolg entspricht `run_agent_response_ok.json`. Das SDK ergänzt die eigene
Server-Identität in `_meta`; der fachliche Golden-Test entfernt ausschließlich
`io.modelcontextprotocol/serverInfo` (wie der script-runner-Vertrag).
Ein zusätzlicher Test vergleicht den Ergebnis-Erzeuger ohne Transport exakt.

Diese **neuen Vorschläge** stehen separat in `proposed/`:

| Datei | Ende / Detail |
|---|---|
| `run_agent_response_limit.json` | limit / steps |
| `run_agent_response_provider.json` | error / provider |
| `run_agent_response_rejected.json` | error / rejected |
| `run_agent_response_cancelled.json` | cancelled |
| `run_agent_response_revoked.json` | revoked |
| `run_agent_response_error.json` | error / gateway |
| `run_log_ok.json` | Inhaltfreies Lauf-Protokoll, Dauer für Vergleich auf 0 gesetzt |

Fehler-Ergebnisse tragen `isError: true`, `steps` und optional `detail`.
Lokale Grenzen: steps, tool_calls, wall; Modellende max_tokens → tokens_out.
Grenzdetails des Kerns werden aus steps/tool_calls/wall/tokens_in/tokens_out
übernommen, unbekannte Details zu gateway. HTTP 401/403 auf beiden inneren
Wegen ergibt revoked; die Fehlerklassen cancelled/revoked ebenso. Unbekannte
Klassen, ungültiges JSON oder Transportfehler ergeben error/gateway.
`error.message` und provider_status werden nicht übernommen (AG8).

Die sechs Ende-Beispiele sind Vorschläge für den Kern, keine nachträgliche
Änderung seines gelieferten Vertrags. Eine Live-Abnahme steht aus.
