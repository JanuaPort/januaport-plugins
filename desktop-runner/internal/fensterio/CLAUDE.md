# CLAUDE.md — fensterio

Es gelten zusätzlich die Regeln in `../../CLAUDE.md`, insbesondere Invarianten 16 und 18.

## Aufbau: Datei → Verantwortung

| Datei | Verantwortung |
|---|---|
| `runtime.go`, `runtime_windows.go` | Verbinden geprüfte Apps, Reader, Appstore und Windows-Laufzeit mit Öffnen und Manifestabgleich. |
| `gateway.go` | Übersetzt Brückenaufrufe in App-Token- und App-MCP-Anfragen. |
| `lokal.go`, `exchange.go` | Übersetzen lokale Status-/Haltbefehle und ordnen Antworten auf der Bedienfeld-Pipe zu. |
| `host.go` | Verarbeitet Brückennachrichten im Hintergrund und verwaltet Antwortschlange sowie Host-Epochen. |
| `events.go` | Übersetzt native Navigation, Fenster-, Download- und Nachrichtenereignisse in Urteile und Startschritte. |
| `login.go` | Steuert Anmeldestatus, Personenwechsel, Dokumentwechsel und Profilbereinigung über die View-Schnittstelle. |
| `login_windows.go`, `loopback_windows.go` | Verbinden Geräte-Anmeldung, PKCE, Systembrowser und lokalen HTTP-Rückruf. |
| `login_surface_windows.go` | Baut und aktualisiert die native Anmeldeleiste samt Layout und Dokumentübergang. |
| `webview_windows.go` | Erstellt die WebView-Oberfläche und bindet COM-Ereignisse, Ressourcenfilter und Browserdatenbereinigung ein. |
| `env.go`, `procargs_windows.go` | Prüfen Umgebungsnamen und lesen die Parameter der Browserprozesse für die Startprüfung. |
| `thread_windows.go`, `wait.go` | Verwalten UI-Thread-Zuordnung und begrenztes Warten mit Nachrichtenverarbeitung. |

## K6c: persönliche PC-Anmeldung

- #1321, unverändert Vertrag 5: `apps[].mode` optional, nur a/b/leer, sonst Manifestfehler.
  Anmeldeleiste mit Knöpfen/Meldungen nur bei b oder fehlendem/leerem Feld (alter Kern);
  in a erscheint nichts davon. Ausschließlich Bedienhilfe: Rechte, `/token`, Login-Fluss
  und K6b-2/K6c-Sperren unverändert. **Eine Ausnahme (#1537):** Wechselt eine offene App
  von b (oder leer) nach a, vergisst `Surface.UpdateApps` die persönliche Sitzung:
  `Login.ToDeviceMode` ruft wie beim Personenwechsel `Gateway.Forget` auf, wechselt die
  Host-Epoche, beendet das Dokument (`about:blank`), löscht die Browserdaten und lädt neu;
  danach gibt `Gateway.useDevice` den Gerätezweig frei (`personal=false`), sonst lieferte die
  App dauerhaft `ErrNichtAngemeldet`. Ein laufender Anmeldeversuch wird verworfen, ein Fehler
  im Übergang schließt das Fenster und lässt das Gateway gesperrt. a → a und a → b tun nichts
  davon. Tests: `login_moduswechsel_test.go` (Ablauf, Fehlerpfade, laufende Anmeldung; Mutanten
  ohne `Forget` und mit `personal=true`), `login_moduswechsel_windows_test.go` (Verdrahtung in
  `Surface.UpdateApps`; das echte WebView-Löschen bleibt Sache der nativen Sonde unten). `Reader` → `Runtime.Open` übernimmt den geprüften Modus;
  `Runtime.Apps/Watch` → `Surface.UpdateApps` aktualisiert offene Fenster beim nächsten
  Manifestabgleich. Die Sequenz verhindert einen älteren Anzeigestand beim asynchronen Öffnen.
  Die Startmeldung „Bitte mit dem eigenen Konto anmelden.“ bleibt nach dem ersten `Login.Pump`:
  `NewLogin` und Control-Anlage nutzen `anmeldung.Message("start")`; nach Abmelden bleibt „Abgemeldet.“.
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
- **Invariante: Jeder Wechsel löscht die Browserdaten, bevor neu geladen wird** (Messung auf der Test-VM, #1537). Personenwechsel, Abmelden
  und Wechsel b → a laufen alle über `Login.switchPerson`: `Quiesce`, dann `ClearBrowsingData`, erst in dessen Abschluss `Reload` und die
  Freigabe des Gateways. `ClearBrowsingData` ist die eigentliche Schutzschicht: Ohne es erreichte `history.go(-2)` die Seite der
  Vor-Person, und Chromium stellte deren Formularwerte wieder her. `about:blank` allein schützt davor nicht. Wer den Wechselpfad
  umbaut, behält die Reihenfolge Quiesce → Clear → Reload. Wächter: `TestWechselLaedtErstNachDemLoeschenDerBrowserdatenNeu` (alle drei
  Arten, Fake-View; Mutant: ohne `ClearBrowsingData` rot). Das echte Löschen in WebView2 belegt nur die native Sonde (unten).
- `about:blank` ist ausschließlich während dieses nativen Übergangs zulässig,
  nur im obersten Rahmen. Der Rückruf akzeptiert keine Seitennachrichten. Ansonsten
  bleiben K6b-2-Navigation, Ressourcenfilter, Browser-Netzsperre und WebRTC unverändert.
- `authorize_url`: HTTPS ohne Userinfo, erneute Prüfung direkt vor ShellExecuteW.
  Keine Shell. Keine neue Logout-Route: Abmelden vergisst den RAM-Träger lokal.
- B1–B4: `panel`, `panelio`, `pipe` strikt; einzige Tür `cmd/bedienfeld → fensterio`.
  Nur `client.Reader`/AppAPI, kein Engine/Spawn/Claim/Zustandsschreiben/Widerruf.

## Belege

`login_test.go`: Fake-View einschließlich gleicher Anzeigenamen, Reihenfolge,
Fehler und Abmelden. `TestLoginStartmeldungBleibtNachPumpUndAbmelden` belegt Startmeldung
und Meldung nach Abmelden; Mutant `leistentext-start` entfernt die Initialisierung
in `NewLogin` (lokal ohne GUI/Container). `login_r4_test.go`: laufende und bereits wartende Antworten
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
