# Draht-Vertrag, Fassung 6 — ETL-Strom und Rückfall

Fassung 6 (JanuaPort/januaport#1215 P2) ergänzt den ETL-Rohstrom und die
Rückfallregel, siehe [ETL-Rohstrom und Rückfall](#etl-rohstrom-und-rückfall-fassung-6-januaportjanuaport1215-p2).
Der Rest dieses Textes ist Fassung 5 (Laufgrenzen, JanuaPort/januaport#1040 P3 und
Schritt L, 0.3.0, Plan 6176555886, SEC 6176617758 L1–L8) und gilt unverändert.
Die Vertragskennung im Draht ist **2 oder 3** (`internal/contract.LegacyVersion`,
`internal/contract.Version`), unabhängig von der Nummer dieser Textfassung: 3 nur,
wenn der Kern sie zulässt, sonst 2. Bestehende Felder und Ende-Klassen gelten weiter;
Referenzdaten stehen unter `golden/`.

## Anmeldung und Kompatibilität

`tools/list` meldet `_meta["januaport.ai/runner"].contract = 2` (Rückfall) oder 3
(Kern ab 0.100.0, siehe Rückfallregel unten), auch ohne Pins
oder bei ungültiger Isolation und auch mit Zeitplänen. Die bisherigen drei
`tools_list*`-Goldens und das neue `tools_list_wall.json` tragen Kennung 2, `tools_list_v3.json` die 3. Das Dateiformat von Zeitplänen bleibt
unverändert bei Version 1; dies ist eine eigene Kennung.

**Benannte Kompatibilitätsgrenze:** Ein Läufer mit `contract: 2` braucht ein
Gateway ab **0.77.0**. Ein älteres Gateway sperrt den Läufer ganz
(„andere Vertragsfassung“). **Update-Reihenfolge: erst das Gateway, dann der Läufer.**
Ein Gateway ab 0.77.0 akzeptiert die Läuferkennungen 1 und 2, ein Kern ab 0.100.0
auch 3. Dieser Läufer meldet 2, bis er an `/healthz` einen Kern ab 0.100.0 liest;
erst dann meldet er 3 (Fassung 6), damit ihn ein Kern, der die 3 nicht kennt, nicht sperrt.

## Kopfzeile auf Weg A

Weg A verwendet `Authorization: Bearer <runtime-key>` auf dem internen
Listener `:8090`: `POST /mcp` und zusätzlich `GET /desktop/package` (D2b).
Der folgende Laufgrenzen-Header gilt für lokale Skript-Läufe über MCP.

Zusätzlich zu Lauf-Token, Lauf-ID und Pin-Commit sendet das Gateway bei
`tools/call` für Kennung 2:

```text
X-Jnpt-Run-Limits: wall_s=<s>;output_bytes=<n>;tool_calls=<n>
```

Genau drei Paare in dieser Reihenfolge, getrennt durch `;`, ohne Leerzeichen.
Zahlen bestehen aus ASCII-Ziffern und sind numerisch positiv. Andere Namen,
Reihenfolge oder Trennzeichen, fehlende/doppelte Paare, Leerzeichen, Null,
negative Werte, Nichtzahlen, führendes `+`, Überlauf bei uint64 und mehrere
Headerwerte sind Formfehler. Ein vorhandener leerer Header ist ein Formfehler.

`contract.ParseRunHeaders` prüft das vor dem Lauf und vor jeder Slot-Belegung.
Ein Formfehler folgt demselben JSON-RPC-Fehlerweg wie ein fehlendes Lauf-Token;
es gibt keinen fachlichen Lauf und keinen Rückfall auf Altgrenzen. Der feste
Fehler lautet `contract: Lauf-Grenzen sind falsch geformt`; der Wert wird weder
im Fehler noch im Dienstlog ausgegeben. Ein nachfolgender gültiger Lauf kann
die noch frische Sandbox sofort benutzen.

Fehlt die Kopfzeile vollständig, gelten unverändert die drei Altgrenzen.
Die Prüfreihenfolge für fachliche Ablehnungen und die Form des Job-Rahmens
werden durch diese Kopfzeile nicht geändert.

| Größe | Ohne Header | Technische Decke | Wirksam im Läufer |
|---|---|---|---|
| Wandzeit | 25 s | 86400 s (24 h) | `min(wall_s, optionalem manifest.timeout_s, konfigurierter Decke)` |
| Ausgabe | 1 MiB | 16777216 Byte (16 MiB) | `min(output_bytes, Decke)` |
| Werkzeugaufrufe | 50 Aufrufe | 10000 | `min(tool_calls, Decke)` |

Werte über einer Decke werden **gekürzt**; nur der Zahlenüberlauf ist ein
Formfehler. Die Konvertierung in `int` erfolgt nach dem Kürzen. Die drei Decken
sind Konstanten im Läufer und durch Literal-Tests geschützt (SEC L8).

**SEC L6 / Schritt L:** Die eigene 25-s-Kappe entfällt. `timeout_s` in
`jnpt-script.yaml` ist optional, ein Vorschlag in 1..86400 ganzen Sekunden.
Mit Vorschlag gewinnt das Minimum aus Header und Manifest; fehlt er,
entscheidet der Header. Ohne Header bleiben 25 s / 1 MiB / 50 Aufrufe
(gegebenenfalls strengere Manifestzeit). Null, negative Werte, Überdecke,
`null` und andere YAML-Typen sind `manifest_invalid`; `tools/call` liefert
`refused/state` mit dem festen Satz „Dieses Skript ist nicht ausführbereit.“
Die Eingabe bleibt auf 256 KiB begrenzt. Desktop-Timeouts bleiben Pflicht.

Der vermittler meldet in **`tools/list` auf Ergebnisebene**:
`_meta["januaport.ai/runner"] = {contract: 2 oder 3, max_wall_s: 86400, isolation, pins, …}`.
`JNPT_SCRIPT_RUNNER_MAX_WALL_S` setzt die positive ganzzahlige Meldung und
die zusätzlich selbst eingehaltene Decke (1..86400; Standard 86400).
Ungültige Konfiguration verhindert den Start mit festem Fehler ohne Wertecho.
Die Env wird in der Compose an den vermittler durchgereicht.
Bei fehlendem Manifest-Timeout melden die Werkzeug-Metadaten
`_meta["januaport.ai/script"].timeout_s = 25` (`contract.DefaultWallS`).
Ohne `timeout_s` meldet der Läufer 25 s; längere Läufe wählt der Aufrufer ausdrücklich.
Dafür dient `start_script_run(wall_s=…)`; im Läufer entscheidet dann der
Grenzheader unter der konfigurierten Decke. Der Metadaten-Ersatzwert ist
keine zusätzliche Laufgrenze. Ein ausdrücklich gesetztes Manifest-Timeout
begrenzt den Lauf weiterhin, auch beim Wert 25.
Der synchrone Kern-Weg nimmt das Werkzeug-Metadatum als ausdrückliche Wahl
(`syncRunLimits`); der Ersatzwert 86400 würde deshalb vor F und bei kleineren
Anlagenobergrenzen abgewiesen. Ohne Header bleibt der Lauf bei höchstens 25 s.

Die Kennung blieb für diese Fassung **2**, weil Header und Ende-Klassen gleich bleiben und
`max_wall_s` additiv ist; eine neue Kennung sperrt der Kern **— außer der Läufer bietet sie nur an,
wenn der Kern sie zulässt (Rückfallregel der Fassung 6: ohne lesbare, ausreichende Kern-Version
bleibt es bei 2, ein alter Kern sperrt den Läufer also nicht)**. Maßgeblich sind
`cmd/jnpt/script_runtime.go` (`DiscoverWithMeta` → `parseRunnerMeta`) und
`script_contract.go` (`runnerMeta.MaxWallS`, `releasedWall`) auf origin/main.
Der Kern glaubt nur positive ganze Sekunden nach unten:
`min(scriptReleasedMaxWallS = 25 s, max_wall_s)`.
**Wirkung erst mit JanuaPort-Schritt F, bis dahin bleibt der Kern-Riegel bei 25 s.**
Eine explizite Wahl über der Kern-Freigabe wird abgewiesen. Die Entfernung
des Internet-Ausgangs (#1069) bleibt Voraussetzung im ausgelieferten Aufbau.

`golden/tools_list_wall.json` ergänzt die Meldung für 0.3.0; die bisherigen
Goldens einschließlich `call_headers_v1.json` bleiben bytegleich.

## Desktop-Pins und Paketabruf auf Weg A (#1165 D2b)

Der Kern schreibt **atomar** `desktop-pins.json` neben `pins.json` in den
Plugin-Schlüsselordner. `pins.json` bleibt unverändert bei Version 1; ein
älterer vermittler ignoriert die neue Datei. Der Umschlag lautet:

```json
{"version":1,"repo_url":"ssh://git@example.org/org/scripts.git","pins":[{"name":"desktop_demo","commit":"0123456789012345678901234567890123456789","path":"desktop/demo","target_device":"device-123","algo":"sha1"}]}
```

`name` folgt der Skript-Pin-Regel, `commit` ist kleingeschriebenes Hex
(40 Zeichen für `sha1`, 64 für `sha256`). `target_device` ist die Geräte-ID
ohne `geraet:`; der Materialisierungsleser erlaubt 1–128 ASCII-Zeichen aus
`A–Z`, `a–z`, `0–9`, `_`, `-`. Pfade sind relativ, mit portablen
Windows-Namensregeln des [Desktop-Vertrags](../../desktop-runner/contract/README.md#paketformat-applicationvndjnptgitobjectsv1)
und der bestehenden `.git`-Sperre. Höchstens 1 MiB für diese Steuerdatei.
Unbekannte Felder, falsche Version, kaputtes JSON oder Daten danach ergeben
keine Desktop-Pins; ungültige Zeilen und doppelte Namen sind `invalid`.
Alle Gründe sind feste Codes ohne Echo der fehlerhaften Werte.

Beide Listen beziehen sich auf **dasselbe Repository**. Ohne `pins.json`
genügt die Desktop-Datei; abweichende Repository-URLs sperren Desktop-Pins.
Ihre gültigen Commits werden mit den Skript-Pins in `wanted/<sha>` vereinigt,
nach atomarer Veröffentlichung von `repo_url`. Der vorhandene Abholer prüft
V2/M7 und Standard-Zweig-Abstammung L3 unverändert. Vorhandene Objekte allein
erteilen keine Freigabe. `failed/` hat Vorrang; Freigaben sind URL-gebunden.

`tools/list` ergänzt `_meta["januaport.ai/runner"].desktop_pins` je Pin um
`{name,commit,path,algo,state,reason,declared_tools,timeout_s,input_schema,description}`.
`name`, `commit`, `path` und `algo` sind **Pflichtfelder in jedem Zustand** und
stammen aus `desktop-pins.json`, nicht aus dem Repository-Manifest. Der Kern
ordnet die Werkzeugdeklaration nur zu, wenn **Name, Commit und Pfad** mit
seinem eigenen Pin übereinstimmen; eine Neuanlage unter demselben Namen ist
keine Übernahme der alten Freigabe. Ohne Desktop-Pins fehlt der Schlüssel.
Die Manifestwerte stammen ausschließlich aus dem nachgerechneten
`jnpt-desktop.yaml` des Pins mit geprüfter Zweigfreigabe; nur bei `ready`
werden sie veröffentlicht. Die sechs Basisfelder plus `python` sind
Pflicht, `timeout_s` ist 1–86400, `requirements` optional ausschließlich
`requirements.lock`. Einstieg, Python-ZIP und gegebenenfalls Lock müssen im
Pin liegen. Unbekannte YAML-Felder, weitere YAML-Dokumente und ungültige
Felder werden mit festen Gründen abgewiesen. Textfelder und Werkzeugnamen
verlangen YAML-Strings, `timeout_s` eine YAML-Ganzzahl; keine stillschweigende
Umwandlung von Booleschen Werten oder Bruchteilen. Paket-/Runtime-/Wheel-Prüfungen
auf dem Gerät bleiben wie im Desktop-Vertrag vorgeschrieben.

Desktop-Pins sind **keine MCP-Werkzeuge** und unabhängig von der lokalen
Sandbox-Isolation bereit. Auch ein gleichnamiger Eintrag in `pins.json`
öffnet keinen zweiten Ausführungsweg: er ist ungültig, `tools/call` lehnt den
Namen mit `refused/pin` ab. Es werden weder Sandbox-Jobs gesendet noch
Bibliotheken vorbereitet oder Windows-Wheels entpackt.

Ein gleichnamiger Git-Pin in `pins.json` und Speicher-Pin aus `state.json/accepted` macht **beide** zu `invalid/manifest_invalid`; `tools/call` antwortet mit `refused`, ohne Sandbox-Job. Ungeprüfte Namen aus `inbox/list.json` lösen diese Kollision nicht aus (SEC F1/H4).

```text
GET /desktop/package?commit=<hex>&path=<relativer-pin-ordner>
Authorization: Bearer <runtime-key>
```

Genau ein `commit` und ein `path`, keine weiteren Parameter. Die Route liest
die aktuelle Desktop-Datei und die Zweigfreigabe; ausschließlich ein dort
freigegebenes Paar wird geliefert. Pin und Freigabe werden nach dem Tree-Walk
erneut geprüft, das gelistete `ready` ist keine Abrufberechtigung.
Ein Pinentzug beendet im Kern alle offenen Aufträge dieses Pins mit `denied`
und bricht einen laufenden Lauf ab; der script-runner liefert weiterhin
ausschließlich Pakete für aktuelle Pins.

- **200:** `Content-Type: application/vnd.jnpt.gitobjects.v1`, binäres Paket
  wie im Desktop-Vertrag: Commit, Bäume bis zum Pin und alle Objekte darunter,
  IDs selbst nachgerechnet, jedes Objekt einmal, ohne Eltern-Commits und
  Geschwisterinhalte. Deterministische Reihenfolge: Commit, Vorfahrenbäume,
  dann Tiefensuche in Git-Baumreihenfolge. Windows-Namen, Faltungskollisionen,
  falsche Typen/Modi, Symlink, Gitlink und mehr als 256 Kanten werden geprüft.
- **404:** unbekanntes/fremdes Paar, fehlender oder widerrufener Pin,
  Manifest-/Objektfehler oder falsche Query. Immer derselbe JSON-Körper
  `{"error":{"class":"not_found","message":"Der Auftrag oder das Paket wurde nicht gefunden."}}`
  mit LF, `Content-Type: application/json`; keine internen Fehlertexte.
- **413:** mehr als **128 MiB einschließlich Kopf und aller Rahmen**.
  Größenprüfung vor Objektallokation; keine teilweise geprüfte HTTP-Ausgabe.
  JSON: `{"error":{"class":"too_large","message":"Die Anfrage überschreitet die zulässige Größe."}}`
  mit LF, `Content-Type: application/json`.
- Bearer-Prüfung wie `/mcp` (401 bei fehlendem/falschem Schlüssel).
  Paketantworten und ihre 404/413 tragen `Cache-Control: no-store`.

Der interne Abruf verwendet den runtime-key. Geräte-/Auftragsbindung des
äußeren Gerätezugsangs bleibt Aufgabe des Kerns; Weg A nimmt kein Geräte-
oder Lauf-Token entgegen.

Zeitpläne für Desktop-Pins zählen als bekannt und können `ready` melden.
Der lokale Taktgeber **feuert sie nicht**, zählt für sie keine verpassten
Läufe und meldet kein lokales Laufergebnis. Den Takt übernimmt das Gerät
aus seinem signierten Manifest; `schedules.json` behält sein Format.

Nachweise: `internal/mediator/desktop_test.go` (echte HTTP-/MCP-/Unix-/Git-
Adapter), `internal/store/desktop_test.go` (Gitobjekte, Grenzen, Namen,
Objektmenge). `TestDesktopPackageGolden` importiert die unveränderten
`desktop-runner/contract/golden/package_{sha1,sha256}.bin` in echte
Objektdatenbanken und verlangt byte-gleiche Ausgabe von `DesktopPackage`.
Die Formatbeispiele enthalten bewusst kein startbares Manifest; die
HTTP-Prüfung nutzt zusätzlich vollständige Desktop-Pins.

## Ausgabe, Aufrufe und Rahmen

Der vermittler misst die Summen selbst (M2/M4). Die Ausgabegrenze ist inklusiv;
das erste Byte darüber endet mit `limit/output`. Die ersten `tool_calls`
Aufrufversuche werden bearbeitet, der nächste endet mit `limit/tool_calls`
und erreicht das Gateway nicht. Der Lauf-Timer gilt auch während Weg B.

Feste Sätze, wortgleich mit Gateway 0.77.0 (`cmd/jnpt/script_contract.go`):

- `limit/output`: „Die Ausgabe des Skripts überschreitet die Grenze dieses Laufs.“
- `limit/tool_calls`: „Das Skript hat die Grenze der Werkzeugaufrufe dieses Laufs überschritten.“

Die gesonderte Protokollgrenze je Rahmen bleibt **1 MiB + 4 KiB**. Sie ist
keine Summengrenze für Ausgabe: der Wächter streamt stdout in 64-KiB-Stücken,
auch mit maximalem JSON-Escaping unter dieser Rahmengrenze. Es gibt im Wächter
und im Bootstrap keine eigene Ausgabe-/Aufrufsumme oder Lauf-Wandzeitkappe.
Der Wächter begrenzt Job-/Bootstrap-Rahmen auf 8 MiB (dazu gehört das volle Stück des
ETL-Stroms, siehe unten); das beschränkt weder die
Gesamtausgabe noch die Zahl der Aufrufe. Lauf-Protokoll (64 KiB), Job-/Codegröße,
Speicher, PIDs und Mindestlebensdauer bleiben eigenständige Grenzen.

## ETL-Rohstrom und Rückfall (Fassung 6, JanuaPort/januaport#1215 P2)

Kennung **3** ist Kennung 2 plus ETL-Rohstrom. Der Kern kennt sie ab seiner
Version **0.100.0** (`contract.MinCoreVersion`, Kern-Gegenstelle K2). Ein Läufer
mit Kennung 2 verhält sich unverändert: Header, Ende-Klassen, Rahmen und alle
Goldens der Fassungen 1 bis 5 bleiben bytegleich (`TestOldGoldensUnchanged`).

### Rückfallregel (E4)

Der Läufer bietet Kennung 3 nur an, wenn der Kern sie zulässt. Der vermittler
liest die Kern-Version aus `GET <Gateway>/healthz` (Feld `version`, höchstens
4 KiB, ohne Umleitung, ohne Proxy-Umgebung) und wählt:

| Lage | Kennung in `tools/list` |
|---|---|
| `version` ist `X.Y.Z` (optional mit `v`, nur Dezimalzahlen ohne führende Null) und ≥ 0.100.0 | **3** |
| `version` ist älter | 2 |
| `version` ist unlesbar: kein JSON, keine Zeichenkette, Vorabversion (`-rc1`), Build-Anhang, Leerraum, `dev`, fehlendes Feld, Status ≠ 200, Umleitung, Kern nicht erreichbar | 2 |
| noch keine Antwort (Start) | 2 |

Das Fenster zwischen Start und erster Antwort und jeder Ausfall führen zu 2,
nie zu 3: Der Rückfall nimmt nur eine Fähigkeit weg (`etl_stream`) und fügt
keine hinzu. Der vermittler fragt alle 30 s (`Config.CoreProbeInterval`) und
loggt nur die gewählte Fassung (`vertragsfassung contract=<n>`), nie den Text
des Kerns. Die Wahl gilt je Lauf ab dem Laufbeginn. Der Satz „eine neue Kennung
sperrt der Kern“ gilt weiter: Deshalb meldet der Läufer 3 nicht blind, sondern nur nach
dieser Prüfung; **Update-Reihenfolge ist damit Empfehlung** (erst der Kern, dann
der Läufer), nicht mehr Pflicht. Unter Kennung 2 kennt der Läufer keinen Strom:
`etl_open` wird mit `etl_end{vertrag}` beantwortet, und der Kern registriert einen
Pin mit `etl_stream` ohnehin nicht (`etl_needs_runner`).

`golden/tools_list_v3.json` ist `tools_list.json` mit `contract: 3`.

### Rahmen auf Weg C (Pull-Modell)

Gleicher Socket, gleiche Rahmung (4 Byte Länge, JSON mit `type`). Auf **jedes**
`etl_next` antwortet der vermittler mit **genau einem** `etl_chunk` oder `etl_end`.
`etl_open` hat keine eigene Antwort.

| Rahmen | Richtung | Inhalt | Golden |
|---|---|---|---|
| `etl_open` | Sandbox → vermittler | `{id, integration, query_id, params?}`, höchstens 64 KiB | `frame_etl_open.json` |
| `etl_next` | Sandbox → vermittler | `{id, close?}` | `frame_etl_next.json` |
| `etl_chunk` | vermittler → Sandbox | `{id, data_b64}`, roh höchstens 256 KiB, an beliebiger Bytegrenze geschnitten | `frame_etl_chunk.json` |
| `etl_end` | vermittler → Sandbox | `{id, status, code?, retry_after_s?}` | `frame_etl_end.json` |

`etl_end.status` ist eines aus `eof` (Körper zu Ende; ob der Strom vollständig war,
sagt der Endsatz in den Daten), `abgewiesen` (mit `code` = HTTP-Status 400, 401, 403,
404, 409, 429 oder 503 und `retry_after_s` als Zahl, nie ein Text des Kerns),
`abgebrochen`, `grenze`, `vertrag`, `geschlossen` (Antwort auf `close`).
Der Wächter reicht die Typen nur in ihrer Richtung durch (`etl_open`, `etl_next`
zum vermittler; `etl_chunk`, `etl_end` zum Skript), sonst nichts.

**Rahmengröße (gemessen):** Ein volles Stück (262 144 Byte roh) ist als Base64
349 528 Byte und als Rahmen 349 584 Byte (341,4 KiB). Die kleinste Leserschranke
auf dem Weg ist `MaxFromSandbox` (1 MiB + 4 KiB, vermittler), die des Wächters
8 MiB; es passt mit mehr als dem Dreifachen Luft, das Stück bleibt bei 256 KiB
(`TestETLFullChunkFitsEveryFrameLimit`). Die Grenzen je Rahmen gelten unverändert
beim Lesen.

### Der vermittler

- Er öffnet nur, wenn das Manifest `etl_stream` in `tools` deklariert; sonst
  `etl_end{abgewiesen}`. Höchstens **ein** Strom je Lauf: Jeder weitere Versuch,
  auch nach dem Ende des ersten, ist abgewiesen und macht das Ergebnis nie „fertig“.
- Den HTTP-Aufruf `POST /api/etl/stream` schickt er erst beim ersten `etl_next`, liest
  den Körper nur auf `etl_next` und puffert höchstens ein Stück. Er ist inhaltsblind:
  Er parst keine Zeilen, zählt Zeilenumbrüche, prüft nur die letzte Zeile strukturell
  auf `{"t":"end","grund":…}` und loggt nur Zähler.
- `etl_open` zählt nicht als Werkzeugaufruf. Stromzeilen zählen weder in `bytes_out`
  noch gegen die Ausgabegrenze; stdout und stderr behalten ihre 16 MiB.
- **Kein Abbruch nach Budget.** Der Deckel liegt allein im Gateway; der Zähler ist
  ein Beleg im Lauf-Ergebnis.
- Wandzeit, verlorene Sandbox, Kontext des Laufs, Skriptende und `close` schließen die
  HTTP-Anfrage.
- **Client (SEC E5):** Lauf-Token nur im `Authorization`-Header; keine Umleitung
  (`CheckRedirect` gibt einen Fehler zurück, ein Redirect ist `vertrag`); fester Pfad
  `/api/etl/stream`; `Proxy: nil`; Anfragekörper höchstens 64 KiB; gelesen wird mit
  Rahmengrenze; feste Fehlerklassen aus dem Statuscode, nie ein Antworttext.

### Feld `stream` im Lauf-Ergebnis

`_meta["januaport.ai/run"].stream = {status, bytes, zeilen}`, **nur wenn der Lauf einen
Strom geöffnet oder versucht hat**, nur Zähler, nie Inhalt. `bytes` sind die vom Kern
gelieferten Bytes, `zeilen` die Datenzeilen ohne Endsatz.

| `status` | Bedeutung |
|---|---|
| `fertig` | Die letzte Zeile war der Endsatz mit Grund `fertig` (und ein weiterer Versuch war nicht da). Nur das ist „vollständig gelesen“. |
| `grenze` | Endsatz mit Grund `deckel` oder `wandzeit`, oder `etl_open` über 64 KiB |
| `abgebrochen` | Endsatz mit anderem Grund, Strom ohne Endsatz, Verbindung abgerissen, Wandzeit, Sandbox verloren, Kontext |
| `geschlossen` | Das Skript hat den Strom selbst verlassen (`close`, Skriptende bei offenem Strom) |
| `abgewiesen` | Kein Manifest-Recht, zweiter Strom, oder HTTP 400/401/403/404/409/429/503 |
| `vertrag` | Unerwartete Antwort (anderer Status, falscher Inhaltstyp, Umleitung), falsche ID oder Form, Fassung 2 |

Der Kern wertet `ok` mit nicht-fertigem Strom als `error/stream` (K2). Beispiele:
`golden/end_ok_stream_fertig.json`, `golden/end_ok_stream_abgebrochen.json`
(unveränderte Kopien der Kern-Beispiele).

### SDK im Skript

`januaport.etl_stream(integration, query_id, params=None)` ist ein Generator, der die
Zeilen als dicts liefert, nie sammelt und die Zeilen über Stückgrenzen zusammensetzt.
Normal endet er nur beim Endsatz `fertig` (nicht mitgeliefert); jeder andere Grund wirft
`januaport.StreamEnded(grund, zeilen, bytes)`, ein fehlender Endsatz
`StreamEnded("abgebrochen")`, eine Abweisung `januaport.StreamRefused(code, retry_after)`.
Das SDK wiederholt nie selbst. `close()` am Generator sendet `etl_next{close}` und liest die
Antwort. Regeln für Skripte (E6): Stromzeilen **nie drucken** (alles, was ein Skript ausgibt,
steht im Lauf-Ergebnis), erst in ein Zwischenziel schreiben und nach `fertig` übernehmen,
nicht in einer Schleife wiederholen.

Nachweise: `internal/mediator/etl_test.go` (httptest-Kern, echter Socket, Kanarien,
Redirect, Rückfall), `internal/frame/etl_test.go`, `internal/guard/etl_linux_test.go`,
`sandbox/test_etl_sdk.py`.

## Referenzen und Nachweise

### Speicherquelle (#1277 S4b)

Abholer-Vertrag aus Plan c-6578615445 und SEC c-6578688656: separate Inbox
`speicher/inbox/list.json` und `speicher/inbox/pkg/<name>-<commit>.pkg`,
signierte vollständige Pinliste mit `zweck:skriptlaeufer-pins.v1`, ausschließlich
SHA-256 und nach Hash sortierte Paketobjekte. Das unterscheidet die Reihenfolge
vom bestehenden Desktop-Paketweg; dessen Goldens und Ausgabe bleiben unverändert.
Die Signatur bindet Name, Commit und Pfad; Git-Objekthashes binden den Inhalt.

`golden/storage/` ist unverändert aus `januaport` Branch `feat/1277-s4b-kern`
Commit `a28a2376cd5986c8d062365fd9d07b61cc12057e`,
`internal/desktop/testdata/runnerpins/` kopiert, einschließlich `SHA256SUMS`.
`TestCoreGoldenVectorsAndChecksums` prüft sämtliche Hashes und alle Fälle aus
`expectations.json` (einschließlich Großschreibvarianten und kleiner Ordnung).
Die Golden-Erwartungen enthalten eine 5-Minuten-Testpolicy; im Betrieb gelten
fest 10 Minuten plus höchstens 2 Minuten Zukunftstoleranz. Der öffentliche
Golden-Schlüssel ist ausschließlich für den reinen Prüfer zugelassen; der
Dateilader weist ihn wie den RFC-8032-Testschlüssel ab.

`TestCopiedContractFunctions` bewacht die Funktionen gegen die gemeinsame
Referenz in `desktop-runner/contract/contract_test.go`. Die einzigen benannten
Abweichungen sind der fehlschlagende SHA-1-Zweig, die Hashsortierung und das
bereits im Desktop-Client geltende DAG-Besuchslimit. Der Gleichlauf mit dem
Kern wird zusätzlich durch dessen signierte Golden-Vektoren geprüft.

`state.json` ist ein atomarer Version-1-Snapshot mit Fingerprint, höchster
Sequenz, `accepted:{manifest,sig}`, Freigaben samt `package_sha256` und festen
Fehlern `{index,state,reason}`. Index -1 bezeichnet die gesamte Liste, sonst
den Index in der angenommenen Pinliste. Frist ausschließlich aus
`accepted.manifest.issued_at`; bei Sequenz 0 Sperre ab eigener Prozessuhr bis
streng nach 12 Minuten, auch bei fehlendem Zustand, Wechsel und Neustart.
Fehlender/gewechselter Schlüssel entfernt Freigaben im selben Durchlauf.
Details und Adapterbelege stehen in [CLAUDE.md](../CLAUDE.md#speicherpins-1277-s4b).

Bote, Senke und Meldung sind angebunden: zunächst `pending`, bei Fehlern
`invalid/fetch_failed` (Liste, Schlüssel, Transport) bzw.
`invalid/hash_mismatch` (Paketstruktur, Hash). Eine Abholerfreigabe allein
ist kein `ready`; Manifest-, Bibliotheks- und Sandboxprüfung bleiben erforderlich.
Kein neues Feld in `tools/list`, Vertragskennung blieb damit 2. Bestehende
Git-/Desktop-Dateien und alle bisherigen Goldens bleiben bytegleich.

- `golden/call_headers.json`: Fassung 5 mit Grenz-Header.
- `golden/call_headers_v1.json`: unveränderte alten Lauf-Header ohne Grenz-Header;
  `TestRunHeadersGoldenV1AndV2` pinnt ihre Altgrenzen als Literale.
- `golden/tools_list*.json`: Vertragskennung 2; `golden/tools_list_v3.json`: Kennung 3 (Fassung 6).
- `golden/frame_etl_{open,next,chunk,end}.json`, `golden/end_ok_stream_{fertig,abgebrochen}.json`: ETL-Strom (Fassung 6).
- `golden/end_limit_{output,tool_calls}.json`: feste Sätze ohne Zahlen.
- Parser: `internal/contract/limits_test.go`; echter MCP-Adapter:
  `internal/mediator/limits_test.go`; echter Wächter-Adapter:
  `internal/guard/limits_linux_test.go`.
- [P3-Abschlussbericht](https://github.com/JanuaPort/januaport/issues/1040#issuecomment-6205261051): Verifikation, Mutationsproben und Fundstellen.

Der Speicherbote veröffentlicht unveränderte HTTP-Bytes atomar in der Inbox;
Redirects, Übergröße und Fehlerantworten werden abgewiesen, `Retry-After` und
404-Backoff von mindestens 5 Minuten eingehalten. Nur der Abholer erteilt
Freigaben. Die Senke öffnet das Paket über eine consumer-seitige Objektquelle,
prüft den exakten Pakethash und die Frist aus demselben `accepted`-Snapshot
vor jeder Codeübergabe, einschließlich nach dem Warten auf die Sandbox.
`internal/mediator/testdata/storage.pkg` ist eine separate ausführbare
Integrationsfixture, kein Kern-Golden. Die Docker-Sonde signiert mit einem
frisch erzeugten Wegwerf-Schlüssel; der Golden-Schlüssel bleibt gesperrt.
