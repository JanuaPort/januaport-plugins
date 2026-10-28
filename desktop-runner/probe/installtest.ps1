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

# Get-InstalltestErrorClass gibt nur feste Klassen aus (eigene throw-Namen aus
# Kleinbuchstaben und Unterstrich); alles andere wird zu „unbekannt“, damit nie
# ein Exception-Text mit Code oder nativer Ausgabe im Log landet.
function Get-InstalltestErrorClass {
    param([Management.Automation.ErrorRecord]$Record)
    $message = [string]$Record.Exception.Message
    if ($message -cmatch '\A[a-z][a-z0-9_]{2,80}\z') { return $message }
    # Fremde Fehler: nur Typname und Skriptzeile, nie der Text (er kann Ausgabe enthalten).
    $type = ($Record.Exception.GetType().Name.ToLowerInvariant() -replace '[^a-z0-9]', '')
    if ($type.Length -gt 48) { $type = $type.Substring(0, 48) }
    $line = 0
    if ($Record.InvocationInfo) { $line = $Record.InvocationInfo.ScriptLineNumber }
    if ($type) { return ('ps_' + $type + '_l' + $line) }
    return 'unbekannt'
}

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
    param([Diagnostics.Process]$Process, [int]$TimeoutSeconds = 30, [string]$Step = 'cmd')
    if (-not $Process.WaitForExit($TimeoutSeconds * 1000)) {
        $Process.Kill($true)
        $Process.WaitForExit()
        throw ('process_timeout_' + $Step)
    }
    # Schritt und Exitcode (hex, ohne Ausgabe) machen den Fehler unterscheidbar.
    if ($Process.ExitCode -ne 0) { throw ('process_failed_' + $Step + '_' + ('{0:x8}' -f $Process.ExitCode)) }
}

function Complete-InstalltestProcess {
    param([Diagnostics.Process]$Process, [int]$TimeoutSeconds = 30, [string]$Step = 'cmd')
    $output = $Process.StandardOutput.ReadToEndAsync()
    $null = $Process.StandardError.ReadToEndAsync()
    try {
        Wait-InstalltestProcessExit $Process $TimeoutSeconds $Step
        # Callers use only status output; pair output is discarded even on success.
        return $output.GetAwaiter().GetResult()
    } finally {
        $Process.Dispose()
    }
}

function Invoke-InstalltestCommand {
    param([string]$FilePath, [string[]]$Arguments, [securestring]$PairCode, [int]$TimeoutSeconds = 30, [string]$Step = 'cmd')
    Complete-InstalltestProcess (Start-InstalltestProcess $FilePath $Arguments $PairCode) $TimeoutSeconds $Step
}

function Invoke-InstalltestAttestation {
    param([string]$Executable, [string]$Bundle, [string]$ReleaseVersion)
    $repo = 'JanuaPort/januaport-plugins'
    $verifyArguments = @('attestation', 'verify', $Executable, '--bundle', $Bundle, '--repo', $repo,
        '--signer-workflow', "$repo/.github/workflows/desktop-runner-release.yml",
        '--source-ref', "refs/tags/desktop-runner-v$ReleaseVersion")
    # Der erste Lauf lädt die Sigstore-Vertrauensbasis; 30 s reichen dafür nicht sicher.
    Invoke-InstalltestCommand 'gh' $verifyArguments -TimeoutSeconds 120 -Step 'gh' | Out-Null
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
        $status = Invoke-InstalltestCommand $Executable ($PrefixArguments + @('status')) -TimeoutSeconds $remaining -Step 'status'
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
    # Ohne geladenes Profil ist der Pfad leer; dann eigene Klasse statt copy_failed.
    if ([string]::IsNullOrWhiteSpace($env:LOCALAPPDATA) -or -not [IO.Path]::IsPathRooted($env:LOCALAPPDATA)) { throw 'profile_not_loaded' }
    Write-Host 'Installtest: Profil geladen.'
    $stateRoot = Join-Path $env:LOCALAPPDATA 'JanuaPort/desktop-runner'
    $destination = Join-Path $env:LOCALAPPDATA "JanuaPort/installtest/$ReleaseVersion"
    $executable = Join-Path $destination 'desktop-runner.exe'
    if ($OnlyCleanup) {
        if (Test-Path -LiteralPath $executable) { Invoke-InstalltestCommand $executable @('uninstall') -Step 'uninstall' | Out-Null }
        Assert-InstalltestClean $task
        return
    }
    Assert-InstalltestFresh $stateRoot $task
    Assert-InstalltestDigest $Directory
    try { New-Item -ItemType Directory -Force $destination | Out-Null }
    catch { throw ('mkdir_failed_' + (Get-InstalltestErrorClass $_)) }
    try { Copy-Item -LiteralPath (Join-Path $Directory 'desktop-runner.exe') -Destination $executable }
    catch { throw ('copy_exe_failed_' + (Get-InstalltestErrorClass $_)) }
    try { Copy-Item -LiteralPath (Join-Path $Directory 'SHA256SUMS') -Destination $destination }
    catch { throw ('copy_sums_failed_' + (Get-InstalltestErrorClass $_)) }
    Write-Host 'Installtest: Laeufer im Profil abgelegt.'
    Assert-InstalltestDigest $destination
    Assert-InstalltestSignature $executable
    $runner = $null
    try {
        Invoke-InstalltestCommand $executable @('pair', '--url', 'https://mcp.januaport.ai') $PairCode -Step 'pair' | Out-Null
        Write-Host 'Installtest: gekoppelt.'
        Invoke-InstalltestCommand $executable @('install') -Step 'install' | Out-Null
        Write-Host 'Installtest: installiert.'
        try { $runner = Start-InstalltestProcess $executable @('run') } catch { throw 'run_start_failed' }
        # Drain silently; never let a full stdout/stderr pipe block the runner.
        $runner.BeginOutputReadLine()
        $runner.BeginErrorReadLine()
        Wait-InstalltestLife $executable $stateRoot $runner
    } finally {
        try { Invoke-InstalltestCommand $executable @('uninstall') -Step 'uninstall' | Out-Null }
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

function New-InstalltestProfile {
    param([string]$Sid, [string]$UserName)
    if (-not ('JnptInstalltest.UserEnv' -as [type])) {
        Add-Type -Namespace JnptInstalltest -Name UserEnv -MemberDefinition @'
[System.Runtime.InteropServices.DllImport("userenv.dll", CharSet = System.Runtime.InteropServices.CharSet.Unicode)]
public static extern int CreateProfile(string pszUserSid, string pszUserName, System.Text.StringBuilder pszProfilePath, uint cchProfilePath);
'@
    }
    $path = [Text.StringBuilder]::new(260)
    $hr = [JnptInstalltest.UserEnv]::CreateProfile($Sid, $UserName, $path, 260)
    # 0x800700B7: Profil existiert bereits.
    if ($hr -ne 0 -and $hr -ne -2147024713) { throw ('profile_create_failed_' + ('{0:x8}' -f $hr)) }
}

# Get-InstalltestWorkerClass liest aus der Ausgabe des Kind-Prozesses nur die
# feste Fehlerzeile des eigenen Skripts.
function Get-InstalltestWorkerClass {
    param([string]$Output)
    foreach ($line in ($Output -split "`r?`n")) {
        if ($line -cmatch '\AInstalltest fehlgeschlagen; Fehlerklasse: ([a-z][a-z0-9_]{2,60})\.\z') { return $Matches[1] }
    }
    return $null
}

# Write-InstalltestWorkerLines reicht nur die festen Zwischenzeilen weiter.
function Write-InstalltestWorkerLines {
    param([string]$Output)
    foreach ($line in ($Output -split "`r?`n")) {
        if ($line -cmatch '\AInstalltest: [A-Za-zÄÖÜäöüß0-9 ,.;()-]{1,120}\z') { Write-Host $line }
    }
}

function Invoke-InstalltestStandardAccount {
    param([string]$ReleaseVersion, [securestring]$PairCode, [string]$Directory)
    if ($env:GITHUB_ACTIONS -ne 'true') { throw 'ci_only' }
    $probeUser = 'JnptIT' + [guid]::NewGuid().ToString('N').Substring(0, 8)
    $password = ConvertTo-SecureString ([guid]::NewGuid().ToString('N') + '!Aa1') -AsPlainText -Force
    $user = New-LocalUser -Name $probeUser -Password $password -AccountNeverExpires
    # Process.Start mit Credential lädt ein Profil nur, wenn es schon existiert.
    # Beim frischen Konto legt CreateProfile (userenv) es vorher an.
    New-InstalltestProfile $user.SID.Value $probeUser
    $pipe = $null
    $child = $null
    $credential = [pscredential]::new($probeUser, $password)
    try {
        Add-LocalGroupMember -SID 'S-1-5-32-545' -Member $user
        Invoke-InstalltestCommand (Join-Path $env:WINDIR 'System32/icacls.exe') @(
            # /T: auch die schon heruntergeladenen Dateien bekommen das Leserecht.
            $Directory, '/grant', "${probeUser}:(OI)(CI)RX", '/T') -Step 'icacls' | Out-Null
        $script = Join-Path $Directory 'installtest.ps1'
        Copy-Item -LiteralPath $PSCommandPath -Destination $script
        $pipeName = 'jnpt-installtest-' + [guid]::NewGuid().ToString('N')
        $pipe = New-InstalltestPipe $pipeName $user.SID
        $connection = $pipe.WaitForConnectionAsync()
        $arguments = @('-NoProfile', '-File', $script, '-Version', $ReleaseVersion,
            '-PipeName', $pipeName, '-VerifiedDirectory', $Directory)
        try {
            $child = Start-InstalltestProcess (Join-Path $PSHOME 'pwsh.exe') $arguments -Credential $credential
        } catch { throw 'worker_start_failed' }
        $childOutput = $child.StandardOutput.ReadToEndAsync()
        $null = $child.StandardError.ReadToEndAsync()
        Send-InstalltestCode $pipe $connection $PairCode
        try { Wait-InstalltestProcessExit $child 480 'worker' }
        catch {
            # Nur feste Zeilen des eigenen Skripts weiterreichen, nie native Ausgabe.
            Write-InstalltestWorkerLines $childOutput.GetAwaiter().GetResult()
            $class = Get-InstalltestWorkerClass $childOutput.GetAwaiter().GetResult()
            if ($class) { throw ('worker_' + $class) }
            throw
        }
        Write-InstalltestWorkerLines $childOutput.GetAwaiter().GetResult()
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
        Write-Host 'Installtest: Standardkonto laeuft.'
        try {
            $pipe = [IO.Pipes.NamedPipeClientStream]::new('.', $PipeName, 'In')
            try {
                $pipe.Connect(30000)
                $reader = [IO.StreamReader]::new($pipe)
                try { $Code = ConvertTo-SecureString ($reader.ReadLine()) -AsPlainText -Force }
                finally { $reader.Dispose() }
            } finally { $pipe.Dispose() }
        } catch { throw 'pipe_read_failed' }
        Write-Host 'Installtest: Code uebernommen.'
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
        try {
            foreach ($asset in @('desktop-runner.exe', 'SHA256SUMS', 'provenance.jsonl')) {
                Invoke-WebRequest "https://github.com/$repo/releases/download/$tag/$asset" -OutFile (Join-Path $directory $asset)
            }
        } catch { throw 'download_failed' }
        Assert-InstalltestDigest $directory
        Write-Host 'Installtest: SHA256 ok.'
        Assert-InstalltestSignature (Join-Path $directory 'desktop-runner.exe')
        Write-Host 'Installtest: Herausgeber ok.'
        try {
            Invoke-InstalltestAttestation (Join-Path $directory 'desktop-runner.exe') (Join-Path $directory 'provenance.jsonl') $Version
        } catch { throw ('provenance_' + (Get-InstalltestErrorClass $_)) }
        Write-Host 'Installtest: Provenance ok.'
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
        Write-Host ('Installtest fehlgeschlagen; Fehlerklasse: ' + (Get-InstalltestErrorClass $_) + '.')
        exit 1
    }
}
