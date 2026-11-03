# Prüfbericht #1306 F4 — linkedin-seite 0.3.0

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
