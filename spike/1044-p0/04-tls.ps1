# Sonde 4: go-tls-box-ca -- Go-Client gegen eine Test-CA; liest Go unter Windows den Benutzer-Store?
. "$PSScriptRoot\lib.ps1"
$ErrorActionPreference = 'Stop'
$name = 'go-tls-box-ca'
$server = $null
$imported = $false
try {
    $dir = Join-Path $Work 'tls'
    Remove-Item $dir -Recurse -Force -ErrorAction SilentlyContinue
    $exe = Join-Path $Work 'tlsprobe.exe'
    $b = Invoke-Capture 'go' @('build', '-o', $exe, './tlsprobe') 300 -WorkingDirectory (Join-Path $PSScriptRoot 'jobprobe')
    if ($b.ExitCode -ne 0) { Write-Probe $name FAIL "go build fehlgeschlagen: $($b.Output)"; return }
    $g = Invoke-Capture $exe @('gen', $dir) 60
    if ($g.ExitCode -ne 0) { Write-Probe $name FAIL "Zertifikatserzeugung: $($g.Output)"; return }

    $port = 18443
    $server = Start-Process -FilePath $exe -ArgumentList @('serve', $dir, "127.0.0.1:$port") -PassThru -WindowStyle Hidden
    $up = $false
    for ($i = 0; $i -lt 40 -and -not $up; $i++) {
        try { $t = [System.Net.Sockets.TcpClient]::new('127.0.0.1', $port); $t.Close(); $up = $true } catch { Start-Sleep -Milliseconds 250 }
    }
    if (-not $up) { Write-Probe $name FAIL 'HTTPS-Server kam nicht hoch'; return }
    $url = "https://127.0.0.1:$port/"
    $ca = Join-Path $dir 'ca.pem'

    $a = Invoke-Capture $exe @('client', $url, 'system+ca', $ca) 30
    Write-Probe 'go-tls-box-ca-a-systempool-plus-ca' $(if ($a.ExitCode -eq 0) { 'PASS' } else { 'FAIL' }) "SystemCertPool()+AppendCertsFromPEM: $($a.Output)"

    $d = Invoke-Capture $exe @('client', $url, 'neuerpool+ca', $ca) 30
    Write-Probe 'go-tls-box-ca-d-neuer-pool-plus-ca' $(if ($d.ExitCode -eq 0) { 'PASS' } else { 'FAIL' }) "NewCertPool()+AppendCertsFromPEM (Rueckfallweg): $($d.Output)"

    $bb = Invoke-Capture $exe @('client', $url, 'system') 30
    $failedAsExpected = ($bb.ExitCode -eq 3 -and $bb.Output -match 'certificate')
    Write-Probe 'go-tls-box-ca-b-nur-systempool' $(if ($failedAsExpected) { 'PASS' } else { 'FAIL' }) "nur System-Pool muss am Zertifikat scheitern: exit=$($bb.ExitCode) $($bb.Output)"

    # c) CA per certutil in den Benutzer-Store (CurrentUser\Root); der Import oeffnet meist einen Sicherheitsdialog
    $imp = Invoke-Capture 'certutil' @('-addstore', '-user', '-f', 'Root', $ca) 20
    $inStore = (Invoke-Capture 'certutil' @('-user', '-store', 'Root', 'Spike Box CA') 30).Output -match 'Spike Box CA'
    $imported = $inStore
    $importNote = if ($imp.ExitCode -eq -999) { 'HAENGT (vermutlich Sicherheitsdialog, in der Sitzung unsichtbar)' } else { "exit=$($imp.ExitCode)" }
    $cc = Invoke-Capture $exe @('client', $url, 'system') 30
    Write-Probe 'go-tls-box-ca-c-benutzerstore-certutil' $(if ($cc.ExitCode -eq 0) { 'PASS' } else { 'FAIL' }) "certutil -addstore -user Root: $importNote, im Store=$inStore; Go mit System-Pool danach: exit=$($cc.ExitCode) $($cc.Output)"
    if ($inStore) { Invoke-Capture 'certutil' @('-delstore', '-user', 'Root', 'Spike Box CA') 30 | Out-Null; $imported = $false }

    # c3) wie c, aber der Sicherheitsdialog wird per UIA (Embeddable aus Sonde 1) bestaetigt
    $c3Note = 'keine Embeddable aus Sonde 1'
    $bestFile = Join-Path $Work 'best-python.txt'
    if (Test-Path $bestFile) {
        $py, $dllDir = (Get-Content $bestFile -Raw).Trim().Split('|')
        $certProc = Start-Process -FilePath 'certutil' -ArgumentList @('-addstore', '-user', '-f', 'Root', $ca) -PassThru
        $click = Invoke-Capture $py @('-I', (Join-Path $PSScriptRoot 'py\clickyes.py'), $dllDir) 60
        $exited = $certProc.WaitForExit(20000)
        if (-not $exited) { $certProc.Kill($true) }
        $c3Note = "UIA: $($click.Output); certutil beendet=$exited exit=$(if ($exited) { $certProc.ExitCode } else { 'n/a' })"
        $in3 = (Invoke-Capture 'certutil' @('-user', '-store', 'Root', 'Spike Box CA') 30).Output -match 'Spike Box CA'
        $imported = $in3
        $c3 = Invoke-Capture $exe @('client', $url, 'system') 30
        Write-Probe 'go-tls-box-ca-c3-benutzerstore-dialog-per-uia' $(if ($c3.ExitCode -eq 0) { 'PASS' } else { 'FAIL' }) "$c3Note; im Store=$in3; Go mit System-Pool danach: exit=$($c3.ExitCode) $($c3.Output); PASS heisst: Go liest den Benutzer-Store"
        if ($in3) { Invoke-Capture 'certutil' @('-delstore', '-user', 'Root', 'Spike Box CA') 30 | Out-Null; $imported = $false }
    } else {
        Write-Probe 'go-tls-box-ca-c3-benutzerstore-dialog-per-uia' FAIL $c3Note
    }

    # c2) dialogfrei: in CurrentUser\My importieren und den Registry-Blob nach ...\Root\Certificates kopieren
    $regRoot = $null
    try {
        $thumb = ([System.Security.Cryptography.X509Certificates.X509Certificate2]::new($ca)).Thumbprint
        $myImp = Invoke-Capture 'certutil' @('-addstore', '-user', '-f', 'CA', $ca) 30
        $src = "HKCU:\Software\Microsoft\SystemCertificates\CA\Certificates\$thumb"
        $regRoot = "HKCU:\Software\Microsoft\SystemCertificates\Root\Certificates\$thumb"
        $blob = (Get-ItemProperty -Path $src -Name Blob -ErrorAction Stop).Blob
        New-Item -Path $regRoot -Force | Out-Null
        Set-ItemProperty -Path $regRoot -Name Blob -Value $blob -Type Binary
        $c2 = Invoke-Capture $exe @('client', $url, 'system') 30
        $ctl = try { (Invoke-WebRequest -Uri $url -UseBasicParsing -TimeoutSec 15).StatusCode.ToString() } catch { 'FEHLER ' + $_.Exception.Message }
        Write-Probe 'go-tls-box-ca-c2-benutzerstore-registry' $(if ($c2.ExitCode -eq 0) { 'PASS' } else { 'FAIL' }) "ohne Dialog (CA-Import + Blob nach HKCU Root kopiert): Kontrolle .NET/Windows-Kette vertraut der CA: $ctl; Go mit System-Pool: exit=$($c2.ExitCode) $($c2.Output); PASS heisst: Go liest den Benutzer-Store"
    } catch {
        Write-Probe "go-tls-box-ca-c2-benutzerstore-registry" FAIL "Ausnahme: $($_.Exception.Message); CA-Import: exit=$($myImp.ExitCode) $($myImp.Output)"
    } finally {
        if ($regRoot) { Remove-Item -Path $regRoot -Recurse -Force -ErrorAction SilentlyContinue }
        Invoke-Capture 'certutil' @('-delstore', '-user', 'CA', 'Spike Box CA') 30 | Out-Null
    }

    # e) Maschinen-Store (LocalMachine\Root, braucht Administrator): kein Benutzer-Dialog erwartet
    $machine = $false
    try {
        $em = Invoke-Capture 'certutil' @('-addstore', '-f', 'Root', $ca) 30
        $machine = ($em.ExitCode -eq 0)
        $ce = Invoke-Capture $exe @('client', $url, 'system') 30
        $emLast = ($em.Output -split "`n")[-1]
        Write-Probe 'go-tls-box-ca-e-maschinenstore' $(if ($ce.ExitCode -eq 0) { 'PASS' } else { 'FAIL' }) "certutil -addstore Root (LocalMachine): exit=$($em.ExitCode) $emLast; Go mit System-Pool: exit=$($ce.ExitCode) $($ce.Output)"
    } finally {
        if ($machine) { Invoke-Capture 'certutil' @('-delstore', 'Root', 'Spike Box CA') 30 | Out-Null }
    }
} catch {
    Write-Probe $name FAIL "Ausnahme: $($_.Exception.Message)"
} finally {
    if ($server -and -not $server.HasExited) { $server.Kill() }
    if ($imported) { Invoke-Capture 'certutil' @('-delstore', '-user', 'Root', 'Spike Box CA') 30 | Out-Null }
}
