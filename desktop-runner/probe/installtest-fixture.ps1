# Synthetic native PowerShell child, no runner/network code.
param([string]$Operation, [Parameter(ValueFromRemainingArguments)][string[]]$Rest)
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
switch ($Operation) {
    status {
        if ($env:JNPT_INSTALLTEST_STATUS -eq 'invalid') {
            Write-Output 'Desktop-Läufer: gekoppelt=false installiert=false'
        } else { Write-Output 'Desktop-Läufer: gekoppelt=true installiert=true' }
    }
    run { Start-Sleep -Seconds 60 }
    fail {
        Write-Output $Rest[-1]
        [Console]::Error.WriteLine($Rest[-1])
        exit 2
    }
    environment { [Console]::Write($env:SHOWCASE_PAIR_CODE) }
    code { [Console]::Write($Rest[-1]) }
}

# Library functions register inert fixtures only in the Windows CI adapter tests.
function New-InstalltestTaskFixture {
    param([string]$Executable, [string]$Arguments, [switch]$NoTrigger)
    $service = New-Object -ComObject 'Schedule.Service'
    $service.Connect()
    $folder = $service.GetFolder('\')
    $definition = $service.NewTask(0)
    $definition.Principal.LogonType = 3
    $definition.Principal.RunLevel = 0
    $trigger = $null
    if (-not $NoTrigger) {
        $trigger = $definition.Triggers.Create(9)
        $trigger.Enabled = $false
    }
    $action = $definition.Actions.Create(0)
    $action.Path = $Executable
    $action.Arguments = $Arguments
    $name = 'JanuaPort-L6-Test-' + [guid]::NewGuid().ToString('N')
    $registered = $folder.RegisterTaskDefinition($name, $definition, 2, $null, $null, 3)
    [Runtime.InteropServices.Marshal]::FinalReleaseComObject($registered) | Out-Null
    return @{ Name = $name; Objects = @($trigger, $action, $definition, $folder, $service); Folder = $folder }
}
function Remove-InstalltestTaskFixture {
    param($FixtureTask)
    $FixtureTask.Folder.DeleteTask($FixtureTask.Name, 0)
    foreach ($com in $FixtureTask.Objects) { if ($com) { [Runtime.InteropServices.Marshal]::FinalReleaseComObject($com) | Out-Null } }
}
