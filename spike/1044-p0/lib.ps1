# Gemeinsame Helfer der Sonden (Spike JanuaPort/januaport#1044 P0). Kein Produktcode.
$script:Root = if ($env:RUNNER_TEMP) { $env:RUNNER_TEMP } else { $env:TEMP }
$script:Work = Join-Path $script:Root 'spike1044'
$script:ProbeFile = Join-Path $script:Work 'probes.txt'
New-Item -ItemType Directory -Force -Path $script:Work | Out-Null

function Write-Probe {
    param([string]$Name, [ValidateSet('PASS', 'FAIL')][string]$Status, [string]$Detail)
    $clean = ($Detail -replace '\s+', ' ').Trim()
    $line = "PROBE $Name $Status $clean"
    Write-Host $line
    Add-Content -Path $script:ProbeFile -Value $line -Encoding utf8
}

# Fuehrt ein Programm aus, liefert Exitcode und die zusammengefasste Ausgabe (stdout+stderr).
function Invoke-Capture {
    param([string]$File, [string[]]$Arguments, [int]$TimeoutSec = 120, [hashtable]$Env = @{}, [string]$WorkingDirectory = '')
    $psi = [System.Diagnostics.ProcessStartInfo]::new($File)
    foreach ($a in $Arguments) { $psi.ArgumentList.Add($a) }
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $psi.UseShellExecute = $false
    if ($WorkingDirectory) { $psi.WorkingDirectory = $WorkingDirectory }
    foreach ($k in $Env.Keys) { $psi.Environment[$k] = [string]$Env[$k] }
    $p = [System.Diagnostics.Process]::Start($psi)
    $so = $p.StandardOutput.ReadToEndAsync()
    $se = $p.StandardError.ReadToEndAsync()
    if (-not $p.WaitForExit($TimeoutSec * 1000)) {
        try { $p.Kill($true) } catch {}
        return [pscustomobject]@{ ExitCode = -999; Output = "ZEITUEBERSCHREITUNG nach $TimeoutSec s" }
    }
    $p.WaitForExit()
    [pscustomobject]@{ ExitCode = $p.ExitCode; Output = ($so.Result + "`n" + $se.Result).Trim() }
}
