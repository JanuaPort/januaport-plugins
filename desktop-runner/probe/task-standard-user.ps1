# CI only: register/delete a test task in a newly created standard account.
# The test itself refuses an elevated token. No change to any existing user.
$ErrorActionPreference = 'Stop'
if ($env:GITHUB_ACTIONS -ne 'true') { throw 'ci_only' }
go test -c -o .probe/platform.test.exe ./internal/platform
if ($LASTEXITCODE -ne 0) { throw 'build_failed' }
$probeUser = 'JnptD4' + [guid]::NewGuid().ToString('N').Substring(0, 8)
$probePassword = ConvertTo-SecureString ([guid]::NewGuid().ToString('N') + '!Aa1') -AsPlainText -Force
$user = New-LocalUser -Name $probeUser -Password $probePassword -AccountNeverExpires
try {
    Add-LocalGroupMember -SID 'S-1-5-32-545' -Member $user
    $binary = (Resolve-Path .probe/platform.test.exe).Path
    $probeDir = (Resolve-Path .probe).Path
    # Only the test artifact directory is made writable to the temporary user.
    & icacls $probeDir /grant "${probeUser}:(OI)(CI)M" | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'acl_failed' }
    $credential = [pscredential]::new("$env:COMPUTERNAME\$probeUser", $probePassword)
    $process = Start-Process -FilePath $binary -ArgumentList '-test.run=^TestWindowsAufgabeOhneAdmin$', '-test.v=true' -WorkingDirectory $probeDir -Credential $credential -LoadUserProfile -WindowStyle Hidden -Wait -PassThru -RedirectStandardOutput "$probeDir/task.out" -RedirectStandardError "$probeDir/task.err"
    Get-Content -LiteralPath "$probeDir/task.out"
    Get-Content -LiteralPath "$probeDir/task.err"
    if ($process.ExitCode -ne 0) { throw 'standard_user_probe_failed' }
} finally {
    Remove-LocalUser -Name $probeUser
}
