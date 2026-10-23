# CLAUDE.md — desktop-runner

## Verantwortung

Desktop-Läufer für JanuaPort #1167 / #1163 D4, Windows/amd64 ohne CGO.
Kopplung, signiertes Manifest, lokaler Takt, Paket-/Runtimeprüfung,
COM-Autostart und DPAPI-Journal sind implementiert. Fake-Kern, Linux-Race-Tests
und native Windows-Sonden; Live-Anlage und Azure-Release stehen aus.
Maßgeblich: [Draht-Vertrag v1](contract/README.md).

## Aufbau

| Pfad | Inhalt |
|---|---|
| main.go | CLI und feste Fehlerausgaben |
| internal/client/http.go | Einziger HTTP-Adapter, E3, Tokenrollen, Widerruf |
| manifest.go, copied_contract.go | Manifest/Kanonisierung/Ed25519, Gitobjekte/SHA-1DC, Cron/IANA |
| environment.go, copied_lock.go, copied_zip.go | Allowlist, Wheels, Entpacken und Umgebungsseal |
| engine.go, bootstrap.py | Gemeinsamer Laufplatz, Takt, Claim/Start, Rahmenkanal/MCP |
| state.go, replace_* | Atomare DPAPI-Dateien, Journal/Marker/Slot-Transaktion, Löschung |
| log.go | Geschlossene numerische Codes, 5 × 1 MiB |
| internal/platform/*_windows.go | DPAPI/ACL, WTS/LastInput, suspendierter Prozess/Job Object, COM/HKCU |
| internal/platform/install.go, other.go | Windows-Nähte und Fail-closed-Stubs für Linux |
| internal/testfixture | Synthetische Gitpakete nur für Tests |
| probe/mutations.py | Negativtests unter entfernten Sperren, exakte Wiederherstellung |
| contract/ | Vertrag/Goldens, eigenständiges D0-Testmodul |
| .github/workflows/desktop-runner*.yml (Repo-Root) | Linux-/Windows-CI und optional signiertes Release |

## Invarianten

1. Nur Pins am Ausführungsort `geraet:<id>`, keine `lokal`-Pins. Weg B:
   Gerät holt alles ab, keine Verbindung von der Anlage zum Gerät.
   `/api/device/v1/`, Contract-Header je Anfrage, no-store je Antwort.
   Läufer-Token überall **außer `pair`**, dort kein Authorization-Header.
   Standard LAN/Tunnel, öffentlich nur Opt-in mit Allowlist und Bremse (C2).
2. Kopplung ersetzt Token per stdin: Code 15 min/einmalig/≥50 Bit,
   Crockford-Gruppen, nach 5 Fehlversuchen verbrannt, globale 429-Bremse.
   E3: nur gültiges HTTPS oder echter Loopback; selbstsigniert nur mit
   `--ca`/`--fingerprint`, kein Abschalten der Prüfung. Ed25519-Key per TOFU
   speichern, Rotation v1 nur neue Kopplung (E2). Widerruf: 401 `revoked`
   ohne message; Credential, Pakete/Umgebungen, Manifest und Journal löschen.
3. E1: Geräte-Manifest mit Ed25519 über exakt kanonische Bytes prüfen:
   rekursiv sortierte UTF-8-Schlüssel, kein äußerer Leerraum/BOM/LF,
   Zahlentoken verlustfrei; genaue Escape-Regeln im Vertrag. Fremde Geräte-ID,
   alte/gleiche seq oder falsche Signatur → Klasse `manifest`; alter Stand
   und letzter erfolgreicher Sync bleiben erhalten. seq uint64, dauerhaft,
   ab 1, je erfolgreichem Sync erhöht, auch bei gleichen Pins.
4. JSON streng mit `DisallowUnknownFields`. Heartbeat ausschließlich
   `contract`, `ready`, `runner_version`, `manifest_seq`; kein Sitzungsstatus,
   keine Leerlaufzeit, kein Konto. Antwort ergänzt `manifest_seq_current`;
   bei höherem Wert Manifest holen. Ergebnis hat kein Ausgabefeld.
5. Takt auf dem Gerät: 1 min vor jedem geplanten Lauf Manifest und bei
   neuem Commit Paket voll prüfen. Offline nur ohne declared_tools und
   `jetzt − letzter erfolgreicher Sync < grace_hours` (1–72 h), sonst
   `refused/offline`. Werkzeuge brauchen Box/Lauf-Token. Deterministischer
   `run_key = pin/schedule/UTC-Slot`; Online-Start mit Werkzeugen über
   `runs/start`, Standbindung per `X-Jnpt-Manifest-Seq`, veraltet 409 manifest.
   Ad-hoc bleibt claim → jobs/start; Job liefert seine Freigabe-manifest_seq.
6. Ein Platz je Gerät für beide Laufarten. Zweiter Claim sofort 204,
   geplanter Start bei anderem Lauf 409 busy; zweiter Start desselben Laufs
   409 already_started ohne Token. MCP verlangt beide Token, zustandslos.
7. Pakete nur für `(commit,path)` eines offenen eigenen Auftrags **oder**
   eines aktuellen eigenen Manifest-Pins, damit Sync ohne Claim funktioniert.
   Unbekannt/fremd byte-gleiche 404. Git-IDs/exakte Objektmenge vor jeder
   Ablage prüfen; fehlend/überzählig, 120000/160000, unsichere Namen oder
   Grenzen → refused/hash, kein Prozess. SHA-1DC statt crypto/sha1 (C3).
8. Drei Hash-Stufen: Git, Wheel-SHA-256 gegen Lock, Umgebungs-Manifest
   (Pfad/Größe/SHA-256) nach Entpacken und vor jedem Start. Python-ZIP
   zusätzlich gegen ausgelieferte python.org-Allowlist (C1), nicht aus Pin;
   fehlend/falsch → refused/runtime. Paket-tools = signierte declared_tools.
9. E4: DPAPI-CurrentUser-Journal zuerst für alle Ergebnisse, höchstens
   200 Ergebniseinträge/16 MiB gespeicherter Bestand inkl. DPAPI-/Rahmen-Overhead,
   Laufmarker und Zähler; ältester Ergebniseintrag fällt mit dauerhaftem Zähler raus.
   Verschlüsselte Laufmarker überleben Neustart; offen → interrupted,
   nie erneut starten. Keine Klartext-state.json. runs/result trägt genau
   run_key oder job_id, Freigabe-manifest_seq, Commit, Offline-Flag und
   Abschlusszeit. Box prüft historische Freigabe und führt vetLog aus.
   Erstes gewinnt dauerhaft, gleich 204/abweichend 409; erst nach 204 löschen
   (Ausnahmen gezählte Verdrängung und Widerruf). Kein Tokenablauf-Cutoff.
10. Box hält Ad-hoc-Aufträge persistent, löscht Eingabe bei Ende; Claim nach
    Box-Neustart funktioniert. Auch Start-/Ergebnis-/Manifesthistorie bleibt;
    kein pauschales Neustart-404 mit Verwerfen. Contract 1 und Paketversion 1,
    Updates zuerst Kern, dann Läufer; geschlossene Klassen inkl. offline.

## Benannte Grenzen

Widerruf wird offline erst beim Kontakt sichtbar, bis zur Gnadenfrist kann
das Gerät weiterlaufen. Ed25519-Rotation v1 nur neue Kopplung. Workstation-
Skripte handeln mit den Rechten der Person. DPAPI CurrentUser schützt nicht
gegen jemanden mit Zugriff auf diese Sitzung (C4).

## Tragende Adaptertests

| Invariante | Tests |
|---|---|
| B1: Git vor Spawn, Objektmenge/Modi/Namen | TestFalscherHashBrichtVorDemStartAb, TestPaketObjektmengeModiUndNamen, TestSHA1Collision, TestPaketMedientypAmHTTPAdapter |
| Runtime/Lock/Umgebung | TestPythonAllowlistUndWheel, TestUmgebungVorJedemStart, TestUmgebungFehlendeZusaetzlicheDateien, TestWheelTagsLockUndManifestVerbote, TestWindowsPythonEchterStartUndManipulation |
| ZIP-Namen/Typen/Kollisionen/Bomben | TestZIPAbweisungAmEntpacker; Kopien: TestCopiedLibraryFunctions |
| E3: TLS vor Code, CA/Pin, keine Redirects/Proxys, Pubkey nötig | TestPairTLSUndLeererPubkey, TestCAFingerprintProxyUndURL, TestRedirectUndRemoteHTTPAbgewiesen |
| E1: Gerät/seq/Signatur, alter Sync unverändert | TestManifestAblehnung, TestManifestSyncAtomarUndOfflineAmAdapter, TestVeralteterStartSynchronisiertErneut; Kopien: TestCopiedContractFunctions |
| Konto/Sitzung/idle vor Spawn | TestKontoUndManipulationVorSpawn, TestReadyVerboteneZustaende |
| B2/B3: keine Listener/Downloads, keine privaten Bodys/Kind-Token | TestKeineListenerDownloaderOderPip, TestNurAnlageUndKeinePrivatenBodys, TestWindowsPythonEchterStartUndManipulation, TestWindowsJobObjectEnkelUndToken |
| Cron/DST, Offline-Grenze und Journal | TestCronZeitzoneUndSlots, TestOfflineRegel, TestOfflineVerweigertWirdJournalisiert |
| E4: Ciphertext-Deckel, Bestätigung, Neustart | TestJournalDeckelBestaetigungNeustart, TestJournalMisstVerschluesselteBytes, TestWindowsNeustartInterrupted, TestErgebnisrueckstauUndAtomarerSlot |
| Widerruf an allen Wegen, keine Wiederbelebung | TestWiderrufAnAllenHTTPWegen, TestWiderrufAllesWeg |
| Native DPAPI/ACL/Entropie/Prozessbaum | TestDPAPIUndACL, TestWindowsDPAPIBrauchtEntropie, TestWindowsSuspendBisJobZuordnung, TestWindowsJobCloseBeendetEnkel |
| Task ohne Admin und begrenzter Rückfall | TestWindowsAufgabeOhneAdmin, TestAutostartRueckfallNurBeiRichtlinie |
| Hygiene | TestLoggerDeckelUndFesteCodes, TestStriktesJSONAmAdapter, TestFrameGrenzen, TestReleaseUndWindowsCI |

Übernommene Funktionen sind im Dateikopf markiert. Wächter vergleichen die
vollständige Funktionsmenge und formatierten Funktionszeilen mit D0 bzw.
script-runner/internal/libs. Dokumentierter Delta im Git-Prüfer: 30.000
Baum-Besuche gegen wiederholte DAG-Expansion. Die eingebettete Allowlist wird
byte-genau mit dem Vertrag verglichen. Der Windows-Bootstrap ist eigenständig
für Pipes implementiert.

Engine.Execute prüft Konto, Paket, Paketmanifest und Umgebungsseal und
schreibt den Laufmarker vor Spawn. Lauf-Token sind kein Feld von Launch.
MCP erhält sie ausschließlich im HTTP-Header. runMu und Benutzermutex
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
Marker, Slot-Merker und Verlustzähler. Atomare Ersatzdateien sind verschlüsselt;
verwaiste Ersatzdateien werden beim exklusiven Start entfernt. Credential,
Config/Manifest und Umgebungsseals liegen in DPAPI-Dateien. Nur Paket- und
Laufzeitcode liegt im Klartext. Status prüft Dateiexistenz und Autostart,
ohne den Journalbestand zu öffnen oder zu bereinigen.

Diese Fassung verwirft stdout/stderr und januaport.log; log auf dem Draht
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
Azure OIDC/Authenticode und reale Kernintegration brauchen externe Abnahme.

Mutationen nur bei exklusivem Worktree-Zugriff: python probe/mutations.py.
Ein Mutant muss kompilieren und sein Zieltest tatsächlich fehlschlagen;
Compilerfehler zählen nicht. finally stellt Quelldateien byte-genau wieder her.
Nach externer Zwangsbeendigung den betroffenen Guard ausdrücklich prüfen.
Abschlussbericht ausschließlich im Chat, keine Berichtsdatei im Repository.

Native Windows-Mutationen zusätzlich: probe/mutations-windows.ps1 (ohne Admin).
