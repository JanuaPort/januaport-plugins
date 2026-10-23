# januaport-plugins

[English](README.md) · **Deutsch**

Beisteller und Desktop-Läufer für JanuaPort: eigenständige Komponenten neben dem Gateway oder auf einem Gerät.

JanuaPort ist ein self-hosted MCP-Gateway. Es verbindet KI-Assistenten fein berechtigt mit den
bestehenden Systemen eines Unternehmens und protokolliert die Zugriffe in einem append-only Audit-Log. Der
Kern von JanuaPort ist proprietäre Software der JanuaPort GmbH und nicht Teil dieses Repositorys. Dieses
Repository ist einer der offenen Ränder darum herum.

- **Lizenz:** Apache License 2.0 ([`LICENSE`](LICENSE), [`NOTICE`](NOTICE))
- **Links:** [januaport.ai](https://januaport.ai) · [Sicherheitsrichtlinie](SECURITY.md) · [Beiträge](CONTRIBUTING.md)

Keine Kundendaten, keine Schlüssel, keine Betreiberwerte in diesem Repository.

---

## Warum Beisteller

Das JanuaPort-Gateway ist ein einziges statisches Binary in einem schlanken Container. Was eine andere
Laufzeit, einen eigenen Zeitplan oder eigene Zugangsdaten braucht, läuft als eigener Beisteller daneben
oder als Desktop-Läufer auf einem Gerät. Die Komponenten erreichen JanuaPort über dokumentierte
Schnittstellen: ein gemeinsames Verzeichnis, den MCP-Endpunkt mit eigenem Token oder die Geräte-API.
Sie lassen sich abschalten, ohne das Gateway anzufassen.

## Enthaltene Beisteller

Kennzeichnung: **Gebaut** · **Im Bau** · **Geplant**. Jeder Eintrag sagt, was genau belegt ist.

| Ordner | Was es tut | Stand |
|---|---|---|
| [`ebics-abholer/`](ebics-abholer/) | Holt täglich den Kontoauszug (camt.053) per EBICS bei der Bank ab und legt ihn als Datei in einem gemeinsamen Verzeichnis ab, aus dem JanuaPort ihn liest. Zahlungen einreichen kann er nicht. | **Gebaut.** Bei der JanuaPort GmbH produktiv im Einsatz für das eigene Firmenkonto (EBICS H005, täglicher Timer-Betrieb seit 07.09.2026). Bisher gegen eine Bank belegt. Image `ghcr.io/januaport/ebics` (amd64, arm64): **im Bau** — Release-Workflow mit Scan gebaut, noch keine Fassung veröffentlicht. |
| [`kartei-rpa/`](kartei-rpa/) | Ein PowerShell-Modul und vier Aktions-Skripte, mit denen ein RPA-Werkzeug (Power Automate Desktop) eine Arbeitsliste aus der JanuaPort-Kartei abarbeitet: freigegebene Sätze holen, einzeln sperren, im Fachsystem buchen, das Ergebnis zurückschreiben. Feldnamen per Einstellungsdatei, Voreinstellung passend zum Rezept „Zahlungseingang“ in `januaport-recipes`. | **Gebaut.** Mit gemocktem Transport getestet (Pester, Windows PowerShell 5.1). Diese Fassung ist noch nicht gegen eine echte Anlage gelaufen. |
| [`box/`](box/) | Provisionierung der JanuaPort Box, eines lüfterlosen Mini-PCs, auf dem das Gateway beim Kunden läuft: Compose-Stack, Host-Härtung, Boot-Ordnung, Caddy-Flächen, Profil für den OpenAI-Tunnel, optionale Sicherung nach SharePoint oder SMB und ein Runbook. Kein eingehender Port aus dem Internet; Fernwartung nur nach Opt-in des Kunden. | **Gebaut.** Läuft seit August 2026 auf einer Pilot-Anlage. Hier liegt eine bereinigte, verallgemeinerte Fassung (neutrale Namen, Installationsort `/opt/jnpt/box`), die in genau dieser Form noch nicht auf einem Gerät gelaufen ist. |
| [`tunnel/`](tunnel/) | Kuratierungs-Nachweis für den Katalog-Eintrag `tunnel`: den Client des OpenAI Secure MCP Tunnel, ein Fremd-Image, das JanuaPort im signierten Plugin-Katalog anbietet. Kein Code; das Changelog hält je kuratiertem Digest den Scan und die geprüfte JanuaPort-Version fest (englisch). | **Gebaut.** Erster Eintrag `v0.0.14`, geprüft mit JanuaPort 0.66 am 27. September 2026. |
| [`script-runner/`](script-runner/) | Führt gepinnte Python-Skripte aus dem Git-Repository des Betreibers aus, wenn eine KI sie über JanuaPort aufruft. Ein Skript darf selbst JanuaPort-Werkzeuge nutzen, mit den Rechten des Aufrufers und nur die deklarierten, und optional Python-Bibliotheken (numpy, pandas, …) als hash-geprüfte Wheels aus dem eigenen Repository des Betreibers nutzen, nur lesend eingehängt (kein pip, kein Download). Drei Dienste aus einem Image: ein vermittler neben dem Gateway, eine Sandbox ohne Netz (ein Lauf je Instanz, gVisor wo vorhanden) und ein abholer per ssh mit Nur-Lese-Deploy-Key. | **Im Bau.** Mit Unit-Tests und Docker-Sonden gegen einen Stub-Gateway und einen lokalen Git-Server geprüft, unter runc und unter gVisor (runsc). Die Gateway-Seite (Pins, Lauf-Token, Audit), Auslieferung und Katalogeintrag fehlen noch; noch nicht auf einer Anlage gelaufen. |
| [`google-reporting/`](google-reporting/) | Feste GSC-/GA4-Berichte und drei Schreibhandgriffe (#1148: Annotation, Sitemap, Kanalgruppe) über privaten Python-REST-Dienst, je Scope ein eigenes Credential, öffentliche Seitenliste, begrenzte Prozessworker. | **Gebaut, Live-Abnahme offen.** Synthetische SDK-/HTTP-/Abbruch-/Privacy-/Audit-Verträge geprüft. Query-Freitext standardmäßig gesperrt (Betreiberschalter). Fassung 1.0.0 mit Release-Workflow vorbereitet (Image `ghcr.io/januaport/google-reporting`), noch nicht getaggt; kein signierter Katalogeintrag. |
| [`agent-runner/`](agent-runner/) | Führt die Modell-/Werkzeugschleife eines Agenten über Modell- und MCP-Durchgang des Kerns aus. Anthropic Messages, lokale Schritt-/Aufruf-/Zeitgrenzen, inhaltfreies Lauf-Protokoll; internes Netz, kein Modell-Schlüssel. | **Gebaut, Live-Durchstich ausstehend.** B1 gegen einen lokalen Fake-Kern, Vertrag und Negativfälle geprüft; keine Live-Anbieteraufrufe. |
| [`desktop-runner/`](desktop-runner/) | Desktop-Läufer für `geraet:<id>`-Pins: Gerät holt alles ab (Weg B), Kopplung und signiertes Manifest, Zeitplantakt auf dem Gerät; werkzeuglos innerhalb der Gnadenfrist offline, spätes Melden aus DPAPI-Journal. | **Implementiert, Live-Abnahme offen.** #1167 D4: Windows/amd64-Client, native COM-/DPAPI-/Job-Object-Sonden, Linux-Race-Tests und Fake-Kern; Release-Strecke mit optionaler Azure-Signatur. Echte Kernintegration und Release-Signierung stehen aus. |

## Releases

Images, die dieses Repository selbst baut, entstehen nur aus einem Versions-Tag, nie aus einem
Branch. Für `script-runner` heißt der Tag `script-runner-vX.Y.Z`: Der Workflow fährt die Unit-Tests,
baut ein Image für `linux/amd64` mit SBOM und Provenance, pusht es per Digest nach
`ghcr.io/januaport/script-runner`, fährt die Docker-Sonden gegen genau diesen Digest und setzt erst
danach den Image-Tag `X.Y.Z`. Ein Schwachstellen-Bericht (Trivy) hängt am Lauf; er ist kein Gate.
Anlagen beziehen das Image nur per Digest aus dem signierten Plugin-Katalog von JanuaPort. Was je
Digest geprüft wurde, steht vor dem Katalogeintrag im `CHANGELOG.md` des Beistellers. Veröffentlicht
ist noch kein Release. `google-reporting` geht denselben Weg mit dem Tag
`google-reporting-vX.Y.Z` und dem Image `ghcr.io/januaport/google-reporting` (Sonden: Config-Digest
gleich gepusht, Benutzer 65532, Unit-Tests im gehärteten Container ohne Netz, Start ohne
Konfiguration endet fail-closed).

## Google Reporting (#1130)

[google-reporting](google-reporting/README.md): privater Python-REST-Beisteller für feste GSC-/GA4-Berichte und seit #1148 drei begrenzte Schreibhandgriffe (Annotation, Sitemap, Kanalgruppe, kein Löschen), offizielle Google-Clients, begrenzte und abbrechbare Arbeit. Stufe 1, gebaut/synthetisch geprüft; Live-Abnahme offen. Fassung 1.0.0 mit Release-Workflow (#1138) und den Korrekturen der GA4-Statusprüfung und des zu späten `end_date` (#1150) vorbereitet. Keine neuen signierten Katalogeinträge oder Tags in diesem Paket.
