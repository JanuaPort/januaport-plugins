# VM-Sonden session-check und uia-in-states fuer einen Sitzungszustand.
# Aufruf als Administrator auf der VM: 20-zustand.ps1 -State unlocked|screensaver|monitor-off|disconnect|lock
# Ablauf: Notepad vorab starten -> Zustand setzen -> messen (beides in der Sitzung von spikeuser).
# Nach disconnect und lock ist ein Neustart noetig (Autologon stellt "entsperrt" wieder her).
param([Parameter(Mandatory)][ValidateSet('unlocked', 'screensaver', 'monitor-off', 'disconnect', 'lock')][string]$State)
. "$PSScriptRoot\lib.ps1"
$ErrorActionPreference = 'Stop'
# Erwartung: darf der Laeufer in diesem Zustand mit Eingabe arbeiten? (Hypothese, die die Messung pruefen soll)
$expect = if ($State -in 'unlocked', 'monitor-off') { 'nutzbar' } else { 'nicht-nutzbar' }
$f = Join-Path $Out "$State.txt"
Remove-Item $f -ErrorAction SilentlyContinue
Remove-Item (Join-Path $Out 'notepad_pre.pid') -ErrorAction SilentlyContinue

"--- qwinsta vorher:"; qwinsta
"setzen(prestart): " + (Invoke-PySession 'setstate.py' "prestart-notepad `"$f`"" 30)
if ($State -ne 'unlocked') {
    "setzen($State): " + (Invoke-PySession 'setstate.py' "$State `"$f`"" 60)
}
Start-Sleep -Seconds 5
"--- qwinsta im Zustand:"; qwinsta
"session-check: " + (Invoke-PySession 'session_check.py' "$State $expect `"$f`"" 60)
"uia-states: " + (Invoke-PySession 'uia_states.py' "$State `"$f`"" 240)
"--- Ergebnisdatei:"
if (Test-Path $f) { Get-Content $f } else { 'KEINE DATEI (Aufgabe lief nicht oder schrieb nichts)' }
if ($State -eq 'screensaver') { Get-Process -ErrorAction SilentlyContinue | Where-Object { $_.Path -like '*.scr' } | Stop-Process -Force }
