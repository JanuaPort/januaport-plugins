# LinkedIn-Seite 0.3.0 — #1237, Stufe A und Stufe B1

Privater Python-REST-Beisteller für Kennzahlen einer fest konfigurierten LinkedIn-Seite.
PO-Entscheid vom 29.10.2026: Option (a), Stufe A. Seit 0.2.0 zusätzlich Stufe B1: Posten,
Bearbeiten, Löschen und eigener Kommentar. Seit 0.3.0 erfolgt die Freigabe im Kern (#1306 F4),
gebunden an den vollständigen Wirkungs-Hash v2 (Abschnitt unten). Synthetisch geprüft; Herstellervertrag und produktive Freigabe sind **noch nicht
live belegt**. Apache 2.0.

## Werkzeuge und Messvertrag

Alle Fachrouten brauchen `Authorization: Bearer <Bridge-Key>`.
Werkzeugrechte und Audit bleiben im Gateway; dies ist kein eigener MCP-Server.

| GET-Route | Parameter | Ergebnis |
|---|---|---|
| `/v1/linkedin/page_statistics` | `period_days` 1–90, Vorgabe 7; optional `end_date` YYYY-MM-DD | Summen Impressionen, Klicks, Seitenaufrufe |
| `/v1/linkedin/followers` | dieselben Zeitraumparameter | aktueller Follower-Gesamtwert und Summe der Follower-Zugänge im Zeitraum |
| `/v1/linkedin/posts` | `limit` 1–10, Vorgabe 10 | letzte veröffentlichte Beiträge nach Erstellungsdatum; Datum, ID, Textanfang bis 120 Zeichen und je Summen Impressionen/Klicks/Reaktionen/Kommentare/Shares |

Zeitraum: UTC, standardmäßig sieben abgeschlossene Tage. `end_date` ist inklusiv:
für Seitenstatistik höchstens gestern, für Follower höchstens **heute minus zwei Tage**.
Die Vorgabe endet jeweils am letzten verfügbaren Tag. Ein später verlangtes Ende ergibt
`end_date_not_available`; es wird nicht geklemmt. API-Ende exklusiv; alle Tagesintervalle
müssen lückenlos und genau einmal vorliegen. Fehlende Pflichtzahlen bleiben ein Formfehler.
Die Beiträge tragen Lebenszeitsummen, keinen impliziten Siebentagefilter.
Impressionen/Klicks stammen aus den Share-Summen; Seitenaufrufe aus der Seitenstatistik.
Follower-Zuwachs bedeutet **Zugänge (organisch + bezahlt), nicht Nettoänderung nach Abgängen**;
der aktuelle Gesamtwert kommt separat aus `networkSizes` / `firstDegreeSize` mit
`edgeType=COMPANY_FOLLOWED_BY_MEMBER` und URL-kodierter Konfigurations-URN im Pfad.
Die Herstellersemantik und der Gesamtwert bleiben Live-Gates. Reaktionen verwenden ausschließlich
das gepinnte Summenfeld `likeCount`; dessen Bedeutung ist live zu bestätigen, keine Ersatzmetrik.
Wenn diese Felder die verlangten Kennzahlen nicht darstellen, bleibt der Vertrag gesperrt
und wird nach Herstellerbeleg korrigiert.

`source_contract.py` enthält sämtliche Hersteller-Endpunkt-, Parameter- und Feldnamen:
**„aus Herstellerdoku, live zu belegen“**. Unbekannte Form, fehlendes Pflichtfeld,
fremde Organisation/Beitrags-ID, unvollständige Statistikseite oder falscher Typ ergibt
`unexpected_source_shape`. Zusätzliche Datenfelder werden vor der Summenprojektion über
eine Positivliste verworfen. Es gibt keine Accountsuche, freien Pfade, personenbezogenen
Aufschlüsselungen oder Einzelabfragen von Reaktionen/Kommentaren. Identitätsfelder und
Member-URNs werden nicht ausgegeben. Bei Member-/Person-URN oder Mention irgendwo im
gesamten Beitragsobjekt (auch künftige verschachtelte Felder) bleibt der Beitrag im Bericht,
aber ohne Textanfang: `text: null` und
`text_ausgelassen: "erwaehnung"`. Unmarkierter Seitenfreitext ist keine allgemeine
Anonymisierung. Nur eine gültige leere Share-Statistikantwort auf genau die angefragte
Beitrags-ID ergibt fünf Zähler mit 0 und `ohne_interaktion: true`. Ein `paging`-Objekt mit
fehlenden oder leeren `links` ist gültig; `rel=next` oder unbekannte Paging-Formen sind Fehler.
Keine fremde Organisation/ID; eine zweite Zeile ist ein
Formfehler (502), ebenso fehlende Pflichtfelder. Bei leerer Liste stammt die Zuordnung
aus dem festen Einzel-GET mit konfigurierter Organisation; widersprechende Metadaten sind Fehler.
Nur bei `likeCount` ist ein negativer Wert dokumentiert: erst alle Tageswerte summieren,
dann eine negative Summe auf 0 setzen und `korrigiert: true` am Beitrag markieren.
Andere negative Zähler, einschließlich Follower und Seitenaufrufe, ergeben
`unexpected_source_shape` (502). Die Beiträge liefern weiterhin Lebenszeitsummen;
die gemeinsame Projektion wahrt auch für Tageswerte die Reihenfolge Summe vor Kappung.
Andere unbekannte Formen bleiben Fehler; fehlende Felder werden nicht durch 0 ersetzt.

Unbekannte/doppelte Parameter, GET-Body und fremde Methoden werden vor dem Worker abgewiesen.
Die Organisation kommt ausschließlich aus `config.json`, als numerische ID oder
`urn:li:organization:<ID>`. `linkedin_version` ist YYYYMM; Vorgabe ist der aktuelle UTC-Monat
beim Laden (bei Erstellung 202610). Betreiber pinnt eine live unterstützte Version.
REST: `https://api.linkedin.com/rest/`, Header `LinkedIn-Version` und
`X-Restli-Protocol-Version: 2.0.0`; Query in Rest.li-2.0-Form wie die Doku 202610
(`timeIntervals=(…)` und `shares=List(…)` mit roher Struktur, URNs kodiert). Fünf feste Hersteller-Endpunkte, einschließlich
`networkSizes`. Development Tier: einzelne GETs, kein BATCH_GET.

Fehler tragen ausschließlich feste Codes in `{"error": code}` (400 Eingabe, 401/403 Bridge,
404 Route, 405 Methode, 429 lokale Grenzen, 502 Quelle/Form, 503 Token/Betrieb, 504 Budget).
Tokenwarnungen ergänzen jede Anwendungsantwort, auch Fehler und `/healthz`, um
`"warnung":"token_laeuft_ab","ablaufdatum":"YYYY-MM-DD"`; nur das Datum wird veröffentlicht.
HTTP-Parserfehler vor der Middleware behalten das feste Fehler-JSON ohne Tokenmetadaten.
`/healthz` prüft lokal den Dienst, macht keinen LinkedIn-Aufruf und bleibt bei Tokenablauf gesund.

## SEC-Prüfpunkte (A2)

- **Scopes:** genau `rw_organization_admin r_organization_social`, als Konstante und im
  Test gepinnt. LinkedIn bietet für die erforderliche Administration keinen Read-only-Scope;
  deshalb ausschließlich feste GET-Fachrouten. Der einzige ausgehende POST dient dem
  festen OAuth-Tokenendpunkt. Der Grant hat mehr Möglichkeiten als dieser Dienst;
  Gatewayrechte allein begrenzen einen entwendeten Token nicht. Die Tokenantwort meldet
  `scope` kommagetrennt (beobachtet 02.11.2026); Trenner sind Komma und/oder Leerraum
  (`oauth.scope_set`), die Menge muss weiter **exakt** die zwei Scopes sein, nichts Zusätzliches,
  nichts Fehlendes, kein anderer Trenner, Nicht-Strings fail-closed.
- **Token-Lebensdauer/Erneuerung:** Access höchstens 60 Tage. Optionales Refresh nur, wenn
  LinkedIn es beim Code-Tausch liefert; höchstens 365 Tage ab Erstzustimmung. LinkedIn
  überschreitet die Höchstwerte gelegentlich knapp (`refresh_token_expires_in` 31536059 =
  59 s zu viel): `oauth.lifetime` deckelt die Frist auf genau 60 bzw. 365 Tage, nie darüber;
  Toleranz höchstens ein Tag (`OVERSHOOT_TOLERANCE_SECONDS`), darüber, nicht ganzzahlig oder
  nicht positiv ergibt `unexpected_source_shape`. Gilt auch für die Erneuerung. Fünf Minuten
  vor Access-Ablauf erneuert ein nächster Abruf im Worker. Neue Tokens bleiben im RAM;
  die ursprüngliche Refresh-Frist wird niemals verlängert. Kein Tresor-Rückschreiben.
  Ohne Refresh ab 14 Tagen vor Access-Ablauf die Warnung, danach `token_abgelaufen`.
  Mit Refresh gilt die Warnschwelle für Refresh-Ablauf (verkürzte Frist wird übernommen).
  Ein gültiger Access-Token funktioniert bis zu seinem eigenen Ablauf weiter.
  Worker-Kill/Neustart verliert RAM-Tokens; neu gestartete Worker benutzen den Tresorstand.
  Rotiert der Hersteller Refresh-Tokens mit Widerruf des alten, ist erneute Zustimmung nötig.
- **Ablage:** Hausmuster #887: `plugin-linkedin-seite` für den Bridge-Key und
  `plugin-linkedin-seite-oauth` für OAuth-JSON mit `client_id`, `client_secret`,
  `access_token`, `access_expires_at`, optional gemeinsam `refresh_token` und
  `refresh_expires_at`. Ablaufwerte als ISO-8601-Zeitstempel mit Zeitzone.
  Secrets ausschließlich Tresor → `/run` → read-only Ordner-Mounts; kein Klartext unter
  `/etc` oder `/opt`, keine Secrets in Envs, Repo, Logs oder Fehlerantworten.
- **Tagesdeckel:** 50 ausgehende HTTP-Versuche pro UTC-Tag im Serverprozess. Jeder
  GET und jeder Refresh zählt **vor** dem Aufruf; Fehlschläge und später abgebrochene
  Versuche zählen mit. Der 51. wird mit `daily_cap_reached` abgewiesen. Ein Bericht
  kann mehrere Versuche kosten (Seite/Follower 2, Beiträge 1 + Anzahl, ggf. Refresh).
  Kein Retry, Redirect oder Batch. Ein Serverneustart setzt den RAM-Zähler auf null.
- **Datenminimierung:** nur Summen und höchstens zehn veröffentlichte Seitenbeiträge.
  Land/Funktion/Seniorität/Branche und beliebige künftige Aufschlüsselungen werden
  vor der Projektion verworfen; keine personenbezogenen Listen-Endpunkte.

## Betreiberschritte

Die Ausgabe auf stdout nie in eine Datei umleiten, nur direkt per Pipe an `jnpt credential set`.
Die Redirect-URI bleibt `http://localhost:8765/linkedin/callback`. Das PO-Skript bindet
Port 8765 auf `127.0.0.1` und `::1`, ausschließlich Loopback. Das Client-Secret wird ohne
Echo oder per `--secret-stdin` gelesen; enthält es Steuerzeichen (unter `cmd` fügt Strg+V
eines ein), bricht das Skript mit fester deutscher Meldung ab und sendet nichts. Unter
Windows PowerShell 7 mit `Read-Host -MaskInput` nutzen (SETUP.md). Gemeinsam genau eine Anfrage:
Die erste Anfrage beendet beide Listener, auch bei falschem State oder Pfad. Nach Abweisung
neu starten; Warte-/Gesamtfrist fünf Minuten. Ohne verfügbares IPv6 arbeitet es mit IPv4
weiter und meldet dies auf stderr. Das gilt ausschließlich bei fehlender IPv6-Unterstützung
oder nicht verfügbarer Loopback-Adresse (EAFNOSUPPORT/EADDRNOTAVAIL/EPROTONOSUPPORT).
Ist `[::1]:8765` belegt (EADDRINUSE), bricht es vor Browserstart mit klarer Meldung ab.
Andere Bindefehler brechen ebenfalls ab; unter Windows werden Sockets exklusiv gebunden.

1. Admin führt [SETUP.md](SETUP.md) aus. Er gibt die Organisations-ID und die verifizierte
   API-Version an den Betreiber. `config.example.json` enthält ausschließlich synthetische
   Werte. Die Konfiguration nach `/opt/jnpt/plugins/linkedin-seite/config.json` legen.
2. Die Zeilen aus `deploy/jnpt-plugins-linkedin-seite.conf.example` in
   `/etc/tmpfiles.d/jnpt-plugins.conf` übernehmen; `systemd-tmpfiles --create` ausführen.
   Alle Ordner gehören 65532:65532, Modus 0750. Das Gateway stellt daraus die Tresor-Stellen
   bereit (seit 0.2.0 vier, die zwei der Stufe B1 sind optional) und schreibt deren
   `runtime-key` nach jedem Boot neu.
3. Einen zufälligen Bridge-Key per stdin in beide Stellen setzen, danach verwerfen:

   ```bash
   K=$(openssl rand -hex 32)
   printf %s "$K" | jnpt credential set --name plugin-linkedin-seite
   printf %s "$K" | jnpt credential set --name linkedin-seite
   unset K
   ```

   Die zweite Stelle ist die Connector-Credential; derselbe Wert wird verwendet.
   OAuth-JSON kommt per Pipe vom PO-Rechner in `plugin-linkedin-seite-oauth` (SETUP).
4. Compose, `.env` aus `deploy/env.example` und Updater-Deskriptor installieren. Nur
   den später signierten Image-Digest verwenden. REQUIRE enthält Konfiguration und beide
   Laufzeitschlüssel; fehlende Dateien bedeuten `unprovisioned`. Compose hängt die Ordner
   `/run/jnpt/plugins/linkedin-seite` nach `/run/secrets/jnpt` und
   `/run/jnpt/plugins/linkedin-seite-oauth` nach `/run/secrets/oauth`, seit 0.2.0 zusätzlich
   `…-post-oauth` nach `/run/secrets/post-oauth` und `…-gateway` nach `/run/secrets/gateway`,
   alle read-only. REQUIRE verlangt nur die zwei Stufe-A-Schlüssel.
5. `jnpt-linkedin-seite-key.path` und `.service` nach `/etc/systemd/system/` legen;
   `systemctl daemon-reload`, dann `systemctl enable --now jnpt-linkedin-seite-key.path`.
   Nur die Path-Unit aktivieren. Zwei `PathChanged` beobachten atomare Schlüsselwechsel;
   `up -d --force-recreate` liest beide Werte neu. Das deckt auch Boot ab. Die generische
   Produktvorlage `jnpt-plugin-key@` kennt nur eine Stelle und genügt hier nicht.
6. Eine freie IP im bestehenden **internal** Netz `jnpt_plugins` reservieren und als
   `LINKEDIN_SEITE_PLUGIN_IP` setzen. Listener ausschließlich dort, Port 8092, kein Hostport.
   Separates Egress-Netz; Host-Firewall/DNS-Policy für `api.linkedin.com:443` und
   `www.linkedin.com:443` nachweisen. Compose erzwingt keine Domain-Allowlist.
   Non-root 65532, read-only RootFS, keine Capabilities, no-new-privileges, 512 MiB,
   1 CPU, 32 PIDs, kleine tmpfs. Kein Docker-Socket oder Tresor-Mount im Dienst.
7. Connector-Vorlage/Katalog und Nutzerrechte im Produkt separat bereitstellen, Ziel
   `http://linkedin-seite:8092`, Timeout 25 Sekunden. Audit für SSO **und** Token auf
   Metadaten; Parameter redigieren. Keine produktive Aktivierung aus diesen Tests ableiten.

Rotation: Bridge-Key gleichzeitig in Stelle und Connector-Credential ersetzen. OAuth-
Zustimmung erneut ausführen und JSON ersetzen; Path-Unit erzeugt Container neu (Gateway-
Auslieferung bis 60 Sekunden plus Recreate). Ohne Refresh spätestens alle 60 Tage,
mit Refresh jährlich vor dessen ursprünglichem Ablauf. Nach Boot/Restore Zugriff prüfen.
Entfernen aus dem Tresor ist kein Widerruf: LinkedIn-Grants beim Anbieter widerrufen.
Rückbau: Connectorrechte/Instanzen entfernen, Grants widerrufen, beide Stellen/Ordner und
Units entfernen. Kein eigener Datenspeicher, keine geplanten Abfragen.

## Grenzen, Prüfung und Release

512 KiB Quell-/Antwortdaten, 8 KiB Anfrage/Parserheader, Pfad 512 Zeichen.
Zwei persistente spawn-Worker, vier Queueplätze, 30 Berichte pro rollender Minute.
20 Sekunden einschließlich Queue/Token/API; Timeout und Client-Abbruch töten den wirklichen
Prozess und warten auf Reap. Kein weiterlaufender Arbeitsthread. Höchstens zwei RAM-
Tokenstände. `safe_http.py` ist die gepinnte aiohttp-Naht aus google-reporting; Upgrades
brauchen erneut Parser-Canarys. Log enthält nur feste Route, festen Code und Dauer.

Python 3.12: `pip-compile --generate-hashes --output-file=requirements.lock requirements.in`,
`python -m pip install --require-hashes -r requirements.lock`,
`python -m unittest discover -s tests -v`, `python tests/mutations.py`.
Alle Provideraufrufe in Tests gehen an lokale Fake-Server mit synthetischen Daten.
Docker: `docker build -t linkedin-seite:local .`; CI wiederholt Tests non-root, read-only,
ohne externes Netz. Tests für Deployment/Setup/Workflows überspringen sich nur im reinen
Image-Test ohne diese Dateien; das PO-Skript wird nicht ins Dienstimage kopiert.

Release spiegelt google-reporting: Tag `linkedin-seite-vX.Y.Z`, Aktionen mit denselben
SHA-Pins, ein amd64-Build mit SBOM/Provenance, Sonden am Digest samt Config-Digest-
Vergleich, erst dann Image-Tag. Kein neuer Aktionstyp. Kuratierung in CHANGELOG.
Signierter Katalog, Connector-Auslieferung und Live-Abnahme liegen außerhalb dieses Pakets.

Offene Live-Gates: Follower-Gesamtsummenform, Zugangssemantik, Reaktionssemantik,
`sortBy=CREATED`, Seiten-/Share-Zeiträume und Tagesvollständigkeit, Development-Tier-
Freigaben, Scope-Negativprobe, PKCE-Unterstützung, Refresh-Verfügbarkeit/-Rotation und
Netz-/Auditprüfung am Zielhost. Vollständige Namensliste und Negativnachweis: `PRUEFBERICHT.md`.

## Stufe B1 — Posten nach Kern-Freigabe (0.3.0)

PO-Zuruf 02.11.2026 („Ihr sollt Beiträge erstellen können“, „voll einsatzbereit“), SEC-PASS
c-6575359082 (Bedingungen 1–5) und c-6575837332 (Umfrage, Video V1–V3).
Freigabevertrag seit #1306: Plan v2/v2.1, SEC c-6602359446 A1/A3 und c-6604738594 H1–H3. Synthetisch geprüft,
**nicht live belegt**. Der Beisteller postet, bearbeitet, löscht und kommentiert auf der eigenen
Unternehmensseite — **ausschließlich** für Einträge des Redaktionsplans, die ein Mensch im
Kern mit passendem Wirkungs-Hash freigegeben hat. Formate: Text (mit Link), ein
Bild, mehrere Bilder (2–20), PDF-Dokument, Umfrage, Video. Stufe A bleibt in Verhalten und
Scopes unverändert; ohne Schreib-Konfiguration antworten die Schreibrouten
`503 schreiben_nicht_eingerichtet`.

### Werkzeuge

Eigener Route-Präfix `/v1/linkedin-post/`, derselbe Bridge-Key wie Stufe A. Die schreibenden
Werkzeuge nehmen **nur die Kartei-ID** (`{"eintrag_id": "<16 Hex>"}` als JSON-Rumpf, sonst nichts),
keinen Text, keine Datei, keine URN. Text, Umfrage und Medien liest der Beisteller selbst.

| Werkzeug (Vorschlag Katalog) | Route | Wirkung |
|---|---|---|
| `linkedin_seite_vorschau` | `GET vorschau?eintrag_id=…&aktion=posten\|bearbeiten\|loeschen\|kommentar` | lesend: Text, Format, Umfrage, Ziel-URN, Medienliste mit Ablage-Dateiname, Größe und sha256 je Datei, `hash`; direkte Wirkungsfelder `aktion`, `ziel_urn`, `text`, `medien[{name, sha256, datei, art, bytes}]`, `umfrage`. Kein LinkedIn-Aufruf. |
| `linkedin_seite_create_post` | `POST create_post` | Post anlegen (`posten`) |
| `linkedin_seite_edit_post` | `POST edit_post` | Text eines eigenen Posts ändern (`bearbeiten`, nur `commentary`) |
| `linkedin_seite_delete_post` | `POST delete_post` | Post löschen (`loeschen`) |
| `linkedin_seite_create_comment` | `POST create_comment` | eigener Kommentar als Organisation unter `post_urn` (`kommentar`) |

Alle vier POST-Routen verlangen zusätzlich genau einen Header:
`X-Jnpt-Freigabe: hash=<freigabe:v2:sha256:…>` (ohne Auftrags-ID, keine zweite Form).
Fehlend, doppelt, falsche Form oder keine Gleichheit mit dem selbst berechneten Hash:
`409 nicht_freigegeben`, vor jedem LinkedIn-Schreibaufruf.

Bestätigter Erfolg: `ausgefuehrt: true`, `aktion`, `eintrag_id`, `hash`, `format`,
`state` (`veroeffentlicht`, `bearbeitet`, `geloescht`, `kommentiert`), `post_urn`,
bei Video `video_urn`, bei Kommentar `kommentar_id`, `bytes`.
Nachweislicher Abbruch vor dem ersten LinkedIn-Schreibversuch: `nicht_ausgefuehrt: true`,
etwa bei 409, Validierung oder Medienprüfung. Fehler nach einem möglichen Schreibversuch
tragen keines der beiden Felder: der Kern behandelt sie als unklar. Das beginnt bereits
bei initializeUpload und gilt auch für Bearbeiten und Löschen. Ein 2xx mit unbrauchbarer
Antwort ist unklar. Keine automatische Wiederholung durch den Kern bei unklarem Ergebnis.

HTTP wartet auf das Endergebnis (harte Grenze 25 Minuten), ohne Zwischenantwort.
F2 hat derzeit 25 Sekunden Call-Frist und eine Runtime-Decke von 120 Sekunden.
Lange Videojobs können im Kern früher unklar werden; eine Spec-Anhebung auf 25 Minuten
reicht wegen der Runtime-Decke nicht aus. Weitere feste Fehlercodes: `schreib_tageslimit`,
`schreib_http_tageslimit`, `schreiben_belegt` (429), `bereits_veroeffentlicht` (409),
Eingabe-/Medien-/Umfragefehler (422/413), Gateway-/Quell-/Upload-/Videofehler (502), Zeitbudget (504).
Stufe-A-Fehler behalten ihren bisherigen Vertrag.

### Kartei-Schema (VENDOR setzt es auf der Anlage)

Der Kern kennt nur `string|integer|number|boolean` (`internal/kartei/load.go`). Listen und
Objekte stehen deshalb als **JSON-String** im Feld; der Beisteller parst sie strikt (keine
doppelten Schlüssel, keine unbekannten Schlüssel). **Keine Datenschutz-Etiketten** an diesen Feldern
(SEC Bed. 5): ein Kürzel `‹…›` im Text wird mit `pseudonym_im_feld` abgewiesen. Ein Satz ist im
Kern höchstens 4 KiB groß.

`redaktionsplan` (bestehende Kartei, fünf Felder kommen dazu, alle optional, `type: string`):

| Feld | Inhalt |
|---|---|
| `post_text` | Text des Posts (Pflicht für `posten`/`bearbeiten`). Wird im Little-Text-Format gesendet: reservierte Zeichen `\|{}@[]()<>#\*_~` werden escaped, eine Erwähnung ist damit unmöglich; `#Wort` bleibt ein Hashtag. |
| `medien` | JSON-Liste von Dateinamen der **einen** Medien-Ablage, Reihenfolge zählt, z. B. `["karussell.pdf"]`. Reine Dateinamen (`A–Z a–z 0–9 . _ -`, höchstens 120 Zeichen), kein Pfad, kein `..`. Endungen: `.png .jpg .jpeg .gif` (Bild), `.pdf` (Dokument, Titel = Dateiname), `.mp4` (Video); der Dateiinhalt muss zur Endung passen. Kombinationen: kein Medium, 1 Bild, 2–20 Bilder, 1 PDF oder 1 Video. |
| `umfrage` | JSON-Objekt `{"frage": "…", "antworten": ["…", "…"], "dauer": 7}`: Frage 1–140 Zeichen, 2–4 verschiedene Antworten je 1–30 Zeichen, `dauer` ∈ {1, 3, 7, 14} Tage. Nichts wird gekürzt; nie zusammen mit `medien`. Eine Umfrage lässt sich bei LinkedIn nachträglich nicht ändern. |
| `post_urn` | `urn:li:share:<n>` oder `urn:li:ugcPost:<n>`: Ziel für `bearbeiten`, `loeschen`, `kommentar`. Für `posten` muss es leer sein. INB-REDAKTION trägt die URN aus der Werkzeug-Antwort ein. |
| `kommentar_text` | Text des eigenen Kommentars (Pflicht für `kommentar`). Ohne Escaping gesendet (die Comments-API dokumentiert Little-Text nicht); enthält er eine URN (`urn:li:`), wird er mit `kommentar_erwaehnung` abgewiesen. |

### Freigabe und Hash v2 (#1306, A3/H3)

Freigeben, Starten und dauerhafte Sperrvermerke liegen im Kern. Der Beisteller liest
keine Freigabesätze; alte Herkunftslabels sind keine Berechtigungsquelle.
Die Vorschau und ihre direkten Wirkungsfelder entstehen aus genau einem gelesenen
Eintrag und denselben geprüften Medienkopien. Die Antwort enthält genau `eintrag_id`,
`aktion`, `format`, `ziel_urn`, `text`, `medien`, `umfrage`, `post_text`, `post_urn`,
`kommentar_text`, `dokument_titel`, `hash`. Jedes Medium enthält genau `name`, `sha256`,
`datei`, `art`, `bytes`; `name` ist der sichtbare Name/Dokumenttitel. Kein zusätzliches
verschachteltes Wirkungsobjekt. Referenz ist die unveränderte F2-Datei
`tests/data/linkedin_vorschau_beispiel.json`. F2 erlaubt bei nahendem Tokenablauf zusätzlich
`warnung`, `ablaufdatum`, `warnung_schreiben`, `ablaufdatum_schreiben`.
Der Kern liest die Vorschau serverseitig und berechnet seinen Hash daraus selbst neu. Für die Ausführung wird der aktuelle
Stand neu geladen und sein Hash mit dem Header des Kerns verglichen.

`hash` = `freigabe:v2:sha256:` + SHA-256 über die UTF-8-Bytes `freigabe:v2`, ein LF,
danach kompaktes kanonisches JSON in Feldfolge `aktion, medien, text, umfrage, ziel_urn`.
Medien geordnet als `[{name, sha256}]`, ohne Sortieren; `name` ist der sichtbare Name,
bei PDF der Dokumenttitel (= Dateiname). Umfrage `null` oder `{antworten, dauer, frage}`;
Antwortreihenfolge zählt, Dauer als Ganzzahl in Tagen. Fehlende/null Listen werden `[]`,
fehlende/null Umfrage wird `null`; ein vorhandenes leeres Umfrageobjekt bleibt verschieden.
UTF-8 ohne Normalisierung, kein HTML- oder JavaScript-Escaping, JSON-Kontrollzeichen,
keine Leerzeichen. Verbindliche unveränderte Kern-Vektoren: `tests/data/hash_golden.json`.
Kein v1-Fallback.

| Aktion | text | ziel_urn | medien / umfrage |
|---|---|---|---|
| posten | exakter post_text | Organisation aus config.json | geprüfte Dateien / Eintragsumfrage |
| bearbeiten | exakter post_text | post_urn | [] / null |
| loeschen | leerer String | post_urn | [] / null |
| kommentar | exakter kommentar_text | post_urn | [] / null |

Little-Text-Escaping ist die verlustfreie Transportdarstellung des Texts. Autor/owner/actor
sind immer die konfigurierte Organisation; übrige Post-Felder feste Konstanten.
Ablauf: Eintrag einmal lesen → Dateien einmal in private RAM-Kopien → Prüfung/Wirkung/Hash →
Header-Gleichheit → report_hash → Zählen/LinkedIn → Kopien überschreiben und verwerfen.

### Rückverbindung zum Gateway (W2, SEC Bed. 3)

Der Beisteller hängt im Netz **`jnpt_plugins`** (`internal: true`). Dort hängt auch der Gateway-
Dienst `jnpt` und lauscht auf Port **8484** (Kern `docker/docker-compose.prod.yml`, Netzblock
`plugins`). Die Gateway-URL steht in `config.json` unter `schreiben.gateway_url` und lautet
**`http://jnpt:8484/mcp`**. Erlaubt ist ausschließlich der Pfad `/mcp`: `/admin/mcp`, Sichten
(`/mcp/v/…`), Query, Fragment, Zugangsdaten in der URL oder ein fehlender Port brechen den Start mit
`startup_failed` ab. Protokoll wie `kartei-rpa`: JSON-RPC über Streamable HTTP (`initialize`,
`notifications/initialized`, `tools/call`), Bearer = eigener Gateway-Token des Beistellers, keine
Umleitung, kein Proxy.

**Token-Umfang des Gateway-Tokens (genau diese zwei Werkzeuge, nichts sonst):**

- `redaktionsplan_get` (Eintrag per ID),
- `redaktion_medien_get_file` (Ablage-Integration `redaktion-medien`, Bytes als eingebettete Ressource).

Die Namen folgen der Kern-Bildung `<name>_<werkzeug>` (Bindestrich → Unterstrich) aus
`schreiben.redaktionsplan` und `schreiben.medien_ablage`. Nicht dabei: kein
`_add`, `_update`, `_delete`, kein `redaktionsplan_search`, kein
`list_files`/`read_file`/`write_file`/`move_file`, kein integrationsweiter Scope, kein Werkzeug auf
`/admin/mcp`. Der Client verweigert jeden anderen Namen schon vor dem HTTP-Aufruf. Token-Art:
**Team-Token** des Teams, in dem die Sätze des Redaktionsplans liegen (Row-Level: der Token sieht nur
Sätze seines Teams, außer eine Kartei steht auf Ebene `unternehmen`); kein persönlicher Token mit
Erben-Bit. Die Medien-Ablage darf **nicht** pseudonymisiert sein (sonst verweigert `get_file`), ihr
`max_file_bytes` höchstens 32 MiB.

### Credentials, Bremse und Spur

- **Schreib-Grant (SEC Bed. 4):** eigene Stelle `plugin-linkedin-seite-post-oauth`, Zustimmung mit
  `tools/zustimmung.py --schreiben`, angefordert wird **genau** `w_organization_social
  w_organization_social_feed`, nichts sonst. Das JSON trägt zusätzlich
  `"scope": "w_organization_social w_organization_social_feed"`: die unveränderte Stufe-A-Prüfung weist
  JSON mit `scope` ab, die Schreib-Prüfung verlangt genau diese Menge — ein Tausch fällt beim Laden auf.
  Lese- und Schreib-Token verlassen nie ihren Prozess; jede Erneuerung des Schreib-Tokens muss wieder
  genau diese zwei Scopes liefern (Trenner Komma oder Leerraum, Reihenfolge egal, Menge exakt).
  Posten, Bearbeiten, Löschen sowie Bild-, Dokument- und Video-Upload stehen unter
  `w_organization_social` (Posts-, Images-, Documents-, Videos-API 202610). Kommentieren als
  Organisation steht unter `w_organization_social_feed`, belegt in der Comments API 202610, Tabelle
  „Permissions“: „w_organization_social_feed | Post, comment, and react on posts on behalf of an organization“ (https://learn.microsoft.com/en-us/linkedin/marketing/community-management/shares/comments-api?view=li-lms-2026-10).
  Die App trägt beide Scopes (VENDOR, Portal vom PO geprüft).
- **Gateway-Token:** eigene Stelle `plugin-linkedin-seite-gateway` (Hausmuster #887), nie Env, nie
  Datei im Klartext, nie im Log.
- **Bremse (SEC Bed. 5):** 5 schreibende Aktionen je UTC-Tag über alle vier Aktionen zusammen, gezählt
  im Serverprozess unmittelbar vor dem ersten LinkedIn-Aufruf der Aktion (Video ab
  `initializeUpload`, auch wenn es danach abbricht). Zusätzlich 45 LinkedIn-HTTP-Versuche je UTC-Tag
  für Schreibaufträge (Development Tier: 100 je Mitglied; Stufe A hat eigene 50). Höchstens ein
  Schreibauftrag gleichzeitig, harte Grenze 25 Minuten; Vorschau 20 Sekunden, 10 je Minute.
- **Idempotenz:** `eintrag_id` + `hash` → bestätigtes Ergebnis, nur im RAM. Wiederholung
  liefert dieselbe URN mit `wiederholt: true` und `ausgefuehrt: true`, ohne zweiten Post. `report_hash` ist ein Komfort gegen doppelte
  erfolgreiche Aufrufe und kein Sperrvermerk. Der Kern hält den dauerhaften Ausführungs-
  und Unklar-Sperrvermerk. Ein Neustart leert Zähler und Cache. `post_urn` pflegt die KI;
  es ist Komfort. Ohne IPC-Permit des Serverprozesses erfolgt kein LinkedIn-Aufruf.
- **Spur:** je beendetem Schreibauftrag genau eine Zeile `spur aktion= eintrag_id= hash= code= urn=
  bytes=`; nie Text, nie Dateiname, nie Upload-Adresse (sie ist vorsigniert), nie Token.

### Uploads (SEC V1–V3)

Upload-Adressen kommen aus der LinkedIn-Antwort und gehen nur an `https://www.linkedin.com/dms-uploads/…`
(exakter Host, kein Port, keine Zugangsdaten, kein Fragment, keine Umleitung). Bild und Dokument:
`PUT` mit Bearer (Images-/Documents-/Vector-Asset-Doku); Video: `PUT` je Teil **ohne** Token
(Videos-Doku: „The upload call shouldn't include an OAuth token“). Video-Teile müssen die Datei
lückenlos und ohne Überlappung abdecken, höchstens `ceil(Größe / 4 MiB)` Teile; sonst kein
`finalizeUpload`. Danach Status alle 30 Sekunden, höchstens 15 Minuten; gepostet wird nur bei
`AVAILABLE`. Größengrenze `schreiben.max_medium_bytes`, Vorgabe und Höchstwert 32 MiB (= harte
Kappe der Ablage); je Eintrag höchstens 64 MiB zusammen. Größere Clips gehen auf diesem Weg nicht.

### Endpunkte (geschlossen, `source_contract.WRITE_ENDPOINTS`)

`POST /rest/posts` (inkl. `content.media`, `content.multiImage`, `content.poll`) · `POST
/rest/posts/{urn}` mit `X-RestLi-Method: PARTIAL_UPDATE` · `DELETE /rest/posts/{urn}` mit
`X-RestLi-Method: DELETE` · `POST /rest/socialActions/{urn}/comments` · `POST
/rest/images?action=initializeUpload` · `POST /rest/documents?action=initializeUpload` · `POST
/rest/videos?action=initializeUpload` · `POST /rest/videos?action=finalizeUpload` · `GET
/rest/videos/{urn}` · `PUT` an die geprüfte Upload-Adresse. Nichts sonst: kein Kommentar-GET, keine
Reaktionen, kein Video-DELETE, keine Untertitel, kein Vorschaubild. Autor/`owner`/`actor` ist immer
die Organisation aus `config.json`. Belege (Microsoft Learn, `?view=li-lms-2026-10`):
https://learn.microsoft.com/en-us/linkedin/marketing/community-management/shares/posts-api ·
https://learn.microsoft.com/en-us/linkedin/marketing/community-management/shares/images-api ·
https://learn.microsoft.com/en-us/linkedin/marketing/community-management/shares/documents-api ·
https://learn.microsoft.com/en-us/linkedin/marketing/community-management/shares/videos-api ·
https://learn.microsoft.com/en-us/linkedin/marketing/community-management/shares/multiimage-post-api ·
https://learn.microsoft.com/en-us/linkedin/marketing/community-management/shares/poll-post-api ·
https://learn.microsoft.com/en-us/linkedin/marketing/community-management/shares/comments-api ·
https://learn.microsoft.com/en-us/linkedin/marketing/community-management/shares/little-text-format

### Betreiberhandgriffe Stufe B1

1. tmpfiles: die zwei neuen Zeilen aus `deploy/jnpt-plugins-linkedin-seite.conf.example`
   (`linkedin-seite-post-oauth`, `linkedin-seite-gateway`) übernehmen und `systemd-tmpfiles --create`
   ausführen — **vor** dem Image-Wechsel auf 0.3.0, sonst startet Compose nicht (Ordner-Mounts mit
   `create_host_path: false`).
2. Path-Unit aus `deploy/` neu ablegen (vier `PathChanged`), `systemctl daemon-reload`.
3. `config.json` um den Block `schreiben` ergänzen (`config.example.json`).
4. Redaktionsplan erweitern (Schema oben), Medien-Ablage `redaktion-medien` als
   Ablage-Integration ohne Pseudonymisierung.
5. Gateway-Token erzeugen (Team-Token, genau die zwei Werkzeuge oben) und per stdin setzen:
   `… | jnpt credential set --name plugin-linkedin-seite-gateway`. Alte Freigabe-Suchrechte aus dem Beisteller-Token entfernen;
   den alten Konfigurationsschlüssel `schreiben.freigabe` entfernen.
6. Schreib-Zustimmung des PO (SETUP.md) in `plugin-linkedin-seite-post-oauth`.
7. Connector-Vorlage um die fünf Werkzeuge ergänzen, F2-Call-Frist derzeit 25 Sekunden (Runtime-Decke 120 Sekunden),
   unabhängig von der Jobgrenze des Beistellers von 25 Minuten; lange Jobs können unklar werden. Audit für SSO und Token auf Metadaten; die Vorschau-Antwort enthält den Post-Text.

### Live-Gates Stufe B1

Gateway-Netzweg Beisteller → `jnpt:8484` im Netz `jnpt_plugins` (Aufruf aus dem Container) ·
`team_token_list`: Beisteller-Token mit genau den zwei Lesewerkzeugen · Row-Level-Sicht
auf den Redaktionsplan · Antwortform von `redaktionsplan_get`/`get_file` am echten Kern ·
Hash/Header/Ergebnisfelder gemeinsam mit F2/F3 prüfen (GATE, außerhalb F4) · Upload-Host `www.linkedin.com` und Token-Pflicht je Upload-Art · ETag-Form der
Video-Teile · `x-restli-id` von Post und Kommentar · Little-Text bei Kommentaren · Testvideo
hochladen und abschließen ohne Post · Größe der echten Clips (≤ 32 MiB) und ihr Weg in die Ablage
(`write_file` trägt nur rund 6 MiB) · PID-/Speichergrenzen des Containers mit laufendem Video und
mit 20 großen Bildern (Kopien bis 64 MiB plus gepufferte Base64-Antwort bis rund 44 MiB).

Nicht in B1: B3 (fremde Kommentare lesen und beantworten), Lesen von Umfrage-Ergebnissen, Posten
als Person, Untertitel, Vorschaubild, Video-Löschen, Seitenpflege (Banner, Slogan).
