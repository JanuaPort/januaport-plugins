# CLAUDE.md — desktop-runner

## Verantwortung

Desktop-Läufer für JanuaPort #1167 / #1163 D4, Windows/amd64 und Linux-Server ohne CGO.
Kopplung, signiertes Manifest, lokaler Takt, Paket-/Runtimeprüfung,
COM-Autostart und DPAPI-Journal sind implementiert. Fake-Kern, Linux-Race-Tests
und native Windows-Sonden; signiertes Windows-Release `desktop-runner-v0.1.0`
vorhanden, Live-Anlage steht aus. Manueller Installationstest: README „Installationstest“.
Maßgeblich: [Draht-Vertrag v1/v2](contract/README.md).

## Aufbau

| Pfad | Inhalt |
|---|---|
| main.go | CLI und feste Fehlerausgaben |
| internal/client/http.go | Einziger HTTP-Adapter, E3, Tokenrollen, Widerruf |
| manifest.go, copied_contract.go | Manifest/Kanonisierung/Ed25519, Gitobjekte/SHA-1DC, Cron/IANA |
| environment.go, copied_lock.go, copied_zip.go | Allowlist, Wheels, Entpacken und Umgebungsseal |
| engine.go, bootstrap.py | Gemeinsamer Laufplatz, Takt, Claim/Start, Rahmenkanal/MCP |
| state.go, replace_* | Atomare OS-geschützte Dateien, Journal/Marker/Slot-Transaktion, Löschung |
| runtemp.go | Lauf-TEMP: RunID/RunTemps, Anlegen nur neu, Aufräumen/Besen ohne Reparse-Punkte, Kind-Umgebung |
| log.go | Geschlossene numerische Codes (1–6), 5 × 1 MiB |
| internal/platform/*_windows.go | DPAPI/ACL (SecureRoot, MkdirPrivate), WTS/LastInput, suspendierter Prozess/Job Object, COM/HKCU |
| internal/platform/install.go, other.go | Windows-Nähte und Fail-closed-Stubs für andere Systeme |
| internal/platform/security_linux.go, server_linux.go | Linux-HMAC/Dateirechte, Sperre und Server-Konfiguration |
| container.go, container_engine.go, secrets_linux.go | Docker-CLI, Startwege und Geheimnisgrenze |
| internal/testfixture | Synthetische Gitpakete nur für Tests |
| probe/mutations.py | Negativtests unter entfernten Sperren, exakte Wiederherstellung |
| probe/installtest*.ps1 | Manueller Release-Installationstest, Offline-Adaptertests und isolierte Mutationen |
| contract/ | Vertrag/Goldens, eigenständiges D0-Testmodul |
| .github/workflows/desktop-runner*.yml (Repo-Root) | Linux-/Windows-CI und optional signiertes Release |

## Invarianten

1. Nur Pins am Ausführungsort `geraet:<id>`, keine `lokal`-Pins. Weg B:
   Gerät holt alles ab, keine Verbindung von der Anlage zum Gerät.
   `/api/device/v1/`, Contract-Header je Anfrage, no-store je Antwort.
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
   ohne message; Credential, Pakete/Umgebungen, Manifest und Journal löschen.
3. E1: Geräte-Manifest mit Ed25519 über exakt kanonische Bytes prüfen:
   rekursiv sortierte UTF-8-Schlüssel, kein äußerer Leerraum/BOM/LF,
   Zahlentoken verlustfrei; genaue Escape-Regeln im Vertrag. Fremde Geräte-ID,
   alte/gleiche seq oder falsche Signatur → Klasse `manifest`; alter Stand
   und letzter erfolgreicher Sync bleiben erhalten. seq uint64, dauerhaft,
   ab 1, je erfolgreichem Sync erhöht, auch bei gleichen Pins.
4. JSON streng mit `DisallowUnknownFields`. Vertrag-1-Heartbeat ausschließlich
   `contract`, `ready`, `runner_version`, `manifest_seq`; kein Sitzungsstatus,
   keine Leerlaufzeit, kein Konto. Antwort ergänzt `manifest_seq_current`;
   bei höherem Wert Manifest holen. Ergebnis hat kein Ausgabefeld.
5. Takt auf dem Gerät: 1 min vor jedem geplanten Lauf Manifest und bei
   neuem Commit unter Windows das Paket voll prüfen; Server prüfen vor Start
   Pull und Image-Digest. Offline nur ohne declared_tools und
   `jetzt − letzter erfolgreicher Sync < grace_hours` (1–72 h), sonst
   `refused/offline`. Werkzeuge brauchen Box/Lauf-Token. Deterministischer
   `run_key = pin/schedule/UTC-Slot`; Online-Start mit Werkzeugen über
   `runs/start`, Standbindung per `X-Jnpt-Manifest-Seq`, veraltet 409 manifest.
   Ad-hoc bleibt claim → jobs/start; Job liefert seine Freigabe-manifest_seq.
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
    kein pauschales Neustart-404 mit Verwerfen. Windows: Contract 1 und Paketversion 1; Linux-Server: Contract 2,
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
    kein `os.RemoveAll`) und löschen sie nur als Verweis. Nichts überlebt einen
    Lauf, keine Größenbremse (Wanduhr-Grenze, eigene Platte). Journal, Ergebnis und
    Geheimnisse liegen nie unter `tmp\`. Die Kind-Umgebung enthält kein
    `LOCALAPPDATA`/`USERPROFILE`/`APPDATA`/`HOME*`/`TMPDIR`; ein Wert aus diesen
    Variablen wird gefiltert, `TEMP`/`TMP` zeigen nur auf das Laufverzeichnis.

## Benannte Grenzen

Widerruf wird offline erst beim Kontakt sichtbar, bis zur Gnadenfrist kann
das Gerät weiterlaufen. Ed25519-Rotation v1 nur neue Kopplung. Workstation-
Skripte handeln mit den Rechten der Person. DPAPI CurrentUser schützt nicht
gegen jemanden mit Zugriff auf diese Sitzung (C4).

## Tragende Adaptertests

| Invariante | Tests |
|---|---|
| B1: Git vor Spawn, Objektmenge/Modi/Namen | TestFalscherHashBrichtVorDemStartAb, TestPaketObjektmengeModiUndNamen, TestSHA1Collision, TestPaketMedientypAmHTTPAdapter, TestGoldenPaketeBytegenauUndManipulation |
| Runtime/Lock/Umgebung | TestPythonAllowlistUndWheel, TestUmgebungVorJedemStart, TestUmgebungFehlendeZusaetzlicheDateien, TestWheelTagsLockUndManifestVerbote, TestWindowsPythonEchterStartUndManipulation |
| ZIP-Namen/Typen/Kollisionen/Bomben | TestZIPAbweisungAmEntpacker; Kopien: TestCopiedLibraryFunctions |
| E3: TLS vor Code, CA/Pin, keine Redirects/Proxys, Pubkey nötig | TestPairTLSUndLeererPubkey, TestCAFingerprintProxyUndURL, TestRedirectUndRemoteHTTPAbgewiesen, TestPairCodeFormat, TestPairLeererPubkey, TestPairRFC8032Pubkey, TestPairLeererPubkeySpeichertNichts, TestPairRFC8032PubkeySpeichertNichts |
| E1: Gerät/seq/Signatur, alter Sync unverändert | TestManifestAblehnung, TestManifestSyncAtomarUndOfflineAmAdapter, TestVeralteterStartSynchronisiertErneut; Kopien: TestCopiedContractFunctions |
| Konto/Sitzung/idle vor Spawn | TestKontoUndManipulationVorSpawn, TestReadyVerboteneZustaende, TestIdleVorSpawnUndMount, TestIdleWirdNieAlsAccountGemeldet |
| Rückstau-Grenze abgelehnter Ergebnisse (L2) | TestAbgelehntesErgebnisStautDenRueckstauNicht, TestAltKernLehntIdleAbJournalStautNicht, TestVerwurfWirdLokalGeloggt |
| Lauf-TEMP (L3–L6) | TestRunIDFormat, TestRunTempsCreateVerlaesstDieBasisNie, TestRunTempsAnlegenNeuUndExklusiv, TestRunTempsRemoveFolgtKeinemVerweis, TestRunTempsSweepRaeumtRestePlusVerweiseOhneEinzusteigen, TestLaufBekommtPrivatesTempAlsArbeitsverzeichnis, TestLaufTempWirdBeiJedemEndeAufgeraeumt, TestKindUmgebungOhneProfil; Windows: TestMkdirPrivateExklusivUndGeprueft, TestRunTempJunctionKanarienvogel, TestWindowsEnvOhneProfilUndTemp, TestWindowsPythonTempProLauf |
| B2/B3 Windows: keine Listener/Downloads, keine privaten Bodys/Kind-Token | TestKeineListenerDownloaderOderPip, TestNurAnlageUndKeinePrivatenBodys, TestWindowsPythonEchterStartUndManipulation, TestWindowsJobObjectEnkelUndToken |
| Cron/DST, Offline-Grenze und Journal | TestCronZeitzoneUndSlots, TestOfflineRegel, TestOfflineVerweigertWirdJournalisiert |
| E4: Ciphertext-Deckel, Bestätigung, Neustart | TestJournalDeckelBestaetigungNeustart, TestJournalMisstVerschluesselteBytes, TestWindowsNeustartInterrupted, TestErgebnisrueckstauUndAtomarerSlot |
| Widerruf an allen Wegen, keine Wiederbelebung | TestWiderrufAnAllenHTTPWegen, TestWiderrufAllesWeg |
| Native DPAPI/ACL/Entropie/Prozessbaum | TestWindowsFehlerBehaltenUrsache, TestPrivateACLAliasUndVerbote, TestPrivateACLAmDateisystem, TestSecureRootVerweigertJunction, TestDPAPIUndACL, TestWindowsDPAPIBrauchtEntropie, TestWindowsSuspendBisJobZuordnung, TestWindowsJobCloseBeendetEnkel |
| Task ohne Admin und begrenzter Rückfall | TestWindowsAufgabeOhneAdmin, TestAutostartRueckfallNurBeiRichtlinie |
| Hygiene | TestLoggerDeckelUndFesteCodes, TestStriktesJSONAmAdapter, TestFrameGrenzen, TestReleaseUndWindowsCI |

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
Widerrufsabbruch verwenden dieselbe Engine-Sperre.

Die Journalgrenze gilt für die veröffentlichte Ciphertext-Datei einschließlich
Marker, Slot-Merker und Verlustzähler. Atomare Ersatzdateien sind unter Windows verschlüsselt, unter Linux authentisiert;
verwaiste Ersatzdateien werden beim exklusiven Start entfernt. Unter Windows liegen Credential,
Config/Manifest und Umgebungsseals in DPAPI-Dateien. Nur Paket- und
Laufzeitcode liegt im Klartext. Status prüft Dateiexistenz und Autostart,
ohne den Journalbestand zu öffnen oder zu bereinigen.

DACLs werden strukturell geprüft: geschützt, genau eine explizite Allow-ACE,
eigene binäre SID, volle Dateirechte und ausschließlich OI/CI-Vererbung.
SDDL-Aliase (etwa SY/BA) sind keine andere Identität; ein Textvergleich
von SID-Darstellungen ist unzulässig. Reparse-Punkte bleiben
auf allen bestehenden Pfadkomponenten gesperrt. Windows-Diagnosen enthalten
nur feste Operationsnamen und OS-Fehlernummer/-text, keine Pfade oder Konten.
Die CLI-Dienstausgabe bleibt unverändert beim festen Fehlercode.
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

Native Windows-Mutationen zusätzlich: probe/mutations-windows.ps1 (ohne Admin).
Mutationsfälle für #1245 L: idle-before-spawn, idle-remap-account, result-reject-jam,
runid-format, runtemp-reuse, runtemp-follow-link, child-env-profile, windows-exclusive.
Die Symlink-Tests von RunTemps überspringen unter Windows ohne Symlink-Recht sichtbar;
dort belegt TestRunTempJunctionKanarienvogel das Aufräumen an einer echten Junction.

## Linux-Server: zusätzliche Invarianten (JanuaPort/januaport#1195)

- Server ausschließlich Vertrag 2, Windows ausschließlich Vertrag 1. Header,
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
| E1/Vertrag, beide Startwege, alte Bytes | TestVertrag2ManifestGolden, TestVertrag1WindowsUnveraendert, TestVertrag2AmHTTPAdapterUndBeideStartwege, TestVertrag2VerweigertFremdeArtUndGeheimnisfelder |
| Linux-Speicher, Rechte und Dienst | TestLinuxDateirechteJournalUndIntegritaet, TestLinuxPrivatePfadeVerweigernSymlinkUndFremdeRechte, TestLinuxDateiUndSperreAmAdapter, TestLinuxKonfigurationUndDienst |

Linux-CI setzt JNPT_DOCKER_REQUIRED=1: kein Skip ohne Docker. Lokale Prüfung
auch im Linux-Prüfcontainer mit Docker-Socket möglich, /tmp für Bind-Proben am
gleichen Hostpfad einbinden und Go-TMPDIR auf ein ausführbares Verzeichnis setzen.
probe/container_mutations.py mutiert neue Sperren mit kompilierenden roten Tests
und stellt jede Datei bytegenau wieder her. Nur exklusiv, keine parallelen Tests.
