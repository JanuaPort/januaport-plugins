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
| `script-runner` | nimmt Aufrufe vom Gateway an, hält die Pins, übergibt den Code an die Sandbox, leitet Werkzeugaufrufe des Skripts an JanuaPort weiter | nur das interne Netz `jnpt_plugins` (kein Ausgang, nur das Gateway daneben), kein Host-Port |
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
  Capabilities. Das Einzige, was sie zusätzlich sieht: die geprüften
  Python-Bibliotheken, nur lesend (siehe „Python-Bibliotheken“).
- **Fehlertexte bleiben drinnen.** Bei einem Fehler erscheinen die Art der
  Ausnahme und die Zeile im Skript; stdout und stderr werden nicht ausgegeben.
  Explizites `januaport.log` geht zusätzlich ausschließlich in die Lauf-Metadaten.
- **Das Dienstlog enthält nur Zählbares** (Ende, Dauer, Bytes, Zahl der
  Werkzeugaufrufe), nie das Lauf-Protokoll. Das Audit schreibt JanuaPort.

## Grenzen v1

Die Werte sind Grenzen dieser Fassung, keine Zusagen.

| Grenze | Wert |
|---|---|
| Wandzeit je Lauf | ≤ 25 s, je Skript kleiner wählbar |
| CPU / RAM / Prozesse | 1 / 512 MiB / 128 |
| Arbeitsordner | 64 MiB `/work`, 16 MiB `/tmp` |
| Eingabe / Ausgabe | ≤ 256 KiB / ≤ 1 MiB |
| Lauf-Protokoll | ≤ 64 KiB einschließlich `\n[gekürzt]\n`; Platz für die Marke wird reserviert |
| Werkzeugaufrufe je Lauf | ≤ 50 |
| Gleichzeitige Läufe | 1 |
| Läufe in Folge | etwa 6 je Minute; dazwischen antwortet der Läufer „belegt, in etwa 10 Sekunden erneut versuchen“ |
| Gepinnter Ordner | ≤ 4 MiB, ≤ 1000 Dateien (der Code, ohne `wheels/`) |
| Bibliotheken | nur mit `requirements`: Wheels für Python 3.12 auf manylinux x86_64; ≤ 300 MiB und ≤ 30 000 Dateien je Lock installiert, ≤ 2 GiB für alle Locks; ein Thread für BLAS/OpenMP |
| Sprache | Python 3.12 mit Standardbibliothek; reines Python unter `vendor/` im Skriptordner; Bibliotheken nur als geprüfte Wheels (siehe unten); **kein pip**, kein Download |
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
# requirements: requirements.lock   # optional: Python-Bibliotheken als Wheels, siehe unten
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

Mit `januaport.log("geprüft", 12)` hängt das Skript `geprüft 12\n` an sein
Lauf-Protokoll an. Andere Werte werden mit `str()` umgewandelt, Teile mit
Leerzeichen getrennt; auch `januaport.log()` erzeugt eine Zeile. Das Ergebnis
steht ausschließlich in `_meta["januaport.ai/run"].log`, auch bei Skriptfehler,
Wandzeit, Speichergrenze oder Abbruch nach dem Start. Leer oder vor dem Start
fehlt das Feld. Ungültiges UTF-8 wird ersetzt. Der Wächter begrenzt bereits
seinen Speicher auf 64 KiB inklusive einmaliger Kürzungsmarke und verwirft
weiteren Text. Das Dienstlog enthält diese Texte nie. Das Gateway muss das
additive Feld übernehmen, damit Nutzer es sehen (Kern-Paket P3).

## Python-Bibliotheken (Wheels im Repository)

Ein Skript darf Bibliotheken wie numpy oder pandas benutzen, wenn der
Skriptordner sie als **Wheels** mitbringt. Der Läufer lädt nichts nach: Es gibt
kein pip im Image und beim Pinnen keinen Zugriff auf das Internet. Die Wheels
liegen im Repository des Betreibers; ein Mensch pinnt den Commit, damit sind sie
über die Git-Hashes gesichert.

```
meine-auswertung/
  jnpt-script.yaml        # requirements: requirements.lock
  main.py
  requirements.lock       # pip-Format, jeder Eintrag name==version mit --hash=sha256:
  wheels/
    pandas-3.0.6-cp312-cp312-manylinux_2_24_x86_64.manylinux_2_28_x86_64.whl
    numpy-2.5.4-cp312-cp312-manylinux_2_27_x86_64.manylinux_2_28_x86_64.whl
    …
```

**Lock bauen.** Wheels für genau diese Plattform laden — aus einem Index, den
Sie benennen und prüfen (hier `pypi.org`; ein eigener Spiegel ist ebenso
möglich), samt Abhängigkeiten, und die Hashes ins Lock schreiben:

```sh
pip download --only-binary=:all: --implementation cp --python-version 3.12 \
  --platform manylinux2014_x86_64 --platform manylinux_2_28_x86_64 \
  --index-url https://pypi.org/simple -d meine-auswertung/wheels pandas
cd meine-auswertung
for w in wheels/*.whl; do
  n=$(basename "$w" | cut -d- -f1); v=$(basename "$w" | cut -d- -f2)
  printf '%s==%s \\n    %s\n' "$n" "$v" "$(pip hash "$w" | tail -1)"
done > requirements.lock
# Gegenprobe: dieselben Wheels, jetzt mit Hash-Pflicht
pip download --only-binary=:all: --require-hashes --no-deps -r requirements.lock \
  --implementation cp --python-version 3.12 --platform manylinux2014_x86_64 \
  --platform manylinux_2_28_x86_64 -d /tmp/gegenprobe
```

**Was der Läufer prüft** (jede Abweichung ergibt einen ungültigen Pin ohne
Werkzeug, der Grund steht in der Pin-Liste):

| Grund | Bedeutung |
|---|---|
| `libs_invalid` | Lock oder Wheel formal falsch; Wheel ohne Lock-Eintrag oder Eintrag ohne Wheel; eine sdist; unsicherer Inhalt (Pfadausbruch, Symlink, …) |
| `libs_hash_mismatch` | die SHA-256 eines Wheels steht nicht im Lock |
| `libs_platform` | Tags außer Python 3.12 auf manylinux x86_64 (kein arm64, kein musllinux, glibc höchstens 2.41) |
| `libs_too_large` | über den Grenzen: 300 MiB und 30 000 Dateien je Lock installiert, 2 GiB für alle Locks |

Der Läufer entpackt jedes Wheel selbst (ohne Python), gehärtet und einmal je
Lock, nach `libs/<Hash des Locks>/`. Bis dahin steht der Pin auf `pending`.
Die Sandbox hängt das Verzeichnis nur lesend an (`sys.path.append`, keine
`.pth`-Dateien, keine Entry-Points) und setzt `OPENBLAS_NUM_THREADS=1` und
`OMP_NUM_THREADS=1`. Ein neues Lock ergibt ein neues Verzeichnis; was kein Pin
mehr braucht, wird aufgeräumt, ein Wechsel der Repository-URL leert alles.

**Lieferkette — was das heißt und was nicht.** Der Hash im Lock und der Pin
durch einen Menschen binden den Lauf an **das, was in Ihrem Repository liegt**.
Sie belegen nicht, dass ein Wheel das echte Paket vom Index ist; eine
Herkunftsprüfung gibt es nicht. Die Wahl der Pakete, Schadpakete und
Typosquatting liegen beim Betreiber, ebenso die Lizenzen der Pakete.

**Benannte Grenze.** Eigene, vertrauliche Pakete teilen sich die Sichtbarkeit
mit allen Skripten der Anlage: Jede Sandbox sieht alle Locks unter `/libs`,
auch die eines anderen Pins.

**Wartung (#1096).** Der vermittler sieht das ganze Volume `libs`, bereitet
Wheels unter `/libs/staging` vor und veröffentlicht erst geprüfte Verzeichnisse
atomar unter `/libs/ready`. Die Sandbox mountet nur den Unterordner `ready`
unter `/libs`, read-only. Sie sieht weder `.tmp` noch `.wheel`; ihr Importpfad
bleibt gleich. Quelle und Ziel des Rename liegen im selben Mount, ein
Kopieren zwischen Dateisystemen ist nicht erforderlich.

Die neue Compose verlangt Docker Engine ≥ 26 und Compose ≥ 2.35 mit
Volume-Subpaths. Neue Volumes übernehmen beide Unterordner aus dem Image.
Bei bestehenden P1-Volumes im Wartungsfenster den Stack anhalten, `ready` und
`staging` für Nutzer 65532 anlegen (staging 0700) und fertige
64-Hex-Verzeichnisse nach `ready` verschieben; Zwischenreste verwerfen.
Alternativ das ableitbare Cache-Volume neu anlegen. Ein Neustart prüft jedes
fertige Verzeichnis einmal gegen sein Manifest und verwirft Abweichungen.

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
  die Sandbox eingehängt sind, kein Anlegen; eingehängt sind nur der
  Socket-Ordner des Läufers und `/libs` (die Bibliotheken), beide read-only —
  die Grenze ist die Mount-Liste.
- **Netz:** Der Läufer hängt nur am Netz `jnpt_plugins`, das die Kern-Compose
  von JanuaPort mit `internal: true` anlegt; dort hängt sonst nur das Gateway.
  Er erreicht `jnpt:8484`, das Gateway ihn unter `script-runner:8090`, ins
  Internet und ins LAN kommt er nicht. Fehlt das Netz auf der Anlage, startet
  die Compose nicht; der Aktuator (ab Fassung 3) bucht dann `unprovisioned`.
- **Gesundheit:** ein Docker-Healthcheck im Container fragt
  `http://127.0.0.1:8091/healthz`: `ok`, sobald der Schlüssel da ist und sich
  eine Sandbox mit unserem Profil gemeldet hat, sonst `not ready` (503); jeder
  andere Pfad 404. Der Aktuator liest den Status (`HEALTH_DOCKER=1` im
  Deskriptor): `docker inspect -f '{{.State.Health.Status}}'
  jnpt-plugin-script-runner-script-runner-1` → `healthy`. Aus dem Netz
  `jnpt_plugins` ist der Port ebenfalls erreichbar und verrät dort nur
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
sh script-runner/probe/run.sh    # Docker-Sonden gegen den echten Stack (lädt numpy/pandas als Wheels per pip)
sh script-runner/probe/measure-libs.sh  # Messung: Bibliotheken im Sandbox-Image
```

Einzelheiten für Entwickler: `CLAUDE.md`. Änderungen: `CHANGELOG.md`.
