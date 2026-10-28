# Offline only. Mutate isolated script copies, execute the real Windows adapters,
# require a red target test, then remove copies. Production source stays byte-identical.
[CmdletBinding()]
param()
$ErrorActionPreference = 'Stop'
$sourcePath = Join-Path $PSScriptRoot 'installtest.ps1'
$testPath = Join-Path $PSScriptRoot 'installtest-tests.ps1'
$original = [IO.File]::ReadAllBytes($sourcePath)
$source = [Text.Encoding]::UTF8.GetString($original)
& $testPath
$mutations = @(
    @('version', "[ValidatePattern('\A[0-9]+\.[0-9]+\.[0-9]+\z')]", ''),
    @('checksum_format', "throw 'checksum_format'", 'return'),
    @('checksum_mismatch', "throw 'checksum_mismatch'", 'return'),
    @('signature_invalid', "throw 'signature_invalid'", 'return'),
    @('publisher_invalid', "throw 'publisher_invalid'", 'return'),
    @('output_secret', "throw 'process_failed'", 'return'),
    @('environment_secret', "`$info.Environment.Remove('SHOWCASE_PAIR_CODE') | Out-Null", ''),
    @('pair_code_missing', "throw 'pair_code_missing'", 'return'),
    @('process_start', "throw 'process_start_failed'", 'return'),
    @('process_timeout', 'if (-not $Process.WaitForExit($TimeoutSeconds * 1000))', 'if ($false)'),
    @('profile_in_use', "throw 'profile_in_use'", 'return'),
    @('run_residue', 'return $taskPresent -or $runPresent', 'return $taskPresent'),
    @('task_residue', 'return $taskPresent -or $runPresent', 'return $runPresent'),
    @('pipe_access', '$security.SetAccessRuleProtection($true, $false)',
        '$security.SetAccessRuleProtection($true, $false); $security.AddAccessRule([IO.Pipes.PipeAccessRule]::new([Security.Principal.SecurityIdentifier]::new("S-1-1-0"), "FullControl", "Allow"))'),
    @('handoff_timeout', 'if (-not $Connection.Wait($TimeoutSeconds * 1000))', 'if ($false)'),
    @('temporary_path', "throw 'temporary_path_invalid'", '$null = 0'),
    @('status_invalid', "throw 'status_invalid'", 'return'),
    @('runner_exited', "throw 'runner_exited'", 'return'),
    @('life_timeout', "throw 'life_timeout'", 'return'),
    @('ci_only', "throw 'ci_only'", 'return')
)
$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
try {
    if (@($identity.Groups.Value) -contains 'S-1-5-32-544') {
        $mutations += ,@('standard_account', "throw 'standard_account_required'", 'return')
    } else { Write-Host 'SKIP mutation standard_account: needs admin/UAC identity.' }
} finally { $identity.Dispose() }
$tempParent = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
$temporary = Join-Path $tempParent ('jnpt-installtest-mutants-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory $temporary | Out-Null
try {
    foreach ($mutation in $mutations) {
        $name, $before, $after = $mutation
        if (-not $source.Contains($before)) { throw "mutation_target_missing: $name" }
        $mutant = Join-Path $temporary "$name.ps1"
        [IO.File]::WriteAllText($mutant, $source.Replace($before, $after), [Text.UTF8Encoding]::new($false))
        # Parser errors are never a successful red mutation.
        [ScriptBlock]::Create([IO.File]::ReadAllText($mutant)) | Out-Null
        $red = $false
        try { & $testPath -ScriptPath $mutant -Only $name }
        catch { $red = $true }
        if (-not $red) { throw "mutation_survived: $name" }
        Write-Host "RED $name"
    }
} finally {
    $resolved = [IO.Path]::GetFullPath($temporary)
    if ([IO.Path]::GetDirectoryName($resolved) -ne $tempParent.TrimEnd('\', '/')) { throw 'mutation_path_invalid' }
    Remove-Item -LiteralPath $resolved -Recurse -Force
    if ([Convert]::ToBase64String([IO.File]::ReadAllBytes($sourcePath)) -ne [Convert]::ToBase64String($original)) {
        throw 'production_source_changed'
    }
}
