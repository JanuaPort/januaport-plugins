# Draht-Vertrag — agent-runner 0.2.0 (Contract 1)

Maßgeblich: `cmd/jnpt/testdata/agent-runner-contract/` des Kerns, geliefert
für JanuaPort/januaport#1099. Die sieben JSON-Dateien und SHA256SUMS wurden
byte-identisch nach `golden/` kopiert. `.gitattributes` deaktiviert für diese
Dateien die Zeilenenden-Konvertierung. Der Test pinnt auch den Hash der
erweiterten SHA256SUMS. #1193 ergänzt `model_request_openai.json`,
`model_response_openai_ok.json` und `run_agent_request_openai.json`;
die sieben ursprünglichen JSON-Dateien bleiben unverändert.

## Eingang und Meldung

MCP `tools/list`: genau ein Werkzeug `run_agent`; Ergebnis-Metadaten
`_meta["januaport.ai/runner"] = {contract: 1, max_wall_s: 300, protocols: ["anthropic","openai"]}` im Standard.
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

`arguments.model.protocol` ist optional. Fehlt das Feld, gilt `anthropic`;
angegeben sind genau `anthropic` und `openai` erlaubt. Unbekannte, leere,
null oder anders typisierte Werte ergeben denselben festen Protokollfehler
vor jedem Lauf und Modellaufruf, ohne Echo des Werts. `protocol` wird bei
fehlendem Feld auch beim Enkodieren ausgelassen: das alte Golden bleibt gleich.

OpenAI-Format braucht JanuaPort mit `model.protocol` (Version folgt); ein älterer Kern schickt kein Protokoll, dann gilt Anthropic.

## Modell und Werkzeuge

Die Operator-Adresse stammt aus `JNPT_GATEWAY_URL`; Anfrage-Pfade müssen
exakt `/api/plugin/agent-runner/mcp` und `/api/plugin/agent-runner/model`
sein. Beide Wege verwenden Bearer Lauf-Token. Es gibt keine Umleitung oder
Umgebungsproxy-Nutzung. Der runtime-key bleibt auf Weg A.

`model_request.json` ist eine akzeptierte Messages-Anfrage und wird im Test
verlustfrei dekodiert/enkodiert. Die produktive Anfrage ergänzt die explizit
geforderten Cache-Marken: System als Textblock mit `cache_control`, Marke
am letzten Werkzeug. Das Golden bleibt als gültiges Beispiel unverändert.

Anthropic — oberste zulässige Felder: `messages`, `system`, `tools`, `tool_choice`,
`max_tokens`, `temperature`, `top_p`, `top_k`, `stop_sequences`, `thinking`,
`metadata`. Der Erzeuger setzt davon messages/system/tools/max_tokens;
**kein model, stream oder tool_choice**. Nachrichten haben nur role/content.
Erlaubte Blöcke: text, image, document, tool_use, tool_result, thinking,
redacted_thinking. Bilder/Dokumente benötigen base64-Quellen. Bilder erlauben
nur `media_type` `image/png`, `image/jpeg`, `image/gif` oder `image/webp`,
auch in verschachtelten Werkzeugergebnissen und Modellantworten. Anbieter-
Werkzeuge werden abgewiesen. Signed thinking wird nicht umgeschrieben.

Werkzeugdefinitionen stammen von `tools/list`; nur deren Namen werden
aufgerufen. MCP-Text und base64-Bilder mit einem dieser vier Medientypen
(`mimeType` in MCP, `media_type` in Messages) werden in `tool_result` projiziert,
strukturierte Ergebnisse als JSON-Text angehängt. Nicht darstellbare
Blöcke einschließlich URL-Bildern und Bildern mit anderem Medientyp werden zu:

> [Werkzeug-Inhalt in diesem Modell-Durchgang nicht darstellbar.]

Modell-Antworten verwenden den Umschlag aus `model_response_ok.json`.
Die usage-Werte werden nicht lokal summiert. Alle assistant-Blöcke werden
angehängt; Werkzeugergebnisse folgen als eine neue user-Nachricht.

### OpenAI Chat Completions

`model_request_openai.json` zeigt `messages` mit zuerst einer `system`- und
einer `user`-Nachricht, `tools` als `type: function` mit
`function.{name,description,parameters}` und `max_tokens`. Der Läufer setzt
weder `model` noch `stream` noch `tool_choice`; der Kern besitzt model/stream.
Die Kern-Allowlist lässt außerdem `max_completion_tokens`, `temperature`,
`top_p`, `stop` sowie `tool_choice: auto|none` zu
(`internal/modelgate/request.go`, `content.go`). Rollen dort:
system/developer/user/assistant/tool; Nachrichtenfelder:
role/content/name/tool_calls/tool_call_id. Die eigene Läuferprüfung ist enger:
nur `messages`, `tools`, `max_tokens`; nur Rollen system/user/assistant/tool,
Nachrichtenfelder role/content/tool_calls/tool_call_id und Text statt Inhaltsteilen.
`developer`, Bild-/Textteile und andere zusätzliche Kern-Felder werden abgewiesen.

`model_response_openai_ok.json` zeigt den unveränderten Gateway-Umschlag:
`response.choices[0].message`, `finish_reason` am ersten Choice. Der Läufer
übernimmt den Text und Funktionsaufrufe, projiziert nur die erlaubten
Nachrichtenfelder und hängt die Assistant-Nachricht an. Provider-Metadaten
bleiben außerhalb der Historie. Funktionsargumente sind JSON-Strings.
Werkzeugergebnisse folgen als einzelne `role: tool`-Nachrichten mit passender
`tool_call_id`. Text und `structuredContent` werden als Text übertragen;
andere MCP-Blöcke werden zum oben genannten festen Platzhalter.

Bei syntaktisch ungültigen oder nicht objektförmigen `arguments` erfolgt
kein MCP-Werkzeugaufruf. Die korrelierte tool-Nachricht enthält den festen
Text „Werkzeug-Eingabe ist kein gültiges JSON-Objekt.“; der Aufrufversuch
zählt lokal gegen das Werkzeugbudget. Das entspricht der Fehlerergebnis-
Semantik von MCP, ohne ein im OpenAI-Vertrag unbekanntes `is_error`-Feld.
Im Anthropic-Pfad weist die vorhandene Inhaltsprüfung ungültiges `input`
bereits beim Lesen der Modellantwort ab.

`stop` ohne Werkzeugaufrufe beendet mit `ok`; `tool_calls` führt die
aufgelisteten Werkzeuge aus; `length` ergibt `limit/tokens_out`. Leere
`choices`, unbekannte Stop-Gründe und unzulässige Nachrichten ergeben
`error/gateway`. `content_filter` nutzt die bestehende Ende-Klasse `limit`
mit dem Detail `content_filter` in `_meta["januaport.ai/run"]` und im Lauf-Log;
Text und Werkzeugaufrufe einer solchen Antwort werden verworfen. Contract 1
und die Ende-Klassen bleiben unverändert. Der aktuelle Kern übernimmt nur
seine bekannten Grenzdetails; `content_filter` bleibt daher im Runner-Ergebnis
und Runner-Log unterscheidbar, wird vom Kern-Audit noch nicht übernommen.
Werkzeugfreigabe, ID-Eindeutigkeit, Grenzen, Fehlerklassen
und Lauf-Protokoll sind für beide Adapter dieselben.
Der Kern liest Nutzung aus `response.usage.prompt_tokens` und
`completion_tokens` der Anbieterantwort und gibt sie im äußeren `usage`
als `tokens_in`/`tokens_out` weiter; der Läufer summiert sie nicht.

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
Lokale Grenzen: steps, tool_calls, wall; Modellende max_tokens (Anthropic) bzw. length (OpenAI) → tokens_out.
Grenzdetails des Kerns werden aus steps/tool_calls/wall/tokens_in/tokens_out
übernommen, unbekannte Details zu gateway. HTTP 401/403 auf beiden inneren
Wegen ergibt revoked; die Fehlerklassen cancelled/revoked ebenso. Unbekannte
Klassen, ungültiges JSON oder Transportfehler ergeben error/gateway.
`error.message` und provider_status werden nicht übernommen (AG8).
Für `error/rejected` gilt unabhängig vom abgewiesenen Feld der feste Satz:
„Der Modell-Durchgang hat die Anfrage des Agenten-Läufers abgewiesen.“ Das
unveränderliche Kern-Golden `model_error_rejected.json` enthält weiterhin
den alten Durchgangstext; dieser wird verworfen, nicht als Läuferantwort benutzt.

Beide ausgehenden HTTP-Pfade begrenzen Antwortkörper im Bearer-Transport auf
4 MiB. Diese Sperre gilt beim Lesen und benötigt keinen Fristablauf. Vor
`session.Close()` wird der Lauf-Kontext abgebrochen, auch nach erfolgreichem
Lauf; dadurch endet das vom SDK abgekoppelte Cleanup innerhalb des Laufs.

Das Lauf-Log enthält zusätzlich optional `detail`: ausschließlich das feste,
bereits klassifizierte Ende-Detail, keine Anbietertexte oder unbekannten Werte.
Bei Erfolg fehlt das Feld; `run_log_ok.json` bleibt byte-identisch.

Die sechs Ende-Beispiele sind Vorschläge für den Kern, keine nachträgliche
Änderung seines gelieferten Vertrags. Eine Live-Abnahme steht aus.
