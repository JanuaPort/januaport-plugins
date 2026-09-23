# Flow-Vorlage: eine Kartei-Arbeitsliste im Fachsystem buchen (Power Automate Desktop)

> **Woher diese Vorlage kommt — und woher nicht.**
> Sie ist eine **Schrittfolge nach der Microsoft-Dokumentation**
> ([Skript-Aktionen](https://learn.microsoft.com/de-de/power-automate/desktop-flows/actions-reference/scripting),
> [Fluss-Steuerung](https://learn.microsoft.com/de-de/power-automate/desktop-flows/actions-reference/flowcontrol),
> [Nebenläufigkeit](https://learn.microsoft.com/de-de/power-automate/desktop-flows/run-desktop-flows-concurrently)),
> **nicht** der Mitschnitt eines gelaufenen Flows. Für dieses Paket wurde
> Power Automate Desktop (PAD) **nicht bedient** — es gibt deshalb bewusst
> **keinen Flow-Text zum Einfügen**. Ein plausibel aussehender, nie gelaufener
> Flow-Text wäre genau die Art Artefakt, die jemand kopiert und die dann bricht.
> Sie bekommen stattdessen die Schrittfolge, jede Einstellung, die man falsch
> machen kann, und die Anleitung, wie Sie Ihren *eigenen* Flow als Text
> sichern (Abschnitt 6).
>
> Die Bezeichnungen können sich zwischen PAD-Fassungen unterscheiden; jede
> Aktion steht deshalb mit deutschem und englischem Namen da.

Die Skripte liegen neben dieser Datei; Einrichtung und Einstellungen stehen in
`../README.md`.

---

## 1. Warum die Schleife im Flow lebt und nicht im Skript

Jede Aktion **„PowerShell-Skript ausführen"** startet einen **eigenen Prozess**.
Zwischen zwei Aktionen gibt es keinen geteilten Zustand: keine Variable, keine
offene Verbindung, kein Modul im Speicher. Und die Klickfolge im Fachsystem ist
eine Folge von **PAD-UI-Aktionen**, kein PowerShell.

Also: Die Schleife (`Für jeden`) steht im Flow, PowerShell übernimmt nur die
vier Berührungen mit JanuaPort. Jedes Skript importiert das Modul, holt den
Token aus dem Credential-Manager, macht den Handschlag (zwei kurze Anfragen)
und tut **einen** Schritt.

```
PAD-Flow                                  PowerShell (je Aktion ein Prozess)
1. Arbeitsliste holen ──────────────────▶ 1-arbeitsliste.ps1
2. JSON -> Objekt; anzahl = 0 -> Ende
3. Fuer jeden Satz:
   a. Satz sperren ─────────────────────▶ 2-satz-sperren.ps1
   b. Klickfolge im Fachsystem (UI-Aktionen)
   c. Erfolg: verbuchen ────────────────▶ 3-satz-verbuchen.ps1
      Fehler in b: fehler schreiben ────▶ 4-satz-fehler.ps1
4. Ende: Zaehler ausgeben
```

---

## 2. Eingabevariablen des Flows

Drei Werte, einmal gesetzt (Aktion **„Variable festlegen"** / *Set variable*,
oder als Eingabevariablen des Flows):

| Variable | Beispielwert | Bedeutung |
|---|---|---|
| `Modulpfad` | `C:\JanuaPort\kartei-rpa` | Ordner, in dem `JanuaPort.Kartei.psm1` liegt |
| `Endpoint` | `https://example.com/mcp` | genau `/mcp`, **kein** Schrägstrich am Ende |
| `Satzart` | `zahlungseingang` | der Name der Kartei-Satzart auf der Anlage |

Feldnamen, Quelle und den Namen des Credential-Eintrags setzt **keine**
Flow-Variable, sondern die optionale Einstellungsdatei neben dem Modul
(`../README.md`, Abschnitt „Einstellungen").

**Der Token ist keine Flow-Variable.** PAD ersetzt seine Variablen im
Skripttext **vor** der Ausführung — ein Token in einer Flow-Variablen stünde im
Klartext im Skript, im Flow-Export und in jedem Screenshot. Er liegt im
Windows-Credential-Manager, angelegt mit `cmdkey` **unter dem Konto, unter dem
der Flow läuft**.

---

## 3. Die Schrittfolge

Vor jeder Skript-Aktion gilt: **Timeout 120** (Abschnitt 4) und
**„Bei Fehler wiederholen" AUS**.

| # | Aktion (deutsch / englisch) | Einstellungen |
|---|---|---|
| 1 | **PowerShell-Skript ausführen** / *Run PowerShell script* | Inhalt: `1-arbeitsliste.ps1`. Ausgabe → `PowershellOutput`, Fehler → `ScriptError` |
| 2 | **Wenn** / *If* | `ScriptError` ist nicht leer → **Meldung anzeigen** und **Flow beenden** (*Stop flow*) |
| 3 | **JSON in benutzerdefiniertes Objekt konvertieren** / *Convert JSON to custom object* | `PowershellOutput` → `Arbeitsliste` |
| 4 | **Wenn** / *If* | `Arbeitsliste.anzahl` = `0` → **Flow beenden**. Ein Lauf ohne Arbeit ist der Normalfall |
| 5 | **Variable festlegen** / *Set variable* | `Lauf` = Wert von `Arbeitsliste.lauf`, `Gebucht` = `0`, `Fehlerhaft` = `0` |
| 6 | **Für jeden** / *For each* | über `Arbeitsliste.saetze`, aktuelles Element `Satz` |
| 6a | **Variable festlegen** / *Set variable* | `SatzId` = Wert von `Satz.id` |
| 6a′ | **PowerShell-Skript ausführen** | Inhalt: `2-satz-sperren.ps1` (nutzt `SatzId` und `Lauf`). Ausgabe → `SperreOutput`, Fehler → `SperreError` |
| 6b | **Wenn** / *If* | `SperreError` ist nicht leer → **Flow beenden**. Kein „Weiter": Scheitert die Sperre, hat jemand dazwischengefunkt |
| 6c | **JSON in benutzerdefiniertes Objekt konvertieren** | `SperreOutput` → `Aktuell` (trägt `id` und die Ausgabefelder, per Voreinstellung `beleg_nummer`, `nummer_art`, `betrag`, `buchungsdatum`) |
| 6d | **Bei Blockfehler** / *On block error* | umschließt 6e; im Fehlerfall: **Ausnahmebehandlung → Nächste Aktion ausführen** (siehe 6g) |
| 6e | *(die Klickfolge im Fachsystem)* | UI-Aktionen: Maske nach `Aktuell.nummer_art` öffnen, `Aktuell.beleg_nummer`, `Aktuell.betrag`, `Aktuell.buchungsdatum` eintragen, buchen, **Buchungsnummer auslesen** → `Buchungsnr`. **Nimmt ein Mensch vor Ort auf** — hier steht bewusst nichts Erfundenes |
| 6f | **PowerShell-Skript ausführen** | Inhalt: `3-satz-verbuchen.ps1`; `SatzId`, `Lauf`, `Buchungsnr`. Danach **Variable erhöhen** `Gebucht` |
| 6g | *(Fehlerzweig aus 6d)* | **PowerShell-Skript ausführen**: `4-satz-fehler.ps1`; `SatzId`, `Lauf`, `Schritt` = Name der gescheiterten Aktion, `Meldung` = PAD-Fehlertext. **Variable erhöhen** `Fehlerhaft`; **Nächstes Schleifenelement** / *Next loop item* |
| 7 | **Ende** | **Meldung anzeigen** oder in eine Protokolldatei: Lauf-Kennung, `Gebucht`, `Fehlerhaft` — **nur Zähler und Lauf-Kennung, keine Satzinhalte** |

Die Skripte erwarten genau diese Flow-Variablen, unter genau diesen Namen:
`Modulpfad`, `Endpoint`, `Satzart`, `SatzId`, `Lauf`, `Buchungsnr`, `Schritt`,
`Meldung`. Den Skripttext ändern Sie nicht — Sie setzen die Variablen.

Nach der Schleife holt der nächste geplante Lauf den Rest: Die Suche liefert
höchstens 100 Sätze je Aufruf. Wer mehr erwartet, lässt den Flow mehrmals
laufen — die Vorprüfung schützt weiterhin vor Überlappung.

---

## 4. Vier Regeln, ohne die es schiefgeht

1. **Timeout ehrlich setzen.** Der Default der Skript-Aktion ist **10 Sekunden**.
   Handschlag plus `get` plus `update` brauchen mehr, sobald das Netz zäh ist.
   **120** ist der Vorschlag. Wird eine Aktion mitten im Schreiben
   abgeschnitten, bleibt ein Satz auf `in_buchung` stehen — der nächste Lauf
   bricht daran ab.
2. **„Bei Fehler wiederholen" AUS.** Ein wiederholter *Lauf* ist harmlos (die
   Vorprüfung bricht ab), ein wiederholter *Schreibschritt* nicht: JanuaPort
   sendet jeden Aufruf genau einmal, es gibt keine Deduplizierung.
3. **Ein Lauf zur Zeit.** Zwei Bremsen, und Sie brauchen beide: die
   PAD-Warteschlange (ein unbeaufsichtigter Bot fährt einen Desktop-Flow zur
   Zeit) **und** die Vorprüfung in Schritt 1, die auch greift, wenn ein anderer
   Rechner oder ein Mensch dazwischenkommt.
4. **`ScriptError` nach jeder Skript-Aktion prüfen.** PAD füllt bei einem
   `throw` die Variable `ScriptError` und lässt den Flow sonst **weiterlaufen**.
   Ohne die `Wenn`-Prüfung klickt der Flow nach einem Fehlschlag munter weiter.

**Was der Flow nie tut:** einen Satz auf `geprueft` zurücksetzen, einen
hängenden `in_buchung`-Satz aufräumen oder einen Schreibschritt wiederholen.
Der hängende Satz ist die Bremse, nicht der Schaden — er zwingt einen Menschen,
im Fachsystem nachzusehen.

---

## 5. Ablaufplanung — die ehrliche Grenze

**Power Automate Desktop plant selbst nicht.** Es gibt keinen Zeitplan im
Desktop-Client. „Werktags um 16 Uhr" heißt einer von drei Wegen:

| Weg | Was nötig ist | Anmerkung |
|---|---|---|
| **Cloud-Flow mit Zeitplan-Trigger**, der den Desktop-Flow startet | Power-Automate-Lizenz mit unbeaufsichtigter RPA (Premium) | der übliche Weg für „läuft ohne mich" |
| **Windows-Aufgabenplanung**, die PAD startet | keine zusätzliche Lizenz, aber angemeldete Sitzung | brüchiger; die Sitzung muss offen sein |
| **Ein Mensch startet den Flow** | nichts | für den Anfang oft völlig ausreichend |

Ein Takt von einmal am Tag ist reichlich: Die Kartei ist **Zustand, kein
Ereignis** — es gibt keinen Trigger, auf den man schneller reagieren könnte.
Welcher Weg es wird, entscheidet der Betreiber; Modul und Skripte sind davon
unabhängig.

---

## 6. Aktionen als Text sichern (wenn Ihr Flow steht)

PAD kopiert markierte Aktionen als **Text** in die Zwischenablage und nimmt
Text auch wieder als Aktionen an:

1. Im Flow-Designer die Aktionen markieren (Strg+A für alle).
2. **Strg+C**.
3. In einen Texteditor einfügen — das ist Ihr Flow-Text.
4. Zurück: Text kopieren, im Designer **Strg+V**.

⚠️ **Bevor Sie so etwas weitergeben:** Der Text enthält alles, was in den
Aktionen steht — Pfade, Rechnernamen, jede gesetzte Variable. Der Token ist
deshalb keine Variable (Abschnitt 2). Prüfen Sie den Text trotzdem, bevor er
ein Ticket oder eine Mail erreicht.

*Dieses Kopier-Format ist in der Microsoft-Dokumentation nicht als
Austauschformat beschrieben; gängige Praxis, aber keine Zusage. Der verlässliche
Weg für ein Backup bleibt der Flow-Export.*

---

## 7. Abnahme

Bevor der Flow scharf geschaltet wird:

1. `tools/list` mit dem Roboter-Token zeigt **drei** Werkzeuge der Satzart
   (`_get`, `_search`, `_update`) — **kein** `_add`, **kein** `_delete`.
2. `Test-JnptAnschluss.ps1` ist auf dem Roboter-Rechner in allen vier Stufen
   grün, und der `ping` steht im Audit der Anlage.
3. Ein Lauf mit drei Testsätzen: drei auf `verbucht`, **sechs** Schreib-Zeilen
   im Audit (je Satz eine für `in_buchung`, eine für `verbucht`).
4. Abbruchtest: ein von Hand auf `in_buchung` gesetzter Satz lässt den nächsten
   Lauf in Schritt 1 abbrechen.
5. Widerrufstest: nach `token revoke` endet der nächste Lauf sofort mit `401`,
   ohne etwas anzufassen.
6. Das Lauf-Protokoll trägt nur Zähler und die Lauf-Kennung.
7. Kein Token-Wert im Flow, im Export oder in einem Screenshot.
8. Fehlertest: ein Satz landet auf `fehler` mit einem Fehlertext **ohne**
   personenbezogene Daten.
