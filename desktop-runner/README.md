# Desktop-Läufer

Windows/amd64-Client für JanuaPort #1167 (Epic #1163 D4), Go ohne CGO.
Implementiert und mit Fake-Kern, Linux-Race-Tests und nativen Windows-Sonden
geprüft. Gemeinsame Live-Abnahme mit der Anlage und Azure-Signierung stehen aus.
Maßgeblich ist der [Draht-Vertrag v1](contract/README.md).

## Einrichten

Die EXE an einen dauerhaften Ort im Benutzerprofil legen. Die Installation
registriert genau diese Datei; Verschieben erfordert erneutes Installieren.
Eine administrative Konsole ist nicht erforderlich.

~~~powershell
.\desktop-runner.exe pair --url https://anlage.example --code 0123456789abcdef.ABCD-EFGH-JKMN
.\desktop-runner.exe install
.\desktop-runner.exe status
.\desktop-runner.exe run        # Vordergrund, Strg+C beendet den Betrieb
.\desktop-runner.exe uninstall  # entfernt beide Autostartwege
~~~

Den einmaligen, 15 Minuten gültigen Kopplungscode erzeugt die Anlage.
Format: <code>&lt;device_id&gt;.XXXX-XXXX-XXXX</code>: 16 kleine Hex-Zeichen,
Punkt und zwölf große Crockford-Base32-Zeichen (60 Bit Geheimnis).
I/L/O/U, Alias-Schreibweisen und Codes ohne Geräte-ID werden abgewiesen.
Bei privater CA zusätzlich <code>--ca C:\Vertrauen\anlage-ca.pem</code>
verwenden; alternativ <code>--fingerprint &lt;64-Hex-Zeichen&gt;</code>
für SHA-256 des exakten DER-Serverzertifikats. Die Optionen schließen sich aus.
Der Fingerprint ersetzt die CA-Kettenprüfung durch eine ausdrückliche
Zertifikatbindung; über einen vertrauenswürdigen Weg beschaffen.
Ohne diese Optionen gilt die normale TLS-Prüfung. HTTP ist nur an numerische
Loopback-Adressen erlaubt; der verbundene Peer wird zusätzlich geprüft.
Umleitungen und Umgebungsproxys sind gesperrt. Kein Abschaltschalter für TLS.

Credential und Ed25519-Key liegen DPAPI-CurrentUser-verschlüsselt mit Entropie
unter <code>%LOCALAPPDATA%\JanuaPort\desktop-runner\</code>. Der DACL erlaubt
ausschließlich dem eigenen Nutzer Zugriff. Ein leerer Anlagenschlüssel oder
der öffentliche RFC-8032-Testschlüssel aus den Goldens wird abgewiesen;
dabei wird nichts gespeichert und eine bestehende Kopplung bleibt unverändert.
Erneutes erfolgreiches Koppeln
ersetzt den alten Vertrauensbereich einschließlich aller lokalen Ergebnisse.
Einen laufenden Läufer vorher beenden.

Die COM-Benutzeraufgabe startet bei Anmeldung des eigenen Kontos:
InteractiveToken, LeastPrivilege, unbegrenzte Laufzeit, drei Neustarts mit
einer Minute Abstand, IgnoreNew, ohne Akku-Bedingungen. Bei verweigerter
Registrierung durch Zugriffsschutz/Richtlinie meldet die Installation deutlich
den Rückfall auf HKCU Run. Dort fehlt die automatische Wiederaufnahme nach
Absturz. Deinstallation entfernt beide Autostartwege, behält unbestätigte
Ergebnisse und beendet keinen bereits laufenden Prozess. Diesen nötigenfalls
über die Aufgabenplanung beenden. Status zeigt nur Kopplungs-/Installations-Bools;
Fehlerausgaben enthalten feste Codes.

## Betrieb und Pakete

Pins gehören zu geraet:<device_id>. Das erwartete Konto ist ein Windows-Name
(DOMÄNE\Benutzer). Konto, aktive WTS-Sitzung (auch gesperrt, nicht getrennt),
optional Leerlauf und geprüfte Pakete werden ausschließlich lokal geprüft.
Heartbeat alle 60 Sekunden: nur contract, ready, runner_version, manifest_seq.

Ed25519, eigene Geräte-ID und steigende Sequenz sichern das Manifest.
Ablehnungen erhalten alten Stand und Gnadenfrist. Lokaler Fünf-Feld-Cron
verwendet robfig/cron/v3 und eingebettete IANA-Zeitzonendaten. Eine Minute vor
fälligen Slots werden Manifest und geänderte Pakete synchronisiert.
Slot und Laufmarker werden gemeinsam dauerhaft gespeichert. Verpasste Minuten
bilden keinen Rückstau. Ad-hoc-Aufträge kommen über jobs/claim; beide Laufarten
teilen einen Platz. Offline laufen nur werkzeuglose Pins, wenn
<code>jetzt - letzter erfolgreicher Sync &lt; grace_hours</code>.
Gleichheit, fehlender Sync und rückwärts verstellte Uhr verweigern den Start.
Ungültige Signaturen zählen nicht als Netzausfall. Verweigerungen werden journalisiert.

Der Pin enthält jnpt-desktop.yaml, Python-Dateien, eine exakt erlaubte
embeddable-ZIP aus [python-allowlist.json](contract/python-allowlist.json)
und gegebenenfalls requirements.lock mit wheels/*.whl. Der Client lädt
Python/Wheels ausschließlich als Bestandteil des Gitpakets von der Anlage,
ohne Paketinstaller. Git-IDs einschließlich SHA-1DC, exakte Objektmenge,
Windows-Pfade, Wheel-SHA-256 und Runtime-Allowlist werden geprüft.

Umgebungen: <code>envs\&lt;commit&gt;-&lt;SHA256-des-Pfads&gt;\</code>.
Die ._pth aktiviert site nicht; .pth-Hooks laufen nicht automatisch.
<code>python.exe -I -B bootstrap.py</code> wird suspendiert erzeugt,
dem Job Object zugeordnet und erst danach fortgesetzt. Wandzeit, Abbruch
und Job-Close beenden den Prozessbaum. Vor jedem Start werden Paket und
Umgebungsdateien nachgerechnet; die Vergleichsliste liegt DPAPI-geschützt
außerhalb der Umgebung. Zusätzliche/geänderte Dateien verweigern den Lauf.

januaport.input und januaport.call stehen zur Verfügung. MCP läuft über
private vererbte Pipes und den Läufer. Lauf-Token bleiben außerhalb von
Kind-Umgebung und Argumenten. stdout/stderr werden verworfen. januaport.log
ist in dieser Fassung wirkungslos; das Drahtfeld log ist immer leer.
Das ist eine Einschränkung gegenüber dem erlaubten begrenzten Laufprotokoll.

Zusätzliche Ressourcendeckel: 30.000 Dateien, 300 MiB entpackte Umgebung,
128 MiB je Datei, 40.000 ZIP-Einträge, höchstens 64:1 oberhalb 1 MiB und
30.000 Baum-Besuche. ZIP64 und mehrteilige ZIPs werden abgewiesen.
Sehr lange Benutzerprofilpfade können Windows-Ladegrenzen erreichen;
ein fehlgeschlagener Prozessstart wird als internal gemeldet.

## Ergebnisse, Neustart und C4

Alle Ergebnisse gehen zuerst ins DPAPI-Journal und ausschließlich über
runs/result zurück; jobs/{id}/result ist kein Alias. Erst 204 entfernt den
Eintrag, Transportfehler und 409 behalten ihn. Der veröffentlichte Ciphertext
einschließlich Marker, Slot-Merkern und Verlustzähler bleibt bei höchstens
200 Ergebnissen/16 MiB. Verdrängung erhöht den dauerhaften Zähler.
Atomare Dateierneuerung benötigt kurzzeitig zusätzlichen Platz für die
verschlüsselte Ersatzdatei. Ein beim Neustart offener Lauf wird interrupted
und nicht erneut gestartet.

401 revoked stoppt laufende Prozesse und löscht Credential, Pakete, Umgebungen,
Manifest und Journal. Offline wird Widerruf erst beim Kontakt bekannt; bis
dahin gilt die Gnadenfrist. Schlüsselrotation in v1 braucht neue Kopplung.

**Workstation-Skripte handeln mit den Rechten der angemeldeten Person.**
Das Job Object ist keine Sicherheits- oder Netzwerksandbox. Der Client selbst
kommuniziert nur mit seiner Anlage; Skriptcode hat weiterhin die OS- und
Netzwerkrechte dieser Person. DPAPI CurrentUser schützt nicht vor Zugriff auf
dieselbe Benutzersitzung, auch nicht vor Veränderungen während eines Skripts.
Dienstlogs enthalten nur feste numerische Codes/Zähler, fünf Dateien zu je
1 MiB; keine Eingaben, Skriptlogs, Token, Konten oder Leerlaufzeiten.

**Ein Gerät ist eine Vertrauensdomäne (SEC D4 A1).** Ein Skript läuft als
derselbe Nutzer wie der Läufer. Es kann deshalb den DPAPI-geschützten
Geräte-Token öffnen und über einen eigenen Prozess `runs/start` für einen
fälligen Slot eines anderen Pins auf demselben Gerät aufrufen. Alle Pins eines
Geräts teilen damit ihr Vertrauen und ihre Werkzeugrechte. Wer getrennte
Werkzeugrechte braucht, nimmt getrennte Geräte bzw. getrennte Konten.

## Prüfung und Release

~~~powershell
$env:CGO_ENABLED='0'
$env:GOOS='windows'
$env:GOARCH='amd64'
go build -trimpath -o desktop-runner.exe .
go vet ./...
go test ./...
Push-Location contract
go test ./...
Pop-Location
~~~

Native Taskprobe ausdrücklich mit
<code>go test ./internal/platform -run TestWindowsAufgabeOhneAdmin</code>
aktivieren, ohne erhöhte Rechte; sie erstellt und entfernt eine Testaufgabe.
Die Python-Startprobe erwartet die unveränderte Allowlist-ZIP in .probe/.
Windows-CI beschafft und prüft sie. probe/mutations.py entfernt Sperren
einzeln, verlangt echte rote Tests und stellt Quellen byte-genau wieder her;
nur bei exklusivem Zugriff ausführen. task-standard-user.ps1 erzeugt nur auf
GitHub-Runnern ein temporäres Standardkonto.

Tag desktop-runner-vX.Y.Z: ein Build ohne CGO, optional Azure OIDC/Microsoft
Artifact Signing im Environment release-signing, danach signtool verify /pa,
SHA256SUMS und Provenance der finalen EXE. Ohne SIGNING_PROFILE bleibt die
EXE unsigniert und die Release-Notiz sagt das ausdrücklich. Fehler bei
gesetztem Profil brechen das Release ab.

Native Windows-Mutationen zusätzlich: probe/mutations-windows.ps1 (ohne Admin).
