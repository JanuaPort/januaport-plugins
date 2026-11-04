# Prüfbericht linkedin-seite 0.3.1 (#1237) und 0.3.0 (#1306 F4)

## #1237 — ein Grant, ein Token-Zustand (aktueller Prüfstand, 0.3.1)

Befund GATE (#1237, 03.11. 21:28Z): Lesewerkzeuge liefen 03.11. 06:50Z, um 07:49Z stimmte der PO einer
zweiten Stelle mit nur den Schreib-Scopes zu, ab 10:52Z lieferte jeder Lesezugriff
`source_access_denied`. **Die Ursache ist nicht live belegt.** Die Herstellerdoku beschreibt genau
dieses Verhalten: „If you request a different scope than the previously granted scope, all the
previous access tokens are invalidated.“
(https://learn.microsoft.com/en-us/linkedin/shared/authentication/authorization-code-flow, Abschnitt
„Access Token Scopes and Lifetime“, abgerufen 04.11.2026.)

### Vorgehen, test-first in drei Zyklen

| Zyklus | Wächter | Rot | Code |
|---|---|---|---|
| Ein Grant, eine Stelle | Scope-Menge, `--gesamt`, Identität, Stelle `post-oauth`, Deploy-Dateien | `pruefung/grant-rot-1.txt`: 52 Tests, 20 Failures, 28 Errors | `source_contract`, `oauth`, `server`, `zustimmung.py`, Compose, Units |
| Ein Token-Zustand | `tests/test_token_state.py`, `OneTokenOnTheWire`, Log-Kanarienvogel | `pruefung/grant-rot-2.txt`: 21 von 21, die Schnittstelle fehlte | `oauth`, `workers`, `post_pool`, beide Adapter, `server` |
| Randfälle der Erneuerung | Eindämmung, Job-Tod, Token-Endpunkt-Ausfall, `close()`, Budget | `pruefung/grant-rot-3.txt`: 25 Tests, 2 rot, in einer Wegwerfkopie gegen den Entwurf | `_send_token` in beiden Pools |
| Doku und Version | `test_version`, `test_setup_guide` | `pruefung/grant-rot-4.txt` | CLAUDE, README, SETUP, CHANGELOG, ARCHITEKTUR, Pins |

Das Rot von Zyklus 2 zeigt nur die fehlende Schnittstelle. Dass die Tests den Defekt selbst treffen,
belegen die Mutanten `g2-job-own-copy*` und `g2-worker-own-copy*`, die je Prozess wieder eine eigene
Kopie einführen.

### Entscheidungen

- **`--gesamt` ist Pflicht und der einzige Modus, der Lese-Modus entfällt.** Ein Skript ohne Flag, das
  still vier Scopes anfordert, hätte eine alte Befehlszeile umgedeutet; ein Lese-Modus wäre die Falle,
  die den Schreibzugriff entwertet. Die Prüfung läuft vor der Secret-Abfrage.
- **Die Zustimmung verlangt `scope` in der Antwort, der Refresh nicht.** Das JSON hält die Menge als
  Beleg fest, eine unbestätigte Menge darf dort nicht stehen. Ein Refresh erweitert keinen Grant, und
  ein Verbot machte aus einer Auslassung einen Ausfall um Tag 55. Die aktuelle Herstellerdoku zeigt
  `scope` auch in der Refresh-Antwort; live belegt ist das nicht. Kommt das Feld, gilt die Menge exakt.
- **Der Serverprozess ist der einzige Erneuerer.** Es gibt echte Prozess-Trennung: zwei persistente
  Worker und je Auftrag ein Job, alle spawn. Bis 0.3.0 hielt jeder eine eigene Kopie, jeder Job begann
  beim Tresorstand, ein gekillter Worker verlor eine laufende Erneuerung (aus dem Code gelesen).
  Verworfen: ein Lease, bei dem ein Kindprozess erneuert. Sein Kill verlöre das Ergebnis, und
  Refresh-Token und Client-Secret blieben in Kindprozessen.
- **Thread-Sperre statt asyncio-Sperre.** Ein abgebrochener Request gäbe eine asyncio-Sperre frei,
  während der Tausch im Thread weiterläuft; der nächste Aufrufer tauschte ein zweites Mal.
- **Schreibfrist bleibt ungeprüft während einer Erneuerung.** Höchstens 36 Sekunden bei 25 Minuten,
  der Job schreibt in der Zeit nichts. Mehr Code wäre mehr Risiko als Nutzen.

### Zweitmeinung

Ein Berater-Aufruf vor der Umsetzung. Verdikt: Kern tragfähig. Übernommen: Eindämmung in
`_send_token` (am Code nachgeprüft; der Berater nannte einen Traceback, tatsächlich fängt die
Middleware den Lesepfad, aber auf dem Schreibpfad hing der Aufruf für immer, rot in
`grant-rot-3.txt`), Docstring in `workers.py`, Tests zu Job-Tod, Token-Endpunkt-Ausfall, `close()`
und Budget-Zeit. Nicht übernommen: `asyncio.timeout_at` um die Erneuerung, siehe oben.

### Ergebnis

- Komplette Suite: **281 Tests OK**, 428 s (`pruefung/grant-baseline.txt`). Ausgangsstand
  `e5dda65`: 241 Tests OK.
- **215/215 Mutationen erkannt**, Originalquellen unverändert (`pruefung/grant-mutations.txt`).
  Ausgangsstand: 186. Entfallen sind neun Anker des früheren Zweit-Grants und der Token-Trennung.
- Windows, Python 3.12.9, aiohttp 3.14.5 (nicht der Hash-Lock-Stand von CI; CI-Lauf im Abschlussbericht).

### Invariante → Nachweis

| Invariante | Negativer Nachweis |
|---|---|
| LinkedIn wird nur nach genau den vier Scopes gefragt | `test_one_authorization_requests_exactly_the_four_scopes`, Mutanten `g1-grant-*`, `g1-write-scope*`, `g1-consent-scope-string` |
| Kein zweiter Grant erzeugbar | `test_without_gesamt_nothing_happens`, `test_schreiben_is_gone_and_cannot_create_a_second_grant`, `test_the_script_offers_no_scope_choice` |
| Antwort mit anderer Menge oder ohne `scope` ergibt kein JSON | `test_a_different_set_in_the_answer…`, `test_an_answer_without_scope…` |
| JSON ohne `scope` oder mit anderer Menge startet nicht | `StoredCredential`, `test_runtime_paths_and_missing_invalid_identity`, `g1-identity-*` |
| Die Stelle `post-oauth` hat keine Wirkung | `test_a_leftover_file_in_the_old_place_has_no_effect`, `test_the_second_place_is_gone_from_the_service`, `test_the_second_grant_place_is_gone_from_every_deploy_file` |
| Genau eine Erneuerung, auch über beide Pfade | `test_concurrent_callers_renew_once`, `test_parallel_read_and_write_renew_once`, `g2-lock`, `g2-worker-own-copy-parallel` |
| Beide Pools teilen den Zustand | `test_serve_wires_one_state_into_both_pools`, `test_the_next_write_job_finds_the_renewed_token`, `test_a_rotated_refresh_token…` |
| Ein gekillter Prozess verliert keine Erneuerung | `test_a_worker_killed_during_a_renewal…`, `test_a_job_killed_during_a_renewal…` |
| Quote und Transport bleiben je Pfad getrennt | `test_only_the_path_that_renews_pays…`, `test_a_spent_read_quota…`, `test_a_spent_write_quota…`, `test_each_caller_pays_on_its_own_transport` |
| Kein Geheimnis in Kindprozessen | `test_no_secret_is_handed_to_a_worker_or_a_job` (echte Spawn-Argumente) |
| Der Server bleibt bedienbar | `test_the_server_stays_responsive_during_a_renewal` |
| `nicht_ausgefuehrt` bleibt wahr | `test_a_failing_token_endpoint_before_the_first_write_is_definite`, `test_a_job_killed…`, bestehende `test_sec_outcome` |
| Kein Token in Antwort oder Log, auch im Serverprozess | `test_a_renewal_in_the_server_process_leaves_no_token_in_response_or_log` |
| Lese-Routen senden nur GET | `test_the_read_path_still_sends_only_get_to_the_api`, Mutant `outbound-get-only` |

Unverändert und weiter von ihren Mutanten gehalten: Freigabe-Kopf, Hash-Vergleich, Tagesdeckel,
Gateway-Trennung (`linkedin-seite` / `linkedin-seite-manage` liegen im Kern, nicht in diesem Repo).

### Grenzen

Synthetisch. Nicht belegt sind die Ursache des Befunds, die Rückkehr des Lesezugriffs nach der neuen
Zustimmung, die Antwortform am echten Refresh und ein Post mit dem Gesamt-Grant. Der Windows-Lauf
ersetzt keinen Linux-Lauf. Das Access-Token im Lese-Worker trägt jetzt die Schreib-Scopes: SEC Bed. 4
ist aufgehoben und braucht eine SEC-Entscheidung.

## SEC c-6608136792 — Variante a und LF-Fixtures (Prüfstand 0.3.0)

- `b750de8`: eigener Test-Commit vor Produktionscode; 16 neue Tests zunächst mit
  13 erwarteten Assertion-Fehlschlägen (28.358 s), kein Syntax-/Importfehler.
  Nachweis: `pruefung/sec-c6608136792-red.txt`.
- Implementierungscommit: `fix(linkedin-seite): expose definite nonexecution in 2xx and pin LF fixtures (#1306)`;
  vollständiger Kopf-SHA im Abschluss. Nur lokale Commits, kein Push und keine Kern-Änderung.
- LEAD Variante a: Nach Bridge- und strenger Headerform-Prüfung liefert ein bewiesener
  Abbruch vor dem ersten LinkedIn-Schreibversuch HTTP 200 mit
  `{"error":"<fester Code>","nicht_ausgefuehrt":true}`. Der Kern wertet dieses Feld nur
  aus 2xx-Körpern aus. Nach Dispatch wird ausschließlich der IPC-Nachweis des Elternprozesses
  verwendet; keine Rückschlüsse allein aus einem HTTP-Status der Quelle.
- Header-/Hash-Abweichung bleibt 409 ohne beide Ergebnisfelder. Die Headerform wird vor
  Body-/Fachvalidierung und Bereitschaft geprüft; die selbst berechnete Hash-Gleichheit
  bleibt nach dem Lesen und vor jedem Schreiben Pflicht. Validierungs-/Medien-/Bereitschafts-
  Abbrüche können schon vor der Hashberechnung auftreten und sind nach gültiger Headerform
  sicher nicht ausgeführt. Bridge-Fehler und Vorschauen behalten ihre Fehlerstatus ohne Ergebnisfelder.
- Der erste Schreibmarker liegt jetzt nach erfolgreichem lokalem HTTP-Permit unmittelbar
  vor dem möglichen Senden. So ist die erste HTTP-Tageskappe eindeutig nicht ausgeführt;
  dieselbe Kappe nach initializeUpload bleibt unklar, mit 429 und ohne beide Felder.
  Quellfehler nach erstem POST/PUT/Edit/Delete sowie verlorene Antworten bleiben unverändert unklar.
- Gezielter Lauf: **49 Tests OK**, 127.355 s (`pruefung/sec-c6608136792-green.txt`).
  Neue echte HTTP-/Spawn-/Adaptertests belegen Tageskappe und erste HTTP-Kappe für alle vier
  Schreibaktionen, Dienst nicht eingerichtet, Pool geschlossen, Token abgelaufen,
  Body-/Eintragsvalidierung, Medienformat/-größe, Gateway-Abweisung und OAuth-Antwortabweisung
  vor dem ersten Fach-Schreibaufruf. Sie verlangen jeweils 200 + Nichtausführung und zählen
  null LinkedIn-Fach-Schreibaufrufe; außer beim ausdrücklich separat geprüften OAuth-Tausch
  sogar null LinkedIn-HTTP-Aufrufe. Antworten haben feste Codes und keine Credentials.
  Negativtests lösen Gate-/Bridgefehler, Vorschaufehler, einen ersten POST mit 403 sowie
  die HTTP-Kappe nach initializeUpload aus und prüfen Status und fehlende Ergebnisfelder.
- Komplette Suite: **241 Tests OK, 0 übersprungen**, 287.613 s
  (`pruefung/sec-c6608136792-baseline.txt`). **186/186 Mutationen erkannt**, Originalquellen unverändert; kompletter Lauf erfolgreich
  (`pruefung/sec-c6608136792-mutations.txt`). 14 neue Mutanten prüfen die sieben frühen
  200-Fälle, Ergebnis-Sperre bei Gate-/Bridgefehlern, Marker nach HTTP-Permit sowie je Fixture
  CRLF und Inhaltsdrift. Syntaxfehler und Timeouts zählen nicht als Kontrollerkennung.
- Vorschau-Beispiel auf LF normalisiert: Datei-SHA-256
  `c238d1617f0582ed7eaf46901368e04de92c47194f61c2bf9cc45f4bdcb54050`.
  hash_golden.json war bereits LF und bleibt bytegleich, SHA-256
  `566c635e69c43e5341e12293256518663e008dd23e6f9cc68588d2ee3f88dd00`.
  Root-.gitattributes: `linkedin-seite/tests/data/*.json text eol=lf`;
  die zwei früheren untergeordneten -text-Regeln sind entfernt.
  `git check-attr text eol` bestätigt text=set, eol=lf für beide Dateien.
- `test_fixture_bytes` prüft rohe Plugin-Dateibytes auf LF und feste SHA-256-Pins.
  Lokal werden beide Kern-Dateien in ../jpwt-1306f2 zusätzlich verglichen; CI ohne diesen
  Worktree verwendet dieselben festen Pins, ohne den Byte-Test zu überspringen.
  Befund der lokalen Windows-Arbeitskopie: Auch die F2-Kern-Beispieldatei hat CRLF,
  ihre Git-Fassung (HEAD/Index) hat LF. Der Test liest in diesem Fall nur die LF-Git-Fassung,
  prüft die exakte reine CRLF-Checkout-Konvertierung und vergleicht dann rohe Plugin-Bytes
  gegen die kanonischen Kern-Bytes. Keine Normalisierung von Wirkungs-/Post-Texten.
  Abweichung von der Annahme im Auftrag: Der lokale Kern-Checkout war ebenfalls CRLF;
  die kanonische Kern-Datei ist LF. Kein Kern-Code und keine Kern-Datei wurde verändert.
- [x] CLAUDE.md, CHANGELOG, README, SETUP, ARCHITEKTUR und aktueller Prüfbericht nachgezogen.
- [x] Neue Tests zuerst separat committet; bestehende HTTP-Status-/Gate-Tests aktualisiert.
- [x] LF-Regel, rohe SHA-256-Tests, lokaler Kern-Abgleich und CI-Pins für beide Fixtures.
- RUECKFRAGE: nein. Keine Live-Abnahme (GATE), keine Spec-/GUI-/Kern-Änderung.

Repo-weite Suche nach Nichtausführungs-/Status-Invarianten UND allen weiteren Wegen
(HTTP-Grenze, Parser, Pool/IPC, API-/Upload-Transport, Quellen, Tests, Mutanten, Git-Attribute
und Fixture-Abgleich): `pruefung/sec-c6608136792-invarianten.txt`.
Fachfremde Attribute/Pins anderer Module beeinflussen den LinkedIn-Pfad nicht und bleiben
unverändert. Alte Prüfartefakte und folgende ältere Berichtsstände sind historische Nachweise;
aktuell gelten ausschließlich die Regeln dieses SEC-Nachtrags. Einschlägige Treffer:

```text
ARCHITEKTUR.md:18
linkedin-seite/tests/mutations.py:170
linkedin-seite/tests/mutations.py:171
linkedin-seite/tests/mutations.py:175
linkedin-seite/tests/mutations.py:176
linkedin-seite/tests/mutations.py:177
linkedin-seite/tests/mutations.py:178
linkedin-seite/tests/mutations.py:179
linkedin-seite/tests/mutations.py:180
linkedin-seite/tests/mutations.py:181
linkedin-seite/tests/mutations.py:182
linkedin-seite/tests/mutations.py:183
linkedin-seite/tests/mutations.py:184
linkedin-seite/tests/mutations.py:212
linkedin-seite/tests/mutations.py:243
linkedin-seite/tests/mutations.py:244
linkedin-seite/tests/mutations.py:245
linkedin-seite/SETUP.md:155
linkedin-seite/SETUP.md:190
linkedin-seite/README.md:213
linkedin-seite/README.md:214
linkedin-seite/README.md:239
linkedin-seite/README.md:253
linkedin-seite/README.md:254
linkedin-seite/tests/test_sec_outcome.py:16
linkedin-seite/tests/test_sec_outcome.py:31
linkedin-seite/tests/test_sec_outcome.py:38
linkedin-seite/tests/test_sec_outcome.py:48
linkedin-seite/tests/test_sec_outcome.py:87
linkedin-seite/tests/test_sec_outcome.py:113
linkedin-seite/tests/test_sec_outcome.py:130
linkedin-seite/tests/test_post_server.py:102
linkedin-seite/tests/test_post_server.py:126
linkedin-seite/tests/test_post_server.py:142
linkedin-seite/tests/test_post_server.py:146
linkedin-seite/tests/test_post_server.py:205
linkedin-seite/tests/test_post_server.py:249
linkedin-seite/tests/test_post_domain.py:174
linkedin-seite/tests/test_post_adapter.py:260
linkedin-seite/tests/test_post_adapter.py:274
linkedin-seite/tests/test_f4.py:66
linkedin-seite/tests/test_f4.py:70
linkedin-seite/linkedin_seite/server.py:115
linkedin-seite/linkedin_seite/server.py:142
linkedin-seite/linkedin_seite/server.py:145
linkedin-seite/linkedin_seite/server.py:181
linkedin-seite/linkedin_seite/server.py:182
linkedin-seite/linkedin_seite/server.py:183
linkedin-seite/linkedin_seite/server.py:192
linkedin-seite/linkedin_seite/server.py:193
linkedin-seite/tests/support_post.py:351
linkedin-seite/linkedin_seite/post_pool.py:149
linkedin-seite/linkedin_seite/post_pool.py:152
linkedin-seite/linkedin_seite/post_pool.py:202
linkedin-seite/linkedin_seite/post_pool.py:233
linkedin-seite/linkedin_seite/post_pool.py:238
linkedin-seite/linkedin_seite/post_pool.py:239
linkedin-seite/linkedin_seite/post_adapter.py:33
linkedin-seite/linkedin_seite/post_adapter.py:36
linkedin-seite/linkedin_seite/post_adapter.py:37
linkedin-seite/linkedin_seite/post_adapter.py:143
linkedin-seite/linkedin_seite/post_adapter.py:175
linkedin-seite/linkedin_seite/post_adapter.py:189
linkedin-seite/linkedin_seite/domain.py:33
linkedin-seite/CHANGELOG.md:6
linkedin-seite/CHANGELOG.md:24
linkedin-seite/CLAUDE.md:106
linkedin-seite/CLAUDE.md:140
linkedin-seite/CLAUDE.md:146
linkedin-seite/CLAUDE.md:147
linkedin-seite/CLAUDE.md:156
.gitattributes:1
linkedin-seite/CHANGELOG.md:9
linkedin-seite/CLAUDE.md:115
linkedin-seite/CLAUDE.md:126
linkedin-seite/CLAUDE.md:177
linkedin-seite/README.md:288
linkedin-seite/README.md:300
linkedin-seite/README.md:303
linkedin-seite/README.md:402
linkedin-seite/tests/mutations.py:75
linkedin-seite/tests/mutations.py:160
linkedin-seite/tests/mutations.py:168
linkedin-seite/tests/mutations.py:185
linkedin-seite/tests/mutations.py:186
linkedin-seite/tests/mutations.py:187
linkedin-seite/tests/mutations.py:188
linkedin-seite/tests/mutations.py:217
linkedin-seite/linkedin_seite/source_contract.py:96
linkedin-seite/tests/test_f2_preview.py:12
linkedin-seite/tests/test_f4.py:14
linkedin-seite/tests/test_fixture_bytes.py:9
linkedin-seite/tests/test_fixture_bytes.py:11
linkedin-seite/tests/test_fixture_bytes.py:24
```

## Vorheriger LEAD-/F2-Prüfstand (vor SEC c-6608136792)


## Nachtrag LEAD — F2-Anschlussvertrag (historischer Prüfstand)

- Nur lesend abgeglichen: `../jpwt-1306f2/docs/connector-linkedin-seite.md` (Ablauf/
  Antwortvertrag, gemeinsames Vorschau-Beispiel) und `internal/connector/testdata/
  linkedin_vorschau_beispiel.json` desselben Worktrees.
- Befund auf `b2a3c05`: ziel_urn/text und medien[].name fehlten direkt; ein zusätzliches
  wirkung-Objekt wurde ausgegeben. Korrigiert in post_adapter.py:99: genau die zwölf
  Schlüssel eintrag_id, aktion, format, ziel_urn, text, medien, umfrage, post_text,
  post_urn, kommentar_text, dokument_titel, hash; pro Medium name, sha256, datei, art, bytes.
  Die vier ausdrücklich von F2 erwarteten Ablaufhinweise bleiben optional erlaubt.
  Hash und direkte Wirkungsfelder kommen aus demselben einmal gelesenen Entry (H3).
- `a8d6312` — eigener Test-Commit vor Code: Beispiel unverändert kopiert, Form-/Typ-/
  Hash-Tests am echten HTTP-/Spawn-/Gateway-/LinkedIn-Adapter; bestehende H3-Tests angepasst.
  Erst rot: 5 Tests, 4 Assertion-Fehlschläge wegen der Vorschauform; Wiederholungsfall grün
  (`pruefung/f2-preview-red.txt`).
- Nach Korrektur: 6 gezielte Tests grün, 12.418 s (`pruefung/f2-preview-green.txt`).
  Der Beispielhash wird unabhängig mit hashlib nachgerechnet; für den entsprechenden echten
  Eintrag sind Schlüssel und Typen identisch und alle Werte bis auf Datei-Bytes/-Hash und
  daraus folgenden Wirkungshash gleich. Das Kern-Beispiel verwendet die synthetischen Bytes
  `synthetisches Dokument` ohne PDF-Signatur; der echte Adaptertest verwendet gültige
  synthetische PDF-Bytes und umgeht keine Medienprüfung. Die Beispieldatei bleibt bytegleich,
  SHA-256 `6ea59e7aa1d07676d7e676a9b87193f48ad39b0bbdde8d63641f134798ed7454`.
- Header geprüft: ausschließlich hash=<v2-Hash mit Präfix>, ohne Auftrags-ID; negative
  Varianten mit ergänzter Auftrags-ID ergeben 409. Kein Header-Code musste geändert werden.
- Wiederholung geprüft: erster Erfolg ausgefuehrt: true; derselbe Hash anschließend
  wiederholt: true UND ausgefuehrt: true, dieselbe URN, genau ein Post und eine gezählte
  Aktion. Produktionscode dafür war bereits korrekt (post_pool.py:185, PostAdapter.run).
- Komplette Suite: **225 Tests OK, 0 übersprungen**, 254.573 s
  (`pruefung/f2-preview-baseline.txt`). **172/172 Mutationen erkannt**, Originalquellen unverändert; Gesamtlauf erfolgreich
  (`pruefung/f2-preview-mutations.txt`). Vier neue Mutanten: unerwartetes
  Wirkungsobjekt, fehlende direkte Felder, fehlender Medienname, Erfolgssignal bei
  wiederholtem Ergebnis entfernt. Originale bleiben unverändert; kein Syntaxfehler oder
  Timeout gilt als Kontrollnachweis.
- [x] Modulvertrag, README, SETUP, CHANGELOG, ARCHITEKTUR und Bericht nachgezogen.
  F2-Fristen präzisiert: Call aktuell 25 Sekunden, Runtime-Decke 120 Sekunden; der
  Beisteller wartet bis 25 Minuten. Lange Jobs können daher im Kern früher unklar werden.
  Keine Veränderung an Version 0.3.0, Docker, Compose, Gatewayrechten oder Kern-Dateien nötig.
- Folgender Implementierungscommit: `fix(linkedin-seite): align preview with F2 response contract (#1306)`.
  Kopf-SHA im Abschluss. Nur lokale Commits, keine Live-Abnahme und kein Push.
- Abweichungen: keine vom Anschlussvertrag; PDF-Testdaten wie oben erläutert.
  RUECKFRAGE: nein.

Repo-weite Suche nach alter Antwortform UND weiteren Wegen (Adapter, HTTP-Middleware,
Ergebnis-Cache, Tests und Mutanten): `pruefung/f2-preview-invarianten.txt`.
Ein verschachteltes Wirkungsfeld verbleibt nur als absichtlicher Negativmutant; historische
Prüfdateien beschreiben den vorigen Stand. Die Suchtreffer dieses Nachtrags sind:

```text
ARCHITEKTUR.md:18
linkedin-seite/CHANGELOG.md:5
linkedin-seite/CHANGELOG.md:8
linkedin-seite/CLAUDE.md:109
linkedin-seite/CLAUDE.md:114
linkedin-seite/CLAUDE.md:146
linkedin-seite/README.md:279
linkedin-seite/README.md:354
linkedin-seite/SETUP.md:193
linkedin-seite/linkedin_seite/redaktion.py:291
linkedin-seite/linkedin_seite/redaktion.py:307
linkedin-seite/linkedin_seite/post_pool.py:185
linkedin-seite/linkedin_seite/post_adapter.py:99
linkedin-seite/linkedin_seite/post_adapter.py:103
linkedin-seite/linkedin_seite/post_adapter.py:104
linkedin-seite/tests/test_post_server.py:65
linkedin-seite/tests/test_post_adapter.py:46
linkedin-seite/tests/test_f2_preview.py:12
linkedin-seite/tests/test_f2_preview.py:15
linkedin-seite/tests/test_f2_preview.py:21
linkedin-seite/tests/mutations.py:165
linkedin-seite/tests/mutations.py:175
linkedin-seite/tests/mutations.py:176
linkedin-seite/tests/mutations.py:177
linkedin-seite/tests/mutations.py:178
```

## Vorheriger F4-Prüfstand (vor dem LEAD-Nachtrag)


Grundlage: Auftrag / Plan v2/v2.1, SEC c-6602359446 (A1, A3), c-6604738594 (H1–H3).
Branch `feat/1306-f4-beisteller`, ausschließlich Worktree `jpp-1306f4` im Repo januaport-plugins.
Root-CLAUDE.md ist im Checkout nicht vorhanden. Modul-CLAUDE, README, SETUP, bisheriger
Prüfbericht und die vier beauftragten Module gelesen; Kern-Hashvertrag und Golden-Datei
nur lesend aus `../jpwt-1306f1/internal/freigabe` verwendet.
Dieser Bericht ersetzt den bisherigen B1-Prüfstand als aktuellen Nachweis; historische
Abnahmen und Herstellerbelege bleiben in der Git-Historie bzw. im CHANGELOG erhalten.

## Commits und Vorgehen

- `7f3dbed` — Test-Commit vor Produktionsänderung: Hash-v2-Goldens und echte HTTP-/Adapter-
  Negativtests für Header, vollständige Wirkung und Ergebnisfelder.
- Folgecommit `feat(linkedin-seite): bind writes to core approval hash v2 (#1306)` enthält
  Umsetzung, Umstellung der Bestandstests, Pflichtmutanten, Doku und diesen Bericht.
  Vollständiger Kopf-SHA wird im Abschluss gemeldet (kein selbstreferenzieller SHA im Commit).
- Autor: JanuaPort GmbH. Ausschließlich lokale Commits; kein Push, gh, PR, Merge oder Tag.

## Ergebnis und Verifikation

- Tests zuerst rot: `PYTHONPATH=tests python -m unittest test_f4.HashV2
  test_f4.F4.test_missing_header_is_409_for_every_write`: 4 Tests, 1 Fehlschlag und 20 Fehler.
  Alter Adapter antwortete ohne Header 403 statt 409; v2-Schnittstelle fehlte.
- Python 3.12.9 lokal, Abhängigkeiten erfolgreich mit
  `python -m pip install --require-hashes -r requirements.lock` installiert.
- Erster vollständiger Lauf `python -m unittest discover -s tests -v`:
  **217 Tests OK, 0 übersprungen**, 235.671 s (`pruefung/f4-tests.txt`).
- Vier ergänzende Tests grün (15.923 s): Prozessende vor/nach realem Schreibversuch,
  unbestätigtes 202, erneute Headerprüfung trotz Ergebnis-Cache und Aktionsaustausch.
- Finaler vollständiger Lauf (Baseline der Mutationsrunde): **221 Tests OK, 0 übersprungen**,
  241.998 s (`pruefung/f4-baseline.txt`), einschließlich aller vier Ergänzungen.
- Mutationen: **168/168 Kontrollen durch rote Proben belegt**: 167 im Gesamtlauf,
  eine zunächst ungültige bestehende Log-Probe nach Korrektur gezielt erneut erkannt
  (1 Test, 1 erwarteter Assertion-Fehlschlag, 2.184 s). Alle 22 F4-Proben schon im
  Gesamtlauf erkannt. Keine Kontrollmutation überlebt; Originale jeweils unverändert.
  Gesamtlauf: `pruefung/f4-mutations.txt`; korrigierter Nachlauf:
  `pruefung/f4-mutation-log-corrected.txt` (Exit 0 des Nachweis-Treibers).
- `git diff --check`: grün. SETUP UTF-8/≤32 KiB, Versionskonsistenz und Deploy-/Release-
  Wächter sind Bestandteil der vollständigen Suite. Kein Container-/Live-/CI-Nachweis behauptet.
- Golden-Datei wörtlich kopiert, bytegleich zum Kern: SHA-256
  `566c635e69c43e5341e12293256518663e008dd23e6f9cc68588d2ee3f88dd00`.
  `.gitattributes` verhindert Zeilenenden-Konvertierung dieser Datei.

## Invariante → Umsetzung → negativer Nachweis

| Invariante | Umsetzung / Fundstellen | Ausgelöstes Verbot und Test |
|---|---|---|
| Alle vier Aktionen nur mit exaktem Header, sonst 409 vor LinkedIn | redaktion.py:110,125; server.py:139; post_adapter.py:113 | F4.test_missing_header_is_409_for_every_write; test_header_exact_equality_before_any_linkedin_write_for_every_action; test_duplicate_header_rejected. Auch echter Adapter ohne HTTP: Approval.test_missing_header_rejected_at_the_actual_adapter. LinkedIn-Fake zählt 0 Aufrufe. |
| Keine zweite Freigabequelle | redaktion.py:65,88; gateway.py:56,128; config.example.json:1 | Gateway/GatewaySSE.test_any_other_tool_is_refused_before_http löst auch die frühere Freigabesuche aus; Approval.test_approval_field_inside_the_plan_is_worthless; WriteConfiguration weist den entfernten Config-Key zurück. |
| Ganze Wirkung, v2 exakt wie Kern | redaktion.py:287,297,311; tests/data/hash_golden.json:1 | HashV2: alle sieben Vektoren auf kanonisches JSON UND exakten Hash; jedes Hashfeld/jeder Medienname/-hash/jede Antwort und beide Listenreihenfolgen verändert. F4.test_stale_hash_for_text_target_comment_media_title_bytes_order_and_poll, test_organization_is_bound_to_preview_and_header und test_action_cannot_be_exchanged_even_with_identical_target_and_text am echten Adapter. Medienbytes: Approval.test_media_bytes_exchanged_after_the_preview. |
| Ein Read / ein Entry für Anzeige und Wirkung | post_adapter.py:84,99; redaktion.py:287 | F4.test_preview_and_effect_use_one_entry_read verändert den Gateway-Eintrag unmittelbar nach dem Lesen; Anzeige, Dokumenttitel, Wirkung und Hash bleiben derselbe Stand; genau ein Eintrags-GET. |
| Erfolg ausdrücklich bestätigt | post_adapter.py:137,156 | F4.test_success_for_every_action_and_only_two_gateway_tools; test_accepted_but_unconfirmed_response_is_unclear (LinkedIn antwortet 202 statt bestätigendem 201/204). |
| Nichtausführung ausschließlich vor erstem Schreibversuch | post_adapter.py:190; post_pool.py:49,135,201; server.py:177 | F4.test_validation_and_media_errors_are_definitely_not_executed; test_errors_after_first_write_have_no_definitive_flags für alle vier Aktionen; test_upload_initialization_already_makes_failure_unclear; test_worker_exit_before_and_after_first_real_write. Bei Fehler nach POST/Edit/Delete/Upload weder Erfolgs- noch Nichtausführungsfeld, auch bei Prozessverlust. |
| Headerprüfung vor RAM-Ergebniscache | post_adapter.py:113; post_pool.py:135 | F4.test_cached_success_still_requires_current_matching_header löst fehlenden, falschen und nach Inhaltsänderung veralteten Header trotz bestehendem Erfolg aus, ohne erneuten LinkedIn-Aufruf. |
| Nur ein Auftrag, Endergebnis statt Zwischenzustand | post_pool.py:230 | Background.test_slow_job_waits_for_result_and_rejects_concurrent_requests: echte verzögerte Upload-Antwort; zweite Anfrage 429 vor eigenem Start; erste wartet bis bestätigtem Ergebnis. |
| Bestehende Scopes/Autor-/Upload-/Bremssperren | source_contract.py, oauth.py, post_adapter.py, gateway.py, post_pool.py | Vollständige Bestandssuite inkl. realem HTTP-Transport, Spawn-Prozessen, Scope-/Bridge-/Quota-/Host-/Redirect-/Medien-/Video-/Log-Canaries. Bestehende Mutanten bleiben erhalten, soweit die jeweilige alte Sperre nicht durch F4 ersetzt wurde. |

## Mutationsproben

`python tests/mutations.py` führt zuerst die komplette Suite auf unveränderten Quellen aus,
kopiert je Probe linkedin_seite/tests/tools in ein eigenes temporäres Verzeichnis, entfernt
oder verfälscht dort genau eine Kontrolle und verlangt einen roten Test. Syntax-/Importfehler
und Timeouts zählen nicht. Danach wird die Kopie verworfen; SHA-256-Fingerprints umfassen
Produktionscode, Tests, PO-Skript und Golden-Datei.

168 Proben sind vorgesehen, darunter 22 F4-Proben: Hash-Gleichheit entfernt, fehlender Header
am echten Adapter durchgelassen, doppelter Header akzeptiert, jedes einzelne v2-Hashfeld
verfälscht (inklusive Medienname/Dateihash und Umfrageunterfelder), Listen umsortiert,
Golden-Hash geändert, Erfolgssignal entfernt, falsche/fehlende Nichtausführung,
Schreibmarker entfernt, unbestätigtes 202 als Erfolg und zweiter Eintrags-Read.
Der erste Gesamtlauf endete wegen `b5-trace-text` mit `failed probes` (167 erkannt,
1 ungültig). Ursache im vorhandenen Mutanten: ein zusätzlicher String wurde an die Stelle
vor dem Hash gesetzt, sein Formatplatzhalter aber ans Ende; dadurch landete eine URN
auf `%d`, die Log-Formatierung schlug fehl und die Probe lief ins Timeout. Ein Timeout
zählt ausdrücklich nicht als erkannte Kontrolle. Der Mutant setzt den zusätzlichen `%s`
jetzt an die passende Stelle; derselbe echte HTTP-/Prozess-Test scheitert sofort an der
unerlaubten Spurzeile. Zusätzlich druckt der Treiber nach Timeout keine veraltete Ausgabe
der vorherigen Probe mehr. Nach dieser reinen Treiberkorrektur wurde ausschließlich die
betroffene Probe wiederholt; Produktionscode und reguläre Tests sind seit der vollständigen
221er-Baseline unverändert. Die abschließende Auswertung steht oben und in beiden Belegen.
Die alte Herkunftslabel-Prüfung und der lokale Unklar-Sperrvermerk samt ihren Mutanten sind
ersetzt; der dauerhafte Sperrvermerk liegt im Kern. `report_hash` ist nur RAM-Komfort.

## Doku-Pflicht

- [x] CLAUDE.md: Modulvertrag 0.3.0, Header/Hash/Ergebnisfelder, IPC-Grenze, Cache-/Kern-Zuständigkeit.
- [x] README.md: Werkzeuge, Wirkung, Hash, zwei Gatewayrechte, Migration/Timeout und Live-Gates.
- [x] SETUP.md: Betreiberablauf, entfernte Suchrechte/Config-Key, Kern-Freigabe, Ergebnisfelder.
- [x] CHANGELOG.md: 0.3.0 mit #1306 und SEC-Bezug; alte Einträge ausdrücklich Release-Historie.
- [x] Dockerfile/MCP-Client: Version 0.3.0; Compose: zwei Lesewerkzeuge und Kern-Header.
- [x] config.example.json und Deploy-/Versions-/Setup-Tests konsistent.
- [x] ARCHITEKTUR.md: repo-weite Zusammenfassung der betroffenen Invarianten aktualisiert.
- [x] PRUEFBERICHT.md: aktueller Prüfstand, Test-/Mutationsnachweis, Suchtreffer und Abweichungen.

## Repo-weite Suche und weitere Wege zum selben Ziel

Zwei Suchläufe prüfen alte Invariantentexte und alternative Kompositionsstellen:
Karteifreigabe/Herkunftslabels/v1/alter Zwischenzustand sowie Header, Parser, Wirkung,
Hash, Cache, IPC, Adapter-API, Upload, Transport, Konfiguration und Werkzeugliste.
Vollständige Treffer mit Datei:Zeile: [pruefung/f4-invarianten.txt](pruefung/f4-invarianten.txt).
Die einschlägigen Treffer sind unten zusätzlich direkt aufgeführt.

Alte Suchwerkzeuge kommen nur im ausdrücklichen Abweisungstest und in historischen
CHANGELOG-/Prüfarbelegen vor. Herkunftslabels haben keinen Produktionspfad im Beisteller mehr.
Fachfremde SSO-/via-Treffer in Box/anderen Beistellern betreffen deren eigene Verträge und
bleiben unverändert. `pruefung/invarianten.txt` ist der historische Nachweis von #1237.
Die Rohsuche schließt Prüfartefakte, diesen Bericht und isolierte Mutationskopien aus.

## Abweichungen, Grenzen, Zuständigkeiten

Keine Abweichung vom beauftragten Header oder Hash v2. Verfahrenshinweis: Eine bestehende
Log-Mutationsprobe musste wegen falscher Formatargumente korrigiert und gezielt wiederholt
werden (siehe Mutationsproben); kein Timeout wird als roter Kontrollnachweis gezählt. Ergänzt: Nur der dokumentierte
LinkedIn-Erfolgsstatus 201 (Create/Kommentar), 204 (Edit/Delete) bzw. 200 (Initialisierung/
Status/Finalize) gilt als bestätigt; etwa 202 bleibt unklar. Die IPC-Markierung erfolgt
konservativ vor dem Transport; eine danach abgewiesene HTTP-Quote kann deshalb ebenfalls
unklar bleiben, aber niemals fälschlich als nicht ausgeführt gelten.

Der Kern hält dauerhafte Start-/Ausführungs-/Unklar-Sperren; der Beisteller besitzt nur
flüchtige Zähler und einen Ergebnis-Cache. Nach Fehler ab erstem möglichen Schreibversuch
entscheidet ausschließlich der Kern über den weiteren Umgang. HTTP wartet maximal 25 Minuten;
Client-/Proxy-Timeouts müssen dazu passen. Eine verlorene Clientverbindung beendet den
Schreibprozess nicht; der Kern behandelt den verlorenen Ausgang als unklar.

Keine Kern-/GUI-/Spec-Änderung, keine Live-Abnahme (GATE), kein Nachfrage-Fenster.
F2/F3 und produktive Integration mit dem Kern bleiben außerhalb dieses F4-Nachweises.
Keine Secrets verwendet; alle Fachaufrufe der Tests gehen an lokale synthetische HTTP-Fakes.

RUECKFRAGE: nein

<details>
<summary>Einschlägige Suchtreffer (Datei:Zeile)</summary>

```text
linkedin-seite/tests/test_sec_tightening.py:61
linkedin-seite/tests/test_post_server.py:161
linkedin-seite/tests/test_post_gateway.py:45
linkedin-seite/tests/test_consent.py:175
linkedin-seite/tests/test_consent.py:176
linkedin-seite/tests/test_consent.py:177
linkedin-seite/tests/test_oauth_toleranz.py:192
linkedin-seite/README.md:165
linkedin-seite/README.md:396
linkedin-seite/CHANGELOG.md:25
linkedin-seite/CHANGELOG.md:29
linkedin-seite/CHANGELOG.md:42
linkedin-seite/CHANGELOG.md:44
ARCHITEKTUR.md:18
linkedin-seite/CLAUDE.md:16
linkedin-seite/CLAUDE.md:104
linkedin-seite/CLAUDE.md:111
linkedin-seite/CLAUDE.md:113
linkedin-seite/CLAUDE.md:114
linkedin-seite/CLAUDE.md:126
linkedin-seite/CLAUDE.md:131
linkedin-seite/CLAUDE.md:132
linkedin-seite/CLAUDE.md:138
linkedin-seite/CHANGELOG.md:6
linkedin-seite/CHANGELOG.md:11
linkedin-seite/CHANGELOG.md:13
linkedin-seite/linkedin_seite/gateway.py:59
linkedin-seite/linkedin_seite/gateway.py:144
linkedin-seite/linkedin_seite/gateway.py:153
linkedin-seite/linkedin_seite/domain.py:42
linkedin-seite/linkedin_seite/domain.py:43
linkedin-seite/linkedin_seite/source_contract.py:105
linkedin-seite/linkedin_seite/server.py:13
linkedin-seite/linkedin_seite/server.py:139
linkedin-seite/linkedin_seite/server.py:177
linkedin-seite/linkedin_seite/server.py:178
linkedin-seite/linkedin_seite/server.py:187
linkedin-seite/linkedin_seite/redaktion.py:24
linkedin-seite/linkedin_seite/redaktion.py:25
linkedin-seite/linkedin_seite/redaktion.py:26
linkedin-seite/linkedin_seite/redaktion.py:29
linkedin-seite/linkedin_seite/redaktion.py:66
linkedin-seite/linkedin_seite/redaktion.py:110
linkedin-seite/linkedin_seite/redaktion.py:311
linkedin-seite/linkedin_seite/post_pool.py:46
linkedin-seite/linkedin_seite/post_pool.py:49
linkedin-seite/linkedin_seite/post_pool.py:88
linkedin-seite/linkedin_seite/post_pool.py:155
linkedin-seite/linkedin_seite/post_pool.py:202
linkedin-seite/linkedin_seite/post_pool.py:233
linkedin-seite/linkedin_seite/post_pool.py:239
linkedin-seite/linkedin_seite/post_adapter.py:8
linkedin-seite/linkedin_seite/post_adapter.py:80
linkedin-seite/linkedin_seite/post_adapter.py:85
linkedin-seite/linkedin_seite/post_adapter.py:89
linkedin-seite/linkedin_seite/post_adapter.py:97
linkedin-seite/linkedin_seite/post_adapter.py:118
linkedin-seite/linkedin_seite/post_adapter.py:137
linkedin-seite/linkedin_seite/post_adapter.py:140
linkedin-seite/linkedin_seite/post_adapter.py:157
linkedin-seite/linkedin_seite/post_adapter.py:159
linkedin-seite/linkedin_seite/post_adapter.py:173
linkedin-seite/linkedin_seite/post_adapter.py:187
linkedin-seite/linkedin_seite/post_adapter.py:192
linkedin-seite/linkedin_seite/post_adapter.py:193
linkedin-seite/tests/mutations.py:39
linkedin-seite/tests/mutations.py:149
linkedin-seite/tests/mutations.py:169
linkedin-seite/tests/mutations.py:170
linkedin-seite/tests/mutations.py:171
linkedin-seite/tests/mutations.py:172
linkedin-seite/tests/mutations.py:174
linkedin-seite/tests/mutations.py:211
linkedin-seite/tests/support_post.py:354
linkedin-seite/tests/support_post.py:358
linkedin-seite/README.md:230
linkedin-seite/README.md:234
linkedin-seite/README.md:237
linkedin-seite/README.md:275
linkedin-seite/README.md:295
linkedin-seite/README.md:345
linkedin-seite/README.md:363
linkedin-seite/README.md:394
linkedin-seite/tests/test_post_domain.py:11
linkedin-seite/tests/test_post_domain.py:12
linkedin-seite/tests/test_post_domain.py:28
linkedin-seite/tests/test_post_domain.py:30
linkedin-seite/tests/test_post_domain.py:41
linkedin-seite/tests/test_post_domain.py:42
linkedin-seite/tests/test_post_domain.py:46
linkedin-seite/tests/test_post_domain.py:52
linkedin-seite/tests/test_post_domain.py:56
linkedin-seite/tests/test_post_domain.py:59
linkedin-seite/tests/test_post_domain.py:62
linkedin-seite/tests/test_post_domain.py:65
linkedin-seite/tests/test_post_domain.py:79
linkedin-seite/tests/test_post_domain.py:88
linkedin-seite/tests/test_post_domain.py:89
linkedin-seite/tests/data/hash_golden.json:12
linkedin-seite/tests/data/hash_golden.json:33
linkedin-seite/tests/data/hash_golden.json:52
linkedin-seite/tests/data/hash_golden.json:68
linkedin-seite/tests/data/hash_golden.json:78
linkedin-seite/tests/data/hash_golden.json:90
linkedin-seite/tests/data/hash_golden.json:102
linkedin-seite/SETUP.md:166
linkedin-seite/SETUP.md:180
linkedin-seite/SETUP.md:183
linkedin-seite/SETUP.md:184
linkedin-seite/SETUP.md:186
linkedin-seite/SETUP.md:189
linkedin-seite/tests/test_setup_guide.py:48
linkedin-seite/tests/test_setup_guide.py:60
linkedin-seite/tests/test_post_server.py:80
linkedin-seite/tests/test_post_server.py:123
linkedin-seite/tests/test_post_server.py:125
linkedin-seite/tests/test_post_server.py:176
linkedin-seite/tests/test_post_server.py:199
linkedin-seite/tests/test_post_server.py:243
linkedin-seite/tests/test_post_server.py:245
linkedin-seite/tests/test_post_server.py:341
linkedin-seite/tests/test_post_server.py:350
linkedin-seite/tests/test_post_server.py:365
linkedin-seite/tests/test_f4.py:23
linkedin-seite/tests/test_f4.py:40
linkedin-seite/tests/test_f4.py:44
linkedin-seite/tests/test_f4.py:48
linkedin-seite/tests/test_f4.py:51
linkedin-seite/tests/test_f4.py:53
linkedin-seite/tests/test_f4.py:60
linkedin-seite/tests/test_f4.py:64
linkedin-seite/tests/test_f4.py:65
linkedin-seite/tests/test_f4.py:67
linkedin-seite/tests/test_f4.py:69
linkedin-seite/tests/test_f4.py:84
linkedin-seite/tests/test_f4.py:86
linkedin-seite/tests/test_f4.py:103
linkedin-seite/tests/test_f4.py:116
linkedin-seite/tests/test_f4.py:148
linkedin-seite/tests/test_f4.py:168
linkedin-seite/tests/test_f4.py:197
linkedin-seite/tests/test_f4.py:246
linkedin-seite/tests/test_post_adapter.py:89
linkedin-seite/tests/test_post_adapter.py:184
linkedin-seite/tests/test_post_adapter.py:234
linkedin-seite/tests/test_post_adapter.py:274
linkedin-seite/tests/test_post_adapter.py:280
```

</details>
