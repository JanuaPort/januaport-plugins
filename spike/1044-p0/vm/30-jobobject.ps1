# VM-Sonde jobobject-real-app: appjob gegen klassische und Paket-Apps (MSIX), in der Sitzung von spikeuser.
# Aufruf als Administrator auf der VM.
. "$PSScriptRoot\lib.ps1"
$ErrorActionPreference = 'Stop'
$env:GOCACHE = Join-Path $Base 'gocache'; $env:GOPATH = Join-Path $Base 'gopath'; $env:GOFLAGS = '-mod=mod'
$go = Join-Path $Base 'go\bin\go.exe'
$exe = Join-Path $Base 'appjob.exe'
Push-Location (Join-Path $PSScriptRoot 'appjob')
$ErrorActionPreference = 'Continue' # go schreibt Fortschritt nach stderr
$buildOut = & $go build -ldflags '-H=windowsgui' -o $exe . 2>&1 | Out-String
if ($LASTEXITCODE -ne 0) { Pop-Location; throw "go build appjob fehlgeschlagen: $buildOut" }
$ErrorActionPreference = 'Stop'
Pop-Location

$res = Join-Path $Out 'jobobject.txt'
Remove-Item $res -ErrorAction SilentlyContinue
$pkgs = Get-AppxPackage -User spikeuser | Where-Object { $_.Name -in 'Microsoft.WindowsTerminal', 'Microsoft.WindowsFeedbackHub' } | ForEach-Object { "$($_.Name)=$($_.PackageFamilyName)" }
"pakete fuer spikeuser: $pkgs"
$alias = 'C:\Users\spikeuser\AppData\Local\Microsoft\WindowsApps\wt.exe'
"wt-alias vorhanden: $(Test-Path $alias)"

$cases = @(
    @{ label = 'notepad-klassisch'; exe = 'C:\Windows\System32\notepad.exe'; args = '' },
    @{ label = 'mspaint'; exe = 'C:\Windows\System32\mspaint.exe'; args = '' },
    @{ label = 'explorer-einzelinstanz'; exe = 'C:\Windows\explorer.exe'; args = 'C:\spike' },
    @{ label = 'msix-terminal-alias'; exe = $alias; args = '' },
    @{ label = 'msix-terminal-appsfolder'; exe = 'C:\Windows\explorer.exe'; args = 'shell:AppsFolder\Microsoft.WindowsTerminal_8wekyb3d8bbwe!App' },
    @{ label = 'msix-feedbackhub-appsfolder'; exe = 'C:\Windows\explorer.exe'; args = 'shell:AppsFolder\Microsoft.WindowsFeedbackHub_8wekyb3d8bbwe!App' }
)
foreach ($c in $cases) {
    if (-not (Test-Path $c.exe)) { Write-Probe "jobobject-$($c.label)" FAIL "Programm fehlt: $($c.exe)" $res | Out-Null; continue }
    $line = "-label $($c.label) -exe `"$($c.exe)`" -args `"$($c.args)`" -wait 12 -out `"$res`""
    $r = Invoke-InSession -Exe $exe -Arguments $line -TimeoutSec 120 -Name 'spike-appjob'
    "$($c.label): Aufgabe $r"
    Start-Sleep -Seconds 3
}
Get-Content $res
