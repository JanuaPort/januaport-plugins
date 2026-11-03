# script-runner — Skript-Läufer für JanuaPort

Der Skript-Läufer führt **gepinnte Python-Skripte** aus dem Git-Repository des
Betreibers oder aus freigegebenen Speicherpaketen aus. Eine KI ruft ein Skript
über JanuaPort wie ein Werkzeug auf;
das Skript darf dabei selbst JanuaPort-Werkzeuge benutzen — mit genau den
Rechten des Aufrufers, begrenzt auf die Werkzeuge, die das Skript deklariert.
So wird aus einer Folge von Werkzeugaufrufen ein wiederholbarer Baustein
(Stufe 3, „Use Cases bauen“).

**Stand: Im Bau** (JanuaPort/januaport#928, Paket P2). Gebaut und getestet ist
der Beisteller selbst; die Gegenstelle für Vertragskennung 2 ist ab Gateway
0.77.0 vorhanden. Auslieferung und Katalogeintrag folgen. Noch nicht auf einer Anlage
gelaufen.

**Benannte Kompatibilitätsgrenze:** Dieser Läufer meldet `contract: 2` und
braucht ein Gateway ab **0.77.0**. Ein älteres Gateway sperrt den Läufer ganz
(„andere Vertragsfassung“). **Update-Reihenfolge: erst das Gateway, dann der Läufer.**

## Wie es arbeitet

Vier dauerhafte Dienste (Apphost optional) und ein kurzer Init-Dienst aus einem Image, neben dem Gateway:

| Dienst | Aufgabe | Netz |
|---|---|---|
| `script-runner` | nimmt Aufrufe vom Gateway an, holt Speicherlisten und -pakete als Bote, hält die Pins, übergibt den Code an die Sandbox, leitet Werkzeugaufrufe des Skripts an JanuaPort weiter | nur das interne Netz `jnpt_plugins` (kein Ausgang, nur das Gateway daneben), kein Host-Port |
| `script-runner-sandbox` | führt **einen** Lauf aus und wird danach mit leerem Arbeitsspeicher neu gestartet | **kein Netz** |
| `script-runner-fetcher` | prüft Git-Pins gegen den Standard-Zweig und holt Objekte per ssh mit einem Nur-Lese-Deploy-Key; prüft Speicherlisten und Pakete unabhängig davon (#1277 S4b) | eigenes Netz mit Ausgang, kein Weg zum Gateway |
| `script-runner-init` | legt die Bibliotheksordner an und beendet sich erfolgreich, bevor die Sandbox startet | **kein Netz**, nur das Volume `libs` |
| `script-runner-apphost` | liefert geprüfte Browser-Apps, bindet die Anmeldung an den Browser und reicht MCP sowie TLS-Freigaben an den privaten Gateway-Listener weiter | ausschließlich `jnpt_apps`, Profil `apps`, kein Host-Port |

Für den **Desktop-Läufer** (#1165 D2b) stellt der Beisteller zusätzlich
geprüfte Pakete bereit. JanuaPort schreibt `desktop-pins.json` atomar neben
`pins.json`; beide nutzen dasselbe Skript-Repository und dieselbe Prüfung
gegen den Standard-Zweig. Das Desktop-Manifest heißt `jnpt-desktop.yaml`
(einschließlich `python`, Timeout 1–86400 Sekunden).
Der interne Abruf `GET /desktop/package?commit=…&path=…` verlangt den
runtime-key und einen aktuell freigegebenen Desktop-Pin, maximal 128 MiB.
Status/Manifestwerte stehen in `tools/list` unter `desktop_pins`, immer mit
`name`, `commit`, `path` und `algo` aus `desktop-pins.json`. Der Kern ordnet
Deklarationen nur bei gleichem Namen, Commit und Pfad seinem eigenen Pin zu.
Bei Pinentzug beendet der Kern offene Aufträge des Pins mit `denied` und
bricht einen laufenden Lauf ab; der Beisteller liefert nur aktuelle Pins.
Desktop-Pins laufen auf dem Gerät: Der Beisteller startet dafür keine
Sandbox und entpackt keine Windows-Wheels. Auch Zeitpläne taktet das Gerät
aus seinem signierten Manifest. Details: [Weg A und Dateiformat](contract/README.md#desktop-pins-und-paketabruf-auf-weg-a-1165-d2b).

Die folgenden Sandbox-/Laufgrenzen und Bibliotheksregeln gelten für lokale
Skript-Pins.

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

## Grenzen je Lauf (Vertragsfassung 5)

Das Gateway übergibt die Laufgrenzen in `X-Jnpt-Run-Limits`. Wandzeit, Ausgabe und
Werkzeugaufrufe sind je Lauf wählbar, bis zu den technischen Decken unten.
Ohne diese Kopfzeile gelten die Altgrenzen 25 s / 1 MiB / 50 Aufrufe.
Eine vorhandene, falsch geformte Kopfzeile wird vor dem Start als
Protokollfehler abgewiesen. Details und Golden-Dateien: [Vertrag](contract/README.md).

| Grenze | Wert |
|---|---|
| Wandzeit je Lauf | Minimum aus Gateway-Vorgabe, optionalem Manifest-Vorschlag und konfigurierter Decke; höchstens 24 h |
| CPU / RAM / Prozesse | 1 / 512 MiB / 128 |
| Arbeitsordner | 64 MiB `/work`, 16 MiB `/tmp` |
| Eingabe / Ausgabe | ≤ 256 KiB / Gateway-Vorgabe, gekürzt auf höchstens 16 MiB |
| Lauf-Protokoll | ≤ 64 KiB einschließlich `\n[gekürzt]\n`; Platz für die Marke wird reserviert |
| Werkzeugaufrufe je Lauf | Gateway-Vorgabe, gekürzt auf höchstens 10 000 |
| Gleichzeitige Läufe | 1 |
| Läufe in Folge | etwa 6 je Minute; dazwischen antwortet der Läufer „belegt, in etwa 10 Sekunden erneut versuchen“ |
| Gepinnter Ordner | ≤ 4 MiB, ≤ 1000 Dateien (der Code, ohne `wheels/`) |
| Bibliotheken | nur mit `requirements`: Wheels für Python 3.12 auf manylinux x86_64; ≤ 300 MiB und ≤ 30 000 Dateien je Lock installiert, ≤ 2 GiB für alle Locks; ein Thread für BLAS/OpenMP |
| Sprache | Python 3.12 mit Standardbibliothek; reines Python unter `vendor/` im Skriptordner; Bibliotheken nur als geprüfte Wheels (siehe unten); **kein pip**, kein Paketindex-Download |
| Git | nur ssh; ein Repository je Anlage |

**Schritt L (0.3.0):** Die eigene 25-s-Kappe entfällt (SEC L6).
`timeout_s` ist optional und schlägt 1..86400 ganze Sekunden vor; fehlt es,
entscheidet die Kopfzeile. Ohne Kopfzeile bleiben höchstens 25 s.
Ohne `timeout_s` meldet der Läufer 25 s; längere Läufe wählt der Aufrufer ausdrücklich.
Dafür dient `start_script_run(wall_s=…)`. Der gemeldete Ersatzwert begrenzt
den Lauf mit Kopfzeile nicht zusätzlich.
`JNPT_SCRIPT_RUNNER_MAX_WALL_S` begrenzt den Läufer zusätzlich (1..86400,
Standard 86400; ungültige Werte verhindern den Start). Der Läufer meldet
diese Decke als `max_wall_s` in den Ergebnis-Metadaten von `tools/list`,
weiterhin mit `contract: 2`.
**Wirkung erst mit JanuaPort-Schritt F, bis dahin bleibt der Kern-Riegel bei 25 s.**

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
timeout_s: 20                   # optionaler Vorschlag: 1 bis 86400 ganze Sekunden
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

**Lock bauen.** Bequemer geht es mit dem offenen Rezept [Python-Bibliotheken bauen](https://github.com/JanuaPort/januaport-recipes/tree/main/rezepte/python-bibliotheken-bauen): `requirements.in` eingeben, Wheels und Hash-Lock beim Bauen erzeugen und als PR prüfen. Die Prüfregeln des Läufers und das Pinnen durch einen Menschen bleiben unverändert.

**Lock von Hand bauen.** Wheels für genau diese Plattform laden — aus einem Index, den
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
unter `/libs`, read-only und mit `nocopy: true`. Sie sieht weder `.tmp` noch `.wheel`; ihr Importpfad
bleibt gleich. Quelle und Ziel des Rename liegen im selben Mount, ein
Kopieren zwischen Dateisystemen ist nicht erforderlich.

Die neue Compose verlangt Docker Engine ≥ 26 und Compose ≥ 2.35 mit
Volume-Subpaths. Vor der Sandbox läuft nun `script-runner-init` mit dem
Unterbefehl `init-libs` als Nutzer 65532: ohne Netz, mit read-only-Dateisystem,
allen Capabilities entzogen und ausschließlich dem vollen Volume `libs`.
Er legt `ready` (0755) und `staging` (0700) an und erhält vorhandene Inhalte.
Die Sandbox wartet auf seinen erfolgreichen Abschluss. Das gilt auch beim
normalen Einstieg `docker compose up -d script-runner`; Image und Compose
müssen gemeinsam aktualisiert werden.

Ein Subpath-Mount legt fehlende Unterordner nicht aus dem Image an.
`nocopy: true` verhindert außerdem, dass Docker den Image-Inhalt von `/libs`
in ein leeres `ready` kopiert. Nach einem frischen Start bleibt die Sicht der
Sandbox dadurch leer, bis geprüfte Bibliotheken veröffentlicht werden.

Bei bestehenden P1-Volumes im Wartungsfenster den Stack anhalten, den neuen
Init-Dienst starten und fertige
64-Hex-Verzeichnisse nach `ready` verschieben; Zwischenreste verwerfen.
Alternativ das ableitbare Cache-Volume neu anlegen. Ein Neustart prüft jedes
fertige Verzeichnis einmal gegen sein Manifest und verwirft Abweichungen.

## Betrieb

- **Standard-Zweig (Git-Quelle):** Nur dessen Kopf und Vorfahren sind pinbar. Der Abholer
  prüft auch schon gespeicherte Pins im nächsten Durchlauf erneut (normal
  alle 5 s). Entfernt ein Force-Push einen gepinnten Commit aus der Abstammung,
  wird der Pin ungültig; neue Läufe werden verweigert. Laufende Skripte enden
  regulär. Ist der Standard-Zweig nicht auflösbar oder nicht abholbar, bleiben
  bereits geprüfte Pins für dieselbe Repository-URL freigegeben. Neue Pins
  werden mit `fetch_failed` abgewiesen; der Läufer loggt eine Warnung.
  Auch andere Prüffehler entziehen keine bestehende Zweigfreigabe: Nur ein
  negativer Vorfahren-Befund (Exit 1) widerruft. Ein Wechsel der Repository-URL
  leert weiterhin alle Git-Freigaben. Alte Stores brauchen
  nach dem Update zunächst eine erfolgreiche Zweigprüfung.
- **Speicherquelle (#1277 S4b):** Der Abholer prüft die signierte Liste
  und Pakete unter `speicher/inbox/` alle 5 s unabhängig von Git und schreibt
  Freigaben nach `speicher/state.json`. Der Vermittler holt Liste und fehlende
  Pakete alle 60 s mit dem Plugin-Runtime-Key, ohne Umleitung und begrenzt;
  `Retry-After` gilt, nach 404 mindestens 5 Minuten Pause. Vor jeder
  Codeübergabe prüft die Senke Freigabe, Frist aus `accepted.manifest.issued_at`
  und Pakethash erneut — auch nach dem Warten auf die Sandbox. Entzug wirkt
  spätestens nach 12 Minuten, auch bei angehaltenem Abholer. Speicherpins
  erscheinen im bestehenden `tools/list`-Vertrag 2, ohne zusätzliche Felder.
  `runner-pins.pub` liegt neben dem Deploy-Key und wird je Durchlauf neu gelesen.
  Fehlender/gewechselter Schlüssel entfernt Speicherfreigaben sofort. Bei
  fehlender `inbox/list.json` gilt „keine Speicherpins“: einmal INFO beim ersten
  Fehlen und je Wechsel vorhanden/fehlend, kein wiederholtes WARN. Freigaben
  entfallen weiterhin sofort; Signatur-, Hash- und Lesefehler warnen je Takt. Bei
  Höchststand 0 (auch Erstinstallation, Rücksetzung und Neustart in der Sperre)
  gilt ab eigener Prozessuhr eine Sperre bis streng nach 12 Minuten; danach
  verlangt die Liste einen höchstens 10 Minuten alten Signierzeitpunkt.
  Die Prüfung schützt gegen Transport und Fehler im Vermittler, nicht gegen
  seine Kompromittierung. Zugesagt ist nur, dass der Vermittler über den
  Abholer weder an den Deploy-Key noch an den Ausgang kommt. Technischer
  Vertrag und Belege: [CLAUDE.md](CLAUDE.md#speicherpins-1277-s4b).
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
- **Schlüssel und Pins:** JanuaPort legt sie selbst ab — `runtime-key`, `pins.json` und gegebenenfalls `desktop-pins.json` in
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
- **Netz:** Der Vermittler hängt nur am Netz `jnpt_plugins`, das die Kern-Compose
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
  `pins.json` ab — ohne Token, ohne Eingabe, ohne Secret. Für lokale Skript-Pins meldet der Läufer
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
docker run --rm -v "$PWD":/repo -w /repo/script-runner golang:1.26.10 go test ./...
sh script-runner/probe/run.sh    # Docker-Sonden gegen den echten Stack (lädt numpy/pandas als Wheels per pip)
sh script-runner/probe/measure-libs.sh  # Messung: Bibliotheken im Sandbox-Image
```

Einzelheiten für Entwickler: `CLAUDE.md`. Änderungen: `CHANGELOG.md`.

## Browser-Apps mit `apphost`

Das optionale Compose-Profil `apps` startet `script-runner-apphost` aus demselben
Image. Der Dienst liefert ausschließlich statische Browser-Dateien aus dem
Live-Paket `apps/<app>/` des Gateways. Python und Git werden dabei nicht gestartet.
Server-Code, PNG und ICO gehören nicht zum Browser-App-Format v1; SVG ist möglich.
Der HTML-Tokenizer stammt aus `golang.org/x/net/html` (BSD-3-Clause).

Für den Betrieb sind drei Werte erforderlich:

| Variable | Inhalt |
|---|---|
| `JNPT_APPS_DOMAIN` | Basissuffix ohne `apps.`, beispielsweise `example.test` |
| `JNPT_APPS_GATEWAY_URL` | URL des **privaten** App-Listeners im Netz `jnpt_apps`, ohne Pfad; niemals der öffentliche Gateway-Listener |
| `JNPT_GATEWAY_PUBLIC_URL` | öffentliche HTTPS-Origin für `/app-login/start`, ohne Pfad |

Die Vault-Stelle `plugin-apphost` wird vom Kern als
`/run/jnpt/plugins/apphost/runtime-key` materialisiert. Der einzige Mount ist
`/run/jnpt/plugins/apphost:/run/secrets/jnpt:ro`; das Verzeichnis und der Schlüssel
müssen für UID 65532 lesbar sein. Kein Schlüssel in Compose, Umgebungsvariablen
oder Aufrufargumenten. Jeder HTTP-Aufruf liest die Schlüsseldatei erneut.
Ein fehlender oder ungültiger Schlüssel verhindert den Start bzw. die Auslieferung.

Der Kern legt das interne Netz `jnpt_apps` an; Apphost hängt ausschließlich daran.
Vermittler, Abholer, Sandbox und Init hängen nie daran. `read_only`, `cap_drop: ALL`,
`no-new-privileges` und UID 65532 sind Pflicht; weder Host-Port noch Store,
Deploy-Key, Socket oder zusätzlicher Mount sind vorgesehen. Die Box-Kopie
`box/docker-compose.script-runner.yml` ist bytegleich und wird zusammen mit dem
Läufer-Paket einschließlich `seccomp/` in dessen Installationsordner verwendet.
Die Box-Gateway-Compose und deren Listener-/Subnetzprüfung werden im Kern gepflegt.

Die beiden Caddy-Dateien werden gemeinsam aktiviert: `apps.caddy` liegt unter
`apps.d/`, `apps-global.caddy` unter `apps-global.d/`. Die Haupt-Caddyfile importiert
`apps-global.d/*.caddy` innerhalb des globalen Optionsblocks und `apps.d/*.caddy`
auf Site-Ebene. Ohne Apps bleiben beide Verzeichnisse leer. Die Box-Kopien liegen
unter `box/`; nur der Site-Block unterscheidet sich durch `tls internal`.
Der benannte Access-Logger `app_login_log` entfernt `code`, `verifier`, `token`
und `c` aus der URI und verwirft Request-/Response-Header im Log, einschließlich
des Session-Set-Cookie am Callback. Ein expliziter Fehlerhandler verhindert,
dass bei ausgefallenem Upstream die Callback-URI im Standard-Fehlerlog landet.

Öffentlich ausgestellte On-Demand-Zertifikate veröffentlichen App-Namen in
öffentlichen Certificate-Transparency-Logs (CT-Logs). App-Namen dürfen deshalb
keine Kunden-, Personen- oder Projektnamen enthalten; neutrale technische Namen
verwenden. Die Box mit `tls internal` veröffentlicht keine Namen in CT-Logs.

Caddy erreicht ausschließlich den Browser-Port `:8080`. Das interne Relay
`:8081/tls-ask` ergänzt den Bearer für `/internal/apps/tls-ask` und bestätigt nur
HTTP 200 des privaten Gateways. Ungültige Domain-Syntax ergibt 403 ohne Gateway-Aufruf;
weitergereicht wird ausschließlich der geprüfte Parameter `domain`. Browser-Pfade unter `/internal/` und `/tls-ask`
liefern 404. DNS benötigt gewöhnlich `*.apps.<domain>`; einzelne A-Records je App
sind ebenfalls möglich. Auf der Box: Wildcard im LAN-DNS und `tls internal`.

Der Paketabruf nutzt `GET /internal/apps/package?app=<name>` mit dem Apphost-Bearer.
ETag und `If-None-Match` tragen den Commit in Anführungszeichen. Jede Fassung wird
über `gitobjects.Objects` einschließlich `Verify` vollständig an Commit und Pfad
gebunden; es gibt kein Dateisystem-Checkout. Pro App bleibt eine Fassung im RAM,
frühestens fünf Sekunden nach Abschluss des Abrufs wird nachgefragt. Je App läuft
höchstens ein Abruf gleichzeitig; wartende Anfragen teilen sein Ergebnis und können
einzeln abbrechen, auch der erste Anfrager. Der gemeinsame Flight verwendet
`context.WithoutCancel` und eine eigene feste Frist von 30 Sekunden; ein
Anfrageabbruch erzeugt kein Fehlerergebnis im Cache.
Andere Apps und Sitzungsprüfungen warten nicht auf diesen Abruf.
304 erhält die Fassung, 404 verwirft sie,
Transport-/Prüffehler sperren die Auslieferung. Ein Neustart leert alle Caches.

Das komplette Paket muss die Ladeprüfung bestehen: höchstens 4 MiB Inhalt und
1000 Dateien, nur Modus `100644`, verpflichtende `index.html`, feste MIME-Liste
(HTML, CSS, JavaScript/MJS, SVG, JSON/Map/Webmanifest und Text). Inline-Scripts,
Event-Attribute, `javascript:` und die eigene kleine Secret-Musterliste sperren
alle Dateien der Fassung. `jnpt.app.json` wird mitgeprüft, aber niemals ausgeliefert.

Nur `<dns-label>.apps.<domain>` wird bedient. Dateien verlangen eine vom Apphost
registrierte, noch gültige Sitzung; bloßes Erfinden eines Session-Cookies genügt
nicht. Navigation ohne Sitzung startet die Anmeldung, Assets antworten mit 401.
Die Anmeldung ist zustandslos: Der Verifier steht ausschließlich im sicheren
HttpOnly-Login-Cookie mit fünf Minuten Laufzeit (`SameSite=Lax`), ohne Login-Tabelle
im Apphost. Ein Callback ohne Cookie oder mit ungültiger Verifier-Form löst keinen
Exchange aus; die App stammt ausschließlich aus dem Host. Der Kern prüft die
Bindung des Codes an App und S256(Verifier), seine 60 Sekunden Laufzeit und die
einmalige Verwendung. Das Login-Cookie wird bei jedem Callback gelöscht. Das Sitzungscookie ist
HttpOnly, Secure, `SameSite=Strict`, und lebt höchstens `max_age` bzw. bis
`expires_at`. Die Antwort führt mit einer skriptfreien Meta-Refresh-Seite zurück.
Ein Apphost-Neustart verwirft bestehende Sitzungen; ein bereits begonnener Login
kann mit seinem Cookie abgeschlossen werden. Die Sitzungstabelle hält höchstens
4096 Einträge. Nach dem Entfernen abgelaufener Einträge verdrängt eine neue Sitzung
bei voller Tabelle die Sitzung mit dem frühesten Ablauf; sie sperrt keine Anmeldung.
Beide Cookies tragen kein
`Domain`-Attribut und gelten damit nur auf ihrem jeweiligen Host. Der Verifier
besteht aus 32 Byte `crypto/rand`, base64url-kodiert. `SameSite=Strict` ist eine
zweite Schicht: Apps und Gateway sind regelmäßig same-site, deshalb trennt es
weder Apps untereinander noch Apps vom Admin. Die Grenze bilden eigener Origin,
`X-Jnpt-App: 1`, CSP und das vollständige Fehlen von `Access-Control-*`-Antwortheadern,
auch bei Gateway-Antworten. Apps-Hosts gehören nie in eine Admin-Trusted-Origin-Liste.

`POST /mcp` und `POST /_jnpt/logout` verlangen den eigenen HTTPS-Origin und
`X-Jnpt-App: 1`. Der Proxy akzeptiert höchstens 8 MiB, setzt den App-Bearer selbst
und übernimmt nur MIME-/MCP-Protokollheader. Browser-Cookies, Authorization,
Forwarded- und X-Jnpt-Header gehen nicht weiter; Gateway-Cookies kommen nicht
zurück. 401 löscht die lokale Sitzung und das Cookie. Logout widerruft über
`exchange {revoke}`. Es gibt keine Gateway-Redirects und keinen Umgebungsproxy.
Logs enthalten keine Cookies, Tokens, Codes, Verifier oder Query-Werte.

Alle Antworten tragen `no-store`, `nosniff`, `no-referrer` und die feste CSP:

```text
default-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; object-src 'none'; form-action 'self'
```

Beide Listener und der ausgehende Dialer setzen `SetMultipathTCP(false)`; der
Dienst setzt zusätzlich `GODEBUG=multipathtcp=0`. Der Start erfolgt in exec-Form
direkt auf das Go-Binary. Python und Shell bleiben als Restrisiko des gemeinsamen
Images vorhanden, bekommen aber keine zusätzlichen Netze oder Mounts.
Die CI prüft das aus dem gebauten Image entnommene Binary mit
`govulncheck -mode binary`; Basis-Digests und `apt-get upgrade` bleiben Pflicht.

Die vollständige Caddy-/Netzprobe läuft unter Windows über
`pwsh -File script-runner/probe/apphost.ps1` (zuvor Image `k4-apphost:1048` bauen).
Sie startet ausschließlich eigene benannte Docker-Container und räumt sie ab.
