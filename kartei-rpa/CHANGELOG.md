# Changelog — `kartei-rpa`

## Unveröffentlicht

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
  **Offen:** `pad/2-satz-sperren.ps1` fragt `$null` nicht ab; im PAD-Flow endet
  der Schritt bei einem vergebenen Satz mit einem Skriptfehler (gebucht wird
  nichts).
