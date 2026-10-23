# Native adapter mutations; run without elevation and with the curated ZIP.
param([string[]]$Names = @())
$ErrorActionPreference = 'Stop'
$moduleRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$cases = @(
    @{Name='pair-empty-key'; File='client/http.go'; Old='|| len(key) != 32'; New='|| len(key) != 32 && false'; Test='TestPairLeererPubkeySpeichertNichts'; Package='.'},
    @{Name='pair-test-key'; File='client/http.go'; Old='== rfc8032TestPublicKey {'; New='== rfc8032TestPublicKey && false {'; Test='TestPairRFC8032PubkeySpeichertNichts'; Package='.'},
    @{Name='suspended'; File='platform/process_windows.go'; Old='windows.CREATE_SUSPENDED |'; New=''; Test='TestWindowsSuspendBisJobZuordnung'},
    @{Name='job-close'; File='platform/process_windows.go'; Old='limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE'; New='limits.BasicLimitInformation.LimitFlags = 0'; Test='TestWindowsJobCloseBeendetEnkel'},
    @{Name='task-privilege'; File='platform/install_windows.go'; Old='<RunLevel>LeastPrivilege</RunLevel>'; New='<RunLevel>HighestAvailable</RunLevel>'; Test='TestWindowsAufgabeOhneAdmin'},
    @{Name='entropy'; File='platform/security_windows.go'; Old='blob(entropy)'; New='blob(nil)'; Test='TestWindowsDPAPIBrauchtEntropie'},
    @{Name='token-env'; File='client/engine.go'; Old='Env: slices.Clone(e.ChildEnv)'; New='Env: append(slices.Clone(e.ChildEnv), "RUN_TOKEN="+runToken)'; Test='TestWindowsPythonEchterStartUndManipulation'},
    @{Name='token-argv'; File='client/engine.go'; Old='Args: []string{"-I", "-B", "bootstrap.py"}'; New='Args: []string{"-I", "-B", "bootstrap.py", runToken}'; Test='TestWindowsPythonEchterStartUndManipulation'},
    @{Name='no-site'; File='client/environment.go'; Old='".zip\n.\n../libs\n"'; New='".zip\n.\n../libs\nimport site\n"'; Test='TestWindowsPythonEchterStartUndManipulation'}
)
Push-Location $moduleRoot
try {
    foreach ($case in $cases) {
        if ($Names.Count -gt 0 -and $case.Name -notin $Names) { continue }
        $package = if ($case.Package) { $case.Package } else { "./internal/platform" }
        $path = Join-Path "$moduleRoot/internal" $case.File
        $original = [IO.File]::ReadAllBytes($path)
        $text = [Text.Encoding]::UTF8.GetString($original)
        if (-not $text.Contains($case.Old)) { throw "guard_missing: $($case.Name)" }
        try {
            [IO.File]::WriteAllText($path, $text.Replace($case.Old, $case.New), [Text.UTF8Encoding]::new($false))
            go test -c -o .probe/native-mutation.exe $package
            if ($LASTEXITCODE -ne 0) { throw "compile_failure_does_not_count: $($case.Name)" }
            Push-Location $package
            try {
                $output = & "$moduleRoot/.probe/native-mutation.exe" "-test.run=^$($case.Test)$" '-test.timeout=25s' 2>&1
                $exitCode = $LASTEXITCODE
            } finally { Pop-Location }
            if ($exitCode -eq 0 -or ($output -join [Environment]::NewLine) -notmatch "--- FAIL: $($case.Test)") {
                throw "mutant_survived: $($case.Name): $output"
            }
            Write-Output "$($case.Name): RED (guard removed)"
        } finally {
            [IO.File]::WriteAllBytes($path, $original)
        }
    }
} finally { Pop-Location }
