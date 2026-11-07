# CLAUDE.md — desktop-runner

## Verantwortung

Desktop-Läufer für JanuaPort #1167 / #1163 D4, Windows/amd64 und Linux-Server ohne CGO.
Kopplung, signiertes Manifest, lokaler Takt, Paket-/Runtimeprüfung,
COM-Autostart und DPAPI-Journal sind implementiert. Fake-Kern, Linux-Race-Tests
und native Windows-Sonden; signiertes Windows-Release `desktop-runner-v0.1.0`
vorhanden, Live-Anlage steht aus. Manueller Installationstest: README „Installationstest“.
Windows spricht Vertrag 5 (K6b-1: Vertrag 4 plus `apps`), Linux-Server unverändert Vertrag 2.
Maßgeblich: [L1-Ergänzungen](README.md#vertrag-4-und-testaufträge);
[contract/](contract/README.md) bleibt der eingefrorene historische D0-Vertrag 1.

## Aufbau

| Pfad | Inhalt |
|---|---|
| main.go | CLI und feste Fehlerausgaben |
| console.go, console_windows.go, console_other.go | GUI-Läufer (`-H=windowsgui`): reine Entscheidung `planConsole`, Windows-Anschluss an die Konsole des Aufrufers, No-op sonst (Invariante 19) |
| internal/client/http.go | Einziger HTTP-Client-Adapter, E3, Tokenrollen, Widerruf (Server-Ausnahme K6c: fensterio/loopback_windows.go) |
| internal/client/sse.go | Liest die SSE-Antwort des Lauf-Zweigs von `mcp` bis zur ersten Nicht-Benachrichtigung (Invariante 1, #1340) |
| manifest.go, copied_contract.go | Manifest/Kanonisierung/Ed25519, Gitobjekte/SHA-1DC, Cron/IANA |
| environment.go, copied_lock.go, copied_zip.go | Allowlist, Wheels, Entpacken und Umgebungsseal |
| engine.go, halt.go, bootstrap.py | Gemeinsamer Laufplatz, Takt, Claim/Start, lokaler Halt, Rahmenkanal/MCP |
| state.go, replace_* | Atomare OS-geschützte Dateien, Journal/Marker/Slot-Transaktion, Löschung |
| runtemp.go | Lauf-TEMP: RunID/RunTemps, Anlegen nur neu, Aufräumen/Besen ohne Reparse-Punkte, Kind-Umgebung |
| log.go | Geschlossene numerische Codes (1–9), 5 × 1 MiB |
| cmd/bedienfeld, internal/panel, internal/panelio | Nativer Tray-Prozess, reines Modell und getrennter Pipe-I/O-Adapter |
| internal/pipe, internal/client/pipe.go | Lokales Schema, Windows-Pipe, Serverprüfung, Setup-Zulassung und interne Rückfragen |
| internal/platform/*_windows.go | DPAPI/ACL (SecureRoot, MkdirPrivate), WTS/LastInput, suspendierter Prozess/Job Object, COM/HKCU |
| internal/platform/install.go, other.go | Windows-Nähte und Fail-closed-Stubs für andere Systeme |
| internal/platform/security_linux.go, server_linux.go | Linux-HMAC/Dateirechte, Sperre und Server-Konfiguration |
| container.go, container_engine.go, secrets_linux.go | Docker-CLI, Startwege und Geheimnisgrenze |
| internal/fenster | Reines Go ohne Netz/Prozess/Datei/Windows (K6b-1): CSP-Literal, Navigation, Nachrichten, Einstellungen, Umschlag, Brücke |
| internal/fensterio | Hinter der Tür des Bedienfelds (K6b-2): Gateway (App-Token im RAM, Modus a: 401 → einmal neu; Modus b: nativer Login), Pipe-Wrapper mit zwei Operationen, `Host` (UI-Thread-sicher), `PanelExchange` über die vorhandene Verbindung, `Runtime` (Apps lesen, öffnen), Urteile für native Ereignisse, `Surface` (nur Windows) |
| internal/wv2com | Nur Windows: dünner COM-Adapter für NavigationStarting, NewWindowRequested, DownloadStarting, Quelle der Webnachricht, ClearBrowsingData |
| internal/edge | Nur Windows: geprüfte, angepasste Kopie von go-webview2 v1.0.19 `pkg/edge` (MIT, NOTICE): kein `os.Exit`, kein `log.Fatal`, Start mit `Begin`/`Ready`/`Cancel`/`Close` statt `Embed` |
| internal/client/reader.go | `Reader`: nur lesender Zugriff des Bedienfelds auf Zugangsdaten, geprüftes Manifest und App-Siegel |
| apptoken.go, applogin.go, appstore.go, appload.go, copied_apphost.go | Vertrag 5 im Client: App-Token, privates Ablegen mit Siegel, Ladeprüfung, markierte apphost-Kopie |
| contract/golden-v5 | Bytegleicher Kernspiegel von Vertrag 5 (Manifest und Routen) mit SHA256SUMS |
| probe/k6b_mutations.py | Mutationen K6b-1 im Container mit Go und Python |
| internal/testfixture | Synthetische Gitpakete nur für Tests |
| probe/mutations.py | Negativtests unter entfernten Sperren, exakte Wiederherstellung |
| probe/installtest*.ps1 | Manueller Release-Installationstest, Offline-Adaptertests und isolierte Mutationen |
| contract/ | Vertrag/Goldens, eigenständiges D0-Testmodul |
| .github/workflows/desktop-runner*.yml (Repo-Root) | Linux-/Windows-CI und optional signiertes Release |

## Invarianten

1. Nur Pins am Ausführungsort `geraet:<id>`, keine `lokal`-Pins. Weg B:
   Gerät holt alles ab, keine Verbindung von der Anlage zum Gerät.
   `/api/device/v1/`, Contract-Header je Anfrage, no-store je Antwort.
   Einzige Ausnahme (#1340): Auf dem Lauf-Token-Zweig von `mcp` schickt der Läufer
   `Accept: application/json, text/event-stream` und nimmt zusätzlich genau
   `text/event-stream` mit `Cache-Control: no-cache, no-transform` an (so schreibt
   go-sdk v1.7.0 stateless); `application/json` mit no-store bleibt gültig. Der Läufer
   liest den Strom (`sse.go`), überspringt Benachrichtigungen (`method` ohne `id`) und
   verlangt als erstes anderes Ereignis die Antwort auf die eigene `id`; fremde `id`,
   Strom ohne Antwort, `Mcp-Session-Id` und mehr als 8 MiB insgesamt sind
   `ErrProtocol`. Auf jedem anderen Endpunkt und auf dem App-Zweig (`requestApp` mit
   App-Token, nur `application/json`, no-store, kein Strom-Accept) ist
   `text/event-stream` ein Protokollfehler. Schutz: `lauf_accept_test.go` gegen die
   Aufnahme `testdata/gosdk_v1.7.0_lauf_mcp.json` des echten SDK.
   Läufer-Token überall **außer `pair`**, dort kein Authorization-Header.
   Standard LAN/Tunnel, öffentlich nur Opt-in mit Allowlist und Bremse (C2).
2. Kopplung ersetzt die manuelle Tokenübergabe: Code 15 min/einmalig/60 Bit,
   Windows `pair --code` nur per argv; Linux-Server liest den Code per stdin.
   Format `<device_id>.XXXX-XXXX-XXXX` (16 kleine Hex-Zeichen,
   Crockford-Gruppen), nach 5 Fehlversuchen verbrannt, globale 429-Bremse.
   E3: nur gültiges HTTPS oder echter Loopback; selbstsigniert nur mit
   `--ca`/`--fingerprint`, kein Abschalten der Prüfung. Ed25519-Key per TOFU
   speichern; leeren Key und RFC-8032-Testvektor 1 vor jeder Speicherung
   abweisen, bestehende Kopplung erhalten. Rotation v1 nur neue Kopplung (E2).
   Widerruf: 401 `revoked`
   ohne message; Credential, Pakete/Umgebungen, Manifest und Journal löschen
   (die Gesamtlöschung der Wurzel steigt wie das Lauf-TEMP-Aufräumen nie in
   Junctions/Symlinks ein, Invariante 12; kein `os.RemoveAll`).
3. E1: Geräte-Manifest mit Ed25519 über exakt kanonische Bytes prüfen:
   rekursiv sortierte UTF-8-Schlüssel, kein äußerer Leerraum/BOM/LF,
   Zahlentoken verlustfrei; genaue Escape-Regeln im Vertrag. Fremde Geräte-ID,
   alte/gleiche seq oder falsche Signatur → Klasse `manifest`; alter Stand
   und letzter erfolgreicher Sync bleiben erhalten. seq uint64, dauerhaft,
   ab 1, je erfolgreichem Sync erhöht, auch bei gleichen Pins.
4. JSON streng mit `DisallowUnknownFields`. Windows-Heartbeat (Vertrag 4/5) ausschließlich
   `contract`, `ready`, `runner_version`, `manifest_seq`; kein Sitzungsstatus,
   keine Leerlaufzeit, kein Konto. Antwort ergänzt `manifest_seq_current`;
   bei höherem Wert Manifest holen. Nur Testaufträge dürfen `result` (JSON bis 64 KiB) senden;
   der normale Report-Weg weist es ab. `apps` wird unter v1–v4 abgewiesen und ist unter v5 Pflicht (Invariante 18),
   Setup-Beschriftungen über 40 Unicode-Zeichen ebenfalls.
5. Takt auf dem Gerät: 1 min vor jedem geplanten Lauf Manifest und bei
   neuem Commit unter Windows das Paket voll prüfen; Server prüfen vor Start
   Pull und Image-Digest. Offline nur ohne declared_tools und
   `max(Uhr, persistente Uhrmarke) − letzter erfolgreicher Sync < grace_hours`
   (1–72 h), sonst
   `refused/offline`. Werkzeuge brauchen Box/Lauf-Token. Deterministischer
   `run_key = pin/schedule/UTC-Slot`; Online-Start mit Werkzeugen über
   `runs/start`, Standbindung per `X-Jnpt-Manifest-Seq`, veraltet 409 manifest.
   Ad-hoc bleibt claim → jobs/start; Job liefert seine Freigabe-manifest_seq.
   Windows K8: Pinfrist vor Gerätefrist, auch längere Pinfristen. Atomare
   DPAPI-Datei `clock`, fortgeschrieben bei Sync, Takt und Offline-Start. Während
   des Prozesses läuft die Marke monoton weiter; nach Neustart gilt mindestens
   die gespeicherte Marke. Defekte Uhrmarke sperrt. Ablauf verweigert mit
   Logcode `7 1`. Auch offline verweigerte Windows-Versuche tragen
   `ran_offline:true`. Das Kernarchiv hält die längste wirksame Pinfrist + 24 h
   Puffer (`Manifest.RetainUntil` als Abgleich); das Journal verfällt nicht.
6. Ein Platz je Gerät für beide Laufarten. Zweiter Claim sofort 204,
   geplanter Start bei anderem Lauf 409 busy; zweiter Start desselben Laufs
   409 already_started ohne Token. MCP verlangt beide Token, zustandslos.
7. Windows-Pakete nur für `(commit,path)` eines offenen eigenen Auftrags **oder**
   eines aktuellen eigenen Manifest-Pins, damit Sync ohne Claim funktioniert.
   Pinentzug beendet im Kern alle offenen Aufträge des Pins mit `denied` und
   bricht einen laufenden Lauf ab; script-runner liefert nur aktuelle Pins.
   Unbekannt/fremd byte-gleiche 404. Git-IDs/exakte Objektmenge vor jeder
   Ablage prüfen; fehlend/überzählig, 120000/160000, unsichere Namen oder
   Grenzen → refused/hash, kein Prozess. SHA-1DC statt crypto/sha1 (C3).
8. Windows hat drei Hash-Stufen: Git, Wheel-SHA-256 gegen Lock, Umgebungs-Manifest
   (Pfad/Größe/SHA-256) nach Entpacken und vor jedem Start. Python-ZIP
   zusätzlich gegen ausgelieferte python.org-Allowlist (C1), nicht aus Pin;
   fehlend/falsch → refused/runtime. Paket-tools = signierte declared_tools.
9. E4: Journal zuerst für alle Ergebnisse (Windows: DPAPI CurrentUser;
   Linux: HMAC-SHA256, Dateien 0600/Verzeichnis 0700), höchstens
   200 Ergebniseinträge/16 MiB gespeicherter Bestand inkl. OS-Schutz-/Rahmen-Overhead,
   Laufmarker und Zähler; ältester Ergebniseintrag fällt mit dauerhaftem Zähler raus.
   Geschützte Laufmarker überleben Neustart; offen → interrupted,
   nie erneut starten. Keine Klartext-state.json. runs/result trägt genau
   run_key oder job_id, Freigabe-manifest_seq, Commit, Offline-Flag und
   Abschlusszeit. Box prüft historische Freigabe und führt vetLog aus.
   Erstes gewinnt dauerhaft, gleich 204/abweichend 409; erst nach 204 löschen
   (Ausnahmen gezählte Verdrängung, ein zehnmal in Folge mit HTTP 400 abgewiesenes
   ältestes Ergebnis und Widerruf). Das abgewiesene Ergebnis erhöht den
   Verlustzähler und hinterlässt die Logzeile 5; es staut den Rückstau nie.
   Alle anderen Fehler (409, 429, 503, Netz) verwerfen nie. Kein Tokenablauf-Cutoff.
10. Box hält Ad-hoc-Aufträge persistent, löscht Eingabe bei Ende; Claim nach
    Box-Neustart funktioniert. Auch Start-/Ergebnis-/Manifesthistorie bleibt;
    kein pauschales Neustart-404 mit Verwerfen. Windows: Contract 5 und unverändert Paketversion 1; Linux-Server: Contract 2,
    Updates zuerst Kern, dann Läufer; geschlossene Klassen inkl. offline und idle.
11. `refused/idle` (#1245 L): Die Startregel ist nicht erfüllt, weil nur der
    Leerlauf fehlt (Sitzung aktiv, Konto passt, Modus `idle`, Leerlauf kürzer als
    `idle_minutes`). Entsteht an derselben Stelle wie `account`, also vor Lauf-TEMP,
    Laufmarker und Spawn. Falsches Konto oder inaktive Sitzung bleibt `account`.
    Nie `idle` → `account` umdeuten (auch nicht für alte Kerne). Release-Hinweis:
    setzt einen Kern mit #1245 K voraus (er nimmt `idle` an); ein alter Kern
    antwortet 400, das Ergebnis staut nicht (Invariante 9).
12. Lauf-TEMP (#1245 L, Befund 6): Je Lauf ein neues, leeres Verzeichnis
    `%LOCALAPPDATA%\JanuaPort\desktop-runner\tmp\<RunID>\` (RunID = SHA-256 des
    Lauf-Schlüssels, genau 64 kleine Hex-Zeichen, vor jedem Pfad geprüft). Es ist
    `TEMP`, `TMP` und Arbeitsverzeichnis des Kinds, nie mehr `envs\…\runtime`
    (der Bootstrap wird per absolutem Pfad gestartet). Anlegen exklusiv (existiert
    es, `internal`, nichts übernommen), DACL wie der Läufer-Ordner, danach geprüft.
    Gelöscht wird nach dem Job-Teardown (`p.Close()`), nie währenddessen; der
    Besen beim Start räumt alles unter `tmp\` ab. Aufräumen und Besen steigen
    nie in Symlinks/Junctions/Reparse-Punkte ein (eigene Rekursion mit Lstat,
    kein `os.RemoveAll`, auch nicht in `State.Revoke`) und löschen sie nur als Verweis. Nichts überlebt einen
    Lauf, keine Größenbremse (Wanduhr-Grenze, eigene Platte). Journal, Ergebnis und
    Geheimnisse liegen nie unter `tmp\`. Die Kind-Umgebung enthält kein
    `LOCALAPPDATA`/`USERPROFILE`/`APPDATA`/`HOME*`/`TMPDIR`; ein Wert aus diesen
    Variablen wird gefiltert, `TEMP`/`TMP` zeigen nur auf das Laufverzeichnis.

13. Testauftrag: signiertes `test_device:true` UND Pinname
    `test_<erste 8 Zeichen der Geräte-ID>`, zusätzlich geprüfte Geräte-,
    Signatur- und Jobbindung (Pin, Commit, Pfad, Algo, Freigabe-seq).
    Claims enthalten kein Testmerkmal. `test_device` wird in v1 bis v5
    angenommen (#1298). `Screenshot` und `ReportTest` prüfen vor HTTP;
    `Report` weist `result` ab. Historische Freigaben liegen separat in
    DPAPI-Dateien `test-<job_id>`, nur Bindungsfelder und signiertes Manifest,
    keine Claim-Eingabe; Journaländerung nur Feld `result`. Replay
    prüft erneut. Testresultate weder offline noch über Setup-run_key.
    `StartSetup` prüft das signierte Manifest, erzeugt 128 Zufallsbits über
    `crypto/rand` (22 Zeichen base64url) und bindet `X-Jnpt-Manifest-Seq`.
    `API.Halt` meldet nur; `Engine.Halt` steuert seit L2 lokal (Invariante 14).
    L3 ergänzt Pipe und Setup-Zulassung, L4 das native Bedienfeld. L5 ergänzt
    `jnpt.frage/ergebnis/screenshot` ausschließlich über den Rahmenkanal.
    L3-2: Go prüft rohe Skript-Rahmen strikt, nie auf Python-Prüfung vertrauen.
    `ergebnis`: höchstens 64 KiB JSON einschließlich Report-Escapes, letzter
    Aufruf gilt; `None` entfernt das Feld, normale Aufträge verwerfen still.
    `screenshot`: vor GDI signierte Testfreigabe und Job-/Umgebungsbindung
    prüfen, sonst `fehler`/Python `None` und null Aufnahmen. Go-GDI ohne CGO,
    ganzer primärer Bildschirm, PNG bis 4 MiB, höchstens fünf Versuche pro Lauf
    einschließlich Fehlern; ein Versuch ist für die automatische Endaufnahme
    reserviert, somit höchstens vier explizite Aufnahmen.
    SEC-R4 F1: Endaufnahme nur ohne aktiven L2-Halt; `haltedLocked()` direkt
    davor unter `e.mu` prüfen, GDI/Upload außerhalb der Sperre. Der äußere
    Kontext erhält die Endaufnahme nach Wandzeitablauf. H2: `verified` prüft
    die Freigabe; der Pin der Laufumgebung muss zusätzlich `order.Pin` gleichen.
    Upload über `API.Screenshot`, keine Bildnachlieferung. Linux-Stub mit Fehler.
    Bei gesperrter Sitzung zeigt die Aufnahme den Sperrbildschirm; je nach
    Sitzung kann GDI nicht verfügbar sein. Das ist eine dokumentierte Grenze.

14. Lokaler Halt (#1240 L2, nur Windows): atomare DPAPI-Datei `halt`
    (`{state, until}`), vor jedem ersten Takt gelesen. B6: offline und über
    Neustart wirksam. L2-2: fehlt → `arbeitet`; unlesbar/ungültig →
    `angehalten` + dauerhaftes lokales Störungsflag, fester Logcode `8 1`.
    Speicherfehler sperren ebenfalls; erst erfolgreicher expliziter Wechsel
    löscht die Störung. Ohne Schreibzugriff ist ein neuer Zustand nicht dauerhaft.
    L2-1: Halt-Prüfung direkt vor `Spawn` innerhalb desselben `e.mu`-Abschnitts;
    Anhalten setzt den Zustand und ruft activeCancel/Kill unter genau dieser
    Sperre auf. Ergebnis `cancelled`, kein Start nach Halt zwischen Claim und Spawn.
    Pause (15/60/0 Minuten, `until` UTC) lässt den laufenden Lauf regulär enden,
    sperrt neue Slots/Claims/Testaufträge und endet automatisch. Offene Claims
    werden abgebrochen, Übergangsantworten verworfen; Heartbeat `ready` bleibt.
    Slotprüfung und atomare Slot-/Markerreservierung teilen ebenfalls `e.mu`.
    B3: Halt schränkt nur ein, Befehle starten nie. Fortsetzen umgeht keine
    bestehenden Startprüfungen. Kein Nachholen, auch nicht innerhalb der
    Fortsetzungsminute; übersprungene Slots ohne Marker/Ergebnis. Pause schwächt
    Anhalten nicht ab. `Engine.Halt`, `Pause`, `HaltStatus` bleiben die maßgeblichen
    Go-Schnittstellen; L3 leitet die geschlossenen Pipe-Befehle darauf weiter.
    Letzten Wechsel via L1-Adapter `POST /halt` nachliefern und koaleszieren,
    maximal ein Versuch je zehn Sekunden (auch Fehler). DPAPI-Datei `halt-attempt`
    erhält die Bremse bei Neustart; defekte/nicht schreibbare Bremse sperrt Meldungen.
    HTTP außerhalb von `e.mu`, damit Widerruf zurückrufen kann; alte Bestätigung
    löscht keinen neueren Wechsel. Eigener Sender läuft auch während Execute.

15. Lokale Pipe (#1240 L3, nur Windows), B1/B2: genau ein benannter Listener
    `\\.\pipe\JanuaPort.Bedienfeld.<erste 16 Hex-Zeichen SHA-256 der Benutzer-SID>`.
    Geschützte DACL nur Benutzer- und Logon-SID, keine AppContainer-/Capability-ACE.
    H1: `FILE_GENERIC_READ | FILE_WRITE_DATA` (`0x12008b`), kein `GENERIC_WRITE`
    und kein `FILE_CREATE_PIPE_INSTANCE`. Native H1-Probe: echte zweite
    Serverinstanz mit freiem Instanzplatz und installierter DACL, auch mit
    Benutzer als Besitzer; geprüftes nicht erhöhtes Medium-Token ohne aktive
    Admin-SID/Privilegien, Gegenprobe mit zusätzlichem Instanzrecht.
    Die Gegenprobe setzt eine direkte Benutzer-ACE und Medium-NWNR ausdrücklich;
    beide Instanzanlagen bekommen einen vollständigen Deskriptor statt Token-
    Standardwerten. Besitzer, Label und Kontroll-ACE werden am Handle nachgeprüft.
    Die gesamte Gegenprobe läuft in einem per `CreateProcessAsUserW` gestarteten
    nicht erhöhten Hilfsprozess, einschließlich erster Instanz; reine Thread-
    Impersonation im erhöhten CI-Prozess genügt nicht. Das primäre Kind-Token
    wird vor jeder Pipe-Anlage geprüft. Quelle, gefiltertes Token und Kind haben
    null Restricting-SIDs, damit keine zweite Zugriffsprüfung die Abweisung
    verursacht. Im Kind laufen schmale und breite DACL; die Negativprobe gegen
    die Produktions-DACL des Elternprozesses bleibt ohne Opt-in verpflichtend.
    Nur die Hilfsprozess-Proben (`standard_process` und
    `TestWindowsPipeRestrictingSIDsVerweigert`) sind Opt-in über
    `JNPT_NATIVE_TOKEN_PROBES=1`: `workflow_dispatch` mit gesetzter Variable
    in einer interaktiven Sitzung oder GATE auf dem Testgerät. Ohne Variable
    fester Skip-Text; mit Variable ist jeder Startfehler ein Testfehler.
    Die Dienst-Sitzung gehosteter Runner kann den gefilterten Helfer mit
    `STATUS_DLL_INIT_FAILED` abweisen. Keine Änderung von Window-Station-/Desktop-ACLs.
    Low-IL READ/WRITE, Fremdkonto, Remote, Squatting und Linux-DACL-Strukturtests
    bleiben vom Opt-in unabhängig.
    `CreateFile(FILE_APPEND_DATA)` allein belegt keine Instanzanlage. L3-1: ausdrücklich
    `S:(ML;;NWNR;;;ME)`; Low-IL READ und WRITE werden abgewiesen.
    `PIPE_REJECT_REMOTE_CLIENTS` und `FILE_FLAG_FIRST_PIPE_INSTANCE`; die erste
    Instanz bleibt bis zum Ende offen, auch zwischen Clients. Name besetzt oder
    Anlagefehler: genau ein Versuch, fester Logcode `9 1`, Läufer ohne Pipe,
    kein Ausweichname. Server prüft den Client-Bildpfad auf `jnpt-bedienfeld.exe`
    im eigenen Ordner. Clientbibliothek prüft `GetNamedPipeServerProcessId`,
    `desktop-runner.exe` im eigenen Ordner, vertrauenswürdiges Authenticode mit
    gleichem Herausgeber (vollständiger Subject-DER des geprüften Unterzeichners)
    und H2: gleiche Benutzer-SID aus dem Token des Server-Prozesses. Fehler →
    `Störung`, vor jeder gesendeten Nachricht. Signaturprüfung ohne UI und ohne
    Netzabruf; unzureichender lokaler Vertrauens-/Sperrlistenstand sperrt ebenfalls.
    Vier Byte Big-Endian-Länge plus 1–4096 Byte UTF-8-JSON; geschlossenes Schema,
    `DisallowUnknownFields`, keine Zusatz-/Doppelfelder, Nulls oder Folgedokumente.
    Golden: `internal/pipe/testdata/messages.json`. Keine Token-, Eingabe-, Pfad-
    oder Modusfelder; Status wird ausdrücklich projiziert, kein Bundle serialisiert.
    L3-2: jeder rohe Rahmen wird am Läufer in Go geprüft. Setup nur aus erneut
    verifiziertem Windows-Manifest (v5; v4 nur als Upgrade-Cache), ohne `frei`, bei passendem Konto/Sitzung, online,
    ohne Halt/Pause und ohne belegten/reservierten Platz. Reservierung umfasst
    HTTP und Prozessvorbereitung; Zeitplan, Ad-hoc und direktes Execute bleiben
    ausgeschlossen. Offene Claims werden abgebrochen und ihre Übergangsantworten
    verworfen; während des Setup-Laufs wird kein neuer Claim geholt.
    L1-Adapter bindet Sequenz und erzeugt die Nonce; keine
    Offline-Warteschlange für Setup-Befehle. Halt/Freigabewechsel während HTTP
    sperrt den Spawn. Unmittelbar vor Spawn wird die Setup-Freigabe unter derselben
    Engine-Sperre erneut gebunden; die L2-Haltprüfung bleibt unverändert.
    Rückfragen lokal: `frage{id,titel,text,optionen,frist_s}`, Titel höchstens
    40 Zeichen, Text 500 Zeichen, 2–4 Optionen zu je 40 Zeichen, 5–3600 Sekunden.
    H4: Titel aus dem Manifest-Pin, nie Skripttext. Vertrag 4 und 5 haben kein Pin-Label;
    Rückfall Pin-Name, auf 40 Zeichen begrenzt. Höchstens eine offene Frage je
    Lauf, in Go am echten stdout-Adapter geprüft. L4 ergänzt diesen H4-Rahmenweg
    samt Laufkontext; skriptseitiger Titel, ungültiger Rahmen oder zweite offene
    Frage → `abgewiesen`, Lauf `internal`. Ohne Panel/bei Frist `wahl:null`;
    Abbruch/Anhalten schließt ohne Wahl. `antwort.wahl` ist nullbasiert.
    L5 ergänzt den Python-Helfer in `bootstrap.py`. Rohe `ergebnis`/`screenshot`
    sind ausschließlich Skript-Rahmen mit Go-Prüfung gemäß Invariante 13;
    im Bedienfeld-Pipe-Schema bleiben sie abgewiesen.
    Setup-Projektion: Pflicht-Bool `braucht_eingabe` ausschließlich aus
    `len(frei)>0` des verifizierten Manifests; `startbar` fasst alle Sperren
    zusammen. Die zweite Schemaänderung ermöglicht den genauen Eingabehinweis.
    Ergebnis und Screenshot bleiben zusätzlich durch die L1-Testauftragsprüfung
    geschützt (Invariante 13). H3: Pipe-Serverhandles sind nicht vererbbar und
    stehen nie in der expliziten Python-Handleliste (nur stdin/stdout/NUL).
    Native Kind- und echte Python-Sonde prüfen Handle-Tabelle, Env und Kommandozeile:
    kein Pipe-Name und kein `jnpt*`. Kein Linux-Listener. K6c erlaubt genau einen kurzlebigen
    Windows-Loopback-Listener in `internal/fensterio/loopback_windows.go` (Invariante 18).
    Unbenannte/unlesbare Pipe-Endpunkte werden bei der Namensabfrage übersprungen;
    Erkennung der Eltern-Pipe, absichtlich vererbtes Handle und eine eigene
    Python-Kontrollpipe sichern die Sonden gegen falsch grüne Ergebnisse.

16. Bedienfeld (#1240 L4; Plan-Invarianten 14/15/18): `jnpt-bedienfeld.exe`,
    separater Windows-Prozess ohne CGO, gebaut mit `-H=windowsgui`.
    `internal/panel`: reines Linux-testbares Modell. `internal/panelio`: I/O.
    `cmd/bedienfeld`: UI-Thread mit LockOSThread und Message-Loop; Pipe-Lesen
    in Goroutinen, Übergabe per PostMessage. **Neu gefasst in K6b-2 (SEC-B1–B4):** Das Bedienfeld
    als Prozess hält seit den App-Fenstern das Gerätetoken (nur lesend, Reader) und einen Netzweg
    zum Gateway, aber **nur hinter einer Tür**: `cmd/bedienfeld -> internal/fensterio` ist die einzige
    benannte Kante. `internal/panel`, `internal/panelio` und `internal/pipe` bleiben strikt ohne
    `net/http`, `internal/client`, `internal/fensterio` und freies `net`; `importExceptions` in
    `TestPanelImportWaechter` bleibt leer, der Wächter ist je Wurzel geschnitten (keine Kantenliste),
    `TestFensterioNurUeberDieTuer` prüft, dass nur `cmd/bedienfeld` die Tür nutzt. Das Bedienfeld selbst
    importiert weder `internal/client` noch `internal/platform` noch `net/http`
    (`TestBedienfeldImportiertClientNicht`, `TestPlatformNurHinterDerTuer`). Hinter der Tür gilt B3: aus
    `internal/client` nur Leser und AppAPI (`TestFensterioRuftNurLeserUndAppAPI`, AST), kein Engine-,
    Spawn-, Claim-, Schreib- oder Widerrufsaufruf; bei „revoked“ ein fester Fehler, gelöscht wird nichts
    (`TestRuntimeWiderrufLoeschtNichts`). B1: Die UI-Thread-Dateien rufen weder Gateway noch Brücke noch
    Zugangsdaten-Lesen noch Pipe-Gespräch (`TestBedienfeldUIThreadOhneNetzUndBruecke`); `Host.Receive`
    blockiert nie, Gateway und lokaler Pfad haben getrennte Plätze (`TestHostAnhaltenBleibtBeiHaengendemGatewayBedienbar`),
    ein WebView2-Absturz (`ProcessFailed`) schließt nur das Fenster (`TestHostFailBrichtAbUndNimmtNichtsMehrAn`,
    nativ `TestWindowsWebViewProzessabsturz`). B4: Die Pipe bleibt tokenfrei; das Token wird erst beim
    Öffnen einer App gelesen (`TestReaderAppsDekodiertKeinToken`) und steht nie in Env, Kommandozeile,
    AdditionalBrowserArguments oder Benutzerdatenverzeichnis (`TestWindowsWebViewProzessbaumOhneTraeger`).
    Win32 via syscall/System32; x/sys/windows nur hinter Pipe und hinter der Tür.
    Ausschließlich `pipe.Dial()` prüft Bildpfad, Authenticode und Benutzer-SID.
    Nichts vor erfolgreicher Prüfung schreiben; kein Ausweichname, Wiederholung
    mit 5–60 Sekunden Abstand. Fünf Sekunden ohne Status oder bei blockiertem
    Schreiben beenden die Verbindung.
    §6.6: Störung aus Zustand, Fehler, Nichterreichbarkeit oder Serverprüfung
    bleibt dauerhaft violett mit exakt
    „Störung. Anhalten: abmelden oder über den Admin halten lassen.“
    Erst gültiger `zustand` nach erneuter erfolgreicher Prüfung löscht sie.
    Fehlende Win32-DLLs/Procs lösen keinen Panic aus: geprüfte Auflösung,
    Modell bleibt in Störung, keine Pipe-Verbindung. Fehlen grundlegende
    Tray-APIs, endet der Prozess ohne Anzeige; sonst fester Störungstext.
    Wächter: `TestBedienfeldProcAufloesung`, native Negativprobe
    `TestWindowsBedienfeldFehlenderProc`. Prozesssonde löst
    `kernelbase.dll!CompareObjectHandles` mit `Find()` auf, sonst Skip mit Grund.
    Menü: Zustand, Pause 15/60/bis Weiter, Weiter, Anhalten/Fortsetzen, Setups,
    „Test“ nur auf Testgeräten, Beenden nur des Bedienfelds. `bis` lokal;
    offline + Fristablauf → „offline, Frist abgelaufen“. Graue Setups nur bei
    `braucht_eingabe:true` mit „braucht Eingabe“ ergänzen.
    Fenster erst nach Klick, kein Hotkey/Toast/Autostart-Code; die einzige Sprechblase ist der feste
    Hinweis bei neuer Rückfrage (Invariante 19, #1240 A2).
    Dialog „JanuaPort – <titel>“, statischer Text, 2–4 gewöhnliche Knöpfe,
    `WS_EX_NOACTIVATE`, `SW_SHOWNOACTIVATE`, kein `BS_DEFPUSHBUTTON`, kein
    Anfangsfokus/SetForegroundWindow/SetFocus/SetActiveWindow. Höchstens einer
    offen; zweite Frage nicht anzeigen. `frage_weg`, Anhalten, Trennung und
    Frist schließen ohne Antwort; Antwort nur `antwort{id,wahl}`.
    Native Prozesssonde: echte Exe ohne Argumente, PEB/Umgebung ohne `jnpt*`
    (Exe-Name ausgenommen), Handle-Vererbung samt Gegenprobe. Läuft über den
    bestehenden Windows-Job. Kein Tray-Rauchtest/lokaler GUI-Start.
    L6 bindet Installation/zweite Aufgabe/Release gemeinsam (Invariante 17); App-Fenster: Invariante 18 (K6b-2).

17. Installation und Release (#1240 L6; Plan-Invariante 19):
    `desktop-runner.exe` und `jnpt-bedienfeld.exe` aus demselben Release im selben
    Ordner. `install` prüft die reguläre Bedienfeld-Datei vor jeder Registrierung;
    fehlt sie, fester CLI-Code E1 ohne Änderung der Einträge. Zwei Anmelde-Aufgaben:
    `JanuaPort-Desktop-<SID>` und `JanuaPort-Bedienfeld-<SID>`, beide LogonTrigger,
    InteractiveToken, LeastPrivilege, gleiche Wiederanlauf-/Akku-Eigenschaften.
    Läufer mit `run`, Bedienfeld ohne Argumente, Credential oder gesetzte Umgebung.
    Rückfall je Exe nur bei Zugriffsschutz/Richtlinie auf HKCU Run:
    `JanuaPortDesktopLaeufer` bzw. `JanuaPortBedienfeld`. Bei Installationsfehlern
    werden vorherige Aufgaben/Run-Werte wiederhergestellt; Rücknahmefehler werden
    ebenfalls gemeldet. `status` zählt beide Exe. `uninstall` versucht beide
    Autostartwege beider Exe auch bei Teilfehlern und beendet das Bedienfeld mit
    passendem Bildpfad und Benutzer-SID. Der Läufer läuft gegebenenfalls weiter.
    Install/Update/Uninstall öffnen keinen DPAPI-Zustand: Halt, Credential und
    unbestätigte Ergebnisse bleiben erhalten. Kein Start der Exe bei Installation.
    Release baut beide ohne CGO und mit `-H=windowsgui` (Invariante 19); vorhandene Azure-
    Signatur für beide, je Exe Existenz und `signtool verify /pa` als Pflicht.
    SHA256SUMS und Provenance binden beide finalen signierten Dateien.
    `TestReleaseUndWindowsCI`, Install-Paartests und `probe/l6_mutations.py`;
    echte COM/HKCU-Proben über die bestehende Standardkonto-Sonde, PS-Adaptertests
    mit Argument-/Summen-Negativfällen. Fristen des Installtests unverändert.

18. PC-Fenster, fensterloser Teil (#1048 K6b-1; Plan 5, SEC K6b (a)–(c)). Kein Fenster, kein
    WebView2-Adapter, keine Änderung am Bedienfeld (das ist K6b-2).
    **Vertrag 5:** `client.WindowsContract = 5`; `apps:[{name,commit,path,mode?}]` ist unter v5 Pflicht
    (auch `[]`), sonst verboten. Name `^[a-z0-9_]{1,48}$`, `commit` 64 kleine Hex-Zeichen,
    `path == apps/<name>`, streng aufsteigend nach Name. Ein Cache v1/v3/v4 liefert beim Upgrade nur
    die Sequenz (Invariante 5/10). `mode` optional, nur a/b/leer, sonst Manifestfehler.
    Nur Bedienhilfe: b zeigt die native Anmeldeleiste, a weder Leiste noch Knöpfe/Meldungen;
    fehlend/leer zeigt sie wie bisher. Neue geprüfte Manifeste aktualisieren auch offene Fenster
    beim Abgleich (`Reader` → `Runtime.Apps/Watch` → `Surface.UpdateApps`); asynchrones Öffnen
    übernimmt keinen älteren Anzeigestand. Rechte, `/token`, Login-Fluss und K6b-2/K6c-Sperren
    bleiben unverändert. Goldens: `contract/golden-v5/` (Kernbytes), Manifest seit #1321 mit mode,
    SHA-256 `7cfb70018cb3265f39245b43de2e0c64f2859b8be366c4b34509454058656ae6`;
    ältere Vertragsgoldens bleiben bytegleich (`TestGoldenV5AusKern`).
    Startmeldung der Leiste: „Bitte mit dem eigenen Konto anmelden.“ bleibt beim ersten
    `Login.Pump` erhalten; `NewLogin` und Control-Anlage nutzen `anmeldung.Message("start")`.
    Nach Abmelden weiterhin „Abgemeldet.“. Beleg: `TestLoginStartmeldungBleibtNachPumpUndAbmelden`,
    Mutant `leistentext-start` (lokal ohne GUI/Container).
    **App-Paket:** `AppStore.Ensure/Prepare/Open`. Erst `decodePackage` (Git-IDs, SHA-1DC/SHA-256,
    exakte Objektmenge, Namen), dann die kopierten apphost-Regeln (Modus nur 100644, ≤ 1000 Dateien,
    ≤ 4 MiB, MIME-Liste, Geheimnismuster, keine aktive Markup). **Das CSP-Literal setzt der Läufer selbst
    ein** (LEAD c-6637272308): nach der Paketprüfung als erstes Element hinter dem `<head>`-Starttag jeder
    HTML-Datei (`injectCSP`), danach erst versiegeln. HTML mit eigener CSP-Meta oder ohne sauberes `<head>`
    (genau ein Starttag, davor nur Doctype/Kommentar/Leerraum/`<html>`) wird abgewiesen; `Open` prüft zusätzlich
    pro HTML-Datei, dass genau dieses Literal an erster Stelle steht (`leadingCSP`). Der Tokenizer ist nur der Vorfilter (davor weder Text noch End-Tags); die eigentliche Prüfung ist `html.Parse` auf dem fertigen Dokument: erstes Element-Kind von `head` ist die Meta mit genau `fenster.CSP`, genau eine CSP-Meta (SEC-R4 F1: Text oder `</br>` vor `<head>` schiebt die Meta für den Parser in den Body). SEC-R4 F4: Eingesetzt wird `<meta charset="utf-8">` und danach das Literal (`fenster.HeadPrefix()`, die ersten zwei Elemente von `head`); HTML mit ESC-Byte (ISO-2022-Umschaltung), NUL oder ungültigem UTF-8 wird abgewiesen, `Open` prüft beides erneut. Grund: `connect-src
    'none'` im Paket bräche `fetch` im Browser bei `fenster=beide`. Abgelegt wird privat im Staging und mit einem Umbenennen unter
    `<Zustand>\apps\<name>\<commit>\`; das Siegel (Pfad, Größe, SHA-256, auch gegen zusätzliche Dateien
    und Verweise) liegt im verschlüsselten Zustand und wird vor **jedem** `Open` geprüft. Nie
    stilles Reparieren: Eine vorhandene, nicht zum Siegel passende Ablage ergibt `refused/hash`
    ohne erneuten Abruf. Widerruf löscht über `State.Revoke` auch `apps\`.
    **App-Token:** `API.AppToken` (`POST apps/{name}/token`, leerer Körper) und `API.AppRPC` (nur
    `tools/list`, `tools/call`) senden `Authorization` (Gerätetoken) plus genau einen
    `X-Jnpt-App-Token`, nie `X-Jnpt-Run-Token`. `AppSession` hat keinen Getter und druckt sich nie.
    `fensterio.Gateway` hält sie nur im RAM, holt sie in Modus a bei 401 `unauthorized` genau einmal neu
    (kein Wiederholen bei der zweiten 401) und bildet jeden Fehler auf einen festen Code ab.
    **Fenster-Policy (`internal/fenster`):** Import-Allowlist (keine Netz-, Prozess-, Datei-,
    Windows-Pakete). `NavigationAllowed` erlaubt nur `https://app-<name mit - statt _>-pc.jnpt.invalid`
    ohne Port, Userinfo, Großschreibung, Steuer- und Nicht-ASCII-Zeichen und **nur für Pfade mit der Endung `.html`** (SEC-R4 F2: SVG, JS, JSON, MAP, TXT tragen keine CSP und laufen sonst auf der App-Origin ohne Richtlinie; kodierte Trenner `%2f %5c %00 %3f %23` abgewiesen; Einstieg `StartURL` = `/index.html`). `MessageAllowed` verlangt
    obersten Rahmen, eigene Origin und einen der vier Befehle; andere Quellen bekommen keine Antwort.
    `Settings()` ist die Sollliste (nur `IsScriptEnabled` und `IsWebMessageEnabled` an).
    Umschlag `{id,befehl,args}`: ≤ 256 KiB, `DisallowUnknownFields`, keine doppelten Schlüssel;
    `status`/`anhalten`/`werkzeuge` verlangen `args:{}`, `werkzeug` `{name,arguments}`.
    **Plan 4c / Träger nie an die Seite:** Antworten tragen ausschließlich feste Codes
    (`fenster.Codes()`); Adapterfehler sind `*fenster.Fehler`-Sentinels, jeder andere Fehler wird
    `fehler`, nie sein Text. Ein Ergebnis mit Token-Muster (`jnpt…_<20+ Zeichen>`, `jnptpc_`,
    `jnptapp_`, auch nach JSON-Unescaping) wird zu `antwort_ungueltig` verworfen.
    **Lokal genau zwei Operationen:** `fenster.Lokal` = `Status`, `Anhalten`; `fensterio.NewLokal`
    sendet nur `status` und `anhalten` über eine übergebene `Exchange` (K6b-2 bindet sie an die
    vorhandene Bedienfeld-Verbindung, es gibt keine zweite Pipe-Verbindung). `Status` projiziert nur
    `zustand`, `bis`, `netz`; `Anhalten` gilt erst, wenn der Läufer `angehalten` bestätigt.
    Grenze (SEC K6b (c), Invariante 15): Die Pipe-ACL schützt nicht gegen den WebView2-Hauptprozess.

    **K6b-2 (App-Fenster im Bedienfeld, SEC-B1–B4 und natives Ereignismodell):** Menüeintrag je App
    (`panel.Model.SetApps`, nur Namen), Öffnen in einer fensterio-Goroutine (`Runtime.OpenAsync`: Manifest
    prüfen, Siegel prüfen, erst dann Zugangsdaten lesen, `client.NewAPI` mit onRevoke ohne Löschen).
    `Surface` (nur Windows, UI-Thread): natives Fenster, WebView2 über `internal/edge` (Kopie von go-webview2
    v1.0.19, siehe unten) plus dünner eigener COM-Adapter `internal/wv2com` für alles, was die Bibliothek nicht durchreicht
    (Vtable-Positionen und IIDs aus WebView2.h, SDK 1.0.2535.41). **Nativ gebunden, Pflicht, sonst öffnet das
    Fenster nicht** (`RunSteps`, fester Fehler `webview_nicht_nutzbar`): `NavigationStarting` und
    `FrameNavigationStarting` (Abbruch der Navigation, auch Redirect und meta refresh; nur `.html`-Seiten der
    eigenen App laufen; die Anfrage verhindert erst Schicht 1 und 2), `NewWindowRequested` (immer `Handled`, nie ein Fenster; externe https-Links nur über
    den nativen Ja-Dialog mit Host, dann `ShellExecuteW`), `DownloadStarting` (Cancel und Handled),
    `WebMessageReceived` mit Quelle (`MessageSource` → `MessageAllowed`), `PermissionRequested` (global
    Deny), `ProcessFailed` (Fenster schließen, Host abbrechen). Dazu `Settings()` vollständig,
    `ClearBrowsingData` (A5, ICoreWebView2_13/Profile2), virtueller Host je App mit Zugriff `DENY`,
    `Layout`: Die WebView bekommt einen Bereich, auf Testgeräten darüber die native Leiste „Test – Testgerät“.
    Das Benutzerdatenverzeichnis `<Zustand>\webview\<app>` entsteht mit `platform.MkdirPrivate`
    (private DACL) und wird vom Widerruf des Läufers ohne Verweisen zu folgen gelöscht
    (`TestRevokeLoeschtWebviewOhneVerweiseZuFolgen`). `WebResourceRequested` ist Schicht 1 (siehe unten). Ein offenes Fenster
    sperrt die Dateien: Das Bedienfeld schließt das Fenster einer App, sobald sie aus der Liste verschwindet (`CloseMissing`),
    und die Liste leert sich sofort, wenn die Zustandsdatei fehlt. **Restgrenze:** Löschte der Läufer vor dem Schließen,
    können Reste von `webview\<app>` bleiben; das Bedienfeld löscht nichts (B3).

    **Start des WebView ist begrenzt (VM-Befund, B1):** Die Bibliothek beendete den Prozess bei jedem Fehler
    (`os.Exit` im Fehlerrückruf) und wartete in `Embed` ohne Grenze (`GetMessage`); ein Fehlstart hing den
    UI-Thread ein. `internal/edge` ist deshalb eine Kopie mit festen Abweichungen: Fehler werden gemeldet und
    gemerkt, der Prozess endet nie; `Embed` ist durch `Begin` (stößt die Umgebung an), `Ready` (fertig oder
    erster Fehler), `Cancel` (spätere Rückrufe wirkungslos) und `Close` ersetzt; der Controller-Rückruf kehrt bei
    jedem Fehler zurück und registriert nur Berechtigungen und Prozessabsturz (das Echo der Seitennachrichten,
    `WebResourceRequested` mit `log.Fatal` und ungenutzte Rückrufe entfallen); ungenutzte Antwort-Erzeugung ist
    entfernt. `NewSurface` wartet über `WaitFor` höchstens `startFrist` (20 s) und verteilt dabei Nachrichten
    (`pumpOnce`, WM_QUIT wird für die Hauptschleife erneut eingestellt); bei Fehler oder Frist `Cancel`, fester
    Fehler, kein Fenster. Vorher prüft der Schritt "com apartment", dass der Thread ein STA ist
    (`checkThread`); `fensterio.EnterUI` initialisiert COM und das Bedienfeld ruft es direkt nach
    `LockOSThread` auf (`TestBedienfeldInitialisiertCOMAufDemUIThread`). Tests: `TestWaitForBegrenzt`,
    `TestStartFristBegrenzt`, `TestEdgeKopieOhneProzessendeUndOhneUnbegrenzteWartezeit`,
    `TestAdapterPruefteThreadUndWartetBegrenzt`; native Sonden `TestWindowsWebViewOhneCOMOeffnetNicht` und
    `TestWindowsWebViewFehlstartHaengtNicht` (erzwungener Fehlstart ohne COM). Jede native Sonde läuft auf einem
    festen Thread mit COM-STA und hat eine eigene Frist (`probeFrist`, 90 s je Sonde und Unterfall): Ein Hänger
    kostet eine Sonde, nicht den Lauf.

    **Die Kopie `internal/edge` ist Sicherheitskern (SEC-Bedingungen):** (1) Herkunft: `doc.go` nennt Repository,
    Tag `v1.0.19`, Commit `11c6e567b916645d5fe6df006681f988ac52c13d` und die `go.sum`-Prüfsumme;
    `internal/edge/upstream-diff.sh` holt das Original, prüft den Commit gegen den Go-Modulproxy und zeigt den
    Diff (`docker run --rm -v "$PWD:/w" -w /w golang:1.26.10 sh internal/edge/upstream-diff.sh`);
    `TestEdgeKopieNenntUpstream` hält doc.go, go.mod und go.sum zusammen. (2) Wächter: kein `os.Exit`, kein
    `log.Fatal`, kein `panic`, keine Endlosschleife, kein `Sleep`/`Wait`, kein `GetMessage`, kein `Embed`, keine
    Rückkanäle, die das Fenster nicht braucht (`TestEdgeKopieOhneProzessendeUndOhneUnbegrenzteWartezeit`,
    `TestEdgeKopieOhnePanicEndlosschleifeUndWarten`; Mutanten `edge-exit`, `edge-fatal`, `edge-panic`,
    `edge-endlosschleife`, `edge-sleep`, `edge-getmessage`, `edge-echo`). (3) Fehlerpfade: Auf jeden
    `errorCallback` in den Rückrufen der Umgebung und des Controllers folgt ein `return`
    (`TestEdgeRueckrufeKehrenNachFehlerZurueck`); `Ready` liefert den ersten Fehler, `NewSurface` macht daraus
    den festen Fehler `webview_nicht_nutzbar` (`RunSteps` verwirft die Ursache), das Fenster bleibt zu, Tray und
    Pipe stehen; nativ belegt `TestWindowsEdgeControllerFehlerBleibtFehler` (Controller-Start mit Elternfenster 0)
    und `TestWindowsWebViewFehlstartHaengtNicht` (Umgebung scheitert). (4) Import-Tür: `internal/edge`,
    `internal/edge/w32` und `internal/wv2com` sind für `panel`, `panelio`, `pipe` und `cmd/bedienfeld` verboten
    (`TestPanelImportWaechter`), und im ganzen Modul importiert nur `internal/fensterio` die Kopie
    (`TestEdgeNurVonFensterio`; Mutant `edge-wird-importiert`). (5) **Prüfpunkt je Läufer-Release: Upstream-
    Sicherheitsfixes.** Vor jedem Release des Läufers prüft der Releasende, ob `wailsapp/go-webview2` oder das
    WebView2-SDK nach `v1.0.19` Sicherheitsfixes veröffentlicht hat (`upstream-diff.sh` und das Changelog des
    Originals ansehen, relevant sind besonders `pkg/edge`, `internal/w32` und die COM-Aufrufe); das Ergebnis
    („nichts relevant“ oder „übernommen in Commit …“) steht im Release-Eintrag. Eine Übernahme ändert Tag,
    Commit und Prüfsumme in doc.go, go.mod und go.sum zugleich und läuft durch dieselbe SEC-Prüfung wie die
    Kopie selbst.

    **Lehre aus Sondenlauf 3:** Die CSP (`script-src 'self'`) blockiert Inline-Skripte; Probeseiten liefern ihre
    Auslöser deshalb als Dateien (`TestWindowsWebViewInlineSkriptBlockiert` belegt es). Der Gateway-Speicher der
    Sonde wird je Fall geleert, sonst beenden Ereignisse früherer Fälle spätere Warteschleifen sofort und
    machen Kontrollen blind. Diagnose je Runde (nur Sonden, nie im Betrieb): Spur je Fenster mit Quelle
    (`fenster.DiagText`: gequotet, begrenzt, Träger maskiert), Urteil `fenster.SourceVerdict` mit festem Grund,
    `NavigationCompleted` mit Erfolg und Status, Antworten als `fenster.ReplySummary` (ok oder fester Code),
    verworfene Webnachrichten mit Grund; im Fehlerfall und bei Frist wird alles ausgegeben.

    **Fremdserver-Befund (Sondenlauf 4) und drei Schichten gegen Ausleitung (SEC):** `NavigationStarting`
    brach `metaRefresh`, `location.href` und fremde Links ab (`NavigationCompleted` status 14), trotzdem kam
    `GET /ziel.html` am Testserver an. Bis die Ursache bewiesen ist, gilt das als echter Ausleitungsweg. Daher
    (1) **WebResourceRequested für alles** (`fenster.ResourceAllowed`, Filter `*` über alle Kontexte): nur der
    eigene virtuelle Host kommt durch (Dokumente zusätzlich mit der `.html`-Regel von `NavigationAllowed`,
    Unterressourcen nur mit dem Host), alles andere bekommt eine lokale 403-Antwort ohne Netz; scheitert die
    Antwort, schließt das Fenster. (2) **Netz im Browserprozess aus** (`fenster.BrowserArguments`): totes
    Proxy-Ziel `0.0.0.0:0` (kein Port, den ein anderes Konto binden kann), `<-loopback>` (auch Loopback geht
    nicht am Proxy vorbei), `--host-resolver-rules="MAP * ~NOTFOUND"` (DNS aus), kein Träger darin. Der
    Kommandozeilenwert ist keine harte Grenze: `WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS` und Richtlinien der
    Maschine oder des Benutzers können ihn übersteuern (siehe Betriebsarten der Sonde unten;
    eine Proxy-Richtlinie ist eine Restgrenze von Schicht 2). Deshalb ist Schicht 1 Pflicht. (3)
    `NavigationStarting` bleibt dritte Schicht. Belegt wird nur nativ: `TestWindowsWebViewFremdserverNull`
    (17 Wege, Loopback und eigene IPv4-Adresse des Gasts, 0 Anfragen und 0 Verbindungen, sehende Kontrolle) und
    `TestWindowsWebViewFremdserverUrsache` (ohne Schichten, nur Schicht 1, nur Schicht 2; Sec-Fetch-*,
    Sec-Purpose, Purpose und Zeitpunkt je Treffer). Auflage SEC (a): Der Schritt `umgebung` entfernt JNPT*-
    Variablen aus der Umgebung des Bedienfelds, bevor der Browser erbt (`jnptNames`); der Produktpfad des
    Datenordners (`<Cache>\JanuaPort\desktop-runner\webview\<app>`) trägt kein jnpt, der öffentliche
    Hostname `*.jnpt.invalid` ist kein Geheimnis und wird in der Sonde nur als solcher ausgenommen und gezählt.
    **Fail-closed nach Wirkung (SEC-Urteil Lauf 5):** Schicht 2 ist die tragende Sperre, Schicht 1 die Pflicht
    daneben. (a) Der Schritt `browser pruefen` liest nach dem Start die Kommandozeile des Browserprozesses
    (`get_BrowserProcessId`, PEB des eigenen Kontos) und verlangt unsere Argumente exakt
    (`fenster.BrowserCommandLineProblems`: Proxy, Umgehungsliste und Resolver genau einmal mit unserem Wert,
    `--proxy-pac-url`, `--no-proxy-server` und `--proxy-auto-detect` verboten); sonst bleibt das Fenster zu (fester
    Fehler, Tray und Halt stehen). Die Meldungen nennen nur Argumentnamen. (b) Jede gesetzte `WEBVIEW2_*`-Variable ist ein Eingriff: Der Schritt `umgebung` lehnt vor allem
    anderen ab, auch vor dem Entfernen der `JNPT*`-Variablen. Der Go-Lader setzt aber selbst im Paket-Init
    (`webviewloader/env_create.go:16-19`, `preventEnvAndRegistryOverrides` `:165-176`, nochmals vor jedem
    Aufruf `:109`) `WEBVIEW2_PIPE_FOR_SCRIPT_DEBUGGER`, `..._ADDITIONAL_BROWSER_ARGUMENTS`,
    `..._BROWSER_EXECUTABLE_FOLDER`, `..._USER_DATA_FOLDER` auf leer und `..._RELEASE_CHANNEL_PREFERENCE` auf `0`,
    damit weder Umgebung noch Registry etwas überschreiben (WebView2 prüft auf Vorhandensein, nicht auf den Wert).
    Darum vergleicht `webview2Findings` die Momentaufnahme vor dem Lader-Init (`internal/envsnap`: nur `os`,
    Importpfad sortiert vor `github.com/wailsapp`, Go initialisiert seit 1.21 in dieser Reihenfolge; hier darf
    nichts stehen) mit der Umgebung jetzt (nur diese fünf Namen mit genau diesen Werten sind der erwartete
    Zustand, jeder andere Name oder Wert ist ein Eingriff). Meldungen tragen Name und Wertlänge, nie den Wert.
    Dass ein überschriebener Wert dabei unsichtbar bliebe, fängt die Prüfung nach Wirkung ab (a). (c) Spekulatives Laden: **Schicht 1 allein ist nicht
    dicht**: Mit nur dem Ressourcenfilter lässt `<link rel=prerender>` ein echtes GET durch (Sondenlauf 7). Die
    Feature-Schalter `Prerender2` und `NoStatePrefetch` wirkten in Runtime 156 nicht und sind entfernt, ebenso
    `--disable-background-networking` und `--dns-prefetch-disable`, für die keine Messung trägt. Was hält, ist
    **Schicht 2**: Das Netz im Browserprozess ist aus, und fällt sie aus (die Kommandozeile trägt sie nicht), öffnet
    das Fenster nicht. Die Ladeprüfung der Läufer-Ablage (`client.spekulativesLaden`) lehnt zusätzlich
    `<link rel=prerender|prefetch|preconnect|dns-prefetch>` und `<script type=speculationrules>` ab; das ist eine
    kleine Zusatzschicht gegen statische Seiten, keine Sperre: dynamisch per Skript erzeugte Elemente deckt sie
    nicht ab. Grenzen von Schicht 2: Sie schützt gegen IT-Richtlinien und Versehen, nicht gegen Prozesse im selben
    Konto (die bleiben A1). Zwei Richtlinien (`ProxyMode`, `AdditionalBrowserArguments`) wurden gemessen und öffneten
    sie nicht; ob WebView2 sie überhaupt liest, ist unbelegt. Eine Übersteuerung, die nicht in der Kommandozeile
    sichtbar wird, erkennt die Prüfung nicht. `JNPT_NATIVE_WEBVIEW_PROBE_MODUS` (`beide`, `nurSchicht1`,
    `nurSchicht2`) fährt `FremdserverNull` mit genau den gewählten Schichten (etwa unter einer vom Admin gesetzten
    Richtlinie). Kontrollen je Schicht: `TestWindowsWebViewSchichtenKontrollen`
    (Seite ohne CSP: ohne Schichten sieht der Server den Abruf; mit nur Schicht 1 meldet die Spur die Anfrage als
    blockiert und es gibt 0 Treffer, `ok` der Seite zählt dort nicht, weil die lokale 403 einen no-cors-Abruf als
    geglückt erscheinen lässt; mit nur Schicht 2 scheitert der Abruf im Netz); `TestWindowsWebViewBrowserpruefungFailClosed`
    (widersprechendes Argument und `WEBVIEW2_*` lassen das Fenster nicht öffnen).
    **Berechtigungen, Maßstab ist die Wirkung:** `TestWindowsWebViewBerechtigungenAbgelehnt` prüft je Berechtigung, ob die
    Seite etwas bekommt. Zwischenablage: ein synthetischer Wert liegt vorher in der Zwischenablage des Gasts, die Kontrolle
    (`zwischenablageKontrolle`, Berechtigungen erlaubt) muss ihn lesen, sonst sagt „erhält nichts“ nichts. Geolocation:
    Ein Rückruf der Seite (Fehlercode, nie Position) ist der Beleg, Zeitablauf nie; bleibt die Abfrage hängen, ist der
    Fall rot und nennt die Windows-Standort-Einwilligung. „Ereignis lehnt ab“ (PermissionRequested mit Deny) und „Browser
    verweigert selbst“ (die Seite erhält nichts, ohne Ereignis) werden getrennt gemeldet; nur das erste belegt die
    Ereignis-Sperre. Lehren aus Lauf 8: Eine Seite im Hintergrund drosselt Zeitgeber auf 1 s, deshalb wartet die Seite
    auf den Fokus nach Zeit (5 s), nicht nach Zählern; die Sonde bringt ihr Fenster vorher in den Vordergrund
    (`forceForeground`, Alt-Trick, nur Sonde, das Ergebnis steht im Protokoll). Steht die Windows-Standort-Einwilligung
    der Maschine (`HKLM\...\ConsentStore\location`) auf `Deny`, bleibt die Standortabfrage der Seite bei `prompt` hängen, ohne
    PermissionRequested und ohne Rückruf (Lauf 8); steht sie auf `Allow`, verlangt die Sonde `PermissionRequested art=3`
    mit Deny und den Rückruf `PERMISSION_DENIED`. Ohne Rückruf bleibt der Fall rot und sagt „unbelegt“.
    Lauf 10: Zwischenablage und Standort laufen in der Sonde im `click`-Handler der Seite (Nutzeraktivierung und Fokus);
    die Seite meldet `document.hasFocus()` im Handler (`klick`), und der Host liefert den Klick auf die Fläche des
    Fensters (im Sekundentakt). Kam nie ein Klick, ist der Fall rot („kein Klick, unbelegt“), nie grün. Ein Fenster im
    Vordergrund hat noch nicht den Fokus der Seite: Das Fenster gibt ihn der WebView beim Anzeigen und bei `WM_SETFOCUS`
    (`s.chromium.Focus`, MoveFocus programmatisch; `TestSurfaceGibtDerSeiteDenFokus`), sonst landet Tastatureingabe nicht
    in der Seite, bis jemand hineinklickt (auch für Nutzer ein Mangel, nicht nur für die Sonde). Die Geolocation-Sonde
    nennt zusätzlich den Zustand des Dienstes `lfsvc` und die Standort-Einwilligung.
    **Lauf 12, echter Produktfehler:** Die WebView wurde nie sichtbar geschaltet (`seite_zustand` meldete `hidden` in allen Fällen,
    Klicks erreichten die Seite nicht, das Fenster blieb weiß). Der Schritt `show` ruft jetzt `Chromium.SetVisible(true)`
    (`PutIsVisible`) vor dem Fokus auf, bei Fehler öffnet das Fenster nicht; `WM_SIZE` schaltet beim Minimieren unsichtbar und
    beim Wiederherstellen sichtbar (`TestSurfaceSchaltetDieWebViewSichtbar`). Für alle Sonden gilt: Jede Seite meldet
    `visibilityState` über `probe.js`, sonst ist die Sonde rot; `TestWindowsWebViewSichtbarMitFarbe` liest dazu im
    Bildschirmbild die Mitte der Fläche (zwei Farben als Kontrolle). Gefährdet durch `hidden` waren: Berechtigungen
    (Chromium vertagt Abfragen für unsichtbare Seiten, passt zur hängenden Geolocation), die Vektoren des spekulativen
    Ladens (Prerender, Prefetch und Preconnect hängen am Zustand der Seite), Zeitgeber (auf 1 s gedrosselt),
    Eingabe und `hasFocus`, der Prozessbaum (andere Renderer-Prozesse). Das sind die Fälle, die nach dem Fix neu zu fahren sind.
    **SEC-R4-Auflagen (Fundstelle, Test, Mutant):** F1 Die Netzsperre trägt als Pflichtargument auch die Richtlinie für WebRTC
    (`NetworkLockArgs`, `BrowserCommandLineProblems`; `TestBrowserArguments`, `TestBrowserCommandLineWebrtcPflicht`;
    nativ `TestWindowsWebViewWebRTCNull` und die Kontrolle `TestWindowsWebViewWebRTCKontrolle`; Wirkt das Argument in
    WebView2 nicht, geht es an SEC zurück, es gibt keine Umgehung). F2 Die Kommandozeilenprüfung normalisiert Schalter
    (`--x`, `-x`, `/x`, Groß- und Kleinschreibung; `TestBrowserCommandLineAndereSchreibweisen`, `TestSplitCommandLine`).
    F3 Die zwei Aufrufe aus v1.0.19 sind ersetzt (`TestEdgeOhneDieZweiUpstreamFehler`), Ergebnis des Prüfpunkts für
    v1.0.19 bis v1.0.23 in `internal/edge/doc.go`. F4 `TestWindowsWebViewEinstellungenZurueckgelesen` liest alle 14
    Einstellungen der Sollliste zurück. F5 Aussagen korrigiert (`TestKeineFalschenAussagen`), der Klartext-Puffer von
    `credential` wird genullt (`TestStrictUndLoeschenNullt`, `TestReaderIdentityLoeschtDenKlartext`). F6 Das Fenster einer
    App schließt, wenn sie aus der Liste verschwindet (`CloseMissing`, `TestCloseMissingSchliesstNurVerschwundene`); fehlt
    die Zustandsdatei, wird die Liste sofort leer. Die Mutanten stehen in `probe/k6b_mutations.py`.
    Lauf 11: `WM_SETFOCUS` kam während `Close` und traf den geleerten Controller (`Chromium.Focus`); im Bedienfeld hätte das den
    Prozess samt Halt beendet. Jetzt prüft jede Methode der Kopie, die `e.controller` oder `e.webview` aufruft, das Feld
    vorher auf nil (`TestEdgeMethodenPruefenControllerUndWebviewAufNil`); ungenutzte Methoden ohne Prüfung sind entfernt.
    Alle Fensternachrichten in `surfaceProc` (`WM_SIZE` über `SetSize`, `WM_SETFOCUS`, `WM_CLOSE`, `WM_DESTROY`) sind damit
    gegen das Zeitfenster nach `Close` gesichert; `Pump` und die Ereignishandler prüfen `closed` bzw. den Host. Der
    Schritt `show` holt das neue Fenster in den Vordergrund (SetForegroundWindow, das Bedienfeld hat gerade die Eingabe
    des Menüklicks) und gibt der WebView den Fokus. Die Sonde meldet je Fenster die Lage (`Fenster rect=… flaeche_mitte_bildschirm`)
    und die Eingabenachrichten (`WM_PARENTNOTIFY maustaste`, `WM_LBUTTONDOWN`, `WM_ACTIVATE`, `WM_SETFOCUS`), die Seite die
    erste Aktivierung (Klick, Maus- oder Zeigertaste, Taste) mit `hasFocus` und ihren Zustand (`seite_zustand`, `fenster_fokus`).
    Eine verworfene Webnachricht (Quelle nicht lesbar) nennt in der Spur das HRESULT, ob der Zeiger leer war und die
    Seite, auf der die WebView steht (`wv2com.ReadString`); verworfen wird weiterhin fail-closed, nie zugelassen.
    Service Worker: `ClearBrowsingData` (Profile2.ClearBrowsingDataAll) wird für Service Worker nativ geprüft
    (`TestWindowsWebViewServiceWorkerWirdGeloescht`). Läufe der nativen Sonden brauchen `-test.timeout 20m`.
    Grenzen: alte Fassungen
    unter `apps\` werden nicht aufgeräumt; die Ereignisse sind unter Linux nur als Urteile geprüft, ihre
    Bindung belegt allein die native Sonde (`webview_native_windows_test.go`, Opt-in).

    **K6c P3 (persönliche Anmeldung, Vertrag 5/P2):** `internal/anmeldung` hält PKCE/State
    und prüft den Rückruf. Der native Anmelden-Knopf (b oder fehlendes/leeres mode;
    in a keine Leiste/Knöpfe/Meldungen) startet `login_windows.go` in einer
    Goroutine: `/login` mit Challenge/Port/State, geprüfte HTTPS-URL ohne Userinfo über
    ShellExecuteW, passender Loopback-Rückruf, `/session` mit Code/Verifier. Kein JWT,
    kein Refresh, keine Logout-Route; Abmelden vergisst das Token lokal.
    Genau ein `net.Listen("tcp4", "127.0.0.1:0")` in `internal/fensterio/loopback_windows.go`;
    dort allein die neue net/http-Server-Ausnahme mit ReadHeaderTimeout/MaxHeaderBytes,
    kein Client. Ab Klick höchstens fünf Minuten oder erster passender Rückruf;
    falscher State/Host: 400, Listener bleibt offen. `TestKeineListenerDownloaderOderPip`
    zählt auch null als Fehler. Fehlerrücksprung nur feste Liste ohne Code (A3).
    Anzeigename max. 128 Unicode-Zeichen ohne Steuer-/Formatzeichen, ausschließlich
    native Leiste. `person_gewechselt` entscheidet, fehlend/null bedeutet true (A4).
    Bei Wechsel/Abmelden: Gateway sperren, Host-Epoche wechseln, alte Antworten verwerfen,
    altes Dokument durch about:blank beenden, ClearBrowsingDataAll erfolgreich abwarten,
    erst dann Sitzung freigeben und neu laden (A5). Nur dieselbe weiterhin angemeldete
    Person behält Seite/Eingaben; ohne lokale Anmeldung neu laden, auch wenn
    `person_gewechselt=false`. `ErrNichtAngemeldet` der persönlichen Sitzung entfernt
    Konto und „Angemeldet“ aus der Leiste. Nur `/session`-403 `kein_zugriff` wird zu
    Zeitablauf (A8); Callback-Zugriffsfehler bleiben unverändert. A9: laufende und
    bereits wartende Antworten über Login-Wechsel geprüft (`login_r4_test.go`,
    Mutanten `login-suspend`/`host-suspend-out`).
    Auch beim Öffnen alte Profildaten löschen, ausdrücklich auch in Modus a. Keine Lockerung der Browser-Netzsperre;
    Service Worker umgehen Schicht 1, dort trägt Schicht 2 allein. B1–B4 gelten unverändert.
    Details/Tests: [fensterio/CLAUDE.md](internal/fensterio/CLAUDE.md).
    `TestWindowsWebViewAnmeldeleisteNachModus`, `TestWindowsWebViewPersonenwechselUndAbmelden` und `TestWindowsWebViewAnmeldeknopf` sind opt-in-Sonden für BOX,
    hier nur kompiliert. Lokales Adapter-Golden `internal/client/testdata/contract5_login_adapter.json`;
    bytegleicher P2-Kernspiegel aus ea50aa7a in `contract/golden-v5/contract5_login_routes.json`,
    SHA-geprüft durch `TestGoldenV5AusKern` und `SHA256SUMS`, mit `-text` gegen autocrlf geschützt.

19. GUI-Läufer und Hinweis bei neuer Rückfrage (#1240 A2, nur Windows):
    **Warum GUI.** Die Exe 0.4.0 war ein Konsolenprogramm (PE-Subsystem 3): Die Benutzeraufgabe öffnete
    bei jeder Anmeldung ein leeres Terminalfenster, dessen Schließen `run` bis zur nächsten Anmeldung
    beendete. Beide Exe werden deshalb mit `-H=windowsgui` gebaut (`TestReleaseUndWindowsCI` prüft jede
    `go build`-Zeile, Mutanten `runner-build-console`/`panel-build-console`); `-X main.version` bleibt.
    **Konsolenanschluss** (`console.go`, `console_windows.go`, `console_other.go`): `attachConsole()` ist die
    erste Anweisung von `main()`. Die reine Funktion `planConsole(args, stdoutValid, stderrValid)` entscheidet:
    `run` (jedes `args[0] == "run"`) hängt sich nie an; gültige Standard-Handles (Pipe oder Datei, z. B. `| Out-Host`,
    Installtest, Umleitung) bleiben unangetastet; je fehlendem Handle (NULL, ungültig, `FILE_TYPE_UNKNOWN`)
    wird `AttachConsole(ATTACH_PARENT_PROCESS)` aufgerufen und nur dieser Kanal auf `CONOUT$` gesetzt. Jeder
    Fehler läuft still weiter, der Exit-Code ändert sich nie. Nicht-Windows-Builds (Linux-Server) sind ein No-op.
    **Folgen für den Betrieb:** Eine GUI-Exe in einer Shell wird nicht abgewartet (PowerShell: `| Out-Host`);
    `run` im Vordergrund hat keine Konsole, Strg+C erreicht ihn nicht mehr (Ende: Abmelden oder
    Prozess beenden; `uninstall` beendet nur das Bedienfeld, Invariante 17). `GenerateConsoleCtrlEvent` und eine Konsole als Voraussetzung gibt es im Code nicht;
    Kindprozesse laufen weiter mit `CREATE_NO_WINDOW` und expliziten Pipe-Handles (`process_windows.go`).
    **Hinweis.** Neue Rückfrage (`jnpt.frage`) öffnet kein Fenster. Damit der Mensch sie nicht verpasst, meldet
    `Model.TakeNotice()` genau einmal je angezeigter neuer Rückfrage einen fälligen Hinweis; `render()` setzt ihn
    unter `a.tray && TakeNotice()` als eigenen `NIM_MODIFY` mit `NIF_INFO` (`notify`, `NIIF_INFO|NIIF_NOSOUND`).
    Text fest im Code (`panel.NoticeTitle` „JanuaPort“, `panel.NoticeText` „JanuaPort braucht eine Antwort“),
    **nie** Titel, Text oder Pin der Rückfrage, weil der Hinweis sichtbar auf dem Bildschirm steht; der Inhalt
    bleibt in Menü und Dialog. Der Zeiger auf den Dialog, nicht die Frage-ID, trägt die Einmaligkeit (IDs
    zählt jedes Skript ab „1“). Entfällt die Frage vor dem Abholen (Entfernung, Frist, Anhalten, Trennung),
    gibt es keinen Hinweis; eine zweite, nicht angezeigte Frage meldet nichts. `trayFlags` bleibt
    `1 | 2 | 4`; NIF_INFO steht nur in `notify`. Weiterhin keine Aktivierung, kein `SetForegroundWindow`,
    `WS_EX_NOACTIVATE` bleibt, der Dialog öffnet nur per Klick. Wächter: `TestFokusWaechter`
    (Flags nur `trayFlags` in `render`, `nifInfo` nur in `notify`), `TestHinweisNurAnEinerStelle` (fester Text,
    ein `NIM_MODIFY`, `notify` einmal in `render` unter `TakeNotice`), Modelltests `TestHinweis*`; Mutanten
    `balloon-flag`, `balloon-in-render`, `notice-*` in `probe/l4_mutations.py`. Ob Windows den Hinweis zeigt
    (Fokus-Assistent, Benachrichtigungseinstellungen), ist nur nativ prüfbar.
    **Symbol nach Autostart:** `render()` setzt `a.tray` aus `panel.TrayUpdate(a.tray, …)`: Scheitert die erste
    Operation (ADD/MODIFY), folgt die andere, weil das Symbol beim ersten Neustart nach `install` schon da sein
    kann; sonst bliebe `tray` falsch und `TakeNotice` würde nie geprüft (Test `TestTrayUpdateVersuche`).

## Benannte Grenzen

Widerruf wird offline erst beim Kontakt sichtbar, bis zur Gnadenfrist kann
das Gerät weiterlaufen. Ed25519-Rotation v1 nur neue Kopplung. Workstation-
Skripte handeln mit den Rechten der Person. DPAPI CurrentUser schützt nicht
gegen jemanden mit Zugriff auf diese Sitzung (C4).

A1 / SEC K6b (c): **ACL schützt nicht gegen den WebView2-Hauptprozess**.
Er läuft mit den Rechten derselben Person auf Medium-IL. Client-Bildprüfung
ist Tiefenverteidigung, keine Grenze gegen gleichberechtigte Prozesse,
UI-Automation oder Austausch von Dateien im eigenen Ordner. Die Medium-
Integritätsmarke schützt gegen lesende und schreibende Low-IL-Clients.
Störung wird in L nur lokal angezeigt und nicht an den Kern gemeldet (§6.6); eine Pipe kann durch einen gleichberechtigten Prozess
belegt oder blockiert werden. L4 zeigt dauerhaft eigene Farbe und exakt
„Störung. Anhalten: abmelden oder über den Admin halten lassen.“
UIA-Skripte desselben Benutzers können das Bedienfeld bedienen; eine Pause kann
das laufende Skript selbst aufheben, endgültig für den Baum ist nur Anhalten.

Anhalten beendet den Job-Baum. Prozesse, die ein Skript außerhalb über WMI,
Aufgabenplanung oder COM gestartet hat, überleben. Bei gesperrter Sitzung zeigt
auch ein Screenshot den Sperrbildschirm.

Prüfpunkt je Läufer-Release: `TestWindowsWebViewWebRTCNull` samt Kontrolle läuft auf dem Testgerät gegen die aktuelle
WebView2-Runtime. Die Kommandozeilenprüfung sieht nur das Argument, nicht seine Wirkung, und die Runtime aktualisiert
sich selbst; ein Bestehen gilt deshalb nur für die gemessene Fassung.

G-L3: Vor dem ersten Geräte-Rollout müssen die nativen Opt-in-Proben mit
`JNPT_NATIVE_TOKEN_PROBES=1` und die Bedienfeld-Prozesssonde auf dem Testgerät
mit PASS ohne SKIP laufen. CI/Kompilierung ersetzen diesen Gate-Beleg nicht.

## Tragende Adaptertests

| Invariante | Tests |
|---|---|
| Anmeldeleiste #1321, mode nur a/b/leer, alte Kerne und Manifestwechsel | client: TestManifestAppModeAmHTTPAdapter, TestManifestV5GoldenRoundtrip; fensterio: TestRuntimeAppModeAusAktuellemManifest, TestRuntimeAppModeUmgehtPersonRequiredNicht, TestRuntimeWatchMeldetModuswechsel, TestAppModeNativVerdrahtet; Windows ohne GUI: TestSurfaceAnmeldeleisteNachModus, TestSurfaceModusAErzeugtKeineControls, TestSurfaceAnzeigemodusVerwirftAltenStand; BOX: TestWindowsWebViewAnmeldeleisteNachModus; Mutanten: leiste-immer, app-mode-ungueltig, anzeige-alter-stand |
| B1: Git vor Spawn, Objektmenge/Modi/Namen | TestFalscherHashBrichtVorDemStartAb, TestPaketObjektmengeModiUndNamen, TestSHA1Collision, TestPaketMedientypAmHTTPAdapter, TestGoldenPaketeBytegenauUndManipulation |
| Runtime/Lock/Umgebung | TestPythonAllowlistUndWheel, TestUmgebungVorJedemStart, TestUmgebungFehlendeZusaetzlicheDateien, TestWheelTagsLockUndManifestVerbote, TestWindowsPythonEchterStartUndManipulation |
| ZIP-Namen/Typen/Kollisionen/Bomben | TestZIPAbweisungAmEntpacker; Kopien: TestCopiedLibraryFunctions |
| E3: TLS vor Code, CA/Pin, keine Redirects/Proxys, Pubkey nötig | TestPairTLSUndLeererPubkey, TestCAFingerprintProxyUndURL, TestRedirectUndRemoteHTTPAbgewiesen, TestPairCodeFormat, TestPairLeererPubkey, TestPairRFC8032Pubkey, TestPairLeererPubkeySpeichertNichts, TestPairRFC8032PubkeySpeichertNichts |
| E1: Gerät/seq/Signatur, alter Sync unverändert | TestManifestAblehnung, TestManifestSyncAtomarUndOfflineAmAdapter, TestVeralteterStartSynchronisiertErneut; Kopien: TestCopiedContractFunctions |
| Konto/Sitzung/idle vor Spawn | TestKontoUndManipulationVorSpawn, TestReadyVerboteneZustaende, TestIdleVorSpawnUndMount, TestIdleWirdNieAlsAccountGemeldet |
| Rückstau-Grenze abgelehnter Ergebnisse (L2) | TestAbgelehntesErgebnisStautDenRueckstauNicht, TestAltKernLehntIdleAbJournalStautNicht, TestVerwurfWirdLokalGeloggt |
| Lauf-TEMP (L3–L6) | TestRunIDFormat, TestRunTempsCreateVerlaesstDieBasisNie, TestRunTempsAnlegenNeuUndExklusiv, TestRunTempsRemoveFolgtKeinemVerweis, TestRunTempsSweepRaeumtRestePlusVerweiseOhneEinzusteigen, TestLaufBekommtPrivatesTempAlsArbeitsverzeichnis, TestLaufTempWirdBeiJedemEndeAufgeraeumt, TestKindUmgebungOhneProfil; Windows: TestMkdirPrivateExklusivUndGeprueft, TestRunTempJunctionKanarienvogel, TestWindowsEnvOhneProfilUndTemp, TestWindowsPythonTempProLauf |
| B2/B3 Windows: genau ein lokaler Pipe-Listener, genau ein kurzlebiger Windows-Loopback-Listener (K6c), keine Downloads/privaten Bodys/Kind-Token | TestKeineListenerDownloaderOderPip, TestNurAnlageUndKeinePrivatenBodys, TestWindowsPythonEchterStartUndManipulation, TestWindowsJobObjectEnkelUndToken |
| Cron/DST, Offline-Grenze und Journal | TestCronZeitzoneUndSlots, TestOfflineRegel, TestOfflineVerweigertWirdJournalisiert |
| E4: Ciphertext-Deckel, Bestätigung, Neustart | TestJournalDeckelBestaetigungNeustart, TestJournalMisstVerschluesselteBytes, TestWindowsNeustartInterrupted, TestErgebnisrueckstauUndAtomarerSlot |
| Widerruf an allen Wegen, keine Wiederbelebung | TestWiderrufAnAllenHTTPWegen, TestWiderrufAllesWeg, TestRevokeFolgtKeinemVerweisInTmp, TestRevokeFolgtKeinerJunctionInTmp (Windows) |
| Native DPAPI/ACL/Entropie/Prozessbaum | TestWindowsFehlerBehaltenUrsache, TestPrivateACLAliasUndVerbote, TestPrivateACLAmDateisystem, TestSecureRootVerweigertJunction, TestDPAPIUndACL, TestWindowsDPAPIBrauchtEntropie, TestWindowsSuspendBisJobZuordnung, TestWindowsJobCloseBeendetEnkel |
| Task ohne Admin und begrenzter Rückfall | TestWindowsAufgabeOhneAdmin, TestAutostartRueckfallNurBeiRichtlinie |
| Vertrag 4 / Testweg / K8 (#1240 L1) | TestVertragJeGeraeteart, TestGoldenV3AusKern, TestManifestV4StriktAmAdapter, TestTestgeraeteSynchronisieren1298, TestScreenshotNurTestgeraet, TestErgebnisNurTestauftrag, TestTestadapterVerweigernFalscheBindung, TestSetupUndHaltAmHTTPAdapter, TestSetupSchemaAmAdapter, TestSetupStartVerweigertFremdeFreigaben, TestL1WiderrufAmHTTPAdapter, TestOfflineFristJePinUndUhrMarke, TestOfflineNachmeldungAmAdapter, TestK8ArchivLaengsteWirksameFrist, TestErgebnisNachNeustartMitSignierterFreigabe, TestVertrag4ClaimUndErgebnisOhneServerfelder; Windows-CI: TestWindowsK8UhrmarkeDPAPINeustart |
| Lokaler Halt B3/B6, L2-1/L2-2 (#1240 L2) | TestHaltUeberstehtNeustart, TestHaltDateiFehltOderDefekt, TestHaltZwischenClaimUndSpawn, TestHaltBeiWriteFrameFehler, TestAnhaltenBeendetLaufOhneNetz, TestPauseLaesstLaufendenZuEnde, TestFortsetzenHoltNichtsNach, TestHaltBefehleStartenNie, TestPauseEndetAutomatisch, TestHaltMeldungKoalesziert, TestHaltRunSperrtClaimsUndHaeltHeartbeat, TestHaltSpeicherfehlerSperrt, TestHaltMeldungNeustartUndBestaetigung, TestHaltMeldungOhneDauerhafteBremseGesperrt, TestHaltBrichtOffenenClaimAb, TestHaltDirekteStartwegeGesperrt, TestPauseNeustartUndAblaufOhneNachholen, TestHaltMeldungWiederholtFehler, TestHaltLaesstServerUnveraendert, TestHaltSperrtSlotreservierung; Windows-CI: TestWindowsAnhaltenBeendetEnkel |
| Installation/Release, Plan 19 | TestInstallBeideOderVorherigerStand, TestInstallOhneBedienfeldAendertNichts, TestInstallBeideMitRichtlinienRueckfall, TestUninstallBeideAuchBeiFehler, TestBedienfeldAufgabeOhneArgumente, TestReleaseUndWindowsCI; Windows: TestWindowsAufgabeOhneAdmin/L6, TestWindowsUninstallBeendetNurEigenesBedienfeld |
| Hygiene | TestLoggerDeckelUndFesteCodes, TestStriktesJSONAmAdapter, TestFrameGrenzen, TestReleaseUndWindowsCI |
| Pipe B1/B2, L3-1/L3-2, H1–H3 (#1240 L3) | TestPipeDACLStruktur, TestPipeBesetztStartetOhnePipe, TestClientPrueftServerbild, TestPipeBefehleGeschlossen, TestPipeGolden, TestPipeRoheRahmenAmLaeufer, TestPipeOhneTraeger, TestSetupStartNurAusManifest, TestSetupStartAmPipeHTTPAdapter, TestSetupHaltWaerendHTTP, TestSetupVeralteteFreigabeStartetNicht, TestSetupFreigabeDirektVorSpawn, TestSetupSperrtUndBrichtClaimAb, TestSetupSperrtAndereStartwege, TestSetupDirektesExecuteBleibtGesperrt, TestSetupFehlerNieOfflineNachmelden, TestPipeHaltBefehle, TestSkriptRoheTestfunktionenBleibenGesperrt, TestPipeKindHandleListe; native Windows-Sonden siehe README „Lokale Pipe“ |
| Bedienfeld, Plan 14/15/18, §6.6, H4 | TestPanelZustaende, TestPanelMenueBefehleUndSetupEingabe, TestPanelFrageNurLokal, TestPanelStoerungBleibtBisNeuGeprueftemZustand, TestPanelServerpruefungSperrtBefehle, TestServerpruefungSendetNichtsAmAdapter, TestVerbindungVerwirftAlteBefehleUndBrichtSchreibenAb, TestBlockiertesSchreibenTrotzHeartbeatWirdGetrennt, TestFokusWaechter, TestBedienfeldNurGepruefterDial, TestPanelImportWaechter, TestL4SchemaPflichtfelder, TestH4RoheFragenAmLaeufer, TestH4SkriptTitelAbgewiesen, TestH4RoheFrageGrenzen, TestPanelEingabeAusVerifiziertemManifest; Windows: TestWindowsBedienfeldProzesssonde |
| GUI-Läufer und Hinweis, Invariante 19 (#1240 A2) | TestKonsolenplan, TestMainHaengtKonsoleZuerstAn, TestReleaseUndWindowsCI, TestHinweisFesterText, TestHinweisEinmalJeNeuerFrage, TestHinweisNurFuerAngezeigteFrage, TestHinweisNurAnEinerStelle, TestFokusWaechter; Windows: TestHandleBenutzbar (nativ) |
| Rückfragen intern, L3-Anteil | TestFrageNurLokal, TestFrageFristUndDisconnect, TestPipeFrageAntwortWeiterleitung, TestPipeHaltSchliesstFrage |
| L5 SEC-R4 F1/H2 | TestL5EndaufnahmeNachHaltUndWandzeit (Anhalten: 0 Aufnahmen/Uploads; Wandzeit: 1, ohne Engine-Sperre), TestL5LaufpinSeparatGebunden (verified gültig, nur Laufpin abweichend); Mutationen completion-halt, completion-wall-context, run-pin-binding |
| PC-Fenster K6b-2 (SEC B1–B4, native Ereignisse) | fensterio: TestHostReceiveBlockiertNie, TestHostAnhaltenBleibtBeiHaengendemGatewayBedienbar, TestHostFailBrichtAbUndNimmtNichtsMehrAn, TestRuntimeOpenAsyncKehrtSofortZurueck, TestRuntimeSchreibtNichtsInDenZustand, TestRuntimeWiderrufLoeschtNichts, TestRuntimeAppsOhneTraeger, TestPanelExchange*, TestNavigationVerdict, TestNewWindowVerdictIstImmerAbgebrochen, TestDownloadVerdictBrichtImmerAb, TestMessageSource, TestRunStepsHaeltBeimErstenFehlerAn, TestAdapterBindetAlleEreignisse, TestAdapterWendetAlleSettingsAn, TestFensterioRuftNurLeserUndAppAPI, TestBedienfeldImportiertClientNicht, TestPlatformNurHinterDerTuer, TestBedienfeldUIThreadOhneNetzUndBruecke, TestFensterioKeinAbbruchUndKeinPanel; panel: TestPanelImportWaechter, TestFensterioNurUeberDieTuer, TestPanelApp*; client: TestReader*, TestSyncLegtAppsAb, TestRevokeLoeschtWebviewOhneVerweiseZuFolgen, TestAppPrepareNachWiderrufLegtNichtsAb; fenster: TestExternalLink, TestDecide, TestLayout; Windows nativ (Opt-in): TestWindowsWebView* |
| PC-Fenster K6b-1 (Plan 4c/5, SEC K6b) | fenster: TestCSPExakt, TestNavigationAllowed, TestMessageAllowed, TestSettingsSollliste, TestUmschlagStrikt, TestUmschlagGrenzeGenau256KiB, TestBrueckeAntwortOhneTraeger, TestBrueckeUnerlaubteQuelleBleibtStumm, TestFensterImportWaechter; fensterio: TestLokalGenauZweiOperationen, TestLokalAnhalten, TestGatewayHolTokenEinmalUndSendetNurZweiTraeger, TestGatewayHoltBei401NeuUndWiederholtEinmal, TestGatewayZweite401IstNichtAngemeldet, TestGatewayFehlerOhneTraeger; client: TestGoldenV5AusKern, TestManifestV5StriktAmAdapter, TestAppTokenAmHTTPAdapter, TestAppTokenAntwortStrikt, TestAppRPCAmHTTPAdapter, TestAppPaketGuterFallLegtPrivatAbUndVersiegelt, TestAppSiegelVorJedemOeffnen, TestAppPaketAblehnungenLegenNichtsAb, TestAppCSPWirdEingesetzt, TestAppCSPAbweisungen, TestLeadingCSP, TestAppCSPJedeHTMLDatei, TestAppSiegelDecktEingesetztesLiteral, TestAppOpenPrueftFuehrendesLiteralTrotzNeuemSiegel, TestEnsureHoltPaketUeberHTTPNurEinmal, TestCopiedApphostFunctions |
| Helfer L5, Plan 3/15/16/18, L3-2/H3 | TestScreenshotNurTestgeraet/roheRahmen, TestErgebnisNurTestauftrag/roheRahmen, client.TestFrageNurLokal (echter Python-Bootstrap), TestL5ErgebnisNullUndGrenzen, TestL5ScreenshotGrenzen, TestL5RahmenStrikt, TestL5BootstrapModule, TestL5TestauftragAmHTTPAdapter, TestKeineListenerDownloaderOderPip, TestScreenshotPNGBufferGrenze, TestScreenshotLinuxStub; Windows nur kompiliert: TestWindowsScreenshotGDI, TestWindowsScreenshotGDIChild, TestWindowsPythonKindOhneBedienfeldPipe |

Übernommene Funktionen sind im Dateikopf markiert. Wächter vergleichen die
vollständige Funktionsmenge und formatierten Funktionszeilen mit D0 bzw.
script-runner/internal/libs. Dokumentierter Delta im Git-Prüfer: 30.000
Baum-Besuche gegen wiederholte DAG-Expansion. Die eingebettete Allowlist wird
byte-genau mit dem Vertrag verglichen. Der Windows-Bootstrap ist eigenständig
für Pipes implementiert.

Engine.Execute (Windows) prüft Konto/Leerlauf, Paket, Paketmanifest und Umgebungsseal,
legt das Lauf-TEMP an und schreibt den Laufmarker vor Spawn. Lauf-Token sind kein Feld des Windows-Launch.
Der Windows-MCP-Weg erhält sie ausschließlich im HTTP-Header. runMu und Benutzermutex
verhindern parallele Starts; beide Startwege verwenden dasselbe Journal.
Slot und claimed-Marker werden atomar reserviert. Ergebnisnachlieferung ist
auf einen Eintrag je Schleife begrenzt, damit der lokale Takt weiterläuft.
409 manifest beim geplanten Start synchronisiert und bindet denselben Slot
einmalig neu, bevor ein zweiter Startversuch erfolgt. Ungültige Syncs ändern
weder Manifest noch LastSync. Verifizierte Umgebungen erhalten eigene
DPAPI-Metadaten für Wiederaufnahme eines abgebrochenen Mehr-Pin-Syncs.
State.Use serialisiert Cache-Schreiber gegen Widerruf. Prozessanlage und
Widerrufsabbruch verwenden dieselbe Engine-Sperre. Der lokale Halt prüft und
beendet unter dieser Sperre (L2-1), bevor ein weiterer Prozess entstehen kann.

Die Journalgrenze gilt für die veröffentlichte Ciphertext-Datei einschließlich
Marker, Slot-Merker und Verlustzähler. Atomare Ersatzdateien sind unter Windows verschlüsselt, unter Linux authentisiert;
verwaiste Ersatzdateien werden beim exklusiven Start entfernt. Unter Windows liegen Credential,
Config/Manifest und Umgebungsseals in DPAPI-Dateien. Nur Paket- und
Laufzeitcode liegt im Klartext. Status prüft Dateiexistenz und Autostart,
ohne den Journalbestand zu öffnen oder zu bereinigen.
`TestWindowsStatusBleibtVorStateLockUndNetz` schützt diesen frühen Lesepfad
auch unter Linux; `TestWindowsStatusLiestKeineDPAPIDateien` führt ihn unter
Windows mit absichtlich ungültigem Credential, Journal, Manifest und Uhrmarke
aus und prüft, dass alle Dateien unverändert bleiben.

DACLs der Zustandsdateien werden strukturell geprüft: geschützt, genau eine explizite Allow-ACE,
eigene binäre SID, volle Dateirechte und ausschließlich OI/CI-Vererbung.
SDDL-Aliase (etwa SY/BA) sind keine andere Identität; ein Textvergleich
von SID-Darstellungen ist unzulässig. Reparse-Punkte bleiben
auf allen bestehenden Pfadkomponenten gesperrt. Windows-Diagnosen enthalten
nur feste Operationsnamen und OS-Fehlernummer/-text, keine Pfade oder Konten.
Die Pipe hat stattdessen genau zwei explizite ACEs und das Medium-Label
(Invariante 15). Die CLI-Dienstausgabe bleibt unverändert beim festen Fehlercode.
TestWindowsCIContext protokolliert nur erhöhte Rechte und Volumen-Gleichheit;
kein Test benötigt einen interaktiven Desktop. Die Standardkonto-Taskprobe
bleibt separat und verweigert weiterhin erhöhte Ausführung.

Beide Plattformen verwerfen stdout/stderr und januaport.log; log auf dem Draht
ist leer. Das Job Object ist keine Benutzer-/Netzwerksandbox (C4).
Zusätzliche Entpackdeckel und Pfadlängengrenzen stehen in README.md.

## Prüfen

Im Modul: go build ./..., go vet ./..., go test ./...; zusätzlich
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./... und unter Linux
go test -race -count=1 ./.... D0 bleibt ein eigenes Modul: dort
go test -count=1 ./..., go vet ./..., go mod verify. Normale Tests ändern
keine Goldens. Den Generator ausschließlich bei bewusster Vertragsänderung
verwenden und den SHA256SUMS-Pin überprüfen.

Native Taskprobe ausdrücklich per -run TestWindowsAufgabeOhneAdmin aktivieren,
ohne erhöhte Rechte. Python-Probe braucht die kuratierte ZIP in .probe/;
fehlende ZIP ergibt einen sichtbaren Skip. Windows-CI beschafft sie und
führt die Taskprobe in einem temporären Standardkonto aus.
Reale Kernintegration braucht externe Abnahme. Der Installationstest prüft das
signierte Release, ohne die Windows-CLI zu erweitern: Code nicht ausgeben,
argv-Sichtbarkeit ausdrücklich benennen. Status zeigt nur zwei Bools;
Journal entfernt bestätigte Ergebnisse sofort. Lokaler Lebensnachweis daher
über erfolgreichen Manifest-Sync, Auftrags-/Heartbeatbelege separat durch GATE.
Beide Autostartwege (COM und HKCU Run) in finally prüfen. Standardkonto ist Pflicht;
vorhandene Kopplung/Autostart erhalten. Aktive WTS-Sitzung/Kontobindung werden
nicht umgangen. Admin-Abbruch liefert aktuell cancelled; interrupted ist Wiederanlauf.
Offline: probe/installtest-tests.ps1, probe/installtest-mutations.ps1. Der Admin-
Negativtest samt Mutation braucht Admin-/UAC-Mitgliedschaft, sonst sichtbarer Skip;
Windows-CI führt die Offline-Adaptertests mit Mutationen aus.
Die echte Provenance-Negativprobe samt Mutation braucht explizit -ReleaseDirectory
mit Release-EXE/Bundle und gh-Zugang; sie führt die Release-EXE nicht aus.

Mutationen nur bei exklusivem Worktree-Zugriff: python probe/mutations.py.
Ein Mutant muss kompilieren und sein Zieltest tatsächlich fehlschlagen;
Compilerfehler zählen nicht. finally stellt Quelldateien byte-genau wieder her.
Nach externer Zwangsbeendigung den betroffenen Guard ausdrücklich prüfen.
Abschlussbericht ausschließlich im Chat, keine Berichtsdatei im Repository.

SEC-R4 F3 ergänzt Mutanten für Träger in JSON-Escapes und Schlüsseln (`traeger-unescape`, `traeger-schluessel`), `rpc-methode`, `rpc-id`, `rpc-version`, `rpc-ergebnis-und-fehler`, `http-beide-traeger`, `http-app-endpoint`, `http-app-praefix`, `gw-revoked-code`.
K6b-2 ergänzt Mutanten für Host (`host-*`), Exchange (`exchange-*`), Leser (`reader-*`), Laufzeit (`runtime-*`), Wächter (`ui-thread-liest`, `bedienfeld-importiert-*`, `panel-importiert-fensterio`), die Ereignisurteile und Pflichtschritte (`nav-*`, `neues-fenster-*`, `download-*`, `quelle-ignoriert`, `schritte-*`, `adapter-*`), Links, Layout und App-Menü.
Native Sonden (nur Test-VM, Opt-in `JNPT_NATIVE_WEBVIEW_PROBE=1`): `go test -c -o fensterio.test.exe ./internal/fensterio` auf dem Build-Rechner, auf der VM `fensterio.test.exe -test.run TestWindowsWebView -test.v`; das Binary braucht dort kein Go, nur die WebView2-Runtime und eine interaktive Sitzung; `PATH` darf kein `jnpt` tragen.
K6b-1-Mutationen: `python3 probe/k6b_mutations.py [name…]`, im Container `golang:1.26.10` mit
nachinstalliertem Python (Worktree als Mount, exklusiv). Jeder Mutant muss kompilieren und den
benannten Test rot machen; Compilerfehler zählen nicht; `finally` stellt die Bytes her. Reine
Linux-Proben, kein Fenster, kein Prozess. Die Mutationen für `WindowsContract` stehen zusätzlich
in `probe/l1_mutations.py` und `probe/l3_mutations.py` (Anker auf die Konstante umgestellt).

L6-Mutationen: `python probe/l6_mutations.py`, exklusiv in `l6-test`, Go 1.26.10.
Git-Archiv statt Windows-Bind-Mount; ausschließlich Container-Kopien mutieren,
kompilierende rote Zieltests und geprüfte Wiederherstellung. Installtest lokal
nicht ausführen, nur PowerShell-Syntax prüfen. Windows-Tests nur cross-kompilieren;
CI startet der LEAD. Kein Tag, Push oder Signieren durch den Implementierer.

L5-Mutationen: `python probe/l5_mutations.py`, exklusiv in `l5-test` mit
Go 1.26.10. Jede Mutation kompiliert und muss den benannten Test rot machen;
Quelldateien werden bytegenau wiederhergestellt. Python: `python3 -B -m unittest
discover -s internal/client -p test_bootstrap.py`. Native GDI-Proben nur durch
LEAD CI/GATE mit `JNPT_GDI_PROBE=1`; separater Hilfsprozess mit 15 Sekunden
Frist, sichtbarer Skip ohne Opt-in/interaktive Sitzung. Lokal nur cross-kompilieren,
niemals GUI oder Bildschirmaufnahme. H3-Handleliste/Env/argv bleiben unverändert.

L4-Mutationen: `python probe/l4_mutations.py`, exklusiv in `l4-test`, Go 1.26.10.
Kompilierende rote Mutanten, bytegenaue Wiederherstellung. Native Mutanten
lokal nur cross-kompilieren; Ausführung durch LEAD in CI, Fokus/Tray am GATE.

L3-Mutationen: `python probe/l3_mutations.py`, exklusiv im Container `l3-test`
mit `golang:1.26.10` und Repo-Mount `/workspace`. Jeder Linux-Mutant muss
kompilieren und seinen Zieltest tatsächlich rot machen; alle Bytes werden in
`finally` wiederhergestellt. Native Mutanten werden standardmäßig ausschließlich
cross-kompiliert; `--native` ist für die durch LEAD gestartete Windows-CI.
Native Kompilierung ist kein Laufzeitbeweis für ACL, IL, Authenticode oder H3.
Keine lokalen GUI-/Desktop-Proben, kein automatischer CI-Dispatch.

L2-Mutationen: `python probe/l2_mutations.py`, Container `l2-test` mit Go 1.26.10
und Repo-Mount `/workspace`, keine parallelen Tests/Mutationen. Der Test für
L2-1 erzwingt tabellarisch `angehalten` und `pausiert` während der Vorbereitung
nach einem echten HTTP-Claim und bewacht zusätzlich die gemeinsame Spawn-Sperre.
Die Mutation „Spawn-Gate nur für angehalten“ muss im Fall `pausiert` rot werden.
`TestHaltBeiWriteFrameFehler` prüft einen echten WriteFrame-Schreibfehler:
nach Anhalten `cancelled`, ohne Anhalten weiterhin `internal`; beide Zuordnungen
sind durch Mutationen abgesichert. Native Sonde
`TestWindowsAnhaltenBeendetEnkel` lokal nur kompilieren; LEAD startet Windows-CI.
Störung bleibt lokal; L2 liefert Logcode/Statusflag, L4 zeigt dauerhaft Farbe
und festen Ausweichtext (Invariante 16). Außerhalb des Job-Baums gestartete Prozesse bleiben
eine Grenze; Bedienung durch Prozesse derselben Person ebenfalls (README).

Native Windows-Mutationen zusätzlich: probe/mutations-windows.ps1 (ohne Admin).
Mutationsfälle für #1245 L: idle-before-spawn, idle-remap-account, result-reject-jam,
runid-format, runtemp-reuse, runtemp-follow-link, revoke-follow-link, child-env-profile,
windows-exclusive. Zu revoke: gegen `os.RemoveAll` bleibt der Test auf Go 1.26.10 und 1.27
grün (dort link-sicher); der Wächter schützt vor einem Rückbau auf einen folgenden Löscher.
Die Symlink-Tests von RunTemps überspringen unter Windows ohne Symlink-Recht sichtbar;
dort belegt TestRunTempJunctionKanarienvogel das Aufräumen an einer echten Junction.

## Linux-Server: zusätzliche Invarianten (JanuaPort/januaport#1195)

- Server ausschließlich Vertrag 2, Windows ausschließlich Vertrag 5 (bis K6b-1: 4). Header,
  Config-Geräteart und signiertes Manifest müssen zusammenpassen. Container-Pins:
  SHA256-Commit = Image-Digest, leerer Pfad, keine declared_tools, kein Paketweg.
  Kanonisierung unverändert; neues K1-Golden in contract/golden-v2 neben den
  unveränderten D0-Goldens und deren SHA256SUMS.
- D1: Container.Validate und dockerArguments bilden eine geschlossene Menge.
  Pin liefert ausschließlich Werte. CLI-Hostprozess über exec.CommandContext,
  nie Shell. Der feste Shell-Startcode IM Container entpackt ausschließlich den
  selbst erzeugten Tar-Stream; der signierte Befehl bleibt argv hinter `exec "$@"`.
  Kein freier Mount, Env, Socket, entrypoint oder Host-Namespace aus dem Pin.
  Der Läufer setzt fest --entrypoint /bin/sh; Image-ENTRYPOINTs werden überschrieben.
  Read-only, UID/GID 65532, ALL-Cap-Drop, no-new-privileges, init, rm, pids=128,
  memory-swap=memory, Pin-Grenzen und noexec/nosuid/nodev-tmpfs sind fest. Pull nur Digest, Vergleich
  der RepoDigests vor Start, danach pull=never. Vollständiges Argument-Golden.
- D2/Frage 3: Kein Gerätetoken-/Läuferverzeichnis im Job. Lauf-Token kommt über
  stdin-Tar in /run/jnpt/token, 0400, UID/GID 65532, nur tmpfs; kein Token in
  docker inspect. Image benötigt /bin/sh und tar; der ENTRYPOINT ist fest /bin/sh. docker cp ist
  wegen read-only/tmpfs-Semantik ungeeignet. Production führt kein docker exec aus;
  die Docker-Tests lesen damit die tatsächlichen Dateirechte im laufenden Job.
- D3: Attached run vermeidet die --rm/docker-wait-Race. kill und rm bei Ende,
  Abbruch und Wandzeit mit eigenem begrenzten Cleanup-Kontext. Neustart zuerst
  gelabelte Container entfernen, danach Journal Recover → interrupted; nie neu
  starten. Widerruf wartet auf Containerbereinigung, bevor State gelöscht wird.
  Fehlgeschlagener Cleanup sperrt weitere Containerstarts bis zum Neustart.
  Dedizierter Daemon je Läufer; das Label wird daemonweit aufgeräumt.
- D4: Nur Claim-Namen ∩ signierte Deklaration; Zeitplanfreigabe aus signiertem
  Manifest. Namen nach K1 (1–63, erster Buchstabe/Ziffer, kein .., max.16,
  eindeutig), lstat, reguläre Datei und symlinkfreier Betreiberpfad 0700/eigene UID.
  Nur read-only Bind nach /run/secrets/<name>, nie Werte in Env.
  Die Datei muss für Container-UID 65532 lesbar sein; rootless gelten die
  tatsächlichen Sub-UID/Sub-GID-Abbildungen (Beispiel und Rechte im README).
  Ergebnis sendet mounted_secret_names, auch []. Marker meldet für interrupted nur die leere, unbekannte Teilmenge;
  nachgewiesene Starts melden die tatsächlichen Mount-Namen.
- D5: Runtime beim Start aus Docker SecurityOptions bzw. Podman host.security
  erkennen, unbekannt/zu alt → refused/runtime. Pair/Heartbeat melden das Merkmal
  nur in Vertrag 2. Rootful wird ehrlich gemeldet und nicht gesperrt.
- D6: Linux-CLI verweigert UID 0 und Job-UID. systemd: eigener Nutzer,
  ProtectSystem=strict, NoNewPrivileges, keine Fähigkeiten. State/Journal 0700/0600,
  HMAC-SHA256 mit eigener 0600-Schlüsseldatei. Legacy-Windows-Adapter sind unter
  Linux gesperrt. Dieselben Bestandsdeckel einschließlich HMAC, Marker und Zähler.
  Persistente Sperre außerhalb des widerrufbaren Zustands.
- K1: Werkzeuglose Zeitpläne starten ohne runs/start und ohne Lauf-Token;
  Ad-hoc bleibt Claim → Start. Bestehende Offline-Grenzen bleiben erhalten.
  **Offener Kern-Nachzug:** desktopResult.valid in cmd/jnpt/desktop_routes.go
  verweigert noch refused/secret und limit/wall (und bis #1245 K refused/idle).
  Keine stille Klassenumdeutung; abgelehnte Ergebnisse verwirft der Läufer nach
  zehn 400-Antworten gezählt (Invariante 9) statt den Rückstau zu blockieren.

| Tragende Server-Invariante | Tests |
|---|---|
| Geschlossene Optionen, Limits, Digest | TestDockerArgumenteGolden, TestDockerVerboteneOptionen, TestContainerGrenzenAmExecutor, TestDigestVergleichNachPull, TestDockerCLIArgumenteOhneShell, TestDockerEigenenEntrypointUeberschreiben (echter Docker) |
| Token-tmpfs, kein Hostcredential/Socket | TestTokenNichtInInspect, TestJobLiestGeraetetokenNicht (echter Docker) |
| Cleanup, kein Wiederstart | TestVerwaisteContainerNachNeustart, TestAbbruchUndWandzeitKillRm, TestContainerFehlgeschlageneBereinigungSperrtNeustart, TestContainerCleanupWartetAufEntfernung, TestContainerHeartbeatAbbruchAmHTTPAdapter, TestContainerWiderrufStopptVorLoeschen, TestDockerAbbruchWandzeitUndNeustart (auch echter Docker) |
| Geheimnis-Schnittmenge, Symlink, kein Env | TestGeheimnisseNurDeklariertUndOhneSymlink, TestGeheimnisseNieAlsEnv, TestKeinGeheimnisMountBeiFehlgeschlagenemStart, TestDockerGeheimnisMountReadOnlyUndInspect (echter Docker) |
| Rootless und Mindestversion | TestRootlessErkennung, TestRuntimeFehltOderZuAlt |
| E1/Vertrag, beide Startwege, alte Bytes | TestVertrag2ManifestGolden, TestVertrag1GoldensUnveraendert, TestVertragJeGeraeteart, TestVertrag2AmHTTPAdapterUndBeideStartwege, TestVertrag2VerweigertFremdeArtUndGeheimnisfelder |
| Linux-Speicher, Rechte und Dienst | TestLinuxDateirechteJournalUndIntegritaet, TestLinuxPrivatePfadeVerweigernSymlinkUndFremdeRechte, TestLinuxDateiUndSperreAmAdapter, TestLinuxKonfigurationUndDienst |

Linux-CI setzt JNPT_DOCKER_REQUIRED=1: kein Skip ohne Docker. Lokale Prüfung
auch im Linux-Prüfcontainer mit Docker-Socket möglich, /tmp für Bind-Proben am
gleichen Hostpfad einbinden und Go-TMPDIR auf ein ausführbares Verzeichnis setzen.
probe/container_mutations.py mutiert neue Sperren mit kompilierenden roten Tests
und stellt jede Datei bytegenau wieder her. Nur exklusiv, keine parallelen Tests.

L1-Mutationen: `python probe/l1_mutations.py` nutzt den laufenden Container
`l1-test` mit Go 1.26.10, ohne parallele Tests. Jeder Mutant muss kompilieren
und am benannten Adaptertest rot werden; `finally` stellt die Bytes wieder her.
Windows-CI ergänzt `TestWindowsK8UhrmarkeDPAPINeustart` (DPAPI, ACL,
Neustart, Manipulation). Keine GUI-Probe für L1.
Upgrade-Regression: `TestTestgeraetUpgradeSynchronisiertVertrag4` und
`TestAlterCacheSperrtClaims` prüfen alten Cache, neuen Sync und die Claim-Sperre.
Kern-Restgrenzen: unbekannte abgelaufene Offline-Slots/Werkzeugläufe werden
vor dem Zählen abgewiesen, `result:null` ebenfalls (README). L5 lässt bei
`jnpt.ergebnis(None)` das Feld weg; Kern-Fix #1300 bleibt separat. Nicht durch
Klassenumdeutung umgehen; Kern bleibt außerhalb von L1/L5.
