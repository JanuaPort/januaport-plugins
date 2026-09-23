# JanuaPort kartei-rpa - Tests fuer die vier PAD-Skripte (JanuaPort/januaport#806).
#
# Jedes Skript wird so gefahren, wie PAD es faehrt: Die Platzhalter werden im
# Skripttext ersetzt, dann laeuft der Text. Der Token kommt aus einem Mock des
# Credential-Managers, der Transport ist gemockt (TestHelfer.ps1).
#
# Das Prozentzeichen steht in diesem Test nur als [char]37 - dieselbe Vorsicht
# wie in den Skripten, damit niemand den Text einer Testdatei in PAD kopiert
# und ueber ein Muster stolpert.

BeforeAll {
    $script:Wurzel = Split-Path -Parent $PSScriptRoot
    $script:PadOrdner = Join-Path $script:Wurzel 'pad'
    Import-Module (Join-Path $script:Wurzel 'JanuaPort.Kartei.psm1') -Force
    $script:P = [string][char]37
}

AfterAll {
    Remove-Module JanuaPort.Kartei -Force -ErrorAction SilentlyContinue
}

Describe 'PAD-Skripte' {

    BeforeAll {
        . (Join-Path $PSScriptRoot 'TestHelfer.ps1')
        Register-TestTransport

        $script:Bekannt = @('Modulpfad', 'Endpoint', 'Satzart', 'SatzId', 'Lauf', 'Buchungsnr', 'Schritt', 'Meldung')

        function Invoke-TestPad {
            # Ersetzt die Platzhalter wie PAD und fuehrt den Text aus.
            param([string]$Name, [hashtable]$Werte)
            $text = [System.IO.File]::ReadAllText((Join-Path $script:PadOrdner $Name))
            foreach ($k in $Werte.Keys) { $text = $text.Replace($script:P + $k + $script:P, $Werte[$k]) }
            $ziel = Join-Path $TestDrive $Name
            [System.IO.File]::WriteAllText($ziel, $text)
            return (. $ziel)
        }

        function Get-TestWerte {
            param([hashtable]$Mehr = @{})
            $w = @{ Modulpfad = $script:Wurzel; Endpoint = 'https://example.com/mcp'; Satzart = 'zahlungseingang' }
            foreach ($k in $Mehr.Keys) { $w[$k] = $Mehr[$k] }
            return $w
        }

        Mock -CommandName Import-Module -MockWith { }
        Mock -CommandName New-JnptLauf -MockWith { 'TEST-LAUF' }
        Mock -ModuleName JanuaPort.Kartei -CommandName Get-JnptTokenFromCredentialManager -MockWith { 'test-token' }
    }

    BeforeEach { Reset-TestTransport }

    AfterAll {
        Remove-Variable -Name JnptTest -Scope Global -ErrorAction SilentlyContinue
    }

    Context 'Waechter' {

        It '<_> enthaelt nur Prozent-Paare um bekannte PAD-Platzhalter' -ForEach @(
            '1-arbeitsliste.ps1', '2-satz-sperren.ps1', '3-satz-verbuchen.ps1', '4-satz-fehler.ps1'
        ) {
            $text = [System.IO.File]::ReadAllText((Join-Path $script:PadOrdner $_))
            $muster = [regex]::Escape($script:P) + '([^' + [regex]::Escape($script:P) + ']*)' + [regex]::Escape($script:P)
            foreach ($m in [regex]::Matches($text, $muster)) {
                $script:Bekannt | Should -Contain $m.Groups[1].Value
            }
            $rest = [regex]::Replace($text, $muster, '')
            $rest.Contains($script:P) | Should -BeFalse
        }

        It '<_> bricht ohne PAD mit klarer Meldung ab, bevor etwas geladen wird' -ForEach @(
            '1-arbeitsliste.ps1', '2-satz-sperren.ps1', '3-satz-verbuchen.ps1', '4-satz-fehler.ps1'
        ) {
            { & (Join-Path $script:PadOrdner $_) } | Should -Throw '*Platzhalter nicht ersetzt*'
            Should -Invoke -CommandName Import-Module -Times 0 -Exactly
        }
    }

    Context 'Ausgabeformen' {

        It '1-arbeitsliste liefert lauf, anzahl und saetze mit id und Ausgabefeldern' {
            Add-TestHandschlag
            Add-TestSearch @()
            Add-TestSearch @((New-TestSatz -Id 's1'))

            $zeile = Invoke-TestPad -Name '1-arbeitsliste.ps1' -Werte (Get-TestWerte)

            $j = ConvertFrom-Json $zeile
            @($j.PSObject.Properties.Name) | Should -Be @('lauf', 'anzahl', 'saetze')
            $j.lauf | Should -Be 'TEST-LAUF'
            $j.anzahl | Should -Be 1
            @($j.saetze[0].PSObject.Properties.Name) | Should -Be @('id', 'beleg_nummer', 'nummer_art', 'betrag', 'buchungsdatum')
            $j.saetze[0].beleg_nummer | Should -Be '47110'
            Should -Invoke -ModuleName JanuaPort.Kartei -CommandName Get-JnptTokenFromCredentialManager `
                -Times 1 -Exactly -ParameterFilter { $Ziel -eq 'jnpt-roboter' }
        }

        It '1-arbeitsliste liefert bei leerer Liste anzahl 0' {
            Add-TestHandschlag
            Add-TestSearch @()
            Add-TestSearch @()
            $j = ConvertFrom-Json (Invoke-TestPad -Name '1-arbeitsliste.ps1' -Werte (Get-TestWerte))
            $j.anzahl | Should -Be 0
            @($j.saetze).Count | Should -Be 0
        }

        It '2-satz-sperren liefert id und die frisch gelesenen Ausgabefelder' {
            Add-TestHandschlag
            Add-TestGet (New-TestSatz -Id 's1' -Felder @{ betrag = '12.34' })
            Add-TestUpdate (New-TestSatz -Id 's1' -Felder @{ status = 'in_buchung' })

            $zeile = Invoke-TestPad -Name '2-satz-sperren.ps1' -Werte (Get-TestWerte @{ SatzId = 's1'; Lauf = 'TEST-LAUF' })

            $j = ConvertFrom-Json $zeile
            @($j.PSObject.Properties.Name) | Should -Be @('id', 'beleg_nummer', 'nummer_art', 'betrag', 'buchungsdatum')
            $j.id | Should -Be 's1'
            $j.betrag | Should -Be '12.34'
        }

        It '3-satz-verbuchen liefert id und status verbucht' {
            Add-TestHandschlag
            Add-TestGet (New-TestSatz -Id 's1' -Felder @{ status = 'in_buchung'; roboter_lauf = 'TEST-LAUF' })
            Add-TestUpdate (New-TestSatz -Id 's1' -Felder @{ status = 'verbucht' })

            $zeile = Invoke-TestPad -Name '3-satz-verbuchen.ps1' `
                        -Werte (Get-TestWerte @{ SatzId = 's1'; Lauf = 'TEST-LAUF'; Buchungsnr = 'BU-1' })

            $j = ConvertFrom-Json $zeile
            @($j.PSObject.Properties.Name) | Should -Be @('id', 'status')
            $j.status | Should -Be 'verbucht'
            (Get-TestAufrufe 'zahlungseingang_update')[0].Body.params.arguments.buchungs_nr | Should -Be 'BU-1'
        }

        It '4-satz-fehler liefert id und status fehler, auch bei Prozentzeichen in der Meldung' {
            Add-TestHandschlag
            Add-TestGet (New-TestSatz -Id 's1' -Felder @{ status = 'in_buchung'; roboter_lauf = 'TEST-LAUF' })
            Add-TestUpdate (New-TestSatz -Id 's1' -Felder @{ status = 'fehler' })
            $meldung = 'Fortschritt 50' + $script:P + ' abgebrochen'

            $zeile = Invoke-TestPad -Name '4-satz-fehler.ps1' `
                        -Werte (Get-TestWerte @{ SatzId = 's1'; Lauf = 'TEST-LAUF'; Schritt = 'Maske'; Meldung = $meldung })

            $j = ConvertFrom-Json $zeile
            @($j.PSObject.Properties.Name) | Should -Be @('id', 'status')
            $j.status | Should -Be 'fehler'
            (Get-TestAufrufe 'zahlungseingang_update')[0].Body.params.arguments.fehler_text | Should -Be ('Maske: ' + $meldung)
        }
    }
}
