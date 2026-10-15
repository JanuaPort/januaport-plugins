# VM-Sonde logon-task: Aufgabe "bei Anmeldung" fuer spikeuser, startet logon_probe.py in der Sitzung.
# Einmal registrieren (-Register), nach jedem Neustart mit Autologon das Ergebnis lesen (-Lesen).
param([switch]$Register, [switch]$Lesen)
. "$PSScriptRoot\lib.ps1"
if ($Register) {
    $a = New-ScheduledTaskAction -Execute $PyW -Argument "-I `"$PyScripts\logon_probe.py`" `"$Out\logon.txt`""
    $t = New-ScheduledTaskTrigger -AtLogOn -User $User
    $p = New-ScheduledTaskPrincipal -UserId $User -LogonType Interactive -RunLevel Limited
    $s = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
    Register-ScheduledTask -TaskName 'spike-logon' -Action $a -Trigger $t -Principal $p -Settings $s -Force | Out-Null
    'registriert'
}
if ($Lesen) {
    $boot = (Get-CimInstance Win32_OperatingSystem).LastBootUpTime
    "boot=$($boot.ToString('HH:mm:ss')) jetzt=$((Get-Date).ToString('HH:mm:ss'))"
    if (Test-Path "$Out\logon.txt") { Get-Content "$Out\logon.txt" }
    qwinsta
    Get-ScheduledTaskInfo -TaskName 'spike-logon' | Select-Object LastRunTime, LastTaskResult | Format-List | Out-String
}
