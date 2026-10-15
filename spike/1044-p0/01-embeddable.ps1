# Sonde 1: embeddable-pywinauto -- pywinauto/pywin32/comtypes als Wheels in Embeddable 3.12 ohne site.
. "$PSScriptRoot\lib.ps1"
$ErrorActionPreference = 'Stop'
$name = 'embeddable-pywinauto'
try {
    $url = 'https://www.python.org/ftp/python/3.12.10/python-3.12.10-embed-amd64.zip'
    # SHA256 beim ersten Abruf selbst berechnet (python.org nennt nur MD5): Trust-on-first-use, dann fest.
    $expected = '4acbed6dd1c744b0376e3b1cf57ce906f9dc9e95e68824584c8099a63025a3c3'
    $zip = Join-Path $Work 'python-embed.zip'
    Invoke-WebRequest -Uri $url -OutFile $zip
    $got = (Get-FileHash $zip -Algorithm SHA256).Hash.ToLower()
    if ($got -ne $expected) { Write-Probe $name FAIL "SHA256 weicht ab: $got"; return }

    # Wheels mit einem separaten Python (Runner-Python), nicht mit der Embeddable
    $wheels = Join-Path $Work 'wheels'
    Remove-Item $wheels -Recurse -Force -ErrorAction SilentlyContinue
    $pip = Invoke-Capture 'python' @('-m', 'pip', 'download', '--only-binary=:all:', '--platform', 'win_amd64',
        '--python-version', '3.12', '--implementation', 'cp', '--abi', 'cp312', '-d', $wheels,
        'pywinauto', 'pywin32', 'comtypes') 300
    if ($pip.ExitCode -ne 0) { Write-Probe $name FAIL "pip download fehlgeschlagen: $($pip.Output)"; return }
    $wheelNames = (Get-ChildItem $wheels -Filter *.whl | ForEach-Object { $_.Name }) -join ','

    # Basis-Embeddable mit ._pth ohne site, mit site-packages-artigem Ordner
    $base = Join-Path $Work 'py-base'
    Remove-Item $base -Recurse -Force -ErrorAction SilentlyContinue
    Expand-Archive -Path $zip -DestinationPath $base
    $sp = Join-Path $base 'Lib\site-packages'
    New-Item -ItemType Directory -Force -Path $sp | Out-Null
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    foreach ($w in Get-ChildItem $wheels -Filter *.whl) {
        [System.IO.Compression.ZipFile]::ExtractToDirectory($w.FullName, $sp, $true)
    }
    $pth = Join-Path $base 'python312._pth'
    @('python312.zip', '.', 'Lib\site-packages', 'Lib\site-packages\win32',
      'Lib\site-packages\win32\lib', 'Lib\site-packages\pythonwin') | Set-Content -Path $pth -Encoding ascii
    # kein 'import site' in der ._pth -> site bleibt aus
    $dllDir = Join-Path $sp 'pywin32_system32'
    $dllNames = if (Test-Path $dllDir) { (Get-ChildItem $dllDir -Filter *.dll | ForEach-Object { $_.Name }) -join ',' } else { 'FEHLT' }

    # Drei Varianten, jede in frischer Kopie: A nur ._pth, B add_dll_directory, C DLLs neben python.exe
    $variants = [ordered]@{
        'A-nur-pth'               = @{ dll = $false; copy = $false }
        'B-add_dll_directory'     = @{ dll = $true;  copy = $false }
        'C-dlls-neben-python.exe' = @{ dll = $false; copy = $true }
    }
    $results = @(); $best = $null
    foreach ($v in $variants.Keys) {
        $dir = Join-Path $Work "py-$($v.Substring(0,1))"
        Remove-Item $dir -Recurse -Force -ErrorAction SilentlyContinue
        Copy-Item $base $dir -Recurse
        if ($variants[$v].copy) {
            Get-ChildItem (Join-Path $dir 'Lib\site-packages\pywin32_system32') -Filter *.dll | Copy-Item -Destination $dir
        }
        $dllArg = if ($variants[$v].dll) { Join-Path $dir 'Lib\site-packages\pywin32_system32' } else { '' }
        $py = Join-Path $dir 'python.exe'
        $r = Invoke-Capture $py @('-I', (Join-Path $PSScriptRoot 'py\import_check.py'), $dllArg) 120
        $ok = ($r.ExitCode -eq 0)
        $tail = (($r.Output -split "`n") | Where-Object { $_ -match '^(OK |win32api|pywintypes|\w*Error)' } | Select-Object -Last 3) -join ' / '
        $results += "$v=$(if ($ok) {'OK'} else {'FEHLER'})[$tail]"
        if ($ok -and -not $best) {
            $best = @{ py = $py; dll = $dllArg; variant = $v }
        }
    }
    # Gegenprobe im Wortlaut des Auftrags: python.exe -I -c (Variante A)
    $c = Invoke-Capture (Join-Path $Work 'py-A\python.exe') @('-I', '-c', 'import pywinauto, win32api, comtypes; from pywinauto import Application; print("c-ok")') 60
    $results += "wortlaut-c-auf-A=exit$($c.ExitCode)"
    if ($best) {
        "$($best.py)|$($best.dll)" | Set-Content (Join-Path $Work 'best-python.txt') -Encoding ascii
        Write-Probe $name PASS "kleinste funktionierende Variante: $($best.variant); Wheels: $wheelNames; pywin32-DLLs: $dllNames; $($results -join ' ; ')"
    } else {
        Write-Probe $name FAIL "keine Variante lief; Wheels: $wheelNames; DLLs: $dllNames; $($results -join ' ; ')"
    }
} catch {
    Write-Probe $name FAIL "Ausnahme: $($_.Exception.Message)"
}
