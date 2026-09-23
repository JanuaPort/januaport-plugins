# JanuaPort kartei-rpa - Waechter ueber den Quelltext des Ordners (JanuaPort/januaport#806).
#
# 1. Alles, was PowerShell liest (.ps1, .psm1, .psd1), ist ASCII ohne BOM:
#    Windows PowerShell 5.1 liest eine Datei ohne BOM als ANSI - ein einziges
#    Sonderzeichen bricht den Parser, bevor eine Anfrage rausgeht.
# 2. Kein Token-Wert im Quelltext.
# 3. Keine Kennung des Pilotbetriebs, aus dem dieser Beisteller stammt - in
#    keiner Datei des Ordners. Die Liste steht nur als SHA-256 der
#    kleingeschriebenen Woerter hier, damit diese Datei sie nicht selbst traegt.
# 4. Hostnamen im Code nur example.com und localhost.

BeforeAll {
    $script:Wurzel = Split-Path -Parent $PSScriptRoot
    $script:Alle = @(Get-ChildItem -Path $script:Wurzel -Recurse -File)
    $script:Quelldateien = @($script:Alle | Where-Object { $_.Extension -in @('.ps1', '.psm1', '.psd1') })

    $script:Verboten = @(
        'b2cf1df0cc2719c5d2bdd5b3db82f7619b039df88485636796c0881396d734cb'
        '4e2a29abb8a91c6707a2a022b3d996b5ccb1a4735a19bcb01c10d78d8e76d8fb'
        '749c7dedb3feb05380e244d81e68a5b8be152e58a32440f63df4b272e699de38'
        '22570a499be9f0c09c41683251a2a6f2c581cd6d40fe42e85b9a0401ba25decf'
        'ec6f049478046f72becb006a2339202eae93911281dd610a7c869adec1ba3297'
        '795b104abe3e4134960ca245ded0e617f162c751209347b7f003bc35e062f43e'
        '4451306b9e1a55fe69efe01b30ef0741afd79a4e8b789ff21120bd028c699951'
        'c1f55f6c00b14ace9b86b5c76b3a1a87ab8e6e02d8453573f742f02443675322'
        '7ee0a6aea916330f53642770fe109e59c0d7d6139f0bc702609029b0124cce4c'
        '93ca06fe74ae6f35e8cff6a63b06df6e0db315bfd5debb9a0b630f3b7c435a67'
        'e46cc8585620ced95d8760b615e3caf9eb086f33ac98ec7309f2d3308e5bbaaa'
    )

    function Get-TestWortHashes {
        # Woerter mit und ohne Unterstrich, damit auch zusammengesetzte
        # Feldnamen und ihre Teile gefunden werden.
        param([string]$Text)
        $sha = [System.Security.Cryptography.SHA256]::Create()
        $klein = $Text.ToLowerInvariant()
        $woerter = @([regex]::Matches($klein, '[\p{L}\p{N}]+') | ForEach-Object { $_.Value }) +
                   @([regex]::Matches($klein, '[\p{L}\p{N}_]+') | ForEach-Object { $_.Value })
        $hashes = @{}
        foreach ($w in ($woerter | Sort-Object -Unique)) {
            $h = -join ($sha.ComputeHash([Text.Encoding]::UTF8.GetBytes($w)) | ForEach-Object { $_.ToString('x2') })
            $hashes[$h] = $true
        }
        return $hashes
    }
}

Describe 'Quelltext des Beistellers' {

    It 'findet ueberhaupt Quelldateien' {
        $script:Quelldateien.Count | Should -BeGreaterThan 8
    }

    It 'enthaelt in .ps1, .psm1 und .psd1 ausschliesslich ASCII (kein BOM, keine Umlaute)' {
        foreach ($d in $script:Quelldateien) {
            $bytes = [System.IO.File]::ReadAllBytes($d.FullName)
            $boese = @($bytes | Where-Object { $_ -gt 127 })
            "$($d.Name): $($boese.Count) Byte(s) ueber 127" | Should -Be "$($d.Name): 0 Byte(s) ueber 127"
        }
    }

    It 'traegt keinen Token-Wert im Quelltext' {
        # Zusammengesetzt, damit diese Datei nicht ueber sich selbst stolpert.
        $praefix = 'jnpt' + '_'
        foreach ($d in $script:Alle) {
            $inhalt = [System.IO.File]::ReadAllText($d.FullName)
            "$($d.Name): $($inhalt.Contains($praefix))" | Should -Be "$($d.Name): False"
        }
    }

    It 'die Wortliste erkennt ein verbotenes Wort (Selbsttest)' {
        # Das Wort wird aus Zeichencodes gebaut, damit es hier nicht im Klartext steht.
        $wort = -join ([char[]](99, 97, 114, 97, 116))
        $treffer = Get-TestWortHashes ('x ' + $wort + '_nummer y')
        $treffer.ContainsKey($script:Verboten[0]) | Should -BeTrue
    }

    It 'nennt keine Kennung des Pilotbetriebs in <_>' -ForEach @(
        Get-ChildItem -Path (Split-Path -Parent $PSScriptRoot) -Recurse -File | ForEach-Object { $_.Name }
    ) {
        $name = $_
        $datei = @($script:Alle | Where-Object { $_.Name -eq $name })[0]
        $hashes = Get-TestWortHashes ($datei.Name + ' ' + [System.IO.File]::ReadAllText($datei.FullName))
        $gefunden = @($script:Verboten | Where-Object { $hashes.ContainsKey($_) })
        $gefunden.Count | Should -Be 0 -Because "$name darf kein Wort der Sperrliste tragen"
    }

    It 'nennt in .ps1, .psm1 und .psd1 nur example.com und localhost als Host' {
        foreach ($d in $script:Quelldateien) {
            $inhalt = [System.IO.File]::ReadAllText($d.FullName)
            foreach ($m in [regex]::Matches($inhalt, 'https?://([A-Za-z0-9.-]+)')) {
                $wirt = $m.Groups[1].Value.ToLowerInvariant()
                "$($d.Name): $wirt" | Should -BeIn @("$($d.Name): example.com", "$($d.Name): localhost")
            }
        }
    }
}
