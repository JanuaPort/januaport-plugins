# LinkedIn-Seite 0.1.0 — #1237, Stufe A

Privater Python-REST-Beisteller für Kennzahlen einer fest konfigurierten LinkedIn-Seite.
PO-Entscheid vom 29.10.2026: Option (a), Stufe A. Synthetisch geprüft;
Herstellervertrag und produktive Freigabe sind **noch nicht live belegt**. Apache 2.0.

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
`X-Restli-Protocol-Version: 2.0.0`. Fünf feste Hersteller-Endpunkte, einschließlich
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
  Gatewayrechte allein begrenzen einen entwendeten Token nicht.
- **Token-Lebensdauer/Erneuerung:** Access höchstens 60 Tage. Optionales Refresh nur, wenn
  LinkedIn es beim Code-Tausch liefert; höchstens 365 Tage ab Erstzustimmung. Fünf Minuten
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
Port 8765 auf `127.0.0.1` und `::1`, ausschließlich Loopback. Gemeinsam genau eine Anfrage:
Die erste Anfrage beendet beide Listener, auch bei falschem State oder Pfad. Nach Abweisung
neu starten; Warte-/Gesamtfrist fünf Minuten. Ohne verfügbares IPv6 arbeitet es mit IPv4
weiter und meldet dies auf stderr. Das gilt ausschließlich bei fehlender IPv6-Unterstützung
oder nicht verfügbarer Loopback-Adresse (EAFNOSUPPORT/EADDRNOTAVAIL/EPROTONOSUPPORT).
Ist `[::1]:8765` belegt (EADDRINUSE), bricht es vor Browserstart mit klarer Meldung ab.
Andere Bindefehler brechen ebenfalls ab; unter Windows werden Sockets exklusiv gebunden.

1. Admin führt [SETUP.md](SETUP.md) aus. Er gibt die Organisations-ID und die verifizierte
   API-Version an den Betreiber. `config.example.json` enthält ausschließlich synthetische
   Werte. Die Konfiguration nach `/opt/jnpt/plugins/linkedin-seite/config.json` legen.
2. Die zwei Zeilen aus `deploy/jnpt-plugins-linkedin-seite.conf.example` in
   `/etc/tmpfiles.d/jnpt-plugins.conf` übernehmen; `systemd-tmpfiles --create` ausführen.
   Beide Ordner gehören 65532:65532, Modus 0750. Das Gateway stellt daraus die zwei
   Tresor-Stellen bereit und schreibt deren `runtime-key` nach jedem Boot neu.
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
   `/run/jnpt/plugins/linkedin-seite-oauth` nach `/run/secrets/oauth`, beide read-only.
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

Stufe B braucht später einen eigenen PO-Auftrag, SEC-Prüfung, getrennte Schreibrechte,
feste Post-Routen, Vorschau/Freigabe, Inhaltsprüfung, Schreibquote, Idempotenz und Audit.
Sie ist hier nicht implementiert.
