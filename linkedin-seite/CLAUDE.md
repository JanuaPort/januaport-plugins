# linkedin-seite 0.3.0 — Modulvertrag (#1306 F4, #1237)

Privater Python-REST-Beisteller, Apache 2.0. PO 29.10.2026 Option (a): Stufe A (Lesen); seit
0.2.0 Stufe B1 (Posten nach Freigabe, SEC c-6575359082 und c-6575837332). Synthetisch geprüft,
Live-Abnahme offen. Kein eigener MCP-Server; für B1 ein eigener, nur lesender MCP-Client.
Root-CLAUDE.md war im Ausgangscheckout nicht vorhanden.

- `domain.py`: fail-closed Konfiguration, Organisation ausschließlich Betreiberwert;
  drei feste Berichte. UTC, 7 Tage Vorgabe, 1–90 Tage, maximal 10 Beiträge.
  Follower-Ende höchstens heute−2, Seite höchstens gestern; spätere Eingabe
  `end_date_not_available`, kein Klemmen. Vorgabe endet am letzten verfügbaren Tag.
- `source_contract.py`: EIN Ort aller Hersteller-Endpunkte, Parameter und Feldnamen,
  Kommentar „aus Herstellerdoku, live zu belegen“. Scopes exakt
  `rw_organization_admin r_organization_social` für Stufe A. Seit 0.2.0 der Block Stufe B1:
  `WRITE_SCOPES` = genau `w_organization_social` + `w_organization_social_feed` (Kommentar, Comments API
  202610), geschlossene `WRITE_ENDPOINTS` (Methode, Pfad je
  Operation, Doku-URL je Zeile), `UPLOAD_HOSTS`/`UPLOAD_PATH_PREFIX`, `UPLOAD_TOKEN` je Upload-Art,
  Video-Teilgröße, Status-Takt und Höchstzeit, Umfrage-Grenzen, reservierte Little-Text-Zeichen.
- `linkedin_adapter.py`: fünf feste REST-Endpunkte, nur einzelne GETs, kein BATCH_GET.
  Query nach Rest.li 2.0 (`restli_query`, 0.1.2): Werte in `RESTLI_STRUCTURED` (`timeIntervals`,
  `shares`, `ugcPosts`) gehen roh, nur die URN darin ist kodiert; alle übrigen Werte voll kodiert.
  Kodierte Klammern beantwortet LinkedIn mit 4xx (`source_rejected`, Showcase 02.11.).
  Follower-Gesamtzahl aus `networkSizes/{kodierte Konfigurations-URN}` mit
  `edgeType=COMPANY_FOLLOWED_BY_MEMBER`, Feld `firstDegreeSize`; Zuwachs sind Zugänge.
  Positivliste verwirft Aufschlüsselungen vor Projektion; Summen, keine Personenlisten.
  Vollständige Tagesabdeckung, feste Typen, Organisation/Beitrags-ID prüfen.
  Unbekannte Form = `unexpected_source_shape`, keine Ersatzmetriken/Null-Fallbacks.
  Ausnahme ausschließlich gültige leere Einzelbeitragsantwort: fünf Zähler 0 und
  `ohne_interaktion: true`. Paging-Objekt mit fehlenden/leeren links zulassen;
  rel=next und unbekannte Paging-Formen abweisen. Keine fremde Org/ID, keine zweite Zeile;
  sonst 502. Leere Liste ist dem festen Einzel-GET zugeordnet; Metadaten müssen passen.
  Nur likeCount darf negativ sein: erst Tageswerte summieren, dann negative Summe
  auf 0 und `korrigiert: true` am Beitrag. Alle anderen negativen Zähler sind Formfehler.
  Mention/Member-/Person-URN überall im gesamten Beitragsobjekt, auch unbekannte Felder:
  Text vor Projektion verwerfen, `text: null` und
  `text_ausgelassen: "erwaehnung"`; Bericht fortsetzen, keine Person im Log/Ergebnis.
- `oauth.py`: fester Tokenendpunkt, einziger POST (OAuth, kein Fach-Schreibweg).
  TLS-Prüfung, keine Proxies/Redirects/Retry. Access bis 60 Tage; Refresh optional bis
  365 Tage ab Erstzustimmung, Frist nie verlängern. Tokenantwort tolerant, wo LinkedIn
  ungenau ist, streng, wo es schützt (0.1.1, c-6571493150): `scope` mit Komma und/oder
  Leerraum getrennt, Menge weiter exakt gleich `SCOPES` (`scope_set`); Laufzeiten bis
  Höchstwert + 1 Tag (`OVERSHOOT_TOLERANCE_SECONDS`) werden auf 60/365 Tage gedeckelt
  (`lifetime`), darüber/nicht int/≤0 = `unexpected_source_shape`. Erneuerung fünf Minuten vor Ablauf
  beim nächsten Abruf ausschließlich RAM; kein Tresor-Rückschreiben. Warnung ab 14 Tagen
  vor Access-Ablauf ohne Refresh bzw. Refresh-Ablauf mit Refresh, nur Datum; danach
  ohne nutzbaren Access `token_abgelaufen`. Metadaten über verkürzte Frist zum Elternprozess.
- `workers.py`: Vorbild google-reporting, zwei spawn-Prozesse, vier Queueplätze,
  30 Berichte/Minute, 20 Sekunden inklusive Queue/Token. Timeout/Disconnect killt und
  reapt vor Ersatz. 50 HTTP-Versuche/UTC-Tag im SERVERPROZESS, IPC-Permit vor jedem
  GET/Refresh, Fehlschläge zählen. Neustart setzt Tageszähler zurück.
- `server.py`: Bridge-Key-Auth, feste GET-Routen, keine freien Pfade; 8 KiB Anfrage,
  Pfad 512, 512 KiB Quelle/Antwort. Feste Fehler-JSON `{"error": code}`; optional die
  definierte Tokenwarnung auf jeder Anwendungsantwort, auch lokalem `/healthz` und Fehler.
  Nur Route/Code/Dauer im Log. Gateway prüft Werkzeugrechte und Auditmetadaten.
- `safe_http.py`: gepinnte aiohttp-Naht unverändert aus google-reporting bis auf Loggername.
  SafeAppRunner Pflicht; Parserlimits 8192; Parserfehler ohne Rohbytes/Exceptions, ohne
  Tokenmetadaten. Upgrade verlangt Canary-Negativproben.
- `tools/zustimmung.py`: PO-Rechner, nicht im Image. State zufällig/geprüft, PKCE S256
  erst nach Herstellerbeleg, sonst ohne; beide Loopbacks 127.0.0.1 und ::1:8765,
  nie Wildcard. Redirect bleibt http://localhost:8765/linkedin/callback.
  Gemeinsam genau eine Anfrage; die erste beendet beide Listener auch bei falschem
  State/Pfad. Nach Abweisung neu starten; Warte-/Gesamtfrist fünf Minuten.
  Nur EAFNOSUPPORT/EADDRNOTAVAIL/EPROTONOSUPPORT: IPv4 weiter, fester stderr-Hinweis.
  EADDRINUSE: klare Port-belegt-Meldung und Abbruch vor Browserstart; andere Fehler
  ebenfalls Abbruch. Windows-Sockets exklusiv binden. Danach beide Listener schließen.
  Secret getpass ohne Echo oder stdin, nie Argument/Datei; Steuerzeichen (Unicode Cc, etwa
  Strg+V aus `cmd`) im Secret = feste deutsche Meldung, Abbruch vor Browser und Token,
  Meldung nennt den Wert nie; unter Windows PowerShell 7 `Read-Host -MaskInput` + `--secret-stdin`.
  Tresor-JSON nur stdout, niemals Datei-Umleitung, nur direkte Pipe an `jnpt credential set`.

Invarianten in README, SETUP, Code, Deploy, CI und Tests gemeinsam nachziehen.
Secret-Hausmuster #887: `plugin-linkedin-seite` (Bridge-Key, zugleich Connector-Credential
`linkedin-seite`) und `plugin-linkedin-seite-oauth` (OAuth-JSON); seit 0.2.0 optional
`plugin-linkedin-seite-post-oauth` (Schreib-Grant) und `plugin-linkedin-seite-gateway` (eigener
Gateway-Token, nur `redaktionsplan_get`, `<ablage>_get_file`). tmpfiles erzeugt alle vier
Ordner 0750/65532; Compose hängt sie read-only ein, eigenes Key-Unit-Paar beobachtet
alle vier `runtime-key` und führt force-recreate aus; REQUIRE verlangt nur die zwei Stufe-A-Schlüssel. Nie Klartext unter /etc oder /opt.
Konfiguration ist secretfrei. Kein Secret in Envs, Repo, Fehlerantwort oder Log.
Dienst bindet nur reservierte Plugin-IP; internal-Netz und Egress-Firewall Betreiberpflicht.

SETUP ist `setup_guide` für den signierten Katalog: UTF-8, höchstens 32 KiB, keine relativen
Links, Adminhandgriffe. README enthält Betreiberhandgriffe, SEC-Prüfpunkte (A2) und Live-Gates.
Tests synthetisch gegen lokalen HTTP-Fake mit echtem Transport/Worker/HTTP-Adapter.
Jede Negativzusage am tatsächlichen Pfad auslösen; Mutation in isolierter Kopie muss rot
werden, Ausgangsdateien bleiben unverändert. Bridge, Scopes, Quote, Minimierung,
Konfigurationsorganisation, GET-only und Token-Logschutz sind Pflichtmutanten.
Hash-Lock mit pip-compile --generate-hashes; Installation --require-hashes, Python 3.12.

Release: `.github/workflows/linkedin-seite{,-release}.yml` spiegeln google-reporting.
Tag `linkedin-seite-vX.Y.Z` ausschließlich durch Auftraggeber; Aktionen SHA-gepinnt,
keine neuen Aktionen. amd64-Build, SBOM/Provenance, Digest-/Config-Digest-Sonden,
Trivy-Bericht, erst dann Tag. Workflow-Änderung zieht Release-Test/CHANGELOG nach.
Version steht in diesem Kopf, README-Kopf und CHANGELOG; Compose/Deskriptor ohne Version.
## Schreibvertrag 0.3.0 (#1306 F4)

Verbindlich: Plan v2/v2.1, SEC c-6602359446 (A1, A3) und c-6604738594 (H1–H3).
Freigabe, Start und dauerhafter Sperrvermerk liegen im Kern. Der Beisteller liest keine
Freigabekartei. Sein Gateway-Token hat genau `redaktionsplan_get` und `<ablage>_get_file`.
`schreiben` enthält gateway_url, redaktionsplan, medien_ablage, max_medium_bytes;
der frühere Konfigurationsschlüssel freigabe ist entfernt und wird abgewiesen.

- `redaktion.py`: Schreiben nimmt ausschließlich `{"eintrag_id":"<16 Hex>"}` an.
  create_post, edit_post, delete_post und create_comment verlangen genau einen Header
  `X-Jnpt-Freigabe: hash=<freigabe:v2:sha256:…>`; keine zweite Form. Fehlender,
  mehrfacher, falsch formatierter oder abweichender Wert ergibt 409 nicht_freigegeben.
  Vor Hash-Gleichheit erfolgt kein LinkedIn-Schreibaufruf.
- `post_adapter.py`: ein Gateway-Lesevorgang, ein unveränderliches Entry-Objekt für
  Vorschau UND Wirkung (H3); Medien einmal in private RAM-Kopien lesen und prüfen.
  Die Vorschau liefert zusätzlich wirkung; Freigabezustand gehört zum Kern.
  Jede Aktion lädt den aktuellen Stand neu und vergleicht ihren selbst berechneten Hash.
  Ablauf: Eintrag → Medien → Wirkung/Hash → Header-Gleichheit → report_hash → Zählen → LinkedIn.
  Kopien werden im finally überschrieben und verworfen.
- Hash exakt wie der Kern: Ausgabe `freigabe:v2:sha256:` + SHA-256 über UTF-8
  `freigabe:v2`, ein LF, kanonisches JSON in Feldfolge aktion, medien, text, umfrage, ziel_urn.
  Medien geordnet als {name, sha256}; name ist der sichtbare Name, bei PDF der Dokumenttitel
  (= Dateiname). Umfrage null oder {antworten, dauer, frage}, dauer ganzzahlige Tage.
  Nil/fehlende Listen = []; fehlende Umfrage = null, leeres Objekt bleibt vorhanden.
  UTF-8 ohne Normalisierung, JSON-Kontrollzeichen, kein HTML-/JavaScript-Escaping, kein Leerraum.
  Unveränderte Kern-Vektoren: tests/data/hash_golden.json; kein v1-Fallback.
- Abbildung: aktion = posten/bearbeiten/loeschen/kommentar; ziel_urn beim Posten =
  cfg.organization, sonst post_urn; text = exakter post_text für Post/Edit, kommentar_text
  für Kommentar, leer für Löschen. Medien/Umfrage nur beim Posten, sonst []/null.
  Little-Text-Escaping ist eine verlustfreie Transportdarstellung des freigegebenen Texts.
  Autor/owner/actor ausschließlich cfg.organization; alle anderen Post-Felder feste Konstanten.
- `gateway.py`: eigener lesender MCP-Client, JSON-RPC/Streamable HTTP (JSON oder SSE),
  nur zwei Werkzeuge aus WriteConfig.tools. Unbekannte Namen scheitern vor HTTP;
  Medienname vor Abruf prüfen, Antwort begrenzen, keine Proxies/Redirects.
- `post_pool.py`: eigener spawn-Prozess je Auftrag, 5 Aktionen und 45 HTTP-Versuche je UTC-Tag
  im Serverprozess. IPC-Permit vor jedem LinkedIn-Aufruf. Vor jedem möglichen Schreibversuch
  meldet der Adapter write, bevor der Transport läuft: auch initializeUpload/Edit/Delete/PUT.
  Bestätigter Erfolg liefert ausgefuehrt: true. Nur nachweislicher Abbruch vor dem ersten
  Schreibversuch liefert nicht_ausgefuehrt: true. Fehler danach tragen keines der Felder,
  auch bei Timeout, Prozessende, fehlerhafter Erfolgsantwort oder Abbruch der Videoverarbeitung.
  Konservative Unklarheit ist zulässig, falsche Nichtausführung nicht (A1).
- Ein Schreibauftrag gleichzeitig; parallele Anfrage 429 schreiben_belegt vor eigenem Start.
  HTTP wartet auf das Endergebnis, höchstens 25 Minuten; Vorschau 20 Sekunden, 10/Minute.
  Client-Abbruch beendet einen Schreibprozess nicht. Keine Zwischenantwort und kein Nachfrage-Fenster.
  report_hash und Ergebnis-Cache im RAM vermeiden Wiederholung bestätigter Erfolge; Neustart
  leert sie. Dieser Komfort ist kein Sperrvermerk; dauerhafte Ausführungs-/Unklar-Sperren hält der Kern.
- `server.py`: Bridge-Prüfung vor Facharbeit, Header über HTTP und WriteRequest bis zum Adapter.
  Frühfehler der Schreibrouten erhalten die Nichtausführung; nach Dispatch nur mit IPC-Nachweis.
  Lese- und Schreib-Grant bleiben getrennt, WRITE_SCOPES exakt w_organization_social und
  w_organization_social_feed, Gateway-Token in eigener Tresor-Stelle.

Medien-/Umfrage-Grenzen und Upload-Host-/Teile-/Status-Prüfungen aus B1 gelten weiter.
Kein GET des Ziel-Posts, kein DRAFT; Kommentartext ohne URN; little escaped reservierte
Zeichen, Hashtags bleiben. post_urn wird durch die KI gepflegt und ist nur Komfort.
B2/B3, Kern-Code, GUI, Kern-Specs und Live-Abnahme sind außerhalb F4.

Verifikation mit Python 3.12, Abhängigkeiten aus requirements.lock:
`python -m pip install --require-hashes -r requirements.lock`, dann im Modul
`python -m unittest discover -s tests -v` und `python tests/mutations.py`.
Pflichtmutanten: Hashvergleich/fehlender Header, jedes Hashfeld, geänderter Golden-Vektor,
Ergebnisfelder vor/nach erstem Schreibversuch, zwei Gateway-Werkzeuge, bestehende SEC-Sperren.
Isolierte Kopien müssen rot werden; Originale bleiben unverändert. Ausgang und Doku-Checkliste
stehen im PRUEFBERICHT. Synthetische Tests ersetzen kein Live-Gate.
