# B1-Abschlussbericht — JanuaPort/januaport#1099

Stand 17.10.2026. Gebaut, Live-Durchstich ausstehend. Ausschließlich im
zugewiesenen Worktree auf `feat/1099-b1-agent-runner` gearbeitet.
Kein Push, PR, Merge, CI-Dispatch oder Live-Modellaufruf.

## Commits und rot vor grün

| Commit | Inhalt / Beleg |
|---|---|
| `9a05ed6` | Tests zuerst, unveränderte Kern-Goldens. `go test ./...` rot: fehlende Runner-/Adapter-Implementierung und Betriebsdateien. Dies war bewusst ein Build-/Dateifehler, noch kein Verhaltensfehler; `evidence/red.txt`. |
| `19ea160` | Implementierung, zusätzliche Negativtests, Betriebsdateien und Pflichtdokumentation; abschließend alle Prüfungen grün. |
| Folgecommit dieses Berichts | Konsolidierter Nachweis, Hashes und Fundstellen; endgültiger Kopf-SHA in der Übergabe. |

Zusätzliche rote Verhaltensnachweise vor der jeweiligen Korrektur:

- `evidence/red-hardening.txt`: 401/403 am Modell- und MCP-Durchgang wurden
  zunächst als error statt revoked behandelt. Danach mit frischem Entzugsmerker
  je Lauf korrigiert. Der Zwischenstand dokumentiert auch die anschließend
  korrigierte Testannahme, nach HTTP-Abbruch müsse noch eine Antwort ankommen.
- `evidence/red-cleanup.txt`: Ein hängendes Session-DELETE brauchte 2,01 s bei
  1 s Laufgrenze. Ursache: Das MCP-SDK löst den Verbindungskontext mit
  `xcontext.Detach` vom Connect-Kontext. Der Transport bindet jetzt jede
  Anfrage und deren Body an den Lauf; vor `session.Close()` wird abgebrochen.

Die Kern-Goldens wurden weder für die Implementierung noch für Mutationen
verändert. Alle acht gelieferten Dateien inklusive SHA256SUMS wurden am Ende
erneut gegen die gelieferte Kopie geprüft: byte-identisch per SHA-256.

## Ergebnis und Verifikation

| Prüfung | Ergebnis |
|---|---|
| `go vet ./...` | PASS, `evidence/vet.txt` |
| `go test ./... -count=1` | PASS, `evidence/green.txt` |
| `go test -race ./... -count=1` | PASS, `evidence/race.txt` |
| golangci-lint 2.9.0 | 0 Befunde, `evidence/lint.txt` |
| Docker-Build | PASS, `evidence/docker-build.txt` |
| Laufzeitsonde des gebauten Images | PASS, `evidence/docker-smoke.txt` |
| Mutationen | 47/47 rot erkannt, danach Änderungen jeweils byte-genau zurückgesetzt |
| `git diff --check` | PASS |

Go-Prüfungen liefen unter Linux im Container `golang:1.26.9`, Lint im
repoüblichen Container `golangci/golangci-lint:v2.9.0`. Das lokal installierte
Go 1.24.0 und der mit Go 1.25 gebaute Linter 2.5.0 passen nicht zum Modul.

Gebautes lokales Image: `jnpt1099-b1-agent-runner:test`, Image-ID
`sha256:bfce169e58ee5b64c9bbac099f5b87dfa990f45125cd5b61b9922d682004964c`.
Die Sonde startet genau dieses Image mit UID 65532, read-only Root-FS und
einem eigenen internen Testnetz. Healthcheck erfolgreich, genau ein Netz,
keine Host-Ports. Ein lokaler HTTP-Testserver auf einem separaten Netz war
aus dem Runner-Netz nicht erreichbar; eine Gegenprobe in dessen Netz war
erfolgreich. Die Sonde prüft damit die Netztrennung ohne Internetanfragen.
Die ausgelieferte Compose wird zusätzlich strukturell streng geprüft.

Die HTTP-Tests verwenden den echten MCP-Server und echten Anthropic-Adapter
gegen einen `httptest`-Kern: vier Modellschritte, drei tools/call-Aufrufe,
korrelierte tool_results, Thinking/Redacted-Thinking, Cache-Marken,
Schritt-/Aufruf-/Zeitgrenzen, Entzug, Abbruch und inhaltfreies Protokoll.
Das ist kein Nachweis eines Live-Durchstichs durch den echten Kern.

## Pflicht-Mutationen und weitere Sperren

| Pflichtmutation | Ausgelöster Nachweis |
|---|---|
| `model` in Request-Struct geschrieben | `TestThinkingAndCache`, `TestLoopGoldenThinkingAndLog` rot |
| `error.message` eingelesen und als Fehlertext durchgereicht | `TestGatewayErrorsFixed` rot; synthetische Kanarie wird erkannt |
| Eingabe vor der JSON-Zeile ins Lauf-Protokoll geschrieben | `TestLoopGoldenThinkingAndLog` rot |
| Dienst zusätzlich ans Netz `external` gehängt | `TestCompose` rot |

`external: true` am vorhandenen Netz `plugins` ist absichtlich unverändert:
es bezeichnet den Besitz beim Kern, der `jnpt_plugins` mit `internal: true`
anlegt. Die Mutation fügt ein zweites, ausgangsfähiges Netz hinzu.

Jede Mutation verändert produktiven Code oder die Betriebsdatei und führt
die einschlägigen Tests aus; Buildfehler werden vom Mutationsskript **nicht**
als Nachweis gezählt. Tests aktivieren die verbotenen Eingaben tatsächlich.
Allowlist-Prüfungen laufen im echten Adapter, Lauf-/Auth-/Kontextprüfungen
am HTTP/MCP-Adapter; gemockt wird nur der Kern. Bei gelöster Wandzeit oder
Pagination beendet die Testfrist den absichtlich hängenden Mutanten.

Zwei anfänglich überlebende Schlüsseldatei-Mutanten zeigten eine Schwäche
der Gegenprobe: Ein unpassender Bearer wurde auch bei aufgehobener
Dateiprüfung abgelehnt. Der Test prüft jetzt zusätzlich, dass der echte
Health-Adapter einen ungültigen Schlüssel nicht als bereit meldet. Die
erneuten Mutationen sind rot. Bei der Anfragegröße maskierte die zweite
Prüfung im SDK zunächst das Entfernen der eigenen Sperre: Die abschließende
Mutation hebt HTTP-Wrapper, SDK-Grenze und Argumentgrenze gemeinsam an.

Die ausführlichen Ausgaben stehen in `evidence/mutations.txt` (46) und
`evidence/mutations-additional.txt` (SDK-Cleanup). Große Testausgaben sind
im Bericht gekürzt; Status und auslösender Test bleiben sichtbar. Das
Skript `probe/mutations.py` reproduziert die Proben und stellt Dateien im
`finally` wieder her. Vollständige Liste folgt unten.

## Pflichtdokumentation

- [x] `agent-runner/CLAUDE.md`: Verantwortung, Aufbau, Vertrag, Invarianten,
  Tests, SDK-Zweitwege und Betriebsentscheidungen.
- [x] `agent-runner/README.md`: Start, Envs, Schlüssel, internes Netz,
  Gesundheit und Lauf-Protokoll.
- [x] `agent-runner/contract/README.md`: Original-Goldens, Cache-Ergänzung,
  Ende-Klassen, neue Referenzdateien und Transportmetadaten.
- [x] `agent-runner/CHANGELOG.md`: 0.1.0, „Gebaut, Live-Durchstich ausstehend“.
- [x] `README.md:38` und `README.de.md:37`: Zeile in der Beisteller-Liste.

## Invarianten: repo-weite Suche und weitere Wege

Gesucht wurden sowohl die Invariantentexte als auch alternative Wege:
`jnpt_plugins`, `runtime-key`, `PropagateRequestCancellation`, `tool_choice`,
`januaport.ai/runner`, `Modell-Schlüssel`, `Lauf-Protokoll`, `CheckRedirect`,
`.Connect(`, `RoundTrip`, `MaxRequestBodyBytes`, `networks:`, `network_mode:`,
`distroless`. Die Fundstellen stehen vollständig in `evidence/invariants.txt`
und als Datei:Zeile-Liste unten. Es wurden keine anderen Module verändert.

| Invariante / weiterer Weg | Fundstellen und Konsequenz |
|---|---|
| Netzbesitz und interne Anlage | `box/docker-compose.box.yml:329`, `:340`, `:341`; Kernnetz ist internal. `script-runner/docker-compose.yml:153`, `script-runner/README.md:259`, `script-runner/CLAUDE.md:304`: vorhandenes Muster bestätigt. Neu `agent-runner/docker-compose.yml:16`, `:23`, Betreiber- und Vertragsdoku. |
| Zweite Netzwege | `script-runner/internal/hygiene/hygiene.go:237`, `:245`, `:334`; `script-runner/internal/hygiene/hygiene_test.go:34`, `:134`. Neu `agent-runner/internal/hygiene/hygiene_test.go:44`, `:83`: unbekannte Compose-Schlüssel, network_mode, ports, extra_hosts, weitere Netze und Mounts werden negativ geprüft. |
| R13 / beide Bearer-Wege | `script-runner/internal/mediator/mediator.go:155`, `:193`, `script-runner/internal/mediator/run.go:293`, `:302`; `script-runner/internal/mediator/schedules.go:120` ist ein separater runtime-key-Weg. Neu `agent-runner/internal/runner/server.go:48`, `:64`, `gateway.go:22`, `contract.go:49`: runtime-key nur Eingang, Lauf-Token beide inneren Wege. |
| Context / SDK-Zweitweg | `script-runner/internal/mediator/mediator.go:167`, `run.go:334`. Neu `agent-runner/internal/runner/loop.go:19`, `gateway.go:22`, `server.go:85`; Test `hardening_test.go:286`. Zusätzlich SDK 1.7.0 `mcp/streamable.go:2031`, `:2722`: abgekoppelter Verbindungskontext/Close, durch Transport-Scope geschlossen. |
| Größenlimit / dritte Linie | `agent-runner/internal/runner/server.go:85`, `:101`, `contract.go:49`, `gateway.go:58`; SDK `mcp/streamable.go:198`, `:225`, `:344`. Alle drei Eingangsgrenzen explizit auf 4 MiB; kombinierte Mutation. |
| Messages / alle Kompositionsstellen | `agent-runner/internal/anthropic/adapter.go:32`, `:52`, `:92`, `:145`; `internal/runner/gateway.go:78`, `loop.go:76`. Validierung am Sender und an Antwortblöcken, sichere tool_result-Projektion und append-only Historie. |
| Fehler und Protokoll | `script-runner/internal/mediator/log_test.go:16`, `:83`, `script-runner/internal/runlog/buffer.go:1` sind das Muster; dessen expliziter Skript-Textkanal ist für Agenten nicht passend. Neu `agent-runner/internal/runlog/log.go:10`, `:28`, `internal/runner/contract.go:76`, `gateway.go:94`: ausschließlich Metadaten, feste Sätze. |
| Wandzeitmeldung | `agent-runner/internal/runner/server.go:70`, `contract.go:17`, `loop.go:19`; Tests `runner_test.go:330`, `:351`, `:442`, `hardening_test.go:189`. Meldung und effektive Kappe aus derselben Konfiguration. |
| Betrieb / zweites Image-Muster | `script-runner/Dockerfile:11` (statischer Go-Build, danach Python-Laufzeit), `ebics-abholer/Dockerfile:73` (UID 65532). Neu `agent-runner/Dockerfile:1`, `:7`, `:9`, `:11`: statisches Binary in distroless/static, Non-Root. |

## Abweichungen, Übergabepunkte und Fragen

- Die verlangte Root-`CLAUDE.md` existiert in diesem Worktree nicht und ist
  auch nicht im Git-Dateibaum enthalten. Die vorhandenen Modulregeln wurden
  gelesen; keine Root-Regel wurde erfunden.
- Der script-runner verwendet wegen Python/Git kein distroless-Laufzeitimage.
  Hier wurde die ausdrückliche B1-Vorgabe distroless/static umgesetzt.
- `model_request.json` ist ein gültiges Minimalbeispiel ohne Cache-Marken.
  B1 ergänzt System-/Werkzeug-Cache wie ausdrücklich beauftragt; die Golden-
  Bytes bleiben unverändert. Der Kern muss diese zulässige Form unterstützen.
- `run_agent_response_ok.json` enthält keine Logdaten. Deshalb bleibt das
  Ergebnis fachlich exakt und das inhaltfreie Lauf-Protokoll geht als eigene
  JSON-Zeile nach stdout, statt die Antwort um ein Feld zu erweitern.
- Neue Ende-Antworten liegen in `contract/proposed/`, getrennt von den
  unveränderten Kern-Goldens: `run_agent_response_{limit,provider,rejected,
  cancelled,revoked,error}.json`. Der Kern kann sie übernehmen; zusätzlich
  `run_log_ok.json` als Protokoll-Golden. Die Klassen cancelled/revoked und
  diese Fehlerantworten sind B1-Vorschläge, keine behauptete Live-Abnahme.
- Nach HTTP-Abbruch ist keine Endantwort mehr garantiert zustellbar; der
  Lauf und innere HTTP-Aufruf enden, das Protokoll meldet cancelled.
- B1 implementiert Anthropic. OpenAI, Ticker, Netzfreigabe, GUI und
  Veröffentlichungsstrecke wurden nicht ergänzt.
- Keine blockierende Frage. Offen bleibt der ausdrücklich ausstehende
  Live-Durchstich durch den echten Kern nach Übernahme dieses Standes.

## SHA-256 aller Referenzdateien

| Datei | SHA-256 |
|---|---|
| `contract/golden/model_error_limit.json` | `dd3ec348abc2356f839d70fa2d209f13af432dc0dcc10fcc6a737050eff7d991` |
| `contract/golden/model_error_provider.json` | `fc9728c4efc0e1174c442fc4c5d7de69b145ebe24961b78997769696a3107854` |
| `contract/golden/model_error_rejected.json` | `564e792773bdcc5dafc0c63db9e0db45f9d259b2093302dc9e8967e2f80efac0` |
| `contract/golden/model_request.json` | `fe0d814b09f6676c6af8c71b808e2fd994a40ad56a604f3e23db677877620b77` |
| `contract/golden/model_response_ok.json` | `b109471da73c26d7bfbad196261125cd425d262b1ba85c7760774cc08f7d6328` |
| `contract/golden/run_agent_request.json` | `8f07ef87f57a53ad06320080d9d56f69d94b3f7d74aa71e85d9eadf76649b079` |
| `contract/golden/run_agent_response_ok.json` | `f6df52dc4c13883b8378faf5a92b67e88184b9f1d1d6d38163c410e9495ef121` |
| `contract/golden/SHA256SUMS` | `102cf8da1799928b02b38d24863fe14956eec56e1b5ff28e29ec0878d6a148bf` |
| `contract/proposed/run_agent_response_cancelled.json` | `882dc8bd58b2a2a2b453bd4d5c1cc60e84ae35ea94e8f92775adf8611536b22d` |
| `contract/proposed/run_agent_response_error.json` | `6eb01cd3c564862a961a32cc89a73fa1b9617d97c5d4cb22244fbc70a4c56d06` |
| `contract/proposed/run_agent_response_limit.json` | `821cfdc01458fa201129e2df9ba5de379b5f232eaec1089d03ddcc9c05626ccc` |
| `contract/proposed/run_agent_response_provider.json` | `d96b22bb323534e1de807121b2b34680ec62087f42ca7215521d16ff189baba3` |
| `contract/proposed/run_agent_response_rejected.json` | `763a69ad51d1494c92b42344362717f67832a7d328f46862d8356ea3f8669953` |
| `contract/proposed/run_agent_response_revoked.json` | `ec23cac54a639fc3805c57492773ce645c903336efaf8ae80677b97b9a96e7bf` |
| `contract/proposed/run_log_ok.json` | `4cfb9624efb1a2af7d9345523ab38aaa31742fd05f9e466b7a3429d8bac714b0` |

## Vollständige Mutationsliste

| Mutation | Ergebnis |
|---|---|
| model im Body | ROT erkannt, zurückgesetzt |
| stream im Body | ROT erkannt, zurückgesetzt |
| Anbietertext durchreichen | ROT erkannt, zurückgesetzt |
| Inhalt im Lauf-Protokoll | ROT erkannt, zurückgesetzt |
| Compose Ausgangsnetz external | ROT erkannt, zurückgesetzt |
| Top-Level Allowlist entfernt | ROT erkannt, zurückgesetzt |
| Erzwungene Werkzeugwahl | ROT erkannt, zurückgesetzt |
| Anbieter-Werkzeuge | ROT erkannt, zurückgesetzt |
| Nachrichten-Zusatzfelder | ROT erkannt, zurückgesetzt |
| Unbekannte Inhaltsbloecke | ROT erkannt, zurückgesetzt |
| Inhaltsblock-Zusatzfelder | ROT erkannt, zurückgesetzt |
| URL statt base64 | ROT erkannt, zurückgesetzt |
| Ungueltiges base64 | ROT erkannt, zurückgesetzt |
| Verschachteltes tool_result | ROT erkannt, zurückgesetzt |
| URL-Bild-Platzhalter entfernt | ROT erkannt, zurückgesetzt |
| Senderpruefung entfernt | ROT erkannt, zurückgesetzt |
| runtime-key Sperre entfernt | ROT erkannt, zurückgesetzt |
| Lauf-Header Sperre entfernt | ROT erkannt, zurückgesetzt |
| Gateway-Pfade und Vertragskennung | ROT erkannt, zurückgesetzt |
| Unbekannte Argumente | ROT erkannt, zurückgesetzt |
| Ungueltige Grenzen | ROT erkannt, zurückgesetzt |
| Schrittgrenze | ROT erkannt, zurückgesetzt |
| Werkzeuggrenze | ROT erkannt, zurückgesetzt |
| Context-Abbruch abgekoppelt | ROT erkannt, zurückgesetzt |
| Eigene Wandzeit ignoriert | ROT erkannt, zurückgesetzt |
| Request-Wandzeit ignoriert | ROT erkannt, zurückgesetzt |
| Entzug ignoriert | ROT erkannt, zurückgesetzt |
| Weiterleitung erlaubt | ROT erkannt, zurückgesetzt |
| Umgebungsproxy erlaubt | ROT erkannt, zurückgesetzt |
| Werkzeug-Allowlist entfernt | ROT erkannt, zurückgesetzt |
| Doppelte Werkzeug-ID | ROT erkannt, zurückgesetzt |
| Doppelte Listeneintraege | ROT erkannt, zurückgesetzt |
| Werkzeugnamen ungeprueft | ROT erkannt, zurückgesetzt |
| Werkzeugliste unbegrenzt | ROT erkannt, zurückgesetzt |
| Cursor-Schleife | ROT erkannt, zurückgesetzt |
| Schluesseldatei unbegrenzt | ROT erkannt, zurückgesetzt |
| Schluessel mit Zeilenumbruch | ROT erkannt, zurückgesetzt |
| Gateway-Konfiguration ungeprueft | ROT erkannt, zurückgesetzt |
| Wandzeit-Konfiguration ungeprueft | ROT erkannt, zurückgesetzt |
| Daten hinter JSON | ROT erkannt, zurückgesetzt |
| Antwortgroesse aufgeweicht | ROT erkannt, zurückgesetzt |
| Anfragegroesse aufgeweicht | ROT erkannt, zurückgesetzt |
| Docker root | ROT erkannt, zurückgesetzt |
| Compose schreibbares Root-FS | ROT erkannt, zurückgesetzt |
| Thinking veraendert | ROT erkannt, zurückgesetzt |
| Cache entfernt | ROT erkannt, zurückgesetzt |
| SDK-Cleanup vom Lauf geloest | ROT erkannt, zurückgesetzt |

## Vollständige repo-weite Fundstellen

Die folgenden Treffer sind nach Datei gruppiert; der vollständige Treffertext steht in `evidence/invariants.txt`. Dokumente/Tests nennen die jeweiligen invarianten Eigenschaften, die Code-Treffer markieren ihre Durchsetzung oder einen alternativen Weg.

- `README.de.md:37`
- `agent-runner/CHANGELOG.md:7`; `agent-runner/CHANGELOG.md:11`; `agent-runner/CHANGELOG.md:13`
- `agent-runner/CLAUDE.md:16`; `agent-runner/CLAUDE.md:23`; `agent-runner/CLAUDE.md:34`; `agent-runner/CLAUDE.md:37`; `agent-runner/CLAUDE.md:49`; `agent-runner/CLAUDE.md:58`; `agent-runner/CLAUDE.md:59`; `agent-runner/CLAUDE.md:67`; `agent-runner/CLAUDE.md:84`; `agent-runner/CLAUDE.md:86`
- `agent-runner/Dockerfile:9`
- `agent-runner/README.md:6`; `agent-runner/README.md:12`; `agent-runner/README.md:13`; `agent-runner/README.md:14`; `agent-runner/README.md:36`; `agent-runner/README.md:41`; `agent-runner/README.md:44`
- `agent-runner/contract/README.md:12`; `agent-runner/contract/README.md:17`; `agent-runner/contract/README.md:34`; `agent-runner/contract/README.md:41`; `agent-runner/contract/README.md:44`; `agent-runner/contract/README.md:77`
- `agent-runner/docker-compose.yml:14`; `agent-runner/docker-compose.yml:23`; `agent-runner/docker-compose.yml:26`
- `agent-runner/internal/anthropic/adapter.go:54`; `agent-runner/internal/anthropic/adapter.go:57`
- `agent-runner/internal/anthropic/adapter_test.go:41`; `agent-runner/internal/anthropic/adapter_test.go:85`
- `agent-runner/internal/hygiene/hygiene_test.go:53`; `agent-runner/internal/hygiene/hygiene_test.go:85`; `agent-runner/internal/hygiene/hygiene_test.go:92`; `agent-runner/internal/hygiene/hygiene_test.go:113`; `agent-runner/internal/hygiene/hygiene_test.go:116`; `agent-runner/internal/hygiene/hygiene_test.go:128`
- `agent-runner/internal/runner/gateway.go:17`; `agent-runner/internal/runner/gateway.go:22`; `agent-runner/internal/runner/gateway.go:34`; `agent-runner/internal/runner/gateway.go:70`
- `agent-runner/internal/runner/loop.go:40`
- `agent-runner/internal/runner/runner_test.go:69`; `agent-runner/internal/runner/runner_test.go:184`; `agent-runner/internal/runner/runner_test.go:185`; `agent-runner/internal/runner/runner_test.go:199`; `agent-runner/internal/runner/runner_test.go:263`; `agent-runner/internal/runner/runner_test.go:282`; `agent-runner/internal/runner/runner_test.go:397`; `agent-runner/internal/runner/runner_test.go:458`; `agent-runner/internal/runner/runner_test.go:473`
- `agent-runner/internal/runner/server.go:70`; `agent-runner/internal/runner/server.go:85`
- `agent-runner/main.go:48`
- `agent-runner/probe/smoke.ps1:13`; `agent-runner/probe/smoke.ps1:14`; `agent-runner/probe/smoke.ps1:32`
- `box/CLAUDE.md:43`
- `box/RUNBOOK.md:391`; `box/RUNBOOK.md:1103`
- `box/docker-compose.box.yml:86`; `box/docker-compose.box.yml:161`; `box/docker-compose.box.yml:166`; `box/docker-compose.box.yml:170`; `box/docker-compose.box.yml:216`; `box/docker-compose.box.yml:251`; `box/docker-compose.box.yml:300`; `box/docker-compose.box.yml:306`; `box/docker-compose.box.yml:340`; `box/docker-compose.box.yml:363`
- `box/tunnel/profiles/box.yaml.example:17`
- `ebics-abholer/CLAUDE.md:24`
- `ebics-abholer/Dockerfile:5`; `ebics-abholer/Dockerfile:73`
- `ebics-abholer/README.md:13`; `ebics-abholer/README.md:215`
- `ebics-abholer/docker-compose.ebics-abholer.yml:72`
- `kartei-rpa/pad/FLOW-VORLAGE.md:184`
- `script-runner/CHANGELOG.md:40`; `script-runner/CHANGELOG.md:98`; `script-runner/CHANGELOG.md:99`; `script-runner/CHANGELOG.md:106`; `script-runner/CHANGELOG.md:123`; `script-runner/CHANGELOG.md:143`; `script-runner/CHANGELOG.md:147`; `script-runner/CHANGELOG.md:435`
- `script-runner/CLAUDE.md:21`; `script-runner/CLAUDE.md:59`; `script-runner/CLAUDE.md:64`; `script-runner/CLAUDE.md:69`; `script-runner/CLAUDE.md:72`; `script-runner/CLAUDE.md:204`; `script-runner/CLAUDE.md:237`; `script-runner/CLAUDE.md:304`; `script-runner/CLAUDE.md:314`; `script-runner/CLAUDE.md:315`; `script-runner/CLAUDE.md:316`; `script-runner/CLAUDE.md:353`; `script-runner/CLAUDE.md:373`; `script-runner/CLAUDE.md:538`; `script-runner/CLAUDE.md:618`; `script-runner/CLAUDE.md:627`; `script-runner/CLAUDE.md:649`; `script-runner/CLAUDE.md:652`; `script-runner/CLAUDE.md:715`; `script-runner/CLAUDE.md:749`; `script-runner/CLAUDE.md:894`
- `script-runner/README.md:25`; `script-runner/README.md:48`; `script-runner/README.md:64`; `script-runner/README.md:115`; `script-runner/README.md:243`; `script-runner/README.md:259`; `script-runner/README.md:270`
- `script-runner/contract/README.md:10`; `script-runner/contract/README.md:80`
- `script-runner/contract/golden/tools_list.json:3`
- `script-runner/contract/golden/tools_list_isolation_invalid.json:3`
- `script-runner/contract/golden/tools_list_schedules.json:3`
- `script-runner/deploy/jnpt-plugins-script-runner.conf.example:10`; `script-runner/deploy/jnpt-plugins-script-runner.conf.example:11`; `script-runner/deploy/jnpt-plugins-script-runner.conf.example:13`
- `script-runner/deploy/script-runner.conf.example:22`; `script-runner/deploy/script-runner.conf.example:23`; `script-runner/deploy/script-runner.conf.example:29`; `script-runner/deploy/script-runner.conf.example:32`
- `script-runner/docker-compose.yml:5`; `script-runner/docker-compose.yml:34`; `script-runner/docker-compose.yml:45`; `script-runner/docker-compose.yml:68`; `script-runner/docker-compose.yml:124`; `script-runner/docker-compose.yml:128`; `script-runner/docker-compose.yml:134`; `script-runner/docker-compose.yml:153`; `script-runner/docker-compose.yml:156`
- `script-runner/internal/contract/contract.go:20`; `script-runner/internal/contract/contract.go:27`; `script-runner/internal/contract/contract.go:200`; `script-runner/internal/contract/contract.go:221`; `script-runner/internal/contract/contract.go:235`
- `script-runner/internal/contract/contract_test.go:228`
- `script-runner/internal/fetcher/fetcher.go:70`
- `script-runner/internal/fetcher/fetcher_test.go:103`; `script-runner/internal/fetcher/fetcher_test.go:305`
- `script-runner/internal/frame/frame_test.go:15`
- `script-runner/internal/hygiene/hygiene.go:39`; `script-runner/internal/hygiene/hygiene.go:234`; `script-runner/internal/hygiene/hygiene.go:243`; `script-runner/internal/hygiene/hygiene.go:423`
- `script-runner/internal/hygiene/hygiene_test.go:49`; `script-runner/internal/hygiene/hygiene_test.go:50`; `script-runner/internal/hygiene/hygiene_test.go:51`; `script-runner/internal/hygiene/hygiene_test.go:53`; `script-runner/internal/hygiene/hygiene_test.go:54`; `script-runner/internal/hygiene/hygiene_test.go:55`; `script-runner/internal/hygiene/hygiene_test.go:56`; `script-runner/internal/hygiene/hygiene_test.go:57`; `script-runner/internal/hygiene/hygiene_test.go:62`; `script-runner/internal/hygiene/hygiene_test.go:109`; `script-runner/internal/hygiene/hygiene_test.go:117`; `script-runner/internal/hygiene/hygiene_test.go:144`; `script-runner/internal/hygiene/hygiene_test.go:146`; `script-runner/internal/hygiene/hygiene_test.go:159`; `script-runner/internal/hygiene/hygiene_test.go:160`; `script-runner/internal/hygiene/hygiene_test.go:347`; `script-runner/internal/hygiene/hygiene_test.go:348`; `script-runner/internal/hygiene/hygiene_test.go:409`; `script-runner/internal/hygiene/hygiene_test.go:420`
- `script-runner/internal/hygiene/release_test.go:382`; `script-runner/internal/hygiene/release_test.go:393`
- `script-runner/internal/mediator/default_branch_test.go:79`
- `script-runner/internal/mediator/helpers_test.go:30`; `script-runner/internal/mediator/helpers_test.go:266`; `script-runner/internal/mediator/helpers_test.go:290`; `script-runner/internal/mediator/helpers_test.go:294`; `script-runner/internal/mediator/helpers_test.go:297`; `script-runner/internal/mediator/helpers_test.go:302`; `script-runner/internal/mediator/helpers_test.go:311`
- `script-runner/internal/mediator/mediator.go:33`; `script-runner/internal/mediator/mediator.go:41`; `script-runner/internal/mediator/mediator.go:94`; `script-runner/internal/mediator/mediator.go:155`; `script-runner/internal/mediator/mediator.go:160`; `script-runner/internal/mediator/mediator.go:167`
- `script-runner/internal/mediator/mediator_test.go:49`; `script-runner/internal/mediator/mediator_test.go:94`
- `script-runner/internal/mediator/outage_test.go:42`
- `script-runner/internal/mediator/pins.go:53`; `script-runner/internal/mediator/pins.go:58`
- `script-runner/internal/mediator/run.go:302`; `script-runner/internal/mediator/run.go:305`; `script-runner/internal/mediator/run.go:311`
- `script-runner/internal/mediator/schedule_test.go:108`; `script-runner/internal/mediator/schedule_test.go:264`; `script-runner/internal/mediator/schedule_test.go:269`; `script-runner/internal/mediator/schedule_test.go:288`; `script-runner/internal/mediator/schedule_test.go:293`; `script-runner/internal/mediator/schedule_test.go:297`; `script-runner/internal/mediator/schedule_test.go:495`
- `script-runner/internal/mediator/schedules.go:120`; `script-runner/internal/mediator/schedules.go:133`
- `script-runner/internal/ticker/ticker.go:4`
- `script-runner/main.go:31`
- `script-runner/probe/compose.probe.yml:4`; `script-runner/probe/compose.probe.yml:20`
- `script-runner/probe/libs_test.go:271`
- `script-runner/probe/network_test.go:13`
- `script-runner/probe/probe_test.go:35`; `script-runner/probe/probe_test.go:246`; `script-runner/probe/probe_test.go:249`; `script-runner/probe/probe_test.go:293`; `script-runner/probe/probe_test.go:298`; `script-runner/probe/probe_test.go:309`; `script-runner/probe/probe_test.go:755`
- `script-runner/probe/run.sh:6`; `script-runner/probe/run.sh:36`
- `script-runner/probe/scripts/log_canary/jnpt-script.yaml:2`
