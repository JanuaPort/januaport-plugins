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
`timeout_s` bleibt Pflicht mit 1..25. Die Eingabe bleibt auf 256 KiB begrenzt.

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
