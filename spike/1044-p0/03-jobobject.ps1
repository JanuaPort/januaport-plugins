# Sonde 3: jobobject-kill -- Job Object (KILL_ON_JOB_CLOSE, Prozess- und Speicherdeckel) um Embeddable-Python.
. "$PSScriptRoot\lib.ps1"
$ErrorActionPreference = 'Stop'
$name = 'jobobject-kill'
try {
    $bestFile = Join-Path $Work 'best-python.txt'
    if (-not (Test-Path $bestFile)) { Write-Probe $name FAIL 'keine funktionierende Embeddable aus Sonde 1'; return }
    $py = (Get-Content $bestFile -Raw).Trim().Split('|')[0]

    $exe = Join-Path $Work 'jobprobe.exe'
    $b = Invoke-Capture 'go' @('build', '-o', $exe, '.') 300 -WorkingDirectory (Join-Path $PSScriptRoot 'jobprobe')
    if ($b.ExitCode -ne 0) { Write-Probe $name FAIL "go build fehlgeschlagen: $($b.Output)"; return }

    $beforeIds = @(Get-Process -Name notepad, ping -ErrorAction SilentlyContinue | ForEach-Object { $_.Id })
    $before = $beforeIds.Count
    $r = Invoke-Capture $exe @('-python', $py, '-scripts', (Join-Path $PSScriptRoot 'py'), '-work', $Work) 240
    $emitted = 0
    foreach ($line in ($r.Output -split "`n")) {
        $line = $line.Trim()
        if ($line.StartsWith('PROBE ')) {
            $parts = $line.Split(' ')
            Write-Probe $parts[1] $parts[2] (($parts | Select-Object -Skip 3) -join ' ')
            $emitted++
        }
    }
    if ($emitted -eq 0) { Write-Probe $name FAIL "jobprobe lieferte keine Ergebnisse (exit $($r.ExitCode)): $($r.Output)" }
    Start-Sleep -Seconds 2
    $left = @(Get-Process -Name notepad, ping -ErrorAction SilentlyContinue | Where-Object { $beforeIds -notcontains $_.Id })
    Write-Probe 'jobobject-aufraeumen' $(if ($left.Count -eq 0) { 'PASS' } else { 'FAIL' }) "notepad/ping vorher=$before neue uebrig=$($left.Count) (Gegenprobe per Get-Process, unabhaengig vom Go-Programm)"
    $left | Stop-Process -Force -ErrorAction SilentlyContinue
} catch {
    Write-Probe $name FAIL "Ausnahme: $($_.Exception.Message)"
}
