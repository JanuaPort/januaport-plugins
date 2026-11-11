# CLAUDE.md — agent-runner

## Verantwortung

Beisteller für die Schleife Modell ↔ Werkzeuge, JanuaPort/januaport#1099 B1,
Apache 2.0 wie das Repository. Ein statisches Go-Binary, ein Dienst.
Anthropic Messages und OpenAI Chat Completions sind die implementierten
Adapter seit 0.2.0, aktuell 0.2.1 (unveröffentlicht), JanuaPort/januaport#1193, Epic #1191.
Zeitpläne/Ticker gehören zu Release B. Betreiberanleitung: `README.md`.

OpenAI-Format braucht JanuaPort mit `model.protocol` (Version folgt); ein älterer Kern schickt kein Protokoll, dann gilt Anthropic.

## Aufbau

| Pfad | Aufgabe |
|---|---|
| `main.go` | HTTP auf 8090, Prozess-Abbruch, Healthcheck-Unterbefehl |
| `internal/runner/contract.go` | Eingang, Lauf-Header, feste Ende-Sätze je Klasse und je Grenz-/Fehlerdetail |
| `internal/runner/server.go` | MCP, runtime-key, genau `run_agent`, `/healthz` |
| `internal/runner/loop.go` | Historie, Grenzen, MCP-Sitzung, Werkzeugfreigabe und Wertung des letzten Werkzeug-Schritts |
| `internal/runner/gateway.go` | Modell-Durchgang, Lauf-Token, beschränkter HTTP-Transport |
| `internal/runner/conversation.go` | Protokollwahl, Adapter und angehängte Historie |
| `internal/openai` | Chat-Completions-Projektion, Prüfung und Werkzeugtexte |
| `internal/anthropic` | Messages-Projektion, Prüfung, Cache, Werkzeugergebnisse |
| `internal/runlog` | Inhaltfreies JSON-Protokoll auf stdout |
| `internal/hygiene` | Strenge Compose-Prüfung und Dockerfile-Gegenproben |
| `contract/golden` | Byte-identische Referenzdateien des Kerns, SHA256SUMS |
| `contract/proposed` | Neue Ende-Antworten und Lauf-Protokoll zur Kernübernahme |
| `probe/mutations.py`, `probe/openai_mutations.py` | Reproduzierbare Mutationen mit Wiederherstellung |
| `internal/hygiene/release_test.go` | Release-Auslöser, Rechte, SHA-Pins und Digest-Bindung |

## Draht-Vertrag

Die sieben ursprünglichen Kern-Dateien unter `contract/golden/` bleiben
byte-identisch. #1193 ergänzt dort drei OpenAI-Beispiele und erweitert
SHA256SUMS; der Test pinnt deren neuen Hash. Ende-Vorschläge bleiben in
`contract/proposed/`. #1228 ergänzt dort das neue OpenAI-Anfragebeispiel;
das ältere Kern-Golden mit `max_tokens` bleibt als Referenz byte-identisch.
Details: `contract/README.md`.

Weg A: `POST /mcp`, Bearer runtime-key. `tools/call run_agent` trägt
`X-Jnpt-Run-Token` und `X-Jnpt-Run-Id`; die ID stimmt mit `arguments.run.id`
überein. Der Token steht nicht in arguments. `model.protocol` ist optional: fehlt es,
gilt Anthropic; angegeben sind nur `anthropic` und `openai` zulässig. Leere,
null und unbekannte Werte scheitern vor dem Lauf mit festem Fehler.
`tools/list` meldet Contract 1, max_wall_s und
`protocols: ["anthropic","openai"]`. MCP-SDK 1.7.0, zustandslos,
JSON-Antworten, `PropagateRequestCancellation: true`. Aktueller Kern und Tests
sprechen MCP 2026-07-28 samt den SDK-Metadaten und Mcp-Method/Mcp-Name.

Weg B: pro Lauf eine MCP-Sitzung am festen Pfad
`/api/plugin/agent-runner/mcp`, zunächst `tools/list` (mit Pagination), danach
`tools/call`. Modell: POST `/api/plugin/agent-runner/model`. Beide mit Bearer
Lauf-Token an derselben Betreiberadresse `JNPT_GATEWAY_URL`.

## Invarianten und Gegenproben

| Invariante | Tragende Prüfung |
|---|---|
| R13: runtime-key je Anfrage frisch aus Ordner-Mount; fehlend/falsch/mehrfach → 401; Rotation wirkt unmittelbar | `TestAuthHeadersAndPathsAtAdapter`, `TestConfigAndKeyFailClosed` |
| Anfrage hat Contract 1, feste Gateway-Pfade, gültige Grenzen und passende eindeutige Header; unbekannte Argumente scheitern | `TestAuthHeadersAndPathsAtAdapter`, `TestMalformedArgumentsAtAdapter` |
| Modell nur über Kern; HTTP-Umleitungen und Umgebungsproxy gesperrt | `TestNoRedirectsOrProxyAtEitherGateway` |
| Anthropic Messages ohne model/stream, keine erzwungene Werkzeugwahl, nur erlaubte Nachrichten/Blöcke, Medien base64, keine Anbieter-Werkzeuge | `TestForbiddenModelPayloads`, `TestModelWireGuards`, `TestUnsafeResponseAndUnknownToolAtAdapter` |
| OpenAI nur messages/tools/max_completion_tokens; max_tokens allein und zusammen mit dem neuen Feld wird abgewiesen, Rollen system/user/assistant/tool, keine developer-Rolle, keine benannten Nachrichten oder Inhaltsteile; nur Funktionswerkzeuge und Text, tool-Ergebnisse mit tool_call_id; ungültige arguments werden lokaler Fehlertext | `TestRequestAndGolden`, `TestForbiddenRequests`, `TestRunnerVocabularyRejected`, `TestOpenAIOutgoingGuardsAtAdapter`, `TestCompletionTokenField`, `TestOpenAILoopTable` |
| Je Modellaufruf min(MaxTokensPerAnswer = 8192, Restbudget tokens_out); Verbrauch aus dem äußeren usage.tokens_out wird über alle Antworten summiert, bei Restbudget ≤ 0 kein weiterer Modellaufruf. OpenAI max_completion_tokens, Anthropic max_tokens; auch nach Werkzeugergebnissen. Kein automatischer Feldwechsel oder Wiederholungsaufruf bei HTTP 400 | `TestTokenBudgetAtAdapter`, `TestOpenAIUnknownCompletionTokensNoRetryAtAdapter` |
| Anthropic max_tokens und OpenAI length beenden auch bei positivem Restbudget als limit/tokens_out; gekürzte Inhalte und Werkzeugaufrufe werden verworfen. Der bestehende Vertrag hat nur feste Ende-Sätze, keinen zusätzlichen Freitext-Grund | `TestAnswerLimitStopAtAdapter`, `TestOpenAIFinishDetailsAtAdapter` |
| Fehlendes Protokoll erhält byte-identische Anthropic-Anfrage; unbekannte/ungültige Werte scheitern vor dem Lauf ohne Echo | `TestLegacyAnthropicRequestBytes`, `TestRunRequestGoldenRoundTrip`, `TestProtocolDefaultAndUnknownAtAdapter`, `TestInvalidProtocolValuesAtAdapter` |
| tool_use darf `caller` nur als `{"type":"direct"}` tragen (Anthropic seit 10/2026); jeder andere caller (Code-Ausführung) oder jedes weitere Feld scheitert; der Block bleibt unverändert in der Historie | `TestToolUseCaller`, `TestLoopToolUseCaller`, `TestLoopProgrammaticCallerRejected` |
| Thinking und redacted_thinking bleiben in der angehängten Historie erhalten | `TestLoopGoldenThinkingAndLog`, `TestThinkingAndCache` |
| Anthropic-Bilder nur base64 mit image/png, image/jpeg, image/gif oder image/webp; andere MCP-Bilder/URL-Bilder werden fester Platzhalter. OpenAI projiziert alle Nichttext-Blöcke zum Platzhalter | `TestImageMediaTypesAtAdapter`, `TestURLImageToolResultAtAdapter`, `TestToolResultSanitization`, `TestToolResultTextProjection` |
| error/rejected nennt unabhängig vom Feld die abgewiesene Läuferanfrage | `TestRejectedMessageAtAdapter`, `TestAllResultGoldens` |
| Antworten auf beiden Gateway-Pfaden höchstens 4 MiB, ohne Fristablauf abgewiesen | `TestResponseBody4MiBLimitAtAdapter` |
| AG8: Fehlertext und unbekannte Fehlerdetails gelangen nicht in Antwort/Protokoll | `TestGatewayErrorsFixed`, `TestLogErrorContentAbsent`, `TestRevocationAtBothGateways` |
| Werkzeugaufrufe nur aus tools/list; eindeutige IDs, gültige Namen, begrenzte Liste/Pagination | `TestUnsafeResponseAndUnknownToolAtAdapter`, `TestToolListAndMultiToolGuards`, `TestToolListSizeLimit` |
| Schritte/Aufrufe lokal begrenzt, Wandzeit = min(Anfrage, Meldung), Standard 300 s; Context-Abbruch beendet laufendes HTTP samt SDK-Cleanup, cancel vor Close | `TestLimitsAtAdapter`, `TestCancellationAndWallAtAdapter`, `TestAdvertisedWallCapEnforcedAtAdapter`, `TestCleanupWithinWallAtAdapter`, `TestCancelBeforeSessionCloseAtAdapter` |
| JanuaPort/januaport#1338: Endet das Modell, und im letzten Werkzeug-Schritt scheiterte mindestens ein Aufruf (`isError` im Werkzeug-Ergebnis oder der feste Fehler bei ungültigem JSON), endet der Lauf `error/tools`. Ein Fehler, nach dem ein Schritt ohne Fehler folgt (Erholung), und ein Lauf ohne Aufruf bleiben `ok`. Die Wertung liest nur `isError`, nie Inhalte, und kommt nie vom Modell; eine Grenze oder ein anderes Ende behält seine Klasse | `TestToolFailureEndsRunAtAdapter`, `TestToolFailureDoesNotOverrideLimit`, `TestOpenAILoopTable`, `TestAllResultGoldens` |
| JanuaPort/januaport#1335: Die feste Meldung nennt bei `limit` die Grenze (steps, tool_calls, tokens_in, tokens_out, wall, content_filter) und bei `error/tools` den Werkzeug-Fehler; unbekanntes Detail behält den allgemeinen Satz, das Detail steht nie wörtlich im Satz | `TestFixedMessagePerDetail`, `TestLimitSentencesAtAdapter`, `TestAllResultGoldens` |
| Lauf-Protokoll enthält nur Schritte, Anzahl und Namen der Werkzeuge, Ende-Klasse, optional festes Ende-Detail, Dauer | `TestLoopGoldenThinkingAndLog` gegen `run_log_ok.json`, `TestOpenAIFinishDetailsAtAdapter` |
| OpenAI content_filter endet limit/content_filter, unterscheidbar von length (limit/tokens_out) und error; keine Ausgabe gefilterter Inhalte und keine Werkzeugausführung | `TestOpenAIFinishDetailsAtAdapter` |
| #1069: genau internes jnpt_plugins, kein Host-Port, kein zusätzliches Ausgangsnetz | `TestCompose`, `TestComposeForbiddenPaths` |
| Distroless, statisch, UID 65532, read-only, cap_drop ALL, no-new-privileges, nur Schlüsselordner ro | `TestDockerNonRootStatic`, `TestComposeForbiddenPaths` |

## Entscheidungen und Grenzen

- `external: true` in Compose bezeichnet das vom Kern verwaltete Netz. Der
  Kern muss es mit `internal: true` anlegen. Das ist dasselbe Modell wie beim
  script-runner, kein externes Ausgangsnetz.
- Der script-runner enthält Python/Git; dieses Image nutzt distroless/static.
  Der Healthcheck wird vom eigenen Binary ausgeführt.
- Nur Anthropic: Cache-Breakpoint am letzten Werkzeug (deckt die Werkzeugliste ab) und am
  System; damit bleiben auch große Werkzeuglisten unter vier Breakpoints.
- Je Modellaufruf `min(MaxTokensPerAnswer, Restbudget tokens_out)`.
  `MaxTokensPerAnswer = 8192` steht in `internal/runner/conversation.go`:
  achtmal mehr Raum als die frühere Grenze 1024 für längere Antworten,
  bei weiterhin begrenzter einzelner Modellantwort. Das ist eine lokale
  Läufergrenze, keine behauptete Anbietergrenze. OpenAI sendet ausschließlich
  `max_completion_tokens`, Anthropic weiterhin `max_tokens`. `loop.go` zieht
  das äußere `usage.tokens_out` jeder Antwort vom Profil-`limits.tokens_out`
  ab, auch nach Werkzeugaufrufen; bei Restbudget ≤ 0 endet der Lauf vor dem
  nächsten Modellaufruf als `limit/tokens_out`. Fehlender Verbrauch zählt als
  0; negative Werte erhöhen das Budget nicht. Der Kern prüft weiterhin die
  tatsächlichen Token-Summen und alle Profilbudgets, auch Cache-Tokens. Ein unbekanntes
  Feld beim Anbieter führt im Lauf zum Fehler, ohne Wiederholung mit `max_tokens`.
  `max_tokens` als interner Stop-Grund (auch für OpenAI-`length`) bleibt bestehen.
  Diese Stoppgründe enden weiterhin als `limit/tokens_out`, auch wenn das
  Laufbudget noch nicht verbraucht ist. Der feste Ende-Satz unterscheidet
  Antwortgrenze und Laufbudget nicht; Contract 1 hat keinen Freitext-Grund.
  Anbieterquellen und ihre Grenzen: `contract/README.md`.
- 4 MiB je eingehender HTTP-Anfrage und je HTTP-Antwort auf beiden
  Gateway-Pfaden (Bearer-Transport), 1024 Werkzeugdefinitionen und
  höchstens 1024 unterschiedliche Folgeseiten sind Protokollschutz.
- Die Erfolgsausgabe besteht aus finalem Text; Anthropic-Thinking steht nur in
  der Modellhistorie. Fehler-Antworten verwenden die feste Satzliste.
- Werkzeug-Fehler (JanuaPort/januaport#1338): Sie gehen weiter als Daten an das
  Modell, das sich erholen darf. Der Läufer wertet allein das Ende: Maßgeblich ist
  nur der letzte Werkzeug-Schritt (Merker `lastStepFailed` in `loop.go`), ein Schritt
  mit zwei Aufrufen gilt als gescheitert, sobald einer scheiterte. Bewusst
  hingenommen: Falsch-Rot (das Modell fragt zuletzt etwas ab, bekommt „nicht
  gefunden“ als Fehler und antwortet richtig; der Lauf wird rot, sichtbar und
  wiederholbar) und Rest-Grün (ein früher gescheiterter Schreibaufruf mit danach
  erfolgreichem Lesen, ebenso ein Lauf ohne Aufruf bleibt `ok`; das erkennt nur eine
  Inhaltsprüfung, die es nicht gibt). Wie bei jedem Ende außer `ok` ersetzt der feste
  Satz den Modelltext. Contract 1 und die Ende-Klassen bleiben; ein älterer Kern
  kennt das Detail `tools` nicht, behält aber das Ende `error`, also nie Grün.
- Bei abgerissenem Relay kann die HTTP-Endantwort den Aufrufer nicht mehr
  erreichen. Der Lauf endet trotzdem, das Protokoll meldet `cancelled`.
- SDK-Zweitweg: Der ausgehende MCP-Transport löst seine Verbindung vom
  Connect-Kontext (`xcontext.Detach`). Deshalb bindet der Bearer-Transport
  jede Anfrage samt Antwort-Body zusätzlich an den Lauf-Kontext. Vor
  `session.Close()` wird dieser abgebrochen; ein hängendes Session-DELETE
  verlängert den Lauf dadurch nicht. Eingehend bleibt zusätzlich
  `PropagateRequestCancellation` aktiviert.
  H1 prüft die Reihenfolge ohne Uhr: Ein erfolgreicher Lauf darf kein
  Session-DELETE zum Kern senden. H2 liest übergroße Antworten auf beiden
  Pfaden ohne Lauf-Frist und verlangt den Größenfehler bei 4 MiB.
- Drei Prüfstellen begrenzen Anfragekörper auf 4 MiB: HTTP-Wrapper,
  explizites SDK-`MaxRequestBodyBytes` und Argumentparser. Die Größenmutation
  hebt alle drei gleichzeitig an; sonst würde die zweite Linie sie verdecken.
- Keine Speicherung von Historie oder Protokoll im Container-Dateisystem.
  stdout wird vom Betreiber/Docker verwaltet. Ein Lauf bekommt eine JSON-Zeile.

## Prüfen

Release B1.1: `.github/workflows/agent-runner-release.yml`, nur
`agent-runner-vX.Y.Z`, ein Build `linux/amd64` mit SBOM und Provenance,
Push per Digest ohne Tag → Config-Digest-Vergleich (SEC C1) und
`pwsh`/`probe/smoke.ps1` gegen genau diesen Digest → Tag `X.Y.Z`.
Die vorhandene PowerShell-Sonde bleibt die einzige Implementierung;
Ubuntu hat `pwsh`. Sie prüft die Isolation mit einer erreichbaren
Positivkontrolle im fremden Netz. `probe/.smoke/` ist ignoriert.
Nur `build`/`publish` haben `packages: write`, dort nur `actions/*`/`docker/*`;
alle Aktions-SHAs entsprechen `script-runner-release.yml` (SEC C2).
Trivy als gepinntes Binary im lesenden Job: Bericht als Artefakt, kein Gate.
Das Gate füllt die Kuratierungsvorlage unter 0.1.0 in `CHANGELOG.md` nach
dem Release mit Digest, Lauf-ID, Scan und geprüfter JanuaPort v0.80.0 aus.
Der Hygiene-Test benötigt den ganzen Checkout samt `.github/workflows/`.
Prüfberichte bleiben außerhalb des öffentlichen Repositories.

```sh
# Im Modul, Go 1.26.10 (lokal oder im Go-Container)
go vet ./...
go test ./...
go test -race -count=1 ./...
golangci-lint run
docker build -t agent-runner:test .
```

Mutationen nur bei exklusivem Zugriff auf diesen Worktree ausführen;
`probe/mutations.py` und `probe/openai_mutations.py` ändern Dateien
vorübergehend und setzen sie byte-genau
zurück. Der Abschlussbericht hält Ergebnisse und verbleibende Grenzen fest.
