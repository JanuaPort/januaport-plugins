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
| `script-runner-fetcher` | holt gepinnte Commits per ssh mit einem Nur-Lese-Deploy-Key | eigenes Netz mit Ausgang, kein Weg zum Gateway |

- **Nur gepinnter Code läuft.** Ein Pin ist Name, Pfad im Repository und voller
  Commit-Hash; gepinnt wird nur von einem Menschen in JanuaPort. Vor jedem Lauf
  rechnet der Läufer jeden Hash des Pfads nach; Symlinks und Submodule weist er
  ab.
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

**Isolation:** Wo gVisor (`runsc`) eingerichtet ist, läuft die Sandbox darin;
sonst unter runc mit der Härtung oben und geteiltem Kernel. Welche Isolation
aktiv ist, zeigt JanuaPort an. Meldet die Sandbox keinen aktiven
Seccomp-Filter, bedient der Läufer nicht.

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

- **Ablage:** `/opt/jnpt/plugins/script-runner/` mit `docker-compose.yml`,
  `seccomp/jnpt-sandbox-seccomp.json` und einer `.env` mit
  `JNPT_SCRIPT_RUNNER_IMAGE`. Start mit
  `docker compose -f /opt/jnpt/plugins/script-runner/docker-compose.yml up -d`.
- **Umgebung:** `JNPT_SCRIPT_RUNNER_IMAGE` (Pflicht), `JNPT_SCRIPT_RUNNER_RUNTIME`
  (Standard `runc`, mit gVisor `runsc`), `JNPT_GATEWAY_URL` (Standard
  `http://jnpt:8484`).
- **Schlüssel:** JanuaPort legt sie selbst ab — `runtime-key` und `pins.json` in
  `/run/jnpt/plugins/script-runner/`, den Deploy-Key in
  `/run/jnpt/plugins/script-runner-git/`. Die Ordner entstehen über
  systemd-tmpfiles; Beispiel: `deploy/jnpt-plugins-script-runner.conf.example`.
  Ein Schlüsselwechsel braucht keinen Neustart.
- **gVisor:** in `/etc/docker/daemon.json` als Runtime `runsc` mit
  `"runtimeArgs": ["--oci-seccomp", "--host-uds=open"]`. Ohne `--oci-seccomp`
  greift das Profil nicht (der Läufer bedient dann nicht); ohne
  `--host-uds=open` erreicht die Sandbox den Läufer nicht.
- **Gesundheit:** `curl -s http://127.0.0.1:8091/healthz` → `ok`, sobald der
  Schlüssel da ist und sich eine Sandbox mit Seccomp gemeldet hat.
- **Deploy-Key:** nur Leserecht auf das eine Skript-Repository. Der Läufer hat
  keinen Weg, zu schreiben.

## Verantwortung

*Platzhalter — folgt vor P4:* Abschnitt zur Rollenverteilung zwischen
Hersteller und Betreiber (was der Betreiber mit einem Skript-Repository
verantwortet, Branch-Schutz, Prüfung vor dem Pinnen).

## Prüfen

```sh
docker run --rm -v "$PWD":/src -w /src golang:1.25.13 go test ./...
sh probe/run.sh    # Docker-Sonden gegen den echten Stack
```

Einzelheiten für Entwickler: `CLAUDE.md`. Änderungen: `CHANGELOG.md`.
