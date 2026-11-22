# Changelog — `kartei-rpa`

## Unveröffentlicht

- **PAD-Schritt 2 meldet einen vergebenen Satz (JanuaPort/januaport#1545).**
  `pad/2-satz-sperren.ps1` gibt, wenn `Lock-JnptSatz` `$null` liefert (ein
  paralleler Lauf hat den Satz reserviert, #1540), genau eine Zeile
  `{"id":"<SatzId>","vergeben":true}` aus, ohne Fehler; bisher endete der Schritt
  mit einem Parameter-Bindungsfehler. Im Normalfall trägt die Ausgabe zusätzlich
  `"vergeben":false`. `pad/FLOW-VORLAGE.md` bekommt dafür den Schritt 6c′:
  `Aktuell.vergeben` = `true` → Nächstes Schleifenelement, ohne Fehlerblock und
  ohne `Fehlerhaft`-Zähler. **Betreiber bestehender Flows:** Schritt 6c′ nach der
  JSON-Konvertierung von Schritt 2 einfügen, sonst läuft ein vergebener Satz in
  die Klickfolge; die Ausgabe von Schritt 2 hat jetzt ein Feld mehr.

- **Sperre mit Vorbedingung (JanuaPort/januaport#1540).** `Lock-JnptSatz` schickt
  mit dem `update` `erwartet = {status: geprueft}`. Hat ein gleichzeitiger Lauf
  den Satz zuerst gesperrt, antwortet der Kern mit `Konflikt: Vorbedingung …`
  und schreibt nichts; der Satz gilt als vergeben: Log-Zeile, Rückgabe `$null`,
  keine Buchung, kein Fehlerstatus. `Invoke-Lauf.ps1` überspringt ihn und zählt
  ihn (`n uebersprungen`). Andere Fehler wirken wie bisher.
  `Set-JnptSatzErgebnis` und `Set-JnptSatzFehler` erwarten `status = in_buchung`
  und die eigene Lauf-Kennung; ein Konflikt dort bricht ab.
  **Mindest-Kernversion: ein Kern mit #1540.** Ein älterer Kern lehnt `erwartet`
  ab, es wird nichts geschrieben (fail-closed); das Modul prüft die Version nicht.
  Im PAD-Flow behandelt das seit #1545 der Schritt 2 (siehe oben).
