# CLAUDE.md — agent-runner

## Verantwortung

Beisteller für die Schleife Modell ↔ Werkzeuge, JanuaPort/januaport#1099 B1,
Apache 2.0 wie das Repository. Ein statisches Go-Binary, ein Dienst.
Anthropic Messages ist der implementierte Adapter. OpenAI gehört zu B2;
Zeitpläne/Ticker zu Release B. Betreiberanleitung: `README.md`.

## Aufbau

| Pfad | Aufgabe |
|---|---|
| `main.go` | HTTP auf 8090, Prozess-Abbruch, Healthcheck-Unterbefehl |
| `internal/runner/contract.go` | Eingang, Lauf-Header, feste Ende-Sätze |
| `internal/runner/server.go` | MCP, runtime-key, genau `run_agent`, `/healthz` |
| `internal/runner/loop.go` | Historie, Grenzen, MCP-Sitzung und Werkzeugfreigabe |
| `internal/runner/gateway.go` | Modell-Durchgang, Lauf-Token, beschränkter HTTP-Transport |
| `internal/anthropic` | Messages-Projektion, Prüfung, Cache, Werkzeugergebnisse |
| `internal/runlog` | Inhaltfreies JSON-Protokoll auf stdout |
| `internal/hygiene` | Strenge Compose-Prüfung und Dockerfile-Gegenproben |
| `contract/golden` | Byte-identische Referenzdateien des Kerns, SHA256SUMS |
| `contract/proposed` | Neue Ende-Antworten und Lauf-Protokoll zur Kernübernahme |
| `probe/mutations.py` | Reproduzierbare Mutationen mit Wiederherstellung |
| `internal/hygiene/release_test.go` | Release-Auslöser, Rechte, SHA-Pins und Digest-Bindung |

## Draht-Vertrag

Maßgeblich sind die sieben Kern-Dateien unter `contract/golden/`; sie werden
nicht regeneriert. Der Test pinnt zusätzlich den Hash ihrer SHA256SUMS.
Neue Beispiele gehören nach `contract/proposed/`, nicht zu den Kern-Goldens.
Details: `contract/README.md`.

Weg A: `POST /mcp`, Bearer runtime-key. `tools/call run_agent` trägt
`X-Jnpt-Run-Token` und `X-Jnpt-Run-Id`; die ID stimmt mit `arguments.run.id`
überein. Der Token steht nicht in arguments. MCP-SDK 1.7.0, zustandslos,
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
| Messages ohne model/stream, keine erzwungene Werkzeugwahl, nur erlaubte Nachrichten/Blöcke, Medien base64, keine Anbieter-Werkzeuge | `TestForbiddenModelPayloads`, `TestModelWireGuards`, `TestUnsafeResponseAndUnknownToolAtAdapter` |
| tool_use darf `caller` nur als `{"type":"direct"}` tragen (Anthropic seit 10/2026); jeder andere caller (Code-Ausführung) oder jedes weitere Feld scheitert; der Block bleibt unverändert in der Historie | `TestToolUseCaller`, `TestLoopToolUseCaller`, `TestLoopProgrammaticCallerRejected` |
| Thinking und redacted_thinking bleiben in der angehängten Historie erhalten | `TestLoopGoldenThinkingAndLog`, `TestThinkingAndCache` |
| Bilder nur base64 mit image/png, image/jpeg, image/gif oder image/webp; andere MCP-Bilder/URL-Bilder werden fester Platzhalter | `TestImageMediaTypesAtAdapter`, `TestURLImageToolResultAtAdapter`, `TestToolResultSanitization` |
| error/rejected nennt unabhängig vom Feld die abgewiesene Läuferanfrage | `TestRejectedMessageAtAdapter`, `TestAllResultGoldens` |
| Antworten auf beiden Gateway-Pfaden höchstens 4 MiB, ohne Fristablauf abgewiesen | `TestResponseBody4MiBLimitAtAdapter` |
| AG8: Fehlertext und unbekannte Fehlerdetails gelangen nicht in Antwort/Protokoll | `TestGatewayErrorsFixed`, `TestLogErrorContentAbsent`, `TestRevocationAtBothGateways` |
| Werkzeugaufrufe nur aus tools/list; eindeutige IDs, gültige Namen, begrenzte Liste/Pagination | `TestUnsafeResponseAndUnknownToolAtAdapter`, `TestToolListAndMultiToolGuards`, `TestToolListSizeLimit` |
| Schritte/Aufrufe lokal begrenzt, Wandzeit = min(Anfrage, Meldung), Standard 300 s; Context-Abbruch beendet laufendes HTTP samt SDK-Cleanup, cancel vor Close | `TestLimitsAtAdapter`, `TestCancellationAndWallAtAdapter`, `TestAdvertisedWallCapEnforcedAtAdapter`, `TestCleanupWithinWallAtAdapter`, `TestCancelBeforeSessionCloseAtAdapter` |
| Lauf-Protokoll enthält nur Schritte, Anzahl und Namen der Werkzeuge, Ende-Klasse, Dauer | `TestLoopGoldenThinkingAndLog` gegen `run_log_ok.json` |
| #1069: genau internes jnpt_plugins, kein Host-Port, kein zusätzliches Ausgangsnetz | `TestCompose`, `TestComposeForbiddenPaths` |
| Distroless, statisch, UID 65532, read-only, cap_drop ALL, no-new-privileges, nur Schlüsselordner ro | `TestDockerNonRootStatic`, `TestComposeForbiddenPaths` |

## Entscheidungen und Grenzen

- `external: true` in Compose bezeichnet das vom Kern verwaltete Netz. Der
  Kern muss es mit `internal: true` anlegen. Das ist dasselbe Modell wie beim
  script-runner, kein externes Ausgangsnetz.
- Der script-runner enthält Python/Git; dieses Image nutzt distroless/static.
  Der Healthcheck wird vom eigenen Binary ausgeführt.
- Cache-Breakpoint am letzten Werkzeug (deckt die Werkzeugliste ab) und am
  System; damit bleiben auch große Werkzeuglisten unter vier Breakpoints.
- `max_tokens = min(1024, limits.tokens_out)` je Modellaufruf. Die tatsächlichen
  Token-Summen prüft ausschließlich der Kern, auch Cache-Tokens.
- 4 MiB je eingehender HTTP-Anfrage und je HTTP-Antwort auf beiden
  Gateway-Pfaden (Bearer-Transport), 1024 Werkzeugdefinitionen und
  höchstens 1024 unterschiedliche Folgeseiten sind Protokollschutz.
- Die Erfolgsausgabe besteht aus finalen Textblöcken; Thinking steht nur in
  der Modellhistorie. Fehler-Antworten verwenden die feste Satzliste.
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
`probe/mutations.py` ändert Dateien vorübergehend und setzt sie byte-genau
zurück. Der Abschlussbericht hält Ergebnisse und verbleibende Grenzen fest.
