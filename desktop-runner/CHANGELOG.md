# Changelog

## Unreleased

### 0.4.0 in Arbeit

- Lokaler Halt im Windows-Engine (#1240 L2): atomare DPAPI-Datei vor dem
  ersten Takt; fehlend bedeutet arbeitet, unlesbar/ungültig angehalten plus
  lokale Störung (`8 1`). Anhalten beendet den Job-Baum auch offline mit
  `cancelled`; Halt und Spawn verwenden dieselbe Sperre (L2-1/L2-2, B6).
- Pause für 15/60 Minuten oder bis Weiter lässt laufende Arbeit enden und
  sperrt Slots/Claims/Testaufträge. Fortsetzen ohne Nachholen und ohne Umgehen
  der Startprüfungen (B3); Heartbeat bleibt bereit. Nur interne Go-Steuerung.
- Letzte Halt-Meldung koalesziert nachliefern, höchstens sechs Versuche pro
  Minute, auch über Neustarts. Race-/Adaptertests und Mutationsproben;
  native Windows-Sonde für Anhalten samt Enkelprozess (Windows-CI).

## 0.3.0 — 2026-11-03

- Testgeräte synchronisieren wieder (#1298): `test_device` wird in jeder
  unterstützten Manifestfassung angenommen; Signaturprüfung bleibt verbindlich.
- Windows spricht Vertrag 4 in Header und JSON, Linux-Server weiter Vertrag 2.
  Strikte Setup-/Frei-/Pinfrist-Typen; `apps` und Beschriftungen über 40 Zeichen
  werden abgewiesen. D0-Modul und Goldens v1/v2 bleiben unverändert.
- Adapter für Setup-Start (Manifestsequenz, zufällige Nonce), Halt-Meldung,
  Testresultate und PNG-Upload. Beide Testwege prüfen signierte Freigabe und
  Geräte-Testpin; der normale Ergebnisweg weist `result` ab.
- K8: Pinfrist vor Gerätefrist, persistente DPAPI-Uhrmarke gegen Uhr-Rücksprung,
  fester Logcode `7 1` und markierte Offline-Nachmeldung. Kern-Goldens gespiegelt,
  Adapter-/Negativtests mit Mutationsproben.
- L1 enthält keine Halt-Steuerung, Pipe, Bedienfeld oder Bildschirmaufnahme.

## 0.2.0 — 2026-10-30

**Setzt einen Kern ab 0.89.0 voraus.** Ältere Kerne lehnen `refused/idle` ab; der Läufer
verwirft solche Ergebnisse nach zehnmal HTTP 400 in Folge mit Logzeile. Skripte bekommen
ein eigenes TEMP je Lauf, das nach dem Lauf gelöscht wird.

- Ablehnungsklasse `refused/idle`: fehlt bei passendem Konto und aktiver Sitzung nur
  der Leerlauf der Startregel, meldet der Läufer `idle` statt `account`, vor jedem
  Start (JanuaPort/januaport#1245 L). **Release-Hinweis: setzt einen Kern mit
  #1245 K voraus** (er nimmt `idle` an). Ein älterer Kern antwortet 400.
- Ein vom Kern zehnmal in Folge mit HTTP 400 abgewiesenes Ergebnis blockiert den
  Rückstau nicht mehr: Es wird gezählt verworfen (Verlustzähler, Logzeile `5 <n>`).
  Bisher blieb es dauerhaft an erster Stelle des Journals liegen (betrifft auch die
  offenen K1-Klassen `refused/secret` und `limit/wall`).
- Privates TEMP je Lauf: frisches Verzeichnis mit DACL des Läufer-Ordners unter
  `%LOCALAPPDATA%\JanuaPort\desktop-runner\tmp\<Lauf-ID>\` als `TEMP`, `TMP` und
  Arbeitsverzeichnis statt `envs\…\runtime`; nach dem Job-Teardown gelöscht, Rest
  räumt der Start-Besen, ohne Junctions oder Symlinks zu betreten. Die Kind-Umgebung
  bleibt ohne `LOCALAPPDATA`/`USERPROFILE`/`APPDATA`. Behebt, dass Skripte ohne TEMP
  in die geprüfte Umgebung schrieben und der nächste Lauf mit `refused/hash` endete
  (Befund 6).
- Widerruf löscht den Zustand mit demselben link-sicheren Löscher (kein `os.RemoveAll`),
  damit eine vom Skript in `tmp\` angelegte Junction nie betreten wird (SEC R1).

- Manueller Installationstest des signierten Windows-Releases in einem frischen
  Standardkonto, mit SHA-256, Authenticode/Herausgeber und GitHub-Provenance,
  Showcase-Kopplung, Installation, fünf Minuten Anlagenkontakt und Deinstallation
  einschließlich Prüfung beider Autostartwege (#1233).
- Offline-Adaptertests und isolierte Mutationsproben für die Testsperren;
  separate Provenance-Negativprobe samt Mutation mit echten Release-Assets und `gh`;
  Dokumentation der Grenzen: Windows-Code nur per argv, kein zuverlässig lokal
  abrufbarer Auftragsabschluss, WTS/Kontobindung auf Hosted-CI und
  `cancelled` beim Admin-Abbruch gegenüber `interrupted` beim Wiederanlauf.

## 0.1.0 — 2026-10-28

Erstes Release: `desktop-runner.exe` für Windows amd64, Authenticode-signiert
(JanuaPort/januaport#1163 D4). Der Linux-Server-Modus ist im Code, wird aber nicht als
Artefakt ausgeliefert; sein Release braucht den Live-Beleg mit rootless Docker (#1195).

- Fester Container-ENTRYPOINT `/bin/sh`, auch bei eigenem Image-ENTRYPOINT;
  Swap-Limit gleich RAM-Limit und Anleitung zur Lesbarkeit von Geheimnissen
  für UID 65532 einschließlich rootless Sub-UID/Sub-GID-Beispiel
  (JanuaPort/januaport#1195).

- Linux-Server-Modus mit Vertrag 2 und signierten Container-Pins
  (JanuaPort/januaport#1195); Windows und Vertrag-1-Goldens bleiben unverändert.
- Docker-CLI über eine testbare Naht, feste Sandbox-Argumente, Digestvergleich,
  tmpfs-Token über privaten stdin, read-only Betreiber-Geheimnisse und Rootless-Meldung.
- Linux-Dateispeicher 0600/0700 mit HMAC-Integrität, bisherige Journaldeckel,
  Abbruch-/Wandzeit-Bereinigung und Wiederanlauf als interrupted.
- Gehärtete systemd-Vorlage, verpflichtende echte Docker-Proben in Linux-CI
  und reproduzierbare Mutationsproben.
- K1 muss noch `refused/secret` und `limit/wall` im Ergebnisadapter akzeptieren.
  Release-Pakete, Signierung, C3, Objekt-URLs und cosign sind nicht enthalten.
