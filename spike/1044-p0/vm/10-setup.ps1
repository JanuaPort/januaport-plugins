# VM-Schritt 1: Verzeichnisse, Testnutzer, Autologon, Embeddable-Python mit pywinauto, Go.
# Laeuft auf der EC2-Instanz als Administrator. Das Passwort entsteht hier zufaellig, wird nie ausgegeben und liegt nur
# in der Registry der Wegwerf-Instanz (Autologon braucht es im Klartext oder als LSA-Geheimnis).
. "$PSScriptRoot\lib.ps1"
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

New-Item -ItemType Directory -Force -Path $Base, $Out, $PyScripts | Out-Null

# --- Testnutzer (Standardnutzer, kein Administrator) mit Autologon
if (-not (Get-LocalUser -Name spikeuser -ErrorAction SilentlyContinue)) {
    $bytes = New-Object byte[] 24
    (New-Object Security.Cryptography.RNGCryptoServiceProvider).GetBytes($bytes)
    $pw = 'Aa1!' + [Convert]::ToBase64String($bytes).Replace('/', 'x').Replace('+', 'y')
    New-LocalUser -Name spikeuser -Password (ConvertTo-SecureString $pw -AsPlainText -Force) -PasswordNeverExpires -AccountNeverExpires | Out-Null
    Add-LocalGroupMember -Group 'Users' -Member spikeuser
    $wl = 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon'
    Set-ItemProperty $wl AutoAdminLogon '1'
    Set-ItemProperty $wl DefaultUserName 'spikeuser'
    Set-ItemProperty $wl DefaultDomainName $env:COMPUTERNAME
    Set-ItemProperty $wl DefaultPassword $pw
    Remove-Variable pw
}
icacls $Base /grant 'spikeuser:(OI)(CI)M' | Out-Null
Write-Probe 'setup-nutzer' PASS 'spikeuser angelegt, Autologon gesetzt'

# --- Embeddable 3.12 mit pywinauto/pywin32/comtypes (Wheels direkt von PyPI, SHA256 aus der PyPI-JSON geprueft)
$pyUrl = 'https://www.python.org/ftp/python/3.12.10/python-3.12.10-embed-amd64.zip'
$pyZip = Join-Path $Base 'python-embed.zip'
Invoke-WebRequest -Uri $pyUrl -OutFile $pyZip
$sha = (Get-FileHash $pyZip -Algorithm SHA256).Hash.ToLower()
if ($sha -ne '4acbed6dd1c744b0376e3b1cf57ce906f9dc9e95e68824584c8099a63025a3c3') { throw "Embeddable-SHA256 weicht ab: $sha" }
Remove-Item $PyDir -Recurse -Force -ErrorAction SilentlyContinue
Expand-Archive -Path $pyZip -DestinationPath $PyDir
$sp = Join-Path $PyDir 'Lib\site-packages'
New-Item -ItemType Directory -Force -Path $sp | Out-Null
foreach ($spec in @('pywinauto/0.6.9', 'pywin32/312', 'comtypes/1.4.17', 'six/1.17.0')) {
    $n, $v = $spec.Split('/')
    $meta = Invoke-RestMethod "https://pypi.org/pypi/$n/$v/json"
    $f = $meta.urls | Where-Object { $_.packagetype -eq 'bdist_wheel' -and ($_.filename -match 'cp312-cp312-win_amd64' -or $_.filename -match 'none-any') } | Select-Object -First 1
    $whl = Join-Path $Base $f.filename
    Invoke-WebRequest -Uri $f.url -OutFile $whl
    if ((Get-FileHash $whl -Algorithm SHA256).Hash.ToLower() -ne $f.digests.sha256) { throw "SHA256 von $($f.filename) weicht ab" }
    Copy-Item $whl "$whl.zip" -Force
    Expand-Archive -Path "$whl.zip" -DestinationPath $sp -Force
}
@('python312.zip', '.', 'Lib\site-packages', 'Lib\site-packages\win32', 'Lib\site-packages\win32\lib', 'Lib\site-packages\pythonwin') |
    Set-Content -Path (Join-Path $PyDir 'python312._pth') -Encoding ascii

# --- Go (nur zum Bauen der Sonden-Exen auf der VM)
$goMeta = Invoke-RestMethod 'https://go.dev/dl/?mode=json'
$goFile = ($goMeta | Select-Object -First 1).files | Where-Object { $_.os -eq 'windows' -and $_.arch -eq 'amd64' -and $_.kind -eq 'archive' }
$goZip = Join-Path $Base 'go.zip'
Invoke-WebRequest -Uri "https://go.dev/dl/$($goFile.filename)" -OutFile $goZip
if ((Get-FileHash $goZip -Algorithm SHA256).Hash.ToLower() -ne $goFile.sha256) { throw 'Go-SHA256 weicht ab' }
Remove-Item (Join-Path $Base 'go') -Recurse -Force -ErrorAction SilentlyContinue
Expand-Archive -Path $goZip -DestinationPath $Base
Write-Probe 'setup-werkzeuge' PASS "python=3.12.10 embeddable, go=$($goFile.version)"
