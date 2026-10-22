# Google Reporting 1.0.0 — #1130

Stufe 1 „Sicher verbinden“. Gebaut und synthetisch geprüft; **noch nicht live
abgenommen oder produktiv provisioniert**, die Fassung 1.0.0 (#1130, #1148, #1150) ist
**noch nicht getaggt**: Der Release-Workflow steht, den Tag setzt GATE. Apache 2.0.
Ein privater Python-Beisteller übersetzt acht feste GET-Berichte in offizielle
Google-REST-Aufrufe. JanuaPort behält Rechte, Audit und Pseudonymisierung.
Keine Accountsuche, freien Dimensionen/Metriken, Nutzer-/Session-/Lead-IDs,
Realtime-, Ads-API oder Odoo-Verbindung. Seit #1148 kommen fünf lesende und fünf
schreibende Verwaltungsrouten dazu (Abschnitt „Verwaltungshandgriffe“): genau die drei
Handgriffe Annotation, Sitemap und Kanalgruppe, kein Löschen, keine weitere Admin-API.

## Vertrag

| Quelle | GET-Pfad unter `/v1/` | Ergebnis |
|---|---|---|
| GSC | `gsc/get_status` | kleine Zugriffsprobe, feste Zustände |
| GSC | `gsc/performance_overview` | Datum, Klicks, Impressionen, CTR, Position |
| GSC | `gsc/query_summary` | begrenzte Query-Auswahl; standardmäßig gesperrt |
| GSC | `gsc/page_summary` | ausschließlich freigegebene öffentliche Pfade |
| GA4 | `ga4/get_status` | Probe plus Kompatibilitätsprüfung der Berichtsdefinitionen (nur die angefragten `apiName`s, jeder muss in der Antwort stehen und `COMPATIBLE` sein) |
| GA4 | `ga4/traffic_summary` | `sessionDefaultChannelGroup`, sessions, engagedSessions |
| GA4 | `ga4/landing_pages` | `landingPage` ohne Querystring, sessions, engagedSessions |
| GA4 | `ga4/key_events` | nur konfigurierte bestehende eventName, keyEvents |

`Authorization: Bearer <Bridge-Key>`: **ein** Bridge-Key für beide Quellen
(Stelle `plugin-google-reporting`, Datei `runtime-key`). Die Rechte je Werkzeug
prüft das Gateway, nicht der Dienst. Status nimmt keine Parameter an.
Alle Berichte erlauben `period_days` (28/90, Default 28), `end_date` (ISO),
`segment` (`all_public`/`atas`/`gateway`/`shared`, Default all_public).
GSC erlaubt optional `page_path` innerhalb des Segments; Ranglisten `limit`
(1–100, Default 25). Übersicht maximal 90 Tageszeilen. Unbekannte, doppelte,
falsch typisierte oder unzulässige Parameter scheitern **vor Auth/Google**.
JanuaPort validiert Enums/einfache Typen; das Zahlenintervall prüft dieser Dienst
(Connector-Enums unterstützen heute nur Strings). Zusätzliche MCP-Argumente
ignoriert die Runtime und schwärzt das Audit; sie erreichen den Dienst nicht.

Der Zeitraum endet höchstens **Quelle-heute minus drei Tage**, inklusiv;
Start = Ende minus Zeitraum plus eins. GSC: `America/Los_Angeles`, GA4:
konfigurierte Property-Zeitzone. Liefert GA4 eine andere Zeitzone, schlägt der
Bericht fehl; fehlende Metadaten sind als nicht verifiziert markiert.
GSC-Tage und GA4-Tage sind keine identischen Intervalle. Sessions sind keine
Klicks, keyEvents keine qualifizierten Leads oder Odoo-Ergebnisse.
Ein wohlgeformtes `end_date` nach diesem letzten Tag (GSC: Pazifikzeit, GA4:
Property-Zeitzone) scheitert mit dem eigenen festen Code `end_date_not_available`
(400, ohne Echo des Datums und ohne Klemmen); ein kaputtes Datum bleibt `invalid_input`.
Wer den Zeitraum nicht festlegen muss, lässt `end_date` weg: Dann gilt genau dieser letzte Tag.

Antworten tragen schema_version 1.0, Quelle/Bericht, `ok`/`partial`/`no_rows`/
`not_configured`, Bereich, tatsächliche Pfadmenge/Konfigurationsfassung, Zeilen,
strukturierte Qualität und Abrufzeit. Keine Google-Rohantwort. GSC nutzt stets
`web`, `final`, `auto` und kennzeichnet die begrenzte Top-Zeilen-Sicht; keine
Summen der Auswahl und keine arithmetisch gemittelte CTR/Position. Fehlende Tage
sind kein Erhebungsbeginn. GA4 nutzt v1beta, feste Filter und
returnPropertyQuota=true; Sampling, Thresholding, `(other)`, Trunkierung,
Metrikbeschränkung, Zeilenanzahl und sichere Quotenfelder bleiben unterscheidbar.
Nicht gelieferte Merkmale bleiben unbekannt. Freie Diagnose-Texte werden verworfen.
Eine echte zulässige Null bleibt Null; unterdrückte/fehlende Zeilen werden nie Null.

Fehler: 400 Eingabe (`invalid_input`, `end_date_not_available`), 401/403 Bridge, 429 lokale Bremse, 502 Quelle, 503 Betrieb/
Quoten, 504 Budget. Nur feste Codes, keine URLs/Exceptions/Secrets. Nur get_status
übersetzt Zugriffs-/Betriebsfehler in feste Diagnosezustände ready, not_configured,
access_denied, quota_limited, unavailable. Normale Berichte behalten HTTP-Fehler;
JanuaPort zeigt bei Nicht-2xx seine bestehende Statusmeldung ohne Fehlerbody.

## Verwaltungshandgriffe (JanuaPort/januaport#1148)

Für das Inbound-ATAS-Team: der Funnel lässt sich ohne menschliches Eingreifen anpassen.
Die Produktvorlagen heißen `google-search-console-manage` und `google-analytics-manage`
(je 1.0.0, derselbe Bridge-Key); die Lesevorlagen bleiben unverändert.

| Quelle | Route unter `/v1/` | Methode | Google-Aufruf, Scope |
|---|---|---|---|
| GSC | `gsc/list_sitemaps` | GET | `sitemaps.list`, `webmasters.readonly` |
| GSC | `gsc/get_sitemap` (`sitemap_url`) | GET | `sitemaps.get`, `webmasters.readonly` |
| GSC | `gsc/inspect_url` (`page_path`) | GET | `urlInspection.index.inspect`, `webmasters.readonly` |
| GSC | `gsc/submit_sitemap` | POST | `sitemaps.submit` (PUT), `webmasters` |
| GA4 | `ga4/list_annotations` | GET | Admin v1alpha `reportingDataAnnotations.list`, `analytics.readonly` |
| GA4 | `ga4/create_annotation` | POST | `reportingDataAnnotations.create`, `analytics.edit` |
| GA4 | `ga4/update_annotation` | POST | `get` (readonly), dann `patch`, `analytics.edit` |
| GA4 | `ga4/list_channel_groups` | GET | `channelGroups.list`, `analytics.readonly` |
| GA4 | `ga4/create_channel_group` | POST | `list` (readonly), dann `create`, `analytics.edit` |
| GA4 | `ga4/update_channel_group` | POST | `get` (readonly), dann `patch`, `analytics.edit` |

Es gibt **eine feste Route je Operation**, keinen generischen Pfad und kein `delete`/`archive`
(404). Site und Property kommen ausschließlich aus der Betreiberkonfiguration, nie aus einem
Parameter. Ein Schreibaufruf holt seinen Schreib-Scope über ein **eigenes Credential-Objekt**;
die Lesewege und der Vorzustand eines Schreibaufrufs bleiben auf dem Readonly-Scope, die Scopes
werden nie kombiniert (`tests/test_manage_adapter.py` pinnt die Menge je Operation).

- **Eingaben** (Body ein JSON-Objekt, höchstens 8 KiB, doppelte und unbekannte Schlüssel gleich
  400; Fehlercode fest, kein Echo): Sitemap-URL nur `https://<konfigurierter Host>/…xml`, ohne
  Query, Fragment, Port, Benutzerteil, `..`, Nicht-ASCII (und bei URL-Präfix-Property mit dem
  Präfix); `inspect_url` nur Pfade aus der öffentlichen Seitenliste; Annotation: **ein** Datum
  (2020-01-01 bis heute + 366 Tage), Titel höchstens 55 Zeichen (Google: 60 inklusive Präfix),
  Beschreibung höchstens 150, Farbe aus `PURPLE BROWN BLUE GREEN RED CYAN` (Vorgabe `BLUE`);
  Kanalgruppe: Liste von 1–20 kleingeschriebenen Quell-Hostnamen, kommagetrennt. Titel und
  Beschreibung laufen durch einen Scan auf E-Mail, Telefonnummer und IBAN (Muster des
  Gateway-Privacy-Scans, `personal_data_rejected`).
- **Annotation `[JP]`:** Der Dienst setzt jedem Titel `[JP] ` voran. `update_annotation`
  ändert nur Annotationen mit diesem Präfix, die nicht vom System erzeugt sind; andere gibt
  der Dienst mit `annotation_not_managed` zurück, ohne zu schreiben.
- **Kanalgruppe:** Der Name kommt aus `ga4_channel_group_name` (Vorgabe „KI-Assistenten“), nie
  aus einem Parameter; `primary` gibt es nicht als Parameter und steht immer auf `false`. Eine
  Gruppe, die ein Mensch angelegt hat (anderer Anzeigename), und die Standardgruppe fasst der
  Dienst nicht an (`channel_group_not_managed`); `update_channel_group` ändert per Patch nur die
  Regeln. Die gebaute Regelliste setzt den KI-Kanal (`eachScopeSource` in der Hostliste) an
  die erste Stelle, danach spiegelt sie die 19 Standardkanäle von Google über
  `eachScopeDefaultChannelGroup`. Eine eigene Gruppe ordnet jede Sitzung ein; ohne diese
  Spiegelung landete alles andere unter „Unassigned“. Google erlaubt 2 eigene Gruppen je
  Property (360: 5) mit höchstens 50 Regeln.
- **Vorschau und Antwort:** Jeder Schreibhandgriff kennt `dry_run` (JSON-Boolean). Die Antwort
  trägt immer `state` (`applied`, `preview`, `unchanged`), `target`, `changed_fields`, `before`
  und `after`; eine Vorschau ruft nichts Schreibendes bei Google auf.
- **Tagesdeckel:** höchstens 20 Schreibversuche je Quelle und UTC-Tag. Der Zähler liegt im
  langlebigen Serverprozess (nicht im Worker, der bei Abbruch getötet wird), zählt den Versuch
  vor dem Google-Aufruf und damit auch Fehlschläge, nicht aber `dry_run`. Der 21. Versuch gibt
  429 `write_cap_reached`. Ein Neustart des Containers setzt den Zähler auf null; das ist eine
  dokumentierte Grenze, ein Aufrufer kann ihn nicht auslösen. Schreibaufrufe laufen ohne Retries.
- **v1alpha:** Annotationen und Kanalgruppen gibt es nur in der Admin API v1alpha; ihre Form
  kann sich ändern. Trägt ein Datensatz, den der Dienst überschreiben würde, ein unbekanntes
  Feld, bricht er mit `unexpected_source_shape` ab, bevor er schreibt. Der Dienst nutzt dafür
  den vorhandenen Discovery-Client (`analyticsadmin` v1alpha aus dem gepinnten
  `google-api-python-client`), keine zusätzliche Abhängigkeit.
- **Änderungszeile:** Für jeden **angewendeten** Schreibaufruf schreibt der Dienst genau eine
  Zeile `change ts=… source=… op=… target=… fields=… before={…} after={…}`. Werte stehen nur für
  Felder mit festem Typ (Datum, Farbe, Hostnamen, Sitemap-URL, Flags); Titel und Beschreibung
  nur als Länge und SHA-256-Präfix. Token, Schlüssel und Bridge-Key stehen nie darin. Das
  Gateway-Audit hält fest, wer welches Werkzeug mit welchem Ergebnis aufrief; diese Zeile hält
  fest, was sich änderte. Eine Mail bei Schreibaufrufen gibt es nicht (eigener Produktschnitt).
- **Grenzen, ehrlich:** `sitemaps.submit` meldet die Sitemap nur an und löst kein Crawling aus;
  „Indexierung beantragen“ hat keine öffentliche API (die Indexing API gilt nur für
  JobPosting und BroadcastEvent und ist nicht eingebaut). Der Sitemap-Ping ist seit 2023
  abgeschaltet. Suchvolumen liefert keine dieser APIs. Die Rollen-Obergrenze bei Google
  (GSC „Uneingeschränkt“, GA4 „Bearbeiter“, keine IAM-Rolle) bestimmt weiter den Schaden bei
  einem abgeflossenen Schlüssel; die Routenliste schützt nur den Weg über das Gateway.

Fehlercodes dieser Routen: `invalid_input`, `personal_data_rejected` (400), `route_not_found`
(404), `sitemap_not_found`, `annotation_not_found`, `channel_group_not_found` (404),
`annotation_not_managed`, `channel_group_not_managed`, `channel_group_exists` (409),
`write_cap_reached` (429), `unexpected_source_shape`, `source_rejected` (502).

## Konfiguration und Datenschutz

`config.example.json` ist vollständig synthetisch. Optional `ga4_channel_group_name`
(1–80 Zeichen, Vorgabe „KI-Assistenten“): Name der einen Kanalgruppe, die der Dienst anlegen
und ändern darf. Pflicht: feste GSC-Property,
GA4-Property-ID ohne `properties/`, IANA-Zeitzone, exakte kanonische Hosts,
drei disjunkte Gruppen mit zusammen 1–50 öffentlichen Pfaden und explizite
vollständige GSC-URLs je Pfad/Host. Keine Querystrings, Fragmente, dynamischen
Nutzerpfade. Pfade höchstens 512 Zeichen, GSC-Regex höchstens 4096 Zeichen;
Überschreitung schlägt fehl, kein ungefilterter Fallback. GSC-URLs müssen zur
Property gehören. GA4 filtert **immer** sowohl landingPage-Werteliste als auch
hostName-Werteliste, auch bei gemischten Properties. Deren Kompatibilität bleibt
vor Live-Freigabe zu bestätigen; keine stillen Ersatzmetriken.

`query_enabled` ist **false als sichere Auslieferungsvorgabe** (Betreiberschalter).
Der erste Start läuft mit `false`, bis die Zahlenberichte live belegt sind; danach
darf der Betreiber den Schalter ohne weitere SEC-Prüfung einschalten (SEC-Zweitprüfung
#1130, V5). Benanntes Restrisiko: Eine Anfrage mit einem Personennamen und mindestens
10 Impressionen kommt durch. Ein vorhandenes Tool ist keine Freigabe der Query-Sicht.
`query_min_impressions` mindestens 10; kleine Query-Gruppen werden unterdrückt.
Die Vorlage scannt das JSON-Feld `query` rekursiv auf email, telefon, iban.
Schwelle und Musterscan anonymisieren keinen Freitext und erkennen keine Namen.
Numerische Übersichten/Seiten können separat freigegeben werden.

`ga4_min_group_size` (Default 10, mindestens 1) gilt bei Landingpages für Sessions,
bei Key-Events für Ereignisanzahl. Den Wert legt der Messvertrag fest; kein Schutz-
versprechen gegen Differenzangriffe und keine Personenzählung. `key_events: null`
liefert typed `not_configured` und erlaubt weiterhin Traffic. Ein optionaler Vertrag:

```json
{"names":["synthetic_goal"],"meaning":"Bestehendes synthetisches Ziel, kein qualifizierter Lead","valid_from":"2026-01-01","valid_to":"2026-12-31","consent_coverage":"Vom Betreiber dokumentiert; API beweist sie nicht"}
```

Nur existierende freigegebene Ereignisse. Voller Zeitraum muss innerhalb der
Vertragsgültigkeit liegen, sonst not_configured. Keine Events/Tracking-Anlage,
keine Rekonstruktion nie erhobener Historie. Briefing-/Registrierungsmessung
bleibt gemäß #1128 aus; der Reportingdienst kann das nicht beweisen.

**Audit-Betriebsbedingung: metadaten für SSO UND Token.** Alle deklarierten
Parameter stehen in redact_params. Bei abfragen/antworten speichert das Gateway
bewusst deklarierte Inhalte trotz dieser Liste. Diese Einstellung gilt je
Zugangsklasse, nicht je Integration. Hier wird sie nie global geändert. Höhere
verbindliche Tiefe benötigt eigenen LEAD/SEC-Schnitt vor Freigabe.
Containerlog enthält nur feste Route, festen Code, Dauer; dazu bei einem angewendeten
Schreibaufruf die Änderungszeile (nur Konfigurationswerte, Freitext als Länge und Hash).
Auch HTTP-Parserfehler vor der Middleware verwenden eine gepinnte aiohttp-Naht
(SafeAppRunner), die Eingabebytes aus Fehlerbody und Log entfernt.
Parserlimits bleiben begrenzt; SDK-/aiohttp-Upgrades verlangen Canary-Proben. Keine Access-Logs,
Google-Rohfehler, Eingabewerte oder Zeilen. Kindprozesse deaktivieren sämtliche
Python-Logs. Keine Datenbank, Berichtscache oder geplante Abfragen.

## Laufzeitgrenzen

512 KiB JSON vor Ausgabe, 8 KiB URL/Query, Pfad 512 Zeichen. **20 Sekunden**
Gesamtbudget inklusive Queue, Token-Refresh und API. Connector-Timeout **25 s**.
Zwei persistente Python-Kindprozesse insgesamt, vier zusätzliche Queueplätze,
30 Berichts-/Probeanforderungen je Quelle pro rollender Minute, dazu höchstens 20
Schreibversuche je Quelle und Tag (nur im RAM des Serverprozesses). Status kann
mehrere feste Kompatibilitätsaufrufe innerhalb derselben Frist enthalten.
Ein Prozess führt immer nur einen Auftrag aus. Token/Clients pro Quelle und
Prozess getrennt, nur im RAM. Reguläre Aufrufe verwenden gültige Tokens weiter;
Ablauf aktualisiert sie. Timeout oder Client-Abbruch tötet den tatsächlichen
Prozess inklusive Token-/Google-Arbeit, wartet begrenzt und ersetzt ihn erst
beim nächsten Auftrag. Dadurch geht dessen RAM-Cache verloren; maximal zwei
Token-Caches je Quelle. Kein Thread-Await mit weiterlaufender Arbeit.

Keine Retry-Kette: GAPIC retry=None, GSC execute(num_retries=0), Auth401-Replay=0,
Token-jwt_grant(can_retry=False), Auth-HTTP-Adapter max_retries=0.
ScopedCredentials nutzt dafür eine **gepinnt getestete interne google-auth-Naht**;
Upgrade muss Tokenfehler/Refresh/Scopes erneut prüfen. TLS bleibt eingeschaltet,
Redirects werden nicht verfolgt. Keine implizite Entwickler-ADC oder interaktiver
Login. Offizielle Clients werden beim Imagebau einschließlich GSC-Discovery
geliefert; keine Pakete oder Discovery-Dokumente zur Laufzeit.

## Einrichtung für den Admin

Die Schritte für den Admin (Google-Seite mit minimalen Rechten nach SEC V1, Eintrag des
Servicekonto-JSON, Scopes des Nutzers, Prüfung, Schlüsselwechsel) stehen in
[SETUP.md](SETUP.md). Die Datei wird je Plugin-Version als `setup_guide` in den signierten
Plugin-Katalog übernommen und ist dann über den Admin-MCP (`admin_get_plugin_setup_guide`),
REST und die GUI abrufbar (JanuaPort/januaport#1137). Höchstens 32 KiB, gültiges UTF-8,
keine relativen Links; `tests/test_setup_guide.py` prüft das.

## Einrichtung, Betrieb und Rückbau

1. Neue zweckgebundene Servicekonto-Identität für diese Website. Seit #1148 (SEC-Obergrenze V1):
   GSC höchstens „Uneingeschränkt“ (nie Inhaber), GA4 höchstens „Bearbeiter“ auf der **konkreten
   Property** (nie Administrator), keine IAM-Rolle; IAM allein genügt nicht. Kein
   Terminagent-Konto, kein Owner/Admin auf Vorrat. Search Console API, Analytics Data API und
   Analytics Admin API aktivieren. Die Lesewege fragen ausschließlich
   webmasters.readonly beziehungsweise analytics.readonly an, ein Schreibaufruf holt
   `webmasters` beziehungsweise `analytics.edit` über ein eigenes Credential-Objekt. Die
   Rechte und Grants funktionieren erst nach kontrolliertem Live-Test als belegt.
2. Keyless nur mit geprüftem Identity-Vertrag; V1 akzeptiert ausschließlich eine
   explizite Servicekonto-Schlüsseldatei. token_uri muss oauth2.googleapis.com/token
   und universe_domain googleapis.com sein; Signer wird ohne Netz validiert.
   Der Schlüssel liegt **nur im Tresor** und kommt als RAM-Datei auf die Anlage
   (Hausmuster #887): keine Klartextdatei unter `/etc` oder `/opt`, keine
   Schlüssel in Envs, Image, Compose, Git oder Chat. Zwei Stellen in der
   Basis-Einrichtung (das Gateway legt sie an, sobald die Ordner aus Schritt 3
   existieren), mit unterschiedlichen Wegen:

   | Stelle | Ordner auf der Anlage | Inhalt von `runtime-key` |
   |---|---|---|
   | `plugin-google-reporting` | `/run/jnpt/plugins/google-reporting/` | der **eine** Bridge-Key, ein Zufallswert; derselbe Wert steht in der Connector-Credential `google-reporting`. Setzt der Betreiber per CLI (siehe unten) |
   | `plugin-google-reporting-google` | `/run/jnpt/plugins/google-reporting-google/` | das Servicekonto-JSON (bis 1 MiB). Trägt der Admin/PO in der Admin-GUI unter Basis-Einrichtung ein |

   **Der Bridge-Key ist ein Zufallswert, den kein Mensch kennen muss.** Der
   Betreiber erzeugt ihn auf der Anlage und setzt ihn über stdin per Betreiber-CLI
   (`jnpt credential set`, Wert nie als Argument; im Container
   `docker compose exec -T jnpt jnpt credential set --name …`, mit der Compose-Datei
   der Anlage) in die Stelle **und** mit demselben Wert in die Connector-Credential.
   Danach wird der Wert verworfen; er erscheint nie in Chat, Log oder Datei:

   ```bash
   K=$(openssl rand -hex 32)            # 64 Zeichen, erfüllt 32–256 druckbare ASCII
   printf %s "$K" | jnpt credential set --name plugin-google-reporting
   printf %s "$K" | jnpt credential set --name google-reporting
   unset K
   ```

   Nur das Servicekonto-JSON trägt der Admin/PO selbst in die Admin-GUI ein
   (Basis-Einrichtung, Stelle `plugin-google-reporting-google`).
   Das Passwortfeld der GUI schluckt beim Einfügen die Zeilenumbrüche. Das schadet
   nicht: Das JSON bleibt gültig, weil die Zeilenumbrüche im `private_key` als
   Backslash-n-Escape stehen. **Die heruntergeladene JSON-Datei nach dem Eintragen
   löschen**; eine Kopie gehört allenfalls in den Passwortmanager des Betreibers,
   nie auf die Platte der Anlage.
3. Auf der Anlage: `config.json` unter `/opt/jnpt/plugins/google-reporting/`
   (kein Secret). Die zwei Ordner unter `/run/jnpt/plugins/` legt systemd-tmpfiles
   an (`deploy/jnpt-plugins-google-reporting.conf.example`, zwei `d`-Zeilen
   `0750 65532 65532`); das Gateway schreibt `runtime-key` hinein, nach jedem Boot
   neu. Compose/.env und Deskriptor aus deploy/ installieren. Deskriptor REQUIRE
   nennt config.json und beide `runtime-key`; fehlt eine Datei, bucht der Aktuator
   `unprovisioned`. Compose hängt die **Ordner** read-only ein
   (`…/google-reporting` → `/run/secrets/jnpt`, `…/google-reporting-google` →
   `/run/secrets/google`), damit ein atomar ersetzter Schlüssel ankommt.
   **Neustart bei Schlüsselwechsel und nach dem Boot:** der Dienst liest beide
   Dateien nur beim Start. Das Unit-Paar `deploy/jnpt-google-reporting-key.path`
   (zwei `PathChanged=`) und `.service` (`docker compose … up -d
   --force-recreate`) nach `/etc/systemd/system/` legen und **nur die Path-Unit**
   aktivieren: `systemctl daemon-reload && systemctl enable --now
   jnpt-google-reporting-key.path`. Das deckt auch den Boot ab: Startet der
   Container vor dem Schreiben der Dateien, endet er mit `startup_failed` (Exit 1);
   das Schreiben löst den Recreate aus. Die Vorlage `jnpt-plugin-key@` des Produkts
   genügt hier nicht, weil sie nur eine Stelle beobachtet und es für
   `google-reporting-google` keine Compose gibt.
   DATA_DIRS bleibt leer; kein eigener Host-Aktuator. Das Image `ghcr.io/januaport/google-reporting`
   entsteht nur aus dem Tag `google-reporting-vX.Y.Z` (Absatz „Release (#1138)“ weiter unten); Tag und signierter
   Katalogeintrag liegen bei GATE/VENDOR, nicht in diesem Änderungspaket.
4. **Netz vor Aktivierung nachweisen:** bestehendes jnpt_plugins muss internal=true
   und passende IPAM besitzen. Betreiber reserviert eine freie IPv4 in diesem Netz
   als GOOGLE_REPORTING_PLUGIN_IP. Der Listener bindet ausschließlich diese IP,
   niemals 0.0.0.0, Loopback oder Egress-IP. Eigenes Compose-Egress-Netz ohne andere
   Workloads; Host-Firewall/DNS-/Egress-Policy auf tatsächlich benötigte Google-Ziele.
   Compose allein setzt keine Domain-Allowlist durch. Initiale Ziele:
   oauth2.googleapis.com:443, searchconsole.googleapis.com:443 (gebündelter GSC-
   Discoveryvertrag), analyticsdata.googleapis.com:443; www.googleapis.com nur
   aufnehmen, falls Testlauf dessen Notwendigkeit belegt. Kein TLS-/Proxy-Bypass.
   Von Egress-Seite und außerhalb Plugin-Netz darf Port8092 nicht erreichbar sein.
   Ohne diesen Nachweis ist der Host nicht fertig provisioniert.
5. Signed Image-Digest einsetzen. Non-root65532, RootFS read-only, 512MiB/1CPU,
   Capabilities entfernt, minimale tmpfs, kein Host-Port/Docker-Socket/Vault-Mount,
   keine Reverse-Proxy-Route. Lokal /healthz prüft nur Startkonfiguration/Dienst,
   keine Google-Probe; temporärer Googleausfall löst kein Update-Rollback aus.
6. Die **eine** Connector-Credential `google-reporting` im JanuaPort-Vault ist
   schon aus Schritt 2 gesetzt (Wert gleich `runtime-key` der Stelle
   `plugin-google-reporting`, nie von Hand getippt). Bytegleiche Vorlagen aus Integrations-/Produktrepo
   (`google-search-console`, `google-analytics`, beide jeweils 1.0.0 und beide mit
   dieser Credential; für den ATAS-Zugang zusätzlich `google-search-console-manage` und
   `google-analytics-manage`, ebenfalls 1.0.0) als **corp**-Instanzen mit
   http://google-reporting:8092 erstellen. Die Schreibwerkzeuge der Manage-Vorlagen bekommt
   nur der ATAS-Zugang, je Werkzeug einzeln (tool-genaues Recht). Team-Selbstanlage erlaubt nur öffentliche HTTPS-Ziele; Admin-MCP
   benötigt target_address-Schalter für private Ziele, alternativ Betreiberweg.
   Aktivierung erstellt keine Instanzen/Rechte. Nur vorgesehenem INBOUND-Zugang
   freigeben.
7. Auditbedingung prüfen; Status, Negativproben, dann Berichte. „JanuaPort SSO Neu“
   Katalog/Werkzeuge neu einlesen. Bestehender REST-Lifecycle/Reload; erstmalige
   Vorlagenauslieferung kann Gateway-/Katalogneustart brauchen.

Rotation: Beim Bridge-Key einen neuen Zufallswert wie in Schritt 2 erzeugen und im
selben Zug in `plugin-google-reporting` und `google-reporting` setzen; beim
Servicekonto-JSON den neuen Wert in der Admin-GUI eintragen (Stelle
`plugin-google-reporting-google`). Das Gateway schreibt die Datei, das Unit-Paar
erzeugt den Container neu (≤ 60 s plus Recreate). Danach positive Probe je Quelle. Beim Google-Schlüssel den neuen
Schlüssel in der Cloud Console anlegen, eintragen, Probe, dann den alten dort
löschen. Der Tresor reist im Backup mit; die RAM-Dateien schreibt das Gateway nach
jedem Boot neu. Restore mit neuer Prüfung.
**Benannte Grenze: Entfernen ist kein Widerruf.** Wird ein Wert in der GUI
entfernt oder ersetzt, bleibt der Google-Schlüssel gültig, bis er in der Cloud
Console gelöscht ist; ein abgeflossener Bridge-Key bleibt gültig, bis er ersetzt
ist.
Update/Rollback nutzt Standarddeskriptor/HEALTH_DOCKER=1 und lokale Gesundheit.
Stop: verbleibende Connectoren melden Fehler, nie Nullberichte. Vollständiger
Rückbau entfernt Rechte/Instanzen, widerruft Bridge-Key/Google-Grants (Schlüssel in
der Cloud Console löschen), entfernt die beiden Stellen und Ordner. Plugin-Purge erledigt externe Grants
und beliebige Hostdateien nicht automatisch.

## Reproduzierbare Prüfung und offene Live-Gates

Python3.12: venv anlegen, `pip install --require-hashes -r requirements.lock`,
`python -m unittest discover -s tests -v`. Nur synthetische Daten; RSA-Testsigner
entstehen flüchtig im Test. Keine echten Credentials/Googleanfragen.
`python tests/mutations.py` muss kritische Mutanten erkennen.
Produkt: `go test ./internal/connector/...` und
`go test ./cmd/jnpt -run 'TestGoogleReporting1130|TestGoogleManage1148'` prüfen echte HTTP-, MCP-,
Privacy-, Policy- und Auditpfade. Golden-Dateien enthalten keine Bridge-Secrets.
Release (#1138): `.github/workflows/google-reporting-release.yml` läuft nur auf den Tag
`google-reporting-vX.Y.Z` (setzt GATE). Ablauf wie agent-runner/script-runner: Hash-Lock,
Unit-Tests und Mutationsproben, ein Build `linux/amd64` mit SBOM und Provenance per Digest nach
GHCR, Sonden gegen genau diesen Digest (Config-Digest = gepusht, Benutzer 65532, Tests im
gehärteten Container ohne Netz, Start ohne Konfiguration endet mit `startup_failed`), erst danach
der Image-Tag. Trivy-Bericht als Artefakt, kein Gate; den Nachweis je Digest trägt GATE in den
Abschnitt „Kuratierung“ von `CHANGELOG.md` ein. Lokal vorab: `actionlint`, `docker build`.
Imagebau: `docker build .`; hinter verwaltetem Proxy optional BuildKit
`--secret id=jnpt_ca,src=<CA-Bundle>`; Secret wird nicht ins Image kopiert.

P3 bleibt offen: kontrollierte negative Live-Grant/Scope-/Schlüsselprobe,
GA4-Kompatibilität inklusive hostname/landingPage/Eventkombinationen,
Zeitzonen-/Consent-/Messvertrag und Seitenfreigabe, Quellenvergleich mit identischem
Bereich, Netz-/Egressnachweis am Zielhost, SEC-Freitext/Kleingruppen/Auditfreigabe,
GATE-Release. Live-DoD: je Quelle echter Bericht über **ChatGPT → JanuaPort SSO Neu
→ Connector → Beisteller → Google**, danach 28/90 Tage soweit vorhanden.
Keine produktive Freigabe aus synthetischen Tests ableiten.

Live-Gates der Verwaltungshandgriffe (#1148): zuerst Staging, danach mit eigenem PO-Go eine
echte Annotation, eine Sitemap-Einreichung und die Kanalgruppe gegen die Produktiv-Property.
Dabei zu belegen: die Feldnamen der Gruppierungsregeln (`eachScopeSource`,
`eachScopeDefaultChannelGroup`; die Google-Referenz nennt keine Werteliste, die Namen stammen
aus Drittdokumentation), die Obergrenze eigener Gruppen der Property und dass die Standardkanäle
in der Gruppe so zählen wie in der Standardgruppe.

Quellen: [GSC Query](https://developers.google.com/webmaster-tools/v1/searchanalytics/query),
[GA4 runReport](https://developers.google.com/analytics/devguides/reporting/data/v1/rest/v1beta/properties/runReport),
[GA4 Metadaten](https://developers.google.com/analytics/devguides/reporting/data/v1/rest/v1beta/ResponseMetaData).

Optionaler repoübergreifender Wire-Test: in diesem Verzeichnis
`python tests/wire_server.py --port 18092` starten. In der Produkt-Worktree:
`TEST_GOOGLE_REPORTING_URL=http://127.0.0.1:18092 go test ./internal/connector/contracttest -run 'ActualPythonService' -count=1` (deckt die Lese- und die #1148-Schreibvorlagen ab).
Der Test trifft die echte HTTP-/Worker-Servicegrenze mit synthetischem Adapter,
keine Google-Identität. Ohne gesetzte URL überspringt die normale Go-CI diesen
optionalen Python-Nachbartest; ihre eigenen HTTP-Goldens bleiben verpflichtend.
Die Plugin-CI installiert den Hash-Lock, prüft Unit-/Mutationssuite, baut das Image
und führt die Tests nochmals als Non-root im Read-only-Container ohne externes Netz aus.
`tests/test_deploy_files.py` prüft Compose, Deskriptor, tmpfiles-Beispiel und Unit-Paar
gegeneinander; im Image-Schritt fehlen diese Dateien, dort überspringt sich der Test,
im Unit-Schritt läuft er.
