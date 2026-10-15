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
