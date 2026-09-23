# JanuaPort kartei-rpa - ein vollstaendiger Lauf OHNE RPA-Werkzeug.
#
# Wofuer: den Durchstich pruefen, bevor ein Flow gebaut wird - und einen Lauf
# von Hand nachfahren, wenn der Flow streikt. Im Betrieb lebt die Schleife im
# Flow (pad/FLOW-VORLAGE.md); dieses Skript ist dieselbe Schleife am Stueck,
# mit einem PLATZHALTER statt der Klickfolge im Fachsystem.
#
# Ohne -Hoechstens arbeitet es ALLE freigegebenen Saetze ab, in so vielen
# Abfrage-Runden wie noetig. -Seitengroesse ist nur die Groesse einer Abfrage,
# kein Lauf-Deckel.
#
# Beispiele:
#   .\Invoke-Lauf.ps1 -Endpoint https://example.com/mcp
#   .\Invoke-Lauf.ps1 -Endpoint https://example.com/mcp -Hoechstens 1
#       Probelauf: bucht genau einen Satz, laesst den Rest auf geprueft.
#
# Ohne -Token kommt der Token aus dem Windows-Credential-Manager. -Token ist
# fuer einen Handlauf und landet in der Kommandozeilen-Historie - im Betrieb
# nicht verwenden. Feldnamen und Quelle kommen aus der optionalen
# Einstellungsdatei neben dem Modul (README).
#
# Das Protokoll traegt Zaehler und Kennungen, KEINE Satzinhalte.

[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$Endpoint,
    [string]$Satzart = 'zahlungseingang',
    [string]$CredentialTarget,
    [string]$Token,

    # Wie viele Saetze je Abfrage geholt werden - der Lauf arbeitet trotzdem
    # ALLE freigegebenen Saetze ab.
    [ValidateRange(1, 100)][int]$Seitengroesse = 100,

    # Echter Lauf-Deckel: bricht ab, sobald so viele Saetze verbucht ODER auf
    # fehler gesetzt wurden. 0 heisst kein Deckel.
    [ValidateRange(0, [int]::MaxValue)][int]$Hoechstens = 0
)

$ErrorActionPreference = 'Stop'
Import-Module (Join-Path $PSScriptRoot 'JanuaPort.Kartei.psm1') -Force

function Invoke-Klickfolge {
    # PLATZHALTER fuer die Klickfolge im Fachsystem. Im Flow steht hier eine
    # Folge von UI-Aktionen, kein PowerShell - die nimmt ein Mensch vor Ort
    # auf. Fuer einen Probelauf liefert die Funktion eine erfundene
    # Buchungsnummer; wirft sie, laeuft der fehler-Zweig.
    param($Satz)
    return 'BU-{0}' -f $Satz.id
}

$verbindung = @{ Endpoint = $Endpoint; Satzart = $Satzart }
if (-not [string]::IsNullOrWhiteSpace($CredentialTarget)) { $verbindung['CredentialTarget'] = $CredentialTarget }
if (-not [string]::IsNullOrWhiteSpace($Token)) { $verbindung['Token'] = $Token }

$jnpt = Connect-JnptKartei @verbindung
$lauf = New-JnptLauf
Write-Host ('verbunden, Protokollfassung {0}, Lauf {1}' -f $jnpt.Protokollfassung, $lauf)

$gebucht = 0
$fehler = 0

# In Runden holen, bis leer: Die Suche liefert hoechstens 100 Saetze und ist
# kein Vollstaendigkeitsbeweis. Jeder geholte Satz verlaesst dabei den Zustand
# geprueft, deshalb endet die Schleife. -Hoechstens ist der einzige Ausstieg
# davor - auch mitten in einer Charge.
:aussen while ($true) {
    $treffer = @(Get-JnptArbeitsliste -Session $jnpt -Limit $Seitengroesse)
    if ($treffer.Count -eq 0) { break }

    foreach ($t in $treffer) {
        # Wirft Lock-JnptSatz, endet der Lauf - gewollt: Dann hat jemand
        # dazwischen gefunkt, und ein Mensch sieht nach.
        $satz = Lock-JnptSatz -Session $jnpt -Id $t.id -Lauf $lauf

        try {
            $buchungsnr = Invoke-Klickfolge -Satz $satz
        } catch {
            Set-JnptSatzFehler -Session $jnpt -Id $t.id -Lauf $lauf `
                -Schritt 'Klickfolge' -Meldung $_.Exception.Message | Out-Null
            $fehler++
            if ($Hoechstens -gt 0 -and ($gebucht + $fehler) -ge $Hoechstens) { break aussen }
            continue
        }

        Set-JnptSatzErgebnis -Session $jnpt -Id $t.id -Lauf $lauf -Buchungsnr $buchungsnr | Out-Null
        $gebucht++
        if ($Hoechstens -gt 0 -and ($gebucht + $fehler) -ge $Hoechstens) { break aussen }
    }
}

Write-Host ('Lauf {0} fertig: {1} verbucht, {2} auf fehler.' -f $lauf, $gebucht, $fehler)
