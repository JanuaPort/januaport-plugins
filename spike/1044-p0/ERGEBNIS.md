# Spike JanuaPort/januaport#1044 P0 - Ergebnis der headless Messung

Spike, kein Produktcode, kein Merge. Umgebung: GitHub-Runner `windows-latest` (Windows Server 2025 Datacenter 10.0.26100,
Konto `runneradmin`, Administrator, **interaktive Konsolensitzung 2 mit offenem Eingabedesktop, 1024x768**), Go 1.26.9,
Git 2.55 für Windows. Maßgeblicher Lauf: <https://github.com/JanuaPort/januaport-plugins/actions/runs/38756280402>
(Commit `a46d02a`). Der erste Lauf <https://github.com/JanuaPort/januaport-plugins/actions/runs/38755852309> hatte
dieselben Werte für Sonde 1 bis 3 und 5; Sonde 4 kam danach um c3 hinzu.

## Ergebnis je Sonde

| Sonde | Ergebnis | Kniff / Befund | Run |
|---|---|---|---|
| 1 `embeddable-pywinauto` | PASS (mit Kniff) | Python 3.12.10 Embeddable (SHA256 geprüft), Wheels `pywinauto 0.6.9`, `pywin32 312`, `comtypes 1.4.17` (+ `six`) per `pip download --only-binary=:all: --platform win_amd64`, entpackt nach `Lib\site-packages`, `._pth` ohne `import site` mit den Zeilen `Lib\site-packages`, `...\win32`, `...\win32\lib`, `...\pythonwin`. **Nur `._pth` reicht nicht:** `win32api` scheitert mit "DLL load failed" (`pywintypes312.dll`/`pythoncom312.dll` liegen in `pywin32_system32`, `pywin32.pth` und `pywin32_bootstrap` laufen ohne `site` nicht). **Zwei Kniffe funktionieren:** (B) `os.add_dll_directory(<site-packages>\pywin32_system32)` im Bootstrap vor dem ersten Import, oder (C) die beiden DLLs neben `python.exe` kopieren. Kein `pywin32_postinstall` nötig. Der Wortlaut `python.exe -I -c "import pywinauto, win32api, comtypes; ..."` geht ohne Kniff **nicht**. Empfehlung: B (kein Kopieren, Bootstrap-Zeile im Läufer-Start). | [Lauf](https://github.com/JanuaPort/januaport-plugins/actions/runs/38756280402) |
| 2 `pywinauto-backends-headless` | PASS (beide) | Notepad gestartet, `connect(process=PID)` klappt mit `win32` und `uia`; Fenster gefunden, Steuerelemente aufgezählt (win32: 2, uia: 26), Text per `type_keys` getippt und zurückgelesen (beide). Gilt **nur** für eine angemeldete, entsperrte Konsolensitzung mit Eingabedesktop (Sitzung 2, `OpenInputDesktop` ok). Keine Aussage zu gesperrt, getrenntem RDP, Bildschirmschoner. | [Lauf](https://github.com/JanuaPort/januaport-plugins/actions/runs/38756280402) |
| 3 `jobobject-kill` | PASS | Go (`golang.org/x/sys/windows`, v0.49.0 wie in `script-runner/go.sum`) legt ein Job Object mit `KILL_ON_JOB_CLOSE` an, weist `python.exe -I` zu; Python startet Notepad und einen Helfer, der `ping` (Enkel) startet. Nach `CloseHandle(job)`: **kein Nachkomme lebt** (4 von 4 vorher lebend, 0 danach; unabhängige Gegenprobe per `Get-Process`: 0 Reste). **Prozessdeckel** `ActiveProcessLimit=3`: Start 1 und 2 leben, Start 3 bis 5 scheitern mit `OSError 1816` (`ERROR_NOT_ENOUGH_QUOTA`), der Start schlägt also **synchron fehl**. **Speicherdeckel** `JobMemoryLimit=200 MB`: Python kommt bis 150 MB, dann `MemoryError` (sauber abfangbar, kein Prozessabbruch). Hinweis: Zuweisung erfolgt **nach** dem Start (Kind wartet auf eine Zeile auf stdin); für das Produkt `CREATE_SUSPENDED` oder `PROC_THREAD_ATTRIBUTE_JOB_LIST` statt dieses Musters. | [Lauf](https://github.com/JanuaPort/januaport-plugins/actions/runs/38756280402) |
| 4 `go-tls-box-ca` | PASS, mit klarer Antwort | a) `SystemCertPool()` + `AppendCertsFromPEM` **geht** unter Windows (PASS). d) `NewCertPool()` + CA geht ebenfalls (Rückfallweg). b) nur System-Pool scheitert wie erwartet mit `x509: certificate signed by unknown authority`. **c) Go liest den Benutzer-Store (CurrentUser\Root): ja.** Aber: `certutil -addstore -user Root` öffnet einen **Sicherheitsdialog** ("Security Warning", Knopf "Yes") und **hängt** ohne Bestätigung (20 s Zeitüberschreitung, nichts im Store). Mit einem UIA-Klick auf "Yes" (c3, pywinauto aus Sonde 1) wird die CA importiert und Go akzeptiert das Serverzertifikat. Dialogfrei (c2: Import nach `CurrentUser\CA`, dann den Registry-Blob nach `HKCU\...\Root\Certificates\<Thumbprint>` kopieren) funktionierte auf dem Runner ebenfalls, Go und .NET vertrauen danach; auf einem verwalteten Entwickler-Windows-11 wurde der Blob dagegen **nicht** vertraut (Gruppenrichtlinie/Defender vermutet), taugt also nicht als verlässlicher Weg. e) `certutil -addstore Root` (LocalMachine, Administrator) geht ohne Dialog, Go liest ihn. Empfehlung: Box-CA im Go-Prozess per `AppendCertsFromPEM` (a oder d) halten, **nicht** in einen Windows-Store importieren. | [Lauf](https://github.com/JanuaPort/januaport-plugins/actions/runs/38756280402) |
| 5 `git-openssh-acl` | PASS (Positiv), Gegenprobe wirkt **nicht** | `ssh-keygen -t ed25519` (Windows-OpenSSH 9.5p2), ACL per `icacls /inheritance:r`, nur `runneradmin:(F)` (SYSTEM und Administratoren entfernt): beide ssh-Clients bieten den Schlüssel an und bekommen `Permission denied (publickey)` (kein "UNPROTECTED PRIVATE KEY"). **Gegenprobe schlägt fehl:** auch mit zusätzlich `Everyone:R`, `Users:R` oder `Everyone:M` bieten Windows-OpenSSH 9.5p2 **und** das ssh aus Git für Windows (OpenSSH 10.5p1) den Schlüssel an, keine Verweigerung. Folgerung: **ssh prüft die Datei-ACL unter Windows nicht streng genug, um sie als Schutz zu verwenden**; der Läufer muss die ACL selbst setzen und per `Get-Acl` prüfen. **Welche ssh.exe nimmt git?** Per Default das ssh aus Git für Windows (`C:\Program Files\Git\usr\bin\ssh.exe`, im Test `/usr/bin/ssh`, OpenSSH 10.5p1), obwohl `C:\Windows\System32\OpenSSH\ssh.exe` im PATH der PowerShell vorn steht; `core.sshCommand`, `GIT_SSH`, `GIT_SSH_COMMAND` waren leer. | [Lauf](https://github.com/JanuaPort/januaport-plugins/actions/runs/38756280402) |

## Nebenbefunde (lokal, nicht aus der CI)

Auf dem verwalteten Windows 11 eines Entwicklers (nicht im Runner) lief Sonde 3 ebenfalls, mit einer Abweichung: Das
**Store-Notepad von Windows 11 entkommt dem Job Object** (die gestartete PID ist nur ein Stub, die eigentliche Anwendung
wird per AppX-Aktivierung außerhalb des Jobs gestartet; Rest-Prozess nach `CloseHandle`). Klassische Win32-Programme und
das Notepad des Server-Runners bleiben im Job. Relevant für die VM: Windows 11 statt Server und die echte Fachanwendung
(klassisch oder Store/UWP/MSIX?) prüfen.

## Was die VM-Messung noch klären muss

Der Runner hat eine **angemeldete, entsperrte Konsolensitzung**; alles, was eine Sperre oder einen fehlenden Desktop
braucht, ist hier nicht gemessen:

1. **Gesperrte Sitzung (Win+L):** Gehen `win32`- und `uia`-Backend, `type_keys`, `SetForegroundWindow` weiter? (Der Runner
   meldete bei `SetForegroundWindow` keinen Fehler; lokal schlug es einmal fehl.) Erwartung: Eingabe und Fokus brechen weg.
2. **Getrenntes RDP (Fenster zu, Sitzung bleibt "Disconnected"):** Der Eingabedesktop fehlt, UIA liefert keinen Baum,
   `SendInput` scheitert. Gegenmittel (`tscon` auf die Konsole, VM-Konsolensitzung, automatische Anmeldung) messen.
3. **Bildschirmschoner und Anzeige aus:** Startet der Schoner und sperrt, wenn "bei Anmeldung fortsetzen" gesetzt ist?
   Richtlinie/Registry zum Abschalten prüfen. Auflösung ohne Monitor (die Runner-Auflösung war 1024x768).
4. **UIA gegen die echte Fachanwendung:** Notepad ist die einfachste Zielanwendung. Offen: Zugriffsstufe (Anwendung als
   Administrator gestartet, UIPI blockiert dann den Nutzer-Läufer), Steuerelementtyp (Win32, WPF, Java, Electron, Citrix),
   Fenstertitel in der Zielsprache, Startzeit und Dialoge beim ersten Start.
5. **Job Object gegen die echte Fachanwendung:** Entkommt sie dem Job (Store/MSIX-Aktivierung, Einzelinstanz-Programme, die
   sich an einen schon laufenden Prozess hängen, Dienste/COM-Server außerhalb des Jobs)? Wird sie sauber beendet oder
   hinterlässt sie Sperrdateien und Wiederherstellungsdialoge?
6. **Windows 11 statt Server 2025:** Store-Notepad-Verhalten (siehe oben), Defender/SmartScreen für `python.exe` und die
   Läufer-Exe aus einem ZIP ohne Signatur, Mark-of-the-Web am entpackten ZIP.
7. **Benutzer-Store und Dialog:** Wenn die Box-CA doch im Store landen soll: der Sicherheitsdialog braucht eine Person
   (kein Dienstkonto, kein gesperrter Desktop). Deshalb die Empfehlung, die CA im Go-Prozess zu halten.
8. **Dienstkonto statt Anmeldung:** Aufgabe "bei Anmeldung" läuft im Benutzerkontext; ein Windows-Dienst (Sitzung 0) hat
   keinen Desktop und wäre für Sonde 2 ungeeignet. In der VM bestätigen.
9. **SHA256 der Embeddable** wurde beim ersten Abruf selbst berechnet (python.org nennt nur MD5), danach fest verdrahtet.
   Für das Release gegen die Sigstore-/GPG-Signatur der Python-Release prüfen.

---

## Teil 2 (EC2): Messung mit echter Sitzung auf Windows Server 2025

Umgebung: EC2 `t3.medium` (eu-central-1a), Windows Server 2025 Datacenter 10.0.26100 aus dem offiziellen Amazon-AMI, Konto
stellwerk. Testnutzer `spikeuser` (Standardnutzer) mit **Autologon**; alle Sitzungsprogramme laufen als Aufgabe
"nur bei Anmeldung" in dessen interaktiver Konsolensitzung 1 (Embeddable-Python 3.12.10 mit dem add_dll_directory-Kniff
aus Teil 1, pywinauto 0.6.9). Gesteuert wurde per WinRM von außen. Skripte, Rohdaten und Screenshots liegen unter
`spike/1044-p0/vm/` (Rohdaten: `vm/ergebnisse/`). Jede Sonde gibt `PROBE <name> PASS|FAIL <detail>` aus. Bei den
Zustandssonden heißt PASS "das Urteil des Läufers stimmt mit der Wirklichkeit überein", nicht "Programm arbeitet".

### Ergebnis je Sonde

| Sonde | Ergebnis | Befund |
|---|---|---|
| 1 `session-check` | PASS mit Planänderung | **Nur `OpenInputDesktop` reicht nicht, `WTSConnectState` auch nicht.** In der Sperre meldet `OpenInputDesktop` weiter `Default/ok` und `WTSConnectState` weiter `Active`; der Sperrbildschirm (`LockApp.exe`) läuft auf dem Desktop Default. Das Urteil v1 ("Active und Desktop Default") lag dort **falsch** (`session-check-lock FAIL`). Zuverlässig ist der Dreiklang v2: `WTSConnectState==Active` **und** Sperrflag aus `WTSQuerySessionInformation(WTSSessionInfoEx)` `entsperrt` **und** der Name des Eingabedesktops `Default` (über `OpenInputDesktop(DESKTOP_SWITCHDESKTOP)` und `GetUserObjectInformation(UOI_NAME)`). v2 stimmt in allen fünf Zuständen (Tabelle unten). `GetForegroundWindow` taugt nur als Zusatz: gesperrt `LockApp.exe`, getrennt und im Schoner keines. |
| 2 `uia-in-states` | PASS mit Einschränkung | Beide Backends **finden** das Fenster und **setzen Text ohne Eingabe** (`set_edit_text`, WM_SETTEXT bzw. ValuePattern) in **allen** Zuständen, auch gesperrt und getrennt. **Tippen** (`type_keys`, SendInput) und `set_focus` gehen nur im entsperrten Zustand (und bei ausgeschalteter Anzeige, siehe Grenze). Fehlertexte: Schoner und getrennt, win32: `RuntimeError: There is no active desktop required for moving mouse cursor!`; uia: `RuntimeError: SendInput() inserted only 0 out of 2 keyboard events`. **Gesperrt: win32 `error: (0, 'SetForegroundWindow', ...)`, uia dagegen ohne Ausnahme, der Text kommt nur nicht an (`gelesen=''`).** Ein leiser Fehlschlag, kein Fehlertext. Das Notepad von Server 2025 ist das klassische (win32: 2 Steuerelemente, uia: 26). |
| 3 `jobobject-real-app` | PASS für klassische Apps, **FAIL für Paket-Apps und Einzelinstanz** | Mit `CREATE_SUSPENDED`, Job-Zuweisung, dann Resume. **Klassisch:** `notepad.exe` und `mspaint.exe` bleiben im Job und sterben mit `CloseHandle`. **Einzelinstanz:** `explorer.exe C:\spike` übergibt an die laufende Shell, der neue `explorer.exe` ist nicht im Job und lebt weiter. **MSIX (Windows Terminal, Feedback Hub):** Aktivierung über `explorer shell:AppsFolder\...` entkommt komplett (App-Prozess nicht im Job, überlebt). Über den Alias `wt.exe` per `CreateProcess` bleiben `WindowsTerminal.exe` und `OpenConsole.exe` im Job, die darin gestartete Shell `powershell.exe` ist **nicht** im Job und überlebte in 2 von 3 sauberen Läufen den `CloseHandle` (nicht deterministisch, nur der letzte Lauf ist als Datei erhalten). Beim Messen mit Konsole (`cmd /c`) hängt sich das Terminal als Standardterminal an die Messung und verfälscht sie; die Sonde läuft deshalb ohne Konsole (`-H=windowsgui`). Ergebnis siehe `vm/ergebnisse/jobobject.txt`, der Verfälschungslauf ist als `jobobject-lauf1-mit-konsole.txt` erhalten. |
| 4 `motw-smartscreen` | PASS (Läufer-Weg), **FAIL (Doppelklick-Weg)** | **Kein** Download-Weg setzt Mark-of-the-Web: `Invoke-WebRequest`, `WebClient`, `curl.exe` und BITS (Windows PowerShell 5.1) lassen die Zone weg. **Kein** Entpacker vererbt sie: ein ZIP mit `ZoneId=3` (Browser-Download simuliert) ergibt mit `Expand-Archive`, .NET `ZipFile` und `tar.exe` Dateien ohne Zone.Identifier. Eine unsignierte Go-Exe mit `ZoneId=3` **startet aus dem Aufgabenplaner und per `CreateProcess` ohne Dialog**. Über ShellExecute und `explorer.exe <exe>` (wie Doppelklick) erscheint **"Windows protected your PC"** (Microsoft Defender SmartScreen), die Exe läuft nicht (ShellExecute meldet 1223); Screenshot `vm/ergebnisse/smartscreen-dialog.jpg`. Die **signierte** `python.exe` aus der Embeddable löst mit `ZoneId=3` keinen Dialog aus und läuft auch per Explorer. Defender (Echtzeitschutz an, Cloud-Schutz "erweitert"): **keine Erkennung** der unsignierten Go-Exe, aber Ereignis 2050: Defender hat die Datei `hello-zone3.exe` (mit Zone) zur Analyse **in die Cloud hochgeladen**. |
| 5 `logon-task` | PASS | Autologon und die Aufgabe "bei Anmeldung" funktionierten in **3 von 3** Neustarts: Aufgabe startete 18 bis 44 s nach dem Systemstart (44 s beim ersten Boot, danach 19 und 18 s), `LastTaskResult=0`, Prozess in **Sitzung 1** (nicht 0), `WTSConnectState=Active`, Eingabedesktop `Default`, Vordergrund `explorer.exe`. Autologon erzeugt auch ohne Monitor und ohne RDP eine Konsolensitzung. Dafür steht das Passwort im Klartext in der Registry (`DefaultPassword`); im Produkt ist das keine Lösung (siehe Planänderungen). |

### Rohsignale je Zustand (Sonde 1, Urteil v2 stimmt überall)

| Zustand | WTSConnectState | Sperrflag | Eingabedesktop (SWITCHDESKTOP) | Vordergrund | Urteil v2 | Tippen (type_keys) |
|---|---|---|---|---|---|---|
| entsperrt | Active | entsperrt | Default/ok | Notepad | nutzbar | geht (beide Backends) |
| Anzeige aus (`SC_MONITORPOWER`) | Active | entsperrt | Default/ok | Notepad | nutzbar | geht (nicht verifiziert, siehe Grenze) |
| Bildschirmschoner läuft | Active | entsperrt | **Screen-saver**/ok | keins | nicht nutzbar | schlägt fehl (siehe oben) |
| Sitzung getrennt (`tsdiscon`) | **Disconnected** | **gesperrt** | **err5 (Zugriff verweigert)** | keins | nicht nutzbar | schlägt fehl |
| gesperrt (`LockWorkStation`) | Active | **gesperrt** | **Default/ok** | **LockApp.exe** | nicht nutzbar | **leiser Fehlschlag** (uia), Fehler (win32) |
| getrennt, dann `tscon 1 /dest:console` als SYSTEM | Active | entsperrt | Default/ok | keins (kurz) | nutzbar | geht |

Zusätzlich gemessen: **Ein Bildschirmschoner mit `ScreenSaverIsSecure=1` sperrt die Sitzung, sobald er endet**
(Prozessende: danach `gesperrt`, `LockApp.exe`; `vm/ergebnisse/vor-reboot.txt`). Das Beenden über simulierte Mausbewegung
(`mouse_event` aus der Sitzung) hat den Schoner nicht beendet: nicht verifiziert, ob echte Eingabe dasselbe auslöst.
`tsdiscon` trennt die Sitzung, auf der Konsole erscheint eine neue Sitzung 2 mit Anmeldebildschirm (`konsole=2`), die
getrennte Sitzung 1 behält ihre Prozesse (Notepad lebt, UIA findet es, `set_edit_text` geht). `tscon` als SYSTEM holt sie
**ohne Passwort** auf die Konsole zurück, entsperrt (sie war nur getrennt, nicht gesperrt). Ein Neustart (Autologon)
beendet die Sperre; aus der Sperre selbst gibt es ohne Passwort keinen Weg.

### Was das am Plan v1 ändert

1. **Bereitschaftsprüfung des Läufers:** Dreiklang v2 (Active, entsperrt, Eingabedesktop `Default`). `OpenInputDesktop`
   allein reicht nicht, `WTSConnectState` allein auch nicht. Den Namen des Eingabedesktops mitlesen: `Screen-saver` ist ein
   eigener Zustand.
2. **Fehlertexte sind kein Signal.** In der Sperre wirft uia-`type_keys` nichts. Der Läufer prüft vor jedem Schritt die
   Bereitschaft und liest nach jedem Tippen den Inhalt zurück. Wo die Anwendung es zulässt, bevorzugt er den eingabefreien
   Weg (`set_edit_text`/ValuePattern), der in allen gemessenen Zuständen arbeitet.
3. **"Sitzung nicht bereit" ist ein eigener Auftragszustand** (wartet, Meldung in Kartei und Betreiber), kein Fehler. Eine
   Sperre kann der Läufer nicht lösen. Eine bloß getrennte Sitzung kann ein Hilfsdienst als SYSTEM per `tscon` zurückholen
   (nur im Pilot mit Betreiber-Zustimmung, es umgeht keine Sperre). Für den Pilotkunden gehört Schoner-Sperre und
   Leerlaufsperre per Richtlinie ausgeschaltet, sonst ist der Rechner nach einer Pause für den Läufer tot.
4. **Job Object schützt nur klassische Programme.** Paket-Apps (MSIX) und Einzelinstanz-Programme (`explorer.exe`)
   entkommen. Der Läufer braucht für solche Zielanwendungen eine zweite Aufräumstufe (Prozesse nach Image-Pfad bzw.
   Package-Family beenden) und die Lückenliste führt das als Bedingung. Erst die echte Fachanwendung klärt, welche Stufe
   gilt.
5. **Läufer-Exe:** Der Start per Aufgabenplaner oder `CreateProcess` stört MOTW und SmartScreen nicht; die Exe sollte
   nie per Doppelklick oder `Start-Process` (ShellExecute) gestartet werden. Beim Selbstladen setzt der Läufer die Zone
   nicht, beim ZIP-Download im Browser **kann** der Explorer-Entpacker sie vererben (nicht gemessen, siehe offene Punkte);
   der Installer entpackt deshalb selbst. Authenticode-Signatur bleibt Empfehlung (signierte Dateien lösten keinen Dialog
   aus, die unsignierte Exe wird von Defender als Probe in die Cloud geladen).
6. **Autologon im Produkt:** Klartext in `DefaultPassword` ist für Kunden nicht vertretbar. Entweder Betreiberentscheid
   mit dem Sysinternals-Werkzeug "Autologon" (LSA-Geheimnis) oder der Kunde meldet das Konto selbst an. Das Verhalten der
   Aufgabe "bei Anmeldung" (zuverlässig, Sitzung 1) gilt unabhängig davon.

### Grenze: was sich von Server 2025 nicht auf Windows 11 überträgt

* **Smart App Control** gibt es nur auf Windows 11 (Neuinstallation). Es kann eine unsignierte Exe auch aus dem
  Aufgabenplaner blockieren. Hier **nicht gemessen**; der Pilotrechner muss das klären.
* **SmartScreen-Reputation und Dialog** (Wortlaut, "Trotzdem ausführen") sind auf dem Client anders verdrahtet als auf dem
  Server (hier: Dialog nur mit "Nicht ausführen" auf der ersten Seite).
* **IE Enhanced Security Configuration** ist nur auf dem Server aktiv; Zonenrichtlinie `1806` stand hier für die
  Internetzone auf "Nachfragen" (1). Ob ESC das Ergebnis der Sonde 4 beeinflusst hat, wurde nicht getrennt gemessen.
* **Anzeige aus und Energiesparen:** `SC_MONITORPOWER` auf einer virtuellen Anzeige ohne Monitor ändert nichts; ein echter
  Monitor, "Modern Standby" und die Anmeldepflicht nach dem Ausschalten der Anzeige sind **nicht verifiziert**.
* **Echtes RDP:** `tsdiscon` erzeugt denselben Sitzungszustand (`Disconnected`) wie das Schließen eines RDP-Fensters,
  eine echte RDP-Verbindung wurde nicht aufgebaut. Gruppenrichtlinien eines Kunden-AD (Sperre bei Trennung, Zeitlimit für
  getrennte Sitzungen) sind nicht abgebildet.
* **Store-Notepad:** Server 2025 hat das klassische Notepad; der Entkommen-Befund aus Teil 1 für das Store-Notepad von
  Windows 11 ist durch die Paket-Apps (Terminal, Feedback Hub) bestätigt, das Store-Notepad selbst nicht gemessen.
* **Zielanwendungstyp:** Gemessen wurde nur Win32 (Notepad). WPF, Java, Electron und Citrix bleiben offen.

### Offene Punkte (nicht verifiziert)

1. Ob der Explorer-Entpacker (`Shell.Application`, Doppelklick auf das ZIP) die Zone an die Dateien vererbt, ist nicht
   gemessen (die drei gemessenen Entpacker tun es nicht).
2. Ende des Schoners durch echte Eingabe (statt Prozessende) und Sperre danach.
3. Die Windows-11-Punkte oben auf dem Pilotrechner.

### Ablauf, Abweichung und Kosten

* **Abweichung vom Auftrag:** Die temporäre IAM-Rolle für SSM ließ sich nicht anlegen (`iam:CreateRole` und
  `iam:AttachRolePolicy` sind für den Benutzer `win_claude` ausdrücklich verweigert; der dabei angelegte leere
  Instance-Profile wurde sofort gelöscht). Ersatz war der im Auftrag vorgesehene Weg über WinRM: HTTPS-Listener (5986),
  Security Group und Windows-Firewall nur für die öffentliche IPv4 dieses Rechners als /32, Anmeldung mit dem
  Administratorpasswort aus `get-password-data` (nie ausgegeben, Schlüsseldatei nach der Messung gelöscht). Nichts war für
  0.0.0.0/0 offen. Statt `t3.large` lief `t3.medium` (kleiner, erlaubt).
* **Laufzeit:** Start 15:06:09 UTC, Beenden 15:51:01 UTC, also 44 min 52 s (0,75 h). Preis laut AWS-Preisliste
  Windows `t3.medium` in eu-central-1 etwa 0,05 bis 0,10 USD je Stunde, dazu öffentliche IPv4 (0,005 USD/h) und 30 GB
  EBS (unter 0,01 USD): **rund 0,10 USD insgesamt**, sicher unter 0,15 USD.
* **Aufgeräumt:** Instanz terminiert, Security Group, Key Pair gelöscht, Volume mit der Instanz gelöscht; es wurde keine
  IAM-Rolle und kein Instance Profile (mehr) angelegt. Belege im Abschlussbericht der Messung.
