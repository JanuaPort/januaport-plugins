# CLAUDE.md — fensterio

Es gelten zusätzlich die Regeln in `../../CLAUDE.md`, insbesondere Invarianten 16 und 18.

## K6c: persönliche PC-Anmeldung

- #1321, unverändert Vertrag 5: `apps[].mode` optional, nur a/b/leer, sonst Manifestfehler.
  Anmeldeleiste mit Knöpfen/Meldungen nur bei b oder fehlendem/leerem Feld (alter Kern);
  in a erscheint nichts davon. Ausschließlich Bedienhilfe: Rechte, `/token`, Login-Fluss
  und K6b-2/K6c-Sperren unverändert. `Reader` → `Runtime.Open` übernimmt den geprüften Modus;
  `Runtime.Apps/Watch` → `Surface.UpdateApps` aktualisiert offene Fenster beim nächsten
  Manifestabgleich. Die Sequenz verhindert einen älteren Anzeigestand beim asynchronen Öffnen.
- `Login.Start` startet nur Hintergrundarbeit. `login_windows.go` ruft den Gerätekanal
  und den Systembrowser auf. `Surface`/`Login.Pump` führen nur native UI-Arbeit aus.
  App-JavaScript kennt keinen Anmelde- oder Abmeldebefehl. Die Pipe bleibt tokenfrei.
- `loopback_windows.go` ist die einzige Listener-Ausnahme: genau einmal
  `net.Listen("tcp4", "127.0.0.1:0")`, nur vom Knopfdruck bis zum ersten passenden
  Rückruf oder höchstens fünf Minuten. `net/http` ist dort ausschließlich Server,
  mit ReadHeaderTimeout und MaxHeaderBytes. Falscher Host/State: 400, nichts verbraucht.
- Rücksprungfehler ausschließlich `kein_zugriff`, `platz_gesperrt`, `abgebrochen`,
  ohne Code und ohne fremden Text. Feste native Meldungen aus `anmeldung.Message`.
  Nur `/session`-403 `kein_zugriff` nach einem Code wird zu `zeitablauf` (A8);
  derselbe Fehler aus dem Callback behält die Zugriffsmeldung.
- Anzeigename ausschließlich in der nativen Leiste, maximal 128 Unicode-Zeichen,
  keine Steuer-/Formatzeichen; nie in Brücke, Pipe oder Log. Wechsel ausschließlich
  nach `person_gewechselt`, fehlend/null bedeutet true. Dieselbe weiterhin lokal
  angemeldete Person behält Eingaben. Ohne lokale Anmeldung wird auch bei false
  bereinigt und neu geladen. `ErrNichtAngemeldet` einer persönlichen Sitzung
  entfernt Namen und „Angemeldet“ aus der Leiste; Netzfehler allein tun das nicht.
- Bei Wechsel/Abmelden sofort Gateway sperren und Host-Epoche wechseln. Alte
  Antworten werden verworfen. Altes Dokument durch `about:blank` beenden, Abschluss
  abwarten, dann `ClearBrowsingDataAll`, erst nach Erfolg neue Sitzung und Seite
  freigeben. Jeder Fehler/20-s-Frist schließt das App-Fenster. Auch beim ersten
  Öffnen werden alte Profildaten vor der ersten App-Navigation gelöscht, auch
  in Modus a. Eine App am PC behält keine lokalen Daten über das Öffnen hinaus.
- `about:blank` ist ausschließlich während dieses nativen Übergangs zulässig,
  nur im obersten Rahmen. Der Rückruf akzeptiert keine Seitennachrichten. Ansonsten
  bleiben K6b-2-Navigation, Ressourcenfilter, Browser-Netzsperre und WebRTC unverändert.
- `authorize_url`: HTTPS ohne Userinfo, erneute Prüfung direkt vor ShellExecuteW.
  Keine Shell. Keine neue Logout-Route: Abmelden vergisst den RAM-Träger lokal.
- B1–B4: `panel`, `panelio`, `pipe` strikt; einzige Tür `cmd/bedienfeld → fensterio`.
  Nur `client.Reader`/AppAPI, kein Engine/Spawn/Claim/Zustandsschreiben/Widerruf.

## Belege

`login_test.go`: Fake-View einschließlich gleicher Anzeigenamen, Reihenfolge,
Fehler und Abmelden. `login_r4_test.go`: laufende und bereits wartende Antworten
über `Login.Start/Pump` verworfen (A9, Mutanten `login-suspend` und
`host-suspend-out`), Ablauf am HTTP-Adapter und Wiederanmeldung ohne lokale
Sitzung. `login_edges_test.go`: alte Antworten, hängendes Gateway,
feste Fehler, Mehrfachklick. `loopback_windows_test.go` und
`login_adapter_windows_test.go`: reine Go-HTTP-Tests, kein Browser/GUI.
`login_wiring_test.go`: Komposition und ShellExecute-Prüfung.
`appmode_test.go`: echter signierter Zustand/Reader, HTTP-/Token-/Brückenweg in beiden
Modi und Watch bei Moduswechsel.
`TestRuntimeAppModeUmgehtPersonRequiredNicht` erzwingt `/token`-409 in a/b/leer am
echten HTTP-Adapter: feste Abweisung und kein MCP-Aufruf (Mutant `person-required`).
`appmode_windows_test.go`: echte Surface-Layoutmethoden
ohne GUI, früher Abbruch vor der Control-Anlage in a. Mutanten `leiste-immer` und
`app-mode-ungueltig` (Feldprüfung am HTTP-/Manifestadapter) und `anzeige-alter-stand`
(älteres Manifest beim asynchronen Öffnen, ohne GUI).
`../client/hygiene_test.go`: exakte Listener-Zählung, rot auch bei null.

Native Sonde **nur BOX/Test-VM**, `JNPT_NATIVE_WEBVIEW_PROBE=1`:
`TestWindowsWebViewAnmeldeleisteNachModus` (echte Controls, Layout und Manifestwechsel),
`TestWindowsWebViewPersonenwechselUndAbmelden` und `TestWindowsWebViewAnmeldeknopf` mit Persistenzkontrolle für
localStorage, sessionStorage, IndexedDB und Service Worker. Sie ist hier lediglich
kompiliert; ohne PASS ist die tatsächliche Bereinigung in WebView2 nicht belegt.
Bestehende Netz-/WebRTC-/Prozessbaumsonden bleiben Pflicht. Service Worker umgehen
Schicht 1; dort trägt ausschließlich die unveränderte Netzsperre im Browserprozess.

`python probe/k6c_mutations.py` aus der Modulwurzel, exklusiv mit dem Container
`k6cp3-test` (überschreibbar mit `JNPT_MUTATION_CONTAINER`, #1321: `j1321p-mode`):
kompilierende rote Mutanten und bytegenaue Wiederherstellung.
Kein nativer WebView-Start im Mutationslauf.
