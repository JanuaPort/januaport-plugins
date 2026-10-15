# Sammelt alle PROBE-Zeilen in $GITHUB_STEP_SUMMARY; der Schritt schlaegt nie fehl.
. "$PSScriptRoot\lib.ps1"
$lines = if (Test-Path $ProbeFile) { Get-Content $ProbeFile } else { @() }
$md = [System.Collections.Generic.List[string]]::new()
$md.Add('## Spike #1044 P0 - Messergebnisse (headless, windows-latest)')
$md.Add('')
$md.Add('| Sonde | Ergebnis | Detail |')
$md.Add('|---|---|---|')
foreach ($l in $lines) {
    $p = $l -split ' ', 4
    if ($p.Count -ge 3 -and $p[0] -eq 'PROBE') {
        $detail = if ($p.Count -ge 4) { $p[3] -replace '\|', '/' } else { '' }
        $md.Add("| $($p[1]) | $($p[2]) | $detail |")
    }
}
$md.Add('')
$md.Add('```text')
$lines | ForEach-Object { $md.Add($_) }
$md.Add('```')
if ($env:GITHUB_STEP_SUMMARY) { $md | Add-Content -Path $env:GITHUB_STEP_SUMMARY -Encoding utf8 }
Write-Host "Sonden: $($lines.Count) Zeilen; PASS=$(@($lines | Where-Object { $_ -match ' PASS ' }).Count) FAIL=$(@($lines | Where-Object { $_ -match ' FAIL ' }).Count)"
exit 0
