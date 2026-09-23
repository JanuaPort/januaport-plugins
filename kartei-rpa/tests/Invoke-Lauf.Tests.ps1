# JanuaPort kartei-rpa - Tests fuer den Probelauf Invoke-Lauf.ps1 (JanuaPort/januaport#806).
#
# Kern: -Hoechstens ist der echte Lauf-Deckel, -Seitengroesse nur die Groesse
# einer Abfrage. Ohne -Hoechstens laeuft der Lauf bis leer - egal wie klein
# die Seitengroesse ist. Genau diese Verwechslung hat im Bestand einmal einen
# ganzen Bestand statt eines Satzes verbucht.
#
# Invoke-Lauf.ps1 laedt sein Modul beim Start per -Force neu; ohne den
# Import-Module-Mock ginge der gemockte Transport auf eine frische
# Modulinstanz verloren.

BeforeAll {
    $script:Wurzel = Split-Path -Parent $PSScriptRoot
    $script:LaufSkript = Join-Path $script:Wurzel 'Invoke-Lauf.ps1'
    Import-Module (Join-Path $script:Wurzel 'JanuaPort.Kartei.psm1') -Force
}

AfterAll {
    Remove-Module JanuaPort.Kartei -Force -ErrorAction SilentlyContinue
}

Describe 'Invoke-Lauf.ps1 - Lauf-Deckel' {

    BeforeAll {
        . (Join-Path $PSScriptRoot 'TestHelfer.ps1')
        Register-TestTransport

        function Invoke-TestLauf {
            # Punkt-Operator: Die Skript-Anweisungen laufen in dieser Sitzung,
            # in der Import-Module und New-JnptLauf gemockt sind.
            param([hashtable]$Params)
            . $script:LaufSkript @Params 2>&1 | Out-Null
        }

        function Add-TestSatzVerbucht {
            # Die vier Antworten fuer einen Satz, der gesperrt und verbucht wird.
            param([string]$Id)
            Add-TestGet (New-TestSatz -Id $Id)
            Add-TestUpdate (New-TestSatz -Id $Id -Felder @{ status = 'in_buchung' })
            Add-TestGet (New-TestSatz -Id $Id -Felder @{ status = 'in_buchung'; roboter_lauf = 'TEST-LAUF' })
            Add-TestUpdate (New-TestSatz -Id $Id -Felder @{ status = 'verbucht' })
        }

        # Feste Lauf-Kennung. Ohne -ModuleName: Das Skript ruft New-JnptLauf
        # aus seiner eigenen Ebene, nicht aus der Modul-Sitzung.
        Mock -CommandName New-JnptLauf -MockWith { 'TEST-LAUF' }
        Mock -CommandName Import-Module -MockWith { }
    }

    BeforeEach { Reset-TestTransport }

    AfterAll {
        Remove-Variable -Name JnptTest -Scope Global -ErrorAction SilentlyContinue
    }

    It '-Hoechstens 1 verbucht genau einen Satz und laesst die uebrigen auf geprueft' {
        Add-TestHandschlag
        Add-TestSearch @()
        Add-TestSearch @((New-TestSatz -Id 's1'), (New-TestSatz -Id 's2'), (New-TestSatz -Id 's3'))
        Add-TestSatzVerbucht 's1'

        Invoke-TestLauf -Params @{ Endpoint = 'https://example.com/mcp'; Token = 'test-token'; Hoechstens = 1 }

        (Get-TestAufrufe 'zahlungseingang_search').Count | Should -Be 2
        $gets = Get-TestAufrufe 'zahlungseingang_get'
        $gets.Count | Should -Be 2
        ($gets | ForEach-Object { $_.Body.params.arguments.id }) | Should -Not -Contain 's2'
        $updates = Get-TestAufrufe 'zahlungseingang_update'
        $updates.Count | Should -Be 2
        ($updates | ForEach-Object { $_.Body.params.arguments.id }) | Should -Not -Contain 's3'
        $updates[-1].Body.params.arguments.status | Should -Be 'verbucht'
    }

    It 'ohne -Hoechstens laeuft er bis leer' {
        Add-TestHandschlag
        Add-TestSearch @()
        Add-TestSearch @((New-TestSatz -Id 's1'), (New-TestSatz -Id 's2'))
        Add-TestSatzVerbucht 's1'
        Add-TestSatzVerbucht 's2'
        Add-TestSearch @()
        Add-TestSearch @()

        Invoke-TestLauf -Params @{ Endpoint = 'https://example.com/mcp'; Token = 'test-token' }

        (Get-TestAufrufe 'zahlungseingang_search').Count | Should -Be 4
        $updates = Get-TestAufrufe 'zahlungseingang_update'
        $updates.Count | Should -Be 4
        @($updates | Where-Object { $_.Body.params.arguments.status -eq 'verbucht' }).Count | Should -Be 2
    }

    It '-Seitengroesse wirkt als Seitengroesse, nicht als Lauf-Deckel' {
        Add-TestHandschlag
        Add-TestSearch @()
        Add-TestSearch @((New-TestSatz -Id 's1'))
        Add-TestSatzVerbucht 's1'
        Add-TestSearch @()
        Add-TestSearch @((New-TestSatz -Id 's2'))
        Add-TestSatzVerbucht 's2'
        Add-TestSearch @()
        Add-TestSearch @()

        Invoke-TestLauf -Params @{ Endpoint = 'https://example.com/mcp'; Token = 'test-token'; Seitengroesse = 1 }

        $suchen = Get-TestAufrufe 'zahlungseingang_search'
        $suchen.Count | Should -Be 6
        $geprueft = @($suchen | Where-Object { $_.Body.params.arguments.status -eq 'geprueft' })
        $geprueft.Count | Should -Be 3
        foreach ($s in $geprueft) { $s.Body.params.arguments.limit | Should -Be 1 }
        $updates = Get-TestAufrufe 'zahlungseingang_update'
        @($updates | Where-Object { $_.Body.params.arguments.status -eq 'verbucht' }).Count | Should -Be 2
    }

    It 'nimmt die Satzart als Parameter' {
        Add-TestHandschlag
        Add-TestSearch @()
        Add-TestSearch @()

        Invoke-TestLauf -Params @{ Endpoint = 'https://example.com/mcp'; Token = 'test-token'; Satzart = 'andere_art' }

        (Get-TestAufrufe 'andere_art_search').Count | Should -Be 2
    }

    It 'begrenzt die Seitengroesse auf 100' {
        $cmd = Get-Command $script:LaufSkript
        $bereich = $cmd.Parameters['Seitengroesse'].Attributes |
            Where-Object { $_ -is [System.Management.Automation.ValidateRangeAttribute] }
        $bereich.MaxRange | Should -Be 100
    }
}
