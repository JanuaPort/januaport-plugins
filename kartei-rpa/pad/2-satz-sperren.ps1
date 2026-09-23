# JanuaPort kartei-rpa - PAD-Aktion 2 von 4: einen Satz sperren.
#
# Erste Aktion INNERHALB der Schleife (For each ueber saetze). Setzt den Satz
# von geprueft auf in_buchung und schreibt die Lauf-Kennung hinein - der
# Status IST die Sperre, es gibt kein If-Match.
#
# Timeout der Aktion: 120 Sekunden. "Bei Fehler wiederholen": AUS.
#
# Ausgabe (PowershellOutput), eine Zeile JSON - frisch aus get, damit die
# Klickfolge mit dem arbeitet, was JETZT dasteht:
#   {"id":"...", <Ausgabefelder>}
#
# Schlaegt fehl (ScriptError), wenn der Satz nicht mehr auf geprueft steht -
# dann hat jemand dazwischen gefunkt. Der Flow beendet den Lauf.

$ErrorActionPreference = 'Stop'

$Modulpfad = @'
%Modulpfad%
'@
$Endpoint = @'
%Endpoint%
'@
$Satzart = @'
%Satzart%
'@
$SatzId = @'
%SatzId%
'@
$Lauf = @'
%Lauf%
'@

foreach ($p in @($Modulpfad, $Endpoint, $Satzart, $SatzId, $Lauf)) {
    # Das Muster wird BERECHNET statt hingeschrieben: PAD ersetzt jedes
    # Prozent-Paar im Skripttext vor der Ausfuehrung - AUCH IN KOMMENTAREN -
    # und meldet "Syntaxfehler", wenn darin keine bekannte Flow-Variable
    # steht. Hingeschrieben macht ausgerechnet diese Pruefung die Datei in
    # PAD unbrauchbar.
    if ($p.Trim() -like ([char]37 + '*' + [char]37)) {
        throw 'Platzhalter nicht ersetzt - dieses Skript laeuft nur als PAD-Aktion. Ohne PAD: Invoke-Lauf.ps1.'
    }
}

Import-Module (Join-Path $Modulpfad.Trim() 'JanuaPort.Kartei.psm1') -Force

$jnpt = Connect-JnptKartei -Endpoint $Endpoint.Trim() -Satzart $Satzart.Trim()
$satz = Lock-JnptSatz -Session $jnpt -Id $SatzId.Trim() -Lauf $Lauf.Trim()

Write-Output (ConvertTo-Json (ConvertTo-JnptAusgabe -Session $jnpt -Satz $satz) -Depth 5 -Compress)
