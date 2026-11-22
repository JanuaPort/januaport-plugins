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

function Get-TestKonfliktText {
    # Der feste Satz des Kerns (#1540), ohne Werte. Die Umlaute kommen aus
    # Zeichencodes: Quelltext dieses Ordners ist ASCII.
    param([string]$Feld = 'status')
    return 'Konflikt: Vorbedingung f{0}r Feld "{1}" nicht erf{2}llt.' -f [char]0xFC, $Feld, [char]0xFC
}

function Add-TestKonflikt {
    # Antwort des Kerns, wenn erwartet nicht mehr stimmt: ein Werkzeugfehler.
    param([string]$Feld = 'status')
    Add-TestErgebnis @{ isError = $true; content = @(@{ type = 'text'; text = (Get-TestKonfliktText -Feld $Feld) }) }
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
        Kern      = $null
    }
}

function New-TestKern {
    # Ein kleiner Kern im Speicher, der erwartet WIRKLICH prueft (#1540): Er
    # haelt die Felder je Satz und weist ein update ab, dessen erwartet nicht
    # zum gespeicherten Stand passt. Eine feste Antwort-Warteschlange kann das
    # nicht - sie lieferte den Konflikt auch dann, wenn das Modul erwartet gar
    # nicht schickt, und ein Mutant ohne erwartet bliebe unentdeckt.
    # VorUpdate laeuft einmal vor dem ersten update und ist der gleichzeitige
    # Roboter; Updates zaehlt die angenommenen Schreibvorgaenge.
    # Der Handler ist selbsttragend: Er laeuft im Mock, also in der Sitzung des
    # Moduls, und sieht die Helfer dieser Datei nicht.
    param([hashtable[]]$Saetze, [scriptblock]$VorUpdate)
    $store = @{}
    foreach ($s in $Saetze) {
        $store[$s.id] = [ordered]@{}
        foreach ($k in $s.fields.Keys) { $store[$s.id][$k] = $s.fields[$k] }
    }
    $handler = {
        param($Body, $Kern)
        $antwort = {
            param($Status, $Result)
            $rpc = @{ jsonrpc = '2.0'; id = 1; result = $Result }
            $bytes = [Text.Encoding]::UTF8.GetBytes("event: message`ndata: $(ConvertTo-Json $rpc -Depth 12 -Compress)`n`n")
            [pscustomobject]@{
                StatusCode       = $Status
                Headers          = @{ 'Content-Type' = 'text/event-stream' }
                RawContentStream = New-Object System.IO.MemoryStream(, $bytes)
            }
        }
        if ($Body.method -eq 'initialize') { return (& $antwort 200 @{ protocolVersion = '2025-11-25' }) }
        if ($Body.method -ne 'tools/call') {
            return [pscustomobject]@{ StatusCode = 202; Headers = @{ 'Content-Type' = 'text/plain' }
                                      RawContentStream = New-Object System.IO.MemoryStream(, [byte[]]@()) }
        }
        $arg = $Body.params.arguments
        if ($Body.params.name -like '*_get') {
            return (& $antwort 200 @{ structuredContent = @{ record = @{ id = $arg.id; fields = $Kern.Store[$arg.id] } } })
        }
        if ($Kern.VorUpdate) {
            $vor = $Kern.VorUpdate
            $Kern.VorUpdate = $null
            & $vor
        }
        $stand = $Kern.Store[$arg.id]
        if ($null -ne $arg.erwartet) {
            foreach ($p in $arg.erwartet.PSObject.Properties) {
                if (([string]$stand[$p.Name]).Trim() -ne ([string]$p.Value).Trim()) {
                    $text = 'Konflikt: Vorbedingung f{0}r Feld "{1}" nicht erf{0}llt.' -f [char]0xFC, $p.Name
                    return (& $antwort 200 @{ isError = $true; content = @(@{ type = 'text'; text = $text }) })
                }
            }
        }
        $Kern.Updates++
        foreach ($p in $arg.PSObject.Properties) {
            if ($p.Name -notin @('id', 'source', 'as_of', 'erwartet')) { $stand[$p.Name] = $p.Value }
        }
        return (& $antwort 200 @{ structuredContent = @{ record = @{ id = $arg.id; fields = $stand } } })
    }
    $global:JnptTest.Kern = @{ Store = $store; VorUpdate = $VorUpdate; Updates = 0; Handler = $handler }
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
        if ($global:JnptTest.Kern) {
            return (& $global:JnptTest.Kern.Handler $global:JnptTest.Aufrufe[-1].Body $global:JnptTest.Kern)
        }
        if ($global:JnptTest.Antworten.Count -eq 0) {
            throw 'Test: die Antwort-Warteschlange ist leer - der Aufruf war nicht vorgesehen.'
        }
        $a = $global:JnptTest.Antworten[0]
        $global:JnptTest.Antworten.RemoveAt(0)
        return $a
    }
}
