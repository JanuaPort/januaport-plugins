# JanuaPort kartei-rpa - Tests fuer das Modul (JanuaPort/januaport#806).
#
# Pester 5 oder neuer. Der Transport ist gemockt (TestHelfer.ps1): Geprueft
# wird, WAS das Modul schicken wuerde - und vor allem, WANN es sich weigert.
#
# Aufruf:
#   Import-Module Pester -MinimumVersion 5.0
#   Invoke-Pester kartei-rpa/tests

BeforeAll {
    $script:Wurzel = Split-Path -Parent $PSScriptRoot
    Import-Module (Join-Path $script:Wurzel 'JanuaPort.Kartei.psm1') -Force
}

AfterAll {
    Remove-Module JanuaPort.Kartei -Force -ErrorAction SilentlyContinue
}

Describe 'JanuaPort.Kartei' {

    BeforeAll {
        . (Join-Path $PSScriptRoot 'TestHelfer.ps1')
        Register-TestTransport
    }

    BeforeEach { Reset-TestTransport }

    AfterAll {
        Remove-Variable -Name JnptTest -Scope Global -ErrorAction SilentlyContinue
    }

    Context 'Transport' {

        It 'packt den SSE-Rahmen aus' {
            Add-TestErgebnis @{ echo = 'durchstich' }
            $r = Invoke-JnptRpc -Endpoint 'https://example.com/mcp' -Token 'test-token' `
                    -Message @{ jsonrpc = '2.0'; id = 1; method = 'ping' }
            $r.echo | Should -Be 'durchstich'
        }

        It 'nimmt 202 ohne Rumpf als Erfolg' {
            $global:JnptTest.Antworten.Add((New-TestAntwort -StatusCode 202 -ContentType 'text/plain')) | Out-Null
            $r = Invoke-JnptRpc -Endpoint 'https://example.com/mcp' -Token 'test-token' `
                    -Message @{ jsonrpc = '2.0'; method = 'notifications/initialized' }
            $r | Should -BeNullOrEmpty
        }

        It 'schickt Bearer und beide Accept-Werte' {
            Add-TestErgebnis @{ ok = $true }
            Invoke-JnptRpc -Endpoint 'https://example.com/mcp' -Token 'test-token' `
                -Message @{ jsonrpc = '2.0'; id = 1; method = 'ping' } | Out-Null
            $kopf = $global:JnptTest.Aufrufe[0].Headers
            $kopf['Authorization'] | Should -Be 'Bearer test-token'
            $kopf['Accept'] | Should -Match 'application/json'
            $kopf['Accept'] | Should -Match 'text/event-stream'
        }

        It 'setzt den Header Mcp-Protocol-Version nicht (Aeren-Falle)' {
            Add-TestErgebnis @{ ok = $true }
            Invoke-JnptRpc -Endpoint 'https://example.com/mcp' -Token 'test-token' `
                -Message @{ jsonrpc = '2.0'; id = 1; method = 'ping' } | Out-Null
            $global:JnptTest.Aufrufe[0].Headers.Keys | Should -Not -Contain 'Mcp-Protocol-Version'
        }

        It 'meldet einen JSON-RPC-Fehler und wiederholt ihn nicht' {
            $rpc = @{ jsonrpc = '2.0'; id = 1; error = @{ code = -32602; message = 'unknown tool "x_update"' } }
            $global:JnptTest.Antworten.Add((New-TestAntwort -Rumpf (New-TestSse (ConvertTo-Json $rpc -Depth 6 -Compress)))) | Out-Null
            { Invoke-JnptRpc -Endpoint 'https://example.com/mcp' -Token 'test-token' `
                -Message @{ jsonrpc = '2.0'; id = 1; method = 'tools/call' } } |
                Should -Throw '*-32602*'
            $global:JnptTest.Aufrufe.Count | Should -Be 1
        }

        It 'meldet isError des Werkzeugs' {
            Add-TestErgebnis @{ isError = $true; content = @(@{ type = 'text'; text = 'kein Zugriff' }) }
            { Invoke-JnptTool -Endpoint 'https://example.com/mcp' -Token 'test-token' -Tool 'x_get' } |
                Should -Throw '*kein Zugriff*'
        }

        It 'wiederholt einen 401 nicht und nennt den Token nicht im Fehler' {
            $global:JnptTest.Wirft = 'HTTP 401 invalid_token'
            $meldung = $null
            try {
                Connect-JnptKartei -Endpoint 'https://example.com/mcp' -Satzart 'zahlungseingang' `
                    -Token 'geheim-test-wert' -Einstellungsdatei (Join-Path $TestDrive 'fehlt.psd1')
            } catch { $meldung = $_.Exception.Message }
            $meldung | Should -Not -BeNullOrEmpty
            $meldung | Should -Not -Match 'geheim-test-wert'
            $global:JnptTest.Aufrufe.Count | Should -Be 1
        }
    }

    Context 'Sitzung und Lauf' {

        It 'macht den Handschlag und liefert die ausgehandelte Fassung' {
            Add-TestHandschlag
            $s = Connect-JnptKartei -Endpoint 'https://example.com/mcp' -Satzart 'zahlungseingang' `
                    -Token 'test-token' -Einstellungsdatei (Join-Path $TestDrive 'fehlt.psd1')
            $s.Protokollfassung | Should -Be '2025-11-25'
            $s.Satzart | Should -Be 'zahlungseingang'
            $s.Endpoint | Should -Be 'https://example.com/mcp'
            $global:JnptTest.Aufrufe[0].Body.method | Should -Be 'initialize'
            $global:JnptTest.Aufrufe[0].Body.params.clientInfo.name | Should -Be 'januaport-kartei-rpa'
            $global:JnptTest.Aufrufe[1].Body.method | Should -Be 'notifications/initialized'
            $global:JnptTest.Aufrufe[1].Body.PSObject.Properties.Name | Should -Not -Contain 'id'
        }

        It 'baut eine Lauf-Kennung aus Zeit und Rechnername' {
            New-JnptLauf | Should -Match '^\d{4}-\d{2}-\d{2}T\d{4}-.+$'
        }
    }

    Context 'Einstellungen' {

        It 'nimmt ohne Datei die Voreinstellungen des Rezepts' {
            $e = Get-JnptKarteiEinstellungen -Pfad (Join-Path $TestDrive 'fehlt.psd1')
            $e.CredentialTarget | Should -Be 'jnpt-roboter'
            $e.Quelle | Should -Be 'roboter:buchung'
            $e.Felder.Buchungsnr | Should -Be 'buchungs_nr'
            $e.Felder.Lauf | Should -Be 'roboter_lauf'
            $e.Felder.FehlerText | Should -Be 'fehler_text'
            $e.Felder.BearbeitetAm | Should -Be 'bearbeitet_am'
            @($e.Ausgabefelder) | Should -Be @('beleg_nummer', 'nummer_art', 'betrag', 'buchungsdatum')
        }

        It 'ueberschreibt per Datei nur, was dort steht' {
            $pfad = Join-Path $TestDrive 'teil.psd1'
            Set-Content -LiteralPath $pfad -Encoding Ascii -Value @'
@{
    Quelle = 'robot:test'
    Felder = @{ Lauf = 'mein_lauf' }
    Ausgabefelder = @('nr', 'betrag')
}
'@
            $e = Get-JnptKarteiEinstellungen -Pfad $pfad
            $e.Quelle | Should -Be 'robot:test'
            $e.Felder.Lauf | Should -Be 'mein_lauf'
            $e.Felder.Buchungsnr | Should -Be 'buchungs_nr'
            $e.CredentialTarget | Should -Be 'jnpt-roboter'
            @($e.Ausgabefelder) | Should -Be @('nr', 'betrag')
        }

        It 'weist einen unbekannten Schluessel in der Datei ab' {
            $pfad = Join-Path $TestDrive 'tippfehler.psd1'
            Set-Content -LiteralPath $pfad -Encoding Ascii -Value "@{ Quele = 'x' }"
            { Get-JnptKarteiEinstellungen -Pfad $pfad } | Should -Throw '*Quele*'
        }

        It 'weist einen unbekannten Feld-Schluessel in der Datei ab' {
            $pfad = Join-Path $TestDrive 'feldtippfehler.psd1'
            Set-Content -LiteralPath $pfad -Encoding Ascii -Value "@{ Felder = @{ Buchungsnummer = 'x' } }"
            { Get-JnptKarteiEinstellungen -Pfad $pfad } | Should -Throw '*Buchungsnummer*'
        }

        It 'weist einen Feldnamen ab, der mit dem Status kollidiert' {
            { Get-JnptKarteiEinstellungen -Pfad (Join-Path $TestDrive 'fehlt.psd1') `
                -Vorgabe @{ Felder = @{ Lauf = 'status' } } } | Should -Throw '*status*'
        }

        It 'weist leere Ausgabefelder ab' {
            { Get-JnptKarteiEinstellungen -Pfad (Join-Path $TestDrive 'fehlt.psd1') `
                -Vorgabe @{ Ausgabefelder = @() } } | Should -Throw '*Ausgabefelder*'
        }

        It 'laesst einen Parameter die Datei schlagen' {
            $pfad = Join-Path $TestDrive 'datei.psd1'
            Set-Content -LiteralPath $pfad -Encoding Ascii -Value "@{ Quelle = 'aus-datei'; CredentialTarget = 'ziel-datei' }"
            Add-TestHandschlag
            $s = Connect-JnptKartei -Endpoint 'https://example.com/mcp' -Satzart 'zahlungseingang' `
                    -Token 'test-token' -Einstellungsdatei $pfad -Quelle 'aus-parameter'
            $s.Quelle | Should -Be 'aus-parameter'
        }

        It 'holt den Token unter dem Credential-Ziel aus der Datei' {
            $pfad = Join-Path $TestDrive 'ziel.psd1'
            Set-Content -LiteralPath $pfad -Encoding Ascii -Value "@{ CredentialTarget = 'ziel-aus-datei' }"
            Mock -ModuleName JanuaPort.Kartei -CommandName Get-JnptTokenFromCredentialManager -MockWith { 'test-token' }
            Add-TestHandschlag
            Connect-JnptKartei -Endpoint 'https://example.com/mcp' -Satzart 'zahlungseingang' `
                -Einstellungsdatei $pfad | Out-Null
            Should -Invoke -ModuleName JanuaPort.Kartei -CommandName Get-JnptTokenFromCredentialManager `
                -Times 1 -Exactly -ParameterFilter { $Ziel -eq 'ziel-aus-datei' }
        }

        It 'prueft die Einstellungen, bevor ein Token geholt wird' {
            $pfad = Join-Path $TestDrive 'kaputt.psd1'
            Set-Content -LiteralPath $pfad -Encoding Ascii -Value "@{ Unbekannt = 1 }"
            Mock -ModuleName JanuaPort.Kartei -CommandName Get-JnptTokenFromCredentialManager -MockWith { 'test-token' }
            { Connect-JnptKartei -Endpoint 'https://example.com/mcp' -Satzart 'zahlungseingang' `
                -Einstellungsdatei $pfad } | Should -Throw '*Unbekannt*'
            Should -Invoke -ModuleName JanuaPort.Kartei -CommandName Get-JnptTokenFromCredentialManager -Times 0 -Exactly
            $global:JnptTest.Aufrufe.Count | Should -Be 0
        }

        It 'traegt die Einstellungen in die Sitzung' {
            Add-TestHandschlag
            $s = Connect-JnptKartei -Endpoint 'https://example.com/mcp' -Satzart 'zahlungseingang' `
                    -Token 'test-token' -Einstellungsdatei (Join-Path $TestDrive 'fehlt.psd1') `
                    -Felder @{ Buchungsnr = 'beleg_ziel' } -Ausgabefelder @('a', 'b')
            $s.Quelle | Should -Be 'roboter:buchung'
            $s.Felder.Buchungsnr | Should -Be 'beleg_ziel'
            $s.Felder.Lauf | Should -Be 'roboter_lauf'
            @($s.Ausgabefelder) | Should -Be @('a', 'b')
        }

        It 'die Beispieldatei ist gueltig und nennt die Voreinstellungen' {
            $e = Get-JnptKarteiEinstellungen -Pfad (Join-Path $script:Wurzel 'kartei-rpa.einstellungen.example.psd1')
            $d = Get-JnptKarteiEinstellungen -Pfad (Join-Path $TestDrive 'fehlt.psd1')
            $e.CredentialTarget | Should -Be $d.CredentialTarget
            $e.Quelle | Should -Be $d.Quelle
            foreach ($k in $d.Felder.Keys) { $e.Felder[$k] | Should -Be $d.Felder[$k] }
            @($e.Ausgabefelder) | Should -Be @($d.Ausgabefelder)
        }
    }

    Context 'Arbeitsliste' {

        It 'bricht ab, wenn ein Satz auf in_buchung steht' {
            Add-TestSearch @(New-TestSatz -Id 'haenger' -Felder @{ status = 'in_buchung' })
            { Get-JnptArbeitsliste -Session (New-TestSitzung) } | Should -Throw '*in_buchung*'
            (Get-TestAufrufe 'zahlungseingang_search').Count | Should -Be 1
        }

        It 'liefert auch bei genau einem Treffer ein Array' {
            Add-TestSearch @()
            Add-TestSearch @(New-TestSatz)
            $liste = Get-JnptArbeitsliste -Session (New-TestSitzung)
            @($liste).Count | Should -Be 1
        }

        It 'liefert bei keinem Treffer ein leeres Array' {
            Add-TestSearch @()
            Add-TestSearch @()
            $liste = Get-JnptArbeitsliste -Session (New-TestSitzung)
            @($liste).Count | Should -Be 0
        }

        It 'sucht zuerst in_buchung, dann geprueft' {
            Add-TestSearch @()
            Add-TestSearch @()
            Get-JnptArbeitsliste -Session (New-TestSitzung) | Out-Null
            $suchen = Get-TestAufrufe 'zahlungseingang_search'
            $suchen[0].Body.params.arguments.status | Should -Be 'in_buchung'
            $suchen[1].Body.params.arguments.status | Should -Be 'geprueft'
            $suchen[1].Body.params.arguments.limit | Should -Be 100
        }
    }

    Context 'Lock-JnptSatz' {

        It 'setzt in_buchung mit der Lauf-Kennung und schreibt genau einmal' {
            Add-TestGet (New-TestSatz)
            Add-TestUpdate (New-TestSatz -Felder @{ status = 'in_buchung' })
            $satz = Lock-JnptSatz -Session (New-TestSitzung) -Id 'a1b2c3d4' -Lauf '2026-09-12T1600-TESTPC'
            $satz.fields.beleg_nummer | Should -Be '47110'

            $ups = Get-TestAufrufe 'zahlungseingang_update'
            $ups.Count | Should -Be 1
            $ups[0].Body.params.arguments.status | Should -Be 'in_buchung'
            $ups[0].Body.params.arguments.roboter_lauf | Should -Be '2026-09-12T1600-TESTPC'
            $ups[0].Body.params.arguments.source | Should -Be 'roboter:buchung'
        }

        It 'schickt jedes gelesene Feld zurueck (vollstaendiges Ueberschreiben)' {
            Add-TestGet (New-TestSatz)
            Add-TestUpdate (New-TestSatz -Felder @{ status = 'in_buchung' })
            Lock-JnptSatz -Session (New-TestSitzung) -Id 'a1b2c3d4' -Lauf 'L1' | Out-Null

            $arg = (Get-TestAufrufe 'zahlungseingang_update')[0].Body.params.arguments
            $namen = $arg.PSObject.Properties.Name
            foreach ($feld in (New-TestSatz).fields.Keys) {
                $namen | Should -Contain $feld
            }
            $namen | Should -Contain 'id'
            $namen | Should -Contain 'source'
            $namen | Should -Contain 'as_of'
            $arg.id | Should -Be 'a1b2c3d4'
            $arg.verwendungszweck | Should -Be 'RE 47110'
            $arg.betrag | Should -Be '980.00'
            $arg.as_of | Should -Match '^\d{4}-\d{2}-\d{2}$'
        }

        It 'verweigert einen Satz, der nicht auf geprueft steht' {
            Add-TestGet (New-TestSatz -Felder @{ status = 'verbucht' })
            { Lock-JnptSatz -Session (New-TestSitzung) -Id 'a1b2c3d4' -Lauf 'L1' } | Should -Throw '*geprueft*'
            (Get-TestAufrufe 'zahlungseingang_update').Count | Should -Be 0
        }

        It 'schreibt in die konfigurierten Felder und behauptet die konfigurierte Quelle' {
            $s = New-TestSitzung -Quelle 'robot:anders' -Felder @{ Lauf = 'mein_lauf'; BearbeitetAm = 'geaendert' }
            Add-TestGet (New-TestSatz -Felder @{ mein_lauf = ''; geaendert = '' })
            Add-TestUpdate (New-TestSatz -Felder @{ status = 'in_buchung' })
            Lock-JnptSatz -Session $s -Id 'a1b2c3d4' -Lauf 'L9' | Out-Null

            $arg = (Get-TestAufrufe 'zahlungseingang_update')[0].Body.params.arguments
            $arg.mein_lauf | Should -Be 'L9'
            $arg.geaendert | Should -Match '^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$'
            $arg.roboter_lauf | Should -Be ''
            $arg.bearbeitet_am | Should -Be ''
            $arg.source | Should -Be 'robot:anders'
        }
    }

    Context 'Set-JnptSatzErgebnis' {

        It 'setzt verbucht mit der Buchungsnummer' {
            Add-TestGet (New-TestSatz -Felder @{ status = 'in_buchung'; roboter_lauf = 'L1' })
            Add-TestUpdate (New-TestSatz -Felder @{ status = 'verbucht' })
            $r = Set-JnptSatzErgebnis -Session (New-TestSitzung) -Id 'a1b2c3d4' -Lauf 'L1' -Buchungsnr 'BU-47110'
            $r.status | Should -Be 'verbucht'
            $r.id | Should -Be 'a1b2c3d4'

            $ups = Get-TestAufrufe 'zahlungseingang_update'
            $ups.Count | Should -Be 1
            $ups[0].Body.params.arguments.status | Should -Be 'verbucht'
            $ups[0].Body.params.arguments.buchungs_nr | Should -Be 'BU-47110'
            $ups[0].Body.params.arguments.roboter_lauf | Should -Be 'L1'
            $ups[0].Body.params.arguments.bearbeitet_am | Should -Match '^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$'
        }

        It 'verweigert einen fremden Lauf' {
            Add-TestGet (New-TestSatz -Felder @{ status = 'in_buchung'; roboter_lauf = 'FREMD' })
            { Set-JnptSatzErgebnis -Session (New-TestSitzung) -Id 'a1b2c3d4' -Lauf 'L1' -Buchungsnr 'BU-1' } |
                Should -Throw '*FREMD*'
            (Get-TestAufrufe 'zahlungseingang_update').Count | Should -Be 0
        }

        It 'verweigert einen Satz, der nicht auf in_buchung steht' {
            Add-TestGet (New-TestSatz -Felder @{ status = 'geprueft'; roboter_lauf = 'L1' })
            { Set-JnptSatzErgebnis -Session (New-TestSitzung) -Id 'a1b2c3d4' -Lauf 'L1' -Buchungsnr 'BU-1' } |
                Should -Throw '*in_buchung*'
            (Get-TestAufrufe 'zahlungseingang_update').Count | Should -Be 0
        }

        It 'verweigert eine bereits gefuellte Buchungsnummer' {
            Add-TestGet (New-TestSatz -Felder @{ status = 'in_buchung'; roboter_lauf = 'L1'; buchungs_nr = 'BU-ALT' })
            { Set-JnptSatzErgebnis -Session (New-TestSitzung) -Id 'a1b2c3d4' -Lauf 'L1' -Buchungsnr 'BU-NEU' } |
                Should -Throw '*buchungs_nr*'
            (Get-TestAufrufe 'zahlungseingang_update').Count | Should -Be 0
        }

        It 'verweigert eine leere Buchungsnummer ohne jeden Aufruf' {
            { Set-JnptSatzErgebnis -Session (New-TestSitzung) -Id 'a1b2c3d4' -Lauf 'L1' -Buchungsnr '   ' } |
                Should -Throw '*Buchungsnummer*'
            $global:JnptTest.Aufrufe.Count | Should -Be 0
        }

        It 'prueft Lauf und Doppelbuchung an den konfigurierten Feldern' {
            $s = New-TestSitzung -Felder @{ Lauf = 'mein_lauf'; Buchungsnr = 'ziel_nr' }
            # Die Standardfelder sind absichtlich "falsch" belegt: Sie duerfen nicht zaehlen.
            Add-TestGet (New-TestSatz -Felder @{ status = 'in_buchung'; mein_lauf = 'L1'; roboter_lauf = 'FREMD'
                                                 ziel_nr = ''; buchungs_nr = 'BU-ALT' })
            Add-TestUpdate (New-TestSatz -Felder @{ status = 'verbucht' })
            Set-JnptSatzErgebnis -Session $s -Id 'a1b2c3d4' -Lauf 'L1' -Buchungsnr 'BU-NEU' | Out-Null

            $arg = (Get-TestAufrufe 'zahlungseingang_update')[0].Body.params.arguments
            $arg.ziel_nr | Should -Be 'BU-NEU'
            $arg.buchungs_nr | Should -Be 'BU-ALT'
            $arg.mein_lauf | Should -Be 'L1'
        }

        It 'verweigert eine gefuellte konfigurierte Buchungsnummer' {
            $s = New-TestSitzung -Felder @{ Buchungsnr = 'ziel_nr' }
            Add-TestGet (New-TestSatz -Felder @{ status = 'in_buchung'; roboter_lauf = 'L1'; ziel_nr = 'BU-ALT' })
            { Set-JnptSatzErgebnis -Session $s -Id 'a1b2c3d4' -Lauf 'L1' -Buchungsnr 'BU-NEU' } |
                Should -Throw '*ziel_nr*'
            (Get-TestAufrufe 'zahlungseingang_update').Count | Should -Be 0
        }
    }

    Context 'Set-JnptSatzFehler' {

        It 'setzt fehler mit Schritt und Meldung' {
            Add-TestGet (New-TestSatz -Felder @{ status = 'in_buchung'; roboter_lauf = 'L1' })
            Add-TestUpdate (New-TestSatz -Felder @{ status = 'fehler' })
            $r = Set-JnptSatzFehler -Session (New-TestSitzung) -Id 'a1b2c3d4' -Lauf 'L1' `
                    -Schritt 'Maske Rechnung' -Meldung 'Nummer nicht gefunden'
            $r.status | Should -Be 'fehler'

            $ups = Get-TestAufrufe 'zahlungseingang_update'
            $ups.Count | Should -Be 1
            $ups[0].Body.params.arguments.status | Should -Be 'fehler'
            $ups[0].Body.params.arguments.fehler_text | Should -Be 'Maske Rechnung: Nummer nicht gefunden'
            $ups[0].Body.params.arguments.buchungs_nr | Should -Be ''
        }

        It 'raeumt Steuerzeichen aus dem Fehlertext und deckelt auf 200 Zeichen' {
            Add-TestGet (New-TestSatz -Felder @{ status = 'in_buchung'; roboter_lauf = 'L1' })
            Add-TestUpdate (New-TestSatz -Felder @{ status = 'fehler' })
            $lang = "Zeile eins`r`nZeile zwei`tTabulator " + ('x' * 300)
            Set-JnptSatzFehler -Session (New-TestSitzung) -Id 'a1b2c3d4' -Lauf 'L1' `
                -Schritt 'Klickfolge' -Meldung $lang | Out-Null

            $text = (Get-TestAufrufe 'zahlungseingang_update')[0].Body.params.arguments.fehler_text
            $text.Length | Should -Be 200
            $text | Should -Not -Match "[`r`n`t]"
            $text | Should -BeLike 'Klickfolge: Zeile eins Zeile zwei Tabulator*'
        }

        It 'verweigert einen fremden Lauf' {
            Add-TestGet (New-TestSatz -Felder @{ status = 'in_buchung'; roboter_lauf = 'FREMD' })
            { Set-JnptSatzFehler -Session (New-TestSitzung) -Id 'a1b2c3d4' -Lauf 'L1' `
                -Schritt 'S' -Meldung 'M' } | Should -Throw '*FREMD*'
            (Get-TestAufrufe 'zahlungseingang_update').Count | Should -Be 0
        }

        It 'schreibt in das konfigurierte Fehlertext-Feld' {
            $s = New-TestSitzung -Felder @{ FehlerText = 'grund' }
            Add-TestGet (New-TestSatz -Felder @{ status = 'in_buchung'; roboter_lauf = 'L1'; grund = '' })
            Add-TestUpdate (New-TestSatz -Felder @{ status = 'fehler' })
            Set-JnptSatzFehler -Session $s -Id 'a1b2c3d4' -Lauf 'L1' -Schritt 'S' -Meldung 'M' | Out-Null

            $arg = (Get-TestAufrufe 'zahlungseingang_update')[0].Body.params.arguments
            $arg.grund | Should -Be 'S: M'
            $arg.fehler_text | Should -Be ''
        }
    }

    Context 'ConvertTo-JnptAusgabe' {

        It 'liefert id und die Ausgabefelder der Voreinstellung, in dieser Reihenfolge' {
            $a = ConvertTo-JnptAusgabe -Session (New-TestSitzung) -Satz (New-TestSatz)
            @($a.Keys) | Should -Be @('id', 'beleg_nummer', 'nummer_art', 'betrag', 'buchungsdatum')
            $a.id | Should -Be 'a1b2c3d4'
            $a.beleg_nummer | Should -Be '47110'
        }

        It 'gibt nur die konfigurierten Ausgabefelder heraus' {
            $s = New-TestSitzung -Ausgabefelder @('betrag', 'waehrung')
            $a = ConvertTo-JnptAusgabe -Session $s -Satz (New-TestSatz)
            @($a.Keys) | Should -Be @('id', 'betrag', 'waehrung')
            $a.waehrung | Should -Be 'EUR'
        }
    }
}
