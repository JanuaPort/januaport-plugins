# januaport-plugins

[English](README.md) · **Deutsch**

Beisteller und Desktop-Läufer für JanuaPort: eigenständige Komponenten neben dem Gateway oder auf einem Gerät.

JanuaPort ist ein self-hosted MCP-Gateway. Es verbindet KI-Assistenten fein berechtigt mit den
bestehenden Systemen eines Unternehmens und protokolliert die Zugriffe in einem append-only Audit-Log. Der
Kern von JanuaPort ist proprietäre Software der JanuaPort GmbH und nicht Teil dieses Repositorys. Dieses
Repository ist einer der offenen Ränder darum herum.

- **Lizenz:** Apache License 2.0 ([`LICENSE`](LICENSE), [`NOTICE`](NOTICE))
- **Links:** [januaport.ai](https://januaport.ai) · [Sicherheitsrichtlinie](SECURITY.md) · [Beiträge](CONTRIBUTING.md)
- **Sprache:** Die Anleitungen in den Unterordnern sind derzeit auf Deutsch.

Keine Kundendaten, keine Schlüssel, keine Betreiberwerte in diesem Repository.

---

## Warum Beisteller

Das JanuaPort-Gateway ist ein einziges statisches Binary in einem schlanken Container. Was eine andere
Laufzeit, einen eigenen Zeitplan oder eigene Zugangsdaten braucht, läuft als eigener Beisteller daneben
oder als Desktop-Läufer auf einem Gerät. Die Komponenten erreichen JanuaPort über dokumentierte
Schnittstellen: ein gemeinsames Verzeichnis, den MCP-Endpunkt mit eigenem Token oder die Geräte-API.
Sie lassen sich abschalten, ohne das Gateway anzufassen.

## Enthaltene Beisteller

Stand: lokale Git-Tags und das jeweilige Modul-CHANGELOG. **Veröffentlicht** bezeichnet die
getaggte Fassung; ein Einsatzbeleg wird gesondert genannt und gilt nur für die belegte Fassung.
Bei `tunnel` ist die im CHANGELOG kuratierte Upstream-Fassung maßgeblich. **Gebaut** ohne
Versionsangabe bezeichnet ungetaggte Komponenten. Jeder Eintrag sagt, was genau belegt ist.

| Ordner | Was es tut | Stand |
|---|---|---|
| [`ebics-abholer/`](ebics-abholer/) | Holt täglich den Kontoauszug (camt.053) per EBICS bei der Bank ab und legt ihn als Datei in einem gemeinsamen Verzeichnis ab, aus dem JanuaPort ihn liest. Zahlungen einreichen kann er nicht. | **Veröffentlicht, im Einsatz belegt (v1.0.1).** Tags `ebics-abholer/v1.0.0` und `ebics-abholer/v1.0.1`; Image `ghcr.io/januaport/ebics` (amd64, arm64). Laut [CHANGELOG](ebics-abholer/CHANGELOG.md) lief genau der 1.0.1-Digest am 19.11.2026 auf der Pilot-Anlage mit Exit 0. Produktiver EBICS-H005-Timer-Betrieb seit 07.09.2026; bisher gegen eine Bank belegt. |
| [`kartei-rpa/`](kartei-rpa/) | Ein PowerShell-Modul und vier Aktions-Skripte, mit denen ein RPA-Werkzeug (Power Automate Desktop) eine Arbeitsliste aus der JanuaPort-Kartei abarbeitet: freigegebene Sätze holen, einzeln sperren, im Fachsystem buchen, das Ergebnis zurückschreiben. Feldnamen per Einstellungsdatei, Voreinstellung passend zum Rezept „Zahlungseingang“ in `januaport-recipes`. | **Gebaut.** Mit gemocktem Transport getestet (Pester, Windows PowerShell 5.1). Diese Fassung ist noch nicht gegen eine echte Anlage gelaufen. |
| [`box/`](box/) | Provisionierung der JanuaPort Box, eines lüfterlosen Mini-PCs, auf dem das Gateway beim Kunden läuft: Compose-Stack, Host-Härtung, Boot-Ordnung, Caddy-Flächen, Profil für den OpenAI-Tunnel, optionale Sicherung nach SharePoint oder SMB und ein Runbook. Kein eingehender Port aus dem Internet; Fernwartung nur nach Opt-in des Kunden. | **Gebaut.** Läuft seit August 2026 auf einer Pilot-Anlage. Hier liegt eine bereinigte, verallgemeinerte Fassung (neutrale Namen, Installationsort `/opt/jnpt/box`), die in genau dieser Form noch nicht auf einem Gerät gelaufen ist. |
| [`tunnel/`](tunnel/) | Kuratierungs-Nachweis für den Katalog-Eintrag `tunnel`: den Client des OpenAI Secure MCP Tunnel, ein Fremd-Image, das JanuaPort im signierten Plugin-Katalog anbietet. Kein Code; das Changelog hält je kuratiertem Digest den Scan und die geprüfte JanuaPort-Version fest. | **Veröffentlicht, nicht im Einsatz belegt (v0.0.15).** Laut [CHANGELOG](tunnel/CHANGELOG.md) am 28.09.2026 für JanuaPort 0.67 kuratiert und katalogisiert; Laufbeleg dieses Digests offen. Upstream-Fassung, kein lokaler Tunnel-Tag. |
| [`script-runner/`](script-runner/) | Führt gepinnte Python-Skripte aus dem Git-Repository des Betreibers oder freigegebenen Speicherpaketen aus, wenn eine KI sie über JanuaPort aufruft. Ein Skript darf selbst JanuaPort-Werkzeuge nutzen, mit den Rechten des Aufrufers und nur die deklarierten, und optional Python-Bibliotheken (numpy, pandas, …) als hash-geprüfte Wheels aus dem eigenen Repository des Betreibers nutzen, nur lesend eingehängt (kein pip, kein Paketindex-Download). Bis zu vier Dienste aus einem Image: ein vermittler neben dem Gateway, eine Sandbox ohne Netz (ein Lauf je Instanz, gVisor wo vorhanden), ein abholer per ssh mit Nur-Lese-Deploy-Key und, hinter dem Compose-Profil `apps`, ein App-Host, der live geschaltete Browser-Apps aus freigegebenen Speicherpaketen ausliefert. | **Veröffentlicht, im Einsatz belegt (v0.4.2).** Tag `script-runner-v0.4.2`; laut [CHANGELOG](script-runner/CHANGELOG.md) seit 05.11.2026 auf der Showcase mit JanuaPort 0.94.2: `script_echo` erfolgreich, Apphost mit `monitor` und `steuer`, App-Anmeldung und Aufruf im Audit belegt. Unit-Tests und Docker-Sonden unter runc und gVisor (runsc). |
| [`google-reporting/`](google-reporting/) | Feste GSC-/GA4-Berichte und drei Schreibhandgriffe (#1148: Annotation, Sitemap, Kanalgruppe) über privaten Python-REST-Dienst, je Scope ein eigenes Credential, öffentliche Seitenliste, begrenzte Prozessworker. | **Veröffentlicht, nicht im Einsatz belegt (v1.1.0).** Tag `google-reporting-v1.1.0`; Image `ghcr.io/januaport/google-reporting`. Synthetische SDK-/HTTP-/Abbruch-/Privacy-/Audit-Verträge geprüft. Mehrere Sites je Dienst (#1379), Site nur aus dem Instanz-Header, Rechte je Instanz; Query-Freitext standardmäßig gesperrt (Betreiberschalter). Das [CHANGELOG](google-reporting/CHANGELOG.md) belegt den Showcase-Betrieb von 1.0.1 seit 28.10.2026, keinen Lauf von 1.1.0. |
| [`linkedin-seite/`](linkedin-seite/) | Stufe A: feste GET-Berichte für eine konfigurierte LinkedIn-Seite, Summen und bis zu zehn Beiträge, OAuth im Tresor, begrenzte Worker und 50 HTTP-Versuche pro UTC-Tag. Stufe B1: Posten nach menschlicher Hash-Freigabe im Kern. Stufe B3 (0.4.0): Kommentare unter eigenen Posts lesen und als Organisation innerhalb fester Live-Prüfungen beantworten, dazu eine tägliche Linkliste. | **Veröffentlicht, nicht im Einsatz belegt (v0.4.1).** Tag `linkedin-seite-v0.4.1`; synthetische HTTP- und Negativtests. Laut [CHANGELOG](linkedin-seite/CHANGELOG.md) sind die erweiterten Kommentarformen und die Diagnose nicht live belegt; Herstellervertrag und Live-Abnahme offen. |
| [`bing-webmaster/`](bing-webmaster/) | Sechs feste Routen für Bing Webmaster und IndexNow über einen privaten Python-REST-Dienst: Indexstand einer Seite, Sitemap-Liste und Einreichungsquote (lesend); Adress-Batch, Sitemap und IndexNow melden (schreibend, `dry_run`, 20 Versuche je Route und UTC-Tag). Der Bing-Schlüssel liegt nur im Beisteller und in der ausgehenden Anfrage an `ssl.bing.com`; nur konfigurierte Seite und Host-Allowlist. | **Veröffentlicht, nicht im Einsatz belegt (v0.1.1).** Tag `bing-webmaster-v0.1.1`; Image `ghcr.io/januaport/bing-webmaster`. Synthetische HTTP-, Key-Canary- und Negativtests gegen einen Loopback-Stub. Das [CHANGELOG](bing-webmaster/CHANGELOG.md) nennt Live-Befunde aus 0.1.0, keinen Lauf von 0.1.1; Herstellervertrag (Anführungszeichen um `url`, Quotenfelder) und Live-Abnahme offen. Keine Seitenstatistik, kein Crawl; AI Performance hat keine API. |
| [`agent-runner/`](agent-runner/) | Führt die Modell-/Werkzeugschleife eines Agenten über Modell- und MCP-Durchgang des Kerns aus. Anthropic Messages und OpenAI Chat Completions, lokale Schritt-/Aufruf-/Zeitgrenzen, inhaltfreies Lauf-Protokoll; internes Netz, kein Modell-Schlüssel. | **Veröffentlicht, nicht im Einsatz belegt (v0.2.2).** Tag `agent-runner-v0.2.2`; Vertrags- und Negativtests gegen einen lokalen Fake-Kern. Das [CHANGELOG](agent-runner/CHANGELOG.md) belegt Live-Anbieteraufrufe mit 0.2.1 auf der Showcase, keinen Lauf von 0.2.2. |
| [`desktop-runner/`](desktop-runner/) | Desktop-Läufer für `geraet:<id>`-Pins: Gerät holt alles ab (Weg B), Kopplung und signiertes Manifest, Zeitplantakt auf dem Gerät; Windows-Vertrag 5 (4 plus zugewiesene PC-Apps; Server: 2), werkzeuglos innerhalb der Pinfrist mit persistenter Uhrmarke offline, spätes Melden aus DPAPI-Journal. | **Veröffentlicht, nicht im Einsatz belegt (v0.5.4).** Tag `desktop-runner-v0.5.4`; signierte Windows-Releases, native COM-/DPAPI-/Job-Object-Sonden, Linux-Race-Tests und Fake-Kern. Frühere Fassungen: manueller [Installationstest](desktop-runner/README.md#installationstest) ohne PO-Gerät (#1233) und VM-Gegenprobe für 0.5.1. Laut [CHANGELOG](desktop-runner/CHANGELOG.md) fehlt für die 0.5.4-Tastenkürzel der Beleg am Gerät; echte Kernintegration separat belegen. |

## Releases

Images, die dieses Repository selbst baut, entstehen nur aus einem Versions-Tag, nie aus einem
Branch. Für `script-runner` heißt der Tag `script-runner-vX.Y.Z`: Der Workflow fährt die Unit-Tests,
baut ein Image für `linux/amd64` mit SBOM und Provenance, pusht es per Digest nach
`ghcr.io/januaport/script-runner`, fährt die Docker-Sonden gegen genau diesen Digest und setzt erst
danach den Image-Tag `X.Y.Z`. Ein Schwachstellen-Bericht (Trivy) hängt am Lauf; er ist kein Gate.
Anlagen beziehen das Image nur per Digest aus dem signierten Plugin-Katalog von JanuaPort. Was je
Digest geprüft wurde, steht vor dem Katalogeintrag im `CHANGELOG.md` des Beistellers.
Aktuell getaggt ist `script-runner-v0.4.2`, mit Showcase-Beleg im CHANGELOG.
`google-reporting` geht denselben Weg mit dem Tag
`google-reporting-vX.Y.Z` und dem Image `ghcr.io/januaport/google-reporting` (Sonden: Config-Digest
gleich gepusht, Benutzer 65532, Unit-Tests im gehärteten Container ohne Netz, Start ohne
Konfiguration endet fail-closed). Aktuell getaggt: `google-reporting-v1.1.0`; Einsatzbeleg im
CHANGELOG für die frühere Fassung 1.0.1, für 1.1.0 offen.

`linkedin-seite` nutzt dasselbe Release-Muster: `linkedin-seite-vX.Y.Z`, Image
`ghcr.io/januaport/linkedin-seite`; aktuell `linkedin-seite-v0.4.1`, Stufen A, B1 und B3.
Einsatzbeleg für 0.4.1 und Katalogisierung sind im CHANGELOG nicht dokumentiert.

`bing-webmaster` nutzt dasselbe Release-Muster: `bing-webmaster-vX.Y.Z`, Image
`ghcr.io/januaport/bing-webmaster`; aktuell `bing-webmaster-v0.1.1`.
Einsatzbeleg für 0.1.1 und Kuratierungsdaten sind im CHANGELOG nicht dokumentiert.

Weitere Tags: `ebics-abholer/v1.0.1` (zuvor `ebics-abholer/v1.0.0`), `agent-runner-v0.2.2`
und `desktop-runner-v0.5.4`. `tunnel` ist ein Fremd-Image: Das CHANGELOG führt die kuratierte
Upstream-Fassung `v0.0.15`, keinen lokalen Release-Tag. Die Stand-Tabelle trennt Release und
Einsatzbeleg. Lokaler Wächter ohne Netz: `sh scripts/check-readme-tags.sh`; keine CI-Anbindung.

## Google Reporting (#1130)

[google-reporting](google-reporting/README.md): privater Python-REST-Beisteller für feste GSC-/GA4-Berichte und seit #1148 drei begrenzte Schreibhandgriffe (Annotation, Sitemap, Kanalgruppe, kein Löschen), offizielle Google-Clients, begrenzte und abbrechbare Prozessworker. Veröffentlicht als `google-reporting-v1.1.0`, mit mehreren Sites je Dienst (#1379). Die Release-Strecke (#1138) sowie die Korrekturen der GA4-Statusprüfung und des zu späten `end_date` (#1150) sind enthalten. Das CHANGELOG belegt Berichte auf der Showcase mit 1.0.1; Einsatz und Live-Abnahme von 1.1.0 sind nicht dokumentiert.
