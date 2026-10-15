# Umgebungsangaben des Laufs (Spike JanuaPort/januaport#1044 P0).
. "$PSScriptRoot\lib.ps1"
$os = Get-CimInstance Win32_OperatingSystem
$py = (Invoke-Capture 'python' @('--version') 20).Output
$gv = (Invoke-Capture 'git' @('--version') 20).Output
$go = (Invoke-Capture 'go' @('version') 20).Output
$who = (Invoke-Capture 'whoami' @('/groups', '/fo', 'csv', '/nh') 20).Output
$admin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
Write-Probe 'umgebung' PASS "os=$($os.Caption) $($os.Version); user=$env:USERNAME; admin=$admin; runner-python=$py; $gv; $go"
