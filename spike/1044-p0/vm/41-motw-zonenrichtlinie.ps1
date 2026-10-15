# VM-Sonde motw (Teil 2): Warum scheitert ShellExecute einer Exe mit ZoneId=3 auf dem Server mit 1223 (ohne sichtbaren Dialog)?
# Liest die Zonenrichtlinie von spikeuser (IE ESC ist auf Windows Server eingeschaltet), uebergibt der Shell
# (explorer.exe <exe>) den Start und haelt die Fenster fest. Fuer Screenshots: Aufruf im Hintergrund, waehrenddessen
# "aws ec2 get-console-screenshot".
. "$PSScriptRoot\lib.ps1"
$work = Join-Path $Base 'motw'
$res = Join-Path $Out 'motw-zone.txt'
Remove-Item $res -ErrorAction SilentlyContinue
$sid = (Get-LocalUser spikeuser).SID.Value
$zonen = foreach ($z in 1, 2, 3, 4) {
    $v = (Get-ItemProperty "Registry::HKEY_USERS\$sid\Software\Microsoft\Windows\CurrentVersion\Internet Settings\Zones\$z" -ErrorAction SilentlyContinue).'1806'
    "Zone$z=$v"
}
$esc = (Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Active Setup\Installed Components\{A509B1A8-37EF-4b3f-8CFC-4F3A74704073}' -ErrorAction SilentlyContinue).IsInstalled
Write-Probe 'motw-zonenrichtlinie-server' PASS "spikeuser Wert 1806 (Anwendungen/unsichere Dateien starten; 0=an 1=nachfragen 3=aus): $($zonen -join ' ') ; IE-ESC fuer Nutzer IsInstalled=$esc" $res
# python-zone3 (signierte python.exe): "gelaufen=nein" ist hier kein Befund, Python schreibt keine Markerzeile;
# massgeblich ist, ob ein SmartScreen-Dialog ("Window Dialog") erscheint oder das Konsolenfenster der Anwendung.
foreach ($t in @(@('ohne-zone', "$work\hello-ohne.exe"), @('zone3', "$work\hello-zone3.exe"), @('python-zone3-signiert', "$work\py-zone3\python.exe"))) {
    "$($t[0]) explorer: " + (Invoke-PySession 'motw_run.py' "$($t[0]) `"$($t[1])`" explorer `"$res`" 25" 90)
}
Get-Content $res
