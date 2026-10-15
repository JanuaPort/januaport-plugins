# VM-Gegenmittel-Probe: getrennte Sitzung per tscon (als SYSTEM, ohne Passwort) wieder an die Konsole holen und messen.
# Aufruf als Administrator auf der VM nach "20-zustand.ps1 -State disconnect".
. "$PSScriptRoot\lib.ps1"
$f = Join-Path $Out 'disconnect-tscon.txt'
Remove-Item $f -ErrorAction SilentlyContinue
$a = New-ScheduledTaskAction -Execute 'tscon.exe' -Argument '1 /dest:console'
$p = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
Register-ScheduledTask -TaskName 'spike-tscon' -Action $a -Principal $p -Force | Out-Null
Start-ScheduledTask -TaskName 'spike-tscon'
Start-Sleep -Seconds 6
'tscon: 0x{0:x}' -f (Get-ScheduledTaskInfo -TaskName 'spike-tscon').LastTaskResult
qwinsta
Invoke-PySession 'session_check.py' "disconnect-tscon nutzbar `"$f`"" 60
Invoke-PySession 'uia_states.py' "disconnect-tscon `"$f`"" 200
Get-Content $f
