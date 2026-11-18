# CLAUDE.md — `ebics-abholer` (Beisteller: camt.053 per EBICS abholen)

Für Agenten, die an diesem Ordner arbeiten. Die Anleitung für Betreiber steht in
`README.md`, das Betreiber-Runbook im Produkt-Repo (`docs/ebics-abholer.md`) — hier
steht, warum das Ding so aussieht, wie es aussieht.

## Verantwortung

Ein Job, kein Dienst: Der systemd-Timer der Anlage startet dreimal täglich
`docker compose … run --rm` (`ebics-abholer.timer`/`.service`). Der Lauf holt
camt.053 per EBICS (H004/H005) und legt die Dateien in `/ablage` ab, aus dem
JanuaPort sie als Datei-Integration liest. Er kennt JanuaPort nicht — kein `/mcp`,
kein Token, kein gemeinsames Netz. Entstanden in JanuaPort/januaport#549; Image und
Release seit JanuaPort/januaport#1005.

## Aufbau: Datei → Verantwortung

| Datei | Verantwortung |
|---|---|
| `fetch.php` | Führt camt.053-Abruf, Dateiaufbereitung, Ablage und anschließende Bankquittung aus. |
| `logik.php` | Entscheidet Konfigurationsprüfung, Zeitfenster, Dateinamen, Prüfsummenvergleich und Exit-Codes. |
| `start.php` | Lädt Konfiguration und Schlüsseldatei und baut Bank- sowie Teilnehmerobjekte. |
| `version.php` | Wendet die H004-/H005-spezifischen Bibliothekseinstellungen an. |
| `init.php` | Erzeugt Teilnehmerschlüssel, sendet INI/HIA und erstellt das Initialisierungsprotokoll. |
| `hpb.php` | Holt Bankschlüssel und führt Erstübernahme, Vergleich oder bestätigten Austausch mit Dateisicherung aus. |
| `letter.php` | Erzeugt Text- und PDF-Initialisierungsprotokolle aus der vorhandenen Schlüsseldatei. |
| `config.example.php` | Zeigt die Betreiberkonfiguration für Bank, Teilnehmer, Abruf und Ablage. |
| `Dockerfile`, `composer.json`, `composer.lock` | Definieren PHP-Imagebau, Erweiterungen und Bibliotheksabhängigkeiten. |
| `docker-compose.ebics-abholer.yml` | Verdrahtet Abholer-Image, Nutzer und Ablage-/Geheimnis-Mounts. |
| `ebics-abholer.service`, `ebics-abholer.timer` | Starten den einmaligen Compose-Lauf an drei täglichen Terminen mit Nachholen. |
| `tests/run.php` | Führt die PHP-Teststrecke mit Bibliotheks-Stubs und echtem Dateisystem aus. |
| `tests/image.sh` | Prüft Quelldateien, Imagehülle und optional das gebaute Image. |

## Öffentliche Schnittstelle

| Teil | Vertrag |
|---|---|
| Image | `ghcr.io/januaport/ebics`, bezogen **per Digest** (`JNPT_EBICS_IMAGE` in der `.env`) |
| Befehle | `php /app/fetch.php` (Default-CMD, `--von/--bis` für Nachzug), `php /app/init.php`, `php /app/hpb.php [--pruefen \| --uebernehmen --x002=… --e002=…]`, `php /app/letter.php` |
| Exit-Codes `fetch.php` | 0 abgeholt/nichts Neues · 1 Konfiguration · 2 Bank/Netz · 3 nicht abgelegt · 4 Bankschlüssel geändert (README) |
| Mounts | `/ablage` (schreibend, das kontrollierte Verzeichnis) · `/geheim` (lesend; schreibend nur für init/hpb per `docker run`) |
| Nutzer | UID 65532 — dieselbe wie das distroless-Gateway; die Host-Verzeichnisse müssen ihr gehören |
| Compose | Projekt `jnpt-plugin-ebics`, Dienst `ebics-abholer`, kein `restart`, kein `networks` |

## Image und Release

- **Dockerfile, drei Stufen:** `vendor` (Composer auf der Bau-Plattform, aus der
  `composer.lock`), `erweiterungen` (bcmath und zip gegen dieselbe PHP-Basis
  kompiliert), Laufzeit (PHP-CLI Alpine, `libzip`, die zwei `.so`, Skripte,
  `vendor/`). Alle Basen per Digest gepinnt; angehoben wird bewusst, Tag **und**
  Digest zusammen.
- **Workflow** `.github/workflows/ebics-abholer.yml`: Pfad `ebics-abholer/**` →
  prüfen (Teststrecke ohne Netz, Bau je Plattform, K-Scan). Tag
  `ebics-abholer/vX.Y.Z` → zusätzlich Push per Digest, K-Scan des Digests, dann
  Versions-Tag, Digest in der Zusammenfassung. Actions per SHA, Trivy als Binary
  mit Prüfsumme.
- **K-Scan (SEC-Regel):** `trivy image --platform <p> --severity CRITICAL,HIGH
  --ignore-unfixed --exit-code 1` je Plattform. Befund mit Fix = Neubau, nicht
  `.trivyignore`. Ausnahmen nur mit CVE, Grund, `exp:` ≤ 90 Tage (`tests/image.sh`
  prüft die Form).
- **CHANGELOG** liegt in diesem Ordner, nicht in einem eigenen `ebics/`: Quelle,
  Tag-Präfix und Workflow-Pfad hängen hier; `tunnel/` ist ein eigener Ordner nur,
  weil es dort keinen Code gibt.

## Invarianten

1. **Nur abholen.** Kein Skript kann einreichen; `tests/run.php` wacht darüber.
2. **Ablegen vor Quittieren**, nie überschreiben, Dateiname nie von der Bank.
   Ein **nicht lesbarer** ZIP-Eintrag (die Bibliothek liefert `false`) wird nie still
   übergangen: `ebicsInhalte` wirft `EbicsAblageFehler`, **bevor** `ebicsAblegen`
   läuft — negative Quittung, Exit 3, keine Datei im Verzeichnis (sonst gäbe es beim
   Neuabruf Doppelte). Ebenso ein Container, aus dem nichts Ablegbares übrig bleibt.
   Nur **wirklich leere** Einträge (etwa Verzeichniseinträge) werden übergangen. Die
   Meldung nennt weder Inhalt noch Eintragsnamen, nur die Position. Die Entscheidung
   steht in `ebicsAuszuegeAblegen` (`logik.php`), die die Quittungs-Closure in
   `fetch.php` ruft — deshalb ohne Bank und ohne `vendor/` testbar. Leere Rohdaten und
   Nicht-ZIP (H004) sind kein Befund und bleiben unverändert.
3. **Nie ein Kontoumsatz, eine IBAN oder ein Schlüsselwort im Protokoll.**
4. **Geheimnisse nie im Image, nie im Repo:** `config.php`, `keyring.json`,
   Sicherungen und das Initialisierungsprotokoll sind über `.gitignore` gesperrt;
   ins Image kommt nur `config.example.php`.
5. **Kein Composer zur Laufzeit.** `composer.json`/`.lock` liegen im Image, weil
   `tests/run.php` sie prüft — das Werkzeug nicht.
6. **Die Teststrecke spricht nie mit dem Netz.** Im Image läuft sie mit
   `--network none` vollständig (104 Fälle).

## Stolperfallen

- **`--ignore-platform-reqs` in der vendor-Stufe ist Absicht** (anderes PHP als zur
  Laufzeit); die Laufzeit-Stufe prüft die Erweiterungen der Bibliothek beim Bau.
- **Alpine statt Debian:** `adduser -D -u 65532`, nicht `useradd`. Wer zurück auf
  Debian will, prüft Größe und K-Scan neu.
- **Projektname gewechselt** (`jnpt-ebics` → `jnpt-plugin-ebics`): Das Default-Netz
  heißt damit anders. Eine Anlage, deren Firewall an einem festen Bridge-Namen
  hängt, pinnt ihn per eigenem Overlay (Box: `jnpt-ebics0`, `box/RUNBOOK.md` §11) —
  der Name bleibt dort stabil, egal wie das Projekt heißt.
- **`docker image inspect .Size`** meldet je nach Image-Store entpackt oder
  komprimiert; `tests/image.sh` misst deshalb mit `du` im Container.
- **Git Bash unter Windows** verbiegt `/app/…`-Pfade: `MSYS_NO_PATHCONV=1` vor
  `tests/image.sh`.

## Prüfen

```bash
bash ebics-abholer/tests/image.sh                 # statisch, ohne Docker-Bau
docker build -t ebics:pruefung ebics-abholer
bash ebics-abholer/tests/image.sh ebics:pruefung  # plus Image (läuft ohne Netz)
```
