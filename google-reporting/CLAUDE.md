# google-reporting 2.0.0 — Modulvertrag (#1130, #1148, #1150, #1176, #1379)

Privater Python-REST-Beisteller, Apache2.0. Stufe 1, gebaut/synthetisch geprüft,
Live-Abnahme offen. Kein eigener MCP-Server oder Gateway-Auth-Typ.

- domain.py: fail-closed Betreiberkonfiguration, feste Berichtseingaben/-filter,
  source-local 1–90 Tage bis heute−3, Datenminimierung und Qualitätsprojektion. `Config` ist die
  Konfiguration **einer** Site; `Sites` (#1379) hält alle und ist die einzige Stelle, die den Header
  in eine Site übersetzt (`resolve`).
- google_adapter.py: offizielle google-auth-, GSC- und GA-Data-REST-Clients;
  Property je Anfrage aus der Site der Anfrage (`config_for`), getrennte readonly Scopes und
  RAM-Caches (gemeinsam, ein Servicekonto). Explizite Datei,
  kein ADC. ScopedCredentials.refresh nutzt gepinnte private SDK-Naht, damit
  Tokenretry/Trust-boundary-Lookup aus bleiben; tatsächliche SDK-Tests Pflicht.
- workers.py: zwei persistente spawn-Prozesse, vier Queueplätze,30/min/Quelle,
 20 s inklusive Queue/Token. Timeout/Disconnect tötet Prozess und belegt keinen
 Hintergrundthread weiter; erst nach Reap Ersatz. RAM-Caches gehen dabei verloren.
- safe_http.py: gepinnte aiohttp-Protokollnaht ersetzt Fehler vor der Middleware
  durch feste JSON-Codes ohne rohe Requestbytes/Exception-Logs. SafeAppRunner
  ist Pflicht, Parserlimits bleiben 8192. Upgrade verlangt Canary-Proben.
- server.py: feste GET-Routen (Berichte, seit #1148 auch die Manage-Listen) und je Schreiboperation
  eine feste POST-Route, **ein** Bridge-Key für beide Quellen
  (`/run/secrets/jnpt/runtime-key`) und das Servicekonto-JSON
  (`/run/secrets/google/runtime-key`), beide beim Start via `load_runtime_files`;
  strikte Vorprüfung, sichere Codes und Route/Site/Code/Dauer-Log, bei einem angewendeten
  Schreibaufruf zusätzlich genau eine Änderungszeile. Die Rechte je
  Werkzeug prüft das Gateway, nicht der Dienst. /healthz lokal ohne Googlezugriff.
- manage.py + manage_adapter.py (#1148): die drei Schreibhandgriffe und ihre Lesewerkzeuge.
  `manage.py` ist rein (Routenmengen, Validierung, Google-Bodies, Projektion, Änderungszeile),
  `manage_adapter.py` ruft die Discovery-Clients (GSC `searchconsole` v1, GA `analyticsadmin`
  v1alpha; **keine** zusätzliche Abhängigkeit, der Lock bleibt unverändert). Site/Property nur
  aus der Konfiguration der aufgelösten Site. Kein delete/archive, kein generischer Pfad.

Schreibhandgriffe (#1148, SEC-Auflagen P1–P4, W1–W6, H1–H3), Invarianten:
- **W1 Scope-Trennung:** ein Credential-Objekt je Scope (`SCOPES` readonly, `WRITE_SCOPES`
  `webmasters`/`analytics.edit`), nie kombiniert; Lesewege und der Vorzustand eines Schreibaufrufs
  bleiben readonly. `tests/test_manage_adapter.py::ScopePins` pinnt die Menge je Operation.
- **P1:** `primary` ist kein Parameter und steht immer auf `false`; ein Patch enthält nur
  `groupingRule` (Mask `grouping_rule`); geändert wird nur die Gruppe mit dem konfigurierten
  `ga4_channel_group_name`; die Regelliste führt die 19 Standardkanäle mit, der KI-Kanal steht
  vor „Referral“ (`eachScopeSource` / `eachScopeDefaultChannelGroup`, Namen aus Drittdoku, live
  zu belegen).
- **P2:** Titel/Beschreibung der Annotation durchlaufen den Scan auf E-Mail/Telefon/IBAN
  (`personal_data_rejected`, kein Echo); in der Änderungszeile nur Länge und Hash.
- **P3:** Tagesdeckel `WRITE_CAP_PER_DAY` = 20 je Quelle, **Site** und UTC-Tag in
  `WorkerPool.write_attempts` (Schlüssel `(site, source, day)`; Serverprozess, nicht Worker); zählt vor
  dem Google-Aufruf, Fehlschläge zählen, `dry_run` nicht; Neustart = null (dokumentiert). Höchstens
  5 × 20 = 100 je Quelle und Tag am ganzen Dienst (SEC D2). Die Minutengrenze 30/Quelle bleibt
  gemeinsam (D3).
- **P4:** Änderungszeile `change ts= site= source= op= target= fields= before= after=` nur bei
  `state=applied`, nur Felder aus `FIXED_FIELDS` (feste Typen) und `FREE_TEXT_FIELDS` (Länge/Hash).
- **Worker-Abbruch nach dem Google-Aufruf:** Dann entsteht keine Änderungszeile; die Spur liefert der
  GA4-Änderungsverlauf bzw. `list_*` (SEC R4, #1148).
- **H1/H2/H3:** Titelpräfix `[JP] `, Sitemap-URL streng (Host aus `hosts`, ASCII, `.xml`, ohne
  Query/Fragment/Port/`..`), unbekanntes v1alpha-Feld im Vorzustand = `unexpected_source_shape`
  vor dem Schreiben. Schreibaufrufe ohne Retries. Eine Änderung an diesen Punkten zieht die
  Mutationstests (`tests/mutations.py`) und die Dokus (README, SETUP.md) nach.

- **GA4-Status (#1150):** `checkCompatibility` antwortet für **alle** Dimensionen und Metriken der
  Property. `check_definitions` filtert auf die angefragten `apiName`s (`verify_compatible`):
  fehlt einer, `report_definition_unverified`; ist einer nicht `COMPATIBLE`,
  `report_definition_incompatible`; Fremdeinträge zählen nie. Kein Längenvergleich.
- **Spätes `end_date` (#1150):** wohlgeformt, aber nach „Quelle-heute minus 3 Tage“ ist es
  `end_date_not_available` (400, fester Code, kein Datum im Fehler, kein Klemmen); nur ein
  kaputtes Datum bleibt `invalid_input`. Die Fehler-JSON trägt weiter nur `{"error": code}`.

Mehrere Sites (#1379, Plan c-6875179147, Goldfische c-6875211441, SEC c-6875252159), Invarianten:
- **Die Site kommt nie aus MCP-Argumenten**, nur aus dem Header `X-Google-Reporting-Site`, den die
  Instanz aus ihrem Pflicht-Instanzwert `site` sendet. `?site=` und `site` im Body sind `invalid_input`.
- **Kein Rückfall auf die erste Site.** Einzelform (ohne `sites`) = Site `default`: ohne Header oder mit
  genau `default` gilt sie. Listenform: Header **immer** Pflicht, auch bei einem Eintrag
  (`site_required`); `default` ist dort als Name verboten. Mehrfach, leer, außerhalb von
  `[a-z][a-z0-9-]{0,31}` oder unbekannt = `site_unknown`. Exakter Byte-Vergleich, kein
  `lower()`/`strip()`, nie der Name im Fehler oder Log (Log: aufgelöste Site, sonst `-`).
- **Eine Anfrage berührt Property, Hosts und Pfade genau einer Site.** Freie URL-/Pfadparameter
  (`inspect_url`, Sitemap, `page_path`) werden nur gegen die aufgelöste Site geprüft. Die Property
  nimmt der Adapter aus `config_for(req)`, nie aus einem Feld des Dienstes.
- **Reihenfolge im Server:** Bridge-Key, dann Site, dann Parsen; **erst auflösen, dann zählen** (SEC D1):
  `site_required`/`site_unknown` zählen bei keiner Site und starten keinen Worker.
- Konfiguration: 1–5 Sites, Hosts disjunkt, `gsc_property` und `ga4_property` je Site eindeutig,
  `MAX_PATHS`/`MAX_REGEX` je Site; Mischform = `invalid_configuration`. Segmente seit 2.0.0 frei
  benannt (unten).
- Egress unverändert, ein Servicekonto, ein Bridge-Key, die Scope-Trennung nach W1 bleibt. `get_status` nennt
  `site` und `hosts`, nie Property-IDs.
- Die Mandantentrennung per Instanz setzt voraus, dass nur der Betreiber Instanzen anlegt (GUI,
  Admin-MCP hinter `target_address`; über `/mcp` nie).
Mutationen dazu: `site-falls-back-to-the-first`, `list-form-with-one-site-needs-no-header`,
`site-header-lowercased`/`-stripped`, `adapter-uses-the-first-site-config`, `write-cap-key-without-site`,
`count-before-the-site-is-resolved`, `site-name-in-the-error`.

Freie Zeiträume, `page_compare`, benannte Segmente (#1379 Teil B, LEAD c-6883881384), Invarianten:
- **`period_days`** ist `^[1-9][0-9]?$` und höchstens `MAX_PERIOD` = 90, Standard 28; `get_status` nimmt
  keinen Parameter. Der letzte Tag bleibt „Quelle-heute minus drei Tage“, auch für GSC (der kürzere
  Abstand ist nicht belegt; eine Messung mit `dataState` ist ein eigener Patch). `final_date` prüft
  `end_date` und `compare_end_date` gleich (`end_date_not_available`, kein Datum im Fehler).
- **`page_compare`** (nur GSC, `REPORTS["gsc"]`): Zeitraum A = `[end−p+1, end]`; B gleich lang, ohne
  `compare_end_date` unmittelbar davor, sonst endend am Wert; B muss ganz vor A liegen (`invalid_input`).
  `Request.compare_*_date` trägt B, `source_calls` = 2 (der Pool zählt beide Abfragen gegen die 30 je
  Quelle und Minute; kein Schreibwerkzeug, kein Tagesdeckel). `build_query(cfg, req, compare=True)` ist B;
  beide Abfragen verlangen `len(urls)` Zeilen (alle freigegebenen URLs), erst die fertige Liste wird
  auf `limit` gekürzt, sonst sähe eine Seite unter dem Zeilenlimit des anderen Zeitraums „unbekannt“ aus.
  `project_compare`: eine Zeile je Pfad mit Quellenzeile in A oder B; `a`/`b`/`delta` tragen je Feld
  `null` statt 0, wenn der Zeitraum keine Zeile hat; CTR und Position nur aus der Quellenzeile, nie
  gemittelt oder berechnet; zwei Zeilen desselben Pfads in einem Zeitraum (zwei URL-Varianten) ergeben
  `null` und die Note `page_rows_ambiguous`; Sortierung Klicks A absteigend, dann Pfad; Antwort mit
  `range` (A) und `compare_range` (B). Der Worker führt beide Abfragen nacheinander im 20-s-Budget aus.
- **Segmente:** `segments` ist ein Objekt aus 1–`MAX_SEGMENTS` (8) Namen `SEGMENT_NAME`
  (`[a-z][a-z0-9-]{0,31}`); `all_public` ist durch das Muster (kein Unterstrich) nie ein Name und
  bleibt fest für alle Pfade der Site. `parse_request` prüft `segment` gegen `cfg.segments` der
  **aufgelösten** Site; unbekannt oder fremd = `invalid_input`, nie ein Rückfall auf `all_public`, nie der
  Name im Fehler. `Sites.from_json` lädt die Datei mit `unique_keys` und lehnt doppelte Schlüssel ab
  (json.loads behielte still den letzten). `get_status` nennt `segments` (nur Namen).
Mutationen dazu: `missing-page-shown-as-zero`, `ctr-computed-from-clicks-and-impressions`,
`b-overlapping-a-accepted`, `period-91-accepted`, `gsc-distance-two-days`,
`unknown-segment-falls-back-to-all-public`, `nine-segments-accepted`, `duplicate-config-keys-accepted`,
`comparison-counted-once`, `second-call-asks-period-a-again`, `comparison-query-limited-to-the-output-limit`,
`delta-sign-flipped`, `ambiguous-path-keeps-one-row`.

Invarianten an allen Code-/Dokuorten nachziehen:512 KiB Antwort,8 KiB Anfrage,
Pfad 512,100 Rangzeilen/90 Tage,100 öffentliche Pfade je Site (1.0.1)/4096Regex,20 s/25 s,
2 Worker/30 pro Quelle pro Minute/keine Retries,query_enabled=false als Auslieferungsvorgabe (Betreiberschalter, SEC V5),
GA-Kleingruppenschwelle aus Messvertrag,Auditmetadaten je SSO/Token.
Privacy-Scan heißt field:query, weil Gateway JSON-Schlüssel rekursiv scannt.
Keine stillen Ersatzmetriken, Null-Fallbacks, freien Googleobjekte oder PII-Logs.

Secret-Ablage (Hausmuster #887, SEC B1): Stellen `plugin-google-reporting` (Bridge-Key,
Zufallswert, vom Betreiber per `jnpt credential set` über stdin gesetzt — zugleich als
Connector-Credential `google-reporting`, danach verworfen) und
`plugin-google-reporting-google` (Servicekonto-JSON, nur Admin-GUI) im Gateway-Tresor; die Ordner
unter `/run/jnpt/plugins/` kommen aus tmpfiles (`deploy/jnpt-plugins-google-reporting.
conf.example`), Compose hängt sie als Ordner read-only ein. Nie Klartext unter `/etc`
oder `/opt`. Der Dienst liest beim Start; Neustart nach Schlüsselwechsel und Boot macht
das eigene Unit-Paar `deploy/jnpt-google-reporting-key.{path,service}` (die Produkt-
Vorlage `jnpt-plugin-key@` kennt nur eine Stelle je Compose). Entfernen ist kein
Widerruf: Der Google-Schlüssel gilt bis zum Löschen in der Cloud Console.
`tests/test_deploy_files.py` hält Compose, Deskriptor, tmpfiles und Units zusammen.

Compose bindet ausschließlich reservierte Plugin-Netz-IP, kein Hostport.
jnpt_plugins bleibt internal; eigenes Egress-Netz benötigt Zielhost-Firewall.
Keine Behauptung, Compose erzwinge die Domain-Allowlist. Lokale Gesundheit und
Google-Readiness getrennt. Vollständiges Runbook und Live-Gates: README.md.

`SETUP.md` ist die Einrichtungsanleitung für den Admin und wird je Plugin-Version als
`setup_guide` in den signierten Katalog übernommen (JanuaPort/januaport#1137). Sie muss ohne
das Repo verständlich sein: höchstens 32 KiB, gültiges UTF-8, keine relativen Links, nur die
Handgriffe des Admins (Betreiberschritte bleiben im README). SEC V1 (minimale Google-Rechte,
Datei löschen, Kopie höchstens im Passwortmanager) steht dort und in
`tests/test_setup_guide.py`; wer die Rechte ändert, zieht beide nach. Seit #1148: GSC höchstens
„Uneingeschränkt“, GA4 höchstens „Bearbeiter“, keine IAM-Rolle (SEC-Obergrenze V1).

Tests enthalten nur synthetische Daten; Signer flüchtig generieren. Lock
mit pip-compile --generate-hashes erstellen, install --require-hashes.
Mutationstests müssen Bridge-Key-Auth, Vorprüfung, Filter, Retries und Kill abdecken, seit
#1148 auch Scope-Trennung, Tagesdeckel, P1 (primary), P2 (Privacy-Scan) und das Titelpräfix, seit
#1379 Site-Auflösung (Rückfall, Pflicht in der Listenform, Groß-/Kleinschreibung, Trimmen, Name im
Fehler), Adapter-Konfiguration je Anfrage und den Deckel-Schlüssel mit Auflösung vor dem Zählen.
Signatur/Katalog/Deployment nicht Teil der Implementierung.

Release (#1138): Version 2.0.0 steht im Kopf dieser Datei, im README-Titel und im CHANGELOG
(sonst nirgends: Deskriptor und Compose tragen keine Version, Anlagen beziehen per Digest aus dem
Katalog). Das Image baut nur `.github/workflows/google-reporting-release.yml` auf den Tag
`google-reporting-vX.Y.Z` (Tag setzt GATE, nie ein Branch). Es spiegelt agent-runner-release:
ein Build amd64 per Digest, SBOM/Provenance, Config-Digest-Vergleich (SEC C1), Sonden gegen den
Digest, Tag erst danach; Aktionen per SHA gepinnt, in Jobs mit `packages: write` nur `actions/*`
und `docker/*` (SEC C2). Abweichung zu den Go-Vorbildern: Der `unit`-Job installiert den
Hash-Lock und fährt `unittest` plus `tests/mutations.py`; die Sonden sind dieselbe Härtung wie
der Image-Job in `google-reporting.yml` (Tests read-only eingehängt, kein Netz) plus Start ohne
Konfiguration. `tests/test_release_workflow.py` hält das zusammen (überspringt sich ohne
`.github/`). Eine Änderung am Workflow zieht diesen Test und den CHANGELOG-Abschnitt „Kuratierung“ nach.
