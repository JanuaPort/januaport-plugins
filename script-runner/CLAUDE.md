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
| `mediator` | `script-runner` | vermittler: MCP-Server für das Gateway (Weg A, `:8090` im internen Netz `jnpt_plugins`), prüft `runtime-key` (R13), liest `pins.json`, einzige Senke für Code (R6, M5), hält den einen Slot, misst Wandzeit und Größen selbst (M2, M4), ruft innere Werkzeuge mit dem Lauf-Token (Weg B), `/healthz` auf `:8091`; Taktgeber für Zeitpläne (#1039 S1, liest `schedules.json`, löst über das Gateway aus) |
| `guard` | `script-runner-sandbox` | Wächter: PID 1 der Sandbox, `PR_SET_DUMPABLE 0`, meldet `hello`, nimmt **genau einen** job, startet `python -I`, räumt den Prozessbaum ab (V1/V3), hält die Instanz 10,5 s (B1) |
| `fetcher` | `script-runner-fetcher` | abholer: prüft Pins gegen den Standard-Zweig und holt Objekte per ssh in den Bare-Store, prüft `wanted/` und `repo_url` selbst (V2, M7) |

**Nicht hier, mit Absicht:** Katalogeintrag und Signatur (die liegt auf dem
Katalog), Host-Units/tmpfiles (P4b, Gateway-Repo — hier nur Beispiele unter
`deploy/`),
Pin-Speicher, Lauf-Token, Relay, Audit, `scan` (P1/P3, Gateway), Rezept (P5).
Zeitplan-Verwaltung, Fälligkeit, Eingabe und Identity eines Zeitplans
(#1039, Kern); hier nur der Taktgeber.
Kein pip im Lauf, keine zweite Sprache, keine Netzfreigabe je Skript, kein
Webhook, keine GUI. Der Git-Anbieter wird ausschließlich mit `ls-remote`
und `fetch` gelesen.

## Aufbau

| Pfad | Inhalt |
|---|---|
| `main.go` | Unterbefehle, feste Orte im Container |
| `internal/contract` | Draht-Vertrag: Namen, Ende-Klassen, feste Sätze, `tools/list`, Header-Prüfung |
| `internal/frame` | Rahmenprotokoll (Weg C): 4 Byte Länge BE + JSON |
| `internal/gitref` | Allowlist-Prüfer: Commit (40/64 Hex), ssh-URL (M7), Pin-Pfad, Pin-Name |
| `internal/pins` | Leser für `pins.json` (Nachtrag 2a) |
| `internal/schedules` | Leser für `schedules.json` (#1039): streng, fail-closed, höchstens 20, Cron wie der Kern (`robfig/cron/v3`), `time/tzdata`, Sommerzeit-Lücken |
| `internal/ticker` | Taktgeber (#1039 S1): reine Logik — fällige Termine, Merker, `missed`/`skipped_busy`, gespiegelte Pausen, `_meta.schedules` |
| `internal/manifest` | Leser für `jnpt-script.yaml` |
| `internal/store` | Senke: Tree-Walk mit Hash-Nachrechnung jedes Objekts (M5) |
| `internal/mediator` | vermittler: MCP, Auth, Pins, Slot/V1, Lauf, Weg B |
| `internal/guard` | Wächter (`guard_linux.go`; anderswo nur ein Platzhalter) |
| `internal/fetcher` | abholer |
| `internal/hygiene` | Wächter-Tests für Compose (Vertrag §6, SEC-Zeile 3), Seccomp-Profil, Dockerfile |
| `sandbox/bootstrap.py` | Python-Seite: Modul `januaport`, Fehlermeldung ohne Text |
| `seccomp/` | Profil, Quelle (moby) und Herleitung `derive.py` |
| `contract/golden/` | Golden-JSON für §2–§4 — P3 kopiert sie unverändert ins Gateway-Repo; dazu `tools_list_schedules.json` (S1 → Gateway-Repo) und `script-runner-schedules.golden.json` (unverändert aus dem Gateway-Repo) |
| `probe/` | Docker-Sonden (Build-Tag `docker`), Test-Überlagerung, SSH-Git-Anbieter |
| `deploy/` | Beispiele: tmpfiles-Zeilen, Host-Deskriptor (echtes `IMAGE_REPO`, runtime-key in `REQUIRE`), `.env.example`, `daemon.json.example` |

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
  Mit Zeitplänen trägt `_meta["januaport.ai/runner"]` zusätzlich `schedules`
  (#1039, siehe „Taktgeber“); ohne Zeitpläne fehlt der Schlüssel.
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
  `exited{code,signal?,error_type?,error_at?}`. `hello` trägt seit Fassung 3
  `profile` (`jnpt`|`other`, Golden `contract/golden/frame_hello.json`). vermittler → Wächter:
  `job{files:[{path,data_b64}],entry,input}`, `tool_result{id,result}`.
- **Weg D** (plugin-intern): Volume `/var/lib/script-runner` mit `wanted/<sha>`
  und `repo_url` (schreibt der vermittler), `store.git`, `store.url`,
  `approved/<sha>` (Inhalt: geprüfte Repository-URL), `failed/<sha>` (Inhalt:
  Grund-Code), `known_hosts` (schreibt der abholer). Marken werden atomar ersetzt;
  Fehler haben Vorrang vor Freigaben. Alte leere Fehler-Marken bleiben
  `fetch_failed`; alte Store-Objekte ohne Freigabe bleiben bis zur Prüfung `pending`.
  Der vermittler veröffentlicht `repo_url` atomar, bevor er neue
  `wanted/<sha>`-Dateien anlegt. Scheitert das Schreiben oder Umbenennen der
  URL, legt dieser Abgleich keine neuen Wunschdateien an. Der abholer kann
  bereits auf die erste sichtbare Wunschdatei reagieren; eine noch leere
  oder alte URL würde sonst die Prüfung vor jedem Git-Aufruf scheitern lassen
  (#1085, `TestWantedWaitsForRepositoryURLPublication`).
- **LEAD Delta v2 / SEC L3 (#1083):** Ein Pin außerhalb der Abstammung des
  Remote-Standard-Zweigs meldet `invalid/not_on_default_branch`.
  Auflösungs-, Fetch- und Git-Prüffehler melden bei noch nicht freigegebenen
  Commits `invalid/fetch_failed`. Freigaben für dieselbe `repo_url` bleiben
  bei einem Ausfall erhalten, mit einer Warnung ohne URL oder Fehlertext.
  Nur ein negativer Vorfahren-Befund (Exit 1) widerruft die Zweigfreigabe;
  andere Git-Prüffehler liefern keine neue Erkenntnis. Ein Wechsel der
  `repo_url` leert die Freigaben weiterhin.
  Der Draht-Vertrag bleibt Fassung 1; nur der Grund-Wert
  `not_on_default_branch` kommt hinzu (`ReasonNotOnDefaultBranch`). Das Gateway
  reicht Gründe als Text durch und hat keine geschlossene Liste.

## Manifest `jnpt-script.yaml` (Vertrag §10: P2 legt die Felder fest)

| Feld | Pflicht | Regel |
|---|---|---|
| `name` | ja | gleich dem Pin-Namen aus `pins.json` (`[a-z0-9_]{1,48}`) |
| `description` | ja | 1–1024 Zeichen; die KI wählt danach aus |
| `entry` | ja | relativer Pfad zu einer `.py`-Datei im Pin-Ordner |
| `input_schema` | ja | JSON-Schema als YAML, `type: object` |
| `tools` | ja (darf leer sein) | qualifizierte Werkzeugnamen `^[a-z][a-z0-9_-]{0,127}$`, keine Dopplung, nie `script_*`/`admin_*` (Vertrag F4: das Gateway registriert `script_<name>`); Basis-Werkzeuge (`catalog`, `memory_*` …) sind zulässig und müssen deklariert sein |
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
   `TestNewConnectionOnlyAfterEOF` und
   `TestV1SecondConnectionLosesRunAndNextJobGoesToFreshInstance` prüfen die
   Ablehnung einer weiteren Verbindung. Vor der nächsten Instanz wartet
   die Testhilfe auf den EOF-Kanal und die Freigabe von `sandboxes.current`
   unter dessen Mutex, bevor sie die nächste Verbindung öffnet; das lokale
   Schließen des Fake-Wächters allein bestätigt die Freigabe noch nicht.
   `TestOversizedFrameLimitAndMediatorSurvives` wartet ebenso auf EOF und
   Slot-Freigabe, bevor er nach dem Rahmenfehler die nächste Instanz verbindet.
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
6. **S2 / B2 — fail-closed.** Nur `hello` mit `seccomp: 2` **und**
   `profile: "jnpt"` und bekannter Isolation kommt in den Slot; sonst
   `isolation: invalid`, Pins `pending`, kein Werkzeug, `/healthz` 503.
   `profile` ist der Selbsttest des Wächters vor `hello`:
   `process_vm_readv(0, NULL, 0, NULL, 0, 0)` → nur **genau EPERM** ergibt
   `jnpt` (EINVAL = Docker-Standard, der Aufruf erreicht den Kernel; Erfolg
   oder ENOSYS = kein bzw. fremder Filter). Es ist der eine Syscall, den unser
   Profil über den Standard hinaus sperrt (Messung S1). Modus 2 allein beweist
   nur irgendeinen Filter.
7. **V2 / M7 / L3 — der abholer vertraut niemandem.** Jede `wanted/`-Zeile
   und die `repo_url` prüft er selbst (Allowlist), bevor Git läuft. Jeder
   gewünschte Commit muss Vorfahr (oder gleich) dem Kopf des
   Remote-Standard-Zweigs sein, für Pins von Menschen und KI gleichermaßen.
   Bei jedem Durchlauf mit gewünschten Commits:
   `git ls-remote --symref origin HEAD` mit Deploy-Key frisch auflösen,
   exakt den angekündigten Kopf-Hash holen und
   `git merge-base --is-ancestor <sha> <kopf>` prüfen. Auch bereits gespeicherte
   und zuvor freigegebene Commits werden geprüft. Kein konfigurierbarer Zweig.
   Kein Vorfahr (Exit 1) → `invalid/not_on_default_branch`, Freigabe widerrufen.
   Ausfall widerruft nicht, nur ein negativer Vorfahren-Befund widerruft.
   Auflösungs-, Fetch- oder Prüffehler (einschließlich Timeout und zu großer
   HEAD-Antwort) → neue Commits `invalid/fetch_failed`, keine neue Freigabe;
   bereits geprüfte Freigaben für dieselbe `repo_url` bleiben erhalten.
   Warnungen tragen weder URLs noch Git-Fehlertexte. Ein Wechsel der
   `repo_url` leert die Freigaben weiterhin. Der Mediator verlangt
   die Freigabe vor seinem Cache und vor jeder Codeübergabe. Die gesamte
   `hardening`-Liste gilt für jeden Git-Aufruf: nur das erlaubte SSH-Protokoll,
   Objektprüfung, keine Hooks und Submodule, `-c gc.auto=0` und
   `-c maintenance.auto=false`. Die beiden Optionen verhindern automatische
   GC und Maintenance: Fetch darf keinen abgelösten Schreiber starten, der
   nach dem Git-Aufruf noch in `objects/` arbeitet und mit dem Entfernen des
   Stores (auch beim Test-Cleanup) konkurriert (#1085). Der abholer führt
   selbst keine GC oder Store-Pflege aus. Fetches ohne Tags und ohne
   Schreiben von `FETCH_HEAD`; kein Checkout. Der Literal-Test
   `TestEveryGitCallDisablesAutomaticMaintenance` prüft die Optionen vor dem
   Unterbefehl für `init`, `config`, `fetch`, `ls-remote`, `cat-file` und
   `merge-base` über den Runner, für SHA-1 und SHA-256.
8. **R8 — kein Weg zurück.** Kein `push`, kein `receive-pack` im Code
   (Wächter `TestNoPushPathInSource`). Der Deploy-Key hängt nur im abholer.
9. **Compose ist Vertrag §6, der Wächter prüft deny by default.**
   `internal/hygiene` dekodiert mit `KnownFields(true)`: Jeder Schlüssel, den
   das Struct nicht kennt, ist ein Verstoß — das Struct kennt genau die
   Schlüssel der ausgelieferten Compose. Zusätzlich sind ausdrücklich
   verboten, auch wenn jemand sie ins Struct aufnimmt: `volumes_from` (würde
   der Sandbox Schlüsselordner und Store einhängen), `userns_mode`/`pid`/`uts`/
   `cgroup` `host` bzw. `container:*`/`service:*`, `ipc` `host`/`shareable`/`container:*`/`service:*`,
   `network_mode` `host`/`container:*`/`service:*`, `devices`, `sysctls`, `extra_hosts`
   (SEC-Zweitprüfung, Bedingung vor dem Merge). Negativproben je Dienst in
   `TestComposeUnknownAndForbiddenKeysPerService`. Wer der Compose einen
   neuen Schlüssel gibt, nimmt ihn bewusst ins Struct auf — das ist eine
   Vertragsfrage.
10. **Der vermittler hat keinen Netzweg außer zum Gateway** (JanuaPort/januaport#1069).
    Er hängt genau am Netz `plugins` = external `jnpt_plugins`, das die
    Kern-Compose mit `internal: true` anlegt und an dem sonst nur `jnpt` hängt.
    Weg A (`script-runner:8090`) und Weg B (`jnpt:8484`) laufen darüber; ins
    Internet und ins LAN gibt es keinen Weg, auch keine Namensauflösung nach
    außen. **Keine Host-Ports**, für keinen Dienst: Ein veröffentlichter Port
    eines Containers, der nur im internen Netz hängt, ist nicht erreichbar
    (SEC-Messung 14.10.), jeder andere wäre ein Weg hinein. Gesundheit deshalb
    über den Docker-Healthcheck (`python3 -I -c …urlopen('http://127.0.0.1:8091/healthz')`,
    alle 10 s, kein neuer Code), den der Aktuator mit `HEALTH_DOCKER=1` liest.
    **Ausgang hat nur der abholer** (eigenes Netz `egress`, git über ssh); er
    hängt nie an `jnpt_plugins` oder `jnpt_default`. Die Sandbox hat
    `network_mode: none`. Jeder andere Dienst hat kein Netz oder nur
    `jnpt_plugins`; ohne Netzliste hinge er am Projekt-Default mit Ausgang.
    Mechanik: `internal/hygiene` (`checkNoEgress`, `checkMediator`,
    `checkFetcher`, Proben in `TestComposeMutationsAreCaught`,
    `TestNoServiceButFetcherHasEgress`), im Kern-Repo der Vorlagen-Wächter
    `cmd/jnpt/compose_plugin_netz_test.go`, und die Sonden
    `probe/network_test.go` (kein Ausgang mit Gegenprobe aus dem abholer,
    `jnpt:8484` erreichbar, genau ein Netz, kein Port, healthy).
    **Reihenfolge auf einer Anlage:** erst das Netz in der Kern-Compose, dann
    Aktuator-Fassung 3, dann diese Compose und der Deskriptor mit
    `HEALTH_DOCKER=1` (JanuaPort `docs/skript-laeufer.md`, „Vor der Einrichtung“).

## Taktgeber (#1039 Paket S1)

Verbindlich ist der Kernvertrag `docs/skript-zeitplaene.md` im Gateway-Repo
(Abschnitt „Vertrag zum S1-Beisteller“), dazu Plan v1, Delta v3 und SEC A1–A5
in JanuaPort/januaport#1039. Der Kern hält Zeitpläne, feste Eingabe und die
gewählte Identity; er entscheidet über Fälligkeit (eigene Uhr, 30 s Toleranz,
`last_fired_slot`) und pausiert selbst. Der Taktgeber im vermittler sagt nur
„Zeitplan X ist fällig, bitte prüfen“.

- **Eingang:** `schedules.json` neben `pins.json`, bei jedem Abgleich (2 s) neu
  gelesen: `{"version":1,"schedules":[{name,pin,commit,cron,timezone,enabled}]}`
  (Golden `contract/golden/script-runner-schedules.golden.json`, unverändert aus
  `cmd/jnpt/testdata/` des Gateway-Repos). Kein Token, keine Eingabe, kein
  Secret. Gelesen werden höchstens 64 KiB (`io.LimitReader`, SEC R4 H1).
  Fehlt die Datei → keine Zeitpläne. Ungültig (größer als 64 KiB, kaputtes JSON,
  unbekanntes oder fehlendes Feld, falsche Version, Daten danach, doppelter
  Name, eine ungültige Zeile) → **keine** Zeitpläne und einmal je Wechsel der
  Code `schedules_invalid`. Mehr als 20 → die ersten 20, Code
  `schedules_over_limit` mit `ignored=<n>`.
- **Cron:** derselbe Parser wie der Kern (`robfig/cron/v3`, fünf Felder,
  Wochentags- und Monatsnamen), Pflicht-Zeitzone (IANA, nie `Local`),
  Mindestabstand fünf Minuten — dieselben Regeln wie `scriptschedule.parse`.
  `time/tzdata` ist eingebettet; das Image bringt keine Zonendateien mit.
- **Auslösen:** nur `enabled:true` und Pin `ready` (wie in `tools/list`, also
  nie bei `isolation: invalid`) mit genau dem Commit des Zeitplans.
  `POST <JNPT_GATEWAY_URL>/api/plugin/script-runner/schedules/{name}/run`,
  kein Body, `Authorization: Bearer <runtime-key>` frisch aus der Datei
  (ohne Schlüssel kein Aufruf), Frist 35 s, keine Umleitung. Jeder Termin in
  einer eigenen Goroutine: Der Abgleich wartet nie auf das Gateway.
- **Antworten:** `200 {"end":…}` → `last_run` (der Termin, UTC) und `last_end`
  (feste Klasse, sonst `unknown`); `busy` zählt `skipped_busy`;
  `paused/token|pin` und `refused/pin|commit|state` spiegeln die Pause des
  Kerns (`state: paused`, `reason`). `409` → nur Code `schedule_refused` mit
  Zähler; Netzfehler, Frist, anderer Status → Code `schedule_failed`. Nie
  eine Wiederholung; der nächste Versuch ist der nächste Termin.
- **Ausgang:** `_meta["januaport.ai/runner"].schedules`, je Zeitplan genau
  `name`, `state` (`ready|paused|disabled`), `reason` (`token|pin|""`),
  `next_run` (nur bei `ready`), `last_run`, `last_end`, `missed`,
  `skipped_busy` — Golden `contract/golden/tools_list_schedules.json`, Form wie
  `scriptschedule.Observation` im Kern ohne das kerninterne `reported`.

**Invarianten (je eine Testzeile):**

1. **V1 bleibt.** Der Taktgeber öffnet keine Sandbox-Verbindung, nimmt keinen
   Slot und schickt nie einen Job; `internal/ticker` kennt weder Netz noch
   Sandbox. Tests: `TestTickerHoldsNoSlotWhileGatewayRunsViaPathA` (der Kern
   ruft während des offenen Aufrufs Weg A — hielte der Taktgeber den Slot,
   käme `busy`), `TestTickerNeverSendsJobToSandbox` (wahrscheinlichste
   Verletzung: Job direkt über Weg C), `TestTriggerWithBusySlotDoesNotBlock`.
2. **Kein Nachholen.** Ausgelöst wird nur der jüngste Termin und nur in den
   ersten 25 s (unter der Kern-Toleranz von 30 s). Ältere zählen in `missed`.
   Termine vor dem ersten Abgleich eines Zeitplans (Neustart) kennt der
   Taktgeber nicht: weder Lauf noch Zählung. `TestNoCatchUpAfterGap`,
   `TestNoCatchUpAfterStart`.
3. **Je Termin höchstens ein Versuch.** Cursor (bis wohin bewertet) plus
   Merker `lastFired`; läuft die Uhr rückwärts, bleibt der Cursor stehen.
   `TestAtMostOneAttemptPerTermin`, `TestClockBackwardsDoesNotRefire`.
4. **Request ohne Body, Bearer = runtime-key, Schlüssel nie im Log.** Der
   Auslöser loggt gar nichts (Fehlertexte von `net/http` tragen die URL);
   geloggt werden nur feste Codes mit Zählern im Taktgeber. Keine Umleitung:
   `CheckRedirect` gibt die erste Antwort zurück, ein 307 ist ein Fehler, der
   Schlüssel erreicht kein zweites Ziel (SEC R4 H2).
   `TestTriggerRequestNoBodyBearerRuntimeKeyNeverLogged`, `TestTriggerFollowsNoRedirect`.
5. **fail-closed beim Lesen** (oben), auch über 64 KiB. `TestParseInvalidFailsClosed`,
   `TestInvalidSchedulesFileFailsClosedWithFixedCode`,
   `TestSchedulesFileOverSixtyFourKiBFailsClosed`.
6. **Nur bereit und aktiv löst aus.** `TestNotEnabledOrPinNotReadyNeverFires`,
   `TestCommitMismatchOrDisabledNeverTriggers`.
7. **Sommerzeit-Lücke:** Termine, die es an diesem Tag nicht gibt (Europe/Berlin,
   letzter Sonntag im März, 02:30), laufen nicht und zählen in `missed`.
   `TestDSTGapNoRunCounted`, `TestDSTGapEuropeBerlin`.
8. **`_meta.schedules` = Kernform, ≤ 20, keine weiteren Felder.**
   `TestSchedulesGoldenMatchesCoreParser`, `TestReportAtMostTwentyAndOnlyContractFields`.
9. **Frist 35 s, kein Wiederholen.** `TestTriggerDeadlineThirtyFiveSecondsNoRetry`.

**Bewusste Entscheidungen und Stolperfallen:**

- **Zustand nur im Speicher.** Kein Zeitplan-Zustand auf dem Volume: Nach einem
  Neustart beginnen `missed`, `skipped_busy`, `last_run` bei null; die
  Wahrheit über den letzten Lauf hält der Kern (`last_fired_slot`).
- **`reason` nur aus Antworten.** Ein Pin, der lokal nicht bereit ist oder
  einen anderen Commit trägt, ergibt `paused` mit leerem Grund — den
  Pausengrund bestimmt der Kern. Eine gespiegelte Pause bleibt, bis der Kern
  den Zeitplan deaktiviert und wieder aktiviert hat (Bestätigung); die
  Auslösung sperrt sie nicht (sonst bliebe sie nach einer schnellen
  Bestätigung hängen) — der Kern antwortet dann 409.
- **Sperr-Reihenfolge:** `toolsList` liest den Bericht des Taktgebers vor
  `m.mu`, `tickSchedules` liest die bereiten Pins vor `Tick`. Taktgeber-Mutex
  und `m.mu` werden nie verschachtelt.
- **Herbst-Rückstellung:** Ein Termin in der doppelten Stunde (02:30 am
  letzten Sonntag im Oktober) ist nach robfig zweimal fällig (00:30 und
  01:30 UTC), beim Kern ebenso. Der Taktgeber löst beide aus, jeden
  höchstens einmal (gemessen, kein eigener Test).
- **Uhr injizierbar** (`Config.Now`), Frist konfigurierbar
  (`Config.TriggerTimeout`, Standard 35 s); die Tests schlafen nicht auf
  Termine, sie setzen die Uhr.

## Ratifizierte Auslegungen (Vertrag Fassung 3)

Diese Auslegungen aus dem Bau von P2 hat LEAD in Fassung 3 bestätigt —
**ratifiziert (F3)**, damit Vertrag:

- `isolation: invalid` → sonst bereite Pins werden als `pending` gemeldet
  (§5 „kein Pin ready“; das Gateway registriert nach §2(a) nichts). —
  ratifiziert (F3)
- `error_at` = `<pin-pfad>/<datei>:<zeile>`, relativ zum Repository. —
  ratifiziert (F3)
- Lauf-Token oder Lauf-ID fehlen oder sind falsch geformt → JSON-RPC-Fehler
  (Protokollverstoß). — ratifiziert (F3)
- `pins.json`: ungültiger Name oder Commit → `manifest_invalid`, ungültiger
  Pfad → `bad_path`. — ratifiziert (F3)
- Grenze v1: gepinnter Ordner ≤ 4 MiB und ≤ 1000 Dateien, sonst
  `manifest_invalid`. — ratifiziert (F3)
- OOM = SIGKILL, das nicht vom Wächter kam → `limit/memory`. — ratifiziert (F3)
- Fork-Bombe: runc → EAGAIN (`error/exception`, `BlockingIOError`), runsc →
  `limit/sandbox_lost`; beides zulässig. — ratifiziert (F3)
- `tools/list` trägt die Cache-Felder des SDK (`ttlMs`, `cacheScope`), das SDK
  setzt `resultType` und die Server-Info in `_meta`; P3 ignoriert sie im
  Abgleich wie der Draht-Test hier. — ratifiziert (F3)
- `/healthz` auf `0.0.0.0:8091` im Container, Host nur `127.0.0.1` — von SEC
  als Grenze angenommen (F3, SEC 5911712051). **Abgelöst durch
  JanuaPort/januaport#1069:** kein Host-Port mehr, `/healthz` nur im Container
  (Healthcheck) und aus dem internen Netz `jnpt_plugins`.

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
  Die Standard-Zweigfreigabe wird vor dem Cache und vor jeder Codeübergabe
  erneut gelesen, auch nach dem Warten auf eine Sandbox. Der Abholer prüft
  alle gewünschten Commits alle 5 s gegen einen frisch aufgelösten Kopf;
  ein Force-Push wird im nächsten erfolgreichen Prüfdurchlauf erkannt.
  Ein Ausfall erhält bisherige Freigaben für dieselbe Repository-URL;
  ausschließlich Exit 1 der Vorfahren-Prüfung widerruft diese Freigabe.
  Ein bereits laufendes Skript wird dadurch nicht nachträglich abgebrochen.
  `RetryAfter` verzögert
  nur fehlgeschlagene Downloads fehlender Wunsch-Objekte, nie die Auflösung
  oder den Fetch des Standard-Zweigkopfs oder die Prüfung gespeicherter Commits.
- **Eigene stdout-Pipe im Wächter** statt `StdoutPipe`: `cmd.Wait` schlösse
  sonst das Leseende, bevor die Ausgabe gelesen ist.
- **OOM = SIGKILL ohne Zutun.** Der vermittler sieht die cgroup der Sandbox
  nicht; ein Skript, das an SIGKILL stirbt, ohne dass der Wächter getötet hat,
  endet als `limit/memory`.
- **`error_at` repo-relativ** (`<pin-pfad>/<datei>:<zeile>`), damit die KI die
  Stelle im Repository direkt findet.
- **Health-Port 8091 im Container auf allen Adressen, ohne Host-Port**
  (seit JanuaPort/januaport#1069; vorher Host-Loopback nach dem Muster des
  Tunnel-Clients). Der Aktuator liest den Docker-Healthcheck statt einer
  Host-URL. **Grenze (von SEC angenommen, F3):** Aus `jnpt_plugins` (also vom
  Gateway) ist `/healthz` erreichbar und verrät nur bereit/nicht bereit —
  Antwort ausschließlich `ok` (200) oder `not ready` (503), jeder andere Pfad
  404 (Test `TestHealthBodiesAndOtherPaths404`).
- **Healthcheck mit `python3` aus dem Image** statt eines Health-Unterbefehls:
  kein neuer Code im vermittler, und das Image hat Python ohnehin (Sandbox).
  `-I` hält Umgebung und Arbeitsverzeichnis heraus. Er läuft als 65532 im
  read-only-Container; dass er kein `.pyc` ablegen kann, stört Python nicht.
- **Nutzer 65532 für vermittler und abholer:** Die Plugin-Ordner auf dem Host
  gehören 65532 mit 0750; `cap_drop: ALL` nimmt root die Leserechte. Die
  Sandbox läuft als 65533.
- **TOFU für den Host-Schlüssel** (`StrictHostKeyChecking=accept-new`,
  `known_hosts` im Store). Die Integrität hängt am Commit-Hash und an der
  Nachrechnung in der Senke, nicht am Kanal.

## Release (P4a)

- **Strecke:** `.github/workflows/script-runner-release.yml` im Repo-Root.
  Auslöser **nur** der Tag `script-runner-vX.Y.Z` (Schema im Job `unit`
  geprüft), den das Quality Gate setzt. Jobs: `unit` → `build` → `probes` und
  `scan` → `publish`.
- **Ein Build, per Digest, ohne Tag:** `build` pusht nur `linux/amd64` mit
  SBOM und Provenance `mode=max` nach `ghcr.io/januaport/script-runner`
  (`push-by-digest`). Erst `publish` setzt den Image-Tag `X.Y.Z` (kein
  `latest`) per `imagetools create` und prüft, dass er auf denselben Digest
  zeigt. Anlagen beziehen das Image nur per Digest aus dem signierten Katalog.
- **SEC C1 — geprüft = ausgeliefert:** `probes` zieht genau den gepushten
  Digest, vergleicht den Config-Digest des lokalen Images (aus `docker save`,
  unabhängig vom Image-Store des Runners — beim containerd-Store ist
  `docker inspect .Id` der Index-Digest, nicht die Config) mit `config.digest`
  der amd64-Plattform im Registry-Index und fährt dann `probe/run.sh` mit
  `P2_IMAGE=<repo>@<digest>`. Die Zusammenfassung des Laufs nennt Digest und
  Lauf-ID; beides gehört in den Kuratierungs-Nachweis (`CHANGELOG.md`).
  Gewählt statt „Build → Sonden → zweiter Build mit Push“, weil ein zweiter
  buildx-Lauf nicht zwingend dasselbe Image erzeugt.
- **SEC C2:** In `build` und `publish` (einzige Jobs mit `packages: write`)
  laufen nur `actions/*` und `docker/*`. Trivy läuft im Job `scan` ohne
  Schreibrecht als Binary mit gepinnter Version und SHA-256, bekommt das Image
  als Datei (kein Token) und legt den Bericht als Artefakt ab — **kein Gate**.
  Alle Aktionen sind per Commit-SHA gepinnt (Kommentar = Fassung).
- **Wächter:** `internal/hygiene/release_test.go` (Auslöser, Rechte je Job,
  Pinning, Build-Parameter, Reihenfolge, E6, Deskriptor, `.env.example`,
  gVisor-Pin). Er liest `.github/workflows/` im Repo-Root — die Unit-Tests
  brauchen deshalb den ganzen Checkout (siehe „Prüfen“).
- **CI-Auslöser (E6):** `script-runner.yml` läuft nur bei Push auf `main` und
  per `workflow_dispatch`; `secret-scan.yml` bleibt auf jedem Push und Pull
  Request (#787, öffentliches Repo).
- **Kuratierung:** Vorlage am Ende von `CHANGELOG.md`; je Image der benötigte
  Commit von Compose und Profil (E3) und die Lauf-ID der Sonden (C1).

## Messung #1041 P0 (Python-Bibliotheken: numpy und pandas)

Gemessen am 15.10.2026 mit `probe/measure-libs.sh` (Messkörper
`probe/p0/measure.py`; bleibt als Werkzeug im Repo, die Wheels werden zur
Laufzeit per `pip download` geladen und nie committet). Aufbau: das echte
Sandbox-Image (`python:3.12-slim` per Digest, Debian 13), die Grenzen der
Compose (512 MiB, 128 PIDs, 1 CPU, `read_only`, ohne Netz, unser Seccomp-Profil,
`/work` und `/tmp` `noexec`), die Bibliotheken als **Nur-Lese-Volume**. Wheels:
numpy 2.5.4, pandas 3.0.6, python-dateutil 2.9.0, six 1.17.0 (zusammen
27,8 MB als Wheels). Messhost: Docker Desktop/WSL2, Kernel 5.15, 32 CPUs, cgroup v1.

| Messwert | runc | runsc |
|---|---|---|
| glibc des Images | **2.41** (Debian 13) | nicht gemessen (kein gVisor lokal; `P0_RUNTIME=runsc-jnpt sh probe/measure-libs.sh` auf einem Host mit der Runtime) |
| Größe installiert (ohne pyc) | 95,8 MB (91,4 MiB), 2478 Dateien | nicht gemessen |
| Größe installiert (mit pyc) | 136,7 MB (130,4 MiB), 4325 Dateien | nicht gemessen |
| Import numpy, ohne pyc (5 Läufe) | Median 0,58 s (0,51–0,62) | nicht gemessen |
| Import numpy, mit pyc | Median 0,49 s | nicht gemessen |
| Import pandas, ohne pyc (5 Läufe) | Median 1,33 s (1,16–1,37) | nicht gemessen |
| Import pandas, mit pyc | Median 0,62 s (0,60–0,82) | nicht gemessen |
| `compileall` (65532) für alles | 5,5 s inkl. Container-Start | nicht gemessen |
| Maximaler RSS, Import pandas | 82 MiB ohne pyc, 71 MiB mit pyc | nicht gemessen |
| Maximaler RSS, Arbeitslast | 80 MiB (ohne pyc), 72–74 MiB (mit pyc) | nicht gemessen |
| Threads = cgroup-PIDs nach Import, ohne Deckel | **32** (= CPUs des Hosts: `cpus: 1` ist eine Quote, keine Zuordnung, OpenBLAS startet je Host-CPU einen Thread) | nicht gemessen |
| Threads = cgroup-PIDs mit `OPENBLAS_NUM_THREADS=1`, `OMP_NUM_THREADS=1` | **1** | nicht gemessen |
| Arbeitslast: 10 000 Zeilen, `groupby` (50 Gruppen), JSON | 6–8 ms; Lauf gesamt 0,91–0,96 s ohne pyc, 0,35 s mit pyc | nicht gemessen |

Messung im Prozess (`time.perf_counter`), ohne Interpreter-Start und
Container-Start. Der Seiten-Cache des Hosts wurde nicht geleert; „kalt“ heißt
hier: neuer Prozess, keine pyc-Datei (das Volume ist read-only, `sys.dont_write_bytecode`
gilt ohnehin), jeder Lauf übersetzt die Module neu. Der erste Lauf einer Reihe
unterscheidet sich nicht auffällig vom Median.

**Entscheidungen aus P0:**

- **Kein pyc vorab (B7 entfällt).** Der Import von pandas ohne pyc liegt bei
  1,2–1,4 s, weit unter der Schwelle von 3 s, und der Lauf gesamt bei unter
  1 s. Der 25-s-Rahmen ist nicht berührt. Ein `compileall` im vermittler wäre
  eine zusätzliche Ausführung von Python auf fremdem Archivinhalt und 41 MB
  mehr Platte für 0,7 s Gewinn. **Wiedervorlage:** Messen auf der Referenz-VM
  (langsamere CPU als der Messhost) und unter runsc; liegt dort pandas ohne pyc
  über 3 s, kommt B7 nachträglich (SEC-Auflage B7, `libs/<hash>.tmp`, Timeout,
  Nutzer 65532, danach `.jnpt-manifest` auch über die pyc).
- **`childEnv` setzt `OPENBLAS_NUM_THREADS=1` und `OMP_NUM_THREADS=1`.**
  Ohne Deckel startet schon `import numpy` so viele Threads wie der Host CPUs
  hat (hier 32 von 128 erlaubten PIDs); auf einem Host mit ≥ 120 CPUs ginge die
  Sandbox am `pids_limit` zugrunde. Mit Deckel ist es ein Thread.
- **glibc-Obergrenze für die Tags: 2.41** (`libs.MaxGlibcMinor`). Die
  manylinux-Tags der Wheels (`manylinux_2_28`, numpy 2.x: `2_27`/`2_28`) liegen
  darunter. Ein Wechsel des Basis-Images verlangt eine neue Messung; die Sonde
  `TestLibsGlibcCeilingMatchesImage` vergleicht die Konstante mit dem Image.
- **Grenzen v1 unverändert** (300 MiB und 30 000 Dateien je Lock, 2 GiB gesamt):
  numpy plus pandas belegen 91 MiB und 2478 Dateien, also rund ein Drittel
  der Größe und ein Zwölftel der Dateizahl. **Nicht nahe an einer Grenze.**
- **`libs` ohne `noexec`:** Native `.so`-Dateien werden mit `mmap(PROT_EXEC)`
  geladen; das geht nicht von einem `noexec`-Mount. `/work` und `/tmp` bleiben
  `noexec`. Das Volume ist read-only und für die Sandbox nicht beschreibbar.
- **Nicht gemessen:** runsc (kein gVisor lokal), Seccomp-Abweisungen unter
  Last (der Import löste keine Abweisung mit Folgen aus, ein Fehler wäre als
  `ImportError` aufgefallen), Messung auf der Referenz-VM.

## Stolperfallen (aus der Messung P0 und dieser Umsetzung)

- **S2:** runsc ignoriert das Seccomp-Profil ohne `--oci-seccomp` (Modus 0).
- **runsc braucht `--host-uds=open`** (gemessen in P2, Vertrag F3 B1): Ohne
  das Flag verweigert gVisor die Verbindung zu einem Unix-Socket, den ein
  Prozess außerhalb der Sandbox angelegt hat (`ECONNREFUSED`) — die Sandbox
  kommt nie in den Slot. `open`, nicht `all`: nur Öffnen eingehängter Sockets,
  kein Anlegen; die Grenze ist die Mount-Liste (R9-Sonde: nur die erwarteten
  Mounts; `/run/script-runner` enthält genau `sandbox.sock`).
- **Eigener Runtime-Name `runsc-jnpt`** (SEC-Bedingung zu B1):
  `runtimeArgs` gelten für jeden Container, der den Runtime-Namen nutzt. Ein
  eigener Eintrag `runsc-jnpt` mit `["--oci-seccomp", "--host-uds=open"]`
  (`deploy/daemon.json.example`), `JNPT_SCRIPT_RUNNER_RUNTIME=runsc-jnpt`; ein
  vorhandenes `runsc` des Betreibers bleibt unberührt. Wächter:
  `TestDaemonJSONExample`, `TestDocsUseOwnRuntimeName`.
- **Seccomp 2 ist unter Docker Desktop kein Beweis für das eigene Profil:**
  Dort meldet auch `seccomp=unconfined` Modus 2 (der Container erbt einen
  Filter der VM); unter runsc ist unconfined = 0. Deshalb der
  Profil-Selbsttest (B2); die Sonden prüfen Standardprofil und unconfined →
  `invalid`, unser Profil → bedient.
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
- **Das Netz `jnpt_plugins` muss vor `up` existieren.** Es gehört der
  Kern-Compose (external hier). Fehlt es, scheitert `up`; der Aktuator prüft
  deshalb vor dem Pull jedes externe Netz und bucht `unprovisioned` (ab
  Fassung 3). Die Sonden legen ein eigenes `<prefix>-plugins` mit
  `--internal` an (`probe/run.sh`); der Test-Läufer hängt dort mit Alias
  `jnpt` und für den Modul-Download zusätzlich an `<prefix>-out`.
- **E1 — relativer Profilpfad:** Der Aktuator ruft
  `docker compose --env-file … -f /opt/jnpt/plugins/script-runner/docker-compose.yml`
  ohne `cd` auf, systemd mit cwd `/`. Compose (gemessen v2.33.0 und v5.5.1)
  liest `seccomp=./seccomp/…` beim Anlegen gegen das **Projektverzeichnis**,
  nicht gegen das cwd; `compose config` zeigt den Pfad trotzdem unaufgelöst
  (`./seccomp/…`) — `config` ist also kein Beleg. Die Sonde
  `TestE1SeccompProfileFromProjectDirWithCwdRoot` legt die Sandbox mit cwd `/`
  an und vergleicht das Profil im Container mit der Datei. Den Pfad deshalb
  **relativ** lassen: ein absoluter Pfad bräche Sonden und lokale Läufe.

## Prüfen

```sh
# Unit-Tests (in einem Linux-Container; auf Windows hängt lokales go test)
# aus dem Repo-Root: der Release-Wächter liest .github/workflows/
docker run --rm -v "$PWD":/repo -w /repo/script-runner golang:1.26.9 go test ./...
python3 script-runner/seccomp/derive.py --check
# Docker-Sonden gegen den echten Stack (P2_PREFIX, Standard p2-928, räumt Stacks auf)
sh script-runner/probe/run.sh
# mit eigenen Container- und Netznamen für parallele Prüfaufträge
P2_PREFIX=jnpt1083p2 sh script-runner/probe/run.sh
# gegen ein fertiges Image (so fährt es die Release-Strecke)
P2_IMAGE=ghcr.io/januaport/script-runner@sha256:… sh script-runner/probe/run.sh
# Workflows
docker run --rm -v "$PWD":/repo -w /repo rhysd/actionlint:latest
```
