# Desktop-Läufer

Windows/amd64-Client und Linux-Server-Läufer für JanuaPort, Go ohne CGO.
Der Server-Modus ergänzt Container-Pins (JanuaPort/januaport#1195).
Implementiert und mit Fake-Kern, Linux-Race-Tests und nativen Windows-Sonden
geprüft. Signiertes Windows-Release: `desktop-runner-v0.1.0` (#1163 D4).
Gemeinsame Live-Abnahme mit der Anlage steht aus.
Maßgeblich ist der [Draht-Vertrag](contract/README.md): historischer v1-D0-Vertrag und v2 für Linux-Server. Windows spricht ab 0.3.0
**Vertrag 4**, mit K6b-1 (Fenster-Teil des PC-Fensters, JanuaPort/januaport#1048, noch ohne Release)
**Vertrag 5** in Header und JSON; er braucht einen Kern mit K6a. Das D0-Modul und die alten Goldens bleiben eingefroren.

## Vertrag 4 und Testaufträge

0.3.0 behebt #1298: Signierte Manifeste mit `test_device:true` werden in allen
unterstützten Fassungen (v1 bis v5) angenommen. Der aktuelle Windows-Sync
verlangt Vertrag 5 (Vertrag 4 plus `apps`); ältere verifizierte Caches (v1, v3, v4)
liefern beim Upgrade nur die Sequenz für den neuen Sync. Der Parser bleibt strikt:
`apps` ist unter v1–v4 gesperrt und unter v5 Pflicht, Setup-Beschriftungen haben
höchstens 40 Unicode-Zeichen.
`apps[].mode` ist optional: nur `a`, `b` oder leer; andere Werte sind Manifestfehler.
Vertrag 5 bleibt unverändert. Der Modus steuert ausschließlich die native Anzeige.
`pins[].setups` enthält feste Eingabe und `frei`-Felddefinitionen;
`pins[].grace_hours` überschreibt die Gerätefrist.

Die HTTP-Adapter bieten Setup-Start mit Manifestsequenz und zufälliger Nonce,
`POST /halt`, Test-`result` und PNG-Upload unter `jobs/{id}/screenshot`.
Ein Testauftrag verlangt signiertes `test_device:true` **und**
`test_<erste 8 Zeichen der Geräte-ID>` als Pin, gebunden an den verifizierten Job.
Normale Ergebnisse dürfen kein `result` tragen; Screenshots gehen nur für
Testaufträge an die Anlage. Testresultate: JSON bis 64 KiB, PNG bis 4 MiB.
Die signierte Jobfreigabe wird für späteres Ergebnis-Replay separat mit DPAPI
gespeichert und vor dem Senden erneut geprüft. Der Kern begrenzt Bilder auf fünf
und auf laufende Tests beziehungsweise zwei Minuten nach deren Ende.

Der Werkzeugaufruf des Laufs (`mcp` mit Lauf-Token) schickt `Accept: application/json, text/event-stream`
und liest die SSE-Antwort des Kerns (go-sdk stateless, `Cache-Control: no-cache, no-transform`); die
Geräte-Token-Wege und der App-Zweig bleiben bei `application/json` mit `no-store` (#1340).

L1 enthält Adapter und K8; L2 ergänzt den lokalen Halt im Engine.
L3 ergänzt Pipe und Setup-Zulassung, L4 das native Bedienfeld.
L5 ergänzt den `jnpt`-Helfer und die Go-seitig freigegebene Bildschirmaufnahme.
Unveränderte Kernreferenzen mit SHA-256:
[golden-v3](internal/client/testdata/golden-v3/README.md).

Grenzen des geprüften Kernstands: Bei einem noch unbekannten Lauf weist die
Nachmelderoute Slots außerhalb der historischen Frist und Werkzeugläufe ab,
auch bei `refused/offline`. Die markierte Meldung erreicht dann den Offline-Zähler
nicht; es gilt die bestehende gezählte Verwerfung nach zehn HTTP-400-Antworten.
Auch `result:null` wird vom geprüften Kernstand abgewiesen. L5 lässt bei
`jnpt.ergebnis(None)` das Feld vollständig weg (Kern-Folgeänderung #1300).

## PC-Fenster, Teil 1: Vertrag 5 und fensterlose Logik (K6b-1, ohne Release)

JanuaPort/januaport#1048 (Hyperfokus #1313 B2). Dieser Stand enthält **kein Fenster**:
keinen WebView2-Adapter, keine Änderung am Bedienfeld, kein Release. Er legt die
prüfbare Grundlage, die K6b-2 an das Fenster hängt. Es gibt keinen lokalen Fensterbeleg.

- **Vertrag 5:** Windows sendet Header und `contract: 5` (siehe [contract/README.md](contract/README.md),
  Spiegel `contract/golden-v5/`). Ein Kern ohne K6a lehnt den Läufer ab; Update-Reihenfolge
  wie bisher zuerst Kern, dann Läufer.
- **App-Paket:** `AppStore` holt das Paket (`packages/{commit}?path=apps/<name>`, Quelle
  Speicher), prüft es vollständig (Git-IDs, SHA-1DC/SHA-256, Objektmenge, Namen, Modus
  100644) und danach die apphost-Regeln als markierte Kopie (`copied_apphost.go`:
  MIME-Liste, Geheimnismuster, keine aktive Markup). Das CSP-Literal `fenster.CSP` setzt der Läufer
  selbst als erstes Element in jedes `<head>` ein und versiegelt danach (davor `<meta charset="utf-8">`, SEC-R4 F4); HTML mit eigener CSP-Meta oder
  ohne sauberes `<head>` wird abgewiesen, `Open` prüft das führende Literal erneut. Die apphost-CSP wird
  nicht übernommen. Ablage privat unter `<Zustand>\apps\<name>\<commit>\`, Siegel (Pfad,
  Größe, SHA-256) im verschlüsselten Zustand, Prüfung vor **jedem** Öffnen, nie stilles
  Reparieren. Widerruf löscht Ablage und Siegel mit dem Zustand.
- **App-Token:** `POST apps/{name}/token`, nur im RAM (`fensterio.Gateway`), in Modus a bei 401 genau
  einmal neu geholt. Modus b verlangt den nativen Anmeldeweg unten. Das Token hat keinen Getter und druckt sich nie.
- **Fenster-Policy (`internal/fenster`, reines Go):** exaktes CSP-Literal,
  `NavigationAllowed` (nur `.html`-Seiten auf `https://app-<name>-pc.jnpt.invalid`, Einstieg `/index.html`, kein Port/Userinfo/
  Großschreibung/Schema-Tricks), `MessageAllowed` (oberster Rahmen, eigene Origin, vier
  Befehle), `Settings()` (nur Skript und Webnachrichten an), Umschlag `{id, befehl, args}`
  (höchstens 256 KiB, strikt) und die Brücke: `status`, `anhalten` (lokal, genau zwei
  Pipe-Operationen), `werkzeuge` (tools/list), `werkzeug` (tools/call). Antworten tragen
  nur feste Codes (`abgelehnt`, `antwort_ungueltig`, `args`, `befehl`, `fehler`,
  `nicht_angemeldet`, `nicht_erreichbar`, `umschlag`, `zu_gross`, `zu_viele`) und nie
  einen Träger; Ergebnisse mit Token-Muster werden verworfen.

Der Fensterteil folgt im nächsten Abschnitt (K6b-2). Mutationen: `probe/k6b_mutations.py` (Container mit Go und Python).

## Persönliche Anmeldung am PC (K6c P3, noch ohne VM-Beleg)

Die Anmeldeleiste erscheint in Modus b. In Modus a erscheinen weder Anmeldeleiste
noch Anmelde-/Abmeldeknopf oder Anmeldemeldung. Fehlt `apps[].mode` oder ist es leer
(älterer Kern), erscheint die Leiste wie bisher. Ein neuer geprüfter Manifeststand
aktualisiert auch offene Fenster beim nächsten Abgleich. Rechte, `/token`, Login-Fluss
und alle Sperren aus K6b-2/K6c bleiben unverändert; der Modus ist nur Bedienhilfe.

Die Leiste zeigt anfangs „Bitte mit dem eigenen Konto anmelden.“ auch nach dem
ersten `Login.Pump`. `NewLogin` und die native Control-Anlage verwenden dafür
`anmeldung.Message("start")`. Nach Abmelden bleibt die Meldung „Abgemeldet.“.

Bei `nicht_angemeldet` in einer Modus-b-App startet **Anmelden** in der nativen
Leiste den Systembrowser. Dort das eigene Konto wählen und die Bestätigungsseite
prüfen. Danach zum App-Fenster zurückkehren. Die Leiste zeigt das Konto; Fehler
erscheinen als feste Meldung. Die Anmeldung läuft höchstens fünf Minuten. Ein
neuer Versuch braucht einen erneuten Klick. **Abmelden** vergisst die Sitzung
lokal und löscht die Browserdaten der App; dafür gibt es keine Kern-Logout-Route.
Die Systembrowser-Anmeldung beim IdP bleibt bestehen.

Voraussetzung: Kern mit Vertrag 5/P2 und `JNPT_APPS_LISTEN`. `/token` meldet für
Modus b `409 person_required`. `/login` und `/session` laufen ausschließlich über
den Gerätekanal. Der PC hält kein JWT und keinen Refresh-Token. Nach Ablauf ist
ein neuer Browser-Login erforderlich. Sobald ein Aufruf die persönliche Sitzung
als nicht angemeldet zurückweist, verschwinden Kontoname und „Angemeldet“ aus der
nativen Leiste. Ein vorübergehender Netzfehler meldet die Person nicht ab.
Keine Anmeldung durch App-JavaScript.

Personenwechsel werden ausschließlich über `person_gewechselt` erkannt, im
Zweifel true. Gleiche Namen sind kein Gleichheitsbeweis. Bei derselben weiterhin
angemeldeten Person bleiben Seite und Eingaben; ohne lokale Anmeldung wird auch
bei `person_gewechselt=false` neu geladen; bei Wechsel und Abmelden wird das alte Dokument
beendet, Browserdaten werden über ClearBrowsingDataAll gelöscht und die App
neu geladen. Eine gescheiterte Bereinigung schließt das App-Fenster. Auch beim
Öffnen werden alte Profildaten vor der ersten App-Seite gelöscht, auch in Modus a:
Eine App am PC behält keine lokalen Daten über das Öffnen hinaus. Anzeigenamen
(maximal 128 Unicode-Zeichen, ohne Steuer-/Formatzeichen) gehören nur in die
native Leiste, nie in Log oder Brücke. Träger bleiben im RAM.

Genau ein kurzlebiger IPv4-Listener auf `127.0.0.1:<zufälliger Port>` im
Bedienfeld: nur ab Klick bis zum ersten passenden Rückruf oder fünf Minuten.
Falscher Host/State liefert 400 und verbraucht nichts. Rücksprungfehler sind
`kein_zugriff`, `platz_gesperrt`, `abgebrochen`, jeweils ohne Code/freien Text.
Nur `403 kein_zugriff` beim anschließenden `/session`-Tausch wird als Zeitablauf
angezeigt: Der Einmalcode kann während der Bestätigung ablaufen. Die
Zugriffsmeldung aus dem Callback bleibt unverändert.
Die Authorize-URL wird als HTTPS ohne Userinfo geprüft und über ShellExecuteW
geöffnet. Netzwerk und Brücke laufen außerhalb des UI-Threads; Halt bleibt
bedienbar. Die Pipe und die Wächter für panel/panelio/pipe bleiben unverändert.
Die WebView behält ihre Netzsperre einschließlich WebRTC; Service Worker umgehen
den Ressourcenfilter, dort trägt allein die Sperre im Browserprozess.

Loopback belegt nur den Browserrechner, nicht den Besitzer des Ports.
Weitergereichte Links/Codes und Mehrbenutzer-Rechner (auch ungekoppelte) bleiben
Sicherheitsgrenzen. Das Audit belegt das Konto, nicht den Menschen am Platz.
IdP-Sperren und Gruppenentzug wirken erst bei JWT-Ablauf. Keycloak und der
HTTPS→Loopback-Sprung wurden hier nicht live geprüft.

BOX fährt zusätzlich `TestWindowsWebViewAnmeldeleisteNachModus`, `TestWindowsWebViewPersonenwechselUndAbmelden` und `TestWindowsWebViewAnmeldeknopf` mit
`JNPT_NATIVE_WEBVIEW_PROBE=1`, interaktiver Sitzung und `-test.timeout 20m`:
Persistenzkontrolle, gleiche Namen, localStorage/sessionStorage/IndexedDB/Service
Worker nach Wechsel und Abmelden. Hier nur cross-kompiliert, keine GUI gestartet.
Die bestehenden WebRTC-, Netz- und Prozessbaumsonden bleiben Pflicht.
Go-HTTP-Tests `TestLoopback*` und `TestLoginVomKnopfBisSessionAmAdapter` öffnen
keinen Browser. Mutationen: `python probe/k6c_mutations.py` mit `k6cp3-test`
(anderer eigener Container über `JNPT_MUTATION_CONTAINER`, für #1321 `j1321p-mode`).
`leiste-immer` prüft ausschließlich das echte Surface-Layout ohne GUI;
`app-mode-ungueltig` entfernt die Feldprüfung am HTTP-/Manifestadapter.
`leistentext-start` entfernt die Startmeldung aus `NewLogin`; der Regressionstest
`TestLoginStartmeldungBleibtNachPumpUndAbmelden` wird rot (lokal ohne GUI/Container).
Das lokale Adapter-Golden ergänzt den bytegleichen P2-Kernspiegel aus ea50aa7a:
`contract/golden-v5/contract5_login_routes.json`, geprüft durch `TestGoldenV5AusKern` und `SHA256SUMS`.
Der Manifestspiegel enthält ab #1321 `mode` (SHA-256
`7cfb70018cb3265f39245b43de2e0c64f2859b8be366c4b34509454058656ae6`);
ein Manifest ohne das Feld bleibt ausdrücklich getestet.

## PC-Fenster, Teil 2: App-Fenster im Bedienfeld (K6b-2, ohne Release)

JanuaPort/januaport#1048. Das Bedienfeld zeigt zugewiesene PC-Apps in einem eigenen Fenster mit WebView2.
Voraussetzung auf dem PC: die **WebView2-Runtime** (Evergreen) und ein Kern mit K6a. Fehlt sie oder eine
nötige Schnittstelle, öffnet das Fenster nicht und das Bedienfeld meldet das nativ; es gibt keinen Weg ohne die Sperren.

- **Menü:** je zugewiesener App ein Eintrag „App öffnen: <name>“ (aus dem vom Läufer geprüften Manifest).
  Die Apps legt der Läufer beim Sync ab; das Bedienfeld liest nur (`client.Reader`), schreibt nie in den
  Zustand und löscht bei einem Widerruf nichts. Das Gerätetoken wird erst beim Öffnen einer App gelesen,
  steht nie in Umgebung, Kommandozeile oder Benutzerdatenverzeichnis und läuft nie über die Pipe.
- **Blockiert nie den Halt:** Gateway, Brücke und Pipe-Gespräch laufen in Goroutinen, nie auf dem
  UI-Thread. Ein hängendes Gateway hält Anhalten nicht auf, ein WebView2-Absturz schließt nur das Fenster.
- **Native Sperren** (Pflicht, sonst öffnet das Fenster nicht): NavigationStarting/FrameNavigationStarting
  (nur `.html`-Seiten der eigenen App; Abbruch der Navigation, auch Redirect und meta refresh; die Anfrage selbst sperren Ressourcenfilter und Netzsperre),
  NewWindowRequested (nie ein Fenster), DownloadStarting (Abbruch), WebMessageReceived mit Quelle,
  PermissionRequested (Deny), ProcessFailed. Externe `https`-Links öffnen nur nach einem nativen
  Ja-Dialog mit Host im Standardbrowser. Auf Testgeräten zeigt eine native Leiste „Test – Testgerät“.
- **Benutzerdatenverzeichnis:** `%LOCALAPPDATA%\JanuaPort\desktop-runner\webview\<app>` mit privater DACL;
  der Widerruf des Läufers löscht es, ohne Verweisen zu folgen.
- **Restgrenzen:** Die ACL der Pipe schützt nicht gegen den WebView2-Hauptprozess (gleiches Konto, mittlere
  Stufe). Die Bibliothek spiegelt jede Seitennachricht an die Seite zurück. Alte App-Fassungen unter
  `apps\` bleiben liegen. Die nativen Ereignisse sind erst durch die Sonden auf dem Testgerät belegt
  (`internal/fensterio/webview_native_windows_test.go`).

### Brücke: Aufruf aus der Seite

```js
// Seite -> Bedienfeld: ein STRING (kein Objekt)
chrome.webview.postMessage(JSON.stringify({ id: "1", befehl: "werkzeuge", args: {} }));

// Bedienfeld -> Seite: e.data ist ein JSON-String. Nachrichten OHNE "ok" ignorieren.
chrome.webview.addEventListener("message", (e) => {
  const m = JSON.parse(e.data);
  if (typeof m !== "object" || m === null || !("ok" in m)) return;
  // { id, ok: true, ergebnis } | { id | null, ok: false, fehler: "<code>" }
});
```

| `befehl` | `args` | `ergebnis` |
|---|---|---|
| `status` | `{}` | `{zustand: "arbeitet" oder "pausiert" oder "angehalten" oder "stoerung", bis?: RFC3339, netz: bool}` |
| `anhalten` | `{}` | `{}` (erst wenn der Läufer „angehalten“ bestätigt) |
| `werkzeuge` | `{}` | Ergebnis von `tools/list` des Gateways, z. B. `{tools: [...]}` |
| `werkzeug` | `{name, arguments}` | Ergebnis von `tools/call`, z. B. `{content: [...], isError?}` |

`id`: String `[A-Za-z0-9_.-]{1,64}`. `name`: `[A-Za-z0-9_.-]{1,128}`, `arguments`: ein Objekt. Höchstens 256 KiB je
Nachricht, keine unbekannten und keine doppelten Schlüssel. `id` ist in der Antwort `null`, wenn der
Umschlag nicht lesbar war. **Feste Fehlercodes:** `abgelehnt`, `antwort_ungueltig`, `args`, `befehl`,
`fehler`, `nicht_angemeldet`, `nicht_erreichbar`, `umschlag`, `zu_gross`, `zu_viele`. Nie ein Fehlertext, nie ein
Token; ein Ergebnis mit Token-Muster wird zu `antwort_ungueltig`. Zeitgrenze 60 s je Befehl (lokal 5 s), höchstens
8 Gateway- und 4 lokale Aufrufe gleichzeitig (`zu_viele`). Die Seite sendet kein `fetch` ins Netz
(`connect-src 'none'`); alles geht über die Brücke. Der Läufer setzt `<meta charset="utf-8">` und die CSP selbst
ein, Seiten sind nur `https://app-<name mit - statt _>-pc.jnpt.invalid/<pfad>.html` (Einstieg `/index.html`).

## Skript-Helfer `jnpt` (0.4.0 in Arbeit, L5)

`import jnpt` steht neben `januaport` bereit. Alle Aufrufe verwenden ausschließlich
den vorhandenen Rahmenkanal. Der Helfer eröffnet keinen Netzweg.

| Aufruf | Verhalten |
|---|---|
| `jnpt.frage(text, optionen, frist_s)` | Nullbasierter Auswahlindex; `None` ohne Bedienfeld, bei Frist oder `fehler`. Text bis 500 Zeichen, 2–4 Optionen mit je bis zu 40 Zeichen, Frist 5–3600 Sekunden. |
| `jnpt.ergebnis(wert)` | JSON bis 64 KiB; letzter Aufruf gilt. `None` löscht das Ergebnis, kein `result`-Feld. Normale Aufträge verwerfen gültige Werte still. |
| `jnpt.screenshot()` | `True` nach Aufnahme und Upload, sonst `None`. Nur für Testaufträge mit signiertem `test_device:true` und Geräte-Testpin, niemals eine Aufnahme vor dieser Prüfung. |

Beispiel: `jnpt.ergebnis({"wahl": jnpt.frage("Fortfahren?", ["Ja", "Nein"], 30)})`.
JSON-Größen gelten in Bytes, einschließlich der Escape-Zeichen des Reports.
Nicht serialisierbare Werte und NaN/Infinity erzeugen einen Python-Fehler.
Ungültige rohe Rahmen (auch unbekannte Felder/Typen oder übergroße Ergebnisse)
beenden den Lauf mit `internal`; gültige Screenshot-Anfragen ohne Freigabe
antworten mit `fehler`, im Helfer also `None`.

**L3-2:** Sämtliche Grenzen und Berechtigungen werden in Go am Läufer geprüft.
Python läuft im selben Interpreter wie das Skript und bildet keine Grenze.
Historische signierte Jobfreigabe, Job-ID, Sequenz, Commit und Pin/Pfad/Algo
werden vor Aufnahme und Ergebnisannahme gebunden; Setup-/Offline-Läufe sind
keine Testaufträge. Der unveränderte L1-Adapter prüft vor HTTP erneut.

GDI erfasst in Go ohne CGO den ganzen primären Bildschirm, PNG bis 4 MiB.
Höchstens fünf Aufnahmeversuche je Lauf, einschließlich Fehlern und der
automatischen Aufnahme am Laufende. Ein Versuch bleibt für diese Endaufnahme
reserviert; somit höchstens vier explizite Aufnahmen. Uploadfehler ändern das Skriptergebnis nicht; Bilder werden nicht
nachgeliefert. Bei gesperrter Sitzung zeigt die Aufnahme den Sperrbildschirm;
GDI kann je nach Sitzung auch nicht verfügbar sein. Linux liefert einen Fehler.

Nach lokalem Anhalten entfällt die Endaufnahme (SEC-R4 F1): Der Läufer prüft
den L2-Haltzustand unmittelbar davor unter seiner Sperre und nimmt außerhalb
der Sperre auf. Nach Ablauf der Wandzeit bleibt die Endaufnahme erhalten.

H3 bleibt unverändert: kein Bedienfeld-Pipe-Handle und kein `jnpt*` in der
Kind-Umgebung/Kommandozeile; das Modul wird ausschließlich im Bootstrap erzeugt.

Prüfung: `python3 -B -m unittest discover -s internal/client -p test_bootstrap.py`,
Go-Tests einschließlich echtem Python-Bootstrap und rohen Rahmen sowie
`python probe/l5_mutations.py` exklusiv in `l5-test` (Go 1.26.10).
`TestWindowsScreenshotGDI` und `TestWindowsScreenshotGDIChild` werden lokal nur
cross-kompiliert. LEAD startet CI/GATE mit `JNPT_GDI_PROBE=1`; der Hilfsprozess
hat eine feste 15-Sekunden-Grenze. Ohne Opt-in bzw. interaktive Sitzung sichtbarer
Skip, keine lokale Aufnahme. H3: `TestWindowsPythonKindOhneBedienfeldPipe`.

## Lokaler Halt (0.4.0 in Arbeit, L2)

Der Windows-Engine bietet intern `Halt(state, until)`, `Pause(15|60|0)` und
`HaltStatus()`. Es gibt in L2 noch keinen CLI-Befehl und keine Bedienoberfläche.
`until` wird als UTC gespeichert; 0 Minuten bedeutet bis zur Fortsetzung.
Seit L3 führt der Läufer die entsprechenden Pipe-Befehle über diese Schnittstelle aus.

- **Anhalten:** setzt `angehalten` und beendet den laufenden Job-Baum sofort
  über Cancel/Kill (`TerminateJobObject`), auch ohne Netz. Sein Ergebnis ist
  `cancelled`. Halt-Prüfung unmittelbar vor Spawn, Zustand und Kill liegen unter
  derselben Engine-Sperre (L2-1); auch ein bereits abgeholter Claim startet danach
  keinen Prozess.
- **Pause:** sperrt neue Slots, Claims und Testaufträge, lässt den laufenden Lauf
  regulär enden und endet bei `until` automatisch. Offene Long-Polls werden
  abgebrochen. `ready` im Heartbeat bleibt von Halt und Pause unabhängig.
  Auch die Slot-/Markerreservierung prüft Halt unter derselben Engine-Sperre.
- **Fortsetzen:** entfernt nur die lokale Einschränkung (B3). Konto, Sitzung,
  Paket, Manifest und Offline-Frist gelten weiter. Verpasste Slots einschließlich
  der angefangenen Fortsetzungsminute werden ohne Ergebnis übersprungen;
  kein Nachholen. Halt-Befehle selbst starten nichts. Eine Pause kann einen
  angehaltenen Zustand nicht abschwächen; dafür ist Fortsetzen erforderlich.

Die atomare DPAPI-CurrentUser-Datei `halt` enthält `{state, until}` und wird vor
dem ersten Takt gelesen (B6). Nur eine fehlende Datei bedeutet `arbeitet`.
Unlesbare/ungültige Dateien bedeuten `angehalten` und eine dauerhafte lokale
Störung mit festem Logcode `8 1` (L2-2). `HaltStatus()` liefert das Störungsflag;
erst ein erfolgreich gespeicherter expliziter Wechsel hebt es auf. Auch ein
Speicherfehler sperrt lokal und wird zurückgegeben; ohne erfolgreichen Schreibzugriff
kann ein neuer Halt nicht für den nächsten Neustart garantiert werden.

Wechsel werden über den vorhandenen `POST /halt`-Adapter nachgeliefert,
auch während eines laufenden Skripts. Es bleibt nur der letzte Zustand offen;
höchstens ein Versuch je zehn Sekunden, einschließlich Fehlern. Die ebenfalls
atomar geschützte Datei `halt-attempt` erhält die Bremse über Neustarts. Ist sie
unlesbar oder nicht schreibbar, bleibt die Meldung ausstehend. Eine rückwärts
gestellte Uhr verzögert die Nachlieferung. Ein Neustart meldet den letzten
gespeicherten Halt erneut. Störung wird in L nur lokal angezeigt und nicht an den Kern gemeldet (§6.6);
auf dem Draht steht der Halt-Zustand `angehalten`.

Das Bedienfeld zeigt Störungen dauerhaft mit eigener Farbe und dem festen Text
„Störung. Anhalten: abmelden oder über den Admin halten lassen.“ Der Job-Abbruch erfasst den
Job-Baum, keine über WMI, Aufgabenplanung oder COM außerhalb gestarteten Prozesse.
DPAPI und diese Schnittstelle bilden keine Grenze gegen andere Prozesse derselben
Person. UIA-Skripte desselben Benutzers können das Bedienfeld bedienen; eine Pause
kann das laufende Skript selbst aufheben, endgültig für den Baum ist nur Anhalten.
Linux-Server und Vertrag 2 bleiben unverändert.

Prüfung: `python probe/l2_mutations.py` im exklusiven Worktree mit Container
`l2-test` (Go 1.26.10). Linux-Race-Tests prüfen die realen HTTP-/State-Adapter;
`TestWindowsAnhaltenBeendetEnkel` prüft in Windows-CI den echten Job-Baum und
DPAPI-Neustart. Lokal wird diese native Sonde nur kompiliert.

## Bedienfeld (0.4.0 in Arbeit, L4)

`cmd/bedienfeld` baut `jnpt-bedienfeld.exe`, Windows-only, ohne CGO:
`CGO_ENABLED=0 GOOS=windows go build -ldflags=-H=windowsgui -o jnpt-bedienfeld.exe ./cmd/bedienfeld`.
Eigener Benutzerprozess; L6 installiert beide Exe aus demselben signierten
Release im selben Ordner mit jeweils einer Anmelde-Aufgabe. Ein unsignierter Branch-Build besteht absichtlich
die Serverprüfung nicht.

Tray: grün „Arbeitet“, gelb „Pausiert“, rot „Angehalten“, violett „Störung“.
Menü: Zustand, Pause 15 min / 1 h / bis Weiter, Weiter, Anhalten/Fortsetzen,
Manifest-Setups, auf Testgeräten „Test“ als Anzeige, Beenden nur des Bedienfelds.
Gesperrte Setups sind grau; nur `braucht_eingabe:true` ergänzt „braucht Eingabe“.
`bis` erscheint als lokale Uhrzeit, offline + Fristablauf als
„offline, Frist abgelaufen“.

§6.6: `stoerung`, `fehler{stoerung}`, unerreichbare Pipe oder gescheiterte
Serverprüfung bleiben dauerhaft violett mit exakt
„Störung. Anhalten: abmelden oder über den Admin halten lassen.“
Erst erneute erfolgreiche Serverprüfung und gültiger `zustand` löschen die
Störung. Andere Fehler löschen sie nicht. Wiederverbindung nur über `pipe.Dial()`,
mit 5 bis 60 Sekunden Abstand, ohne Ausweichnamen; nichts an ungeprüfte Pipes.
Fehlender Status oder blockierendes Schreiben bricht die Verbindung nach fünf Sekunden ab.

`internal/panel` entscheidet als reines Linux-testbares Modell über Anzeige,
Menü, Dialog und Befehle. `internal/panelio` ist der getrennte I/O-Adapter.
Win32 rendert auf einem mit `runtime.LockOSThread` gebundenen UI-Thread;
Pipe-Goroutines wecken ihn über `PostMessage`, ohne Go-Zeiger in LPARAM.
Kein Credential und kein eigener Netzweg. `TestPanelImportWaechter` verbietet
transitiv `net/http`, `internal/client` und `net` außer über `internal/pipe`.
Die benannte Ausnahmeliste `importExceptions` ist leer. Win32 nutzt `syscall`
mit System32-DLLs; direktes `x/sys/windows` würde dessen transitives `net` hereinziehen.
DLL-/Proc-Auflösung ist geprüft und löst keinen Panic aus. Fehlt eine API,
bleibt das Modell in Störung und startet keine Pipe-Verbindung. Das Tray zeigt
den festen Störungstext, sofern seine grundlegenden APIs verfügbar sind;
fehlen diese, endet der Prozess ohne Anzeige und ohne Panic.

Plan-Invariante 14: Fenster erst nach Klick auf „Rückfrage anzeigen“;
Titel „JanuaPort – <titel>“, statischer Text und 2–4 gewöhnliche Knöpfe.
`WS_EX_NOACTIVATE`, `SW_SHOWNOACTIVATE`, kein Default-Knopf, Anfangsfokus,
`SetForegroundWindow`, `SetFocus` oder `SetActiveWindow`. Keine Toasts oder Hotkeys.
Die einzige Sprechblase ist der Hinweis bei einer neuen Rückfrage (#1240): ein
Windows-Hinweis am Symbol (`NIF_INFO`, ohne Ton) mit festem Titel „JanuaPort“ und festem
Text „JanuaPort braucht eine Antwort“, genau einmal je neuer Rückfrage. Er trägt weder Titel
noch Text der Rückfrage noch den Pin-Namen, weil er sichtbar auf dem Bildschirm steht; der
Inhalt bleibt in Menü und Dialog. Er aktiviert nichts und öffnet den Dialog nicht.
Höchstens ein Dialog; zweite Frage nicht anzeigen.
`frage_weg`, Anhalten, Trennung und Frist schließen ohne Antwort. X verbirgt
die Frage bis zum erneuten Klick oder Fristende.
UIA-Skripte desselben Benutzers können das Bedienfeld bedienen;
endgültig für den Baum ist nur Anhalten.

Prüfung: `python probe/l4_mutations.py`, exklusiver Container `l4-test`
(Go 1.26.10), Linux-Race-Tests, Linux-/Windows-vet, GUI-Cross-Build und D0.
`TestWindowsBedienfeldProzesssonde` baut die echte Exe, startet ohne Argumente
und prüft PEB/Umgebung sowie Handle-Vererbung mit positiver Gegenprobe.
`CompareObjectHandles` wird aus `kernelbase.dll` nach erfolgreichem `Find()`
verwendet; fehlt die API, überspringt die Sonde mit Begründung.
`TestWindowsBedienfeldFehlenderProc` prüft echte fehlende DLLs/Procs ohne GUI;
`TestBedienfeldProcAufloesung` bewacht Panic-Freiheit und die Verbindungssperre.
Sie läuft bereits durch `go test ./...` im bestehenden Windows-Job;
keine Workflowänderung. Die Startsonde prüft keinen sichtbaren Tray;
ein Tray-Rauchtest ist nicht enthalten. Lokal native Tests/Mutanten nur kompilieren.
Fokusverhalten, Tray und signierte Verbindung bleiben Nachweise durch CI/GATE
auf dem Testgerät, niemals lokale GUI-Proben auf dem Arbeitsrechner.

## Lokale Pipe (0.4.0 in Arbeit, L3)

Der Windows-Läufer bietet genau einen benannten Listener unter
`\\.\pipe\JanuaPort.Bedienfeld.<SHA-256(SID)[:16]>` an; der Suffix sind die ersten
16 kleinen Hex-Zeichen des Hashes der Benutzer-SID als UTF-8. Linux-Server und
Vertrag 2 bleiben unverändert. L4 ergänzt Bedienfeld-Exe und Tray;
L5 ergänzt `bootstrap.py`; L6 ergänzt die gemeinsame Installation und Release-Bindung.

**B1 / L3-1 / H1:** geschützte DACL nur für Benutzer- und Logon-SID mit
`FILE_GENERIC_READ | FILE_WRITE_DATA` (`0x12008b`). Kein `GENERIC_WRITE`, weil
dessen `FILE_APPEND_DATA` bei Pipes weitere Serverinstanzen erlauben würde.
Die native Rechteprobe versucht eine echte zweite Serverinstanz mit freiem
Instanzplatz und der am Listener ausgelesenen DACL. Sie verwendet auch unter
erhöhter CI ein geprüftes nicht erhöhtes Medium-Token ohne aktive Admin-SID
oder Privilegien und prüft zusätzlich den Benutzer als Besitzer. Eine Gegenprobe
mit erweitertem Instanzrecht muss gelingen. `CreateFile(FILE_APPEND_DATA)`
öffnet nur einen Client-Endpunkt und belegt allein keine weitere Serverinstanz.
Die Gegenprobe verwendet eine ausdrückliche Benutzer-ACE und das Medium-Label
`S:(ML;;NWNR;;;ME)`. Sie läuft vollständig in einem per `CreateProcessAsUserW`
gestarteten nicht erhöhten Hilfsprozess, der beide Instanzen anlegt. Er prüft
sein primäres Token vor der ersten Pipe-Anlage; bloße Thread-Impersonation im
erhöhten CI-Prozess reicht nicht. Quelle, gefiltertes Token und Kind müssen
null Restricting-SIDs haben, damit keine zusätzliche Zugriffsprüfung einen
falsch grünen Negativtest erzeugt. Ein echtes Token mit einer Restricting-SID
wird in einer eigenen Gegenprobe abgewiesen. Schmale und breite DACL werden
im selben Kind geprüft; die Negativprobe gegen die Produktions-DACL im
Elternprozess bleibt zusätzlich erhalten. Beide Instanzen behalten ausdrückliche
Deskriptoren; Besitzer, Label und Kontroll-ACE werden am Handle nachgeprüft.
Kein Rückfall auf einen erhöhten Helfer. Die Hilfsprozess-Proben sind Opt-in
über `JNPT_NATIVE_TOKEN_PROBES=1`: `workflow_dispatch` mit gesetzter Variable
in einer interaktiven Sitzung oder GATE auf dem Testgerät. Betroffen sind nur
`TestWindowsPipeClientDarfKeineInstanzAnlegen/standard_process` und
`TestWindowsPipeRestrictingSIDsVerweigert`. Ohne Variable melden sie:
„opt-in: Hilfsprozess mit gefiltertem Token braucht interaktive Sitzung (JNPT_NATIVE_TOKEN_PROBES=1)“.
Mit gesetzter Variable bleibt ein Startfehler ein Fehler, niemals ein Skip.
Gehostete Dienst-Sitzungen können den Helfer mit `STATUS_DLL_INIT_FAILED`
abweisen; Window-Station- und Desktop-ACLs werden dafür nicht verändert.
Die Negativprobe `production_server` gegen die Produktions-DACL läuft weiterhin
ohne Hilfsprozess und ohne Opt-in. Low-IL READ/WRITE, Fremdkonto, Remote,
Squatting und die strukturellen DACL-Tests unter Linux bleiben unabhängig davon.
Ausdrückliches Label `S:(ML;;NWNR;;;ME)` sperrt Low-IL beim Lesen und Schreiben;
keine AppContainer-/Capability-ACE. `PIPE_REJECT_REMOTE_CLIENTS` sperrt Remote-
Clients. **B2:** `FILE_FLAG_FIRST_PIPE_INSTANCE`, erste Instanz bleibt auch beim
Wiederverbinden offen. Ist der Name besetzt oder die Pipe nicht anlegbar, läuft
der Läufer ohne Pipe weiter: fester Logcode `9 1`, ein Versuch, kein Ausweichname.

Der Server prüft den Client-Bildpfad auf `jnpt-bedienfeld.exe` im eigenen Ordner.
Die L4-Clientbibliothek `internal/pipe.Dial` prüft vor dem ersten Schreiben
`GetNamedPipeServerProcessId`, `desktop-runner.exe` im eigenen Ordner, die
Benutzer-SID im Server-Prozesstoken (H2) und Authenticode mit demselben
Herausgeber wie die eigene Exe. Der komplette Subject-DER stammt aus der von
WinVerifyTrust geprüften Unterzeichnerkette. Dateien bleiben während dieser
Prüfung gegen Schreiben/Löschen geöffnet. Fehler ergeben ausschließlich
„Störung“. Keine UI und kein Netzabruf für die Signaturprüfung; fehlt lokal
der erforderliche Vertrauens-/Sperrlistenstand, wird ebenfalls verweigert.

**A1 / SEC K6b (c): ACL schützt nicht gegen den WebView2-Hauptprozess.**
Er hat die Rechte derselben Person auf Medium-IL. Der Bildpfad ist
Tiefenverteidigung, keine Grenze gegen gleichberechtigte Prozesse oder
UI-Automation. Die Integritätsmarke schützt gegen Low-IL-Clients. Belegung
oder Blockieren durch gleichberechtigte Prozesse bleibt möglich. Störung ist
lokal; die dauerhafte Tray-Anzeige nennt den Ausweichweg wörtlich:
„Störung. Anhalten: abmelden oder über den Admin halten lassen.“

Das Protokoll besteht aus vier Byte Big-Endian-Länge und 1–4096 Byte UTF-8-JSON.
`DisallowUnknownFields`, genaue Feldmengen und Prüfung auf doppelte Schlüssel,
Nulls und angehängte Dokumente gelten auf beiden Seiten. Das neue Golden liegt
unter `internal/pipe/testdata/messages.json`; bestehende Vertrags-Goldens bleiben
bytegleich. Zulässige Nachrichten:

| Richtung | Nachrichten |
|---|---|
| Client → Läufer | `status`, `pause{minuten:15\|60\|0}`, `weiter`, `anhalten`, `fortsetzen`, `setup_start{pin,setup}`, `antwort{id,wahl}` |
| Läufer → Client | `zustand{zustand,bis?,test,netz,beschaeftigt,frist_abgelaufen?,setups:[{pin,setup,label,startbar,braucht_eingabe}]}`, `frage{id,titel,text,optionen,frist_s}`, `frage_weg{id}`, `fehler{code}` |

Alle Nachrichten tragen `type`. `zustand` ist `arbeitet`, `pausiert`,
`angehalten` oder `stoerung`; `bis` ist UTC. Fehlercodes sind ausschließlich
`protokoll`, `abgewiesen`, `stoerung`. Niemals Credential, Token-, Eingabe-,
Pfad- oder Modusfelder: der Status wird ausdrücklich aus erlaubten Feldern
zusammengesetzt. Passt die vollständige Setup-Liste nicht in 4 KiB, folgt
`fehler{code:stoerung}`; sie wird nicht still abgeschnitten. `netz` beschreibt
die letzte Sync-/Heartbeat-Beobachtung, keine garantierte künftige Erreichbarkeit.

**L3-2:** Der Läufer prüft rohe Rahmen selbst in Go. `setup_start` wird vor der
Kernanfrage abgewiesen bei Halt/Pause, Belegung/Reservierung, fehlender Online-
Beobachtung, unpassendem Konto/Sitzung, unbekanntem oder unverifiziertem Setup
und Setups mit `frei`. Diese sind nicht startbar. Das Pflicht-Bool
`braucht_eingabe` stammt ausschließlich aus `len(frei)>0` im verifizierten Manifest;
L4 zeigt genau bei true „braucht Eingabe“, sonst keinen Zusatztext. Zulässige Starts verwenden unverändert den L1-Adapter mit Manifest-
Sequenz und Zufallsnonce. Eine gemeinsame Windows-Reservierung deckt HTTP,
Prozessvorbereitung und Lauf ab und schließt Zeitplan, Ad-hoc und direktes
Execute aus. Halt/Freigabewechsel während HTTP verhindert den Start; die L2-
Sperre unmittelbar vor Spawn bleibt erhalten; dort wird auch die Setup-Freigabe
erneut geprüft. Offene Claims werden beim Setup-Start abgebrochen, ihre Antworten
verworfen und neue Claims während des Setup-Laufs gesperrt. Ein gescheiterter Setup-Befehl
wird nicht offline vorgemerkt oder erneut gesendet (`9 2`); ausgeführte Läufe
benutzen das bestehende Ergebnisjournal mit `run_key=setup/…`, `ran_offline:false`.

`Engine.Questions` ist die interne lokale Weiterleitung: Text höchstens
500 Unicode-Zeichen, 2–4 Optionen zu höchstens 40 Zeichen, Frist 5–3600 Sekunden,
höchstens eine offene Frage je Lauf. `wahl` ist nullbasiert; fremde/abgelaufene
Antworten werden abgewiesen. H4: Pflichtfeld `titel`, höchstens 40 Zeichen,
aus dem Manifest-Pin, nie aus Skripttext. Vertrag 4 und 5 haben kein Pin-Label;
Rückfall ist der Pin-Name (auf 40 Zeichen begrenzt).
L4 bindet hierfür rohe `frage`-Rahmen an den Go-Laufkontext und prüft Grenzen,
skriptseitige Titel und parallele Fragen am echten Execute-Adapter. Ungültige
oder zweite offene Rahmen werden mit `abgewiesen` zurückgewiesen, Laufende
`internal`. Ohne Panel oder nach Frist ist die Skriptantwort `wahl:null`;
Abbruch/Anhalten schließt ohne Wahl. Panel-Antwort nur `antwort{id,wahl}`.
L5 ergänzt den Python-Helfer und die strikt geprüften Skript-Rahmen
`ergebnis`/`screenshot` (siehe Helfer-API oben). Diese sind keine Pipe-Befehle.
Die L1-Adapter erlauben Ergebnis/Screenshot weiterhin nur bei verifiziertem
Testauftrag (signiertes `test_device` und Testpin samt Jobbindung).

**H3:** Serverhandles sind nicht vererbbar; der Python-Prozess erhält über
`PROC_THREAD_ATTRIBUTE_HANDLE_LIST` ausschließlich stdin, stdout und NUL.
Der Pipe-Name wird nicht über Env oder Kommandozeile an das Kind weitergegeben.
Die Sonden prüfen die tatsächliche Handle-Tabelle und zusätzlich das echte
Python-Kind (kuratierte ZIP wie bei den vorhandenen Windows-Integrationstests).
Unbenannte oder nicht abfragbare Endpunkte dürfen die Namenssonde nicht abbrechen.
Die Eltern-Pipe muss sichtbar bleiben; ein absichtlich vererbtes Bedienfeld-Handle
muss den Kindtest rot machen. Die Python-Sonde muss zudem ihre eigene Kontrollpipe
finden. Ihr Laufzeitpfad bleibt kurz wie bei der vorhandenen Python-Adapterprobe.

Prüfung: `python probe/l3_mutations.py` im exklusiven Container `l3-test`
(`golang:1.26.10`). Linux prüft Schema, rohe Rahmen am Engine-/HTTP-Adapter,
Rückfragen, Kanarien-Credential und Hygiene mit genau einem Pipe-Listener.
Native Tests lokal nur kompilieren; Ausführung durch LEAD in Windows-CI:
`TestWindowsPipeDACLUndLabel`, `TestWindowsPipeLowILReadWrite`,
`TestWindowsPipeFremdesKonto` (temporäres fremdes Standardkonto, Anlage/Löschung
braucht erhöhte CI-Testrechte), `TestWindowsPipeFremdesToken` (anonymes Token),
`TestWindowsPipeClientDarfKeineInstanzAnlegen`, `TestWindowsPipeRestrictingSIDsVerweigert`,
`TestWindowsPipeRemoteAbgewiesen`,
`TestWindowsPipeBesetzt`, `TestWindowsClientPrueftEchtenServer`,
`TestWindowsServerPrueftClientbildUndReconnect`,
`TestWindowsKindOhneBedienfeldPipe`, `TestWindowsPythonKindOhneBedienfeldPipe`,
`TestWindowsPipeKindsondeErkenntVererbtesHandle`.
`probe/l3_mutations.py --native` führt die nativen Mutationen dort aus.
H1: `native-create-instance` und `native-create-instance-bit` erweitern die
Produktions-ACL. `native-create-instance-child` prüft die Mutation isoliert im
Hilfsprozess; `native-helper-token` entfernt dessen gefiltertes Prozesstoken,
`native-restricting-token` entfernt die Restricting-SID-Prüfung.
`native-helper-start` und `native-restricting-helper-start` erzwingen einen
Startfehler; beide Opt-in-Proben müssen dann rot werden.
Diese Hilfsprozess-Mutationen benötigen bei `--native` ebenfalls
`JNPT_NATIVE_TOKEN_PROBES=1`; ohne Opt-in werden sie ausdrücklich als übersprungen
gemeldet, nicht als erkannte Mutationen. Cross-Kompilierung bleibt ohne Opt-in möglich.
H3: `native-h3` und `native-h3-python` vererben das echte
Serverhandle; beide Kindtests müssen jeweils einzeln rot werden.
Fehlende Kontoanlage-Rechte, ein fehlender lokaler SMB-Transport oder eine fehlende Python-ZIP
wird als Skip ausgewiesen; die Struktur- und native Kindsonde bleiben aktiv.
Authenticode mit den beiden gemeinsam signierten Release-Exes bleibt zusätzlich
GATE-Aufgabe für L4/L6; ein unsignierter Testprozess muss bereits abgewiesen werden.

G-L3 vor dem ersten Geräte-Rollout: Auf dem Testgerät laufen die nativen
Opt-in-Proben mit `JNPT_NATIVE_TOKEN_PROBES=1` und die Bedienfeld-Prozesssonde
mit PASS ohne SKIP. Ein Windows-Build oder ein Hosted-CI-Skip ersetzt das nicht.

## Einrichten unter Windows

`desktop-runner.exe` und `jnpt-bedienfeld.exe` aus demselben Release an einen
dauerhaften Ort im Benutzerprofil in denselben Ordner legen. Die Installation
registriert beide Dateien; Verschieben erfordert erneutes Installieren. Fehlt
die reguläre Bedienfeld-Exe, scheitert `install` vor jeder Änderung mit dem festen
Fehlercode E1. Bei weiteren Fehlern stellt es die vorherigen Autostarteinträge
wieder her und meldet auch Fehler einer Rücknahme. Install/Update erhalten den
lokalen Halt-Zustand. Nach jedem Tausch der Exe-Dateien `install` erneut aufrufen,
damit beide Autostarteinträge zu den neuen Dateien passen.
Eine administrative Konsole ist nicht erforderlich.

Beide Exe sind GUI-Programme (`-H=windowsgui`, #1240): Die Benutzeraufgabe öffnet kein
Terminalfenster. `pair`, `install`, `status` und `uninstall` hängen sich an die Konsole des
Aufrufers, wenn die Ausgabe nicht umgeleitet ist. PowerShell wartet auf ein GUI-Programm nicht
und zeigt den Prompt vor dessen Ausgabe; `| Out-Host` lässt es warten.

~~~powershell
.\desktop-runner.exe pair --url https://anlage.example --code 0123456789abcdef.ABCD-EFGH-JKMN | Out-Host
.\desktop-runner.exe install | Out-Host
.\desktop-runner.exe status | Out-Host
.\desktop-runner.exe run        # Dauerbetrieb ohne Konsole; Ende: Abmelden oder Stop-Process -Name desktop-runner
.\desktop-runner.exe uninstall | Out-Host  # entfernt Autostart beider Exe, beendet das Bedienfeld
~~~

Den einmaligen, 15 Minuten gültigen Kopplungscode erzeugt die Anlage.
Format: <code>&lt;device_id&gt;.XXXX-XXXX-XXXX</code>: 16 kleine Hex-Zeichen,
Punkt und zwölf große Crockford-Base32-Zeichen (60 Bit Geheimnis).
I/L/O/U, Alias-Schreibweisen und Codes ohne Geräte-ID werden abgewiesen.
Bei privater CA zusätzlich <code>--ca C:\Vertrauen\anlage-ca.pem</code>
verwenden; alternativ <code>--fingerprint &lt;64-Hex-Zeichen&gt;</code>
für SHA-256 des exakten DER-Serverzertifikats. Die Optionen schließen sich aus.
Der Fingerprint ersetzt die CA-Kettenprüfung durch eine ausdrückliche
Zertifikatbindung; über einen vertrauenswürdigen Weg beschaffen.
Ohne diese Optionen gilt die normale TLS-Prüfung. HTTP ist nur an numerische
Loopback-Adressen erlaubt; der verbundene Peer wird zusätzlich geprüft.
Umleitungen und Umgebungsproxys sind gesperrt. Kein Abschaltschalter für TLS.

Credential und Ed25519-Key liegen DPAPI-CurrentUser-verschlüsselt mit Entropie
unter <code>%LOCALAPPDATA%\JanuaPort\desktop-runner\</code>. Der DACL erlaubt
ausschließlich dem eigenen Nutzer Zugriff; die Prüfung vergleicht binäre SIDs,
Rechte und Vererbungsflags unabhängig von SDDL-Aliasnamen. Adminrechte sind
für Kopplung und DPAPI nicht erforderlich und werden dort nicht abgewiesen.
Junctions in bestehenden Pfadkomponenten werden abgewiesen.
Ein leerer Anlagenschlüssel oder
der öffentliche RFC-8032-Testschlüssel aus den Goldens wird abgewiesen;
dabei wird nichts gespeichert und eine bestehende Kopplung bleibt unverändert.
Erneutes erfolgreiches Koppeln
ersetzt den alten Vertrauensbereich einschließlich aller lokalen Ergebnisse.
Einen laufenden Läufer vorher beenden.

Zwei COM-Benutzeraufgaben starten bei Anmeldung des eigenen Kontos:
`JanuaPort-Desktop-<SID>` den Läufer mit `run`, `JanuaPort-Bedienfeld-<SID>`
das Bedienfeld ohne Argumente, Credential oder gesetzte Umgebung. Beide nutzen
InteractiveToken, LeastPrivilege, unbegrenzte Laufzeit, drei Neustarts mit
einer Minute Abstand, IgnoreNew, ohne Akku-Bedingungen. Bei verweigerter
Registrierung durch Zugriffsschutz/Richtlinie meldet die Installation deutlich
den Rückfall auf HKCU Run. Dort fehlt die automatische Wiederaufnahme nach
Absturz. Die HKCU-Run-Werte heißen `JanuaPortDesktopLaeufer` und
`JanuaPortBedienfeld`. Deinstallation entfernt COM und HKCU Run für beide Exe,
beendet ein laufendes Bedienfeld aus diesem Ordner für den eigenen Benutzer und
lässt die DPAPI-Dateien einschließlich Halt, Kopplung und unbestätigten Ergebnissen
liegen. Einen laufenden Läufer nötigenfalls über die Aufgabenplanung beenden.
Status zeigt nur Kopplungs-/Installations-Bools; installiert verlangt beide Exe;
Fehlerausgaben enthalten feste Codes.

## Betrieb und Python-Pakete unter Windows

Pins gehören zu geraet:<device_id>. Das erwartete Konto ist ein Windows-Name
(DOMÄNE\Benutzer). Konto, aktive WTS-Sitzung (auch gesperrt, nicht getrennt),
optional Leerlauf und geprüfte Pakete werden ausschließlich lokal geprüft.
Fehlt bei passendem Konto und aktiver Sitzung nur der Leerlauf der Startregel,
lautet die Verweigerung `refused/idle`, sonst `refused/account`; beides vor
jedem Prozessstart, Lauf-Verzeichnis und Laufmarker. **Voraussetzung: ein Kern
mit #1245 K** (nimmt `idle` an); der Kern wird vor dem Läufer aktualisiert. Ein
alter Kern antwortet 400 und der Läufer verwirft das Ergebnis nach zehn
Versuchen gezählt (siehe „Ergebnisse“); er deutet `idle` nie in `account` um.
Heartbeat alle 60 Sekunden: nur contract, ready, runner_version, manifest_seq.

Ed25519, eigene Geräte-ID und steigende Sequenz sichern das Manifest.
Ablehnungen erhalten alten Stand und Gnadenfrist. Lokaler Fünf-Feld-Cron
verwendet robfig/cron/v3 und eingebettete IANA-Zeitzonendaten. Eine Minute vor
fälligen Slots werden Manifest und geänderte Pakete synchronisiert.
Slot und Laufmarker werden gemeinsam dauerhaft gespeichert. Verpasste Minuten
bilden keinen Rückstau. Ad-hoc-Aufträge kommen über jobs/claim; beide Laufarten
teilen einen Platz. Offline laufen nur werkzeuglose Pins, wenn
<code>max(Uhr, persistente Uhrmarke) - letzter erfolgreicher Sync &lt; grace_hours</code>.
Die Pinfrist hat Vorrang vor der Gerätefrist (1–72 Stunden, auch längere Fristen).
Gleichheit und fehlender Sync verweigern den Start. Die DPAPI-Datei <code>clock</code>
übersteht Neustarts; die Marke läuft während des Prozesses monoton weiter.
Ein Uhr-Rücksprung setzt die Frist nicht zurück. Eine defekte Uhrmarke sperrt.
Nach Ablauf steht der feste Logcode <code>7 1</code> im lokalen Log. Offline-Versuche
werden mit <code>ran_offline:true</code> nachgemeldet, auch bei Verweigerung.
Das Kernarchiv berücksichtigt die längste wirksame Pinfrist plus 24 Stunden
Nachmeldepuffer; das Journal verfällt nicht.
Ungültige Signaturen zählen nicht als Netzausfall. Verweigerungen werden journalisiert.

Der Pin enthält jnpt-desktop.yaml, Python-Dateien, eine exakt erlaubte
embeddable-ZIP aus [python-allowlist.json](contract/python-allowlist.json)
und gegebenenfalls requirements.lock mit wheels/*.whl. Der Client lädt
Python/Wheels ausschließlich als Bestandteil des Gitpakets von der Anlage,
ohne Paketinstaller. Git-IDs einschließlich SHA-1DC, exakte Objektmenge,
Windows-Pfade, Wheel-SHA-256 und Runtime-Allowlist werden geprüft.

Umgebungen: <code>envs\&lt;commit&gt;-&lt;SHA256-des-Pfads&gt;\</code>.
Die ._pth aktiviert site nicht; .pth-Hooks laufen nicht automatisch.
<code>python.exe -I -B bootstrap.py</code> wird suspendiert erzeugt,
dem Job Object zugeordnet und erst danach fortgesetzt. Wandzeit, Abbruch
und Job-Close beenden den Prozessbaum. Vor jedem Start werden Paket und
Umgebungsdateien nachgerechnet; die Vergleichsliste liegt DPAPI-geschützt
außerhalb der Umgebung. Zusätzliche/geänderte Dateien verweigern den Lauf.

**Skripte bekommen ein privates TEMP.** Jeder Lauf erhält ein frisches, leeres
Verzeichnis <code>%LOCALAPPDATA%\JanuaPort\desktop-runner\tmp\&lt;Lauf-ID&gt;\</code>
(Lauf-ID: SHA-256 des Lauf-Schlüssels). Es ist <code>TEMP</code>, <code>TMP</code>
und Arbeitsverzeichnis des Skripts, trägt dieselbe private DACL wie der
Läufer-Ordner und wird nach dem Lauf **gelöscht**, nachdem der Prozessbaum beendet
ist; Reste eines Absturzes räumt der Läufer beim Start ab. Nichts überlebt einen
Lauf, es gibt keine Größenbremse (Wandzeit, eigene Platte). Wer Daten behalten
will, übergibt sie als Ergebnis an Werkzeuge der Anlage. Aufräumen und Start-Besen
steigen nie in Junctions oder Symlinks ein, auch nicht in solche, die das Skript
selbst angelegt hat. Schreiben ins Pin-Verzeichnis oder die Umgebung bleibt
verboten: die Prüfung vor jedem Start verweigert danach mit `refused/hash`.
`LOCALAPPDATA`, `USERPROFILE` und `APPDATA` fehlen in der Kind-Umgebung; wer sie
braucht, nimmt TEMP.

januaport.input und januaport.call stehen zur Verfügung. MCP läuft über
private vererbte Pipes und den Läufer. Lauf-Token bleiben außerhalb von
Kind-Umgebung und Argumenten. stdout/stderr werden verworfen. januaport.log
ist in dieser Fassung wirkungslos; das Drahtfeld log ist immer leer.
Das ist eine Einschränkung gegenüber dem erlaubten begrenzten Laufprotokoll.

Zusätzliche Ressourcendeckel: 30.000 Dateien, 300 MiB entpackte Umgebung,
128 MiB je Datei, 40.000 ZIP-Einträge, höchstens 64:1 oberhalb 1 MiB und
30.000 Baum-Besuche. ZIP64 und mehrteilige ZIPs werden abgewiesen.
Sehr lange Benutzerprofilpfade können Windows-Ladegrenzen erreichen;
ein fehlgeschlagener Prozessstart wird als internal gemeldet.

## Ergebnisse, Neustart und C4

Alle Ergebnisse gehen zuerst ins lokale Journal (Windows: DPAPI; Linux:
HMAC-SHA256 und Dateirechte) und ausschließlich über
runs/result zurück; jobs/{id}/result ist kein Alias. Erst 204 entfernt den
Eintrag, Transportfehler und 409 behalten ihn. Ausnahme: Weist der Kern das
älteste Ergebnis zehnmal in Folge mit HTTP 400 ab (es wird auch beim elften Mal
abgewiesen, etwa weil ein alter Kern `refused/idle` nicht kennt), verwirft der
Läufer genau dieses Ergebnis. Der Verlustzähler steigt, das Log erhält die feste
Zeile `5 <Zähler>`, nachfolgende Ergebnisse werden zugestellt. Vor dieser Grenze
blockierte ein solches Ergebnis den Rückstau dauerhaft. Der Versuchszähler lebt
nur im Speicher. Jeder andere Fehler (409, 429, 503, Netz) setzt ihn zurück und
verwirft nie. Der veröffentlichte Ciphertext
einschließlich Marker, Slot-Merkern und Verlustzähler bleibt bei höchstens
200 Ergebnissen/16 MiB. Verdrängung erhöht den dauerhaften Zähler.
Atomare Dateierneuerung benötigt kurzzeitig zusätzlichen Platz für die
geschützte Ersatzdatei. Ein beim Neustart offener Lauf wird interrupted
und nicht erneut gestartet.

401 revoked stoppt laufende Prozesse und löscht Credential, Pakete, Umgebungen,
Manifest und Journal; die Löschung folgt keiner Junction und keinem Symlink, auch
nicht in einem Lauf-Verzeichnis. Offline wird Widerruf erst beim Kontakt bekannt; bis
dahin gilt die Gnadenfrist. Schlüsselrotation in v1 braucht neue Kopplung.

**Workstation-Skripte handeln mit den Rechten der angemeldeten Person.**
Das Job Object ist keine Sicherheits- oder Netzwerksandbox. Der Client selbst
kommuniziert nur mit seiner Anlage; Skriptcode hat weiterhin die OS- und
Netzwerkrechte dieser Person. DPAPI CurrentUser schützt nicht vor Zugriff auf
dieselbe Benutzersitzung, auch nicht vor Veränderungen während eines Skripts.
Dienstlogs enthalten nur feste numerische Codes/Zähler, fünf Dateien zu je
1 MiB; keine Eingaben, Skriptlogs, Token, Konten oder Leerlaufzeiten. Codes:
1 gestartet, 2 Sync fehlgeschlagen, 3 verweigert, 4 abgeschlossen, 5 Ergebnis
verworfen (Zähler), 6 Rest eines Lauf-Verzeichnisses blieb liegen,
7 Offline-Start verweigert (K8: Frist oder Werkzeugbindung),
8 lokale Halt-Störung (Datei unlesbar/ungültig oder Speicherung fehlgeschlagen).

**Unter Windows ist ein Gerät eine Vertrauensdomäne (SEC D4 A1).** Ein Skript läuft als
derselbe Nutzer wie der Läufer. Es kann deshalb den DPAPI-geschützten
Geräte-Token öffnen und über einen eigenen Prozess `runs/start` für einen
fälligen Slot eines anderen Pins auf demselben Gerät aufrufen. Alle Pins eines
Geräts teilen damit ihr Vertrauen und ihre Werkzeugrechte. Wer getrennte
Werkzeugrechte braucht, nimmt getrennte Geräte bzw. getrennte Konten.

## Prüfung und Release

~~~powershell
$env:CGO_ENABLED='0'
$env:GOOS='windows'
$env:GOARCH='amd64'
go build -trimpath -ldflags=-H=windowsgui -o desktop-runner.exe .
go build -trimpath -ldflags=-H=windowsgui -o jnpt-bedienfeld.exe ./cmd/bedienfeld
go vet ./...
go test ./...
Push-Location contract
go test ./...
Pop-Location
~~~

Native Taskprobe ausdrücklich mit
<code>go test ./internal/platform -run TestWindowsAufgabeOhneAdmin</code>
aktivieren, ohne erhöhte Rechte; sie erstellt und entfernt isolierte Testaufgaben
und Run-Werte für beide Exe, ohne sie auszuführen.
Die Python-Startproben (auch TEMP je Lauf samt Junction im Kind) erwarten die
unveränderte Allowlist-ZIP in .probe/.
Windows-CI beschafft und prüft sie. probe/mutations.py entfernt Sperren
einzeln, verlangt echte rote Tests und stellt Quellen byte-genau wieder her;
nur bei exklusivem Zugriff ausführen. task-standard-user.ps1 erzeugt nur auf
GitHub-Runnern ein temporäres Standardkonto.

Tag desktop-runner-vX.Y.Z: beide Exe ohne CGO und mit `-H=windowsgui` bauen, dann Pflicht-Signatur beider Dateien über Azure
OIDC/Microsoft Artifact Signing im Environment release-signing (nur für Tags
desktop-runner-v* freigegeben), danach je Exe Existenzprüfung und
`signtool verify /pa`. SHA256SUMS und Provenance binden beide finalen signierten
Dateien. Fehlt eine Exe oder SIGNING_PROFILE, ist eine Signatur ungültig oder scheitert ein Schritt,
bricht das Release ab; ein unsigniertes Release gibt es nicht.

L6: `python probe/l6_mutations.py` im exklusiven Container `l6-test` mit Go 1.26.10.
Elf kompilierende Mutanten für Installation, Rücknahme, Argumente und Release;
Containerquellen werden wiederhergestellt. Lokal kein Installtest/GUI-Start,
nur Windows-Cross-Build und PowerShell-Syntax. Windows-CI startet der LEAD.

Native Windows-Mutationen zusätzlich: probe/mutations-windows.ps1 (ohne Admin).

## Installationstest

Der manuelle Workflow `.github/workflows/desktop-runner-installtest.yml` prüft die
Client-Installation ohne PO und ohne PO-Gerät auf einem frischen `windows-latest`.
`workflow_dispatch` nimmt `version` ohne `v` entgegen, Standard `0.1.0`.
Die Probe ist Stufe 2 „Gemeinsam bauen“, nicht release-blockierend; kein Zeitplan,
Mehrgeräte- oder Lasttest. Die Logik liegt in `probe/installtest.ps1`.

GATE richtet vor dem Lauf das Environment `showcase-installtest` ein: ausschließlich
Branch `main` freigeben und einen erforderlichen Reviewer setzen (kein Selbstreview).
Diese GitHub-Einstellungen sind Voraussetzung und werden nicht durch die YAML-Datei
angelegt. Der Auftraggeber übernimmt Push und Dispatch von `main` nach Review.
GATE legt auf dem Showcase ein frisches Windows-Gerät und einen Test-Pin mit
synthetischem `print`, ohne Werkzeuge, Zugangsdaten oder Kundendaten an. Geräteart
`workstation`/`dedicated`, Startpolicy `always`; ein Ad-hoc-Auftrag muss zusätzlich
für das Gerät bereitstehen. Pin allein bedeutet noch keine Zustellung.
Unmittelbar vor dem Lauf erzeugt GATE den einmaligen, kurz gültigen Kopplungscode
per Showcase-CLI und setzt ihn als Environment-Secret `SHOWCASE_PAIR_CODE`.
Danach löscht GATE das Secret auch bei fehlgeschlagenem/abgebrochenem Lauf.

Ab 0.4.0 prüft die Probe beide Exe zusammen; ältere Releases mit nur einer Exe
werden abgewiesen. Im unveränderten manuellen Workflow die Version ausdrücklich
auf das neue Release setzen (der historische Standard bleibt 0.1.0).
Die Probe lädt beide Exe, `SHA256SUMS` und `provenance.jsonl` aus dem öffentlichen Release
in `JanuaPort/januaport-plugins`. Vor jeder Ausführung verlangt sie SHA-256,
Authenticode-Status `Valid`, den Herausgeber `JanuaPort GmbH` und
`gh attestation verify` mit Repository, Release-Workflow und Tag als Herkunft.
GitHub-Tokenrechte: nur `contents: read` und `attestations: read`.
Anschließend erstellt sie wie die Taskprobe ein temporäres Standardkonto,
lädt dessen Profil und legt beide geprüften Exe dauerhaft unter
`%LOCALAPPDATA%\JanuaPort\installtest\<version>\` ab.
Koppeln und Installieren laufen ohne Adminrechte; `status` muss
`gekoppelt=true installiert=true` ausgeben. Beide Installationswege zählen:
COM-Benutzeraufgabe und der vom Läufer gemeldete Richtlinien-Rückfall auf HKCU Run
werden für jede Exe geprüft; die Bedienfeld-Aufgabe und ihr Run-Wert müssen ohne
Argumente auf die richtige Datei zeigen.

Der Code liegt im Skript als `SecureString` vor und wird dem Standardkonto über
eine Named Pipe übergeben, deren DACL nur dieses Konto und den CI-Hauptnutzer
zulässt. Er wird nicht als Klartextdatei abgelegt und nicht ausgegeben;
Prozessausgaben werden auch im Fehlerfall abgefangen, Fehler bleiben fest.
**Grenze von Windows v0.1.0:** `pair` unterstützt ausschließlich `--code`, kein stdin.
Der Code ist während dieses kurzen Prozessstarts in dessen Argumentliste sichtbar.
Die stdin-Unterstützung im Linux-Server gilt nicht für die Windows-EXE.

`install` startet den Läufer erst bei der nächsten Anmeldung. Deshalb startet die
Probe zusätzlich `run` im selben Standardkonto und beobachtet ihn fünf Minuten.
Sie prüft Prozess und `status` und liest ausschließlich das lokale DPAPI-geschützte
Manifest, um einen erfolgreichen Sync als Lebenszeichen des Anlagenkontakts zu
erkennen. Ohne Sync wird der Lauf rot. Das ist kein Nachweis eines bestätigten
Heartbeats und kein `ready`-Nachweis. Der Beobachtungszeitraum bleibt für GATEs
serverseitige Prüfungen offen.

**Auftragsabschluss lokal nicht zuverlässig beobachtbar:** `status` zeigt nur die
beiden Bools. Das DPAPI-Journal enthält unbestätigte Ergebnisse; nach HTTP 204 wird
der Eintrag sofort gelöscht. Ein Polling könnte das `ok` verpassen. Die Probe liest
deshalb keine Auftragsinhalte und behauptet keinen lokal belegten Auftragsabschluss.
GATE belegt auf dem Showcase Lebenszeichen, Auftrag `ok`, Audit ohne Inhalt sowie
den Admin-Abbruch eines zweiten, absichtlich langen Pins. Dessen tatsächliche
Endklasse dokumentiert GATE: der aktuelle Heartbeat-Abbruchpfad liefert `cancelled`;
`interrupted` bezeichnet im Windows-Code einen beim Neustart offenen Laufmarker.

**Weitere Voraussetzung für die Auftragsbelege:** Der Läufer verlangt eine aktive
WTS-Sitzung und ein exakt passendes `expected_account` (`DOMÄNE\Benutzer`). Das
temporäre Konto heißt zufällig `JnptIT…`; die Hosted-CI garantiert keine aktive
Benutzersitzung dafür. Diese Probe umgeht weder Konto- noch Sitzungsprüfung.
Auftrags- und Abbruchbelege sind deshalb auf Hosted-CI nicht garantiert und müssen
gegebenenfalls auf einem dafür eingerichteten Test-PC erfolgen. Die CLI sendet
keinen Kontonamen; GATE muss die Konto-/Sitzungsvoraussetzungen separat herstellen.

In `finally` laufen `uninstall`, das Beenden des explizit gestarteten Prozesses und
die Abfrage der Aufgabenplanung über COM sowie von HKCU Run. Jede verbleibende
eigene Autostartregistrierung macht den Test rot. Ein zweiter Cleanup im Standardkonto
deckt auch einen vom Skript zeitlich abgebrochenen Worker ab. Harter Runnerverlust
kann `finally` verhindern; GATE räumt dann das Showcase-Gerät trotzdem auf.
GATE entfernt nach dem Lauf das Gerät und belegt, dass es verschwunden ist und
seine Credentials ungültig sind. `uninstall` allein widerruft keine Credentials.

Manuell auf einem frischen Windows/amd64-Testprofil ohne Administrator-Mitgliedschaft
mit PowerShell 7 und aktueller GitHub CLI (`gh attestation verify` einschließlich
`--source-ref` verfügbar):

~~~powershell
./probe/installtest.ps1 -Version 0.4.0 -Code (Read-Host 'Showcase-Code' -AsSecureString)
~~~

Ein bereits benutztes Läuferprofil oder vorhandener Autostart wird abgewiesen,
damit eine bestehende Kopplung erhalten bleibt. EXE und lokaler Zustand bleiben
nach Deinstallation im Testprofil; Showcase-Widerruf und Profilentsorgung sind
Betreiberaufgaben. `-CreateStandardUser` ist ausschließlich für GitHub-CI gedacht.

Nicht belegt: SmartScreen/Zertifikatreputation auf einem echten Arbeitsplatz-PC,
Firewall, Proxy und Virenschutz beim Pilot sowie der Linux-Server-Läufer (#1195).
Offline-Verifikation ohne Release-/Showcase-Zugriff:
`./probe/installtest-tests.ps1`; Mutationen mit echten Windows-Adaptern:
`./probe/installtest-mutations.ps1`. Mutanten laufen als isolierte Skriptkopien;
der Produktionsquelltext bleibt bytegleich. Der negative Admin-Kontotest samt Mutation
braucht ein Admin-/UAC-Konto und läuft im Windows-CI; lokale Standardkonten melden einen Skip.

GATE prüft die Provenance-Abweisung separat am echten `gh`-Adapter: Release-EXE und
`provenance.jsonl` in einem Verzeichnis bereitstellen, dann mit GitHub-CLI-Zugang
`./probe/installtest-mutations.ps1 -ReleaseDirectory C:\Probe\release -ReleaseVersion 0.1.0`
aufrufen. Erst muss die unveränderte EXE die Herkunftsprüfung bestehen; anschließend
muss eine Kopie mit angehängtem Byte abgewiesen werden. Der entsprechende Mutant
überspringt die Herkunftsprüfung und muss rot werden. Diese optionale Probe führt
`gh`, aber keine Release-EXE aus; ohne `-ReleaseDirectory` bleibt sie sichtbar übersprungen.

## Linux-Server-Modus

Nur Geräteart `server`, Startpolicy `always`, Vertrag 2. Der Läufer holt Manifest,
Aufträge und Abbrüche selbst ab; er öffnet keinen Port. Ein gemeinsamer Laufplatz
für Ad-hoc und Zeitpläne, pro Lauf ein eigener Container. Python, WTS und DPAPI
werden auf diesem Weg nicht verwendet. `declared_tools` bleibt in K1 leer.

### Installation mit rootless Docker oder Podman

Standard ist ein eigener Systemnutzer `jnpt-runner` mit einem dedizierten
rootless Docker-Daemon (Docker 20.10/API 1.41 oder neuer). Der Nutzer darf nicht
root und nicht die feste Job-UID 65532 sein. Docker rootless gemäß dessen
Installationsanleitung mit Sub-UID/Sub-GID-Bereichen und dauerhaftem Userdienst
einrichten. Kein gemeinsam genutzter Daemon: Beim Neustart entfernt der Läufer
alle Container mit Label `ai.januaport.run`.

1. `CGO_ENABLED=0 go build -trimpath -o jnpt-runner .`, Binärdatei unter
   `/usr/local/bin/jnpt-runner` installieren (Release-Pakete/Signatur folgen separat).
2. [Konfigurationsvorlage](systemd/runner.example.json) nach
   `/etc/jnpt-runner/runner.json` kopieren. `mode` muss `container` sein;
   `docker_path`, `state_dir` und ein gesetztes `secrets_dir` sind absolute Pfade.
   Zustand und Geheimnisverzeichnis dürfen sich nicht überlappen.
3. `/var/lib/jnpt-runner` und `/var/lib/jnpt-runner/state` mit Eigentümer
   `jnpt-runner`, Modus 0700 anlegen. Credential, Integritätsschlüssel, Manifest,
   Journal und Sperrdatei erhalten 0600. Fremde Rechte oder Symlinks werden
   abgewiesen. Der Sperr-Inode liegt außerhalb des bei Widerruf gelöschten `state`.
4. [systemd-Unit](systemd/jnpt-runner.service) installieren. Ein Drop-in setzt
   `Environment=DOCKER_HOST=unix:///run/user/<uid>/docker.sock` für den tatsächlichen
   Nutzer. Die Unit läuft ohne Fähigkeiten, mit `NoNewPrivileges`, read-only
   System und Schreibrecht ausschließlich im Zustandsverzeichnis. Keine
   privilegierte Läufer-Installation; rootful erfordert allenfalls Socket-Gruppe.
5. Als `jnpt-runner` vor Dienststart koppeln:
   `jnpt-runner pair --config /etc/jnpt-runner/runner.json --url https://anlage.example`.
   Den Code über stdin eingeben, danach EOF; dieselbe Docker-Umgebung wie im Dienst
   setzen. `--ca` oder `--fingerprint` funktionieren wie unter Windows.
   Dann `systemctl enable --now jnpt-runner` und
   `jnpt-runner status --config /etc/jnpt-runner/runner.json` prüfen.

Podman ab Version 4 kann als Docker-kompatibler CLI-Pfad konfiguriert werden.
Für die gehärtete Unit einen separat gestarteten rootless Podman-API-Userdienst
und `podman-remote` verwenden; `CONTAINER_HOST` zeigt auf dessen lokalen Socket.
So benötigt der Läufer keine privilegierten Usernamespace-Helfer. Podman benötigt
für `--init` auch seinen Init-Helfer. Die Rootless-Erkennung prüft Docker
`SecurityOptions` bzw. Podman `host.security.rootless`, nicht den CLI-Namen.
Kopplung und Heartbeat melden `rootless`/`rootful` ehrlich. Rootful bleibt erlaubt:
Wer den Läufer samt rootful Socket übernimmt, kann den Host übernehmen.
Die lokalen Docker-Belege verwenden rootful Docker; Podman/rootless-Erkennung
ist über synthetische Runtime-Antworten geprüft, kein Live-Podman-Beleg.

### Image, Eingabe und Lauf-Token

Nur `repo@sha256:<64 kleine Hex-Zeichen>`, ohne Tag. Vor jedem Lauf Pull per
Digest und Vergleich von `.RepoDigests` mit dem freigegebenen Digest; bei
Abweichung `refused/hash`, ohne Jobstart. Der eigentliche Start verwendet
`--pull never`, Nicht-root UID/GID 65532, read-only Wurzel, `cap-drop ALL`,
`no-new-privileges`, 128 Prozesse, Pin-CPU/RAM/Wandzeit, `--init` und `--rm`.
`--memory-swap` ist gleich `--memory`: Das gemeinsame RAM-plus-Swap-Limit
entspricht dem RAM-Limit; zusätzlicher Swap ist damit ausgeschlossen.
Die CPU-/RAM-/Wandzeit-Obergrenzen entsprechen K1: 16000 mCPU, 32 GiB, 86400 s.
`/tmp` (64 MiB) und `/run/jnpt` (1 MiB) sind `noexec,nosuid,nodev`-tmpfs.
Der Containername ist `jnpt-run-` plus den ersten 32 Hex-Zeichen von SHA256 des
Laufschlüssels; das Label enthält den ursprünglichen Schlüssel.

**Image-Voraussetzung:** Das Job-Image muss `/bin/sh` und `tar` enthalten.
Der Läufer setzt ausdrücklich `--entrypoint /bin/sh` und überschreibt damit
auch einen eigenen Image-ENTRYPOINT. Der feste Startcode im Job entpackt einen
vom Läufer erzeugten Tar-Stream aus stdin und
führt anschließend den signierten Befehl mit `exec "$@"` aus. Pin-Argumente
werden nicht in Shelltext eingesetzt. Der Host startet ausschließlich die CLI
per Argumentliste. Der Pin kann den festen ENTRYPOINT nicht überschreiben.
Diese Image-Konvention erweitert das K1-Pin-JSON nicht.

Der Stream enthält `/run/jnpt/input.json`, `/run/jnpt/token` und zuletzt `ready`,
jeweils UID/GID 65532, Modus 0400. Vor dem signierten Befehl ist alles verfügbar.
Der Lauf-Token steht weder in Env, Argumenten noch Labels und bleibt im Job-tmpfs.
Der gewählte exec-freie Weg funktioniert auch bei rootless UID-Abbildung.
`docker cp` ist hierfür ungeeignet: Docker verweigert das Schreiben bei read-only
Wurzel mit `--tmpfs`; alternative Mount-Formen liefern keinen verlässlichen
Zugriff auf das laufende tmpfs. Es gibt keinen Bind-Mount des Läufer-Verzeichnisses,
Gerätetokens oder Docker-Sockets. Die echte Jobprobe belegt fehlenden Zugriff
auf den Gerätetoken (im Job fehlt der Hostpfad) und beide üblichen Socketpfade.

K1-Zeitpläne sind werkzeuglos und erhalten keinen Lauf-Token: K1 verweigert dann
`runs/start`. Der Client startet aus dem signierten Manifest, auch offline nur
innerhalb der bisherigen Gnadenfrist. Die Token-Datei ist dabei leer. Ad-hoc
verwendet Claim → Start. Ausgabe wird verworfen, Docker-Logging ist abgeschaltet.
Registry-Zugriff für den Digest-Pull ist auch bei einem Offline-Lauf erforderlich.

### Betreiber-Geheimnisse und Netz

Geheimnisse sind kernseitig ab Werk aus. Das Betreiber-Verzeichnis, beispielsweise
`/etc/jnpt-runner/secrets`, gehört `jnpt-runner` mit Modus 0700. Eine reguläre Datei
je Name; keine Symlinks, auch nicht im Verzeichnispfad. Die Datei muss für die
Container-UID 65532 lesbar sein: etwa Modus 0444 innerhalb des 0700-
Verzeichnisses oder passend zugeordnete UID. Werte werden nicht eingelesen oder
als Env übertragen. Pro Name genau ein read-only Bind nach `/run/secrets/<name>`.
Ein deklarierter und erlaubter, aber fehlender Eintrag ergibt `refused/secret`.

Bei rootful Docker ohne zusätzliche Usernamespace-Abbildung entspricht
Container-UID/GID 65532 der Host-UID/GID 65532. Rootless Docker und Podman bilden
die IDs hingegen über die Bereiche aus `/etc/subuid` und `/etc/subgid` ab.
Synthetisches Beispiel für beide Runtimes mit Standard-Abbildung:

```text
/etc/subuid:  jnpt-runner:100000:65536
/etc/subgid:  jnpt-runner:100000:65536

uid_map:     Container-ID  Host-ID  Anzahl
                       0     1000       1
                       1   100000   65536
gid_map:               0     1000       1
                       1   100000   65536
```

Bei dieser Abbildung gilt `Host-ID = 100000 + (Container-ID - 1)`:
Container-UID/GID **65532 entspricht Host-UID/GID 165531**. UID 0 im Namespace
entspricht hier dem Läufer-Nutzer mit Host-UID/GID 1000. Die Bereiche und
Abbildungen sind installationsabhängig; bei abweichenden Sub-GIDs die GID
getrennt berechnen. Maßgeblich ist die tatsächliche Abbildung des Jobs auf dem
Linux-Host des Daemons. Für einen laufenden Job prüfen:

```sh
job_name=jnpt-run-... # tatsächlichen Containernamen einsetzen
job_pid=$(docker inspect --format '{{.State.Pid}}' "$job_name")
sudo cat "/proc/$job_pid/uid_map" "/proc/$job_pid/gid_map"
```

Bei Podman denselben Aufruf mit `podman inspect` verwenden. Auf diesem Host
lassen sich die Rechte für die obige Beispiel-Abbildung so setzen:

```sh
sudo install -d -m 0700 -o jnpt-runner -g jnpt-runner /etc/jnpt-runner/secrets
sudo install -m 0400 -o 165531 -g 165531 ./synthetic-secret /etc/jnpt-runner/secrets/alpha
```

Das Verzeichnis bleibt Eigentum des Läufer-Nutzers; nur die Geheimnis-Datei
gehört der abgebildeten Job-UID. Bei lokalem rootless Podman kann alternativ
als `jnpt-runner` außerhalb der gehärteten Unit `podman unshare chown 65532:65532
/etc/jnpt-runner/secrets/alpha` und anschließend `podman unshare chmod 0400
/etc/jnpt-runner/secrets/alpha` verwendet werden. Das gilt für die gezeigte
Standard-Abbildung; bei weiteren Usernamespaces wieder die Job-Abbildung prüfen.

Ad-hoc bindet ausschließlich `allowed_secret_names` ∩ signierte `secret_names`.
Fehlende/leere Claim-Liste bindet nichts; lokale Konfiguration erweitert keine
Freigabe. Zeitpläne verwenden die signierte Deklaration, auch im historischen
Offline-Fenster. K1-Namen: `[a-z0-9][a-z0-9_.-]{0,62}`, kein `..`, eindeutig,
höchstens 16.
Ergebnisse senden `mounted_secret_names`, ausdrücklich auch `[]`. Nur nachgewiesene Starts melden Mount-Namen. Bei einem Absturz vor der
Ergebnissicherung meldet der Marker die leere, unbekannte Teilmenge; die signierte
Deklaration bleibt im Kern-Audit erhalten. Ein fehlgeschlagener Start wird nicht
als ausgeführter Geheimnis-Mount ausgegeben.

Netz: `none` oder ein vorab vom Betreiber angelegtes benanntes Netz. `host`,
`bridge`, `default`, `private`, `pasta`, `slirp4netns`, `container:` und `ns:` sind
gesperrt. Keine DNS-/Host-/IP-Zusatzoptionen. Beispiel:
`docker network create --internal jnpt_jobs`; nur den Kern und diese Jobs mit
diesem Netz verbinden. Das begrenzt externen Ausgang, ist aber keine Ziel-Allowlist:
Andere Netzteilnehmer bleiben erreichbar. Weitere Ziele, Firewall und Routing
verantwortet der Betreiber; der Kern prüft sie nicht. `none` isoliert das Netz.

Abbruch, Wandzeit und normales Ende versuchen immer `kill` und `rm --force`.
Neustart räumt zuerst gelabelte Container ab, dann journalisiert er offene Läufe
als `interrupted`. Bei fehlgeschlagener Bereinigung startet der Dienst nicht.
Keine Wiederholung alter Läufe. Ein fehlgeschlagener Cleanup sperrt weitere
Containerstarts bis zum Neustart. Das Linux-Journal behält dieselben Deckel:
200 Ergebnisse/16 MiB einschließlich HMAC, Markern und Zähler; HMAC prüft Integrität,
Dateirechte schützen Vertraulichkeit. Der Integritätsschlüssel liegt separat 0600.

### Server-Prüfung und K1-Abstimmung

`go vet ./...`, `go test ./...` und unter Linux `go test -race -count=1 ./...`.
`JNPT_DOCKER_REQUIRED=1` macht fehlendes Docker zum Fehler; im Linux-CI-Job ist
das gesetzt. Ohne Docker überspringen die echten Proben sichtbar. Docker-Tests
prüfen Token/Inspect, Gerätetoken-/Socket-Isolation, read-only Geheimnisse sowie
Abbruch, Wandzeit und Wiederanlauf. Synthetisches Testimage: Alpine, nach Pull
stets per Digest; optional `JNPT_TEST_IMAGE=repo@sha256:…` vorgeben.
`python probe/container_mutations.py` prüft die neuen Sperren mit exakter
Wiederherstellung, optional `--container <lokaler Linux-Prüfcontainer>`.

**Bekannte K1-Lücke:** Auf `origin/feat/1195-server-container` verweigert
`cmd/jnpt/desktop_routes.go:desktopResult.valid` noch `refused/secret` und
`limit/wall`. Paket A sendet die beauftragten Klassen unverändert. Bis der Kern
sie akzeptiert, wiederholt der Läufer diese Ergebnisse nach HTTP 400 zehnmal und
verwirft sie dann gezählt (siehe „Ergebnisse“), damit der Rückstau nicht blockiert.
Keine stille Umdeutung in `hash` oder `timeout`; der Kern benötigt vor Live-Abnahme
den Nachzug.
