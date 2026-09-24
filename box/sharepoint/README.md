# box/sharepoint — SharePoint als Sicherungs- und Ablageziel der Box

Zwei **Bringer** auf dem Host, kein Produktcode. Die KI arbeitet auf der lokalen Ablage
(ext4); die Bringer verbinden sie mit zwei SharePoint-Sites des Kunden — app-only,
`Sites.Selected`, Site-Grant `write` je App.

| Datei | Zweck |
|---|---|
| `install-rclone.sh` | rclone als statisches Binary aus dem GitHub-Release, SHA256 geprüft (Debian-apt ist zu alt für `client_credentials`) |
| `sicherung.app.example`, `ablage.app.example` | Kennungen je App (Client-/Tenant-ID) → `/etc/jnpt/rclone/<name>.app` |
| `zugang-sp.sh` | **tippt der Mensch**: Secrets (ohne Echo) + Site-URLs → `<name>.secret` (0600), `<name>.site` |
| `sp-token-probe.py` | Messung ohne Site-URL: Token, Rollen, 403-Gegenproben |
| `sp-einrichten.py` | Messung mit Site: 200/403/404, Bibliotheken, Schreibprobe, Wurzel-403; schreibt den rclone-Remote (ohne Secret) |
| `rclone-probe.sh` | lsd / copyto / moveto / purge mit neutraler Datei |
| `jnpt-sicherung-sharepoint.{sh,service,timer}` | Sicherung: `jnpt backup` lokal → `.teil` → moveto → Größe gegenlesen → Generationen (root, 02:30) |
| `jnpt-ablage-abgleich.{sh,service,timer}` | Zwei-Wege-Abgleich mit `rclone bisync` (**uid 65532**, alle 5 min) |

Reihenfolge, Stolperfallen und die Prüfschritte: [`../RUNBOOK.md`](../RUNBOOK.md),
Abschnitt „Ablage und Sicherung über SharePoint".

Vor dem Einsatz anpassen: `OBEN`/`LOKAL` in `jnpt-ablage-abgleich.sh` und
`ReadWritePaths` in `jnpt-ablage-abgleich.service` (Voreinstellung: der Platzhalter
`beispiel`).

⚠️ **Der Abgleich muss als uid 65532 laufen** — `fs.protected_hardlinks=1`: Was root
anlegt, kann der Gateway-Prozess nicht per Hardlink verschieben (`move_file` scheitert).

⚠️ Kein Secret in diesem Verzeichnis, in keiner Config, in keinem Log — nur in den
0600-Dateien auf dem Host.
