# CLAUDE.md — `script-runner` (Beisteller: Skript-Läufer)

Für Agenten, die an diesem Ordner arbeiten. Die Anleitung für Betreiber steht
in `README.md` — hier steht, warum das Ding so aussieht, wie es aussieht.

## Verantwortung

Der Skript-Läufer führt **gepinnte Python-Skripte** aus einem Git-Repository
des Kunden aus, ausgelöst von einer KI über JanuaPort. Er läuft als Beisteller
neben dem Gateway; das Gateway selbst führt nie Code aus (R1). Entstanden in
JanuaPort/januaport#928 (Paket P2). Verbindlich sind der **Draht-Vertrag**
(Fassung 1 + Fassung 2 + Nachtrag 2a in JanuaPort/januaport#928) und das
Bedrohungsmodell von SEC (T1–T12, R1–R13, M1–M7, V1–V3); die Nummern in
Kommentaren und Tests verweisen darauf.

**Ein statisches Go-Binary, ein Image, drei Dienste** (Unterbefehle):

| Unterbefehl | Dienst | Verantwortung |
|---|---|---|
| `mediator` | `script-runner` | vermittler: MCP-Server für das Gateway (Weg A, `:8090` im Netz `jnpt_default`), prüft `runtime-key` (R13), liest `pins.json`, einzige Senke für Code (R6, M5), hält den einen Slot, misst Wandzeit und Größen selbst (M2, M4), ruft innere Werkzeuge mit dem Lauf-Token (Weg B), `/healthz` auf `:8091` |
| `guard` | `script-runner-sandbox` | Wächter: PID 1 der Sandbox, `PR_SET_DUMPABLE 0`, meldet `hello`, nimmt **genau einen** job, startet `python -I`, räumt den Prozessbaum ab (V1/V3), hält die Instanz 10,5 s (B1) |
| `fetcher` | `script-runner-fetcher` | abholer: holt gepinnte Commits per ssh in den Bare-Store, prüft `wanted/` und `repo_url` selbst (V2, M7) |

**Nicht hier, mit Absicht:** Katalogeintrag, Release- und Signierstrecke,
Host-Units/tmpfiles (P4, Gateway-Repo — hier nur Beispiele unter `deploy/`),
Pin-Speicher, Lauf-Token, Relay, Audit, `scan` (P1/P3, Gateway), Rezept (P5).
Kein pip im Lauf, keine zweite Sprache, keine Netzfreigabe je Skript, kein
Webhook, keine GUI. Es gibt keinen Weg zurück zum Git-Anbieter außer `fetch`.

## Aufbau

| Pfad | Inhalt |
|---|---|
| `main.go` | Unterbefehle, feste Orte im Container |
| `internal/contract` | Draht-Vertrag: Namen, Ende-Klassen, feste Sätze, `tools/list`, Header-Prüfung |
| `internal/frame` | Rahmenprotokoll (Weg C): 4 Byte Länge BE + JSON |
| `internal/gitref` | Allowlist-Prüfer: Commit (40/64 Hex), ssh-URL (M7), Pin-Pfad, Pin-Name |
| `internal/pins` | Leser für `pins.json` (Nachtrag 2a) |
| `internal/manifest` | Leser für `jnpt-script.yaml` |
| `internal/store` | Senke: Tree-Walk mit Hash-Nachrechnung jedes Objekts (M5) |
| `internal/mediator` | vermittler: MCP, Auth, Pins, Slot/V1, Lauf, Weg B |
| `internal/guard` | Wächter (`guard_linux.go`; anderswo nur ein Platzhalter) |
| `internal/fetcher` | abholer |
| `internal/hygiene` | Wächter-Tests für Compose (Vertrag §6, SEC-Zeile 3), Seccomp-Profil, Dockerfile |
| `sandbox/bootstrap.py` | Python-Seite: Modul `januaport`, Fehlermeldung ohne Text |
| `seccomp/` | Profil, Quelle (moby) und Herleitung `derive.py` |
| `contract/golden/` | Golden-JSON für §2–§4 — P3 kopiert sie unverändert ins Gateway-Repo |
| `probe/` | Docker-Sonden (Build-Tag `docker`), Test-Überlagerung, SSH-Git-Anbieter |
| `deploy/` | Beispiele: tmpfiles-Zeilen, Host-Deskriptor |

## Draht-Vertrag (Kurzfassung, maßgeblich ist das Ticket)

- **Weg A** Gateway → vermittler: `POST http://script-runner:8090/mcp`,
  `Authorization: Bearer <runtime-key>`. MCP stateless, JSON-Antworten.
  `tools/list` und `tools/call` beantwortet eine Receiving-Middleware selbst
  (die Werkzeuge hängen an den Pins, nicht an einer festen Registrierung).
- **`tools/list`:** je `ready`-Pin ein Werkzeug (Name = Pin-Name,
  `_meta["januaport.ai/script"]`), auf dem Ergebnis
  `_meta["januaport.ai/runner"]` mit `contract: 1`, `isolation` und **allen**
  Pins samt Zustand. Ist `isolation` `invalid`, erscheint kein Werkzeug und
  jeder sonst bereite Pin als `pending` (§5: „dann ist kein Pin ready“).
- **`tools/call`:** Header `X-Jnpt-Run-Token` (`jnptrun_` + 43 base64url),
  `X-Jnpt-Run-Id` (UUIDv4), `X-Jnpt-Pin-Commit`. Fehlen Token oder ID oder sind
  sie falsch geformt → JSON-RPC-Fehler (Protokollverstoß). Alles Fachliche als
  `CallToolResult` mit `_meta["januaport.ai/run"]` und festem Satz. Reihenfolge
  der Ablehnungen: `refused/pin` → `refused/commit` → `refused/state` →
  `refused/input` → `busy`.
- **Weg B:** eine MCP-Sitzung je Lauf an `JNPT_GATEWAY_URL/mcp` mit
  `Authorization: Bearer <Lauf-Token>`. Das Token verlässt den vermittler nie
  Richtung Sandbox.
- **Weg C** (plugin-intern): Unix-Socket `/run/script-runner/sandbox.sock`.
  Wächter → vermittler: `hello{instance,isolation,seccomp}`,
  `tool_call{id,name,arguments}`, `output{data}`,
  `exited{code,signal?,error_type?,error_at?}`. vermittler → Wächter:
  `job{files:[{path,data_b64}],entry,input}`, `tool_result{id,result}`.
- **Weg D** (plugin-intern): Volume `/var/lib/script-runner` mit `wanted/<sha>`
  und `repo_url` (schreibt der vermittler), `store.git`, `store.url`,
  `failed/<sha>`, `known_hosts` (schreibt der abholer).

## Manifest `jnpt-script.yaml` (Vertrag §10: P2 legt die Felder fest)

| Feld | Pflicht | Regel |
|---|---|---|
| `name` | ja | gleich dem Pin-Namen aus `pins.json` (`[a-z0-9_]{1,48}`) |
| `description` | ja | 1–1024 Zeichen; die KI wählt danach aus |
| `entry` | ja | relativer Pfad zu einer `.py`-Datei im Pin-Ordner |
| `input_schema` | ja | JSON-Schema als YAML, `type: object` |
| `tools` | ja (darf leer sein) | qualifizierte Werkzeugnamen `^[a-z][a-z0-9_-]{0,127}$`, keine Dopplung, nie `skript_*`/`admin_*`; Basis-Werkzeuge (`catalog`, `memory_*` …) sind zulässig und müssen deklariert sein |
| `timeout_s` | ja | 1–25 |

Unbekannte Felder machen das Manifest ungültig (`manifest_invalid`). Ob ein
deklariertes Werkzeug existiert, prüft nur das Gateway (§2c); der vermittler
prüft die Form (`tool_not_qualified`) und lässt im Lauf nur deklarierte
Werkzeuge durch (M1, zweite Linie).

**Python-Seite:** Eingabe als JSON auf stdin und als `januaport.input`,
Ausgabe auf stdout, Werkzeuge per `januaport.call(name, arguments)` → dict
(`CallToolResult`), `januaport.ToolError` bei `isError`. `sys.path` = Pin-Ordner
und `vendor/` darin (reines Python, R12).

## Invarianten (die tragen die Sicherheit)

1. **V1 — höchstens eine Sandbox-Verbindung.** `sandboxes.accept` nimmt eine
   neue Verbindung erst, wenn die vorige EOF geliefert hat; jede weitere wird
   sofort geschlossen, ein laufender Lauf endet `limit/sandbox_lost`. Der
   Wächter schließt erst, wenn der Prozessbaum tot ist (Reihenfolge `finish`:
   exited → kill(-1) → waitpid → Restprozesse? → close → Frist). **Nie** eine
   Zeitgrenze einbauen, nach der der vermittler ohne EOF neu annimmt — das wäre
   genau das V1-Fenster.
2. **M2 — der vermittler misst selbst.** Wandzeit mit eigener Uhr ab dem
   Senden des jobs; `exited` zählt nur für ok/error innerhalb der Wandzeit.
   Der Wächter setzt `PR_SET_DUMPABLE 0` als erste Handlung und bedient nur
   als PID 1.
3. **M4 — eine Verbindung = ein Lauf; Größen beim Lesen.** Rahmen ≤ 1 MiB +
   4 KiB, Ausgabe ≤ 1 MiB, ≤ 50 Werkzeugaufrufe. Nach dem Ende hängt der Lauf
   ab (`detach`), jeder weitere Rahmen wird verworfen, der Wächter bekommt EOF.
   Socket-Ordner in der Sandbox read-only.
4. **M5 / R6 — eine Senke.** Code kommt nur aus `store.Load`: Tree-Walk aus der
   Objekt-DB, jeder Hash (Commit, Bäume, Blobs) selbst nachgerechnet, 120000 /
   160000 / unzulässige Namen abgewiesen — vor **jeder** Übergabe neu, nicht
   aus dem Zustands-Cache. Ohne Pin kein Lauf.
5. **M6 — nie Text aus der Sandbox.** stderr des Skripts geht nach
   `/dev/null`. Die KI bekommt nur feste Sätze, `error_type` (Regex-geprüft)
   und `error_at` (nur `<Datei im Pin>:<Zeile>`, sonst weg). Ausgabe nur bei
   `ok`. Der vermittler loggt nur Zählwerte und feste Codes — nie Argumente,
   Ausgabe, Fehlertexte oder git-stderr.
6. **S2 — fail-closed.** Nur `hello` mit `seccomp: 2` und bekannter Isolation
   kommt in den Slot; sonst `isolation: invalid`, kein Werkzeug, `/healthz` 503.
7. **V2 / M7 — der abholer vertraut niemandem.** Jede `wanted/`-Zeile und die
   `repo_url` prüft er selbst (Allowlist), bevor git läuft; git nur mit
   `protocol.allow=never`, `protocol.ssh.allow=always`, `transfer.fsckObjects`,
   ohne Hooks und Submodule; Aufruf `git fetch origin -- <sha>`.
8. **R8 — kein Weg zurück.** Kein `push`, kein `receive-pack` im Code
   (Wächter `TestNoPushPathInSource`). Der Deploy-Key hängt nur im abholer.
9. **Compose ist Vertrag §6.** `internal/hygiene` prüft jede Zeile samt
   Negativproben; eine Änderung dort ist eine Vertragsfrage.

## Bewusste Design-Entscheidungen

- **Middleware statt registrierter Werkzeuge.** Das SDK beantwortet einen
  unbekannten Werkzeugnamen mit einem JSON-RPC-Fehler; der Vertrag verlangt
  `refused/pin` als Ergebnis. Die Middleware beantwortet `tools/list` und
  `tools/call` vollständig selbst; das SDK macht nur Transport und Protokoll.
- **Stateless-MCP.** Das Gateway öffnet je Aufruf eine frische Sitzung; der
  vermittler hält keinen Sitzungszustand.
- **`pins.json` und Schlüssel bei jedem Abgleich lesen** (2 s). Das deckt „bei
  jeder Änderung“ und „alle 60 s“ ab und macht einen Neustart nach einem
  Schlüsselwechsel überflüssig. Die Prüfung an der Senke wird je
  (Name, Pfad, Commit) gecacht; vor jedem Lauf wird trotzdem neu gerechnet.
- **Eigene stdout-Pipe im Wächter** statt `StdoutPipe`: `cmd.Wait` schlösse
  sonst das Leseende, bevor die Ausgabe gelesen ist.
- **OOM = SIGKILL ohne Zutun.** Der vermittler sieht die cgroup der Sandbox
  nicht; ein Skript, das an SIGKILL stirbt, ohne dass der Wächter getötet hat,
  endet als `limit/memory`.
- **`error_at` repo-relativ** (`<pin-pfad>/<datei>:<zeile>`), damit die KI die
  Stelle im Repository direkt findet.
- **Health-Port 8091 im Container auf allen Adressen, auf dem Host nur
  Loopback** (Muster Tunnel-Client). Der Aktuator braucht eine Host-URL; aus
  `jnpt_default` ist `/healthz` damit ebenfalls erreichbar — es verrät nur
  „bereit/nicht bereit“.
- **Nutzer 65532 für vermittler und abholer:** Die Plugin-Ordner auf dem Host
  gehören 65532 mit 0750; `cap_drop: ALL` nimmt root die Leserechte. Die
  Sandbox läuft als 65533.
- **TOFU für den Host-Schlüssel** (`StrictHostKeyChecking=accept-new`,
  `known_hosts` im Store). Die Integrität hängt am Commit-Hash und an der
  Nachrechnung in der Senke, nicht am Kanal.

## Stolperfallen (aus der Messung P0 und dieser Umsetzung)

- **S2:** runsc ignoriert das Seccomp-Profil ohne `--oci-seccomp` (Modus 0).
- **runsc braucht `--host-uds=open`** (gemessen hier, P2): Ohne das Flag
  verweigert gVisor die Verbindung zu einem Unix-Socket, den ein Prozess
  außerhalb der Sandbox angelegt hat (`ECONNREFUSED`) — die Sandbox kommt nie
  in den Slot. Vertrag §6 nennt nur `--oci-seccomp`; Änderung ist gemeldet.
- **Seccomp 2 ist unter Docker Desktop kein Beweis für das eigene Profil:**
  Dort meldet auch `seccomp=unconfined` Modus 2 (der Container erbt einen
  Filter der VM). Unter runc auf einem normalen Host und unter runsc ist
  unconfined = 0.
- **G1:** gVisor verbraucht selbst Host-PIDs; eine Fork-Bombe endet dort als
  `limit/sandbox_lost`, unter runc als EAGAIN (`error/exception`,
  `BlockingIOError`). Deckel 128.
- **G2:** tmpfs ohne `mode/uid/gid` ist nach einem Neustart root:755.
- **B1:** dockerd verdoppelt den Restart-Backoff für Instanzen < 10 s; der
  Wächter hält jede Instanz ≥ 10,5 s ab Start. Folge: ≈ 6 Läufe je Minute in
  Folge, Aufrufe dazwischen bekommen `busy` (Grenze v1).
- **gVisor und `/proc/1/fd`:** Das Auflisten der fd-Nummern von PID 1 ist unter
  runsc trotz `PR_SET_DUMPABLE 0` möglich, das Öffnen nicht (M2 hält).
- **`restart: always` startet denselben Container neu** (M3): „frisch“ heißt
  „neu gestartet mit leerem tmpfs“.
- **CRLF:** `.gitattributes` erzwingt LF; `run.sh`, Dockerfiles und Golden-Dateien
  brechen sonst in einem Windows-Checkout.

## Prüfen

```sh
# Unit-Tests (in einem Linux-Container; auf Windows hängt lokales go test)
docker run --rm -v "$PWD":/src -w /src golang:1.25.13 go test ./...
python3 seccomp/derive.py --check
# Docker-Sonden gegen den echten Stack (Präfix p2-928-, räumt selbst auf)
sh probe/run.sh
```
