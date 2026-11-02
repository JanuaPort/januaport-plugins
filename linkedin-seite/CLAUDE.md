# linkedin-seite 0.2.0 — Modulvertrag (#1237)

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
  `WRITE_SCOPES` = genau `w_organization_social`, geschlossene `WRITE_ENDPOINTS` (Methode, Pfad je
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
Gateway-Token, nur `redaktionsplan_get`, `redaktion_freigabe_search`, `<ablage>_get_file`). tmpfiles erzeugt alle vier
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
## Stufe B1 (0.2.0) — Invarianten

- `redaktion.py` (rein, ohne IO): `WriteConfig` aus dem optionalen Config-Block `schreiben`
  (`gateway_url` streng `http(s)://<host>:<port>/mcp`, nie `/admin/mcp`; Kartei-/Ablage-Namen; Größe
  Vorgabe = Höchstwert 32 MiB). Schreibrouten nehmen nur `{"eintrag_id": <16 Hex>}`. Kartei-Felder
  `post_text`, `medien` (JSON-Liste), `umfrage` (JSON-Objekt), `post_urn`, `kommentar_text` als
  string, weil der Kern nur `string|integer|number|boolean` kennt; strikt geparst. Umfrage-Grenzen
  vor dem Senden, nichts kürzen. `posten` verlangt leere `post_urn`. Kürzel `‹…›` in einem Feld und
  URNs im Kommentartext werden abgewiesen. Hash = SHA-256 über `HASH_PREFIX` + kanonisches JSON
  (sortiert, UTF-8, kompakt) der gepostet-relevanten Felder und `{datei, bytes, sha256}` je Medium;
  eingefrorene Vektoren in `tests/test_post_domain.py` — eine Änderung entwertet jede Freigabe und
  braucht ein neues `v`. `freigegeben` verlangt `eintrag_id`, `aktion`, `hash` exakt **und**
  `via == updated_via == "SSO"`. `little` escaped alle reservierten Zeichen, `#Wort` bleibt Hashtag.
  `upload_ziel`: https, `netloc == hostname` (kein Port, keine Zugangsdaten), Host exakt aus der
  Liste, Pfadpräfix, kein Fragment. `video_teile`: lückenlos, ohne Überlappung, ≤ ceil(Größe/4 MiB).
- `gateway.py`: MCP-Client (JSON-RPC über Streamable HTTP, Legacy-Kette wie `kartei-rpa`, JSON oder
  SSE). Ruft **nur** die drei Werkzeuge aus `WriteConfig.tools`; jeder andere Name scheitert vor dem
  HTTP-Aufruf. Kein Proxy, keine Umleitung, Antwortgröße begrenzt (beim Datei-Abruf 4/3 · Grenze +
  1 MiB, Lesen bricht dort ab). Dateiname vor dem Aufruf geprüft. `Medium` ist die eine private
  Kopie (`bytearray`), `loeschen()` überschreibt und verwirft sie. Feste Fehlercodes, kein
  Gateway-Text.
- `post_adapter.py`: Reihenfolge Eintrag → Medien einmal lesen → Hash → Freigabe → `report_hash`
  (Idempotenz) → erst dann `begin_action` (zählt) und LinkedIn. Nur Operationen aus
  `WRITE_ENDPOINTS` (`api`) und geprüfte Upload-Adressen (`upload`); Autor/owner/actor nur
  `cfg.organization`; alle übrigen Post-Felder Konstanten (`POST_SHELL`). `Tokens` mit
  `WRITE_SCOPES` (auch die Erneuerung). Kopien im `finally` gelöscht. Video: alle Teile geprüft vor
  dem ersten PUT, ETag je Teil, `finalizeUpload`, Status in festem Takt bis Höchstzeit, Post nur bei
  `AVAILABLE`.
- `post_pool.py`: je Auftrag ein spawn-Prozess (`job_main`, Logging aus). Der Serverprozess hält
  Tageszähler (5 Aktionen, 45 HTTP), Idempotenz-Map und den einen laufenden Auftrag; der Auftrag
  fragt vor jedem LinkedIn-Aufruf über seine Leitung — ohne Serverprozess kein Aufruf. Warten 20 s,
  danach `202 in_arbeit`, harte Grenze 25 min, Client-Abbruch beendet einen Schreibauftrag nicht
  (Vorschau schon). Ein Ergebnis/Fehler nach 202 wird beim nächsten Aufruf genau einmal geliefert.
  Vor dem nicht wiederholbaren Schlussaufruf (`post_create`, `comment_create`) meldet der Auftrag
  `final`; scheitert er danach, sperrt `unbekannt` dieselbe `eintrag_id`+`hash` mit
  `409 ergebnis_unbekannt` (kein Doppelpost durch Wiederholung).
  Spur: eine Zeile je Schreibauftrag, nur Aktion, ID, Hash, Code, URN, Größe.
- `server.py`: eigener Präfix `/v1/linkedin-post/`, gemeinsame Bridge-Prüfung `check_bridge`,
  `load_write_side` lädt Schreib-Grant (`load_write_identity`, verlangt `scope`) und Gateway-Token;
  fehlt/ungültig ⇒ Schreiben aus (`schreiben=<grund>` im Log), Stufe A läuft unverändert.
- `oauth.py`: `token_result`/`Tokens` mit `scopes`-Parameter (Vorgabe Stufe-A-Scopes), Lese-JSON
  ohne `scope`, Schreib-JSON mit `scope` = `w_organization_social`; damit fällt ein Tausch in beiden
  Richtungen beim Laden auf.
- `tools/zustimmung.py --schreiben`: fordert genau `w_organization_social` an, schreibt `scope` ins
  JSON, nennt die Ziel-Stelle auf stderr; ohne Schalter byte-gleicher Aufruf wie 0.1.2.

Bewusste Entscheidungen und Grenzen: Medien als JSON-String statt Kartei-Liste (Kern-Typen);
Dateiinhalt über seinen SHA-256 im Hash (gleichwertig, nicht wortgleich mit SEC Bed. 2);
Dokumenttitel = Dateiname (bei LinkedIn Pflicht); Kommentartext unescaped (Little-Text für
Kommentare nicht dokumentiert), deshalb ohne URN; kein `GET` des Ziel-Posts (nicht in der
geschlossenen Liste) — die Freigabe des Menschen bindet die URN; kein DRAFT (Doku: nur `PUBLISHED`
beim Anlegen); Zähler und Idempotenz nur im RAM, Neustart setzt zurück; `post_urn` schreibt die KI,
sie ist Komfort und kein Schutz gegen die KI. Token-Label `SSO` ist im Kern nicht reserviert:
Betreiberregel. Pflichtmutanten B1 stehen in `tests/mutations.py` (Präfixe `b1-`…`b5-`, `v1-`…`v3-`,
`poll-`). B2-Rest (Untertitel, Vorschaubild, Video-Löschen) und B3 (fremde Kommentare) sind nicht
gebaut.
