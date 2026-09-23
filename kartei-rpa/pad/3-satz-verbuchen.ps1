# JanuaPort kartei-rpa - PAD-Aktion 3 von 4: den Satz auf verbucht setzen.
#
# Letzte Aktion in der Schleife, NACH der erfolgreichen Klickfolge im
# Fachsystem. Der Platzhalter Buchungsnr ist die Nummer, die das Fachsystem
# nach dem Buchen anzeigt - die Klickfolge liest sie aus und reicht sie als
# Flow-Variable hierher.
#
# Timeout der Aktion: 120 Sekunden. "Bei Fehler wiederholen": AUS - ein
# wiederholter Schreibschritt ist genau das, was hier nie passieren darf.
#
# Ausgabe (PowershellOutput): {"id":"...","status":"verbucht"}
#
# Schlaegt fehl (ScriptError), wenn der Satz nicht auf in_buchung steht, zu
# einem fremden Lauf gehoert oder schon eine Buchungsnummer traegt. In allen
# drei Faellen wird NICHT geschrieben.

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
$Buchungsnr = @'
%Buchungsnr%
'@

foreach ($p in @($Modulpfad, $Endpoint, $Satzart, $SatzId, $Lauf, $Buchungsnr)) {
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
$r = Set-JnptSatzErgebnis -Session $jnpt -Id $SatzId.Trim() -Lauf $Lauf.Trim() -Buchungsnr $Buchungsnr.Trim()

Write-Output (ConvertTo-Json ([ordered]@{ id = $r.id; status = $r.status }) -Depth 5 -Compress)
