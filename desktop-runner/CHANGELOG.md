# Changelog

## Unveröffentlicht — Anmeldeleiste nach App-Modus (#1321)

- Startmeldung „Bitte mit dem eigenen Konto anmelden.“ bleibt nach dem ersten
  `Login.Pump` erhalten: `NewLogin` und Control-Anlage nutzen `anmeldung.Message("start")`.
  Nach Abmelden weiterhin „Abgemeldet.“; Regressionstest und Mutant `leistentext-start`.
- Vertrag 5 nimmt `apps[].mode` optional an (a/b/leer, sonst Manifestfehler).
  In Modus a keine Anmeldeleiste, keine Knöpfe/Meldungen; in b und bei fehlendem/leerem
  Feld wie bisher. Neue geprüfte Manifeste aktualisieren auch offene Fenster beim Abgleich.
- Ausschließlich Anzeige: Rechte, `/token`, Login-Fluss und K6b-2/K6c-Sperren unverändert.
  Browserdaten werden weiterhin beim Öffnen in beiden Modi gelöscht.
- Manifest-Golden bytegleich aus `jpwt-1321` aktualisiert und SHA-geprüft; die übrigen
  Goldens bleiben unverändert. Native Controls/Manifestwechsel prüft BOX mit
  `TestWindowsWebViewAnmeldeleisteNachModus`; hier keine GUI-Ausführung.

## Unveröffentlicht — K6c P3 (#1048)

- Persönliche PC-Anmeldung über nativen Knopf, Systembrowser, PKCE und zeitlich
  begrenzten IPv4-Loopback; Fehlerklassen ausschließlich als feste native Meldung.
- Personenwechsel über `person_gewechselt`, Browserdaten bei Wechsel/Abmelden
  löschen und alte Antworten verwerfen. B1–B4 und Browser-Netzsperren unverändert.
- SEC-R4: abgelaufener Einmalcode mit Zeitablaufmeldung, abgelaufene persönliche
  Sitzung ohne Kontoname/Anmeldestatus, Wiederanmeldung ohne lokale Sitzung lädt neu.
  Laufende und wartende Altantworten über Login mit zwei Mutanten gepinnt.
- Lokales Adapter-Golden und bytegleicher P2-Kernspiegel aus ea50aa7a mit SHA-Wächter.
  WebView-Bereinigung einschließlich sessionStorage bleibt bis zur neuen BOX-Sonde
  unbelegt; kein lokaler GUI-Test, kein Release.

## Unreleased

- K6b-2, SEC-R4: Netzsperre mit Pflichtargument auch für UDP-Wege, Schalter-Normalisierung, zwei Korrekturen der Kopie, Rückleseprobe der Einstellungen, Fenster schließen bei Entzug einer App.
- K6b-2 (JanuaPort/januaport#1048): App-Fenster im Bedienfeld (WebView2 über eine angepasste Kopie von
  `go-webview2` v1.0.19 in `internal/edge` und einen dünnen eigenen COM-Adapter), ohne Release. Der Start des
  WebView ist begrenzt (20 s, fester Fehler, Fenster bleibt zu, kein `os.Exit`), COM-STA auf dem UI-Thread. Menüeintrag je zugewiesener App; Zustand nur
  lesend (`client.Reader`), Gerätetoken erst beim Öffnen; Gateway, Brücke und Pipe-Gespräch nie auf dem
  UI-Thread (Halt bleibt bedienbar); Wächter je Wurzel mit der einen Tür `cmd/bedienfeld -> internal/fensterio`.
  Native Pflichtereignisse NavigationStarting, FrameNavigationStarting, NewWindowRequested, DownloadStarting,
  WebMessageReceived mit Quelle, PermissionRequested (Deny), ProcessFailed; fehlt eine Schnittstelle, öffnet das
  Fenster nicht. Nur `.html`-Seiten, externe https-Links nur nach nativem Ja-Dialog, native Testleiste,
  Benutzerdatenverzeichnis `webview\<app>` mit privater DACL. Der Läufer legt die Apps beim Sync ab
  (Logcode 10 bei Fehlern). Native Sonden je Ereignis (Opt-in), Brücken-API in der README.

- K6b-1 (JanuaPort/januaport#1048, Hyperfokus #1313 B2), fensterloser Teil des PC-Fensters, ohne
  Fenster und ohne Release. **Windows spricht Vertrag 5** (Header und JSON; Vertrag 4 plus
  `apps:[{name,commit,path,mode?}]`, optionaler Modus seit #1321); der Läufer braucht einen Kern mit K6a, ältere Caches (v1, v3, v4)
  liefern beim Upgrade nur die Sequenz. `apps` wird streng gelesen (Name, SHA-256-Commit,
  Pfad `apps/<name>`, sortiert), unter v1–v4 abgewiesen. Goldens des Kerns als
  `contract/golden-v5/` gespiegelt, alle bestehenden Goldens bytegleich.
- App-Paket: `AppStore` holt, prüft (Git-IDs, SHA-1DC/SHA-256, apphost-Regeln als markierte Kopie
  `copied_apphost.go`), legt privat unter `apps\<name>\<commit>\` ab und versiegelt; das Siegel
  wird vor jedem Öffnen geprüft. Das CSP-Literal setzt der Läufer beim Ablegen selbst als erstes
  Element in jedes `<head>` ein (vor dem Siegel); eigene CSP-Meta oder kein sauberes `<head>` wird
  abgewiesen, `Open` prüft das führende Literal; geprüft wird zuletzt mit dem HTML-Parser (SEC-R4 F1); davor steht die Zeichensatz-Meta, ESC/NUL/ungültiges UTF-8 wird abgewiesen (F4). Die apphost-CSP wird nicht übernommen.
- App-Token (`POST apps/{name}/token`) nur im RAM, bei 401 einmal neu geholt; gedruckt wird es nie.
- `internal/fenster` (reines Go, Import-Wächter): exaktes CSP-Literal, `NavigationAllowed` (nur `.html`-Seiten, SEC-R4 F2),
  `MessageAllowed`, `Settings()`-Sollliste, strikter Umschlag (256 KiB) und die Brücke mit
  `status`, `anhalten` (lokal, genau zwei Pipe-Operationen), `werkzeuge`, `werkzeug`;
  Antworten nur mit festen Codes, nie mit einem Träger. `internal/fensterio` hängt Gateway
  und Pipe-Wrapper an. Mutationen: `probe/k6b_mutations.py`. SEC-R4 F3: Tests für Träger in Escapes und
  Schlüsseln, nur `tools/list`/`tools/call`, nie zwei Träger, App-Token nur an `mcp`, Antwort-ID, Widerruf.

## 0.4.0 — 2026-11-04 (JanuaPort/januaport#1240)

- L6: gemeinsame Installation von Läufer und Bedienfeld aus demselben Ordner;
  zweite Anmelde-Aufgabe mit HKCU-Run-Rückfall, Bedienfeld ohne Argumente.
  Fehlende Bedienfeld-Exe: E1 ohne Registrierung; Fehler nehmen Autostartänderungen
  zurück. Uninstall entfernt beide Einträge und beendet das eigene Bedienfeld;
  Halt und übrige DPAPI-Dateien bleiben erhalten.
- Release baut und signiert beide Exe, prüft beide mit signtool und bindet beide
  in SHA256SUMS/Provenance. Installtest prüft beide Autostartwege und Argumentfreiheit;
  Invariante 19, Negativtests und L6-Mutationen ergänzt. G-L3 vor Geräte-Rollout:
  native Opt-in-Proben und Bedienfeld-Prozesssonde auf dem Testgerät PASS ohne SKIP.
- L6 SEC-R4 F1: Installtest hält die RunLevel-Prüfung mit eigenem Fall und Mutante fest;
  README nennt `install` nach dem Exe-Tausch (H3).
- L5 SEC-R4 F1: keine Endaufnahme nach lokalem Anhalten. L2-Haltzustand
  unmittelbar davor unter `e.mu` geprüft, Aufnahme/Upload außerhalb der Sperre;
  nach Wandzeitablauf bleibt die Endaufnahme erhalten. Regression und Mutationen
  für Halt/Wandzeit sowie H2: isolierte Bindung des Laufpins an die Testfreigabe.

- `jnpt`-Helfer (#1240 L5) neben `januaport`: lokale Rückfragen, letztes
  JSON-Ergebnis bis 64 KiB nur für Testaufträge, `None` ohne `result`-Feld.
  Sämtliche Grenzen und Testfreigaben am Go-Läufer, auch für rohe Rahmen (L3-2).
- Go-GDI-Aufnahme des primären Bildschirms nur mit signiertem `test_device`
  und Geräte-Testpin samt Jobbindung; PNG bis 4 MiB, fünf Versuche einschließlich
  automatischer Endaufnahme. Linux-Stub, Sperrbildschirm-Grenze dokumentiert.
  Tests mit gezähltem Fake-Aufnehmer und echtem HTTP-Adapter, Python-Proben und
  Mutationen. Native GDI/H3 lokal nur kompiliert, keine Aufnahme/GUI gestartet.
- Bedienfeld-Prozesssonde: `CompareObjectHandles` aus `kernelbase.dll`,
  explizite Verfügbarkeitsprüfung und begründeter Skip statt Panic.
  Produktive DLL-/Proc-Auflösung ebenfalls ohne Panic; fehlende API hält
  das Modell in Störung und sperrt die Verbindung. Bei fehlenden grundlegenden
  Tray-APIs ist keine Anzeige möglich. Negative Loader-Tests und Mutationen.
- Natives Bedienfeld (#1240 L4): Windows-GUI-Exe ohne CGO, Tray mit
  Zustand/Pause/Weiter/Anhalten/Fortsetzen/Setups/Testanzeige und lokalem Beenden.
  Reines Panel-Modell, getrennter Pipe-I/O-Adapter, UI-Thread per PostMessage.
  Dauerhafte Störung mit festem Ausweichtext bis erneuter Serverprüfung und
  gültigem Zustand; Verbindung ausschließlich über die L3-Clientbibliothek.
- Rückfragen ohne Fokusraub, erst nach Klick; Frist/Entfernung/Anhalten schließen
  ohne Antwort. H4: Pflicht-Titel aus Manifest-Pin (v4: Rückfall Pin-Name),
  Go-Prüfung roher Skript-Rahmen und höchstens eine offene Frage je Lauf.
  Pflicht-Bool `braucht_eingabe` aus verifiziertem Setup-Manifest ermöglicht den
  genauen Hinweis; Schema-Golden ergänzt. L4 selbst änderte den Python-Helfer nicht.
- Fokus- und transitive Import-Wächter (benannte Ausnahmeliste leer), Modell-,
  Adapter- und Mutationstests, native Prozesssonde im bestehenden Windows-Job.
  Kein Install-/Release-Umbau/lokaler GUI-Start; native Nachweise durch CI/GATE.

- Windows-Pipe-Sonden (#1240 L3): Instanzanlage mit geprüftem Standardtoken
  und echter zweiter Serverinstanz statt bloßem Client-Open; Besitzer- und
  Rechte-Gegenproben. Kindsonden überspringen unbenannte/unlesbare Endpunkte,
  behalten verpflichtende Erkennungskontrollen und getrennte Mutationen für
  Go- und Python-Kind. Kurzer Pfad für die eingebettete Python-Testlaufzeit.
  Produktions-DACL und Handle-Vererbung unverändert.
- H1-Positivkontrolle: direkte Benutzer-ACE, ausdrückliches Medium-NWNR und
  Deskriptoren für beide Instanzanlagen, einschließlich Besitzer/Gruppe der
  zweiten Instanz. Beide Instanzen entstehen jetzt im selben nicht erhöhten
  Hilfsprozess (`CreateProcessAsUserW`), statt die erste im erhöhten CI-Prozess
  anzulegen. Primäres Kind-Token wird vor der Pipe-Anlage geprüft; Restricting-
  SIDs müssen in Quelle, gefiltertem Token und Kind fehlen. Eigener Negativtest
  mit echter Restricting-SID und Mutationen für Kind-Token und Kind-DACL.
  Die Negativprobe gegen die Produktions-DACL des Elternprozesses bleibt erhalten.
- Die beiden H1-Hilfsprozess-Proben und ihre nativen Mutationen sind Opt-in
  über `JNPT_NATIVE_TOKEN_PROBES=1` in einer interaktiven Sitzung. Ohne Opt-in
  fester Skip-Hinweis, mit Opt-in bleiben Startfehler rot. Die Negativprobe
  gegen die Produktions-DACL und die übrigen Pipe-Sicherheitsprüfungen bleiben
  unabhängig davon; keine Änderung von Window-Station- oder Desktop-ACLs.
- Lokale Windows-Pipe zwischen Läufer und künftigem Bedienfeld (#1240 L3):
  geschlossenes JSON-Schema mit 4-KiB-Rahmen und eigenem Golden, genau ein
  benannter Listener. B1/B2, ausdrückliches Medium-Label mit No-Read-Up und
  No-Write-Up (L3-1), schmale Client-Rechte ohne Instanzrecht (H1), Remote-
  Abweisung und kein Ausweichen bei belegtem Namen (`9 1`).
- Clientbibliothek prüft Server-PID, Bildpfad, Benutzer-SID (H2) und gleiches
  Authenticode-Herausgebermerkmal; Server prüft Client-Bildpfad. A1 bleibt:
  ACL schützt nicht gegen den WebView2-Hauptprozess derselben Person.
- Setup-Zulassung am Läufer in Go (L3-2): nur verifizierte Manifest-Setups
  ohne freie Eingabe, bei Online-Beobachtung und freiem Platz; Halt/Pause
  sperren. Reservierung über HTTP und Spawn, keine Offline-Setup-Warteschlange.
  Pipe-Haltbefehle verwenden L2; interne Rückfragen samt Frist/Antwort für L5.
- Rohe Rahmen, Kanarien-Credential, Hygiene und Mutationsproben; native
  Windows-Sonden für DACL/IL, Squatting, Prozessidentität und H3 (kein
  Pipe-Handle/-Name im Kind, auch echtes Python). Native Ausführung durch
  Windows-CI. L3 selbst enthielt kein Bedienfeld/Tray und keinen Python-Helfer,
  Install- oder Release-Umbau; das Bedienfeld folgt oben unter L4.
- Lokaler Halt im Windows-Engine (#1240 L2): atomare DPAPI-Datei vor dem
  ersten Takt; fehlend bedeutet arbeitet, unlesbar/ungültig angehalten plus
  lokale Störung (`8 1`). Anhalten beendet den Job-Baum auch offline mit
  `cancelled`; Halt und Spawn verwenden dieselbe Sperre (L2-1/L2-2, B6).
- Pause für 15/60 Minuten oder bis Weiter lässt laufende Arbeit enden und
  sperrt Slots/Claims/Testaufträge. Fortsetzen ohne Nachholen und ohne Umgehen
  der Startprüfungen (B3); Heartbeat bleibt bereit. Interne Go-Steuerung,
  seit L3 zusätzlich über die lokale Pipe erreichbar.
- Letzte Halt-Meldung koalesziert nachliefern, höchstens sechs Versuche pro
  Minute, auch über Neustarts. Race-/Adaptertests und Mutationsproben;
  native Windows-Sonde für Anhalten samt Enkelprozess (Windows-CI).

## 0.3.0 — 2026-11-03

- Testgeräte synchronisieren wieder (#1298): `test_device` wird in jeder
  unterstützten Manifestfassung angenommen; Signaturprüfung bleibt verbindlich.
- Windows spricht Vertrag 4 in Header und JSON, Linux-Server weiter Vertrag 2.
  Strikte Setup-/Frei-/Pinfrist-Typen; `apps` und Beschriftungen über 40 Zeichen
  werden abgewiesen. D0-Modul und Goldens v1/v2 bleiben unverändert.
- Adapter für Setup-Start (Manifestsequenz, zufällige Nonce), Halt-Meldung,
  Testresultate und PNG-Upload. Beide Testwege prüfen signierte Freigabe und
  Geräte-Testpin; der normale Ergebnisweg weist `result` ab.
- K8: Pinfrist vor Gerätefrist, persistente DPAPI-Uhrmarke gegen Uhr-Rücksprung,
  fester Logcode `7 1` und markierte Offline-Nachmeldung. Kern-Goldens gespiegelt,
  Adapter-/Negativtests mit Mutationsproben.
- L1 enthält keine Halt-Steuerung, Pipe, Bedienfeld oder Bildschirmaufnahme.

## 0.2.0 — 2026-10-30

**Setzt einen Kern ab 0.89.0 voraus.** Ältere Kerne lehnen `refused/idle` ab; der Läufer
verwirft solche Ergebnisse nach zehnmal HTTP 400 in Folge mit Logzeile. Skripte bekommen
ein eigenes TEMP je Lauf, das nach dem Lauf gelöscht wird.

- Ablehnungsklasse `refused/idle`: fehlt bei passendem Konto und aktiver Sitzung nur
  der Leerlauf der Startregel, meldet der Läufer `idle` statt `account`, vor jedem
  Start (JanuaPort/januaport#1245 L). **Release-Hinweis: setzt einen Kern mit
  #1245 K voraus** (er nimmt `idle` an). Ein älterer Kern antwortet 400.
- Ein vom Kern zehnmal in Folge mit HTTP 400 abgewiesenes Ergebnis blockiert den
  Rückstau nicht mehr: Es wird gezählt verworfen (Verlustzähler, Logzeile `5 <n>`).
  Bisher blieb es dauerhaft an erster Stelle des Journals liegen (betrifft auch die
  offenen K1-Klassen `refused/secret` und `limit/wall`).
- Privates TEMP je Lauf: frisches Verzeichnis mit DACL des Läufer-Ordners unter
  `%LOCALAPPDATA%\JanuaPort\desktop-runner\tmp\<Lauf-ID>\` als `TEMP`, `TMP` und
  Arbeitsverzeichnis statt `envs\…\runtime`; nach dem Job-Teardown gelöscht, Rest
  räumt der Start-Besen, ohne Junctions oder Symlinks zu betreten. Die Kind-Umgebung
  bleibt ohne `LOCALAPPDATA`/`USERPROFILE`/`APPDATA`. Behebt, dass Skripte ohne TEMP
  in die geprüfte Umgebung schrieben und der nächste Lauf mit `refused/hash` endete
  (Befund 6).
- Widerruf löscht den Zustand mit demselben link-sicheren Löscher (kein `os.RemoveAll`),
  damit eine vom Skript in `tmp\` angelegte Junction nie betreten wird (SEC R1).

- Manueller Installationstest des signierten Windows-Releases in einem frischen
  Standardkonto, mit SHA-256, Authenticode/Herausgeber und GitHub-Provenance,
  Showcase-Kopplung, Installation, fünf Minuten Anlagenkontakt und Deinstallation
  einschließlich Prüfung beider Autostartwege (#1233).
- Offline-Adaptertests und isolierte Mutationsproben für die Testsperren;
  separate Provenance-Negativprobe samt Mutation mit echten Release-Assets und `gh`;
  Dokumentation der Grenzen: Windows-Code nur per argv, kein zuverlässig lokal
  abrufbarer Auftragsabschluss, WTS/Kontobindung auf Hosted-CI und
  `cancelled` beim Admin-Abbruch gegenüber `interrupted` beim Wiederanlauf.

## 0.1.0 — 2026-10-28

Erstes Release: `desktop-runner.exe` für Windows amd64, Authenticode-signiert
(JanuaPort/januaport#1163 D4). Der Linux-Server-Modus ist im Code, wird aber nicht als
Artefakt ausgeliefert; sein Release braucht den Live-Beleg mit rootless Docker (#1195).

- Fester Container-ENTRYPOINT `/bin/sh`, auch bei eigenem Image-ENTRYPOINT;
  Swap-Limit gleich RAM-Limit und Anleitung zur Lesbarkeit von Geheimnissen
  für UID 65532 einschließlich rootless Sub-UID/Sub-GID-Beispiel
  (JanuaPort/januaport#1195).

- Linux-Server-Modus mit Vertrag 2 und signierten Container-Pins
  (JanuaPort/januaport#1195); Windows und Vertrag-1-Goldens bleiben unverändert.
- Docker-CLI über eine testbare Naht, feste Sandbox-Argumente, Digestvergleich,
  tmpfs-Token über privaten stdin, read-only Betreiber-Geheimnisse und Rootless-Meldung.
- Linux-Dateispeicher 0600/0700 mit HMAC-Integrität, bisherige Journaldeckel,
  Abbruch-/Wandzeit-Bereinigung und Wiederanlauf als interrupted.
- Gehärtete systemd-Vorlage, verpflichtende echte Docker-Proben in Linux-CI
  und reproduzierbare Mutationsproben.
- K1 muss noch `refused/secret` und `limit/wall` im Ergebnisadapter akzeptieren.
  Release-Pakete, Signierung, C3, Objekt-URLs und cosign sind nicht enthalten.
