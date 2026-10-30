# linkedin-seite 0.1.0 — Modulvertrag (#1237)

Privater Python-REST-Beisteller, Apache 2.0. PO 29.10.2026 Option (a): ausschließlich
Stufe A. Synthetisch geprüft, Live-Abnahme offen. Kein eigener MCP-Server.
Root-CLAUDE.md war im Ausgangscheckout nicht vorhanden.

- `domain.py`: fail-closed Konfiguration, Organisation ausschließlich Betreiberwert;
  drei feste Berichte. UTC, 7 Tage Vorgabe, 1–90 Tage, maximal 10 Beiträge.
  Follower-Ende höchstens heute−2, Seite höchstens gestern; spätere Eingabe
  `end_date_not_available`, kein Klemmen. Vorgabe endet am letzten verfügbaren Tag.
- `source_contract.py`: EIN Ort aller Hersteller-Endpunkte, Parameter und Feldnamen,
  Kommentar „aus Herstellerdoku, live zu belegen“. Scopes exakt
  `rw_organization_admin r_organization_social`. Kein weiterer Scope, kein Schreibadapter.
- `linkedin_adapter.py`: fünf feste REST-Endpunkte, nur einzelne GETs, kein BATCH_GET.
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
  365 Tage ab Erstzustimmung, Frist nie verlängern. Erneuerung fünf Minuten vor Ablauf
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
  Secret getpass ohne Echo oder stdin, nie Argument/Datei; Tresor-JSON nur stdout,
  niemals Datei-Umleitung, nur direkte Pipe an `jnpt credential set`.

Invarianten in README, SETUP, Code, Deploy, CI und Tests gemeinsam nachziehen.
Secret-Hausmuster #887: `plugin-linkedin-seite` (Bridge-Key, zugleich Connector-Credential
`linkedin-seite`) und `plugin-linkedin-seite-oauth` (OAuth-JSON). tmpfiles erzeugt beide
Ordner 0750/65532; Compose hängt Ordner read-only ein, eigenes Key-Unit-Paar beobachtet
beide `runtime-key` und führt force-recreate aus. Nie Klartext unter /etc oder /opt.
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
Stufe B ist nicht implementiert; später separater Auftrag und SEC-/Rechte-/Auditvertrag.
