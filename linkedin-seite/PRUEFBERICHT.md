# Prüfbericht #1237 — Stufe B1 (0.2.0, Posten nach Freigabe)

Grundlage: SEC-PASS c-6575359082 (Bedingungen 1–5), Nachtrag c-6575372963, c-6575837332
(Umfrage, Video V1–V3). Branch `feat/1237-stufe-b1` auf main `6ea970c`, eigener Worktree, zuerst
ein roter Test-Commit, dann der Bau. Kein PR, kein Merge, kein Tag. Alle LinkedIn- und
Gateway-Aufrufe der Tests gehen an lokale HTTP-Fakes (`tests/support_post.py`) auf freien Ports.

## Prüfstand

- **Zuerst rot:** Commit `test(linkedin-seite): Stufe B1 …` auf `6ea970c`. Lauf: 127 Tests, 6 Fehlschläge
  und 8 Fehler (vier neue Testmodule scheitern am fehlenden Bau: `linkedin_seite.redaktion`,
  `.gateway`, `.post_adapter`; Deploy-, Versions- und Doku-Wächter rot). Stufe A unverändert grün.
- **CI-gleich, Linux:** `python:3.12-slim` am Digest des Dockerfiles (Python 3.12.15),
  `pip install --require-hashes -r requirements.lock`, `python -m unittest discover -s tests`:
  **207 Tests OK, 0 übersprungen** (106 s); `python tests/mutations.py`: Baseline grün,
  **155 von 155 Mutationen erkannt**, Originalquellen unverändert (652 s).
- **Image-Job:** `docker build`, Tests im Image non-root 65532, read-only, ohne Netz, 512 MiB,
  1 CPU, 32 PIDs: **207 Tests OK, 52 übersprungen** (Deploy-/Release-/Setup-/PO-Skript-Tests ohne
  deren Dateien).
- **Windows, Python 3.12.9 lokal:** vollständige Suite grün.
- Vorher 115 Tests / 90 Mutationen; nachher 207 / 155 (92 neue Tests, 65 neue Pflichtmutanten).
  Drei Stufe-A-Mutanten haben neue Anker (`get-only`: die gemeinsame `check_bridge`;
  `expanded-grant`, `scope-exact-set-wire`: `set(scopes)`), Prüfung und Testziel unverändert.
- **Zweitmeinung (A13):** Design-Check nach dem roten Test (übernommen: URN im Kommentartext
  abweisen; Neustart-/Waisen-/Widerrufsgrenzen dokumentiert) und Code-Review vor dem Bericht
  (übernommen: Sperre `ergebnis_unbekannt` nach gesendetem Schlussaufruf, Tests zuerst rot).

## SEC-Bedingung → Code → Test (je mit Pflichtmutante in `tests/mutations.py`)

| Bedingung | Code | Test (Mutante) |
|---|---|---|
| Bed. 1 Autor nur Organisation | `post_adapter.py` `_posten` `author`, `_asset` `owner`, `run` `actor` = `cfg.organization` | `Formats.test_text_post_…`, `…single_image…`, `…own_comment…` (`b1-author-person`, `b1-owner-person`, `b1-actor-person`) |
| Bed. 1 geschlossene Liste | `source_contract.WRITE_ENDPOINTS`, `post_adapter.api` | `ClosedEndpoints.test_endpoint_table…`, `…comment_get_reactions…` (`b1-comment-get`, `b1-closed-list-check`, `b1-endpoint-table`, `b1-foreign-method`, `b1-partial-update-header`) |
| Bed. 2 Freigabe aus zweiter Kartei, alle vier Aktionen | `redaktion.freigegeben`, `post_adapter.run` | `Approval.*` (`b2-hash-mismatch`, `b2-approval-field-in-plan`, `b2-delete-without-record`) |
| Bed. 2 Hash über Inhalt und Bytes | `redaktion.canonical`/`freigabe_hash` | `test_post_domain.Hash.*` mit eingefrorenen Vektoren, `Approval.test_media_bytes_exchanged…`, `…text_changed…` (`b2-hash-over-bytes`, `b2-action-in-hash`, `b2-text-in-hash`, `b2-poll-order-in-hash`) |
| Bed. 2/SEC-Empfehlung nur SSO-Sätze | `redaktion.freigegeben` (`via`, `updated_via`) | `Approval.test_only_sso_written…`, Domäne `Approval` (`b2-sso-only`, `b2-updated-via-sso`) |
| Bed. 3 W2 nur lesend, werkzeuggenau | `gateway.GatewayClient.call` (geschlossene Liste aus `WriteConfig.tools`) | `test_post_gateway.*` JSON und SSE (`b3-closed-tools`) |
| Bed. 3 nur `/mcp` | `redaktion.gateway_url` | `WriteConfiguration.test_gateway_url_only_mcp…` (`b3-mcp-only`) |
| Bed. 3 Pfad der Medien | `redaktion.check_datei` in `parse_medien` und `gateway.datei` | `Gateway.test_paths_rejected_before_any_call`, `Requests.test_media_names…` (`b3-path-before-call`, `b3-plan-path-guard`) |
| Bed. 4 eigenes Credential, genau `w_organization_social` | `source_contract.WRITE_SCOPES`, `zustimmung.py --schreiben`, `oauth.validate_write_identity`, `server.load_write_side` | `test_post_consent.*`, `Tokens.*`, `WriteToken.*` (`b4-write-scope`, `b4-consent-scope-string`, `b4-write-identity-marker`, `b4-write-loader`, `b4-write-place`, `b4-write-refresh-scope`) |
| Bed. 4 Token nie getauscht | getrennte Prozesse/Stellen | `TokenSeparationOnTheWire.*` (`b4-token-exchange`) |
| Bed. 5 Tagesgrenze 5 über alle Aktionen | `post_pool.WritePool._count` (Serverprozess), `post_adapter._begin` | `Flow.test_five_write_actions…`, `ClosedEndpoints.test_action_is_counted…` (`b5-daily-cap`, `b5-count-before-request`) |
| Bed. 5 Idempotenz `eintrag_id`+`hash` | `post_pool._reply` (`hash` → `fertig`/`weiter`), `done` | `Flow.test_post_then_idempotent…`, `Background.*` (`b5-idempotency`, `b5-one-job`) |
| Bed. 5 kein Doppelpost nach unklarem Ausgang | `post_adapter.api` (`final_request` vor `post_create`/`comment_create`), `post_pool.unbekannt` | `Flow.test_unknown_outcome…`, `…failure_before_the_final_request…` (`b5-unknown-outcome`, `b5-final-marker`) |
| Bed. 5 Log nur IDs/URN/Hash/Größe | `post_pool._trace`, `job_main` ohne Logging | `LogCanary.*` (echter Subprozess, laute Transport-Naht), `Flow…` (`b5-job-log-silence`, `b5-trace-text`) |
| V1 Upload-Host, https, kein Port/`@`, kein Redirect, Token nur wo nötig | `redaktion.upload_ziel`, `post_adapter.upload`, `oauth.NoRedirect`, `source_contract.UPLOAD_TOKEN` | `UploadTarget.*`, `Formats.test_video…` (`v1-host`, `v1-https`, `v1-userinfo-port`, `v1-path-prefix`, `v1-no-redirect`, `v1-check-before-put`, `v1-video-without-token`) |
| V2 Freigabe vor `initializeUpload` | `post_adapter.run` (Freigabe vor `_begin`) | `Approval.test_hash_mismatch…` (Video, null Aufrufe) (`v2-approval-before-upload`) |
| V2 einmal lesen, Upload aus der Kopie, Kopie gelöscht | `gateway.Medium`, `post_adapter._video`/`_wipe` | `UploadBytes.test_file_exchanged…`, `…private_copies…` (`v2-read-once`, `v2-copy-wiped`) |
| V2 Teile lückenlos, ≤ ceil(Größe/4 MiB) | `redaktion.video_teile` | `UploadBytes.test_part_list…` (`v2-parts-gap-overlap`, `v2-parts-count`, `v2-parts-cover`) |
| V2 Größengrenze vor dem Lesen | `gateway.zu_gross`, Antwortdeckel in `_post` | `UploadBytes.test_oversized…`, `Gateway.test_size_limit…` (`v2-size-limit`) |
| V3 Status begrenzt, Post nur bei `AVAILABLE`, zählt einmal | `post_adapter._video` | `VideoStatus.*`, `Flow.test_aborted_video…` (`v3-max-wait`, `v3-available-only`, `v3-failed-status`, `b5-video-counts-once`) |
| Umfrage-Grenzen, keine Medien | `redaktion.parse_umfrage`, `check_format` | `Polls.*` (`poll-question-length`, `poll-option-count`, `poll-duration`, `poll-no-media`) |
| Keine Erwähnung | `redaktion.little`, Kommentar ohne URN | `LittleText.*`, `Entries.test_comment_text…` (`little-escape`, `comment-urn-refused`) |

## Herstellerbelege (Microsoft Learn, LinkedIn Marketing API, `?view=li-lms-2026-10`)

| Endpunkt / Aussage | Doku-Seite | Wortlaut (Auszug) |
|---|---|---|
| `POST /rest/posts`, 201, `x-restli-id` | shares/posts-api | „You should get a 201 response and the response header x-restli-id contains the Post ID“ |
| `PARTIAL_UPDATE`, 204, nur `commentary` u. a. | shares/posts-api | „the header `X-RestLi-Method` must be included in the request and set to `PARTIAL_UPDATE`“; „A successful response returns a `204` code.“ |
| `DELETE /rest/posts/{urn}`, idempotent | shares/posts-api | „Post deletions are idempotent. Deletion requests for a previously deleted UGC Post will return a `204` code“ |
| kein DRAFT beim Anlegen | shares/post-api-schema | „PUBLISHED is the only accepted field during creation.“; „DRAFT - Represents content that's accessible only to the author and is not yet published.“ |
| `images?action=initializeUpload`, Upload mit Token | shares/images-api, shares/vector-asset-api | „The upload call requires a valid OAuth token in the 'Authorization' header. This is different than the upload video call which doesn't accept an OAuth token.“ |
| `documents?action=initializeUpload`, `title` Pflicht | shares/documents-api, shares/post-api-schema | „The media title (Required field for Documents API).“ |
| Video init/PUT/finalize/Status | shares/videos-api | „The first entry is byte range: 0 - 4194303, the next one with 4194304 - 8388607“; Status `PROCESSING`, `PROCESSING_FAILED`, `AVAILABLE`, `WAITING_UPLOAD` |
| MultiImage 2–20 | shares/multiimage-post-api | „minimum of 2 images and maximum of 20 images“ |
| Umfrage-Grenzen | shares/poll-post-api | Frage „Max length of 140 characters“, Optionen „at least two. Maximum 4 options“, „Maximum length of 30 characters“, „Updating poll content is not allowed.“ |
| Kommentar als Organisation | shares/comments-api | `POST https://api.linkedin.com/rest/socialActions/{shareUrn\|ugcPostUrn\|commentUrn}/comments`, „actor … organization URN or person URN“ |
| Upload-Host | images-/documents-/videos-api | Beispiele `https://www.linkedin.com/dms-uploads/…` |
| Little-Text | shares/little-text-format | „All reserved characters need to be escaped with a backslash, even if those characters are not used in one of the supported elements or templates.“ |
| Scope | shares/posts-api | „w_organization_social — Post, comment, and like posts on behalf of an organization.“ |

## Scope-Befund

`w_organization_social` deckt laut Posts-, Images-, Documents- und Videos-API Posten, Bearbeiten,
Löschen und die Uploads. Für Kommentare widerspricht sich die Doku: die Posts-API nennt „comment“
unter `w_organization_social`, die Comments-API-Seite listet nur `w_organization_social_feed`.
Nicht still erweitert; angefordert wird genau `w_organization_social`. Live-Gate.

## Befund `UpdatedViaLabel`

Die Kartei-Antwort liefert die Schreib-Herkunft: `via` (Zugang der Anlage) und `updated_via`
(Zugang der letzten Änderung), `internal/kartei/render.go` `recordPayload`; für SSO lautet der Wert
`SSO` (`cmd/jnpt/principal_teams.go` `principalViaSSO`), für Tokens das frei vergebene Token-Label.
Umgesetzt: beide müssen `SSO` sein. Restlücke: der Kern reserviert das Label `SSO` nicht — ein Token
mit diesem Label bestünde die Prüfung. Betreiberregel, kein Code.

## Abweichungen und Grenzen

- SEC Bed. 3 nennt `kartei_redaktionsplan_get`/`kartei_redaktion_freigabe_search` und
  `<einstieg>_read_file`. Am Kern heißen die Werkzeuge `redaktionsplan_get`,
  `redaktion_freigabe_search` (`karteiQualifiedName`, kein `kartei_`-Präfix) und für Bytes
  `<ablage>_get_file` (`read_file` liefert nur Text).
- Hash über den SHA-256 jeder Datei statt über die Rohbytes selbst: gleichwertige Bindung, nicht
  wortgleich mit Bed. 2.
- Tageszähler und Idempotenz nur im RAM; Neustart setzt zurück (wie Stufe A). `post_urn` schreibt die
  KI und ist deshalb kein Schutz gegen sie.
- Zusätzlich zur Aktionsgrenze 45 LinkedIn-HTTP-Versuche je UTC-Tag für Schreibaufträge
  (Development Tier 100 je Mitglied); Betriebsgrenze, keine SEC-Bedingung.

## Offene Live-Gates

Siehe README „Live-Gates Stufe B1“.

---


# Stufe A (Bestand, unverändert)


Basis: `77830b0c52cfaef2b257c7616e5a3e32af37ba9e`. SEC-PASS laut Auftrag für
658bf9a..77830b0 (#1237 c-6509587143). Genau ein weiterer lokaler Commit, kein Amend/Push/PR/Tag.
Branch `feat/1237-linkedin-seite`, eigener Worktree.

## Prüfstand

- **Zuerst rot:** drei neue Tests auf 77830b0, zwei Assertion-Fehler (belegter IPv6-Port und sonstiger Bindefehler starten trotzdem den Browser), ein erwarteter Formfehler bei gültigem Paging. Kein Import-/Syntaxfehler.
- Vollständige Suite: **89 Tests grün, 0 übersprungen**, `python -m unittest` im gehärteten Python-3.12-Container mit vollständigem Repo-Mount.
- **77/77 Mutationen erkannt**, vollständige Baseline grün, Originalquellen SHA-256-identisch. Vier zusätzliche Mutanten gegenüber 77830b0, plus die separate Windows-Mutation unten. `git diff --check` grün.
- **Windows/Python 3.14: 3 neue Tests und 9 bestehende Consent-Tests grün.** Zusätzliche Windows-Mutation: exklusive Bindung in isolierter Kopie entfernt → Port-Kollisionstest rot (eine Assertion), Originalquellen SHA-256-unverändert.
- Docker-Build erfolgreich; Hash-Lock/Abhängigkeiten unverändert. Image-Suite: **89 entdeckt, 51 ausgeführt/grün, 38 erwartete Skips** (15 PO-/Loopback-, 1 Doku-, 5 Deploy-, 14 Release-, 3 Setup-Tests ohne deren Dateien). Container: read-only, UID 65532, keine Capabilities/externes Netz, no-new-privileges, 512 MiB/1 CPU/32 PIDs.
- Ein Zwischenlauf scheiterte am TIME_WAIT-Zustand des Test-Sockets; der tatsächliche Portbelegungs-Fake nutzt unter Linux SO_REUSEADDR und unter Windows SO_EXCLUSIVEADDRUSE. Die vor der Windows-Korrektur abgebrochene Mutationsrunde zählt nicht als Endnachweis.

## Diff dieses Nachtrags

- **Paging:** einzige pauschale Produktionssperre `PAGING in raw` in `linkedin_adapter.py:stats` entfernt. Gemeinsame Prüfung `ensure_complete` bleibt: paging muss Objekt sein, links fehlt oder ist Liste; next-Link und unbekannte Formen → 502. Tests prüfen leere und nichtleere Einzelbeitragsantworten mit fehlenden/leeren links, next-Link und falschen Typen. Alle Fundstellen repo-weit geprüft; die pauschale Sperre kommt nur noch im absichtlich roten Mutanten vor. Oberhalb des Paging-Objekts ist ein unmodelliertes next-Feld weiterhin eine unbekannte Quellform.
- **H1b:** EADDRINUSE bricht vor Browserstart/Token-Tausch mit „IPv6-Port [::1]:8765 ist belegt; Zustimmung abgebrochen.“ ab und schließt IPv4. Andere Fehler brechen ebenfalls ab. Fallback ausschließlich bei EAFNOSUPPORT/EADDRNOTAVAIL/EPROTONOSUPPORT, jeweils getestet. Unter Windows exklusive Socket-Bindung, damit Portkollisionen zuverlässig erkannt werden. Echter belegter ::1-Port wird auf Linux und Windows geprüft.
- README, SETUP, CLAUDE, CHANGELOG, ARCHITEKTUR, Tests, Mutationen und Invariantenbeleg nachgezogen. Redirect, Scopes und alle übrigen SEC-Grenzen bleiben bestehen.

## Fortgeltender SEC-Vertrag mit aktueller Paging-Präzisierung

| SEC-Punkt | Befund auf 08fdf61 | Korrektur / Nachweis |
|---|---|---|
| (3) Nullinteraktion | Einzelbeitragsantwort streng zuordnen | Nur gültige leere `elements` des festen Einzel-GET; `paging` mit fehlenden/leeren links gültig, next-Link/unbekannte Form abweisen; Org-/ID-Metadaten müssen passen. Fremde ID, fehlende ID einer vorhandenen Zeile, fremde Org und zweite Zeile → 502 `unexpected_source_shape`. Auch alternative ID-Felder dürfen nicht widersprechen. |
| (4) Negative Werte | Alle Zähler wurden einzeln gekappt | Nur `likeCount` darf negativ sein. Gemeinsame Summenprojektion addiert zuerst alle Werte, kappt erst negative Summe, dann `korrigiert: true`. Impressionen/Klicks/Kommentare/Shares, sämtliche Follower-Zähler und Seitenaufrufe bleiben strikt nichtnegativ. |
| (1) networkSizes / Scopes | Fester GET/Pfad/edgeType und Konfigurationsorganisation bereits richtig | Beibehalten; Test erzwingt dieselbe Anfrage selbst bei fremder Org/edgeType als intern übergebenen Parametern. Scope-Menge exakt gepinnt; Wissensbeleg unten. |
| (5) Personenbezug | Nur Text/Attribute wurden untersucht | Rekursive Untersuchung des gesamten Beitragsobjekts vor Minimierung, einschließlich unbekannter Felder/Schlüssel. Member-/Person-URN oder Mention → `text: null`, `text_ausgelassen: erwaehnung`; Kennzahlen bleiben. Echte Dienstantwort und Logs enthalten weder Canary-Name noch Personen-URN. |
| H1 | Erste gültige Anfrage, fehlerhafte ließen die Zustimmung offen | Zwei reine Loopback-Listener, gemeinsam genau eine Anfrage; erste behandelt und beide geschlossen, auch bei falschem State/Pfad. Fehler → neu starten. Redirect unverändert localhost:8765/linkedin/callback; IPv4-Fallback nur bei fehlendem IPv6, belegter Port/andere Fehler brechen ab. |

Die leere Statistik hat selbst keine Zeile zur Organisationsprüfung. Ihre Zuordnung erfolgt
über den festen GET für die konfigurierte Organisation und genau eine geprüfte Beitrags-ID;
vorhandene widersprechende Org-/ID-Metadaten sind Fehler. Keine fremde oder zweite Zeile wird
als „fehlend“ umgedeutet. Kein alternativer/freier Lesepfad und kein Schreibweg hinzugefügt.

Die Beiträge liefern weiterhin Lebenszeitsummen. Für die gemeinsame echte Adapterprojektion
belegt eine HTTP-Probe Tageswerte **−3 + 5 = 2**, **−5 + 3 → 0 mit Korrekturflag** und
**−3 + 3 = 0 ohne Korrekturflag**. Mutant „Tage einzeln kappen“ muss rot werden.
Dafür wurde keine neue öffentliche Zeitraum-Beitragsroute eingeführt.

## Beleg zu (1): networkSizes und die gepinnten Scopes — live zu belegen

**Wissensbeleg aus der bekannten LinkedIn-Herstellerdokumentation, kein aktuell abgerufener
oder am Tenant bestätigter Beleg:** Der Dokuvertrag „Organization Network Size“, Abschnitt
Berechtigungen, ordnet das Lesen der Organisations-Followerzahl über `networkSizes` mit
`edgeType=COMPANY_FOLLOWED_BY_MEMBER` der Organisationsverwaltung mit
`rw_organization_admin` zu. Dieser Scope ist bereits Teil des gepinnten Grants;
`r_organization_social` bleibt für die Social-Leseoperationen erforderlich. Für diesen
Organisations-GET ist nach diesem bekannten Vertrag kein zusätzlicher Scope nötig.
Die zustimmende Person benötigt die passende Admin-Rolle für genau diese Organisation;
App-/Produkt-/Tier-Freigabe sind zusätzliche Voraussetzungen, keine erweiterten Scopes.

**Live zu belegen:** Auf der freigegebenen App/Version mit exakt
`rw_organization_admin r_organization_social` den festen eigenen Organisations-GET prüfen:
URL-kodierte URN, fester edgeType, Antwortfeld `firstDegreeSize`, Admin-Rolle und Grant.
Die synthetischen Tests beweisen Anfrageform und unveränderte Scope-Menge, keine tatsächliche
LinkedIn-Berechtigung. Widerspricht die Live-Abnahme diesem Wissensstand oder verlangt sie
einen zusätzlichen Scope, bleibt die Freigabe offen: kein Scope-Fallback, kein automatisches
Nachfordern; Entscheidung des Auftraggebers als FRAGE einholen. Im Code steht kein weiterer Scope.

## Invarianten an allen Orten (Pflicht 1)

Die vollständigen repo-weiten Treffer mit **Datei:Zeile** stehen in
[pruefung/invarianten.txt](pruefung/invarianten.txt). Suche 1 erfasst Invariantentexte und
Hausmuster; Suche 2 alle LinkedIn-Kompositionsstellen im gesamten Repo; Suche 3 zusätzliche
Berechtigungs-, Transport-, OAuth-, Logging- und Quotenwege im Modul. Suche 4 erfasst
repo-weit die geänderten Mess-/Callback-/Minimierungsinvarianten und alte Vertragsnamen.
Die vier Suchbefehle stehen mit ihren vollständigen Treffern in der Datei.

| Invariante / zusätzlicher Weg | Umsetzung und mitgezogene Doku |
|---|---|
| Ein Bridge-Key, feste GET-Routen, keine Fremdorganisation | `server.py:create_app`, `domain.py:Config/parse_request`, `linkedin_adapter.py:get/stats/run`; README Vertrag und SEC, CLAUDE, SETUP, ARCHITEKTUR und beide Root-READMEs |
| Zweiter Berechtigungsweg OAuth-Code-Tausch und Refresh | `source_contract.py:SCOPES`, `oauth.py:token_result/Tokens.access`, `tools/zustimmung.py:authorization/consent`; README SEC und Rotation, SETUP, CLAUDE |
| 50 einzelne Versuche vor HTTP, nicht pro Bericht | `oauth.py:Transport.send` → Worker-Pipe → `workers.py:WorkerPool._permit`; README/SETUP/CLAUDE/CHANGELOG/ARCHITEKTUR/Root-READMEs |
| Projektion ohne Personenaufschlüsselung | `linkedin_adapter.py:minimize/minimize_posts/stats/share_sums/run`, gesamtes Beitragsobjekt einschließlich unbekannter verschachtelter Felder; README Messvertrag/SEC, CLAUDE |
| 7 UTC-Tage, Follower bis heute−2, Seite bis gestern, 90 Tage, 10 Beiträge, 120 Zeichen | `domain.py:parse_request`, `linkedin_adapter.py:stats/run`; README Routentabelle/Messvertrag, CLAUDE |
| networkSizes, Nullinteraktion, negative Like-Summe; übrige Zähler strikt, Dual-Loopback/eine Anfrage insgesamt | `source_contract.py`, `linkedin_adapter.py`, `tools/zustimmung.py`; README/SETUP/CLAUDE/CHANGELOG/ARCHITEKTUR; `test_acceptance.py`, Adapter-/Boundary-/Consent-/Logtests |
| Tokenfristen, Warnungen, keine Persistierung | `oauth.py:validate_identity/token_result/Tokens/warning`, `workers.py` Fristmetadaten, `server.py:warnings`; README/SETUP/CLAUDE |
| Fehler-/Logschutz einschließlich Parser und Bibliotheken | `safe_http.py`, `server.py:errors/main`, `workers.py:worker_main`, `oauth.py:Transport.send`, `tools/zustimmung.py:Handler/main`; README/CLAUDE/SETUP |
| Zwei Prozessworker, Queue/Minute/Timeout/Kill | `workers.py`, Disconnect in `server.py`, `safe_http.py:SafeAppRunner`; README Laufzeit, CLAUDE |
| Zwei Tresorstellen, kein Secret unter /etc oder /opt | `server.py` feste Pfade, Compose, Deskriptor, tmpfiles, beide Key-Units; README Betreiberschritte, SETUP, CLAUDE, ARCHITEKTUR |
| Release/Katalog-Anleitung | beide neuen Workflows, Dockerfile/Lock, README/CLAUDE/CHANGELOG/SETUP, Root-Releases; `test_release_workflow.py`, `test_setup_guide.py`, `test_deploy_files.py` |

Ergebnis: keine zusätzliche LinkedIn-Integration und kein weiterer LinkedIn-Adapter im Repo.
Die Google-Schreibwege gehören ausschließlich zum unveränderten Google-Modul. Die neue
Implementierung übernimmt sie nicht. Einziger LinkedIn-POST ist der feste OAuth-Tokenendpunkt;
Code-Tausch erfolgt nur am PO-Rechner. Tests verwenden separate Loopback-Transportklassen,
die weder Konfiguration noch Dienstimage anbieten. Werkzeugrechte/Connector-Auslieferung
liegen im Produktrepo und sind hier nicht implementiert.

## Negativkontrollen und Mutationen (Pflicht 2)

Grüne vollständige Baseline, danach je eine Sperre in einer isolierten Kopie entfernen;
der angegebene Test muss rot werden. Import-/Syntaxfehler und äußerer Timeout zählen nicht.
SHA-256 der Originalquellen vor/nach der Runde bleibt gleich. Echter HTTP-Transport,
echter Service/Worker und echte Loopback-Sockets; keine bloßen Adapter-Mocks.

| Kurz entfernte/veränderte Sperre | Tatsächlich ausgeführter Negativtest |
|---|---|
| `bridge-key` | `test_boundary.Boundary.test_bridge_missing_wrong_duplicate_never_reach_adapter` |
| `duplicate-auth` | `test_boundary.Boundary.test_bridge_missing_wrong_duplicate_never_reach_adapter` |
| `get-only` | `test_boundary.Boundary.test_no_write_method_no_generic_or_person_routes` |
| `get-body` | `test_boundary.Boundary.test_body_rejected_and_local_health_does_not_use_api` |
| `private-bind` | `test_contract.Contract.test_bind_rejects_wildcard_loopback_ipv6_and_public` |
| `unknown-org-input` | `test_boundary.Boundary.test_org_paths_parameters_duplicates_dates_rejected` |
| `duplicate-input` | `test_boundary.Boundary.test_org_paths_parameters_duplicates_dates_rejected` |
| `period-cap` | `test_boundary.Boundary.test_org_paths_parameters_duplicates_dates_rejected` |
| `future-date` | `test_boundary.Boundary.test_org_paths_parameters_duplicates_dates_rejected` |
| `post-input-cap` | `test_contract.Contract.test_post_limit_and_unknown_parameters` |
| `scope-set` | `test_adapter.Adapter.test_scopes_pinned_and_expanded_grant_rejected_on_wire` |
| `expanded-grant` | `test_adapter.Adapter.test_scopes_pinned_and_expanded_grant_rejected_on_wire` |
| `daily-cap` | `test_boundary.Workers.test_daily_cap_counts_individual_gets_and_failures_parent_utc` |
| `count-before-http` | `test_boundary.Workers.test_cap_stops_partway_posts_and_counts_token_exchange` |
| `minute-throttle` | `test_boundary.Workers.test_minute_throttle_and_closed_pool` |
| `bounded-queue` | `test_boundary.Workers.test_two_workers_four_queue_slots` |
| `kill-real-work` | `test_boundary.Workers.test_timeout_kills_real_http_and_counter_survives` |
| `minimize-before-projection` | `test_adapter.Adapter.test_no_person_fields_and_breakdowns_before_projection` |
| `nested-data-minimization` | `test_adapter.Adapter.test_nested_breakdowns_discarded_by_real_adapter_before_sums` |
| `org-config-wire` | `test_adapter.Adapter.test_fixed_get_routes_org_headers_and_sums` |
| `response-org` | `test_adapter.Adapter.test_period_gaps_duplicates_foreign_org_and_next_page_rejected` |
| `period-coverage` | `test_adapter.Adapter.test_period_gaps_duplicates_foreign_org_and_next_page_rejected` |
| `no-partial-pagination` | `test_adapter.Adapter.test_period_gaps_duplicates_foreign_org_and_next_page_rejected` |
| `numeric-no-null-fallback` | `test_adapter.Adapter.test_missing_invalid_metrics_no_zero_or_replacement` |
| `post-author` | `test_adapter.Adapter.test_posts_foreign_author_bad_id_unpublished_overlimit` |
| `member-urn-in-text` | `test_logs.Logs.test_real_service_mentions_never_in_response_or_log` |
| `text-length` | `test_adapter.Adapter.test_fixed_get_routes_org_headers_and_sums` |
| `fixed-endpoints` | `test_adapter.Adapter.test_unknown_operation_and_endpoint_rejected_before_http` |
| `outbound-get-only` | `test_adapter.Adapter.test_fixed_get_routes_org_headers_and_sums` |
| `source-redirect` | `test_adapter.Adapter.test_errors_redirects_no_retry_no_raw_error` |
| `source-retry` | `test_adapter.Adapter.test_errors_redirects_no_retry_no_raw_error` |
| `post-statistics-id` | `test_sec_tightening.StrictSource.test_zero_only_valid_empty_single_post_no_paging_foreign_org_or_id` |
| `post-created-order` | `test_adapter.Adapter.test_post_statistics_id_and_created_order_fail_closed` |
| `source-byte-cap` | `test_adapter.Adapter.test_oversized_and_malformed_source` |
| `response-byte-cap` | `test_boundary.Boundary.test_output_limit` |
| `token-expired` | `test_adapter.Adapter.test_expired_without_refresh_never_calls_http` |
| `refresh-no-extension` | `test_adapter.Adapter.test_refresh_ram_only_no_extension_and_single_exchange` |
| `warning` | `test_adapter.Adapter.test_token_response_bounds_and_warning_dates` |
| `token-log-library` | `test_logs.Logs.test_real_server_worker_library_logs_redact_tokens` |
| `token-log-http` | `test_boundary.Boundary.test_source_failure_token_never_in_log_or_error` |
| `parser-redaction` | `test_boundary.Boundary.test_parser_limits_path_limit_safe_json_and_no_token_logs` |
| `consent-state` | `test_consent.Consent.test_wrong_state_duplicate_wrong_path_stop_before_exchange` |
| `consent-loopback` | `test_consent.Consent.test_listener_is_loopback_and_state_changes_each_run` |
| `consent-hard-deadline` | `test_consent.Consent.test_main_hard_deadline_exits_even_if_transport_hangs` |
| `consent-no-echo` | `test_consent.Consent.test_getpass_echo_fallback_is_rejected` |
| `consent-secret-argument-redaction` | `test_consent.Consent.test_secret_not_an_argument_stdout_json_only_no_files` |
| `network-org-config` | `test_acceptance.Manufacturer.test_network_total_fixed_encoded_org` |
| `network-org-encoding` | `test_acceptance.Manufacturer.test_network_total_fixed_encoded_org` |
| `network-edge-type` | `test_acceptance.Manufacturer.test_network_total_fixed_encoded_org` |
| `follower-two-day-lag` | `test_boundary.Boundary.test_follower_lag_and_org_override_rejected_before_worker` |
| `missing-post-zero` | `test_acceptance.Manufacturer.test_missing_post_statistics_only_documented_empty_shape` |
| `missing-post-flag` | `test_acceptance.Manufacturer.test_missing_post_statistics_only_documented_empty_shape` |
| `negative-count-clamp` | `test_acceptance.Manufacturer.test_negative_like_count_clamped_and_marked` |
| `negative-count-visible` | `test_acceptance.Manufacturer.test_negative_like_count_clamped_and_marked` |
| `mention-attributes` | `test_acceptance.Manufacturer.test_mentions_text_and_attributes_redacted_without_aborting` |
| `mention-marker` | `test_acceptance.Manufacturer.test_mentions_text_and_attributes_redacted_without_aborting` |
| `attribute-shape` | `test_acceptance.Manufacturer.test_unknown_attributes_and_negative_dates_are_not_zero` |
| `negative-date` | `test_acceptance.Manufacturer.test_unknown_attributes_and_negative_dates_are_not_zero` |
| `consent-ipv6-loopback` | `test_acceptance.DualLoopback.test_both_loopbacks_one_request_and_close` |
| `consent-ipv6-fallback` | `test_acceptance.DualLoopback.test_ipv6_failure_continues_ipv4_with_notice` |
| `consent-ipv6-notice` | `test_acceptance.DualLoopback.test_ipv6_failure_continues_ipv4_with_notice` |
| `consent-one-attempt-even-invalid` | `test_sec_tightening.FirstRequest.test_invalid_first_request_on_either_socket_closes_both` |
| `consent-only-one` | `test_acceptance.DualLoopback.test_two_valid_callbacks_only_one_exchange` |
| `post-no-top-level-next` | `test_sec_tightening.StrictSource.test_zero_only_valid_empty_single_post_no_paging_foreign_org_or_id` |
| `post-envelope-org` | `test_sec_tightening.StrictSource.test_zero_only_valid_empty_single_post_no_paging_foreign_org_or_id` |
| `post-envelope-id` | `test_sec_tightening.StrictSource.test_zero_only_valid_empty_single_post_no_paging_foreign_org_or_id` |
| `post-second-row` | `test_sec_tightening.StrictSource.test_zero_only_valid_empty_single_post_no_paging_foreign_org_or_id` |
| `post-other-id` | `test_sec_tightening.StrictSource.test_zero_only_valid_empty_single_post_no_paging_foreign_org_or_id` |
| `zero-only-single-post` | `test_sec_tightening.StrictSource.test_zero_only_valid_empty_single_post_no_paging_foreign_org_or_id` |
| `negative-only-likes` | `test_sec_tightening.StrictSource.test_only_like_count_may_be_negative` |
| `negative-followers` | `test_acceptance.Manufacturer.test_negative_aggregate_counters_and_invalid_types_rejected` |
| `likes-sum-before-clamp` | `test_sec_tightening.StrictSource.test_like_period_sum_before_clamp_real_transport` |
| `person-reference-entire-object` | `test_logs.Logs.test_real_service_mentions_never_in_response_or_log` |
| `post-valid-paging` | `test_final_sec.Paging.test_single_post_empty_or_missing_links_valid_next_and_bad_shapes_rejected` |
| `paging-shape` | `test_final_sec.Paging.test_single_post_empty_or_missing_links_valid_next_and_bad_shapes_rejected` |
| `ipv6-occupied-port` | `test_final_sec.IPv6Failure.test_actual_ipv6_port_in_use_aborts_before_browser_or_token` |
| `ipv6-only-unavailable-fallback` | `test_final_sec.IPv6Failure.test_unknown_ipv6_error_does_not_fall_back` |
| `scope-comma-separator` | `test_oauth_toleranz.ScopeSeparators.test_comma_separated_as_sent_by_linkedin` |
| `scope-only-comma-and-whitespace` | `test_oauth_toleranz.ScopeSeparators.test_only_comma_and_whitespace_separate` |
| `scope-empty-parts` | `test_oauth_toleranz.ScopeSeparators.test_comma_with_spaces_and_mixed_separators` |
| `scope-exact-set-wire` | `test_oauth_toleranz.RealLinkedInShapeThroughRefreshPath.test_wrong_scope_on_the_refresh_path_still_fails_closed_with_one_call` |
| `lifetime-cap` | `test_oauth_toleranz.LifetimeTolerance.test_observed_linkedin_overshoot_is_capped_at_exactly_365_days` |
| `lifetime-no-overshoot-tolerance` | `test_oauth_toleranz.LifetimeTolerance.test_observed_linkedin_overshoot_is_capped_at_exactly_365_days` |
| `lifetime-unbounded-overshoot` | `test_oauth_toleranz.LifetimeTolerance.test_refresh_boundary_cap_plus_one_day_capped_beyond_rejected` |
| `lifetime-positive` | `test_oauth_toleranz.LifetimeTolerance.test_refresh_invalid_types_and_values_rejected` |
| `lifetime-int-type` | `test_oauth_toleranz.LifetimeTolerance.test_refresh_invalid_types_and_values_rejected` |
| `access-lifetime-capped` | `test_oauth_toleranz.LifetimeTolerance.test_access_overshoot_is_capped_at_exactly_60_days` |
| `refresh-lifetime-capped` | `test_oauth_toleranz.LifetimeTolerance.test_observed_linkedin_overshoot_is_capped_at_exactly_365_days` |
| `secret-control-character` | `test_consent.Consent.test_control_character_in_secret_is_rejected_with_german_message_and_nothing_is_sent` |

0.1.1 (JanuaPort/januaport#1237): die zwölf letzten Zeilen belegen Komma-Scope, gedeckelte
Laufzeit-Überschreitung (bis Höchstwert + 1 Tag) und die Steuerzeichen-Abweisung im
Zustimmungsskript. Der Anker von `expanded-grant` folgt dem neuen Aufruf `scope_set`.

Zusätzlich unter Windows: SO_EXCLUSIVEADDRUSE-Block entfernt;
`test_final_sec.IPv6Failure.test_actual_ipv6_port_in_use_aborts_before_browser_or_token`
wird rot. Isolierte Kopie, Originalquellen SHA-256-identisch.

## Alle Namen „aus Herstellerdoku, live zu belegen“

Einziges Quellmodul `source_contract.py`; Vokabular und Scope-Menge in diesem Nachtrag
unverändert. Herstellerwissen und übermittelte SEC-Befunde sind keine Live-Abnahme.

| Konstante | Name/Wert | Fundstelle |
|---|---|---|
| `API_ROOT` | `https://api.linkedin.com/rest/` | `linkedin-seite/linkedin_seite/source_contract.py:7` |
| `TOKEN_URL` | `https://www.linkedin.com/oauth/v2/accessToken` | `linkedin-seite/linkedin_seite/source_contract.py:8` |
| `AUTHORIZE_URL` | `https://www.linkedin.com/oauth/v2/authorization` | `linkedin-seite/linkedin_seite/source_contract.py:9` |
| `REDIRECT_URI` | `http://localhost:8765/linkedin/callback` | `linkedin-seite/linkedin_seite/source_contract.py:10` |
| `SCOPES` | `('rw_organization_admin', 'r_organization_social')` | `linkedin-seite/linkedin_seite/source_contract.py:11` |
| `VERSION_HEADER` | `LinkedIn-Version` | `linkedin-seite/linkedin_seite/source_contract.py:12` |
| `PROTOCOL_HEADER` | `X-Restli-Protocol-Version` | `linkedin-seite/linkedin_seite/source_contract.py:13` |
| `PROTOCOL_VERSION` | `2.0.0` | `linkedin-seite/linkedin_seite/source_contract.py:14` |
| `PAGE` | `organizationPageStatistics` | `linkedin-seite/linkedin_seite/source_contract.py:15` |
| `FOLLOWERS` | `organizationalEntityFollowerStatistics` | `linkedin-seite/linkedin_seite/source_contract.py:16` |
| `SHARES` | `organizationalEntityShareStatistics` | `linkedin-seite/linkedin_seite/source_contract.py:17` |
| `POSTS` | `posts` | `linkedin-seite/linkedin_seite/source_contract.py:18` |
| `NETWORK` | `networkSizes` | `linkedin-seite/linkedin_seite/source_contract.py:19` |
| `EDGE_TYPE` | `edgeType` | `linkedin-seite/linkedin_seite/source_contract.py:20` |
| `FOLLOWED_BY_MEMBER` | `COMPANY_FOLLOWED_BY_MEMBER` | `linkedin-seite/linkedin_seite/source_contract.py:21` |
| `NETWORK_SIZE` | `firstDegreeSize` | `linkedin-seite/linkedin_seite/source_contract.py:22` |
| `Q` | `q` | `linkedin-seite/linkedin_seite/source_contract.py:23` |
| `ORGANIZATION` | `organization` | `linkedin-seite/linkedin_seite/source_contract.py:24` |
| `ENTITY` | `organizationalEntity` | `linkedin-seite/linkedin_seite/source_contract.py:25` |
| `AUTHOR` | `author` | `linkedin-seite/linkedin_seite/source_contract.py:26` |
| `INTERVALS` | `timeIntervals` | `linkedin-seite/linkedin_seite/source_contract.py:27` |
| `INTERVAL_FORMAT` | `(timeGranularityType:DAY,timeRange:(start:{start},end:{end}))` | `linkedin-seite/linkedin_seite/source_contract.py:28` |
| `SHARE_IDS` | `shares` | `linkedin-seite/linkedin_seite/source_contract.py:29` |
| `UGC_IDS` | `ugcPosts` | `linkedin-seite/linkedin_seite/source_contract.py:30` |
| `COUNT` | `count` | `linkedin-seite/linkedin_seite/source_contract.py:31` |
| `START` | `start` | `linkedin-seite/linkedin_seite/source_contract.py:32` |
| `SORT` | `sortBy` | `linkedin-seite/linkedin_seite/source_contract.py:33` |
| `CREATED_SORT` | `CREATED` | `linkedin-seite/linkedin_seite/source_contract.py:34` |
| `SHARE_ID` | `share` | `linkedin-seite/linkedin_seite/source_contract.py:35` |
| `UGC_ID` | `ugcPost` | `linkedin-seite/linkedin_seite/source_contract.py:36` |
| `ELEMENTS` | `elements` | `linkedin-seite/linkedin_seite/source_contract.py:37` |
| `PAGING` | `paging` | `linkedin-seite/linkedin_seite/source_contract.py:38` |
| `LINKS` | `links` | `linkedin-seite/linkedin_seite/source_contract.py:39` |
| `REL` | `rel` | `linkedin-seite/linkedin_seite/source_contract.py:40` |
| `NEXT` | `next` | `linkedin-seite/linkedin_seite/source_contract.py:41` |
| `INTERVAL` | `timeRange` | `linkedin-seite/linkedin_seite/source_contract.py:42` |
| `END` | `end` | `linkedin-seite/linkedin_seite/source_contract.py:43` |
| `PAGE_STATS` | `totalPageStatistics` | `linkedin-seite/linkedin_seite/source_contract.py:44` |
| `VIEWS` | `views` | `linkedin-seite/linkedin_seite/source_contract.py:45` |
| `ALL_VIEWS` | `allPageViews` | `linkedin-seite/linkedin_seite/source_contract.py:46` |
| `PAGE_VIEWS` | `pageViews` | `linkedin-seite/linkedin_seite/source_contract.py:47` |
| `SHARE_STATS` | `totalShareStatistics` | `linkedin-seite/linkedin_seite/source_contract.py:48` |
| `IMPRESSION` | `impressionCount` | `linkedin-seite/linkedin_seite/source_contract.py:49` |
| `CLICK` | `clickCount` | `linkedin-seite/linkedin_seite/source_contract.py:50` |
| `REACTION` | `likeCount` | `linkedin-seite/linkedin_seite/source_contract.py:51` |
| `COMMENT` | `commentCount` | `linkedin-seite/linkedin_seite/source_contract.py:52` |
| `SHARE` | `shareCount` | `linkedin-seite/linkedin_seite/source_contract.py:53` |
| `FOLLOWER_GAIN` | `followerGains` | `linkedin-seite/linkedin_seite/source_contract.py:54` |
| `ORGANIC_GAIN` | `organicFollowerGain` | `linkedin-seite/linkedin_seite/source_contract.py:55` |
| `PAID_GAIN` | `paidFollowerGain` | `linkedin-seite/linkedin_seite/source_contract.py:56` |
| `ID` | `id` | `linkedin-seite/linkedin_seite/source_contract.py:57` |
| `CREATED` | `createdAt` | `linkedin-seite/linkedin_seite/source_contract.py:58` |
| `TEXT` | `commentary` | `linkedin-seite/linkedin_seite/source_contract.py:59` |
| `ATTRIBUTES` | `attributes` | `linkedin-seite/linkedin_seite/source_contract.py:60` |
| `MENTION_MARKERS` | `('urn:li:member:', 'urn:li:person:', 'memberattributedentity', 'mention', '@[')` | `linkedin-seite/linkedin_seite/source_contract.py:62` |
| `LIFECYCLE` | `lifecycleState` | `linkedin-seite/linkedin_seite/source_contract.py:63` |
| `PUBLISHED` | `PUBLISHED` | `linkedin-seite/linkedin_seite/source_contract.py:64` |
| `CLIENT_ID` | `client_id` | `linkedin-seite/linkedin_seite/source_contract.py:65` |
| `CLIENT_SECRET` | `client_secret` | `linkedin-seite/linkedin_seite/source_contract.py:66` |
| `ACCESS_TOKEN` | `access_token` | `linkedin-seite/linkedin_seite/source_contract.py:67` |
| `EXPIRES_IN` | `expires_in` | `linkedin-seite/linkedin_seite/source_contract.py:68` |
| `REFRESH_TOKEN` | `refresh_token` | `linkedin-seite/linkedin_seite/source_contract.py:69` |
| `REFRESH_EXPIRES_IN` | `refresh_token_expires_in` | `linkedin-seite/linkedin_seite/source_contract.py:70` |
| `TOKEN_TYPE` | `token_type` | `linkedin-seite/linkedin_seite/source_contract.py:71` |
| `SCOPE` | `scope` | `linkedin-seite/linkedin_seite/source_contract.py:72` |
| `GRANT_TYPE` | `grant_type` | `linkedin-seite/linkedin_seite/source_contract.py:73` |
| `AUTH_CODE` | `authorization_code` | `linkedin-seite/linkedin_seite/source_contract.py:74` |
| `CODE` | `code` | `linkedin-seite/linkedin_seite/source_contract.py:75` |
| `STATE` | `state` | `linkedin-seite/linkedin_seite/source_contract.py:76` |
| `REDIRECT` | `redirect_uri` | `linkedin-seite/linkedin_seite/source_contract.py:77` |
| `RESPONSE_TYPE` | `response_type` | `linkedin-seite/linkedin_seite/source_contract.py:78` |
| `CODE_CHALLENGE` | `code_challenge` | `linkedin-seite/linkedin_seite/source_contract.py:79` |
| `CODE_CHALLENGE_METHOD` | `code_challenge_method` | `linkedin-seite/linkedin_seite/source_contract.py:80` |
| `CODE_VERIFIER` | `code_verifier` | `linkedin-seite/linkedin_seite/source_contract.py:81` |
| `S256` | `S256` | `linkedin-seite/linkedin_seite/source_contract.py:82` |

## Offene Live-Fragen

FRAGE: Bestätigt die freigegebene App/Version networkSizes mit genau den beiden gepinnten Scopes und der konfigurierten Organisations-Admin-Rolle? Wissensbeleg siehe oben; zusätzliche Scopes werden nicht implementiert.

FRAGE: Bestätigt die Live-Probe die Zweitage-Verzögerung der Follower, gültige leere Einzel-Share-Antworten mit gültigem Paging ohne next-Link, nur bei likeCount negative Werte und die konkreten Mention-Formen? Andere Formen bleiben fail-closed.

FRAGE: Deckt likeCount die gewünschte Reaktionssumme ab und bestätigt der Hersteller die Tagesabdeckung, Zugangssemantik sowie sortBy=CREATED? Keine Ersatzmetrik oder andere Sortierung.

FRAGE: Unterstützt diese App PKCE S256 und Refresh-Tokens; bleiben Refresh-Tokens bei Erneuerung gültig? Ohne Beleg bleibt PKCE aus, ohne Refresh gilt Neuzustimmung alle 60 Tage.

FRAGE: Wer führt die Live-Abnahme einschließlich Gateway-Rechte/Audit, Katalog und Egress-Firewall auf der Zielanlage durch?

## Stufe B später — nur benannt

Separater PO-Auftrag und SEC-Abnahme, getrennte Schreibberechtigungen, feste Post-Routen,
Vorschau/Freigabe, Inhalts-/Datenschutzprüfung, Schreibquote, Idempotenz und Änderungs-Audit.
Keine Umsetzung und keine zusätzlichen Scopes in Stufe A.

## Geänderte Dateien dieses Nachtrags

13 Dateien gegenüber 77830b0:

- `ARCHITEKTUR.md`
- `linkedin-seite/CHANGELOG.md`
- `linkedin-seite/CLAUDE.md`
- `linkedin-seite/PRUEFBERICHT.md`
- `linkedin-seite/README.md`
- `linkedin-seite/SETUP.md`
- `linkedin-seite/linkedin_seite/linkedin_adapter.py`
- `linkedin-seite/pruefung/invarianten.txt`
- `linkedin-seite/tests/mutations.py`
- `linkedin-seite/tests/test_acceptance.py`
- `linkedin-seite/tests/test_final_sec.py`
- `linkedin-seite/tests/test_sec_tightening.py`
- `linkedin-seite/tools/zustimmung.py`
