# JanuaPort kartei-rpa - PAD-Aktion 4 von 4: den Satz auf fehler setzen.
#
# Steht im Fehlerblock ("Bei Fehler") um die Klickfolge im Fachsystem. Der
# Platzhalter Schritt ist der Name der Aktion, die gescheitert ist, Meldung
# die Fehlermeldung von PAD. Danach geht der Flow zum naechsten Satz - der
# Lauf endet nicht, aber das Fachsystem bleibt fuer diesen Beleg unangetastet.
#
# ACHTUNG: KEINE personenbezogenen Daten in die Meldung. Schrittname und
# Oberflaechen-Meldung genuegen ("Nummer nicht gefunden", "Nummer
# mehrdeutig"). Das Modul bringt den Text auf eine Zeile und deckelt ihn auf
# 200 Zeichen - das ist eine Bremse, kein Freibrief.
#
# Timeout der Aktion: 120 Sekunden. "Bei Fehler wiederholen": AUS.
#
# Ausgabe (PowershellOutput): {"id":"...","status":"fehler"}

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
$Schritt = @'
%Schritt%
'@
$Meldung = @'
%Meldung%
'@

# Schritt und Meldung sind absichtlich nicht in der Pruefung: Eine echte
# Oberflaechen-Meldung darf Prozentzeichen tragen.
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
$r = Set-JnptSatzFehler -Session $jnpt -Id $SatzId.Trim() -Lauf $Lauf.Trim() `
        -Schritt $Schritt.Trim() -Meldung $Meldung.Trim()

Write-Output (ConvertTo-Json ([ordered]@{ id = $r.id; status = $r.status }) -Depth 5 -Compress)
