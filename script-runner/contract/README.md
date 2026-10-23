# Draht-Vertrag, Fassung 5 — Laufgrenzen

JanuaPort/januaport#1040 P3, Plan 6176555886, SEC 6176617758 L1–L8.
Die Vertragskennung im Draht ist **2** (`internal/contract.Version`), unabhängig
von der Nummer dieser Textfassung. Bestehende Felder und Ende-Klassen gelten
weiter; Referenzdaten stehen unter `golden/`.

## Anmeldung und Kompatibilität

`tools/list` meldet `_meta["januaport.ai/runner"].contract = 2`, auch ohne Pins
oder bei ungültiger Isolation und auch mit Zeitplänen. Alle drei `tools_list*`
Golden-Dateien tragen diese Kennung. Das Dateiformat von Zeitplänen bleibt
unverändert bei Version 1; dies ist eine eigene Kennung.

**Benannte Kompatibilitätsgrenze:** Ein Läufer mit `contract: 2` braucht ein
Gateway ab **0.77.0**. Ein älteres Gateway sperrt den Läufer ganz
(„andere Vertragsfassung“). **Update-Reihenfolge: erst das Gateway, dann der Läufer.**
Ein Gateway ab 0.77.0 akzeptiert die Läuferkennungen 1 und 2; dieser Läufer
meldet ausschließlich 2.

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

| Größe | Ohne Header | Technische Decke | Wirksam in P3 |
|---|---|---|---|
| Wandzeit | 25 s | 86400 s (24 h) | `min(wall_s, manifest.timeout_s, 25 s)` |
| Ausgabe | 1 MiB | 16777216 Byte (16 MiB) | `min(output_bytes, Decke)` |
| Werkzeugaufrufe | 50 Aufrufe | 10000 | `min(tool_calls, Decke)` |

Werte über einer Decke werden **gekürzt**; nur der Zahlenüberlauf ist ein
Formfehler. Die Konvertierung in `int` erfolgt nach dem Kürzen. Die drei Decken
sind Konstanten im Läufer und durch Literal-Tests geschützt (SEC L8).

**SEC L6:** `mediator.l6WallCap` hält P3 unabhängig von der technischen
Wandzeitdecke bei 25 Sekunden. Vor längeren Läufen muss der vermittler seinen
Internet-Ausgang verlieren (#1069); die Freigabe und das optionale Manifestfeld
gehören zu P4/P5. Auch die bereits gehärtete Compose hebt diese Kappe nicht auf.
`timeout_s` bleibt für `jnpt-script.yaml` Pflicht mit 1..25. Die Eingabe bleibt auf 256 KiB begrenzt.

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
Der Wächter begrenzt Job-/Bootstrap-Rahmen auf 8 MiB; das beschränkt weder die
Gesamtausgabe noch die Zahl der Aufrufe. Lauf-Protokoll (64 KiB), Job-/Codegröße,
Speicher, PIDs und Mindestlebensdauer bleiben eigenständige Grenzen.

## Referenzen und Nachweise

- `golden/call_headers.json`: Fassung 5 mit Grenz-Header.
- `golden/call_headers_v1.json`: unveränderte alten Lauf-Header ohne Grenz-Header;
  `TestRunHeadersGoldenV1AndV2` pinnt ihre Altgrenzen als Literale.
- `golden/tools_list*.json`: Vertragskennung 2.
- `golden/end_limit_{output,tool_calls}.json`: feste Sätze ohne Zahlen.
- Parser: `internal/contract/limits_test.go`; echter MCP-Adapter:
  `internal/mediator/limits_test.go`; echter Wächter-Adapter:
  `internal/guard/limits_linux_test.go`.
- [P3-Abschlussbericht](https://github.com/JanuaPort/januaport/issues/1040#issuecomment-6205261051): Verifikation, Mutationsproben und Fundstellen.
