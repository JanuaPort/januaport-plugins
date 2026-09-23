# JanuaPort kartei-rpa - PAD-Aktion 1 von 4: die Arbeitsliste holen.
#
# Inhalt der PAD-Aktion "PowerShell-Skript ausfuehren" (Run PowerShell script).
# Die Platzhalter in Prozentzeichen sind PAD-Variablen und werden VOR der
# Ausfuehrung ersetzt. Der Token steht bewusst NICHT darunter - er kommt aus
# dem Windows-Credential-Manager (README).
#
# Timeout der Aktion: 120 Sekunden (der Default 10 reisst den Lauf).
# "Bei Fehler wiederholen": AUS.
#
# Ausgabe (PowershellOutput), eine Zeile JSON:
#   {"lauf":"...","anzahl":2,"saetze":[{"id":"...", <Ausgabefelder>}]}
# Ausgabefelder sind per Voreinstellung beleg_nummer, nummer_art, betrag,
# buchungsdatum - genau die Werte, die die Klickfolge im Fachsystem tippt.
# Fehler landen in ScriptError; der Flow prueft ScriptError nach JEDER Aktion.
#
# Diese Aktion bricht ab, wenn schon ein Satz auf in_buchung steht: Dann ist
# ein frueherer Lauf mittendrin gestorben, und ein Mensch muss im Fachsystem
# nachsehen. Das ist die Bremse, kein Schaden.

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

foreach ($p in @($Modulpfad, $Endpoint, $Satzart)) {
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

$jnpt  = Connect-JnptKartei -Endpoint $Endpoint.Trim() -Satzart $Satzart.Trim()
$lauf  = New-JnptLauf
$liste = @(Get-JnptArbeitsliste -Session $jnpt)
$saetze = @(foreach ($s in $liste) { ConvertTo-JnptAusgabe -Session $jnpt -Satz $s })

Write-Output (ConvertTo-Json ([ordered]@{
    lauf   = $lauf
    anzahl = $saetze.Count
    saetze = $saetze
}) -Depth 5 -Compress)
