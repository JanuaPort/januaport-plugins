# EBICS-Abholer

Ein kleiner, eigenständiger Container, der täglich den Kontoauszug (camt.053)
per EBICS bei Ihrer Bank abholt und als Datei in einem gemeinsamen Verzeichnis
ablegt, aus dem JanuaPort ihn liest.

## Was es ist

- **Bauklasse Aktuator:** ein optionaler Beisteller **neben** dem
  JanuaPort-Gateway, kein Teil davon. Eigener Docker-Container, eigener
  Compose-Stack, eigener Projektname.
- **Warum getrennt statt eingebaut:** JanuaPort selbst ist ein einziges,
  CGO-freies, statisches Go-Binary in einem distroless-Container — das ist ein
  Produktversprechen. Die einzige gepflegte Bibliothek, die die aktuellen
  EBICS-Protokollversionen (H003–H005) abdeckt, ist in PHP geschrieben; für Go
  gibt es dafür derzeit keine brauchbare Umsetzung. Also ein zweiter, kleiner
  Container daneben, statt PHP ins Gateway zu holen.
- **Was es NICHT ist:**
  - **Kein gehosteter EBICS-Dienst.** Niemand sitzt zwischen Ihnen und Ihrer
    Bank. Schlüssel und Zugang bleiben auf Ihrer eigenen Anlage.
  - **Keine Zahlungseinreichung.** Kein Skript kann etwas einreichen — das ist
    strukturell so gebaut, nicht nur „nicht implementiert". Lassen Sie sich
    zusätzlich von Ihrer Bank bestätigen, dass der Teilnehmer nur
    Abruf-Rechte hat.

## Datenvertrag zum Gateway

Wichtig, bevor Sie weiterlesen: Der Abholer spricht **nicht direkt mit
JanuaPort**. Kein `/mcp`-Aufruf, kein eigener Token, keine Kartei-Satzart — er
kennt JanuaPort strukturell gar nicht. Er redet ausschließlich mit der Bank
(EBICS über HTTPS) und mit dem Dateisystem.

Die einzige Naht ist ein **gemeinsames Verzeichnis**:

- Der Abholer schreibt Dateien nach `/ablage` im Container — bei Ihnen ein
  Host-Verzeichnis (z. B. `/var/lib/jnpt/ebics-eingang`).
- Genau dieses Host-Verzeichnis hängen Sie schreibgeschützt bei JanuaPort als
  Quelle einer **Datei-Integrationsart** ein; JanuaPort liest die abgelegten
  Dateien von dort mit seinem camt.053-Parser.
- Abgelegt wird, **bevor** die Quittung an die Bank geht: Erst wenn jede Datei
  eines Laufs vollständig liegt, wird positiv quittiert — sonst negativ, und
  die Bank führt die Daten weiter als neu. Damit geht bei einem Fehlschlag
  (volle Platte, Rechteproblem) kein Auszug verloren.
- **Dateiname:** `camt053-JJJJ-MM-TT-NN.xml`. Das Datum darin ist der
  **Abruftag**, nicht der Buchungstag (der steht innerhalb der Datei). Bei
  einem Namenskonflikt wird nummeriert (`…-NN-2.xml`), **nie überschrieben**.
  Der Name wird vom Abholer selbst gebildet und nie aus Angaben der Bank
  übernommen (Schutz vor Pfad-Ausbruch).
- Im Betriebsprotokoll (journald) stehen ausschließlich **Anzahl und
  Dateinamen** der abgelegten Dateien — nie ein Kontoumsatz, nie eine IBAN,
  nie ein Betrag.

## Voraussetzungen

- Docker und `docker compose` auf einem Host, der dasselbe Ablage-Verzeichnis
  wie JanuaPort erreicht (muss nicht dieselbe Maschine sein).
- Ein **EBICS-Zugang bei Ihrer Bank**, eingerichtet mit ausschließlich
  Abruf-Rechten (kein Einreichen). Sie erhalten dafür ein Schreiben mit:
  Host-ID, URL des Bankzugangs, Kunden-/Partner-ID, Teilnehmer-/User-ID, der
  unterstützten Protokollversion (`H004` = EBICS 2.5, `H005` = EBICS 3.0) und
  den **Prüfsummen der Bankschlüssel** — heben Sie dieses Schreiben auf, Sie
  brauchen es später für einen Pflichtschritt.
- Ein Passwortmanager: Dort landen das selbstgewählte Schlüsselwort der
  Schlüsseldatei und später die Sicherung der Schlüsseldatei selbst.
- **Zeitlicher Vorlauf:** Zwischen Antrag und erstem Auszug liegen üblicherweise
  **Wochen** (Freischaltung durch die Bank, Papierprotokoll mit Unterschrift).
  Das ist kein technisches Problem — beantragen Sie den Zugang rechtzeitig.

## Einrichtung

1. Verzeichnisse anlegen: ein Ablage-Verzeichnis (Eigentümer UID 65532, wie
   der Container läuft) und ein separates, restriktives Verzeichnis für
   Zugangsdaten/Schlüsseldatei (`chmod 700`, ebenfalls UID 65532).
2. `config.example.php` nach `config.php` kopieren und **von Hand in einem
   Editor** ausfüllen — Host-ID, URL (**muss** `https://` sein), Partner-/
   User-ID, Protokollversion, Schlüsselwort, Ablage-Pfad. Niemals durch einen
   Chat, ein KI-Werkzeug oder ein Ticketsystem schleusen.
3. Image bauen: `docker compose -f docker-compose.ebics-abholer.yml build`.
4. Einmalig `init.php` ausführen — erzeugt die Schlüsselpaare, sendet INI/HIA
   an die Bank und schreibt ein Initialisierungsprotokoll (PDF). **Ab hier
   läuft eine Frist** (bei den meisten Banken zehn Tage), bis das
   unterschriebene Protokoll bei der Bank vorliegen muss.
5. Schlüsseldatei **und** Schlüsselwort sofort sichern, beide zusammen im
   Passwortmanager — ohne das Wort ist die Datei wertlos.
6. Nach Freischaltung durch die Bank: `hpb.php` (Erstabruf) holt die
   Bankschlüssel. **Pflicht-Handgriff:** die ausgegebenen Prüfsummen von Hand
   mit dem Schreiben Ihrer Bank vergleichen. Stimmen sie nicht überein: **nicht
   weitermachen**, bei der Bank melden — das ist der einzige Schutz gegen einen
   untergeschobenen Gegenüber.
7. Ersten Abruf von Hand ausführen und beobachten, danach den Zeitgeber
   scharf schalten (siehe Betrieb).

> ⚠️ Ein `-v`-Mount an `docker compose run` übersteuert das `:ro` der
> Compose-Datei **nicht**. Für die schreibenden Schritte (`init.php`,
> Erstabruf mit `hpb.php`, Schlüsselübernahme mit `--uebernehmen`) braucht es
> **`docker run`** mit eigenem Mount statt `docker compose run`.

## Betrieb

- Geweckt wird der tägliche Lauf von einem **systemd-Timer**
  (`ebics-abholer.timer`/`.service`, liegen bei), nicht von einem Scheduler im
  Container — die Uhr steht auf dem Host.
- Standardmäßig dreimal am Tag (`05:30`, `11:30`, `17:30`, Server-Zeitzone, mit
  Zufalls-Streuung): der erste Lauf ist der eigentliche, die beiden späteren
  sind Nachholversuche, falls die Bank morgens nicht erreichbar war. Ein
  zusätzlicher Lauf ist folgenlos — die Bank liefert je Auftrag nur, was sie
  noch nicht quittiert hat.
- `Persistent=true`: Ein verpasster Lauf (Ausfallzeit, Neustart, Urlaub) wird
  beim nächsten Start nachgeholt. Es geht kein Auszug verloren.
- **Alarmierung: ausschließlich auf Exit-Code ≠ 0 schalten**, nie auf „ist
  heute eine Datei angekommen". Ein Kontoauszug existiert **je Buchungstag,
  nicht je Kalendertag** — an Wochenenden und Feiertagen liefert die Bank
  schlicht nichts, und genau das ist Exit 0 (Erfolg), keine Störung. Ein
  Alarm, der jeden stillen Sonntag rot färbt, erzieht dazu, rote Meldungen zu
  ignorieren.
- **Exit-Codes von `fetch.php`:**

  | Code | Bedeutung |
  |---|---|
  | `0` | abgeholt · oder die Bank hatte nichts Neues |
  | `1` | Konfiguration/Umgebung unbrauchbar — nichts abgerufen |
  | `2` | echter Fehler bei Bank, Netz oder Protokoll |
  | `3` | abgerufen, aber **nicht abgelegt** (negativ quittiert — die Bank behält die Daten, der nächste Lauf holt sie erneut) |
  | `4` | Bankschlüssel passen nicht mehr (EBICS `091008`) — nichts abgerufen, nichts quittiert |

- **Schlüsselwechsel der Bank:** Banken erneuern ihre EBICS-Schlüssel
  gelegentlich; das zeigt sich als Exit `4`. `hpb.php --pruefen` (read-only)
  vergleicht die aktuellen Bankschlüssel gegen die gespeicherten, ohne zu
  schreiben. `hpb.php --uebernehmen --x002=… --e002=…` übernimmt neue
  Schlüssel — **nur**, wenn beide übergebenen Prüfsummen zum **neuen**
  Schreiben der Bank passen (aus dem Schreiben abschreiben, nie von der
  `--pruefen`-Bildschirmausgabe zurückspielen). Vor jedem Schreiben wandert die
  alte Schlüsseldatei als nummerierte Sicherung zur Seite — überschrieben wird
  nie.

## Grenzen

- **Nur Abholen.** Kein Skript kann etwas einreichen (kein Zahlungsauftrag,
  keine Unterschriftenmappe).
- **Kein automatisches Datumsfenster.** Der tägliche Lauf holt, was die Bank
  noch nicht quittiert hat. Ein Nachzug über einen bestimmten Zeitraum
  (`--von`/`--bis`) ist ein bewusster Handgriff von Hand und kann Dateien
  doppelt liefern (werden nummeriert, nie überschrieben).
- **Ein erfolgreich getestetes Institut ist kein Beleg für alle.** Die
  eingesetzte Bibliothek deckt H003–H005 ab, aber Protokollversion,
  Auftragsarten und Freischaltpraxis unterscheiden sich je Bank.
- **Kein automatischer Abgleich der erlaubten Abrufe.** Welche Auszüge ein
  Teilnehmer abrufen darf (HAA/HTD), klären Sie mit Ihrer Bank — dafür ist die
  Auftragskennung in der Konfiguration frei einstellbar, aber nicht
  selbst-erkundend.
- **Kein gehosteter/verwalteter Dienst.** Sie betreiben den Container selbst,
  auf eigener Infrastruktur.
- Teilt mit JanuaPort **ausschließlich** das Ablage-Verzeichnis — kein
  Netzwerk, keine Datenbank, kein Docker-Socket, kein Eintrag im
  Gateway-Compose-Stack.

## Sicherheitshinweise

- **Schlüssel und Kontodaten gehören nie ins Repo.** `config.php` (Ihre
  ausgefüllten Zugangsdaten), `keyring.json` (die Schlüsseldatei — ab
  Freischaltung Ihr Bankzugang selbst), ihre nummerierten Sicherungen und das
  Initialisierungsprotokoll (PDF) sind über `.gitignore` ausgeschlossen. Nur
  die Vorlage `config.example.php` mit Platzhaltern ist Teil dieses Ordners.
- **Die Ablage IST Kontodaten.** Das Verzeichnis, in das der Abholer schreibt,
  enthält echte Kontoauszüge — behandeln Sie es entsprechend (Zugriffsrechte,
  Backups, keine Kopie an Dritte).
- `config.php` niemals durch einen Chat, ein KI-Werkzeug oder ein
  Ticketsystem schleusen — von Hand in einem Editor ausfüllen.
- Läuft als **Non-root, UID 65532** — dieselbe Kennung wie ein
  distroless-Gateway.
- Container-Härtung: `read_only: true`, `cap_drop: ALL`,
  `no-new-privileges:true`, kein gemeinsames Netzwerk mit dem Gateway.
- Klartext-HTTP zur Bank wird verweigert — die konfigurierte URL muss
  `https://` sein.
- Schreibvorgänge (Auszüge wie Schlüsseldatei) laufen atomar über eine
  versteckte Zwischendatei plus `rename` — kein halb geschriebener Auszug,
  kein halb ersetzter Schlüssel, auch bei einem Absturz mitten im Lauf.
- Der Prüfsummen-Abgleich bei einem Bankschlüsselwechsel ist der einzige
  Schutz gegen einen untergeschobenen Gegenüber — kein Auto-Accept, keine
  Abkürzung dafür vorgesehen.

## Status

Beim Hersteller produktiv im Einsatz belegt: Erstinitialisierung gegen eine
echte Bank am 20.08.2026, seit 07.09.2026 im laufenden Timer-Betrieb für ein
eigenes Firmenkonto (EBICS H005) — inklusive eines realen
Bankschlüssel-Prüflaufs. Ein Beleg gegen ein zweites Institut oder gegen EBICS
H004 steht aus.

## Enthaltene Dateien

| Datei | Wofür |
|---|---|
| `fetch.php` | der tägliche Abruf |
| `init.php` | einmalige Erst-Initialisierung (Schlüssel erzeugen, INI/HIA senden) |
| `hpb.php` | Bankschlüssel holen/prüfen/übernehmen (Erstabruf, `--pruefen`, `--uebernehmen`) |
| `letter.php` | Initialisierungsprotokoll aus einer vorhandenen Schlüsseldatei neu erzeugen (spricht mit niemandem) |
| `logik.php` / `start.php` / `version.php` | interne Logik- und I/O-Bausteine |
| `docker-compose.ebics-abholer.yml`, `Dockerfile` | Container-Setup |
| `ebics-abholer.service`, `ebics-abholer.timer` | systemd-Einheiten für den täglichen Lauf |
| `config.example.php` | Konfigurationsvorlage (nur Platzhalter, kein echter Zugang) |
| `tests/run.php` | Testsuite ohne jeden Netzwerkzugriff (assert-basiert, kein PHPUnit) |

Tests ausführen (reine Logik, kein Netz, keine Bank):

```bash
docker run --rm -v "$PWD:/app" -w /app php:8.5-cli php tests/run.php
```

---

Lizenz: Apache License 2.0, siehe [`../LICENSE`](../LICENSE) und
[`../NOTICE`](../NOTICE). Beiträge: [`../CONTRIBUTING.md`](../CONTRIBUTING.md).
Sicherheitslücken bitte an `security@januaport.ai`.
