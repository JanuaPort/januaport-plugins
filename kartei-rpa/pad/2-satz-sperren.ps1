# JanuaPort kartei-rpa - PAD-Aktion 2 von 4: einen Satz sperren.
#
# Erste Aktion INNERHALB der Schleife (For each ueber saetze). Setzt den Satz
# von geprueft auf in_buchung und schreibt die Lauf-Kennung hinein - der
# Status IST die Sperre, es gibt kein If-Match.
#
# Timeout der Aktion: 120 Sekunden. "Bei Fehler wiederholen": AUS.
#
# Ausgabe (PowershellOutput), genau eine Zeile JSON - frisch aus get, damit die
# Klickfolge mit dem arbeitet, was JETZT dasteht:
#   {"id":"...", <Ausgabefelder>, "vergeben":false}
# Das Feld vergeben steht immer da, damit der Flow es immer findet.
#
# Fall "vergeben": Hat ein gleichzeitiger Lauf den Satz zuerst gesperrt
# (Vorbedingung am update, Kern #1540), liefert Lock-JnptSatz nichts. Dann gibt
# das Skript genau eine Zeile aus, ohne Fehler und ohne Schreibzugriff:
#   {"id":"...","vergeben":true}
# Der Flow springt zum naechsten Satz - kein Buchen, kein Fehlerstatus, kein
# Aufruf von Aktion 4. Die Log-Zeile des Moduls wird unterdrueckt, damit auf
# stdout nur diese eine Zeile steht.
#
# Schlaegt sonst fehl (ScriptError), wenn der Satz nicht mehr auf geprueft steht
# oder schon eine Buchungsnummer traegt - dann hat jemand dazwischen gefunkt,
# oder der Satz ist schon gebucht. Es wird nichts geschrieben; der Flow
# beendet den Lauf.

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
# 6>$null: Lock-JnptSatz meldet "vergeben" per Write-Host; die Zeile soll nicht
# vor das JSON auf stdout geraten.
$satz = Lock-JnptSatz -Session $jnpt -Id $SatzId.Trim() -Lauf $Lauf.Trim() 6>$null

if ($null -eq $satz) {
    Write-Output (ConvertTo-Json ([ordered]@{ id = $SatzId.Trim(); vergeben = $true }) -Compress)
    return
}

$ausgabe = ConvertTo-JnptAusgabe -Session $jnpt -Satz $satz
$ausgabe['vergeben'] = $false
Write-Output (ConvertTo-Json $ausgabe -Depth 5 -Compress)
