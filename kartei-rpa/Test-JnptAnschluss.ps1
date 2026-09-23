# JanuaPort kartei-rpa - Anschluss-Pruefung auf dem Roboter-Rechner.
#
# Wofuer: VOR dem Flow-Bau feststellen, ob dieser Rechner die Anlage erreicht -
# und wenn nicht, WELCHER Handgriff fehlt. Vier Stufen bauen aufeinander auf;
# die oberste rote ist die Ursache, alles darunter wird nicht versucht.
#
# Warum es das gibt: Ein Flow, dessen erste Anfrage im Netz stirbt, sieht in
# PAD aus wie ein Flow, der durchgelaufen ist - der Fehler steht in
# ScriptError, PAD macht weiter. Im Audit der Anlage steht dann NICHTS, und
# ein leeres Audit heisst "hier ist nichts angekommen", nicht "es ist nichts
# passiert".
#
# Beispiele:
#   .\Test-JnptAnschluss.ps1 -Endpoint https://example.com/mcp
#   .\Test-JnptAnschluss.ps1 -Endpoint https://example.com/mcp -ErwarteteIP 192.0.2.10
#
# Das Skript liest NUR: Es ruft ping auf (das Diagnose-Werkzeug, das jeder
# gueltige Zugang aufrufen darf) und fasst keinen Satz an.

[CmdletBinding()]
param(
    # Der Endpunkt, genau wie ihn der Flow benutzt - mit /mcp, ohne
    # Schraegstrich am Ende.
    [Parameter(Mandatory = $true)][string]$Endpoint,

    # Optional: die erwartete Adresse der Anlage. Weicht die Aufloesung ab,
    # ist der hosts- oder DNS-Eintrag die Ursache.
    [string]$ErwarteteIP,

    # Name des Credential-Manager-Eintrags. Ohne Angabe gilt die
    # Einstellungsdatei neben dem Modul bzw. die Voreinstellung.
    [string]$CredentialTarget,

    # Ordner mit JanuaPort.Kartei.psm1 (fuer Stufe 4).
    [string]$Modulpfad = $PSScriptRoot
)

$ErrorActionPreference = 'Stop'
$script:rot = 0

function Write-Stufe($nummer, $titel) { Write-Host "`n[$nummer] $titel" }
function Write-Gruen($text) { Write-Host "     OK     $text" -ForegroundColor Green }
function Write-Rot($text, $rat) {
    Write-Host "     FEHLER $text" -ForegroundColor Red
    Write-Host "     -> $rat" -ForegroundColor Yellow
    $script:rot++
}
function Test-Uebersprungen {
    if ($script:rot -eq 0) { return $false }
    Write-Host '     uebersprungen (eine Stufe darueber ist rot)' -ForegroundColor DarkGray
    return $true
}

$uri = [Uri]$Endpoint
$wirt = $uri.Host

Write-Host 'JanuaPort - Anschluss-Pruefung'
Write-Host "Endpunkt: $Endpoint"
Write-Host "Konto:    $env:USERDOMAIN\$env:USERNAME"

# --- 1. Namensaufloesung -----------------------------------------------------
# Eine nackte IP im Endpunkt ist ein eigener Fehler: Ohne Namen sendet der
# Client keine SNI (RFC 6066), und das Zertifikat traegt in der Regel nur
# einen DNS-Namen.
Write-Stufe 1 'Namensaufloesung'
if ($wirt -as [ipaddress]) {
    Write-Rot "Der Endpunkt nennt eine IP-Adresse ($wirt)." `
        'Den Zertifikatsnamen der Anlage eintragen, nicht die IP.'
} else {
    try {
        $adressen = @([System.Net.Dns]::GetHostAddresses($wirt) | ForEach-Object { $_.IPAddressToString })
        Write-Gruen "$wirt loest auf: $($adressen -join ', ')"
        if ($ErwarteteIP -and $adressen -notcontains $ErwarteteIP) {
            Write-Rot "Erwartet war $ErwarteteIP." 'hosts-Eintrag auf diesem Rechner oder DNS-Eintrag im Netz pruefen.'
        }
    } catch {
        Write-Rot "$wirt loest nicht auf." `
            'hosts-Eintrag ergaenzen (C:\Windows\System32\drivers\etc\hosts, als Administrator) oder DNS-Eintrag setzen.'
    }
}

# --- 2. TCP ------------------------------------------------------------------
Write-Stufe 2 'Anlage erreichbar (TCP)'
if (-not (Test-Uebersprungen)) {
    $port = $uri.Port
    $tcp = Test-NetConnection -ComputerName $wirt -Port $port -WarningAction SilentlyContinue
    if ($tcp.TcpTestSucceeded) {
        Write-Gruen "$($wirt):$port nimmt Verbindungen an"
    } else {
        Write-Rot "$($wirt):$port antwortet nicht." 'Firewall, falscher Port, oder die Anlage laeuft nicht.'
    }
}

# --- 3. TLS ------------------------------------------------------------------
# Signiert die Anlage im LAN mit einer eigenen Wurzel, muss diese im
# COMPUTER-Speicher liegen. Sonst lehnt .NET ab und PowerShell meldet nur
# "die zugrunde liegende Verbindung wurde geschlossen".
Write-Stufe 3 'TLS-Vertrauen'
if (-not (Test-Uebersprungen)) {
    try {
        Invoke-WebRequest -Uri $Endpoint -Method Post -TimeoutSec 10 -UseBasicParsing `
            -Headers @{ 'Accept' = 'application/json, text/event-stream' } -ContentType 'application/json' `
            -Body '{"jsonrpc":"2.0","id":1,"method":"ping"}' | Out-Null
        Write-Gruen 'TLS akzeptiert (die Anlage hat ohne Ausweis geantwortet - ungewoehnlich)'
    } catch {
        $antwort = $_.Exception.Response
        if ($antwort) {
            $code = [int]$antwort.StatusCode
            if ($code -eq 401) { Write-Gruen 'TLS akzeptiert, Anlage antwortet mit 401 - richtig so' }
            else { Write-Gruen "TLS akzeptiert (HTTP $code)" }
        } else {
            Write-Rot "Keine TLS-Verbindung: $($_.Exception.Message.Split([char]10)[0])" `
                'Die Wurzel der Anlage in den COMPUTER-Zertifikatspeicher importieren: certlm.msc -> Vertrauenswuerdige Stammzertifizierungsstellen.'
        }
    }
}

# --- 4. Token ----------------------------------------------------------------
# Der Credential-Manager ist BENUTZERgebunden: Laeuft der Flow unter einem
# anderen Konto als cmdkey, findet er den Eintrag nicht - und die Meldung sagt
# nicht "falsches Konto".
Write-Stufe 4 "Token im Credential-Manager (Konto: $env:USERNAME)"
if (-not (Test-Uebersprungen)) {
    $modul = Join-Path $Modulpfad 'JanuaPort.Kartei.psm1'
    if (-not (Test-Path -LiteralPath $modul)) {
        Write-Rot "JanuaPort.Kartei.psm1 nicht gefunden unter $Modulpfad." '-Modulpfad auf den Ordner mit dem Modul setzen.'
    } else {
        Import-Module $modul -Force
        if ([string]::IsNullOrWhiteSpace($CredentialTarget)) {
            $CredentialTarget = (Get-JnptKarteiEinstellungen -Pfad (Join-Path $Modulpfad 'kartei-rpa.einstellungen.psd1')).CredentialTarget
        }
        $token = $null
        try {
            $token = Get-JnptTokenFromCredentialManager -Ziel $CredentialTarget
            Write-Gruen "Eintrag '$CredentialTarget' gefunden"
        } catch {
            Write-Rot "Kein Eintrag '$CredentialTarget' fuer dieses Konto." `
                "cmdkey /generic:$CredentialTarget /user:jnpt /pass  - UNTER DEM KONTO, unter dem der Flow laeuft."
        }
        if ($token) {
            try {
                # Connect-Jnpt liefert die Protokollfassung, Invoke-JnptTool
                # nimmt Endpoint/Token/Tool einzeln - kein Sitzungsobjekt.
                $fassung = Connect-Jnpt -Endpoint $Endpoint -Token $token
                $null = Invoke-JnptTool -Endpoint $Endpoint -Token $token -Tool 'ping'
                Write-Gruen "ping erfolgreich (Protokollfassung $fassung)"
                Write-Host "`n     Gegenprobe im Audit der Anlage: eine ping-Zeile mit diesem Zugang." -ForegroundColor Cyan
            } catch {
                Write-Rot "Token abgelehnt oder Aufruf gescheitert: $($_.Exception.Message.Split([char]10)[0])" `
                    'Token widerrufen oder abgelaufen? In der Anlage: jnpt token list.'
            }
        }
    }
}

Write-Host ''
if ($script:rot -eq 0) {
    Write-Host 'ALLE VIER STUFEN GRUEN - dieser Rechner kann Flows bauen.' -ForegroundColor Green
    exit 0
}
Write-Host "$($script:rot) Stufe(n) rot - die OBERSTE rote Stufe ist die Ursache." -ForegroundColor Red
exit 1
