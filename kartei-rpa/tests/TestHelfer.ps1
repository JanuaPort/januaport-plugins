# JanuaPort kartei-rpa - gemeinsame Testhelfer (JanuaPort/januaport#806).
#
# Wird von den *.Tests.ps1 im BeforeAll per Punkt-Operator geladen. Kein
# eigener Test (der Name endet nicht auf .Tests.ps1).
#
# Der Transport ist gemockt: Es geht nie eine echte HTTP-Anfrage raus. Der Mock
# lebt in der Sitzung des Moduls, die Tests in ihrer eigenen - der gemeinsame
# Zustand liegt deshalb bewusst in $global:JnptTest.
#
# Alle Daten hier sind erfunden - keine Kundendaten, kein echtes Pseudonym.

function New-TestAntwort {
    param(
        [int]$StatusCode = 200,
        [string]$ContentType = 'text/event-stream',
        [string]$Rumpf = ''
    )
    $bytes = [Text.Encoding]::UTF8.GetBytes($Rumpf)
    [pscustomobject]@{
        StatusCode       = $StatusCode
        Headers          = @{ 'Content-Type' = $ContentType }
        RawContentStream = New-Object System.IO.MemoryStream(, $bytes)
    }
}

function New-TestSse {
    param([string]$Json)
    return "event: message`ndata: $Json`n`n"
}

function Add-TestErgebnis {
    param([hashtable]$Result)
    $rpc = @{ jsonrpc = '2.0'; id = 1; result = $Result }
    $a = New-TestAntwort -Rumpf (New-TestSse (ConvertTo-Json $rpc -Depth 12 -Compress))
    $global:JnptTest.Antworten.Add($a) | Out-Null
}

function Add-TestHandschlag {
    Add-TestErgebnis @{ protocolVersion = '2025-11-25' }
    $global:JnptTest.Antworten.Add((New-TestAntwort -StatusCode 202 -ContentType 'text/plain')) | Out-Null
}

function Add-TestGet {
    param($Satz)
    Add-TestErgebnis @{ structuredContent = @{ record = $Satz } }
}

function Add-TestSearch {
    param($Saetze = @())
    Add-TestErgebnis @{ structuredContent = @{ records = @($Saetze) } }
}

function Add-TestUpdate {
    param($Satz)
    Add-TestErgebnis @{ structuredContent = @{ record = $Satz } }
}

function New-TestSatz {
    # Die Felder der generischen Satzart "zahlungseingang" aus dem Rezept.
    param([string]$Id = 'a1b2c3d4', [hashtable]$Felder = @{})
    $f = [ordered]@{
        transaktionsschluessel = 'MUSTER-2026-09-01'
        kontoauszug            = 'auszug-2026-09-01.xml'
        eintrag_index          = 1
        buchungsdatum          = '2026-09-01'
        betrag                 = '980.00'
        waehrung               = 'EUR'
        gegenpartei            = '<name-9d3a77b4>'
        verwendungszweck       = 'RE 47110'
        status                 = 'geprueft'
        trefferart             = 'eindeutig'
        nummer_art             = 'rechnung'
        beleg_nummer           = '47110'
        buchungs_nr            = ''
        roboter_lauf           = ''
        fehler_text            = ''
        bearbeitet_am          = ''
        notiz                  = ''
    }
    foreach ($k in $Felder.Keys) { $f[$k] = $Felder[$k] }
    return @{ id = $Id; fields = $f }
}

function New-TestSitzung {
    # Dieselbe Form, die Connect-JnptKartei liefert - mit den Voreinstellungen.
    param([string]$Quelle = 'roboter:buchung', [hashtable]$Felder = @{}, [string[]]$Ausgabefelder)
    $f = @{
        Buchungsnr   = 'buchungs_nr'
        Lauf         = 'roboter_lauf'
        FehlerText   = 'fehler_text'
        BearbeitetAm = 'bearbeitet_am'
    }
    foreach ($k in $Felder.Keys) { $f[$k] = $Felder[$k] }
    if (-not $Ausgabefelder) { $Ausgabefelder = @('beleg_nummer', 'nummer_art', 'betrag', 'buchungsdatum') }
    [pscustomobject]@{
        Endpoint         = 'https://example.com/mcp'
        Token            = 'test-token-ohne-praefix'
        Satzart          = 'zahlungseingang'
        Protokollfassung = '2025-11-25'
        TimeoutSec       = 60
        Quelle           = $Quelle
        Felder           = $f
        Ausgabefelder    = $Ausgabefelder
    }
}

function Get-TestAufrufe {
    # Das Komma haelt das Array am Leben: PowerShell packt sonst eine
    # einelementige Liste aus, und .Count ist danach leer statt 1.
    param([string]$Werkzeug)
    $treffer = @($global:JnptTest.Aufrufe | Where-Object {
        $_.Body.method -eq 'tools/call' -and $_.Body.params.name -eq $Werkzeug
    })
    return , $treffer
}

function Reset-TestTransport {
    $global:JnptTest = @{
        Antworten = New-Object System.Collections.ArrayList
        Aufrufe   = New-Object System.Collections.ArrayList
        Wirft     = $null
    }
}

function Register-TestTransport {
    # Muss aus einem BeforeAll heraus gerufen werden (Mock gilt fuer den Block).
    Mock -ModuleName JanuaPort.Kartei -CommandName Invoke-WebRequest -MockWith {
        $roh = if ($Body -is [byte[]]) { [Text.Encoding]::UTF8.GetString($Body) } else { [string]$Body }
        $global:JnptTest.Aufrufe.Add([pscustomobject]@{
            Uri     = $Uri
            Headers = $Headers
            Body    = (ConvertFrom-Json $roh)
        }) | Out-Null
        if ($global:JnptTest.Wirft) { throw $global:JnptTest.Wirft }
        if ($global:JnptTest.Antworten.Count -eq 0) {
            throw 'Test: die Antwort-Warteschlange ist leer - der Aufruf war nicht vorgesehen.'
        }
        $a = $global:JnptTest.Antworten[0]
        $global:JnptTest.Antworten.RemoveAt(0)
        return $a
    }
}
