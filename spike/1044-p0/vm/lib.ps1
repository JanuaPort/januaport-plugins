# Gemeinsame Helfer der VM-Sonden (Spike JanuaPort/januaport#1044 P0, Teil 2). Kein Produktcode.
# Laufen auf der EC2-Instanz als Administrator (per WinRM). Die Sitzungsprogramme laufen als Benutzer "spikeuser".
$script:Base = 'C:\spike'
$script:Out = Join-Path $Base 'out'
$script:PyDir = Join-Path $Base 'py312'
$script:PyW = Join-Path $PyDir 'pythonw.exe'
$script:PyScripts = Join-Path $Base 'py'
$script:User = "$env:COMPUTERNAME\spikeuser"
New-Item -ItemType Directory -Force -Path $Out | Out-Null

function Write-Probe {
    param([string]$Name, [ValidateSet('PASS', 'FAIL')][string]$Status, [string]$Detail, [string]$File = (Join-Path $Out 'probes.txt'))
    $line = "PROBE $Name $Status $(($Detail -replace '\s+', ' ').Trim())"
    Add-Content -Path $File -Value $line -Encoding utf8
    $line
}

# Startet ein Programm in der interaktiven Sitzung von spikeuser (Aufgabe "nur bei Anmeldung") und wartet auf das Ende.
function Invoke-InSession {
    param([string]$Exe, [string]$Arguments, [int]$TimeoutSec = 90, [string]$Name = 'spike-adhoc')
    $a = New-ScheduledTaskAction -Execute $Exe -Argument $Arguments
    $p = New-ScheduledTaskPrincipal -UserId $User -LogonType Interactive -RunLevel Limited
    $s = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable
    Register-ScheduledTask -TaskName $Name -Action $a -Principal $p -Settings $s -Force | Out-Null
    Start-ScheduledTask -TaskName $Name
    Start-Sleep -Seconds 2
    $deadline = (Get-Date).AddSeconds($TimeoutSec)
    while ((Get-ScheduledTask -TaskName $Name).State -eq 'Running' -and (Get-Date) -lt $deadline) { Start-Sleep -Milliseconds 500 }
    if ((Get-ScheduledTask -TaskName $Name).State -eq 'Running') {
        Stop-ScheduledTask -TaskName $Name
        return 'ZEITUEBERSCHREITUNG'
    }
    '0x{0:x}' -f (Get-ScheduledTaskInfo -TaskName $Name).LastTaskResult
}

function Invoke-PySession {
    param([string]$Script, [string]$ScriptArgs, [int]$TimeoutSec = 90)
    Invoke-InSession -Exe $PyW -Arguments "-I `"$(Join-Path $PyScripts $Script)`" $ScriptArgs" -TimeoutSec $TimeoutSec
}
