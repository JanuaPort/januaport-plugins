# Offline adapter tests only: synthetic files/processes/DPAPI/COM/HKCU/pipes.
# Default: no gh, runner, downloads or Showcase. -ReleaseDirectory explicitly
# enables only the real gh provenance adapter (never executes the released EXE).
[CmdletBinding()]
param([string]$ScriptPath = (Join-Path $PSScriptRoot 'installtest.ps1'),
    [string]$Only = '*', [string]$ReleaseDirectory, [string]$ReleaseVersion = '0.1.0')
$ErrorActionPreference = 'Stop'
. $ScriptPath
. (Join-Path $PSScriptRoot 'installtest-fixture.ps1') -Operation library
$Fixture = Join-Path $PSHOME 'pwsh.exe'
$fixturePrefix = @('-NoProfile', '-File', (Join-Path $PSScriptRoot 'installtest-fixture.ps1'))

function Start-Fixture {
    param([string[]]$Arguments)
    Start-InstalltestProcess $Fixture ($fixturePrefix + $Arguments)
}

function Invoke-Fixture {
    param([string[]]$Arguments, [securestring]$PairCode)
    Invoke-InstalltestCommand $Fixture ($fixturePrefix + $Arguments) $PairCode
}

function Assert-Rejected {
    param([scriptblock]$Action, [string]$Reason)
    try { & $Action | Out-Null }
    catch {
        if ($Reason -and $_.Exception.Message -cnotlike $Reason) { throw "wrong_rejection: $($_.Exception.Message)" }
        return
    }
    throw 'forbidden_operation_accepted'
}

$testRoot = Join-Path ([IO.Path]::GetTempPath()) ('jnpt-installtest-tests-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory $testRoot | Out-Null
try {
    $tests = [ordered]@{
        version = {
            Assert-Rejected { & { . $ScriptPath -Version '0.1.0/../other' } }
            Assert-Rejected { & { . $ScriptPath -Version "0.1.0`n" } }
        }
        checksum_format = {
            [IO.File]::WriteAllText((Join-Path $testRoot 'desktop-runner.exe'), 'synthetic')
            [IO.File]::WriteAllText((Join-Path $testRoot 'SHA256SUMS'), 'not-a-checksum')
            Assert-Rejected { Assert-InstalltestDigest $testRoot } 'checksum_format'
            [IO.File]::WriteAllText((Join-Path $testRoot 'SHA256SUMS'), "not-a-checksum`nnot-a-checksum")
            Assert-Rejected { Assert-InstalltestDigest $testRoot } 'checksum_format'
        }
        checksum_mismatch = {
            [IO.File]::WriteAllText((Join-Path $testRoot 'desktop-runner.exe'), 'tampered')
            [IO.File]::WriteAllText((Join-Path $testRoot 'SHA256SUMS'), ((('0' * 64) + ' *desktop-runner.exe') + "`n" + (('0' * 64) + ' *jnpt-bedienfeld.exe')))
            Assert-Rejected { Assert-InstalltestDigest $testRoot } 'checksum_mismatch'
            [IO.File]::WriteAllText((Join-Path $testRoot 'jnpt-bedienfeld.exe'), 'synthetic-panel')
            $panelHash = (Get-FileHash (Join-Path $testRoot 'jnpt-bedienfeld.exe')).Hash.ToLowerInvariant()
            $hash = (Get-FileHash (Join-Path $testRoot 'desktop-runner.exe')).Hash.ToLowerInvariant()
            [IO.File]::WriteAllText((Join-Path $testRoot 'SHA256SUMS'), "$hash *desktop-runner.exe`n$panelHash *jnpt-bedienfeld.exe`n")
            Assert-InstalltestDigest $testRoot
        }
        panel_checksum_missing = {
            [IO.File]::WriteAllText((Join-Path $testRoot 'desktop-runner.exe'), 'synthetic')
            $hash = (Get-FileHash (Join-Path $testRoot 'desktop-runner.exe')).Hash.ToLowerInvariant()
            [IO.File]::WriteAllText((Join-Path $testRoot 'SHA256SUMS'), "$hash *desktop-runner.exe`n")
            Assert-Rejected { Assert-InstalltestDigest $testRoot } 'checksum_format'
        }
        panel_checksum_mismatch = {
            [IO.File]::WriteAllText((Join-Path $testRoot 'desktop-runner.exe'), 'synthetic')
            [IO.File]::WriteAllText((Join-Path $testRoot 'jnpt-bedienfeld.exe'), 'tampered-panel')
            $hash = (Get-FileHash (Join-Path $testRoot 'desktop-runner.exe')).Hash.ToLowerInvariant()
            [IO.File]::WriteAllText((Join-Path $testRoot 'SHA256SUMS'), ("$hash *desktop-runner.exe`n" + ('0' * 64) + ' *jnpt-bedienfeld.exe'))
            Assert-Rejected { Assert-InstalltestDigest $testRoot } 'checksum_mismatch'
        }
        panel_arguments = {
            $fixtureTask = New-InstalltestTaskFixture $Fixture 'forbidden-argument'
            try {
                Assert-Rejected { Assert-InstalltestEntry $fixtureTask.Name 'absent-synthetic-run' $Fixture -Panel } 'panel_arguments'
            } finally { Remove-InstalltestTaskFixture $fixtureTask }
        }
        panel_run_arguments = {
            $value = 'JanuaPort-L6-Test-' + [guid]::NewGuid().ToString('N')
            $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Software\Microsoft\Windows\CurrentVersion\Run')
            try {
                $key.SetValue($value, ('"' + $Fixture + '" forbidden-argument'))
                Assert-Rejected { Assert-InstalltestEntry 'absent-synthetic-task' $value $Fixture -Panel } 'run_command_invalid'
                $key.SetValue($value, ('"' + $Fixture + '"'))
                Assert-InstalltestEntry 'absent-synthetic-task' $value $Fixture -Panel
            } finally { $key.DeleteValue($value); $key.Dispose() }
        }
        panel_task_missing = {
            Assert-Rejected { Assert-InstalltestEntry 'absent-synthetic-task' 'absent-synthetic-value' $Fixture -Panel } 'autostart_missing'
        }
        runner_arguments = {
            $fixtureTask = New-InstalltestTaskFixture $Fixture 'forbidden-argument'
            try {
                Assert-Rejected { Assert-InstalltestEntry $fixtureTask.Name 'absent-synthetic-run' $Fixture } 'runner_arguments'
            } finally { Remove-InstalltestTaskFixture $fixtureTask }
        }
        task_command_invalid = {
            $fixtureTask = New-InstalltestTaskFixture $Fixture ''
            try {
                Assert-Rejected { Assert-InstalltestEntry $fixtureTask.Name 'absent-synthetic-run' ($Fixture + '.wrong') -Panel } 'task_command_invalid'
            } finally { Remove-InstalltestTaskFixture $fixtureTask }
        }
        task_principal_invalid = {
            $fixtureTask = New-InstalltestTaskFixture $Fixture '' -NoTrigger
            try {
                Assert-Rejected { Assert-InstalltestEntry $fixtureTask.Name 'absent-synthetic-run' $Fixture -Panel } 'task_principal_invalid'
            } finally { Remove-InstalltestTaskFixture $fixtureTask }
        }
        signature_invalid = {
            $unsigned = Join-Path $testRoot 'unsigned.ps1'
            [IO.File]::WriteAllText($unsigned, '# synthetic unsigned script')
            Assert-Rejected { Assert-InstalltestSignature $unsigned } 'signature_invalid'
        }
        publisher_invalid = {
            # Trusted Microsoft signature, wrong publisher. Actual Authenticode API.
            Assert-Rejected { Assert-InstalltestSignature (Join-Path $PSHOME 'pwsh.exe') } 'publisher_invalid'
        }
        output_secret = {
            $sentinel = 'synthetic-secret-' + [guid]::NewGuid().ToString('N')
            $secret = ConvertTo-SecureString $sentinel -AsPlainText -Force
            $captured = @(& {
                try { Invoke-Fixture @('fail') $secret }
                catch { $_.Exception.Message }
            } *>&1) -join "`n"
            if ($captured.Contains($sentinel) -or $captured.Trim() -cnotlike 'process_failed_cmd_*') { throw 'secret_or_failure_not_suppressed' }
            # Verify native argv escaping/transmission with a synthetic code.
            if ((Invoke-Fixture @('code') $secret) -ne $sentinel) { throw 'argv_handoff_failed' }
        }
        environment_secret = {
            $previous = $env:SHOWCASE_PAIR_CODE
            try {
                $env:SHOWCASE_PAIR_CODE = 'synthetic-env-secret'
                if (Invoke-Fixture @('environment')) { throw 'secret_inherited' }
            } finally { $env:SHOWCASE_PAIR_CODE = $previous }
        }
        pair_code_missing = {
            $previous = $env:SHOWCASE_PAIR_CODE
            try {
                $env:SHOWCASE_PAIR_CODE = $null
                Assert-Rejected { Get-InstalltestCode -RequireEnvironment } 'pair_code_missing'
            } finally { $env:SHOWCASE_PAIR_CODE = $previous }
        }
        process_start = {
            Assert-Rejected { Invoke-InstalltestCommand (Join-Path $testRoot 'missing.exe') @('run') } 'process_start_failed'
        }
        acl_command = {
            Assert-Rejected {
                Invoke-InstalltestCommand (Join-Path $env:WINDIR 'System32/icacls.exe') @(
                    (Join-Path $testRoot 'missing-directory'), '/grant', "${env:USERNAME}:(OI)(CI)RX")
            } 'process_failed_cmd_*'
        }
        process_timeout = {
            $process = Start-Fixture @('run')
            $idToCheck = $process.Id
            try {
                Assert-Rejected { Complete-InstalltestProcess $process 1 } 'process_timeout_cmd'
                if (Get-Process -Id $idToCheck -ErrorAction SilentlyContinue) { throw 'timed_out_process_survived' }
            } finally { Stop-Process -Id $idToCheck -Force -ErrorAction SilentlyContinue }
        }
        profile_in_use = {
            $state = Join-Path $testRoot 'existing-state'
            New-Item -ItemType Directory -Force $state | Out-Null
            Assert-Rejected { Assert-InstalltestFresh $state ('absent-' + [guid]::NewGuid()) } 'profile_in_use'
        }
        run_residue = {
            $value = 'JanuaPort-Installtest-Test-' + [guid]::NewGuid().ToString('N')
            $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Software\Microsoft\Windows\CurrentVersion\Run')
            try {
                $key.SetValue($value, 'synthetic-never-executed')
                Assert-Rejected { Assert-InstalltestClean 'absent-synthetic-task' $value } 'autostart_remaining'
            } finally { $key.DeleteValue($value); $key.Dispose() }
            Assert-InstalltestClean 'absent-synthetic-task' $value
        }
        task_residue = {
            $service = New-Object -ComObject 'Schedule.Service'
            $service.Connect()
            $folder = $service.GetFolder('\')
            $definition = $service.NewTask(0)
            $definition.Principal.LogonType = 3
            $definition.Principal.RunLevel = 0
            $action = $definition.Actions.Create(0)
            $action.Path = $Fixture
            $action.Arguments = 'code synthetic'
            # No trigger: the synthetic task is registered but never executed.
            $name = 'JanuaPort-Installtest-Test-' + [guid]::NewGuid().ToString('N')
            try {
                $registered = $folder.RegisterTaskDefinition($name, $definition, 2, $null, $null, 3)
                [Runtime.InteropServices.Marshal]::FinalReleaseComObject($registered) | Out-Null
                Assert-Rejected { Assert-InstalltestClean $name 'absent-synthetic-value' } 'autostart_remaining'
            } finally {
                $folder.DeleteTask($name, 0)
                foreach ($com in @($action, $definition, $folder, $service)) {
                    [Runtime.InteropServices.Marshal]::FinalReleaseComObject($com) | Out-Null
                }
            }
            Assert-InstalltestClean $name 'absent-synthetic-value'
        }
        pipe_access = {
            $name = 'jnpt-installtest-test-' + [guid]::NewGuid().ToString('N')
            # Only SYSTEM/LocalService allowed; current test identity is neither.
            $server = New-InstalltestPipe $name 'S-1-5-19' 'S-1-5-18'
            $client = [IO.Pipes.NamedPipeClientStream]::new('.', $name, 'In')
            try { Assert-Rejected { $client.Connect(1000) } }
            finally { $client.Dispose(); $server.Dispose() }
            # Also exercise a real permitted connection using the production ACL.
            $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User
            $server = New-InstalltestPipe $name $sid
            $client = [IO.Pipes.NamedPipeClientStream]::new('.', $name, 'In')
            try {
                $pending = $server.WaitForConnectionAsync()
                $client.Connect(1000)
                if (-not $pending.Wait(1000)) { throw 'permitted_pipe_failed' }
            } finally { $client.Dispose(); $server.Dispose() }
        }
        handoff_timeout = {
            $name = 'jnpt-installtest-test-' + [guid]::NewGuid().ToString('N')
            $server = New-InstalltestPipe $name ([Security.Principal.WindowsIdentity]::GetCurrent().User)
            try {
                $pending = $server.WaitForConnectionAsync()
                $secret = ConvertTo-SecureString 'synthetic' -AsPlainText -Force
                Assert-Rejected { Send-InstalltestCode $server $pending $secret 1 } 'handoff_timeout'
            } finally { $server.Dispose() }
        }
        temporary_path = {
            $victim = Join-Path $testRoot 'must-survive'
            New-Item -ItemType Directory $victim | Out-Null
            Assert-Rejected { Remove-InstalltestTemporaryDirectory $victim (Join-Path $testRoot 'other-parent') } 'temporary_path_invalid'
            if (-not (Test-Path -LiteralPath $victim)) { throw 'unrelated_directory_deleted' }
        }
        manifest_dpapi = {
            $path = Join-Path $testRoot 'manifest'
            [IO.File]::WriteAllText($path, 'invalid-dpapi')
            Assert-Rejected { Read-InstalltestManifest $testRoot }
            $clear = [Text.Encoding]::UTF8.GetBytes('{"manifest":{"last_sync":"2026-10-28T12:00:00Z"}}')
            $entropy = [Text.Encoding]::UTF8.GetBytes('JanuaPort/Desktop-Laeufer/v1/CurrentUser')
            $sealed = [Security.Cryptography.ProtectedData]::Protect($clear, $entropy, 'CurrentUser')
            [IO.File]::WriteAllBytes($path, $sealed)
            if (-not (Read-InstalltestManifest $testRoot).manifest.last_sync) { throw 'manifest_read_failed' }
            Remove-Item -LiteralPath $path
        }
        status_invalid = {
            $previous = $env:JNPT_INSTALLTEST_STATUS
            $runner = Start-Fixture @('run')
            try {
                $env:JNPT_INSTALLTEST_STATUS = 'invalid'
                Assert-Rejected { Wait-InstalltestLife $Fixture $testRoot $runner 2 $fixturePrefix } 'status_invalid'
            } finally {
                $env:JNPT_INSTALLTEST_STATUS = $previous
                $runner.Kill($true); $runner.WaitForExit(); $runner.Dispose()
            }
        }
        runner_exited = {
            $runner = Start-Fixture @('code', 'synthetic')
            try {
                $runner.WaitForExit()
                Assert-Rejected { Wait-InstalltestLife $Fixture $testRoot $runner 2 $fixturePrefix } 'runner_exited'
            } finally { $runner.Dispose() }
        }
        life_timeout = {
            $runner = Start-Fixture @('run')
            try { Assert-Rejected { Wait-InstalltestLife $Fixture $testRoot $runner 2 $fixturePrefix } 'life_timeout' }
            finally { $runner.Kill($true); $runner.WaitForExit(); $runner.Dispose() }
        }
        life_success = {
            $path = Join-Path $testRoot 'manifest'
            $clear = [Text.Encoding]::UTF8.GetBytes('{"manifest":{"last_sync":"2026-10-28T12:00:00Z"}}')
            $entropy = [Text.Encoding]::UTF8.GetBytes('JanuaPort/Desktop-Laeufer/v1/CurrentUser')
            [IO.File]::WriteAllBytes($path, [Security.Cryptography.ProtectedData]::Protect($clear, $entropy, 'CurrentUser'))
            # Regression: startup deliberately exceeds the observation window.
            # The child announces readiness after startup, before its operation.
            $lifeFixture = Join-Path $testRoot 'life-fixture.ps1'
            $fixtureScript = (Join-Path $PSScriptRoot 'installtest-fixture.ps1').Replace("'", "''")
            $lifeSource = @'
param([string]$Operation)
Start-Sleep -Milliseconds 1500
[IO.File]::WriteAllText((Join-Path $PSScriptRoot "$Operation-$PID.ready"), 'ready')
& '__FIXTURE__' $Operation
'@
            [IO.File]::WriteAllText($lifeFixture, $lifeSource.Replace('__FIXTURE__', $fixtureScript))
            $lifePrefix = @('-NoProfile', '-File', $lifeFixture)
            $runner = $null
            $statuses = [Collections.Generic.Queue[Diagnostics.Process]]::new()
            try {
                # Start all native children outside the observation window. Two
                # status samples cover one second with the real 500 ms cadence.
                $startupDeadline = [DateTime]::UtcNow.AddSeconds(30)
                $runner = Start-InstalltestProcess $Fixture ($lifePrefix + @('run'))
                for ($i = 0; $i -lt 2; $i++) {
                    $statuses.Enqueue((Start-InstalltestProcess $Fixture ($lifePrefix + @('status'))))
                }
                foreach ($child in (@($runner) + $statuses.ToArray())) {
                    $operation = if ($child.Id -eq $runner.Id) { 'run' } else { 'status' }
                    $ready = Join-Path $testRoot ("$operation-" + $child.Id + '.ready')
                    while (-not (Test-Path -LiteralPath $ready -PathType Leaf)) {
                        if ($child.HasExited) { throw 'fixture_exited_before_ready' }
                        if ([DateTime]::UtcNow -ge $startupDeadline) { throw 'fixture_ready_timeout' }
                        Start-Sleep -Milliseconds 25
                    }
                }
                # This override lives only in this test's scriptblock scope.
                # Invoke-InstalltestCommand/Complete-InstalltestProcess consume
                # real native output and retain the one-second status timeout.
                function Start-InstalltestProcess {
                    param([string]$FilePath, [string[]]$Arguments)
                    if ($FilePath -cne $Fixture -or $Arguments[-1] -cne 'status' -or $statuses.Count -eq 0) {
                        throw 'fixture_unexpected_command'
                    }
                    return $statuses.Dequeue()
                }
                $observation = [Diagnostics.Stopwatch]::StartNew()
                Wait-InstalltestLife $Fixture $testRoot $runner 1 $lifePrefix
                if ($observation.Elapsed.TotalSeconds -lt 1) { throw 'life_observation_shortened' }
            }
            finally {
                while ($statuses.Count -gt 0) {
                    $child = $statuses.Dequeue()
                    try {
                        if (-not $child.HasExited) { $child.Kill($true); $child.WaitForExit() }
                    } finally { $child.Dispose() }
                }
                if ($runner) {
                    try {
                        if (-not $runner.HasExited) { $runner.Kill($true); $runner.WaitForExit() }
                    } finally { $runner.Dispose() }
                }
                Remove-Item -LiteralPath $path
            }
        }
        standard_account = {
            $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
            try {
                if (@($identity.Groups.Value) -contains 'S-1-5-32-544') {
                    Assert-Rejected { Assert-InstalltestStandardUser } 'standard_account_required'
                } else {
                    Assert-InstalltestStandardUser
                    Write-Host 'SKIP negative standard_account: needs admin/UAC identity (Windows CI).'
                }
            } finally { $identity.Dispose() }
        }
        ci_only = {
            $previous = $env:GITHUB_ACTIONS
            try {
                $env:GITHUB_ACTIONS = 'false'
                Assert-Rejected { Invoke-InstalltestStandardAccount '0.1.0' $null $testRoot } 'ci_only'
            } finally { $env:GITHUB_ACTIONS = $previous }
        }
    }
    if ($ReleaseDirectory) {
        $tests['provenance_invalid'] = {
            $releaseExe = Join-Path $ReleaseDirectory 'desktop-runner.exe'
            $bundle = Join-Path $ReleaseDirectory 'provenance.jsonl'
            # Positive control must pass first: authentication, trust roots,
            # workflow and tag cannot be the cause of the subsequent rejection.
            Invoke-InstalltestAttestation $releaseExe $bundle $ReleaseVersion
            $tampered = Join-Path $testRoot 'tampered.exe'
            Copy-Item -LiteralPath $releaseExe -Destination $tampered
            Invoke-InstalltestAttestation $tampered $bundle $ReleaseVersion
            $stream = [IO.File]::Open($tampered, [IO.FileMode]::Append)
            try { $stream.WriteByte(0) } finally { $stream.Dispose() }
            Assert-Rejected { Invoke-InstalltestAttestation $tampered $bundle $ReleaseVersion } 'process_failed_gh_*'
        }
    } elseif ($Only -eq '*' -or $Only -eq 'provenance_invalid') {
        Write-Host 'SKIP provenance_invalid: real release assets/gh require explicit -ReleaseDirectory.'
    }
    foreach ($test in $tests.GetEnumerator()) {
        if ($test.Key -like $Only) {
            & $test.Value
            Write-Host "PASS $($test.Key)"
        }
    }
} finally {
    $resolved = [IO.Path]::GetFullPath($testRoot)
    if ([IO.Path]::GetDirectoryName($resolved) -ne [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\', '/')) { throw 'test_path_invalid' }
    Remove-Item -LiteralPath $resolved -Recurse -Force
}
