# script-runner — Skript-Läufer für JanuaPort

Der Skript-Läufer führt **gepinnte Python-Skripte** aus dem Git-Repository des
Betreibers aus. Eine KI ruft ein Skript über JanuaPort wie ein Werkzeug auf;
das Skript darf dabei selbst JanuaPort-Werkzeuge benutzen — mit genau den
Rechten des Aufrufers, begrenzt auf die Werkzeuge, die das Skript deklariert.
So wird aus einer Folge von Werkzeugaufrufen ein wiederholbarer Baustein
(Stufe 3, „Use Cases bauen“).

**Stand: Im Bau** (JanuaPort/januaport#928, Paket P2). Gebaut und getestet ist
der Beisteller selbst; die Gegenstelle im Gateway (Pins, Lauf-Token, Audit),
die Auslieferung und der Katalogeintrag folgen. Noch nicht auf einer Anlage
gelaufen.

## Wie es arbeitet

Drei Dienste aus einem Image, neben dem Gateway:

| Dienst | Aufgabe | Netz |
|---|---|---|
| `script-runner` | nimmt Aufrufe vom Gateway an, hält die Pins, übergibt den Code an die Sandbox, leitet Werkzeugaufrufe des Skripts an JanuaPort weiter | nur `jnpt_default`, kein Host-Port für MCP |
| `script-runner-sandbox` | führt **einen** Lauf aus und wird danach mit leerem Arbeitsspeicher neu gestartet | **kein Netz** |
| `script-runner-fetcher` | prüft Pins gegen den Standard-Zweig und holt Objekte per ssh mit einem Nur-Lese-Deploy-Key | eigenes Netz mit Ausgang, kein Weg zum Gateway |

- **Nur gepinnter Code läuft.** Ein Pin ist Name, Pfad im Repository und voller
  Commit-Hash. Pinbar sind ausschließlich Commits, die Vorfahr des aktuellen
  Kopfes des Remote-Standard-Zweigs sind, einschließlich des Kopfes selbst.
  Das gilt für jeden Pin, von Menschen und über das Admin-MCP. Der Läufer löst
  den Standard-Zweig bei jeder Prüfung frisch auf; der Name ist nicht
  konfigurierbar und muss nicht `main` sein. Commits ausschließlich auf einem
  Seitenzweig oder in einem nicht gemergten PR werden als
  `invalid/not_on_default_branch` abgewiesen. Vor jedem Lauf rechnet der
  Läufer jeden Hash des Pfads nach; Symlinks und Submodule weist er ab.
- **Die Sandbox hat nichts:** kein Netz, keinen Schlüssel, kein Repository,
  keine Ablage, schreibgeschütztes Dateisystem außer zwei kleinen,
  nicht ausführbaren Arbeitsordnern, eigene Seccomp-Sperrliste, keine
  Capabilities.
- **Fehler bleiben drinnen.** Die KI sieht bei einem Fehler nur die Art der
  Ausnahme und die Zeile im Skript — nie einen Traceback, nie Daten.
- **Protokolliert wird nur Zählbares** (Ende, Dauer, Bytes, Zahl der
  Werkzeugaufrufe). Das Audit schreibt JanuaPort.

## Grenzen v1

Die Werte sind Grenzen dieser Fassung, keine Zusagen.

| Grenze | Wert |
|---|---|
| Wandzeit je Lauf | ≤ 25 s, je Skript kleiner wählbar |
| CPU / RAM / Prozesse | 1 / 512 MiB / 128 |
| Arbeitsordner | 64 MiB `/work`, 16 MiB `/tmp` |
| Eingabe / Ausgabe | ≤ 256 KiB / ≤ 1 MiB |
| Werkzeugaufrufe je Lauf | ≤ 50 |
| Gleichzeitige Läufe | 1 |
| Läufe in Folge | etwa 6 je Minute; dazwischen antwortet der Läufer „belegt, in etwa 10 Sekunden erneut versuchen“ |
| Gepinnter Ordner | ≤ 4 MiB, ≤ 1000 Dateien |
| Sprache | Python 3.12 mit Standardbibliothek; reines Python unter `vendor/` im Skriptordner; **kein pip**, keine kompilierten Erweiterungen |
| Git | nur ssh; ein Repository je Anlage |

**Isolation:** Wo gVisor (Runtime `runsc-jnpt`) eingerichtet ist, läuft die
Sandbox darin; sonst unter runc mit der Härtung oben und geteiltem Kernel.
Welche Isolation aktiv ist, zeigt JanuaPort an. Vor jeder Anmeldung prüft die
Sandbox selbst, dass genau **unser** Seccomp-Profil greift (ein gesperrter
Syscall muss mit EPERM abgewiesen werden). Greift ein anderes oder gar kein
Profil, bedient der Läufer nicht.

## Ein Skript anlegen

Jeder Skriptordner im Repository trägt `jnpt-script.yaml`:

```yaml
name: invoice_check            # gleich dem Pin-Namen
description: Prüft offene Rechnungen gegen die Kartei.
entry: main.py
input_schema:
  type: object
  properties:
    limit: {type: integer}
tools:                          # abschließend: nur diese Werkzeuge
  - kartei_find
timeout_s: 20                   # 1 bis 25
```

```python
import json, sys
import januaport                       # vom Läufer bereitgestellt

found = januaport.call("kartei_find", {"q": "offen"})
json.dump({"offen": found.get("structuredContent")}, sys.stdout)
```

Die Eingabe steht als JSON auf stdin und in `januaport.input`, die Ausgabe
geht nach stdout. Ist sie ein JSON-Objekt, bekommt die KI sie zusätzlich
strukturiert.

## Betrieb

- **Standard-Zweig:** Nur dessen Kopf und Vorfahren sind pinbar. Der Abholer
  prüft auch schon gespeicherte Pins im nächsten Durchlauf erneut (normal
  alle 5 s). Entfernt ein Force-Push einen gepinnten Commit aus der Abstammung,
  wird der Pin ungültig; neue Läufe werden verweigert. Laufende Skripte enden
  regulär. Ist der Standard-Zweig nicht auflösbar oder nicht abholbar, bleiben
  bereits geprüfte Pins für dieselbe Repository-URL freigegeben. Neue Pins
  werden mit `fetch_failed` abgewiesen; der Läufer loggt eine Warnung.
  Auch andere Prüffehler entziehen keine bestehende Zweigfreigabe: Nur ein
  negativer Vorfahren-Befund (Exit 1) widerruft. Ein Wechsel der Repository-URL
  leert weiterhin alle Freigaben. Alte Stores brauchen
  nach dem Update zunächst eine erfolgreiche Zweigprüfung.
- **Image:** `ghcr.io/januaport/script-runner`, gebaut von der
  Release-Strecke dieses Repos (Tag `script-runner-vX.Y.Z`, nur `linux/amd64`,
  mit SBOM und Provenance). Die Anlage bezieht es nur per Digest aus dem
  signierten Katalog; der Aktuator schreibt `JNPT_SCRIPT_RUNNER_IMAGE` in die
  `.env` (Beispiel: `deploy/.env.example`, Deskriptor:
  `deploy/script-runner.conf.example`). Was je Digest geprüft wurde, steht in
  `CHANGELOG.md`.
- **Ablage:** `/opt/jnpt/plugins/script-runner/` mit `docker-compose.yml`,
  `seccomp/jnpt-sandbox-seccomp.json` und einer `.env` mit
  `JNPT_SCRIPT_RUNNER_IMAGE`. Start mit
  `docker compose -f /opt/jnpt/plugins/script-runner/docker-compose.yml up -d`.
- **Umgebung:** `JNPT_SCRIPT_RUNNER_IMAGE` (Pflicht), `JNPT_SCRIPT_RUNNER_RUNTIME`
  (Standard `runc`, mit gVisor `runsc-jnpt`), `JNPT_GATEWAY_URL` (Standard
  `http://jnpt:8484`).
- **Schlüssel:** JanuaPort legt sie selbst ab — `runtime-key` und `pins.json` in
  `/run/jnpt/plugins/script-runner/`, den Deploy-Key in
  `/run/jnpt/plugins/script-runner-git/`. Die Ordner entstehen über
  systemd-tmpfiles; Beispiel: `deploy/jnpt-plugins-script-runner.conf.example`.
  Ein Schlüsselwechsel braucht keinen Neustart.
- **gVisor:** in `/etc/docker/daemon.json` ein **eigener** Runtime-Eintrag
  `runsc-jnpt` mit `"runtimeArgs": ["--oci-seccomp", "--host-uds=open"]`
  (Vorlage: `deploy/daemon.json.example`) und `JNPT_SCRIPT_RUNNER_RUNTIME=runsc-jnpt`
  in der `.env`. Ein eigener Name, weil `runtimeArgs` für jeden Container
  gelten, der den Namen nutzt — ein vorhandenes `runsc` des Betreibers bleibt
  so unberührt. Ohne `--oci-seccomp` greift das Profil nicht (der Läufer
  bedient dann nicht); ohne `--host-uds=open` erreicht die Sandbox den Läufer
  nicht. `open` (nicht `all`) erlaubt nur das Öffnen von Unix-Sockets, die in
  die Sandbox eingehängt sind, kein Anlegen; eingehängt ist nur der
  Socket-Ordner des Läufers, read-only — die Grenze ist die Mount-Liste.
- **Gesundheit:** `curl -s http://127.0.0.1:8091/healthz` → `ok`, sobald der
  Schlüssel da ist und sich eine Sandbox mit unserem Profil gemeldet hat, sonst
  `not ready` (503); jeder andere Pfad 404. Auf dem Host nur Loopback; aus dem
  Netz `jnpt_default` ist der Port ebenfalls erreichbar und verrät dort nur
  bereit/nicht bereit.
- **Deploy-Key:** nur Leserecht auf das eine Skript-Repository. Der Läufer hat
  keinen Weg, zu schreiben.
- **Zeitpläne** (JanuaPort/januaport#1039, im Bau): Angelegt und verwaltet
  werden sie in JanuaPort (Einstellungen › Plugins › Skript-Läufer, REST oder
  Admin-MCP), nicht hier. JanuaPort legt sie als `schedules.json` neben
  `pins.json` ab — ohne Token, ohne Eingabe, ohne Secret. Der Läufer meldet
  einen fälligen Termin an JanuaPort; JanuaPort prüft Fälligkeit, Identity und
  Rechte und startet den Lauf auf dem normalen Weg. Ist der Läufer gerade
  belegt, wird der Termin übersprungen und gezählt. **Kein Nachholen:** Ein
  Termin, der wegen eines Neustarts, einer Pause oder einer Störung verpasst
  wurde, läuft nicht später; er wird gezählt. Jeder Zeitplan hat eine eigene
  Zeitzone (IANA, z. B. `Europe/Berlin`); ein Termin in der Sommerzeit-Lücke
  (letzter Sonntag im März, 02:00–03:00) entfällt und wird gezählt, einer in
  der doppelten Stunde im Oktober läuft zweimal. Höchstens 20 Zeitpläne,
  Mindestabstand fünf Minuten. Ein Neustart des Läufers setzt seine Zähler
  zurück; den letzten Lauf kennt JanuaPort.

## Verantwortung

*Platzhalter — folgt vor P4:* Abschnitt zur Rollenverteilung zwischen
Hersteller und Betreiber (was der Betreiber mit einem Skript-Repository
verantwortet, Branch-Schutz, Prüfung vor dem Pinnen).

## Prüfen

```sh
# aus dem Repo-Root
docker run --rm -v "$PWD":/repo -w /repo/script-runner golang:1.26.9 go test ./...
sh script-runner/probe/run.sh    # Docker-Sonden gegen den echten Stack
```

Einzelheiten für Entwickler: `CLAUDE.md`. Änderungen: `CHANGELOG.md`.
