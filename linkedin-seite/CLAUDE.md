# linkedin-seite 0.4.0 — Modulvertrag (#1306 F4, #1237)

Privater Python-REST-Beisteller, Apache 2.0. PO 29.10.2026 Option (a): Stufe A (Lesen); seit
0.2.0 Stufe B1 (Posten nach Freigabe, SEC c-6575359082 und c-6575837332); seit 0.4.0 Stufe B3
(Kommentare lesen und als Organisation beantworten, ohne Einzelfreigabe; Abschnitt unten). Synthetisch
geprüft, Live-Abnahme offen. Kein eigener MCP-Server; für B1 ein eigener, nur lesender MCP-Client.
Root-CLAUDE.md war im Ausgangscheckout nicht vorhanden.

- `domain.py`: fail-closed Konfiguration, Organisation ausschließlich Betreiberwert;
  drei feste Berichte. UTC, 7 Tage Vorgabe, 1–90 Tage, maximal 10 Beiträge.
  Follower-Ende höchstens heute−2, Seite höchstens gestern; spätere Eingabe
  `end_date_not_available`, kein Klemmen. Vorgabe endet am letzten verfügbaren Tag.
- `source_contract.py`: EIN Ort aller Hersteller-Endpunkte, Parameter und Feldnamen,
  Kommentar „aus Herstellerdoku, live zu belegen“. `SCOPES` = was der Lesepfad braucht
  (`rw_organization_admin r_organization_social`), `WRITE_SCOPES` = was der Schreibpfad braucht
  (`w_organization_social` + `w_organization_social_feed`, Kommentar, Comments API 202610),
  `COMMUNITY_SCOPES` = was der Kommentar-Pfad zusätzlich braucht (`r_organization_social_feed`, 0.4.0),
  `GRANT_SCOPES` = die Vereinigung aller drei, der eine Grant (0.3.1), genau fünf Werte (0.4.0).
  `SCOPES` nennt nie einen `*_feed`-Scope (SEC K5). Geschlossene `WRITE_ENDPOINTS` (Methode, Pfad je
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
  Leerraum getrennt, Menge exakt gleich `GRANT_SCOPES` (`scope_set`); die Zustimmung verlangt das
  Feld (`scope_required`), beim Refresh darf es fehlen, weil ein Refresh keinen Grant erweitert und
  eine Ablehnung ein Fehlen zum Ausfall um Tag 55 machte. `validate_identity` verlangt `scope` exakt
  gleich `GRANT_SCOPES`; JSON ohne `scope` oder mit anderer Menge startet nicht. Laufzeiten bis
  Höchstwert + 1 Tag (`OVERSHOOT_TOLERANCE_SECONDS`) werden auf 60/365 Tage gedeckelt
  (`lifetime`), darüber/nicht int/≤0 = `unexpected_source_shape`. Erneuerung fünf Minuten vor Ablauf
  beim nächsten Abruf ausschließlich RAM; kein Tresor-Rückschreiben. Warnung ab 14 Tagen
  vor Access-Ablauf ohne Refresh bzw. Refresh-Ablauf mit Refresh, nur Datum; danach
  ohne nutzbaren Access `token_abgelaufen`. Der Token-Zustand: Abschnitt unten.
- `workers.py`: Vorbild google-reporting, zwei spawn-Prozesse, vier Queueplätze,
  30 Berichte/Minute, 20 Sekunden inklusive Queue/Token. Timeout/Disconnect killt und
  reapt vor Ersatz. 50 HTTP-Versuche/UTC-Tag im SERVERPROZESS, IPC-Permit vor jedem GET,
  Refresh zählt über den Transport des Pools, Fehlschläge zählen. Neustart setzt Tageszähler zurück.
- `server.py`: Bridge-Key-Auth, feste GET-Routen, keine freien Pfade; 8 KiB Anfrage,
  Pfad 512, 512 KiB Quelle/Antwort. Feste Fehler-JSON `{"error": code}`; optional die
  definierte Tokenwarnung auf jeder Anwendungsantwort, auch lokalem `/healthz` und Fehler.
  Nur Route/Code/Dauer im Log. Gateway prüft Werkzeugrechte und Auditmetadaten.
- `safe_http.py`: gepinnte aiohttp-Naht unverändert aus google-reporting bis auf Loggername.
  SafeAppRunner Pflicht; Parserlimits 8192; Parserfehler ohne Rohbytes/Exceptions, ohne
  Tokenmetadaten. Upgrade verlangt Canary-Negativproben.
- `tools/zustimmung.py`: PO-Rechner, nicht im Image. State zufällig/geprüft, PKCE S256
  erst nach Herstellerbeleg, sonst ohne; beide Loopbacks 127.0.0.1 und ::1:8765,
  nie Wildcard. Redirect bleibt http://localhost:8765/linkedin/callback. `--gesamt` ist Pflicht und
  der einzige Modus, geparst vor der Secret-Abfrage: Eine alte Befehlszeile endet mit
  `ungueltige_argumente` statt still einen anderen Scope-Satz anzufordern, der frühere Tokens entwertet.
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
`linkedin-seite`) und `plugin-linkedin-seite-oauth` (OAuth-JSON des einen Grants); seit 0.2.0
optional `plugin-linkedin-seite-gateway` (eigener Gateway-Token, nur `redaktionsplan_get`,
`<ablage>_get_file`). Die frühere Stelle `plugin-linkedin-seite-post-oauth` ist mit 0.3.1 entfallen.
tmpfiles erzeugt alle drei Ordner 0750/65532; Compose hängt sie read-only ein, eigenes Key-Unit-Paar
beobachtet alle drei `runtime-key` und führt force-recreate aus; REQUIRE verlangt nur Bridge-Key und Grant.
Nie Klartext unter /etc oder /opt.
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
## Ein Grant, ein Token-Zustand (0.3.1, #1237)

Befund 03.11.2026: Nach der zweiten Zustimmung derselben App für dasselbe Mitglied, nur mit den
Schreib-Scopes, lieferte jeder Lesezugriff `source_access_denied`. Die Herstellerdoku sagt dazu: „If
you request a different scope than the previously granted scope, all the previous access tokens are
invalidated.“ Live ist die Ursache nicht belegt. Daraus folgt: **ein Grant, keine zweite Zustimmung.**
Wer einen Scope ergänzt, fordert den ganzen neuen Satz in einer Zustimmung an und ersetzt das JSON.

Prozessmodell: Serverprozess, zwei persistente Lese-Worker und je Auftrag ein Schreib- oder
Vorschau-Prozess, alle spawn. Bis 0.3.0 hielt jeder Worker eine eigene Token-Kopie, jeder Job begann
wieder beim Tresorstand, und ein gekillter Worker nahm eine laufende Erneuerung mit.
Seit 0.3.1 gilt:

- `oauth.Tokens` existiert genau einmal, im Serverprozess, gebaut in `server.build_pools` und beiden
  Pools übergeben. `access(transport)` ist die einzige Stelle, die erneuert.
- Eine `threading.Lock` umfasst Prüfung, Tausch und Übernahme. Bewusst keine asyncio-Sperre: Ein
  abgebrochener Request gäbe sie frei, während der Tausch im Thread weiterläuft, und der nächste
  tauschte ein zweites Mal. `access()` läuft deshalb immer über `asyncio.to_thread`, nie auf dem
  Event-Loop; ein gekillter Worker verliert das Ergebnis nicht, es landet im Zustand.
- `self.data` wird in einem Schritt ersetzt, weil `warning()` ohne Sperre liest.
- Worker und Jobs halten `oauth.TokenClient`: `('token',)` über die vorhandene Pipe, Antwort das
  Access-Token, je Aufruf, ungecacht. Spawn-Argumente tragen keinen Token, kein Refresh-Token, kein
  Client-Secret. Die frühere `expiry`-Nachricht ist entfallen, der Zustand liegt ja beim Server.
- Permit- und Transportgrenzen bleiben je Pfad getrennt: Jeder Pool baut seinen eigenen `Transport`
  mit seinem eigenen Zähler (`_permit`, `_permit_http`). Wer erneuert, zahlt auf seinem Deckel; ein
  leerer Lesedeckel stoppt nur den Lesepfad. Beide Zähler tragen `counter_lock`, weil neben dem
  Event-Loop nun auch ein Erneuerungs-Thread zählt.
- `job.write_started` und `nicht_ausgefuehrt` sind unberührt: Eine Erneuerung ist kein Schreibversuch.
  Eine Vorschau bekommt kein Token (`vorschau_schreibt`).
- `_send_token` dämmt unerwartete Fehler ein wie zuvor `worker_main` (502 `source_unavailable`).
  Ohne das stirbt `_supervise`, `job.future` bleibt offen und der Schreibaufruf hängt für immer.

Bewusste Grenzen: Die 25-Minuten-Frist eines Schreibjobs wird während einer Erneuerung nicht geprüft
und kann sich im Normalfall um bis zu 36 Sekunden verschieben, bei Störung des Tokenendpunkts länger (18 s Socket-Timeout je Operation, gescheiterte Erneuerungen laufen hintereinander), der Job schreibt
in der Zeit nichts. Beim Herunterfahren wartet der Dienst bis zu 18 Sekunden auf einen laufenden
Tausch. Refresh-Tokens bleiben nur im RAM: Nach einem Neustart beginnt der Dienst beim Tresorstand.

Unverändert: SEC-Trennung im Gateway (Lesevorlage `linkedin-seite`, Schreibvorlage
`linkedin-seite-manage`), Freigabe-Kopf `X-Jnpt-Freigabe`, Hash-Vergleich, Tagesdeckel, Lese-Routen
nur GET, kein neues Werkzeug, keine Token-Werte in Logs oder Antworten. SEC Bed. 4 („eigener
Schreib-Grant, nie getauscht“) ist damit aufgehoben: Das Access-Token im Lese-Worker trägt jetzt die
Schreib-Scopes. Getrennt sind Pfade und Rechte, nicht mehr die Tokens.

## Schreibvertrag 0.3.0 (#1306 F4)

Verbindlich: Plan v2/v2.1, SEC c-6602359446 (A1, A3) und c-6604738594 (H1–H3).
Freigabe, Start und dauerhafter Sperrvermerk liegen im Kern. Der Beisteller liest keine
Freigabekartei. Sein Gateway-Token hat genau `redaktionsplan_get` und `<ablage>_get_file`.
`schreiben` enthält gateway_url, redaktionsplan, medien_ablage, max_medium_bytes;
der frühere Konfigurationsschlüssel freigabe ist entfernt und wird abgewiesen.

- `redaktion.py`: Schreiben nimmt ausschließlich `{"eintrag_id":"<16 Hex>"}` an.
  create_post, edit_post, delete_post und create_comment verlangen genau einen Header
  `X-Jnpt-Freigabe: hash=<freigabe:v2:sha256:…>`; ohne Auftrags-ID, keine zweite Form. Fehlender,
  mehrfacher, falsch formatierter oder abweichender Wert ergibt 409 nicht_freigegeben.
  Diese Gate-Antwort trägt weder nicht_ausgefuehrt noch ausgefuehrt.
  Vor Hash-Gleichheit erfolgt kein LinkedIn-Schreibaufruf.
- `post_adapter.py`: ein Gateway-Lesevorgang, ein unveränderliches Entry-Objekt für
  Vorschau UND Wirkung (H3); Medien einmal in private RAM-Kopien lesen und prüfen.
  Die Vorschau entspricht dem F2-Anschlussvertrag: exakt eintrag_id, aktion, format,
  ziel_urn, text, medien[{name, sha256, datei, art, bytes}], umfrage, post_text, post_urn,
  kommentar_text, dokument_titel, hash. Kein zusätzliches verschachteltes Wirkungsfeld.
  Wirkung und Anzeige verwenden dasselbe Entry-Objekt; der Kern berechnet seinen Hash neu
  aus den direkten Wirkungsfeldern. Unveränderte Formreferenz:
  tests/data/linkedin_vorschau_beispiel.json. Optionale Ablaufhinweise erlaubt F2 ausdrücklich:
  warnung, ablaufdatum, warnung_schreiben, ablaufdatum_schreiben. Freigabezustand gehört zum Kern.
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
  meldet der Adapter write nach erfolgreichem HTTP-Permit unmittelbar vor dem Senden:
  auch initializeUpload/Edit/Delete/PUT. Ablehnung der ersten HTTP-Quote bleibt so eindeutig vor Wirkung.
  Bestätigter Erfolg liefert ausgefuehrt: true. Nach Bridge- und Headerform-Prüfung liefert
  nur nachweislicher Abbruch vor dem ersten Schreibversuch HTTP 200 mit nicht_ausgefuehrt: true
  und festem error-Code (SEC c-6608136792, LEAD Variante a). Das gilt für lokale Tages-/HTTP-Kappe,
  belegten/geschlossenen/nicht eingerichteten Dienst, Validierung, Medienprüfung und Quellenfehler
  vor dem Senden. Der Kern liest diesen Indikator nur aus 2xx-Antworten. Fehler ab dem ersten möglichen Schreibversuch tragen keines der Felder,
  auch bei Timeout, Prozessende, fehlerhafter Erfolgsantwort oder Abbruch der Videoverarbeitung.
  Konservative Unklarheit ist zulässig, falsche Nichtausführung nicht (A1).
- Ein Schreibauftrag gleichzeitig; parallele Anfrage HTTP 200, schreiben_belegt und
  nicht_ausgefuehrt: true vor eigenem Start.
  HTTP wartet auf das Endergebnis, höchstens 25 Minuten; Vorschau 20 Sekunden, 10/Minute.
  F2 begrenzt den Call aktuell auf 25 Sekunden (Runtime-Decke 120 Sekunden); lange Videojobs
  können im Kern daher bereits vorher unklar werden. Client-Abbruch beendet einen Schreibprozess nicht. Keine Zwischenantwort und kein Nachfrage-Fenster.
  report_hash und Ergebnis-Cache im RAM vermeiden Wiederholung bestätigter Erfolge; Neustart
  leert sie. Wiederholung liefert wiederholt: true UND ausgefuehrt: true, ohne zweiten Post.
  Dieser Komfort ist kein Sperrvermerk; dauerhafte Ausführungs-/Unklar-Sperren hält der Kern.
- `server.py`: Bridge-Prüfung vor Facharbeit, Header über HTTP und WriteRequest bis zum Adapter.
  Die strenge Headerform wird vor Body-/Fachvalidierung und Bereitschaft geprüft; danach
  dürfen eindeutige Frühfehler als HTTP 200 mit nicht_ausgefuehrt: true zurückkommen,
  nach Dispatch ausschließlich mit IPC-Nachweis. Hash-Gleichheit wird nach dem Laden geprüft:
  nicht_freigegeben bleibt immer 409 ohne Ergebnisfeld. Ohne erfolgreiche Bridge-/Headerprüfung
  sowie für Vorschauen bleiben Fehlerstatus und fehlende Ergebnisindikatoren erhalten.
  Lesen und Schreiben teilen seit 0.3.1 den einen Grant (Abschnitt oben), der Gateway-Token
  liegt in eigener Tresor-Stelle.

Medien-/Umfrage-Grenzen und Upload-Host-/Teile-/Status-Prüfungen aus B1 gelten weiter.
Kein GET des Ziel-Posts, kein DRAFT; Kommentartext ohne URN; little escaped reservierte
Zeichen, Hashtags bleiben. post_urn wird durch die KI gepflegt und ist nur Komfort.
B2/B3, Kern-Code, GUI, Kern-Specs und Live-Abnahme sind außerhalb F4.

Verifikation mit Python 3.12, Abhängigkeiten aus requirements.lock:
`python -m pip install --require-hashes -r requirements.lock`, dann im Modul
`python -m unittest discover -s tests -v` und `python tests/mutations.py`.
Pflichtmutanten: Hashvergleich/fehlender Header, jedes Hashfeld, geänderter Golden-Vektor,
Ergebnisfelder vor/nach erstem Schreibversuch, zwei Gateway-Werkzeuge, bestehende SEC-Sperren,
exakte Scope-Menge, `--gesamt` als Pflicht, Sperre um die Erneuerung, eigene Kopie je Prozess.
Isolierte Kopien müssen rot werden; Originale bleiben unverändert. Ausgang und Doku-Checkliste
stehen im PRUEFBERICHT. Synthetische Tests ersetzen kein Live-Gate.

Gemeinsame JSON-Fixtures sind LF-Dateien. Root-.gitattributes enthält
`linkedin-seite/tests/data/*.json text eol=lf`; untergeordnete -text-Ausnahmen entfallen.
`test_fixture_bytes` prüft LF, feste Datei-SHA-256 und lokal die Kern-Dateien aus
../jpwt-1306f2. Bei Windows-CRLF im Kern-Checkout wird dessen exakte LF-Git-Fassung gelesen
und die reine Checkout-Konvertierung geprüft; der Kern wird nicht geändert. CI ohne den
Kern-Worktree verwendet dieselben fest eingetragenen SHA-256-Werte. Dies betrifft nur
Dateizeilenenden der Fixtures, niemals Normalisierung der freizugebenden Texte.

## Stufe B3: Kommentare unter eigenen Posts (0.4.0, #1237)

Verbindlich: Schnitt c-6747016143, SEC-PASS c-6747087768 (S1–S4, K2–K5), SEC T1 c-6759978021 zur
Tagesliste, PO c-6759929959. Rangfolge SEC/PO vor Schnitt vor Plan c-6746942607. Eigene Kern-Vorlage
`linkedin-seite-community` (Entwurf `entwurf/linkedin-seite-community.yaml`, portiert CONNECTOR), eigene
Route `/v1/linkedin-kommentare/`, derselbe Bridge-Key (SEC Frage 4: kein zweiter).

- `kommentare.py`: reiner Vertrag ohne IO. Eingaben (`parse_comments`, `parse_reply`, `parse_tagesliste`),
  `ANLAESSE` (`dank`, `sachfrage_zum_post`, `gespraech_anbieten`, S2), `TEXT_REGELN` als feste geordnete
  Liste mit je eigenem Code, geprüft auf einer NFKC+casefold-Kopie, gesendet wird der Text unverändert.
  URN-Muster mit Ziffern in jedem ID-Teil (T1), `kommentar_aus` verifiziert ein Element: `commentUrn` in
  Activity-Form, `id` = dessen Kommentar-Teil, `object` = diese Activity oder genau der gelistete Post;
  sonst `nicht_lesbar`. `ausgabe` ist die einzige Projektion nach außen: nie `actor`, Name, Headline,
  Personen-URN, Erwähnung oder Bild. Links baut das Plugin selbst aus Ziffern (`link_kommentar`,
  `link_antwort`). `MERKMALE` sind feste Werte. Eigene Personen (`kommentare.eigene_personen` in der
  Konfiguration) zählen als „von uns“ und werden nie ausgegeben oder geloggt.
- `community_adapter.py`: läuft je Aufruf in einem eigenen spawn-Prozess. Nur `COMMUNITY_ENDPOINTS`
  (Post holen, Posts listen, Kommentare eines Posts, Antworten eines Kommentars, Antwort anlegen);
  kein Löschen, kein Bearbeiten, keine Reaktion, keine Profilabfrage. Zugehörigkeit nur über die Liste
  genau dieses Posts. `reply` prüft vor dem Senden live: eigener veröffentlichter Post höchstens 30 Tage
  (K2), Liste vollständig (`zu_viele_kommentare`) und ganz lesbar (`kommentare_nicht_lesbar`, sonst
  wäre ein Verfasser unbekannt), Eltern gelistet (`kommentar_unbekannt`), oberste
  Ebene (`ebene`), nicht gelöscht, nicht von uns, Text nicht zurückgehalten (`erwaehnung_im_kommentar`),
  keine eigene Antwort (`wiederholt`, immer live gelesen, nie aus `commentsSummary`), keine eigene
  Antwort an denselben Verfasser unter diesem Post (`verfasser_beantwortet`). Je Aufruf eigene
  HTTP-Decke (`comments` 25, `tagesliste` 40, `reply` 12, Prüfungen zwei darunter). Autor ist immer
  `cfg.organization`. Der POST trägt genau `actor`, `object` (Post), `parentComment`, `message.text`.
  Nach dem Senden Nachlesen: `antwort_urn` aus `x-restli-id` (Ziffern), `doppelt`, `nachgelesen`.
- `community_pool.py`: ein spawn-Prozess je Aufruf, Token über die Pipe, Permit vor jeder Anfrage, im
  Job ist Logging abgeschaltet. **Kein Antwort-Zähler** (S3): der Deckel ist `daily_cap: 8` am Werkzeug
  `reply` im Kern und zählt Versuche. Gezählt werden nur LinkedIn-HTTP-Versuche des Pfads (60/UTC-Tag,
  `kommentare_http_tageslimit`), wie bei Lesen (50) und Schreiben (45). Antworten laufen in einer
  Warteschlange im Prozess (asyncio-Sperre, FIFO, höchstens ein Wartender, gezählt über `in_reihe` statt
  über `reihe.locked()`, das zwischen Freigabe und Aufwecken frei aussieht), 60 s Abstand zwischen
  Sendeversuchen (`last_send`, gesetzt beim IPC `write`), Sperre 10 Minuten je `kommentar_urn` ab dem
  Sendeversuch und solange ein Aufruf für ihn läuft. Ablehnungen vor dem Senden sperren und verzögern
  nicht. Ein Lese-Job bekommt nie ein `write` (`lesen_schreibt`). Nach dem Start besitzt der Job die
  Warteschlange; sein Supervisor gibt sie frei, ein abgebrochener Request stoppt keinen Sendeversuch. Der
  Supervisor fängt jede unerwartete Ausnahme (`worker_failed`), sonst bliebe die Anfrage offen und der
  Lese-Platz dauerhaft belegt; ein Job, der nicht startet, ist bewiesen nicht ausgeführt.
- `server.py`: `COMMUNITY_ROUTES` (`comments` GET, `tagesliste` GET, `reply` POST). Bridge zuerst; danach
  ist jede bewiesene Ablehnung von `reply` HTTP 200 mit `nicht_ausgefuehrt` und festem Code, nach dem
  Dispatch nur mit IPC-Nachweis ohne Sendeversuch. `build_pools` liefert drei Pools auf einem
  `Tokens`-Objekt; der Community-Pool nimmt keinen Gateway-Token.

Invarianten B3: Scope-Menge genau fünf; Statistik-Leser ohne `*_feed`, sein Adapter erreicht
`socialActions` nicht (das Token selbst trägt alle fünf, Trennung auf Anfrage-Ebene). Keine Persistenz,
kein Cache: Sperren und Abstand nur im RAM eines Prozesses. Kommentartext nie im Log; die Spur je
Antwort trägt nur URNs, Code, `anlass` und `doppelt`. Die Tagesliste trägt nur Links und feste Merkmale
(T1), `anlass` ist mangels Speicher nicht rekonstruierbar und fehlt. Kein Gateway, kein Redaktionsplan
im Community-Pfad (S1). Die B1-Freigabe für `create_comment` ist unverändert.

Aktivierungsregel (S1, T2): Das Antwort-Token trägt genau `linkedin-seite-community:comments`,
`linkedin-seite-community:reply` und `linkedin-seite:posts`, sonst nichts. `tagesliste` liegt am
Redaktions-Token, nie `comments` dort. Not-Aus: der PO entzieht `linkedin-seite-community:reply` in der
Admin-Oberfläche; danach der Widerruf bei LinkedIn.

Bewusste Grenzen: Sperre und Abstand überleben keinen Neustart; danach hält nur das Live-Nachlesen,
und das steht unter LinkedIns Lesekonsistenz. Ein Prozess, ein Server: mehr Instanzen hebeln die
Warteschlange aus. `commentsSummary` kann nachlaufen: in `comments` und `tagesliste` darf ein frisch
beantworteter Kommentar kurz als offen stehen, `reply` liest immer live. Lesen 50 + Schreiben 45 +
Kommentare 60 liegt über den 100 Aufrufen je Mitglied und Tag im Development Tier.

Pflichtmutanten B3 (`b3-*` in `tests/mutations.py`): fremder/alter Post, Eltern eines anderen Posts,
Eltern von uns oder eigener Person, zweite Ebene, gelöscht, blinde Antwort, Zweitantwort, Summary statt
Live-Lesen, gleicher Verfasser, unvollständige Liste, Prüfdecke, Autor, geschlossene Endpunkte, Text-
und Anlass-Wiederholung, unklar nach dem Senden (Adapter und Pool), Anlass-Enum, Länge, Link, Erwähnung,
NFKC, Preis, Prozent, Personen-, Log- und Tageslisten-Kanarienvogel, T1-Ziffern, Schlüssel object+id,
Abstand, Warteschlange, Sperre und In-Flight, Lese-Job ohne Schreibrecht, HTTP-Deckel, kein Antwort-
Zähler, Bridge, 200 vor dem Dispatch, Fünf-Scope-Menge, Statistik-Leser mit `_feed`-Scope und Endpunkt,
Vorlagen-Enum und `daily_cap`.
