# VM-Sonde motw-smartscreen: Mark-of-the-Web nach Download und Entpacken, Start einer unsignierten Go-Exe ohne Dialog,
# Meldungen von Defender und SmartScreen. Aufruf als Administrator auf der VM.
. "$PSScriptRoot\lib.ps1"
$ErrorActionPreference = 'Continue'
$ProgressPreference = 'SilentlyContinue'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
$res = Join-Path $Out 'motw.txt'
Remove-Item $res -ErrorAction SilentlyContinue
$work = Join-Path $Base 'motw'
Remove-Item $work -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force $work | Out-Null

function Get-Zone([string]$Path) {
    $s = Get-Item -LiteralPath $Path -Stream Zone.Identifier -ErrorAction SilentlyContinue
    if ($s) { (Get-Content -LiteralPath $Path -Stream Zone.Identifier | Where-Object { $_ -match 'ZoneId' }) -join '' } else { 'keine' }
}
function Add-Zone([string]$Path) {
    Set-Content -LiteralPath $Path -Stream Zone.Identifier -Value "[ZoneTransfer]`r`nZoneId=3`r`nHostUrl=https://example.invalid/release.zip"
}

# --- A: Welche Download-Wege setzen die Zone? (python.org-Embeddable)
$url = 'https://www.python.org/ftp/python/3.12.10/python-3.12.10-embed-amd64.zip'
Invoke-WebRequest -Uri $url -OutFile "$work\iwr.zip"
(New-Object Net.WebClient).DownloadFile($url, "$work\webclient.zip")
curl.exe -sL -o "$work\curl.zip" $url
Start-BitsTransfer -Source $url -Destination "$work\bits.zip"
$a = foreach ($n in 'iwr', 'webclient', 'curl', 'bits') { "$n=$(Get-Zone "$work\$n.zip")" }
Write-Probe 'motw-download-zone' PASS ("Zone.Identifier nach Download: " + ($a -join ' ; ') + " (PowerShell $($PSVersionTable.PSVersion))") $res

# --- B: unsignierte Go-Exe bauen
$env:GOCACHE = Join-Path $Base 'gocache'; $env:GOPATH = Join-Path $Base 'gopath'
$go = Join-Path $Base 'go\bin\go.exe'
Push-Location (Join-Path $PSScriptRoot 'hello')
$b = cmd /c "`"$go`" build -o `"$work\hello.exe`" . 2>&1"
Pop-Location
$sig = (Get-AuthenticodeSignature "$work\hello.exe").Status
$pysig = (Get-AuthenticodeSignature "$($PyDir)\python.exe").Status
"hello.exe signatur=$sig ; python.exe der Embeddable signatur=$pysig ; build=$b"

# --- C: Release-ZIP mit Exe + Embeddable bauen, Browser-Download simulieren (IWR setzt die Zone nicht)
$stage = Join-Path $work 'stage'
New-Item -ItemType Directory -Force $stage | Out-Null
Copy-Item "$work\hello.exe" $stage
Expand-Archive "$work\iwr.zip" (Join-Path $stage 'py') -Force
Compress-Archive -Path "$stage\*" -DestinationPath "$work\release.zip" -Force
Add-Zone "$work\release.zip"

# --- D: Erben die entpackten Dateien die Zone? (verschiedene Entpacker)
Add-Type -AssemblyName System.IO.Compression.FileSystem
Expand-Archive "$work\release.zip" "$work\x-expand-archive" -Force
[IO.Compression.ZipFile]::ExtractToDirectory("$work\release.zip", "$work\x-dotnet")
New-Item -ItemType Directory "$work\x-tar" | Out-Null
tar.exe -xf "$work\release.zip" -C "$work\x-tar"
$d = foreach ($m in 'expand-archive', 'dotnet', 'tar') { "$m[hello.exe]=$(Get-Zone "$work\x-$m\hello.exe") $m[python.exe]=$(Get-Zone "$work\x-$m\py\python.exe")" }
Write-Probe 'motw-vererbung-beim-entpacken' PASS ("ZIP traegt ZoneId=3; nach dem Entpacken: " + ($d -join ' ; ')) $res

# --- E: Start in der Sitzung. hello.exe und python.exe mit Zone (vom Entpacker unabhaengig: direkt markiert)
Copy-Item "$work\hello.exe" "$work\hello-zone3.exe"; Add-Zone "$work\hello-zone3.exe"
Copy-Item "$work\hello.exe" "$work\hello-ohne.exe"
Copy-Item $PyDir "$work\py-zone3" -Recurse; Get-ChildItem "$work\py-zone3" -Recurse -File | ForEach-Object { Add-Zone $_.FullName }
foreach ($t in @(@('ohne-zone', "$work\hello-ohne.exe"), @('zone3', "$work\hello-zone3.exe"))) {
    foreach ($m in 'createprocess', 'shellexecute') {
        "$($t[0]) $m : " + (Invoke-PySession 'motw_run.py' "$($t[0]) `"$($t[1])`" $m `"$res`"" 60)
    }
}
$m0 = (Get-Content (Join-Path $Out 'hello-ran.txt') | Measure-Object).Count
Set-Content "$work\py-marker.py" "open(r'C:\spike\out\hello-ran.txt', 'a').write('python-zone3 gelaufen' + chr(10))" -Encoding ascii
$pyArgs = "-I `"$work\py-marker.py`""
$r = Invoke-InSession -Exe "$work\py-zone3\python.exe" -Arguments $pyArgs -TimeoutSec 30 -Name 'spike-motw-py'
$m1 = (Get-Content (Join-Path $Out 'hello-ran.txt') | Measure-Object).Count
Write-Probe 'motw-python-zone3-aufgabenplaner' $(if ($m1 -gt $m0) { 'PASS' } else { 'FAIL' }) "signierte python.exe mit ZoneId=3 aus Aufgabe: Ergebnis=$r gelaufen=$($m1 - $m0)" $res
# Aufgabenplaner direkt (Aktion = Exe mit Zone), ohne Python dazwischen
$before = (Get-Content (Join-Path $Out 'hello-ran.txt') -ErrorAction SilentlyContinue | Measure-Object).Count
$r = Invoke-InSession -Exe "$work\hello-zone3.exe" -Arguments 'aufgabe' -TimeoutSec 30 -Name 'spike-motw-task'
$after = (Get-Content (Join-Path $Out 'hello-ran.txt') -ErrorAction SilentlyContinue | Measure-Object).Count
Write-Probe 'motw-zone3-aufgabenplaner' $(if ($after -gt $before -and $r -eq '0x0') { 'PASS' } else { 'FAIL' }) "Aufgabe Ergebnis=$r gelaufen=$($after - $before) Zeile(n)" $res

# --- F: Defender und SmartScreen
$mp = Get-MpComputerStatus
$det = @(Get-MpThreatDetection -ErrorAction SilentlyContinue | Select-Object -ExpandProperty ThreatID)
$ev = @(Get-WinEvent -FilterHashtable @{ LogName = 'Microsoft-Windows-Windows Defender/Operational'; Id = 1116, 1117, 1118, 1119, 5007 } -MaxEvents 20 -ErrorAction SilentlyContinue | ForEach-Object { "$($_.Id)@$($_.TimeCreated.ToString('HH:mm:ss'))" })
$ss = (Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Explorer' -Name SmartScreenEnabled -ErrorAction SilentlyContinue).SmartScreenEnabled
$ssPol = (Get-ItemProperty 'HKLM:\SOFTWARE\Policies\Microsoft\Windows\System' -Name EnableSmartScreen -ErrorAction SilentlyContinue).EnableSmartScreen
$ssProc = @(Get-Process smartscreen -ErrorAction SilentlyContinue).Count
Write-Probe 'defender-smartscreen' PASS ("Echtzeitschutz=$($mp.RealTimeProtectionEnabled) AMService=$($mp.AMServiceEnabled) Cloud(MAPS)=$((Get-MpPreference).MAPSReporting) Signaturen=$($mp.AntivirusSignatureVersion) Erkennungen=$($det -join ',') Defender-Ereignisse(1116/1117=Fund)=$($ev -join ',') SmartScreenEnabled=$ss Richtlinie=$ssPol smartscreen.exe-Prozesse=$ssProc") $res
Get-Content $res
