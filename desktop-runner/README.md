# Desktop-Läufer

Windows/amd64-Client und Linux-Server-Läufer für JanuaPort, Go ohne CGO.
Der Server-Modus ergänzt Container-Pins (JanuaPort/januaport#1195).
Implementiert und mit Fake-Kern, Linux-Race-Tests und nativen Windows-Sonden
geprüft. Signiertes Windows-Release: `desktop-runner-v0.1.0` (#1163 D4).
Gemeinsame Live-Abnahme mit der Anlage steht aus.
Maßgeblich ist der [Draht-Vertrag](contract/README.md): v1 für Windows, v2 für Linux-Server.

## Einrichten unter Windows

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
ausschließlich dem eigenen Nutzer Zugriff; die Prüfung vergleicht binäre SIDs,
Rechte und Vererbungsflags unabhängig von SDDL-Aliasnamen. Adminrechte sind
für Kopplung und DPAPI nicht erforderlich und werden dort nicht abgewiesen.
Junctions in bestehenden Pfadkomponenten werden abgewiesen.
Ein leerer Anlagenschlüssel oder
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

## Betrieb und Python-Pakete unter Windows

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

Alle Ergebnisse gehen zuerst ins lokale Journal (Windows: DPAPI; Linux:
HMAC-SHA256 und Dateirechte) und ausschließlich über
runs/result zurück; jobs/{id}/result ist kein Alias. Erst 204 entfernt den
Eintrag, Transportfehler und 409 behalten ihn. Der veröffentlichte Ciphertext
einschließlich Marker, Slot-Merkern und Verlustzähler bleibt bei höchstens
200 Ergebnissen/16 MiB. Verdrängung erhöht den dauerhaften Zähler.
Atomare Dateierneuerung benötigt kurzzeitig zusätzlichen Platz für die
geschützte Ersatzdatei. Ein beim Neustart offener Lauf wird interrupted
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

**Unter Windows ist ein Gerät eine Vertrauensdomäne (SEC D4 A1).** Ein Skript läuft als
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

Tag desktop-runner-vX.Y.Z: ein Build ohne CGO, dann Pflicht-Signatur über Azure
OIDC/Microsoft Artifact Signing im Environment release-signing (nur für Tags
desktop-runner-v* freigegeben), danach signtool verify /pa, SHA256SUMS und
Provenance der finalen EXE. Fehlt SIGNING_PROFILE oder scheitert ein Schritt,
bricht das Release ab; ein unsigniertes Release gibt es nicht.

Native Windows-Mutationen zusätzlich: probe/mutations-windows.ps1 (ohne Admin).

## Installationstest

Der manuelle Workflow `.github/workflows/desktop-runner-installtest.yml` prüft die
Client-Installation ohne PO und ohne PO-Gerät auf einem frischen `windows-latest`.
`workflow_dispatch` nimmt `version` ohne `v` entgegen, Standard `0.1.0`.
Die Probe ist Stufe 2 „Gemeinsam bauen“, nicht release-blockierend; kein Zeitplan,
Mehrgeräte- oder Lasttest. Die Logik liegt in `probe/installtest.ps1`.

GATE richtet vor dem Lauf das Environment `showcase-installtest` ein: ausschließlich
Branch `main` freigeben und einen erforderlichen Reviewer setzen (kein Selbstreview).
Diese GitHub-Einstellungen sind Voraussetzung und werden nicht durch die YAML-Datei
angelegt. Der Auftraggeber übernimmt Push und Dispatch von `main` nach Review.
GATE legt auf dem Showcase ein frisches Windows-Gerät und einen Test-Pin mit
synthetischem `print`, ohne Werkzeuge, Zugangsdaten oder Kundendaten an. Geräteart
`workstation`/`dedicated`, Startpolicy `always`; ein Ad-hoc-Auftrag muss zusätzlich
für das Gerät bereitstehen. Pin allein bedeutet noch keine Zustellung.
Unmittelbar vor dem Lauf erzeugt GATE den einmaligen, kurz gültigen Kopplungscode
per Showcase-CLI und setzt ihn als Environment-Secret `SHOWCASE_PAIR_CODE`.
Danach löscht GATE das Secret auch bei fehlgeschlagenem/abgebrochenem Lauf.

Die Probe lädt EXE, `SHA256SUMS` und `provenance.jsonl` aus dem öffentlichen Release
in `JanuaPort/januaport-plugins`. Vor jeder Ausführung verlangt sie SHA-256,
Authenticode-Status `Valid`, den Herausgeber `JanuaPort GmbH` und
`gh attestation verify` mit Repository, Release-Workflow und Tag als Herkunft.
GitHub-Tokenrechte: nur `contents: read` und `attestations: read`.
Anschließend erstellt sie wie die Taskprobe ein temporäres Standardkonto,
lädt dessen Profil und legt die geprüfte EXE dauerhaft unter
`%LOCALAPPDATA%\JanuaPort\installtest\<version>\desktop-runner.exe` ab.
Koppeln und Installieren laufen ohne Adminrechte; `status` muss
`gekoppelt=true installiert=true` ausgeben. Beide Installationswege zählen:
COM-Benutzeraufgabe und der vom Läufer gemeldete Richtlinien-Rückfall auf HKCU Run.

Der Code liegt im Skript als `SecureString` vor und wird dem Standardkonto über
eine Named Pipe übergeben, deren DACL nur dieses Konto und den CI-Hauptnutzer
zulässt. Er wird nicht als Klartextdatei abgelegt und nicht ausgegeben;
Prozessausgaben werden auch im Fehlerfall abgefangen, Fehler bleiben fest.
**Grenze von Windows v0.1.0:** `pair` unterstützt ausschließlich `--code`, kein stdin.
Der Code ist während dieses kurzen Prozessstarts in dessen Argumentliste sichtbar.
Die stdin-Unterstützung im Linux-Server gilt nicht für die Windows-EXE.

`install` startet den Läufer erst bei der nächsten Anmeldung. Deshalb startet die
Probe zusätzlich `run` im selben Standardkonto und beobachtet ihn fünf Minuten.
Sie prüft Prozess und `status` und liest ausschließlich das lokale DPAPI-geschützte
Manifest, um einen erfolgreichen Sync als Lebenszeichen des Anlagenkontakts zu
erkennen. Ohne Sync wird der Lauf rot. Das ist kein Nachweis eines bestätigten
Heartbeats und kein `ready`-Nachweis. Der Beobachtungszeitraum bleibt für GATEs
serverseitige Prüfungen offen.

**Auftragsabschluss lokal nicht zuverlässig beobachtbar:** `status` zeigt nur die
beiden Bools. Das DPAPI-Journal enthält unbestätigte Ergebnisse; nach HTTP 204 wird
der Eintrag sofort gelöscht. Ein Polling könnte das `ok` verpassen. Die Probe liest
deshalb keine Auftragsinhalte und behauptet keinen lokal belegten Auftragsabschluss.
GATE belegt auf dem Showcase Lebenszeichen, Auftrag `ok`, Audit ohne Inhalt sowie
den Admin-Abbruch eines zweiten, absichtlich langen Pins. Dessen tatsächliche
Endklasse dokumentiert GATE: der aktuelle Heartbeat-Abbruchpfad liefert `cancelled`;
`interrupted` bezeichnet im Windows-Code einen beim Neustart offenen Laufmarker.

**Weitere Voraussetzung für die Auftragsbelege:** Der Läufer verlangt eine aktive
WTS-Sitzung und ein exakt passendes `expected_account` (`DOMÄNE\Benutzer`). Das
temporäre Konto heißt zufällig `JnptIT…`; die Hosted-CI garantiert keine aktive
Benutzersitzung dafür. Diese Probe umgeht weder Konto- noch Sitzungsprüfung.
Auftrags- und Abbruchbelege sind deshalb auf Hosted-CI nicht garantiert und müssen
gegebenenfalls auf einem dafür eingerichteten Test-PC erfolgen. Die CLI sendet
keinen Kontonamen; GATE muss die Konto-/Sitzungsvoraussetzungen separat herstellen.

In `finally` laufen `uninstall`, das Beenden des explizit gestarteten Prozesses und
die Abfrage der Aufgabenplanung über COM sowie von HKCU Run. Jede verbleibende
eigene Autostartregistrierung macht den Test rot. Ein zweiter Cleanup im Standardkonto
deckt auch einen vom Skript zeitlich abgebrochenen Worker ab. Harter Runnerverlust
kann `finally` verhindern; GATE räumt dann das Showcase-Gerät trotzdem auf.
GATE entfernt nach dem Lauf das Gerät und belegt, dass es verschwunden ist und
seine Credentials ungültig sind. `uninstall` allein widerruft keine Credentials.

Manuell auf einem frischen Windows/amd64-Testprofil ohne Administrator-Mitgliedschaft
mit PowerShell 7 und aktueller GitHub CLI (`gh attestation verify` einschließlich
`--source-ref` verfügbar):

~~~powershell
./probe/installtest.ps1 -Version 0.1.0 -Code (Read-Host 'Showcase-Code' -AsSecureString)
~~~

Ein bereits benutztes Läuferprofil oder vorhandener Autostart wird abgewiesen,
damit eine bestehende Kopplung erhalten bleibt. EXE und lokaler Zustand bleiben
nach Deinstallation im Testprofil; Showcase-Widerruf und Profilentsorgung sind
Betreiberaufgaben. `-CreateStandardUser` ist ausschließlich für GitHub-CI gedacht.

Nicht belegt: SmartScreen/Zertifikatreputation auf einem echten Arbeitsplatz-PC,
Firewall, Proxy und Virenschutz beim Pilot sowie der Linux-Server-Läufer (#1195).
Offline-Verifikation ohne Release-/Showcase-Zugriff:
`./probe/installtest-tests.ps1`; Mutationen mit echten Windows-Adaptern:
`./probe/installtest-mutations.ps1`. Mutanten laufen als isolierte Skriptkopien;
der Produktionsquelltext bleibt bytegleich. Der negative Admin-Kontotest samt Mutation
braucht ein Admin-/UAC-Konto und läuft im Windows-CI; lokale Standardkonten melden einen Skip.

GATE prüft die Provenance-Abweisung separat am echten `gh`-Adapter: Release-EXE und
`provenance.jsonl` in einem Verzeichnis bereitstellen, dann mit GitHub-CLI-Zugang
`./probe/installtest-mutations.ps1 -ReleaseDirectory C:\Probe\release -ReleaseVersion 0.1.0`
aufrufen. Erst muss die unveränderte EXE die Herkunftsprüfung bestehen; anschließend
muss eine Kopie mit angehängtem Byte abgewiesen werden. Der entsprechende Mutant
überspringt die Herkunftsprüfung und muss rot werden. Diese optionale Probe führt
`gh`, aber keine Release-EXE aus; ohne `-ReleaseDirectory` bleibt sie sichtbar übersprungen.

## Linux-Server-Modus

Nur Geräteart `server`, Startpolicy `always`, Vertrag 2. Der Läufer holt Manifest,
Aufträge und Abbrüche selbst ab; er öffnet keinen Port. Ein gemeinsamer Laufplatz
für Ad-hoc und Zeitpläne, pro Lauf ein eigener Container. Python, WTS und DPAPI
werden auf diesem Weg nicht verwendet. `declared_tools` bleibt in K1 leer.

### Installation mit rootless Docker oder Podman

Standard ist ein eigener Systemnutzer `jnpt-runner` mit einem dedizierten
rootless Docker-Daemon (Docker 20.10/API 1.41 oder neuer). Der Nutzer darf nicht
root und nicht die feste Job-UID 65532 sein. Docker rootless gemäß dessen
Installationsanleitung mit Sub-UID/Sub-GID-Bereichen und dauerhaftem Userdienst
einrichten. Kein gemeinsam genutzter Daemon: Beim Neustart entfernt der Läufer
alle Container mit Label `ai.januaport.run`.

1. `CGO_ENABLED=0 go build -trimpath -o jnpt-runner .`, Binärdatei unter
   `/usr/local/bin/jnpt-runner` installieren (Release-Pakete/Signatur folgen separat).
2. [Konfigurationsvorlage](systemd/runner.example.json) nach
   `/etc/jnpt-runner/runner.json` kopieren. `mode` muss `container` sein;
   `docker_path`, `state_dir` und ein gesetztes `secrets_dir` sind absolute Pfade.
   Zustand und Geheimnisverzeichnis dürfen sich nicht überlappen.
3. `/var/lib/jnpt-runner` und `/var/lib/jnpt-runner/state` mit Eigentümer
   `jnpt-runner`, Modus 0700 anlegen. Credential, Integritätsschlüssel, Manifest,
   Journal und Sperrdatei erhalten 0600. Fremde Rechte oder Symlinks werden
   abgewiesen. Der Sperr-Inode liegt außerhalb des bei Widerruf gelöschten `state`.
4. [systemd-Unit](systemd/jnpt-runner.service) installieren. Ein Drop-in setzt
   `Environment=DOCKER_HOST=unix:///run/user/<uid>/docker.sock` für den tatsächlichen
   Nutzer. Die Unit läuft ohne Fähigkeiten, mit `NoNewPrivileges`, read-only
   System und Schreibrecht ausschließlich im Zustandsverzeichnis. Keine
   privilegierte Läufer-Installation; rootful erfordert allenfalls Socket-Gruppe.
5. Als `jnpt-runner` vor Dienststart koppeln:
   `jnpt-runner pair --config /etc/jnpt-runner/runner.json --url https://anlage.example`.
   Den Code über stdin eingeben, danach EOF; dieselbe Docker-Umgebung wie im Dienst
   setzen. `--ca` oder `--fingerprint` funktionieren wie unter Windows.
   Dann `systemctl enable --now jnpt-runner` und
   `jnpt-runner status --config /etc/jnpt-runner/runner.json` prüfen.

Podman ab Version 4 kann als Docker-kompatibler CLI-Pfad konfiguriert werden.
Für die gehärtete Unit einen separat gestarteten rootless Podman-API-Userdienst
und `podman-remote` verwenden; `CONTAINER_HOST` zeigt auf dessen lokalen Socket.
So benötigt der Läufer keine privilegierten Usernamespace-Helfer. Podman benötigt
für `--init` auch seinen Init-Helfer. Die Rootless-Erkennung prüft Docker
`SecurityOptions` bzw. Podman `host.security.rootless`, nicht den CLI-Namen.
Kopplung und Heartbeat melden `rootless`/`rootful` ehrlich. Rootful bleibt erlaubt:
Wer den Läufer samt rootful Socket übernimmt, kann den Host übernehmen.
Die lokalen Docker-Belege verwenden rootful Docker; Podman/rootless-Erkennung
ist über synthetische Runtime-Antworten geprüft, kein Live-Podman-Beleg.

### Image, Eingabe und Lauf-Token

Nur `repo@sha256:<64 kleine Hex-Zeichen>`, ohne Tag. Vor jedem Lauf Pull per
Digest und Vergleich von `.RepoDigests` mit dem freigegebenen Digest; bei
Abweichung `refused/hash`, ohne Jobstart. Der eigentliche Start verwendet
`--pull never`, Nicht-root UID/GID 65532, read-only Wurzel, `cap-drop ALL`,
`no-new-privileges`, 128 Prozesse, Pin-CPU/RAM/Wandzeit, `--init` und `--rm`.
`--memory-swap` ist gleich `--memory`: Das gemeinsame RAM-plus-Swap-Limit
entspricht dem RAM-Limit; zusätzlicher Swap ist damit ausgeschlossen.
Die CPU-/RAM-/Wandzeit-Obergrenzen entsprechen K1: 16000 mCPU, 32 GiB, 86400 s.
`/tmp` (64 MiB) und `/run/jnpt` (1 MiB) sind `noexec,nosuid,nodev`-tmpfs.
Der Containername ist `jnpt-run-` plus den ersten 32 Hex-Zeichen von SHA256 des
Laufschlüssels; das Label enthält den ursprünglichen Schlüssel.

**Image-Voraussetzung:** Das Job-Image muss `/bin/sh` und `tar` enthalten.
Der Läufer setzt ausdrücklich `--entrypoint /bin/sh` und überschreibt damit
auch einen eigenen Image-ENTRYPOINT. Der feste Startcode im Job entpackt einen
vom Läufer erzeugten Tar-Stream aus stdin und
führt anschließend den signierten Befehl mit `exec "$@"` aus. Pin-Argumente
werden nicht in Shelltext eingesetzt. Der Host startet ausschließlich die CLI
per Argumentliste. Der Pin kann den festen ENTRYPOINT nicht überschreiben.
Diese Image-Konvention erweitert das K1-Pin-JSON nicht.

Der Stream enthält `/run/jnpt/input.json`, `/run/jnpt/token` und zuletzt `ready`,
jeweils UID/GID 65532, Modus 0400. Vor dem signierten Befehl ist alles verfügbar.
Der Lauf-Token steht weder in Env, Argumenten noch Labels und bleibt im Job-tmpfs.
Der gewählte exec-freie Weg funktioniert auch bei rootless UID-Abbildung.
`docker cp` ist hierfür ungeeignet: Docker verweigert das Schreiben bei read-only
Wurzel mit `--tmpfs`; alternative Mount-Formen liefern keinen verlässlichen
Zugriff auf das laufende tmpfs. Es gibt keinen Bind-Mount des Läufer-Verzeichnisses,
Gerätetokens oder Docker-Sockets. Die echte Jobprobe belegt fehlenden Zugriff
auf den Gerätetoken (im Job fehlt der Hostpfad) und beide üblichen Socketpfade.

K1-Zeitpläne sind werkzeuglos und erhalten keinen Lauf-Token: K1 verweigert dann
`runs/start`. Der Client startet aus dem signierten Manifest, auch offline nur
innerhalb der bisherigen Gnadenfrist. Die Token-Datei ist dabei leer. Ad-hoc
verwendet Claim → Start. Ausgabe wird verworfen, Docker-Logging ist abgeschaltet.
Registry-Zugriff für den Digest-Pull ist auch bei einem Offline-Lauf erforderlich.

### Betreiber-Geheimnisse und Netz

Geheimnisse sind kernseitig ab Werk aus. Das Betreiber-Verzeichnis, beispielsweise
`/etc/jnpt-runner/secrets`, gehört `jnpt-runner` mit Modus 0700. Eine reguläre Datei
je Name; keine Symlinks, auch nicht im Verzeichnispfad. Die Datei muss für die
Container-UID 65532 lesbar sein: etwa Modus 0444 innerhalb des 0700-
Verzeichnisses oder passend zugeordnete UID. Werte werden nicht eingelesen oder
als Env übertragen. Pro Name genau ein read-only Bind nach `/run/secrets/<name>`.
Ein deklarierter und erlaubter, aber fehlender Eintrag ergibt `refused/secret`.

Bei rootful Docker ohne zusätzliche Usernamespace-Abbildung entspricht
Container-UID/GID 65532 der Host-UID/GID 65532. Rootless Docker und Podman bilden
die IDs hingegen über die Bereiche aus `/etc/subuid` und `/etc/subgid` ab.
Synthetisches Beispiel für beide Runtimes mit Standard-Abbildung:

```text
/etc/subuid:  jnpt-runner:100000:65536
/etc/subgid:  jnpt-runner:100000:65536

uid_map:     Container-ID  Host-ID  Anzahl
                       0     1000       1
                       1   100000   65536
gid_map:               0     1000       1
                       1   100000   65536
```

Bei dieser Abbildung gilt `Host-ID = 100000 + (Container-ID - 1)`:
Container-UID/GID **65532 entspricht Host-UID/GID 165531**. UID 0 im Namespace
entspricht hier dem Läufer-Nutzer mit Host-UID/GID 1000. Die Bereiche und
Abbildungen sind installationsabhängig; bei abweichenden Sub-GIDs die GID
getrennt berechnen. Maßgeblich ist die tatsächliche Abbildung des Jobs auf dem
Linux-Host des Daemons. Für einen laufenden Job prüfen:

```sh
job_name=jnpt-run-... # tatsächlichen Containernamen einsetzen
job_pid=$(docker inspect --format '{{.State.Pid}}' "$job_name")
sudo cat "/proc/$job_pid/uid_map" "/proc/$job_pid/gid_map"
```

Bei Podman denselben Aufruf mit `podman inspect` verwenden. Auf diesem Host
lassen sich die Rechte für die obige Beispiel-Abbildung so setzen:

```sh
sudo install -d -m 0700 -o jnpt-runner -g jnpt-runner /etc/jnpt-runner/secrets
sudo install -m 0400 -o 165531 -g 165531 ./synthetic-secret /etc/jnpt-runner/secrets/alpha
```

Das Verzeichnis bleibt Eigentum des Läufer-Nutzers; nur die Geheimnis-Datei
gehört der abgebildeten Job-UID. Bei lokalem rootless Podman kann alternativ
als `jnpt-runner` außerhalb der gehärteten Unit `podman unshare chown 65532:65532
/etc/jnpt-runner/secrets/alpha` und anschließend `podman unshare chmod 0400
/etc/jnpt-runner/secrets/alpha` verwendet werden. Das gilt für die gezeigte
Standard-Abbildung; bei weiteren Usernamespaces wieder die Job-Abbildung prüfen.

Ad-hoc bindet ausschließlich `allowed_secret_names` ∩ signierte `secret_names`.
Fehlende/leere Claim-Liste bindet nichts; lokale Konfiguration erweitert keine
Freigabe. Zeitpläne verwenden die signierte Deklaration, auch im historischen
Offline-Fenster. K1-Namen: `[a-z0-9][a-z0-9_.-]{0,62}`, kein `..`, eindeutig,
höchstens 16.
Ergebnisse senden `mounted_secret_names`, ausdrücklich auch `[]`. Nur nachgewiesene Starts melden Mount-Namen. Bei einem Absturz vor der
Ergebnissicherung meldet der Marker die leere, unbekannte Teilmenge; die signierte
Deklaration bleibt im Kern-Audit erhalten. Ein fehlgeschlagener Start wird nicht
als ausgeführter Geheimnis-Mount ausgegeben.

Netz: `none` oder ein vorab vom Betreiber angelegtes benanntes Netz. `host`,
`bridge`, `default`, `private`, `pasta`, `slirp4netns`, `container:` und `ns:` sind
gesperrt. Keine DNS-/Host-/IP-Zusatzoptionen. Beispiel:
`docker network create --internal jnpt_jobs`; nur den Kern und diese Jobs mit
diesem Netz verbinden. Das begrenzt externen Ausgang, ist aber keine Ziel-Allowlist:
Andere Netzteilnehmer bleiben erreichbar. Weitere Ziele, Firewall und Routing
verantwortet der Betreiber; der Kern prüft sie nicht. `none` isoliert das Netz.

Abbruch, Wandzeit und normales Ende versuchen immer `kill` und `rm --force`.
Neustart räumt zuerst gelabelte Container ab, dann journalisiert er offene Läufe
als `interrupted`. Bei fehlgeschlagener Bereinigung startet der Dienst nicht.
Keine Wiederholung alter Läufe. Ein fehlgeschlagener Cleanup sperrt weitere
Containerstarts bis zum Neustart. Das Linux-Journal behält dieselben Deckel:
200 Ergebnisse/16 MiB einschließlich HMAC, Markern und Zähler; HMAC prüft Integrität,
Dateirechte schützen Vertraulichkeit. Der Integritätsschlüssel liegt separat 0600.

### Server-Prüfung und K1-Abstimmung

`go vet ./...`, `go test ./...` und unter Linux `go test -race -count=1 ./...`.
`JNPT_DOCKER_REQUIRED=1` macht fehlendes Docker zum Fehler; im Linux-CI-Job ist
das gesetzt. Ohne Docker überspringen die echten Proben sichtbar. Docker-Tests
prüfen Token/Inspect, Gerätetoken-/Socket-Isolation, read-only Geheimnisse sowie
Abbruch, Wandzeit und Wiederanlauf. Synthetisches Testimage: Alpine, nach Pull
stets per Digest; optional `JNPT_TEST_IMAGE=repo@sha256:…` vorgeben.
`python probe/container_mutations.py` prüft die neuen Sperren mit exakter
Wiederherstellung, optional `--container <lokaler Linux-Prüfcontainer>`.

**Bekannte K1-Lücke:** Auf `origin/feat/1195-server-container` verweigert
`cmd/jnpt/desktop_routes.go:desktopResult.valid` noch `refused/secret` und
`limit/wall`. Paket A sendet die beauftragten Klassen unverändert. Bis der Kern
sie akzeptiert, bleiben diese Ergebnisse nach HTTP 400 im Journal. Keine stille
Umdeutung in `hash` oder `timeout`; der Kern benötigt vor Live-Abnahme den Nachzug.
