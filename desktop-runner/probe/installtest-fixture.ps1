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
