# Sonde 5: git-openssh-acl -- Schluesseldatei-ACL (icacls) gegen Windows-OpenSSH und das ssh aus Git fuer Windows.
. "$PSScriptRoot\lib.ps1"
$ErrorActionPreference = 'Stop'
$name = 'git-openssh-acl'
try {
    $winSsh = Join-Path $env:SystemRoot 'System32\OpenSSH\ssh.exe'
    $winKeygen = Join-Path $env:SystemRoot 'System32\OpenSSH\ssh-keygen.exe'
    $gitCmd = (Get-Command git -ErrorAction Stop).Source
    $gitSsh = $null
    for ($d = Split-Path $gitCmd; $d -and -not $gitSsh; $d = Split-Path $d) {
        if (Test-Path (Join-Path $d 'usr\bin\ssh.exe')) { $gitSsh = Join-Path $d 'usr\bin\ssh.exe' }
    }
    if (-not $gitSsh) { $gitSsh = 'C:\Program Files\Git\usr\bin\ssh.exe' }
    $inv = "git=$gitCmd; winssh=$(Test-Path $winSsh); gitssh=$gitSsh ($(Test-Path $gitSsh))"

    # Welche ssh.exe nimmt git per Default? (ssh -V und command -v laufen in der sh, die git selbst nimmt)
    $cfg = Invoke-Capture 'git' @('config', '--show-origin', '--get', 'core.sshCommand') 30
    $envInfo = "GIT_SSH=$($env:GIT_SSH) GIT_SSH_COMMAND=$($env:GIT_SSH_COMMAND) core.sshCommand=[$($cfg.Output)]"
    $which = Invoke-Capture 'git' @('-c', 'core.sshCommand=ssh -V; command -v ssh >&2; exit 1', 'ls-remote', 'ssh://github.com/JanuaPort/januaport-plugins.git') 60
    $oneLine = ($which.Output -split "`n" | Where-Object { $_ -match 'OpenSSH|/ssh|ssh\.exe' } | Select-Object -First 3) -join ' / '
    $pathHit = (Get-Command ssh -ErrorAction SilentlyContinue).Source
    Write-Probe 'git-ssh-default' PASS "git nimmt per Default: [$oneLine]; PowerShell-PATH-ssh=$pathHit; $envInfo; $inv"

    $user = [System.Security.Principal.WindowsIdentity]::GetCurrent().Name
    $keyDir = Join-Path $Work 'sshkey'
    Remove-Item $keyDir -Recurse -Force -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Force -Path $keyDir | Out-Null
    $key = Join-Path $keyDir 'id_spike'
    $kg = Invoke-Capture $winKeygen @('-q', '-t', 'ed25519', '-N', '', '-f', $key, '-C', 'spike-1044') 60
    if ($kg.ExitCode -ne 0 -or -not (Test-Path $key)) { Write-Probe $name FAIL "ssh-keygen: $($kg.Output)"; return }
    $emptyCfg = Join-Path $keyDir 'config'; '' | Set-Content $emptyCfg
    $known = Join-Path $keyDir 'known_hosts'

    function Test-Ssh([string]$ssh, [string]$keyPath) {
        $r = Invoke-Capture $ssh @('-F', $emptyCfg, '-i', $keyPath, '-o', 'IdentitiesOnly=yes', '-o', 'BatchMode=yes',
            '-o', 'StrictHostKeyChecking=accept-new', '-o', "UserKnownHostsFile=$known", '-o', 'ConnectTimeout=20',
            '-v', '-T', '-l', 'git', 'github.com') 60
        $keep = 'Permission denied|UNPROTECTED|too open|bad permissions|Load key|no such identity|Offering public key|Authenticated|Server accepts'
        $r.Output = (($r.Output -split "`n") | Where-Object { $_ -match $keep } | ForEach-Object { ($_.Trim() -replace '(?i)(identity file|public key:)? ?\S*id_spike\S*', '$1 <schluessel>') }) -join ' | '
        $r
    }
    function Get-AclSummary([string]$path) {
        $a = Get-Acl $path
        $rules = ($a.Access | ForEach-Object { "$($_.IdentityReference):$($_.FileSystemRights)" }) -join ','
        "owner=$($a.Owner) vererbt=$(-not $a.AreAccessRulesProtected) regeln=[$rules]"
    }
    function Set-AclOnlyMe([string]$path) {
        icacls $path /inheritance:r | Out-Null
        icacls $path /grant:r "${user}:(F)" | Out-Null
        icacls $path /remove:g '*S-1-5-18' '*S-1-5-32-544' '*S-1-1-0' '*S-1-5-32-545' '*S-1-5-11' | Out-Null   # SYSTEM, Administratoren, Everyone, Users, Authenticated Users
    }

    $clients = @(
        @{ id = 'winssh'; exe = $winSsh; path = $key },
        @{ id = 'gitssh'; exe = $gitSsh; path = ($key -replace '\\', '/') }
    )
    foreach ($client in $clients) {
        if (-not (Test-Path $client.exe)) { Write-Probe "$name-$($client.id)" FAIL "$($client.exe) nicht vorhanden"; continue }
        $ver = (Invoke-Capture $client.exe @('-V') 20).Output
        Set-AclOnlyMe $key
        $acl = Get-AclSummary $key
        $ok = Test-Ssh $client.exe $client.path
        $accepted = ($ok.Output -match 'Permission denied \(publickey\)') -and ($ok.Output -notmatch 'UNPROTECTED')
        Write-Probe "$name-$($client.id)-nur-ich" $(if ($accepted) { 'PASS' } else { 'FAIL' }) "[$ver] ACL: $acl => ssh: exit=$($ok.ExitCode) $($ok.Output)"

        foreach ($g in @(@('*S-1-1-0:(R)', 'Everyone-R'), @('*S-1-5-32-545:(R)', 'Users-R'), @('*S-1-1-0:(M)', 'Everyone-M'))) {
            Set-AclOnlyMe $key
            icacls $key /grant $g[0] | Out-Null   # SID statt Name: sprachunabhaengig
            $aclBad = Get-AclSummary $key
            $bad = Test-Ssh $client.exe $client.path
            $refused = ($bad.Output -match 'UNPROTECTED PRIVATE KEY|bad permissions|too open')
            Write-Probe "$name-$($client.id)-gegenprobe-$($g[1])" $(if ($refused) { 'PASS' } else { 'FAIL' }) "ACL: $aclBad => ssh: exit=$($bad.ExitCode) $($bad.Output)"
        }
    }
} catch {
    Write-Probe $name FAIL "Ausnahme: $($_.Exception.Message)"
}
