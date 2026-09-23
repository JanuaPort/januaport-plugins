# CLAUDE.md — `kartei-rpa` (Beisteller: Kartei-Arbeitsliste für RPA)

Für Agenten, die an diesem Ordner arbeiten. Die Anleitung für Betreiber steht
in `README.md` — hier steht, warum das Ding so aussieht, wie es aussieht.

## Verantwortung

PowerShell, das die Kartei-Werkzeuge einer Satzart über JanuaPorts `/mcp`
aufruft, damit ein RPA-Werkzeug (vorgesehen: Power Automate Desktop) eine
Arbeitsliste abarbeitet. Kein Gateway-Code, kein Go, nichts davon läuft in der
Anlage. Entstanden in JanuaPort/januaport#806 als verallgemeinerte Neufassung
eines Pilot-Beistellers; Muster `arbeitsliste-rpa`, Voreinstellungen = Satzart
`zahlungseingang` des Rezepts in `januaport-recipes`.

## Öffentliche Schnittstelle

`JanuaPort.Kartei.psm1` exportiert vierzehn Funktionen:

| Gruppe | Funktionen |
|---|---|
| Transport | `Invoke-JnptRpc`, `Connect-Jnpt`, `Invoke-JnptTool` |
| Zugangsdaten | `Get-JnptTokenFromCredentialManager` (Parameter `-Ziel`) |
| Einstellungen | `Get-JnptKarteiEinstellungen` (`-Pfad`, `-Vorgabe`) |
| Kartei-Eigenheiten | `ConvertTo-JnptSaetze`, `New-JnptUpdateArgs`, `ConvertTo-JnptAusgabe` |
| Lauf | `Connect-JnptKartei`, `New-JnptLauf`, `Get-JnptArbeitsliste`, `Lock-JnptSatz`, `Set-JnptSatzErgebnis`, `Set-JnptSatzFehler` |

Nicht exportiert und frei änderbar: `Find-JnptSaetze`, `Get-JnptSatz`,
`Set-JnptSatz`, `New-JnptAenderung`, `Get-JnptFeld`, `Assert-JnptEigenerLauf`,
`Format-JnptFehlerText`, `Merge-JnptEinstellungen`, `Assert-JnptEinstellungen`.

Das **Sitzungsobjekt** aus `Connect-JnptKartei` trägt `Endpoint`, `Token`,
`Satzart`, `Protokollfassung`, `TimeoutSec`, `Quelle`, `Felder` (Hashtable mit
`Buchungsnr`, `Lauf`, `FehlerText`, `BearbeitetAm`) und `Ausgabefelder`. Es ist
die einzige Form, in der der Token durch den Code reist.

Die **PAD-Platzhalter** sind Teil der Schnittstelle und eingefroren:
`Modulpfad`, `Endpoint`, `Satzart`, `SatzId`, `Lauf`, `Buchungsnr`, `Schritt`,
`Meldung`. Ein neuer Platzhalter bricht jeden gebauten Flow; neue Werte gehören
in die Einstellungsdatei.

Die **Ausgabeformen** der vier Skripte sind der Vertrag mit dem Flow:
Schritt 1 `{"lauf","anzahl","saetze":[{"id",<Ausgabefelder>}]}`, Schritt 2
`{"id",<Ausgabefelder>}` frisch aus `get`, Schritte 3/4 `{"id","status"}`,
Abbruch = `throw` (ScriptError).

## Einstellungen

Drei Ebenen, jede überschreibt die vorige: **Voreinstellung** im Modul →
**Datei** `kartei-rpa.einstellungen.psd1` neben dem Modul
(`Import-PowerShellDataFile`) → **Parameter** von `Connect-JnptKartei`.
`Felder` wird je Schlüssel zusammengeführt, `Ausgabefelder` als Ganzes ersetzt.
Geprüft wird in `Get-JnptKarteiEinstellungen`, **bevor** ein Token geholt wird.

**Nicht einstellbar, mit Absicht:** die Status-Werte (`geprueft`,
`in_buchung`, `verbucht`, `fehler`) — sie sind der Vertrag des Musters; und die
Satzart — sie ist eine Flow-Variable, weil ein Rechner mehrere Flows für
verschiedene Satzarten tragen kann.

## Invarianten (die tragen die Sicherheit dieses Pakets)

1. **Jedes Schreiben liest vorher.** Genau eine Stelle ruft `…_update`:
   `Set-JnptSatz`. Jeder Aufrufer hat davor `Get-JnptSatz` gerufen und den
   Zustand geprüft. `update` überschreibt vollständig — `New-JnptUpdateArgs`
   schickt alle gelesenen Felder zurück.
2. **Kein Retry auf Schreibschritten.** JanuaPort sendet einen `tools/call`
   genau einmal, ohne Deduplizierung. Eine Wiederholungsschleife hier wäre ein
   Fehler, kein Feature.
3. **Nur eigene Sätze fortschreiben.** `Assert-JnptEigenerLauf` verlangt
   `status = in_buchung` **und** Lauf-Feld = dieser Lauf.
4. **Nie doppelt buchen.** `Set-JnptSatzErgebnis` verweigert ein gefülltes
   Buchungsnummer-Feld und eine leere Buchungsnummer (letzteres ohne Aufruf).
5. **Kein Aufräumen.** Ein hängender `in_buchung`-Satz bricht den nächsten
   Lauf ab (`Get-JnptArbeitsliste`). Wer das „repariert", nimmt dem Betreiber
   die einzige Absicherung gegen Doppelbuchungen.
6. **Suche bis leer, Deckel 100 je Abfrage.** `Invoke-Lauf.ps1` holt in Runden,
   bis `geprueft` leer ist; `-Seitengroesse` ist kein Lauf-Deckel, nur
   `-Hoechstens` ist einer.
7. **Der Token erscheint nie** in Log, Ausgabe oder Fehlertext. Es gibt keinen
   PAD-Platzhalter für ihn — er kommt aus dem Credential-Manager.
8. **Der Fehlertext ist eine Meldung:** eine Zeile, höchstens 200 Zeichen.
9. **Eingestellte Feldnamen kollidieren nicht** mit `id`, `status`, `source`,
   `as_of` und nicht miteinander — sonst könnte eine Einstellung den Status
   überschreiben.
10. **Quelltext (.ps1/.psm1/.psd1) ist ASCII ohne BOM.** Windows PowerShell 5.1
    liest eine Datei ohne BOM als ANSI; ein Sonderzeichen bricht den Parser.
    Auch Meldungstexte („Saetze", nicht „Sätze").

Die Tests in `tests/Quelltext.Tests.ps1` erzwingen 10, dazu: kein Token-Wert,
keine Kennung des Pilotbetriebs (Sperrliste als SHA-256), nur `example.com` und
`localhost` als Host im Code. `tests/Pad.Tests.ps1` erzwingt, dass in `pad/*.ps1`
kein Prozent-Paar außer den bekannten Platzhaltern steht.

## Bewusste Design-Entscheidungen

- **Die Schleife lebt im Flow, nicht im Modul.** Jede PAD-Aktion „PowerShell-
  Skript ausführen" ist ein eigener Prozess, und die Klickfolge ist eine Folge
  von UI-Aktionen. Deshalb liefert das Modul Schritte, `pad/*.ps1` ist je ein
  Schritt mit einer Zeile JSON. `Invoke-Lauf.ps1` ist dieselbe Schleife am
  Stück, für Probeläufe.
- **Einstellungsdatei statt neuer Platzhalter.** Neue Platzhalter hätten jeden
  Flow gebrochen und die Fehlerfläche in PAD vergrößert; eine optionale Datei
  neben dem Modul ändert nichts, solange sie fehlt.
- **Unbekannter Schlüssel = Fehler.** Ein still ignorierter Tippfehler hieße:
  der Roboter schreibt in das Voreinstellungsfeld, das es in dieser Satzart
  vielleicht gar nicht gibt — und die Doppelbuchungs-Prüfung sähe ein leeres
  Feld.
- **Kein `Set-StrictMode`.** Der Transport lebt davon, dass ein fehlendes Feld
  `$null` ist (`isError`, `structuredContent.record`).
- **Kein Modul-Manifest, kein Installer.** Eine `.psm1`, vier `.ps1` und eine
  optionale `.psd1` sind kopierbar; mehr wäre Ballast.
- **`@(…)` um jedes gezählte Ergebnis.** PowerShell 5.1 packt eine
  einelementige Liste aus; `.Count` ist danach leer statt `1`.
- **Kein Alias `-Limit` an `Invoke-Lauf.ps1`.** Die Vorfassung trug ihn für
  alte Aufrufe; dieses Paket hat keine.

## Stolperfallen

- **PAD ersetzt jedes Prozent-Paar im Skripttext — auch in Kommentaren** — und
  meldet „Syntaxfehler", wenn darin keine bekannte Flow-Variable steht. Das
  Prüfmuster für nicht ersetzte Platzhalter wird deshalb mit `[char]37`
  **berechnet**, nie hingeschrieben. Ein Test wacht darüber.
- **PAD-Platzhalter gehören in einfach quotierte Here-Strings.** Eine
  Oberflächen-Meldung darf Anführungszeichen und Zeilenumbrüche tragen.
- **`Mock -ModuleName`.** Die Mocks laufen in der Sitzung des Moduls, die
  Tests in ihrer eigenen. Gemeinsamer Zustand geht über `$global:JnptTest`
  (`tests/TestHelfer.ps1`).
- **Skripte testen, die ihr Modul selbst neu laden.** `Invoke-Lauf.ps1` und
  `pad/*.ps1` rufen `Import-Module … -Force`. Die Tests mocken `Import-Module`
  (ohne `-ModuleName`) als No-Op und fahren das Skript per Punkt-Operator. Ein
  Mock einer exportierten Funktion, die das Skript direkt ruft (`New-JnptLauf`),
  braucht ebenfalls kein `-ModuleName`; eine Funktion, die das Modul intern
  ruft (`Get-JnptTokenFromCredentialManager`), braucht es.
- **`Get-JnptTokenFromCredentialManager` ist Windows-only** (P/Invoke auf
  `advapi32`). Die Tests rufen sie nie echt auf.
- **Die Ären-Falle:** den Header `Mcp-Protocol-Version` nicht setzen. Ein Test
  hält das fest.
- **Die Sperrliste der Pilot-Kennungen erweitern:** SHA-256 des
  kleingeschriebenen Worts berechnen und in `tests/Quelltext.Tests.ps1`
  eintragen — das Wort selbst gehört in keine Datei dieses Repos.

## Verwandt

`README.md` (Betreiber) · `pad/FLOW-VORLAGE.md` (Schrittfolge in PAD-Begriffen)
· Rezept „Zahlungseingang" in `januaport-recipes` (Satzart und Use Case) ·
Workflow `.github/workflows/kartei-rpa.yml`.
