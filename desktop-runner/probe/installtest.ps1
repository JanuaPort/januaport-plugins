# Windows/amd64; no live run in ordinary CI. Dot-source for offline adapter tests.
[CmdletBinding()]
param(
    [ValidatePattern('\A[0-9]+\.[0-9]+\.[0-9]+\z')][string]$Version = '0.1.0',
    [securestring]$Code,
    [switch]$CreateStandardUser,
    # Internal standard-account handoff: the pipe name contains no secret.
    [string]$PipeName,
    [string]$VerifiedDirectory,
    [switch]$CleanupOnly
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Start-InstalltestProcess {
    param([string]$FilePath, [string[]]$Arguments, [securestring]$PairCode,
        [pscredential]$Credential)
    $info = [Diagnostics.ProcessStartInfo]::new($FilePath)
    $info.UseShellExecute = $false
    $info.CreateNoWindow = $true
    $info.WindowStyle = 'Hidden'
    $info.RedirectStandardOutput = $true
    $info.RedirectStandardError = $true
    $info.StandardOutputEncoding = [Text.UTF8Encoding]::new($false)
    $info.StandardErrorEncoding = [Text.UTF8Encoding]::new($false)
    # Neither runner nor standard-account child inherits these secrets.
    $info.Environment.Remove('SHOWCASE_PAIR_CODE') | Out-Null
    foreach ($argument in $Arguments) { $info.ArgumentList.Add($argument) }
    if ($PairCode) {
        $pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($PairCode)
        try {
            # Windows v0.1.0 has no stdin option. Never pass this argv to a shell
            # or print ProcessStartInfo / native output, including on failure.
            $info.ArgumentList.Add('--code')
            $info.ArgumentList.Add([Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer))
        } finally { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer) }
    }
    if ($Credential) {
        $info.Environment.Remove('GH_TOKEN') | Out-Null
        $info.UserName = $Credential.UserName
        $info.Domain = $env:COMPUTERNAME
        $info.Password = $Credential.Password
        $info.LoadUserProfile = $true
    }
    $process = [Diagnostics.Process]::new()
    $process.StartInfo = $info
    try {
        if (-not $process.Start()) { throw 'process_start_failed' }
        return $process
    } catch {
        $process.Dispose()
        throw 'process_start_failed'
    } finally {
        $info.ArgumentList.Clear()
    }
}

function Wait-InstalltestProcessExit {
    param([Diagnostics.Process]$Process, [int]$TimeoutSeconds = 30)
    if (-not $Process.WaitForExit($TimeoutSeconds * 1000)) {
        $Process.Kill($true)
        $Process.WaitForExit()
        throw 'process_timeout'
    }
    if ($Process.ExitCode -ne 0) { throw 'process_failed' }
}

function Complete-InstalltestProcess {
    param([Diagnostics.Process]$Process, [int]$TimeoutSeconds = 30)
    $output = $Process.StandardOutput.ReadToEndAsync()
    $null = $Process.StandardError.ReadToEndAsync()
    try {
        Wait-InstalltestProcessExit $Process $TimeoutSeconds
        # Callers use only status output; pair output is discarded even on success.
        return $output.GetAwaiter().GetResult()
    } finally {
        $Process.Dispose()
    }
}

function Invoke-InstalltestCommand {
    param([string]$FilePath, [string[]]$Arguments, [securestring]$PairCode, [int]$TimeoutSeconds = 30)
    Complete-InstalltestProcess (Start-InstalltestProcess $FilePath $Arguments $PairCode) $TimeoutSeconds
}

function Invoke-InstalltestAttestation {
    param([string]$Executable, [string]$Bundle, [string]$ReleaseVersion)
    $repo = 'JanuaPort/januaport-plugins'
    $verifyArguments = @('attestation', 'verify', $Executable, '--bundle', $Bundle, '--repo', $repo,
        '--signer-workflow', "$repo/.github/workflows/desktop-runner-release.yml",
        '--source-ref', "refs/tags/desktop-runner-v$ReleaseVersion")
    Invoke-InstalltestCommand 'gh' $verifyArguments | Out-Null
}

function Assert-InstalltestStandardUser {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    try {
        $principal = [Security.Principal.WindowsPrincipal]::new($identity)
        # Check both an enabled admin token and deny-only membership (UAC).
        if ($principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator) -or
            @($identity.Groups.Value) -contains 'S-1-5-32-544') { throw 'standard_account_required' }
    } finally { $identity.Dispose() }
}

function Assert-InstalltestDigest {
    param([string]$Directory)
    $sums = @(Get-Content -LiteralPath (Join-Path $Directory 'SHA256SUMS'))
    if ($sums.Count -ne 1 -or $sums[0] -cnotmatch '^([0-9a-f]{64}) [ *]desktop-runner\.exe$') {
        throw 'checksum_format'
    }
    $expected = $Matches[1]
    if ((Get-FileHash -LiteralPath (Join-Path $Directory 'desktop-runner.exe') -Algorithm SHA256).Hash.ToLowerInvariant() -ne $expected) {
        throw 'checksum_mismatch'
    }
}

function Assert-InstalltestSignature {
    param([string]$Path)
    $signature = Get-AuthenticodeSignature -LiteralPath $Path
    if ($signature.Status -ne 'Valid') { throw 'signature_invalid' }
    if ($signature.SignerCertificate.GetNameInfo([Security.Cryptography.X509Certificates.X509NameType]::SimpleName, $false) -cne 'JanuaPort GmbH') {
        throw 'publisher_invalid'
    }
}

function Get-InstalltestAutostart {
    param([string]$TaskName, [string]$RunValue = 'JanuaPortDesktopLaeufer')
    # Enumerate through the real COM adapter; access errors must propagate.
    # Catching GetTask errors as "absent" would hide an access-denied task.
    $service = New-Object -ComObject 'Schedule.Service'
    $service.Connect()
    $folder = $service.GetFolder('\')
    $tasks = $folder.GetTasks(1)
    try {
        $taskPresent = @($tasks | Where-Object Name -EQ $TaskName).Count -ne 0
    } finally {
        [Runtime.InteropServices.Marshal]::FinalReleaseComObject($tasks) | Out-Null
        [Runtime.InteropServices.Marshal]::FinalReleaseComObject($folder) | Out-Null
        [Runtime.InteropServices.Marshal]::FinalReleaseComObject($service) | Out-Null
    }
    $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Software\Microsoft\Windows\CurrentVersion\Run')
    try { $runPresent = $null -ne $key -and $null -ne $key.GetValue($RunValue) }
    finally { if ($key) { $key.Dispose() } }
    return $taskPresent -or $runPresent
}

function Assert-InstalltestClean {
    param([string]$TaskName, [string]$RunValue = 'JanuaPortDesktopLaeufer')
    if (Get-InstalltestAutostart $TaskName $RunValue) { throw 'autostart_remaining' }
}

function Assert-InstalltestFresh {
    param([string]$StateRoot, [string]$TaskName)
    Assert-InstalltestClean $TaskName
    if (Test-Path -LiteralPath $StateRoot) { throw 'profile_in_use' }
}

function Read-InstalltestManifest {
    param([string]$StateRoot)
    $path = Join-Path $StateRoot 'manifest'
    if (-not (Test-Path -LiteralPath $path)) { return $null }
    $entropy = [Text.Encoding]::UTF8.GetBytes('JanuaPort/Desktop-Laeufer/v1/CurrentUser')
    $clear = [Security.Cryptography.ProtectedData]::Unprotect(
        [IO.File]::ReadAllBytes($path), $entropy, [Security.Cryptography.DataProtectionScope]::CurrentUser)
    try { return [Text.Encoding]::UTF8.GetString($clear) | ConvertFrom-Json }
    finally { [Array]::Clear($clear) }
}

function Wait-InstalltestLife {
    param([string]$Executable, [string]$StateRoot, [Diagnostics.Process]$Runner,
        [int]$Seconds = 300, [string[]]$PrefixArguments = @())
    $deadline = [DateTime]::UtcNow.AddSeconds($Seconds)
    $synced = $false
    # Keep the full observation window for GATE's ok/cancel probes. A manifest
    # saved after pairing proves contact, not heartbeat acknowledgement or ok.
    while ([DateTime]::UtcNow -lt $deadline) {
        if ($Runner.HasExited) { throw 'runner_exited' }
        $remaining = [Math]::Max(1, [int]($deadline - [DateTime]::UtcNow).TotalSeconds)
        $status = Invoke-InstalltestCommand $Executable ($PrefixArguments + @('status')) -TimeoutSeconds $remaining
        if ($status.Trim() -cne 'Desktop-Läufer: gekoppelt=true installiert=true') { throw 'status_invalid' }
        $manifest = Read-InstalltestManifest $StateRoot
        if ($manifest -and [DateTime]$manifest.manifest.last_sync -gt [DateTime]::MinValue) { $synced = $true }
        Start-Sleep -Milliseconds 500
    }
    if (-not $synced) { throw 'life_timeout' }
}

function Invoke-InstalltestWorker {
    param([string]$ReleaseVersion, [securestring]$PairCode, [string]$Directory,
        [switch]$OnlyCleanup)
    Assert-InstalltestStandardUser
    $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $task = 'JanuaPort-Desktop-' + $sid
    # Environment.SpecialFolder reads the loaded user's profile, not the CI
    # parent's inherited LOCALAPPDATA. Go's os.UserCacheDir uses LOCALAPPDATA.
    $env:LOCALAPPDATA = [Environment]::GetFolderPath('LocalApplicationData')
    $stateRoot = Join-Path $env:LOCALAPPDATA 'JanuaPort/desktop-runner'
    $destination = Join-Path $env:LOCALAPPDATA "JanuaPort/installtest/$ReleaseVersion"
    $executable = Join-Path $destination 'desktop-runner.exe'
    if ($OnlyCleanup) {
        if (Test-Path -LiteralPath $executable) { Invoke-InstalltestCommand $executable @('uninstall') | Out-Null }
        Assert-InstalltestClean $task
        return
    }
    Assert-InstalltestFresh $stateRoot $task
    Assert-InstalltestDigest $Directory
    New-Item -ItemType Directory -Force $destination | Out-Null
    Copy-Item -LiteralPath (Join-Path $Directory 'desktop-runner.exe') -Destination $executable
    Copy-Item -LiteralPath (Join-Path $Directory 'SHA256SUMS') -Destination $destination
    Assert-InstalltestDigest $destination
    Assert-InstalltestSignature $executable
    $runner = $null
    try {
        Invoke-InstalltestCommand $executable @('pair', '--url', 'https://mcp.januaport.ai') $PairCode | Out-Null
        Invoke-InstalltestCommand $executable @('install') | Out-Null
        $runner = Start-InstalltestProcess $executable @('run')
        # Drain silently; never let a full stdout/stderr pipe block the runner.
        $runner.BeginOutputReadLine()
        $runner.BeginErrorReadLine()
        Wait-InstalltestLife $executable $stateRoot $runner
    } finally {
        try { Invoke-InstalltestCommand $executable @('uninstall') | Out-Null }
        finally {
            try {
                # uninstall removes autostart, but does not stop a running runner.
                if ($runner -and -not $runner.HasExited) { $runner.Kill($true); $runner.WaitForExit() }
            } finally {
                if ($runner) { $runner.Dispose() }
                Assert-InstalltestClean $task
            }
        }
    }
}

function New-InstalltestPipe {
    param([string]$Name, [Security.Principal.SecurityIdentifier]$WorkerSID,
        [Security.Principal.SecurityIdentifier]$OwnerSID = [Security.Principal.WindowsIdentity]::GetCurrent().User)
    $security = [IO.Pipes.PipeSecurity]::new()
    $security.SetAccessRuleProtection($true, $false)
    foreach ($pipeSid in @($WorkerSID, $OwnerSID)) {
        $security.AddAccessRule([IO.Pipes.PipeAccessRule]::new($pipeSid, 'FullControl', 'Allow'))
    }
    return [IO.Pipes.NamedPipeServerStreamAcl]::Create($Name, 'Out', 1, 'Byte', 'Asynchronous', 0, 0, $security)
}

function Send-InstalltestCode {
    param([IO.Pipes.NamedPipeServerStream]$Pipe, [Threading.Tasks.Task]$Connection,
        [securestring]$PairCode, [int]$TimeoutSeconds = 45)
    if (-not $Connection.Wait($TimeoutSeconds * 1000)) { throw 'handoff_timeout' }
    $pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($PairCode)
    try {
        $writer = [IO.StreamWriter]::new($Pipe, [Text.UTF8Encoding]::new($false), 1024, $true)
        try {
            $writer.WriteLine([Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer))
            $writer.Flush()
        } finally { $writer.Dispose() }
    } finally { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer) }
}

function Invoke-InstalltestStandardAccount {
    param([string]$ReleaseVersion, [securestring]$PairCode, [string]$Directory)
    if ($env:GITHUB_ACTIONS -ne 'true') { throw 'ci_only' }
    $probeUser = 'JnptIT' + [guid]::NewGuid().ToString('N').Substring(0, 8)
    $password = ConvertTo-SecureString ([guid]::NewGuid().ToString('N') + '!Aa1') -AsPlainText -Force
    $user = New-LocalUser -Name $probeUser -Password $password -AccountNeverExpires
    $pipe = $null
    $child = $null
    $credential = [pscredential]::new($probeUser, $password)
    try {
        Add-LocalGroupMember -SID 'S-1-5-32-545' -Member $user
        Invoke-InstalltestCommand (Join-Path $env:WINDIR 'System32/icacls.exe') @(
            $Directory, '/grant', "${probeUser}:(OI)(CI)RX") | Out-Null
        $script = Join-Path $Directory 'installtest.ps1'
        Copy-Item -LiteralPath $PSCommandPath -Destination $script
        $pipeName = 'jnpt-installtest-' + [guid]::NewGuid().ToString('N')
        $pipe = New-InstalltestPipe $pipeName $user.SID
        $connection = $pipe.WaitForConnectionAsync()
        $arguments = @('-NoProfile', '-File', $script, '-Version', $ReleaseVersion,
            '-PipeName', $pipeName, '-VerifiedDirectory', $Directory)
        $child = Start-InstalltestProcess (Join-Path $PSHOME 'pwsh.exe') $arguments -Credential $credential
        $child.BeginOutputReadLine()
        $child.BeginErrorReadLine()
        Send-InstalltestCode $pipe $connection $PairCode
        Wait-InstalltestProcessExit $child 480
    } finally {
        try {
            if ($child) {
                if (-not $child.HasExited) { $child.Kill($true); $child.WaitForExit() }
                $child.Dispose()
            }
            if ($pipe) { $pipe.Dispose() }
            # A bounded second cleanup also covers a killed/failed worker.
            if (Test-Path -LiteralPath (Join-Path $Directory 'installtest.ps1')) {
                $cleanup = Start-InstalltestProcess (Join-Path $PSHOME 'pwsh.exe') @('-NoProfile', '-File',
                    (Join-Path $Directory 'installtest.ps1'), '-Version', $ReleaseVersion, '-CleanupOnly') -Credential $credential
                Complete-InstalltestProcess $cleanup 45 | Out-Null
            }
        } finally { Remove-LocalUser -Name $probeUser }
    }
}

function Remove-InstalltestTemporaryDirectory {
    param([string]$Directory, [string]$Parent)
    $resolved = [IO.Path]::GetFullPath($Directory)
    if ([IO.Path]::GetDirectoryName($resolved) -ne [IO.Path]::GetFullPath($Parent).TrimEnd('\', '/')) { throw 'temporary_path_invalid' }
    Remove-Item -LiteralPath $resolved -Recurse -Force
}

function Get-InstalltestCode {
    param([securestring]$InputCode, [switch]$RequireEnvironment)
    if ($InputCode) { return $InputCode }
    if (-not [string]::IsNullOrWhiteSpace($env:SHOWCASE_PAIR_CODE)) {
        return ConvertTo-SecureString $env:SHOWCASE_PAIR_CODE -AsPlainText -Force
    }
    if ($RequireEnvironment) { throw 'pair_code_missing' }
    return Read-Host 'Einmaliger Showcase-Kopplungscode' -AsSecureString
}

function Invoke-Installtest {
    if ($CleanupOnly) { Invoke-InstalltestWorker $Version -OnlyCleanup; return }
    if ($PipeName) {
        $pipe = [IO.Pipes.NamedPipeClientStream]::new('.', $PipeName, 'In')
        try {
            $pipe.Connect(30000)
            $reader = [IO.StreamReader]::new($pipe)
            try { $Code = ConvertTo-SecureString ($reader.ReadLine()) -AsPlainText -Force }
            finally { $reader.Dispose() }
        } finally { $pipe.Dispose() }
        Invoke-InstalltestWorker $Version $Code $VerifiedDirectory
        return
    }
    $Code = Get-InstalltestCode $Code -RequireEnvironment:$CreateStandardUser
    [Environment]::SetEnvironmentVariable('SHOWCASE_PAIR_CODE', $null, 'Process')
    if (-not $CreateStandardUser) { Assert-InstalltestStandardUser }
    $temporaryRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
    $directory = Join-Path $temporaryRoot ('jnpt-installtest-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory $directory | Out-Null
    try {
        Write-Host 'Installtest: Release laden und SHA256, Herausgeber, Provenance prüfen.'
        $repo = 'JanuaPort/januaport-plugins'
        $tag = "desktop-runner-v$Version"
        foreach ($asset in @('desktop-runner.exe', 'SHA256SUMS', 'provenance.jsonl')) {
            Invoke-WebRequest "https://github.com/$repo/releases/download/$tag/$asset" -OutFile (Join-Path $directory $asset)
        }
        Assert-InstalltestDigest $directory
        Assert-InstalltestSignature (Join-Path $directory 'desktop-runner.exe')
        Invoke-InstalltestAttestation (Join-Path $directory 'desktop-runner.exe') (Join-Path $directory 'provenance.jsonl') $Version
        if ($CreateStandardUser) { Invoke-InstalltestStandardAccount $Version $Code $directory }
        else { Invoke-InstalltestWorker $Version $Code $directory }
        Write-Host 'Installtest: Standardkonto, Kopplung, Installation, 5 Minuten Betrieb und Deinstallation geprüft.'
        Write-Host 'Showcase-Heartbeat, Auftrag ok, Admin-Abbruch und Widerruf: separater GATE-Beleg erforderlich.'
    } finally {
        # Resolve and constrain before recursive removal, even on failure.
        Remove-InstalltestTemporaryDirectory $directory $temporaryRoot
    }
}

if ($MyInvocation.InvocationName -ne '.') {
    try { Invoke-Installtest }
    catch {
        # Native output, argv and exception detail may contain the pairing code.
        # Emit only this fixed diagnostic, never $_ / an ErrorRecord.
        Write-Host 'Installtest fehlgeschlagen; Releaseprüfung, Standardkonto, Betrieb oder Cleanup nicht bestanden.'
        exit 1
    }
}
