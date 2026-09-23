# kartei-rpa — eine Kartei-Arbeitsliste per RPA abarbeiten

Ein PowerShell-Modul und vier Aktions-Skripte, mit denen ein RPA-Werkzeug
(vorgesehen: **Power Automate Desktop**, kurz PAD) eine Arbeitsliste aus der
JanuaPort-Kartei abarbeitet: freigegebene Sätze holen, einzeln sperren, im
Fachsystem buchen, das Ergebnis zurückschreiben.

## Was es ist

- **Ein Beisteller neben JanuaPort, kein Teil davon.** Es läuft auf dem
  Roboter-Rechner (Windows), spricht JanuaPort über `/mcp` an und ruft dort
  genau drei Werkzeuge einer Kartei-Satzart: `<satzart>_search`,
  `<satzart>_get`, `<satzart>_update`.
- **Das Muster dahinter:** „Arbeitsliste für RPA" — eine KI bereitet Sätze vor,
  ein Mensch gibt sie frei (`geprueft`), der Roboter bucht sie im Fachsystem
  (`in_buchung` → `verbucht` oder `fehler`). Das Rezept „Zahlungseingang" in
  `januaport-recipes` beschreibt einen ganzen Use Case nach diesem Muster; die
  Voreinstellungen hier passen zu dessen Satzart `zahlungseingang`.
- **Was es NICHT ist:** kein Zeitplan, kein Retry, keine Oberfläche, keine
  Klickfolge. Was im Fachsystem geklickt wird, nimmt ein Mensch vor Ort in PAD
  auf — hier steht dazu bewusst nichts Erfundenes.

## Der Vertrag mit der Satzart

Der Roboter kennt vier Status-Werte. Sie sind **fest** — sie sind der Vertrag
des Musters, nicht einstellbar:

| Status | Wer setzt ihn | Bedeutung |
|---|---|---|
| `geprueft` | KI nach Freigabe durch einen Menschen | Arbeit für den Roboter |
| `in_buchung` | Roboter (Schritt 2) | gesperrt — **der Status ist die Sperre** |
| `verbucht` | Roboter (Schritt 3) | im Fachsystem gebucht, Buchungsnummer steht im Satz |
| `fehler` | Roboter (Schritt 4) | nicht gebucht, Schritt und Meldung stehen im Satz |

Dazu braucht die Satzart vier Felder, deren **Namen** Sie einstellen können
(Voreinstellung in Klammern): Buchungsnummer (`buchungs_nr`), Lauf-Kennung
(`roboter_lauf`), Fehlertext (`fehler_text`), Bearbeitungszeitpunkt
(`bearbeitet_am`). Und die Felder, die der Roboter zum Tippen braucht
(`beleg_nummer`, `nummer_art`, `betrag`, `buchungsdatum`).

## Einstellungen

Ohne jede Einstellung passt das Paket zur Satzart `zahlungseingang` des
Rezepts. Trägt Ihre Satzart andere Feldnamen, legen Sie **neben das Modul** eine
Datei `kartei-rpa.einstellungen.psd1`. Vorlage mit allen Schlüsseln und ihren
Voreinstellungen: `kartei-rpa.einstellungen.example.psd1`.

| Schlüssel | Voreinstellung | Wofür |
|---|---|---|
| `CredentialTarget` | `jnpt-roboter` | Name des Eintrags im Windows-Credential-Manager |
| `Quelle` | `roboter:buchung` | was jedes `update` als `source` behauptet (steht im Audit) |
| `Felder.Buchungsnr` | `buchungs_nr` | Feld für die Buchungsnummer aus dem Fachsystem |
| `Felder.Lauf` | `roboter_lauf` | Feld für die Lauf-Kennung |
| `Felder.FehlerText` | `fehler_text` | Feld für Schritt und Meldung |
| `Felder.BearbeitetAm` | `bearbeitet_am` | Feld für den Zeitpunkt |
| `Ausgabefelder` | `beleg_nummer, nummer_art, betrag, buchungsdatum` | was Schritt 1 und 2 je Satz an den Flow geben (dazu immer `id`) |

Regeln:

- **Fehlt die Datei, gelten die Voreinstellungen.** In der Datei steht nur,
  was abweicht.
- **Ein unbekannter Schlüssel ist ein Fehler** — auch innerhalb von `Felder`.
  Ein Tippfehler fällt nicht still auf die Voreinstellung zurück, sondern
  bricht den Schritt ab, **bevor** ein Token geholt oder eine Anfrage gesendet
  wird.
- Ein Feldname darf nicht `id`, `status`, `source` oder `as_of` sein, und zwei
  Rollen dürfen nicht auf dasselbe Feld zeigen.
- Wer das Modul direkt aufruft, kann jede Einstellung auch als Parameter von
  `Connect-JnptKartei` übergeben; ein Parameter schlägt die Datei.
- Die Satzart selbst ist **keine** Einstellung, sondern eine Flow-Variable
  (siehe `pad/FLOW-VORLAGE.md`) bzw. der Parameter `-Satzart`.
- Die Datei ist nur ASCII (Windows PowerShell 5.1) und trägt **nie** einen
  Token.

## Voraussetzungen

- Windows mit **Windows PowerShell 5.1** (die Fassung, die PAD startet).
- Eine Kartei-Satzart mit den Feldern oben, auf der Anlage eingerichtet.
- Ein **Token** für den Roboter mit genau zwei Scopes — die Satzart lesen und
  **ein** werkzeug-genaues Schreibrecht:

  ```bash
  jnpt token create --label "<roboter-token>" --owner <user-id> \
    --allow zahlungseingang \
    --allow zahlungseingang:zahlungseingang_update
  ```

  Kein `add`, kein `delete`: Der Roboter legt nichts an und löscht nichts.
  Ein Token ist ein Zugang und belegt einen Platz, bis er widerrufen wird.

## Einrichtung auf dem Roboter-Rechner

Alle Schritte **in der Sitzung des Kontos, unter dem der Flow läuft** — der
Credential-Manager ist benutzer- *und* gerätegebunden.

**1. Dateien kopieren**, z. B. nach `C:\JanuaPort\kartei-rpa` (Modul,
`pad\`, bei Bedarf Ihre Einstellungsdatei). Den Ordner `tests\` braucht der
Rechner nicht.

**2. Sperrvermerk entfernen.** Heruntergeladene Dateien blockiert Windows:

```powershell
Get-ChildItem C:\JanuaPort\kartei-rpa -Recurse -Include *.ps1,*.psm1,*.psd1 | Unblock-File
```

**3. Ausführungsrichtlinie prüfen.** Die Dateien sind nicht signiert:

```powershell
Get-ExecutionPolicy -List
Set-ExecutionPolicy -Scope CurrentUser RemoteSigned   # falls Restricted oder AllSigned
```

**4. Token ablegen.** Der Wert erscheint bei `token create` genau einmal:

```powershell
cmdkey /generic:jnpt-roboter /user:roboter /pass:<Token-Wert>
```

Der Name nach `/generic:` muss `CredentialTarget` entsprechen. Prüfen, ohne
den Wert zu zeigen:

```powershell
Import-Module C:\JanuaPort\kartei-rpa\JanuaPort.Kartei.psm1
(Get-JnptTokenFromCredentialManager -Ziel 'jnpt-roboter').Length
```

**5. Anschluss prüfen** — bevor irgendein Flow gebaut wird:

```powershell
C:\JanuaPort\kartei-rpa\Test-JnptAnschluss.ps1 -Endpoint https://example.com/mcp
```

Vier Stufen: Namensauflösung, TCP, TLS-Vertrauen, Token mit `ping`. Die
oberste rote Stufe ist die Ursache; der Rat dazu steht in der Ausgabe. Das
Skript liest nur. Ohne `-CredentialTarget` nimmt es den Namen aus Ihrer
Einstellungsdatei bzw. die Voreinstellung. Danach steht im Audit der Anlage
eine `ping`-Zeile mit diesem Zugang — fehlt sie, ist nichts angekommen.

**6. Werkzeug-Zuschnitt prüfen.** Am Katalog, nicht im Vertrauen:

```powershell
$ep  = 'https://example.com/mcp'
$tok = Get-JnptTokenFromCredentialManager -Ziel 'jnpt-roboter'
Connect-Jnpt -Endpoint $ep -Token $tok
(Invoke-JnptRpc -Endpoint $ep -Token $tok -Message @{ jsonrpc = '2.0'; id = 99; method = 'tools/list' }).tools.name
```

Erwartet: `<satzart>_get`, `_search`, `_update` — weder `_add` noch `_delete`.

## Ein Probelauf ohne PAD

```powershell
C:\JanuaPort\kartei-rpa\Invoke-Lauf.ps1 -Endpoint https://example.com/mcp -Hoechstens 1
```

Derselbe Lauf wie im Flow, am Stück — mit einem **Platzhalter** statt der
Klickfolge, der eine erfundene Buchungsnummer liefert. Also **nur gegen
Testsätze** fahren: Der Satz steht danach auf `verbucht`, obwohl im
Fachsystem nichts gebucht wurde.

- `-Hoechstens <n>` ist der **Lauf-Deckel**: Nach `n` verbuchten oder auf
  `fehler` gesetzten Sätzen ist Schluss; der Rest bleibt auf `geprueft`.
- `-Seitengroesse <n>` (1–100) ist nur die Größe **einer** Abfrage. Ohne
  `-Hoechstens` arbeitet der Lauf **alle** freigegebenen Sätze ab, egal wie
  klein die Seitengröße ist. Die kleinste Zahl an der falschen Stelle sieht
  sicher aus und ist es nicht.
- `-Satzart` (Voreinstellung `zahlungseingang`), `-CredentialTarget` und
  `-Token` (nur für einen Handlauf; landet in der Kommandozeilen-Historie)
  sind optional.

Die Ausgabe trägt nur Zähler und die Lauf-Kennung, nie Satzinhalte.

## Mit PAD

`pad/FLOW-VORLAGE.md` lesen, dann den Inhalt der vier Skripte je in eine
Aktion **„PowerShell-Skript ausführen"** einfügen:

| Skript | Schritt | Ausgabe (eine Zeile JSON) |
|---|---|---|
| `pad/1-arbeitsliste.ps1` | Vorprüfung + freigegebene Sätze holen | `{"lauf","anzahl","saetze":[{"id", <Ausgabefelder>}]}` |
| `pad/2-satz-sperren.ps1` | Satz auf `in_buchung` | `{"id", <Ausgabefelder>}`, frisch gelesen |
| `pad/3-satz-verbuchen.ps1` | Satz auf `verbucht` mit Buchungsnummer | `{"id","status"}` |
| `pad/4-satz-fehler.ps1` | Satz auf `fehler` mit Schritt und Meldung | `{"id","status"}` |

Ein Abbruch ist immer ein Skriptfehler (PAD-Variable `ScriptError`).

Die Platzhalter in Prozentzeichen sind PAD-Variablen: `Modulpfad`,
`Endpoint`, `Satzart`, `SatzId`, `Lauf`, `Buchungsnr`, `Schritt`, `Meldung`.
Es gibt **keinen** Platzhalter für den Token. Zwei Einstellungen je Aktion
sind nicht verhandelbar: **Timeout 120** (der Default 10 reißt den Lauf) und
**„Bei Fehler wiederholen" AUS**. Läuft ein Skript versehentlich ohne PAD,
bricht es mit „Platzhalter nicht ersetzt" ab.

## Was das Modul garantiert

- **Jedes Schreiben liest vorher.** Es gibt genau eine Stelle, die
  `<satzart>_update` ruft; davor steht immer ein `get` mit Prüfung. Weil
  `update` vollständig überschreibt, gehen **alle** gelesenen Felder zurück.
- **Nie doppelt buchen:** Schritt 3 verweigert einen Satz mit schon gefüllter
  Buchungsnummer und eine leere Buchungsnummer.
- **Nur eigene Sätze:** Schritt 3 und 4 verlangen `in_buchung` **und** die
  Lauf-Kennung dieses Laufs. Einen fremden Lauf fasst der Roboter nicht an.
- **Die Bremse:** Steht schon ein Satz auf `in_buchung`, bricht Schritt 1 ab.
  Dann ist ein früherer Lauf mittendrin gestorben, und ein Mensch sieht im
  Fachsystem nach. Kein Skript räumt das weg.
- **Kein Retry auf Schreibschritten.** JanuaPort sendet jeden Aufruf genau
  einmal, es gibt keine Deduplizierung.
- **Der Token erscheint nie** in einer Ausgabe, einem Protokoll oder einem
  Fehlertext.
- **Der Fehlertext ist eine Meldung, kein Protokoll:** eine Zeile, höchstens
  200 Zeichen. Keine personenbezogenen Daten hineinschreiben.

## Grenzen

- **Kein Push, kein Trigger.** Die Kartei meldet sich nicht; der Flow fragt in
  seinem Takt. PAD selbst plant nicht (`pad/FLOW-VORLAGE.md` §5).
- **Kein Konflikt-Schutz** über den Status hinaus — kein `If-Match`, keine
  Transaktion. Der Status ist die Sperre und trägt, solange sich alle daran
  halten.
- **Pseudonyme reisen wörtlich mit.** Der Roboter bekommt nur die
  Ausgabefelder — wählen Sie sie so, dass er nichts anderes braucht.
- **Kein Ersatz für einen Connector.** Hat das Fachsystem eine Schnittstelle,
  gehört es als Integration an JanuaPort, nicht an eine Klickfolge.

## Status

- **Getestet:** mit Pester unter Windows PowerShell 5.1, Transport gemockt
  (Workflow `kartei-rpa`). Die Tests prüfen jede Verweigerung, das
  vollständige Überschreiben, die Einstellungen, die Ausgabeformen der vier
  PAD-Skripte und den Quelltext selbst (nur ASCII, kein Token-Wert, keine
  fremden Prozent-Paare in `pad/`).
- **Herkunft:** Die Vorfassung dieses Moduls lief in einem Pilotbetrieb gegen
  eine echte Anlage über TLS. **Diese verallgemeinerte Fassung ist neu
  geschrieben und noch nicht gegen eine echte Anlage gelaufen.**
- **PAD wurde für dieses Paket nicht bedient.** Die Schrittfolge in
  `pad/FLOW-VORLAGE.md` stammt aus der Microsoft-Dokumentation; gelaufen ist
  das PowerShell, das PAD ausführen würde.

## Tests

```powershell
Import-Module Pester -MinimumVersion 5.0
Invoke-Pester kartei-rpa/tests
```

## Enthaltene Dateien

| Datei | Inhalt |
|---|---|
| `JanuaPort.Kartei.psm1` | das Modul: Transport, Token aus dem Credential-Manager, Einstellungen, die Schritte des Laufs |
| `kartei-rpa.einstellungen.example.psd1` | Vorlage der optionalen Einstellungsdatei |
| `pad/1-arbeitsliste.ps1` … `pad/4-satz-fehler.ps1` | die vier PAD-Aktionen |
| `pad/FLOW-VORLAGE.md` | die Schrittfolge in PAD-Begriffen |
| `Invoke-Lauf.ps1` | Probelauf ohne PAD |
| `Test-JnptAnschluss.ps1` | Anschluss-Prüfung auf dem Roboter-Rechner |
| `tests/` | Pester-Tests |
| `CLAUDE.md` | Hinweise für KI-Agenten, die an diesem Ordner arbeiten |
