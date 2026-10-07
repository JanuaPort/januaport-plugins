# Changelog — EBICS-Abholer, Katalog-Eintrag `ebics`

Das Image `ghcr.io/januaport/ebics` baut der Workflow
`.github/workflows/ebics-abholer.yml` aus diesem Ordner, ausgelöst durch einen Tag
`ebics-abholer/vX.Y.Z`. Jeder Eintrag hier hält eine Fassung fest: welcher Digest,
was gescannt wurde, mit welcher JanuaPort-Fassung sie geprüft ist und an welchem Tag.

Ein Eintrag sagt nicht, dass das Image frei von bekannten Schwachstellen ist. Er sagt,
dass genau dieser Digest gebaut, gescannt und mit der genannten JanuaPort-Fassung
gesehen wurde.

**K-Scan (Regel, verbindlich):** je Plattform
`trivy image --platform <p> --severity CRITICAL,HIGH --ignore-unfixed --exit-code 1`.
Ein Befund mit Fix wird durch Neubau behoben (Basis oder Abhängigkeit anheben), nicht
weggeschrieben. Ausnahmen nur in `.trivyignore`, je Zeile mit CVE, Grund und `exp:`
höchstens 90 Tage. CRITICAL- und HIGH-Befunde ohne Fix blocken nicht, stehen aber hier.

## 1.0.0 — noch nicht veröffentlicht

| Feld | Wert |
|---|---|
| Image | `ghcr.io/januaport/ebics` |
| Version | `1.0.0` (Tag `ebics-abholer/v1.0.0`, setzt der Gate-Halter nach dem Merge) |
| Digest (OCI-Index) | `sha256:<trägt der Gate-Halter aus der Zusammenfassung des Release-Laufs ein>` |
| Plattformen | `linux/amd64`, `linux/arm64` |
| Art | Job (einmaliger Lauf per systemd-Timer), kein Dienst |
| Basis | `php:8.5.11-cli-alpine3.24` (per Digest gepinnt), Composer 2.10.3 nur in der Bau-Stufe |
| Bibliothek | `ebics-api/ebics-client-php` 3.2.0, `setasign/fpdf` 1.8.2 (aus der `composer.lock`) |
| Geprüft mit | JanuaPort 0.71.6 |
| Datum | 7. Oktober 2026 (Bau und Scan vor dem Release) |

**Was sich gegenüber dem bisherigen Selbstbau ändert:** Das Image kommt per Digest aus
der Registry statt aus `docker compose build` auf der Anlage. Es ist mehrstufig gebaut
(Composer und Compiler bleiben in den Bau-Stufen) und auf Alpine umgestellt: entpackt
144 MB (amd64) bzw. 127 MB (arm64) statt 601 MB. Abruflogik, Schlüsselbehandlung,
Nutzer (65532), Mounts (`/ablage`, `/geheim`) und Befehle sind unverändert; die
Teststrecke `tests/run.php` läuft im Image in beiden Architekturen grün (95 Fälle,
ohne Netz). Der Compose-Projektname heißt jetzt `jnpt-plugin-ebics`.

**Scan vor dem Release** (lokal gebautes Image, nicht der Release-Digest): Trivy
0.75.0, Schwachstellen-Datenbank v2 vom 7. Oktober 2026, Scanner `vuln`, Alpine 3.24.2.
Beide Plattformen gleich: **0 kritisch · 0 hoch** · 2 mittel · 0 niedrig; die
Composer-Pakete ohne Befund. K-Scan nach Regel: **grün** für `linux/amd64` und
`linux/arm64`. CRITICAL/HIGH ohne Fix: keine. Ausnahmen in `.trivyignore`: keine.
Die beiden mittleren Befunde haben einen Fix in Alpine und verschwinden mit dem
nächsten Anheben der Basis:

- `nghttp2-libs` 1.69.0-r0: CVE-2026-58055 (behoben in 1.70.0-r0)
- `zlib` 1.3.2-r0: CVE-2026-85091 (behoben in 1.3.2-r1)

**Bindender Scan:** Der Release-Lauf scannt den gepushten Digest je Plattform noch
einmal und setzt den Versions-Tag erst danach. Sein Ergebnis und sein Datum ersetzen
beim Eintragen des Digests die Zeilen oben.

**Laufbeleg:** steht aus — ein Lauf mit genau diesem Digest am Showcase (Start,
Konfig-Prüfung, sauberer Abbruch ohne Schlüssel) und ein Lauf von Hand auf der
Pilot-Anlage mit Exit 0 (JanuaPort/januaport#1005, Schritte 2 und 4).

Quelle: JanuaPort/januaport#1005.
