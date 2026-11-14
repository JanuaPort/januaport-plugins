# Änderungen — Google Reporting

## 2.0.0 — 2026-11-14

- JanuaPort/januaport#1379 (Teil B): **freie Zeiträume.** `period_days` ist eine ganze Zahl von
  1 bis 90 als Dezimalstring ohne führende Null (`^[1-9][0-9]?$`, höchstens 90), Standard 28, für
  alle Berichte beider Quellen außer `get_status`. Vorher nur 28 oder 90. 0, 91, `07`, `7.0`, leer,
  `-1` und `1e1` sind `invalid_input`. `end_date` bleibt höchstens „Quelle-heute minus drei Tage“
  (sonst `end_date_not_available`); der GSC-Abstand von drei Tagen ist nicht belegt und bleibt, eine
  Messung mit `dataState` folgt gesondert.
- **Neuer Bericht `gsc/page_compare`** (nur GSC): je freigegebener Seite `clicks`, `impressions`,
  `ctr`, `position` für zwei gleich lange Zeiträume A und B und das `delta` (A minus B). Parameter
  wie `page_summary` (`period_days`, `end_date`, `segment`, `page_path`, `limit`) plus optional
  `compare_end_date`. B liegt ohne `compare_end_date` unmittelbar vor A und muss sonst vollständig
  vor A liegen (`invalid_input`); `compare_end_date` hat dieselbe Datumsprüfung wie `end_date`. Zwei
  Search-Console-Abfragen (Dimension `page`, derselbe Seitenfilter), beide zählen gegen die 30 je
  Quelle und Minute. Fehlt einer Seite in einem Zeitraum die Zeile, sind Werte und `delta` `null`
  (nicht 0) mit Hinweis in `quality.notes`; CTR und Position kommen aus der Quellenzeile und werden
  nie gemittelt oder berechnet. Antwort mit `range` (A) und `compare_range` (B). Beide Abfragen
  verlangen alle freigegebenen URLs, `limit` kürzt die sortierte Liste (Klicks in A absteigend,
  dann Pfad).
- **Frei benannte Segmente je Site** (LEAD-Entscheid c-6883881384): `segments` hat 1 bis 8 Namen nach
  `[a-z][a-z0-9-]{0,31}`; `all_public` bleibt reserviert und fest. Die Pfadliste ist unverändert
  (`gsc_urls` = Vereinigung aller Segmente, ein Pfad in höchstens einem Segment). `segment` wird
  gegen die Segmente der aufgelösten Site geprüft: unbekannt oder das Segment einer anderen Site ist
  `invalid_input`, nie ein stiller Rückfall auf `all_public`, nie der Name im Fehler. Die Site
  `januaport` trägt weiter genau `atas`, `gateway`, `shared`. `get_status` nennt die Segmentnamen.
  Ein doppelter Schlüssel in `config.json` (auch ein doppelter Segmentname) ist
  `invalid_configuration`.
- Keine neue Abhängigkeit, Lock unverändert, Egress, Servicekonto, Bridge-Key, Scope-Trennung und
  Tagesdeckel unverändert, kein `page_compare` für GA4. Die Vorlagen `google-search-console` und
  `google-analytics` heben auf 2.0.0 (MAJOR); Bestandsinstanzen werden nicht automatisch
  angehoben, der Instanzwert `site` bleibt. Siehe README „Anhebung auf 2.0.0“.
- Tests: Zeitraumtabelle je Quelle, Enddatum am Tageswechsel je Zeitzone, Bereiche A und B für
  p = 1, 7, 90, Seite nur in A, nur in B und in beiden, Golden für beide Abfragen, Minutengrenze,
  zweite Site, Segmenttabelle (0/1/8/9 Segmente, ungültige und doppelte Namen, fremdes und
  unbekanntes Segment) und 13 neue Mutanten.

## 1.1.0 — 2026-11-14

- JanuaPort/januaport#1379 (Teil A): **mehrere Sites in einem Dienst, Rechte je Site.** `config.json`
  führt optional die Liste `sites` (1–5 Einträge mit `name` und den Feldern der bisherigen
  Konfiguration); die Einzelform bleibt gültig und ist die Site `default`, ohne Migration. Hosts
  sind je Site disjunkt, `gsc_property` und `ga4_property` eindeutig, `MAX_PATHS`/`MAX_REGEX`
  gelten je Site. Die Site kommt aus dem Header `X-Google-Reporting-Site`, den die Instanz aus
  ihrem Pflicht-Instanzwert `site` sendet (Vorlagen 1.1.0); sie ist nie ein MCP-Parameter. Der
  Dienst löst sie nach der Bridge-Key-Prüfung und vor jedem Parsen auf. Einzelform: ohne Header
  oder `default`. Listenform: Header immer Pflicht, auch bei einem Eintrag (`site_required`);
  mehrfach, leer, außerhalb des Musters oder unbekannt ist `site_unknown`; exakter Byte-Vergleich,
  kein Rückfall auf die erste Site, nie der Name im Fehler. Eine Anfrage berührt Property, Hosts und
  Pfade genau einer Site; fremde Sitemap-Hosts, Seiten und Pfade sind `invalid_input`. Berichte
  tragen `scope.site`, `get_status` nennt `site` und `hosts` (nie Property-IDs), Verwaltungsantworten
  `site`; Log- und Änderungszeile tragen `site=`.
- Tagesdeckel (SEC D1): Schlüssel `(site, source, day)`, erst die Site auflösen, dann zählen;
  `site_required` und `site_unknown` zählen nirgends. Höchstens 5 × 20 = 100 Schreibversuche je
  Quelle und Tag am ganzen Dienst (D2). Die Minutengrenze 30/Quelle bleibt gemeinsam (D3).
- Neue Beispieldatei `config.example.sites.json`. Keine neue Abhängigkeit, Lock unverändert,
  Egress, Servicekonto, Bridge-Key und die Scope-Trennung nach W1 unverändert. Keine freien Zeiträume,
  kein `page_compare` (Teil B), keine neuen Segmente.
- Tests: Konfigurationstabelle, Auflösungstabelle, HTTP-Matrix (Header × Form × Anfrageart),
  Trennung bis in jeden Google-Aufruf, Deckel A×20/A21/B1, acht neue Mutanten. Die Betreiberfolge
  (Umstellung und Anhebung der Bestandsinstanzen in einem Wartungsgang) steht im README.

## 1.0.1 — 2026-10-28

- JanuaPort/januaport#1176: Die Betreiberkonfiguration nimmt zusammen bis zu **100** öffentliche
  Pfade statt 50 (`MAX_PATHS`), für die Integrationsseiten-Serie. Es bleibt bei exakten Pfaden
  ohne Präfix. Die GSC-Regex-Grenze von 4096 Zeichen über alle URLs bleibt unverändert und
  fail-closed: Eine Konfiguration darüber lädt nicht und läuft nie ungefiltert. Mit einem Host und Pfaden wie
  `/integrationen/<name>` passen rund 90 Pfade. Tests: 100 laden, 101 abgewiesen (die Regex liegt
  dabei unter der Grenze), lange Pfade scheitern an der Regex-Grenze, der Segmentfilter trifft jeden
  der 100 Pfade exakt und keinen Unterpfad. Keine neue Abhängigkeit, keine Änderung an Routen
  oder Antworten.

## 1.0.0 — 2026-10-22

**Erste Release-Fassung.** Sie umfasst JanuaPort/januaport#1130 (Berichte),
#1148 (Schreibhandgriffe) und #1150 (Statusprüfung, spätes `end_date`). Die Release-Strecke
(JanuaPort/januaport#1138) ist `.github/workflows/google-reporting-release.yml`: Tag
`google-reporting-vX.Y.Z` → Hash-Lock, Unit-Tests und Mutationsproben → ein Build `linux/amd64`
mit SBOM/Provenance per Digest nach `ghcr.io/januaport/google-reporting` → Config-Digest-Vergleich
(SEC C1) und Sonden gegen genau diesen Digest → Image-Tag `X.Y.Z` auf denselben Digest. Trivy-Bericht
als Artefakt, kein Gate. SHA-Pins und Schreibrechte nach dem agent-runner-/script-runner-Vorbild
(SEC C2), Wächter in `tests/test_release_workflow.py`. Kein Katalogeintrag (VENDOR, nach Live-Beleg).

- JanuaPort/januaport#1150: GA4 `get_status` meldete auf echten Properties immer `unavailable`.
  `checkCompatibility` antwortet für alle Dimensionen und Metriken der Property; die Prüfung
  verglich die Länge mit den angefragten. Jetzt zählen nur die angefragten `apiName`s: jeder
  muss vorkommen und `COMPATIBLE` sein (`report_definition_unverified` bzw. `_incompatible`
  sonst). Ein wohlgeformtes, aber zu spätes `end_date` endet mit dem festen Code
  `end_date_not_available` (400) statt pauschal `invalid_input`; kein Echo, kein Klemmen.
  Tabellentests, REST-Test mit Ganz-Property-Antwort, drei neue Mutanten.
- JanuaPort/januaport#1148: drei Schreibhandgriffe für das Inbound-ATAS-Team: GA4-Annotation
  (anlegen, ändern, auflisten), GSC-Sitemap (einreichen, auflisten, lesen), GA4-Kanalgruppe
  „KI-Assistenten“ (anlegen, ändern, auflisten), dazu die URL-Inspektion (lesend). Feste Routen
  je Operation (`/v1/gsc/…`, `/v1/ga4/…`, kein `delete`), Site und Property nur aus der
  Betreiberkonfiguration, je Schreibscope (`webmasters`, `analytics.edit`) ein eigenes
  Credential-Objekt, `dry_run` mit `before`/`after`, Tagesdeckel 20 Schreibversuche je Quelle
  im Serverprozess, Änderungszeile im Log (Freitext nur als Länge und Hash), Titelpräfix `[JP]`,
  Privacy-Scan der Annotationstexte, Kanalgruppe nie `primary` und mit den Standardkanälen.
  Neuer optionaler Konfigurationswert `ga4_channel_group_name`. Keine neue Abhängigkeit: Admin
  API v1alpha über den vorhandenen, gepinnten Discovery-Client. SETUP.md: GSC „Uneingeschränkt“,
  GA4 „Bearbeiter“, dritte API. Ändert die Log-Invariante „Route/Code/Dauer“ um die
  Änderungszeile bei angewendeten Schreibaufrufen. Gebaut/synthetisch geprüft; Live-Abnahme
  (Staging, dann mit PO-Go) offen.
- JanuaPort/januaport#1137: Einrichtungsanleitung für den Admin in `SETUP.md` (Google-Seite mit
  minimalen Rechten nach SEC V1, Eintrag des Servicekonto-JSON, Nutzer-Scopes, Prüfung,
  Schlüsselwechsel). Sie geht als `setup_guide` in den signierten Plugin-Katalog; das README
  verweist darauf. `tests/test_setup_guide.py` prüft 32 KiB, UTF-8 und den V1-Inhalt.
- #1130: privater Python-REST-Beisteller mit acht festen GSC-/GA4-Berichten,
  getrennten Readonly-Scopes, öffentlichen Seitenfiltern,
  killbaren begrenzten Prozessen und flüchtigen Token-Caches.
- Gepinnte offizielle Clients und Hash-Lock, abgesicherte Container-/Hostvorlage,
  Runbook sowie synthetische HTTP-/SDK-/Abbruch-/Mutationstests.
- Query-Freitext standardmäßig gesperrt (Betreiberschalter). Auditmetadaten, minimale Grants,
  GA4-Mess-/Consentvertrag, Zielhost-Netz und Live-Durchstich bleiben Freigabegates.
  Keine Veröffentlichung, Katalogsignatur oder produktive Abnahme in diesem Paket.
- #1130 B1 (SEC-Zweitprüfung): Secret-Ablage nach Hausmuster #887. Ein Bridge-Key für
  beide Quellen (Stelle `plugin-google-reporting`, Datei `runtime-key`) statt zwei
  getrennter Keys; `bridge_keys_must_differ` und die Quellentrennung im Bridge-Auth
  entfallen. Der Bridge-Key ist ein Zufallswert und wird per Betreiber-CLI (stdin) in Stelle und
  Connector-Credential `google-reporting` gesetzt. Das Servicekonto-JSON kommt aus der
  Stelle `plugin-google-reporting-google` (Admin-GUI, Basis-Einrichtung). Compose mountet zwei Ordner read-only
  (`/run/secrets/jnpt`, `/run/secrets/google`) statt dreier Datei-Mounts; Deskriptor-
  REQUIRE, tmpfiles-Beispiel und ein eigenes Unit-Paar für Neustart bei Schlüsselwechsel
  und nach Boot sind neu. `load_identity` akzeptiert das JSON auch ohne Zeilenumbrüche
  (GUI-Passwortfeld). Entfernen ist kein Widerruf: Der Google-Schlüssel gilt bis zum
  Löschen in der Cloud Console. Alt: `google-service-account.json`, `gsc-bridge-key`,
  `ga4-bridge-key` (nie veröffentlicht).

Die folgende Vorlage ist kein Release-Nachweis; GATE trägt die Werte nach dem grünen Release-Lauf
und vor dem Katalogeintrag ein, nach dem Muster von agent-runner und script-runner.

### Kuratierung v1.0.1 — 2026-10-28 (GATE)

| Feld | Wert |
|---|---|
| Image | `ghcr.io/januaport/google-reporting` |
| Tag | `google-reporting-v1.0.1` (Image-Tag `1.0.1`) auf `b769332` |
| Digest (OCI-Index) | `sha256:797f59c787fff429e8ac8c0ce1c4276173c2481e787a2aa6c4f9db014b4cd651` |
| Plattform | `linux/amd64` (`sha256:d3a87a685bfd8f6facc8a2c5107d5a7dab30403a8f57f9947394b213da9e0e9c`), sonst keine; dazu der Attestations-Eintrag `sha256:4ffa62f8…` |
| Art | `service`, eigener Build (Plugins-Repo) |
| Geprüft mit | JanuaPort 0.87.0 (Showcase, 28.10.2026) |
| Benötigter Commit von Compose | `docker-compose.yml` wie seit `8c96724` unverändert |
| Lauf der Sonden | Release-Lauf `40716415122`: hat genau diesen Digest geprüft (SEC C1) |

**Änderung gegenüber 1.0.0:** nur `MAX_PATHS` 50 → 100 (#1176). Die Regex-Grenze von 4096 Zeichen bleibt fail-closed.

**Scan:** Trivy, Scan vom 2026-10-28T15:31Z, Debian 13.7: **0 critical · 44 high**, alle **ohne behobene Fassung** in der Debian-Basis von `python:3.12-slim`. Es sind dieselben wie bei 1.0.0 (util-linux-Familie, libacl1, ncurses, libsystemd0/libudev1, perl-base). Mit Fix gibt es nur 5 medium und 1 low. Bewertung wie bei 1.0.0.

**Live auf dem Showcase seit 28.10.2026 16:05Z** (Sicherung `.env.vor-1.0.1`): Container healthy, Konfiguration `januaport-2026-10-28` mit 45 Pfaden lädt. GSC `get_status` ready, `page_summary` über 28 Tage liefert 19 Zeilen.

**Entscheid:** löst 1.0.0 auf dem Showcase ab. Katalogfähig; nur 1.0.1 kommt in den Katalog (#1219), den Eintrag setzt VENDOR mit PO-Go (PO-Go 28.10. im Fenster von VENDOR).

### Kuratierung v1.0.0 — 2026-10-22 (GATE)

| Feld | Wert |
|---|---|
| Image | `ghcr.io/januaport/google-reporting` |
| Tag | `google-reporting-v1.0.0` (Image-Tag `1.0.0`) auf `3c1ad7f` |
| Digest (OCI-Index) | `sha256:540b1bb78c215c5bf737d65651aa7e9f00115dadcc2817bd078edc29aff52119` |
| Plattform | `linux/amd64` (`sha256:71241b0ef550614f60934658c558fb2cd223c07875218a3d341347671a8fb16f`), sonst keine; dazu der Attestations-Eintrag `sha256:db231ea7…` |
| Art | `service`, eigener Build (Plugins-Repo) |
| Geprüft mit | JanuaPort 0.82.0 (Showcase, 22.10.2026) |
| Benötigter Commit von Compose | `docker-compose.yml` wie seit `793b79d` unverändert (byte-gleich mit 3c1ad7f, sha256 `abd4cc51…`) |
| Lauf der Sonden | Release-Lauf `39748937674`: hat genau diesen Digest geprüft (SEC C1, Config `sha256:6d135481…` geprüft = gepusht) |

**Digest** zweimal gemessen am 22.10.2026: `digest.txt` im Artefakt des Laufs und `docker buildx imagetools inspect …:1.0.0`. Beide stimmen überein. SBOM und Provenance hängen als Attestation am Index.

**Sonden:** grün im Lauf `39748937674`. Benutzer 65532:65532, Unit-Tests im Container ohne Netz, read-only, `cap_drop ALL`. Ein Start ohne Konfiguration endet mit Exit 1 und genau `startup_failed`.

**Scan:** Trivy 0.74.0 (Artefakt `trivy-google-reporting-v1.0.0`), Datenbank vom 22.10.2026, Debian 13.7, Scanner `vuln`: **0 critical · 44 high** · 63 medium · 62 low · 1 unknown. Alle 44 high liegen in der Debian-Basis von `python:3.12-slim` und haben **keine behobene Fassung**: util-linux-Familie (CVE-2026-76642, -78408, -78409, -78410), libacl1 (CVE-2026-54369), ncurses (CVE-2025-69720), libsystemd0/libudev1 (CVE-2026-16742), perl-base (CVE-2026-9538). Keiner davon liegt in den Python-Paketen aus dem Hash-Lock. Der Dienst ruft keines dieser Programme auf und läuft als 65532 mit read-only Root-FS ohne Capabilities. Mit Fix gibt es nur 5 medium und 1 low, aufzunehmen beim nächsten Neubau.

**Live auf dem Showcase seit 22.10.2026 08:40Z** (statt des lokalen Images 793b79d): Container healthy, Egress-Netz erreicht den Dienst nicht. GSC `get_status` ready, **GA4 `get_status` ok mit `report_definitions: validated`** (#1150 live), GSC/GA4-Berichte über 28 Tage liefern Daten.

**Entscheid:** Der Showcase-Betrieb per Digest ist freigegeben. Einen Katalogeintrag gibt es **noch nicht**: Er folgt nach dem Live-Beleg der Schreibhandgriffe (#1148, Staging, dann PO-Go) durch VENDOR mit PO-Go.

Quelle: JanuaPort/januaport#1138, #1130, #1148, #1150.

### Kuratierung

```markdown
### v1.0.0 — <Datum>

| Feld | Wert |
|---|---|
| Image | `ghcr.io/januaport/google-reporting` |
| Tag | `google-reporting-v1.0.0` (Image-Tag `1.0.0`) |
| Digest (OCI-Index) | `sha256:<nach Release ergänzen>` |
| Plattform | `linux/amd64` (`sha256:<nach Release ergänzen>`), sonst keine |
| Art | `service`, eigener Build (Plugins-Repo) |
| Geprüft mit | <JanuaPort-Fassung nach Live-Beleg bestätigen> |
| Benötigter Commit von Compose | `<Commit im Plugins-Repo>` (`docker-compose.yml`) |
| Lauf der Sonden | Release-Lauf `<Lauf-ID>` — hat genau diesen Digest geprüft (SEC C1) |

**Digest** zweimal gemessen am <Datum>: Zusammenfassung des Release-Laufs (`digest.txt` im
Artefakt) und `docker buildx imagetools inspect`. Beide stimmen überein. SBOM und Provenance
hängen als Attestation am Index.

**Sonden:** grün im Lauf `<Lauf-ID>` gegen genau diesen Digest: Benutzer 65532:65532,
Unit-Tests im Container ohne Netz, read-only, alle Capabilities entfernt; Start ohne
Konfiguration endet mit Exit 1 und genau `startup_failed`. Der Config-Digest des geprüften
Images ist gleich dem der amd64-Plattform des gepushten.

**Scan:** Trivy <Fassung> (Artefakt des Laufs), Datenbank vom <Datum>, Scanner `vuln`:
**<n> critical · <n> high** · <n> medium · <n> low · <n> unknown. Hohe Funde einzeln, je mit
behobener Fassung oder Begründung (Python-Pakete aus dem Hash-Lock, Debian-Basis).

**Inhalt:** `python:3.12-slim` (im Dockerfile per Digest gepinnt), Pakete nur aus
`requirements.lock` mit Hashes, Dienst `google_reporting`, Benutzer 65532.

**Isolation:** UID/GID 65532, read-only Root-FS, `cap_drop: ALL`, no-new-privileges,
Schlüsselordner nur lesend, internes Plugin-Netz plus eigenes Egress-Netz (Zielhost-Firewall
beim Betreiber, Compose erzwingt keine Domain-Allowlist).

**Grenzen v1:** nur `linux/amd64`; Live-Beleg der Berichte und Schreibhandgriffe steht aus
(README, Live-Gates). Ein Image, das eine neue Compose braucht, ist ein Wartungsschritt mit
Runbook-Eintrag, kein stilles Katalog-Update.

**Entscheid:** <katalogisiert / nicht katalogisiert>, weil <Begründung>.

Quelle: JanuaPort/januaport#1138, #1130, #1148, #1150.
```
