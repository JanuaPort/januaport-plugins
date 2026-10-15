# Sonde 2: pywinauto-backends-headless -- Notepad, win32- und uia-Backend, mit der Embeddable aus Sonde 1.
. "$PSScriptRoot\lib.ps1"
$ErrorActionPreference = 'Stop'
$name = 'pywinauto-backends-headless'
try {
    $bestFile = Join-Path $Work 'best-python.txt'
    if (-not (Test-Path $bestFile)) { Write-Probe $name FAIL 'keine funktionierende Embeddable aus Sonde 1'; return }
    $py, $dll = (Get-Content $bestFile -Raw).Trim().Split('|')
    $r = Invoke-Capture $py @('-I', (Join-Path $PSScriptRoot 'py\backends.py'), $dll) 240
    foreach ($line in ($r.Output -split "`n")) {
        $line = $line.Trim()
        if ($line.StartsWith('PROBE ')) {
            $parts = $line.Split(' ')
            Write-Probe $parts[1] $parts[2] (($parts | Select-Object -Skip 3) -join ' ')
        }
    }
    if ($r.Output -notmatch 'PROBE pywinauto-backends-headless-') {
        Write-Probe $name FAIL "backends.py lieferte keine Ergebnisse (exit $($r.ExitCode)): $($r.Output)"
    }
} catch {
    Write-Probe $name FAIL "Ausnahme: $($_.Exception.Message)"
}
