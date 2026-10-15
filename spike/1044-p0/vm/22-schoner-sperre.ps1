# VM-Sonde: Bildschirmschoner mit "bei Fortsetzung Anmeldeseite" (ScreenSaverIsSecure=1): sperrt das Ende des Schoners?
# Misst im Schoner und nach simulierter Eingabe. Danach ist ein Neustart noetig, falls gesperrt.
. "$PSScriptRoot\lib.ps1"
$f = Join-Path $Out 'screensaver-secure.txt'
Remove-Item $f -ErrorAction SilentlyContinue
Invoke-PySession 'setstate.py' "screensaver-secure `"$f`"" 60
Invoke-PySession 'session_check.py' "screensaver-secure nicht-nutzbar `"$f`"" 60
Invoke-PySession 'setstate.py' "resume `"$f`"" 60
Start-Sleep -Seconds 3
Invoke-PySession 'session_check.py' "screensaver-secure-nach-ende nicht-nutzbar `"$f`"" 60
# Schoner beenden (Prozess beenden): sperrt Windows die Sitzung, weil ScreenSaverIsSecure=1 gesetzt ist?
Get-Process -ErrorAction SilentlyContinue | Where-Object { $_.Path -like '*.scr' } | Stop-Process -Force
Start-Sleep -Seconds 4
Invoke-PySession 'session_check.py' "screensaver-secure-nach-prozessende nicht-nutzbar `"$f`"" 60
# Aufraeumen: Schoner aus, damit spaetere Sonden nicht gestoert werden
$sid = (Get-LocalUser spikeuser).SID.Value
$k = "Registry::HKEY_USERS\$sid\Control Panel\Desktop"
Set-ItemProperty $k ScreenSaveActive '0'; Set-ItemProperty $k ScreenSaverIsSecure '0'
Get-Content $f
