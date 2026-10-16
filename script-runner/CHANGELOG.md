# Changelog — script-runner

## 0.2.0 — 2026-10-16 (JanuaPort/januaport#1040 P3, #1041 P1/P2, #1096, #1069)

- **Vertragsfassung 5, `contract: 2`:** Das Gateway gibt Wandzeit, Ausgabebytes
  und Werkzeugaufrufe je Lauf über `X-Jnpt-Run-Limits` vor. Strenges Format,
  feste Fehler ohne Headerwert und Ablehnung vor Lauf/Slot bei Formfehlern.
  Ohne Header bleiben die Altgrenzen 25 s / 1 MiB / 50 Aufrufe; höhere Vorgaben
  werden auf die technischen Decken 24 h / 16 MiB / 10 000 gekürzt.
- **SEC L6:** Die eigenständige 25-s-Kappe bleibt in P3 wirksam:
  Minimum aus Kopfzeile, Manifest und Kappe. Manifest weiterhin Pflichtfeld
  `timeout_s` in 1..25; Eingabe unverändert höchstens 256 KiB. Ausgabe und
  Aufrufe nutzen die Grenze dieses Laufs, auch im festen Fehlertext wie im
  Gateway 0.77.0. Wächter streamt ohne kleinere Summengrenze.
- **Benannte Kompatibilitätsgrenze (Minor-Version):** Ein Läufer mit
  `contract: 2` braucht ein Gateway ab **0.77.0**. Ein älteres Gateway sperrt
  den Läufer ganz („andere Vertragsfassung“).
  **Update-Reihenfolge: erst das Gateway, dann der Läufer.** Neues Image nötig.
- Test-first, Golden-Dateien für Kennung 2 und Altgrenzen ohne Header,
  Adaptertests und 19 wirksame Mutationsproben. Nachweis:
  [P3-Abschlussbericht](https://github.com/JanuaPort/januaport/issues/1040#issuecomment-6205261051).

### Ebenfalls in 0.2.0 (seit 0.1.4)

- **Unit-Test für unsichtbares Staging (#1041, SEC H1):** Prüft leeres `ready` während `build` und Veröffentlichung des fertigen Locks per atomarem Rename.

- **Kaltstart des Bibliotheks-Volumes abgesichert (JanuaPort/januaport#1041,
  #1096, LEAD-Review).** Der kurze Dienst `script-runner-init` führt als
  65532 ohne Netz ausschließlich `init-libs` aus. Die Sandbox wartet auf
  `service_completed_successfully` und mountet `ready` mit `nocopy: true`:
  fehlende Subpaths verhindern den ersten Start nicht mehr; Docker kopiert
  keine Image-Unterordner in die Sandbox-Sicht. Weiter genau ein zusätzlicher
  Sandbox-Mount. Hygiene prüft Init und Abhängigkeit; eine Sonde startet nach
  `down -v` frisch über Sandbox- und Betreiber-Einstieg und verlangt `/libs=[]`.

- **Lauf-Protokoll (JanuaPort/januaport#1041 P2).** `januaport.log(*teile)`
  liefert Text ausschließlich als additives `_meta["januaport.ai/run"].log`,
  auch bei Fehler, Wandzeit, Speicher und Abbruch nach dem Start; leer fehlt
  das Feld. Das Log bleibt additiv; aktuelle Vertragskennung siehe 0.2.0.
  Fester 64-KiB-Puffer bereits im Wächter,
  UTF-8-Ersatz und einmalige Kürzungsmarke innerhalb der Grenze; nie im
  Dienstlog. Golden-Dateien, Docker-Pin `log_canary` und Mutationsprüfungen.
- **Privates Wheel-Staging (JanuaPort/januaport#1096).** Ein Volume, zwei
  Unterordner: nur der vermittler und der vorgeschaltete Init sehen `staging`; die Sandbox mountet nur
  `ready` (weiter genau ein Zusatzmount). Veröffentlichung nach Manifestprüfung
  durch atomaren Rename im selben Mount. Bestehende P1-Caches brauchen eine
  Migration im Wartungsfenster (README); Engine ≥ 26, Compose ≥ 2.35.
  SEC H1/H2: Tests gegen Zentralverzeichnis-Anhang und Ensure während Reset.

- **Abbruch durch das Gateway gibt den Platz frei (JanuaPort/januaport#1040, SEC L1).**
  Bricht das Gateway einen laufenden Lauf ab (Entzug beim Aufrufer), endet er
  jetzt sofort mit `end=internal`; die Sandbox räumt ab und meldet nach
  `MinLifetime` eine neue Instanz. Vorher lief ein abgebrochener Lauf bis zu
  seiner Wandzeit weiter und hielt den einzigen Platz. Gemessen mit der neuen
  Docker-Sonde `TestL1AbbruchGibtPlatzFrei`: Platz frei nach 3,0 s bzw. 10,1 s,
  zweiter Lauf fertig nach 3,1 s bzw. 10,6 s (Schranken 18 s / 22 s).
  **Neues Image nötig**, keine Compose-Änderung.

- **Python-Bibliotheken als Wheels im Skript-Repo (JanuaPort/januaport#1041,
  P0 + P1; Variante A, kein Download, kein pip).** Ein Pin-Ordner kann
  `requirements.lock` (pip-Format, `--hash=sha256:`, nur Wheels) und
  `wheels/*.whl` mitbringen; das Manifest trägt dann `requirements:
  requirements.lock` (neues optionales Feld, nur dieser Wert; ohne das Feld
  verhält sich der Pin byte-gleich wie bisher, ein alter Läufer weist es als
  `manifest_invalid` ab). Der vermittler prüft Lock und Plattform-Tags
  (`cp312`/`abi3`/`py3`/`none` × `manylinux*_x86_64`/`any`, glibc bis 2.41),
  gleicht Wheel-Dateien und Lock-Einträge 1:1 ab, rechnet jedes Wheel gegen
  den Git-Hash (M5) und das Lock (SHA-256) nach und entpackt es gehärtet
  (SEC B4: Größen beim Streamen, Pfad- und Namensprüfung inkl. Case-Folding,
  keine Symlinks und Geräte, Modi 0444/0555, kein Zip64, Bomben-Test mit
  Spitzen-Heap) **asynchron** nach `libs/<Lock-Hash>/`; der Pin bleibt bis
  dahin `pending`. Neue Gründe `libs_invalid`, `libs_hash_mismatch`,
  `libs_platform`, `libs_too_large`. Jedes Verzeichnis trägt
  `.jnpt-manifest` (Pfad, Größe, SHA-256 je Datei, SEC B3) und wird beim Start
  einmal dagegen geprüft; Abweichungen werden verworfen. Ein Lock wird einmal
  vorbereitet, Nicht-mehr-Referenziertes aufgeräumt, ein URL-Wechsel leert
  `libs`. Der job trägt `libs` (Lock-Hash, 64 Hex); der Wächter prüft es als
  Hex, der Bootstrap hängt `/libs/<hash>` per `sys.path.append` an (kein
  `site`, keine `.pth`, keine Entry-Points); `childEnv` setzt
  `OPENBLAS_NUM_THREADS=1` und `OMP_NUM_THREADS=1`. **Compose:** neues Volume
  `libs` (beim vermittler beschreibbar, in der Sandbox read-only als der eine
  zusätzliche Mount, ohne `noexec`; `/work` und `/tmp` bleiben `noexec`) —
  **neue Compose und neues Image, ein Wartungsschritt mit Runbook-Eintrag**
  (P5). Das Bibliotheksformat bleibt unverändert. **Benannte Grenze:** Eigene,
  vertrauliche Pakete teilen sich die Sichtbarkeit mit allen Skripten der
  Anlage. **Messung P0** (numpy 2.5.4, pandas 3.0.6, runc): 91 MiB und 2478
  Dateien installiert, pandas-Import ohne pyc 1,2–1,4 s, RSS 82 MiB, ohne
  Thread-Deckel so viele Threads wie der Host CPUs hat; deshalb kein pyc vorab
  (SEC B7 nicht gebaut). Sonden `numpy_ok`, `libs_readonly`, `libs_tampered`,
  `no_pip`, `probe/measure-libs.sh`; runsc nicht gemessen. Nicht in diesem
  Paket: `januaport.log` (P2), Kern-Doku und Anleitung für die KI (P4),
  Release und Kuratierung (P5).

- **vermittler nur im internen Netz `jnpt_plugins` (JanuaPort/januaport#1069).**
  Der Dienst `script-runner` hängt nur noch am externen Netz `jnpt_plugins`, das
  die Kern-Compose mit `internal: true` anlegt (statt `jnpt_default`): kein Weg
  ins Internet oder ins LAN, Weg A und Weg B bleiben. Kein Host-Port mehr (vorher
  `127.0.0.1:8091`); Gesundheit über einen Docker-Healthcheck (`python3` des
  Images gegen `/healthz`, alle 10 s). Deskriptor-Vorlage `HEALTH_DOCKER=1`.
  Hygiene: keine Host-Ports, kein Dienst außer dem abholer mit Ausgang. Neue
  Sonden `probe/network_test.go`. **Neue Compose, kein neues Image nötig.**
  Voraussetzung auf der Anlage, in dieser Reihenfolge: Netz `jnpt_plugins` in
  der Kern-Compose, Aktuator-Fassung 3, dann diese Compose und der Deskriptor.

## 0.1.4 — 2026-10-14 (JanuaPort/januaport#1039 S1, #1057)

Getaggt als `script-runner-v0.1.4`. Taktgeber für Zeitpläne (S1) und
Sicherheitsfixes der Basisschicht. Funde ohne Fix: Ausnahme bis 2027-01-08 in
JanuaPort/januaport#1059. Der Kuratierungs-Nachweis:

| Feld | Wert |
|---|---|
| Image | `ghcr.io/januaport/script-runner` |
| Tag | `script-runner-v0.1.4` (Image-Tag `0.1.4`) |
| Digest (OCI-Index) | `sha256:c7bdbaa80b806dd300f1f5ae2fc578e674914b1dec799718c02fab0eeeb3b7ba` |
| Plattform | `linux/amd64` (`sha256:2d9d19e1d6f5bceeab556e98079823c651b70251ee14caa8003cc8d12a81b44f`), sonst keine (der zweite Eintrag ist die Attestation) |
| Art | `service`, eigener Build (Plugins-Repo) |
| Geprüft mit | Sonden der Release-Strecke; der Zeitplan-Lauf gegen JanuaPort folgt mit dem Live-Durchstich JanuaPort/januaport#1039 |
| Benötigter Commit von Compose und Profil | `d3dca37` (`docker-compose.yml`, `seccomp/jnpt-sandbox-seccomp.json`); die Compose mit internem Netz aus JanuaPort/januaport#1069 setzt das Netz `jnpt_plugins` der Kern-Compose voraus und ist ein eigener Schritt |
| Lauf der Sonden | Release-Lauf `38641095822`: hat genau diesen Digest geprüft (SEC C1) |

**Digest** zweimal gemessen am 2026-10-14: Log des Release-Laufs
(`exporting manifest list sha256:c7bdbaa8…`, `digest.txt` im Trivy-Artefakt)
und `docker buildx imagetools inspect`. Beide stimmen überein.

**Sonden:** grün im Lauf `38641095822` gegen genau diesen Digest.

**Scan:** Trivy 0.74.0 (Artefakt `trivy-script-runner-v0.1.4`, erstellt
2026-10-14T21:34Z), Scanner `vuln`: **1 critical · 59 high** · 94 medium ·
138 low · 6 unknown. **Kein Fund mit Fix-Fassung, gleich welcher Schwere**
(0.1.3: 3 UNKNOWN mit Fix; vor #1057 lokal 7 mit Fix, darunter 2 HIGH). Alle
60 hohen Funde (1 CRITICAL, openssh-client CVE-2026-60002, und 59 HIGH) sind
ohne Fix und laufen als Ausnahme in JanuaPort/januaport#1059.

- **Taktgeber für Zeitpläne (JanuaPort/januaport#1039, Paket S1):** Der
  vermittler liest `schedules.json` aus dem Plugin-Ordner (streng, fail-closed,
  höchstens 64 KiB und 20 Zeitpläne) und meldet fällige Termine per
  `POST /api/plugin/script-runner/schedules/{name}/run` an das Gateway — ohne
  Body, mit dem runtime-key, Frist 35 s, ohne Umleitung, ohne Wiederholung. Den Lauf startet
  das Gateway über Weg A; der Taktgeber hält keinen Slot (V1). Kein Nachholen;
  verpasste und in die Sommerzeit-Lücke fallende Termine zählen in `missed`,
  belegte in `skipped_busy`. Zustand je Zeitplan in `tools/list` unter
  `_meta["januaport.ai/runner"].schedules` (Golden
  `contract/golden/tools_list_schedules.json`); ohne Zeitpläne bleibt
  `tools/list` unverändert. Neue Abhängigkeit `github.com/robfig/cron/v3`
  v3.0.1 (MIT, derselbe Parser wie im Gateway), `time/tzdata` eingebettet.
  Das Golden `script-runner-schedules.golden.json` ist unverändert aus dem
  Gateway-Repo übernommen.
- **Sicherheitsfixes der Basisschicht (JanuaPort/januaport#1057, SEC):** Der
  Build ruft nach `apt-get update` jetzt `apt-get upgrade` auf. Der gepinnte
  Digest von `python:3.12-slim` fror bisher alle Pakete der Basisschicht ein.
  Ein Neubau nach einem Trivy-Fund mit Fix holte deshalb weder liblzma5
  (DSA-6549-1, 5.8.1-1+deb13u2) noch libcurl3t64-gnutls (8.14.1-2+deb13u7,
  darunter zwei HIGH). Die Regel „Fund mit Fix erzwingt Neubau“ greift damit
  erst jetzt auch für diese Pakete.
- `golang.org/x/sys` v0.41.0 → v0.49.0 (CVE-2026-39824 / GO-2026-5024).
  Hygiene: Die Lücke liegt nur in `x/sys/windows`, der Läufer baut nur für
  Linux.
- **Beleg (SEC, 14.10.2026, Trivy 0.74.0, lokal gebaut):** vorher 7 Funde mit
  Fix (2 HIGH, 1 MEDIUM, 4 UNKNOWN), nachher **keiner**. HIGH 61 → 59,
  CRITICAL bleibt 1 (openssh CVE-2026-60002 ohne Fix, Ausnahme #1059). Tests
  und `go vet` grün in `golang:1.26.9`.

## 0.1.3 — 2026-10-11 (JanuaPort/januaport#1085; enthält 0.1.2)

Getaggt als `script-runner-v0.1.3`. Enthält alles aus 0.1.2 (#1083 P2, nur
Vorfahren des Standard-Zweigs) und die folgenden Korrekturen. Funde ohne Fix wie
in 0.1.2: Ausnahme bis 2027-01-08 in JanuaPort/januaport#1059. Der
Kuratierungs-Nachweis unten.

| Feld | Wert |
|---|---|
| Image | `ghcr.io/januaport/script-runner` |
| Tag | `script-runner-v0.1.3` (Image-Tag `0.1.3`) |
| Digest (OCI-Index) | `sha256:47e2f63730edb02e4037cddf91d8f632d8822d39cae40ab24edf2e47b84eb568` |
| Plattform | `linux/amd64` (`sha256:113d393ca308e877528635453756b65d2aecf9cc09516a7eaf76a3508bb87513`), sonst keine (der zweite Eintrag ist die Attestation) |
| Art | `service`, eigener Build (Plugins-Repo) |
| Geprüft mit | Sonden der Release-Strecke; der Lauf gegen JanuaPort folgt mit der Belegfahrt JanuaPort/januaport#928 P4c |
| Benötigter Commit von Compose und Profil | `98752f7` (`docker-compose.yml`, `seccomp/jnpt-sandbox-seccomp.json`) |
| Lauf der Sonden | Release-Lauf `38124655517`: hat genau diesen Digest geprüft (SEC C1) |

**Digest** zweimal gemessen am 2026-10-11: Zusammenfassung des Release-Laufs
(`Tag 0.1.3 → sha256:47e2f637…`) und `docker buildx imagetools inspect`. Beide
stimmen überein. SBOM (136 Pakete) und Provenance hängen als Attestation am
Index; die Provenance nennt den Release-Lauf `38124655517`.

**Sonden:** grün im Lauf `38124655517` gegen genau diesen Digest.

**Scan:** Trivy 0.74.0 (Artefakt `trivy-script-runner-v0.1.3`, erstellt
2026-10-11T08:25Z), Scanner `vuln`: **1 critical · 60 high** · 94 medium ·
117 low · 15 unknown. **Kein HIGH oder CRITICAL mit Fix-Fassung.** Alle 61 hohen
Funde sind ohne Fix und dieselben wie in 0.1.1 (openssh-client CVE-2026-60002
CRITICAL, dazu util-linux, libcurl/gnutls, libgnutls, libexpat, ncurses, Perl,
libsystemd/libudev, libacl). Sie laufen als Ausnahme bis 2027-01-08 in
JanuaPort/januaport#1059, eingeordnet von SEC: fremde Eingabe nur vom Git-Server
über ssh und aus dem Pin-Python in der Sandbox ohne Netz; curl/gnutls nicht
erreichbar. Mit Fix nur 3 UNKNOWN.

- LEAD-Nachtrag zu #1085: Der vermittler veröffentlicht `repo_url` atomar
  vor neuen `wanted/<sha>`-Dateien und legt bei einem Schreib- oder
  Rename-Fehler der URL keine neuen Wunschdateien an. Zuvor konnte der
  Abholer bereits die Wunschdatei sehen, aber noch eine leere URL lesen:
  `TestPRCommitNeverExecutable` meldete dann `fetch_failed`, ohne dass Git
  oder die Vorfahren-Prüfung überhaupt liefen. `Once` war bereits synchron;
  kein Rennen in der Zustandsabfrage und kein negativer Git-Befund, der
  falsch zugeordnet wurde. Die Zusicherung `not_on_default_branch` bleibt
  unverändert, ohne zusätzliche Sleeps oder Zusicherungs-Retries.
- Tests zuerst im eigenen Commit `ec7653b`: Zwei deterministische Unterfälle
  verhindern die Veröffentlichung einer Wunschdatei bei Schreib- bzw.
  Rename-Fehlern der URL und prüfen die Veröffentlichung nach Behebung des
  Dateifehlers. Die vollständige Basismessung fand außerdem ein bestehendes
  Test-Rennen in `TestV1SecondConnectionLosesRunAndNextJobGoesToFreshInstance`:
  Die nächste Fake-Sandbox verband vor gelesenem EOF und Slot-Freigabe der
  vorigen. Auch dieser Test nutzt jetzt die vorhandene EOF-Testhilfe; V1
  und alle Zusicherungen bleiben unverändert.
- Die Basismessung mit `-cpu 1` zeigte dasselbe Test-Rennen nach übergroßen
  Rahmen in `TestOversizedFrameLimitAndMediatorSurvives`. Test-Commit
  `3c3c538` wartet auch dort auf EOF und Slot-Freigabe, bevor die nächste
  Fake-Sandbox verbindet. Die Rahmen-Limit- und Folgejob-Zusicherungen
  bleiben unverändert.
- Abholer (#1085): `-c gc.auto=0` und `-c maintenance.auto=false` gehören
  zur Härtung jedes Git-Aufrufs, einschließlich `init`, `config`, `fetch`,
  `ls-remote`, `cat-file` und `merge-base`. Damit startet Fetch keine
  automatische GC oder abgelöste Maintenance, die nach dem Git-Aufruf noch
  in `objects/` arbeitet und mit dem Entfernen des Stores konkurriert.
  Keine eigene GC und keine Store-Pflege im Abholer.
- Tests zuerst im eigenen Commit `5194fb0`: Literal-Prüfung beider Optionen
  vor jedem Unterbefehl über den Git-Runner-Fake, für SHA-1 und SHA-256;
  die vorhandene Härtungsprüfung zieht beide Optionen nach. Der Ausfall-Test
  und sein Cleanup bleiben unverändert.

**Verifikation des LEAD-Nachtrags am 11.10.2026:** Container
`jnpt1085-followup`, Image `golang:1.25.13`, Git 2.47.3, über
`GOTOOLCHAIN=auto` Go 1.26.9. Der unveränderte PR-Test wurde mit
`GOMAXPROCS=1` und per strace um 100 ms verzögertem `renameat` gezielt rot:
Der Trace zeigt die sichtbare Wunschdatei, das Lesen von `repo_url = "\n"`
vor deren Umbenennung und das Schreiben von `fetch_failed`, ohne Git-Aufruf.
Mit dem Fix ist dieselbe Probe grün und erreicht die Vorfahren-Prüfung.
Die neue Veröffentlichungsregression war vor dem Fix in beiden Unterfällen
rot und ist danach grün.

Vollständige Paketserien, jeweils `go test -count=50 -v` auf
`./internal/mediator/ ./internal/fetcher/`, zusätzlich mit `-cpu 1`.
Zahlen sind fehlgeschlagene **Testausführungen**; jeder Tabellenlauf enthält
50 Wiederholungen je Paket:

| Stand | CPU | Mediator-Fehler | Fetcher-Fehler |
|---|---|---|---|
| Vorher (`9d75462`) | Standard | 1 (V1-Test) | 0 |
| Vorher (`9d75462`) | `-cpu 1` | 89 (48 V1, 41 übergroße Rahmen) | 0 |
| Zwischenstand: Produkt-Fix und V1-Testhilfe | Standard | 0 | 0 |
| Zwischenstand: Produkt-Fix und V1-Testhilfe | `-cpu 1` | 34 (übergroße Rahmen) | 0 |
| Endstand: beide Sandbox-Übergänge synchronisiert | Standard | 0 | 0 |
| Endstand: beide Sandbox-Übergänge synchronisiert | `-cpu 1` | 0 | 0 |

Die 89 Fehler der CPU-Basismessung verteilen sich auf 49 von 50
Paketwiederholungen. Der PR-Test selbst war in allen regulären Serien grün;
sein Fehler wurde gezielt durch die verzögerte Veröffentlichung reproduziert.
Fetcher: in allen sechs Serien grün. Keine Cleanup-Fehler und keine
`--- SKIP`-Zeile. Zusätzlich jeweils 50 grüne Wiederholungen mit `-race`
und `-cpu 1` für PR-/Veröffentlichungsregression, beide V1-Tests und den
Test für übergroße Rahmen. `go build ./...`, `go test -count=1 -v ./...`
(alle zehn internen Pakete grün, Root ohne Tests) und `golangci-lint run`
v2.9.0 erfolgreich, 0 Befunde. `CLAUDE.md` und Codekommentar beschreiben
die Veröffentlichungsreihenfolge; die V1-Testhilfe ist ebenfalls nachgezogen.

**Lokale Verifikation am 11.10.2026 (#1085):** Container
`jnpt1085-validation`, Image `golang:1.25.13`, Git 2.47.3;
`GOTOOLCHAIN=auto` lädt wegen `go.mod` Go 1.26.9. Die Literal-Tests waren
vor dem Fix rot und danach grün. Stabilitätsprüfung jeweils dreimal
`go test -count=30 -run TestOutagePreservesOnlyApprovalsForSameRepository ./internal/fetcher/`:
vorher **90 Wiederholungen, 0 Fehlschläge**, nachher **90 Wiederholungen,
0 Fehlschläge** (je 810 Unterfälle). Der gemeldete Cleanup-Fehler wurde
lokal nicht reproduziert.

Ein zusätzlicher Prozess-Trace je Zustand bestätigt den verdächtigten
Mechanismus, nicht die konkrete CI-Fehlerursache: Vorher startet Fetch
`git maintenance run --auto --no-quiet --detach`, mit `setsid()` und
15 Anlageversuchen für `store.git/objects/maintenance.lock`; nachher gibt
es im Store weder Maintenance-Prozesse noch solche Zugriffe. Fünf weitere
automatische Maintenance-Aufrufe betreffen in beiden Traces nur den Aufbau
des Test-Remotes, vor den Ausfall-Unterfällen. Der lokale Runner-Adapter
startet echtes Git und reicht die Härtung weiter. Beide Trace-Läufe sind grün.

`go build ./...`, `go test -count=1 -v ./...` (alle zehn internen Pakete
grün, Root ohne Tests) und `golangci-lint run` v2.9.0 (mit Go 1.26.9 gebaut,
0 Befunde) erfolgreich. Keine `--- SKIP`-Zeile im ausführlichen Testlog.

## 0.1.2 — 2026-10-11 (JanuaPort/januaport#1083, P2) — getaggt, nicht veröffentlicht

Getaggt als `script-runner-v0.1.2`, **nicht veröffentlicht:** Die Unit-Tests der
Release-Strecke (Lauf `38121476095`, zwei Versuche) scheiterten am Test-Rennen
nach `git gc --auto`/Maintenance (#1085). Es wurde kein Image gebaut. Ausgeliefert
wird 0.1.3. Funde ohne Fix (openssh-client CVE-2026-60002
CRITICAL und 60 HIGH aus 0.1.1, unverändert) laufen als Ausnahme bis 2027-01-08 in
JanuaPort/januaport#1059. Der Kuratierungs-Nachweis mit dem Digest folgt nach dem
Release-Lauf.

- SEC L3 / LEAD Delta v2: Jeder Pin muss Vorfahr (oder gleich) dem Kopf des
  Remote-Standard-Zweigs sein, unabhängig davon, wer pinnt. Jeder Durchlauf
  mit gewünschten Commits löst `git ls-remote --symref origin HEAD` frisch
  auf, holt exakt den angekündigten Kopf-Hash und prüft
  `git merge-base --is-ancestor <sha> <kopf>`. Kein konfigurierbarer Zweig.
- Commits nur auf Seitenzweigen oder unter `refs/pull/*` werden als
  `invalid/not_on_default_branch` abgewiesen. Das ist der einzige zusätzliche
  Grund-Wert im Gateway-Vertrag; Auflösungs-, Fetch- und Git-Prüffehler
  verwenden bei noch nicht freigegebenen Commits `fetch_failed` und erteilen
  keine neue Freigabe.
- LEAD-Nachbesserung: Ausfall widerruft nicht, nur ein negativer
  Vorfahren-Befund widerruft. Bereits geprüfte Freigaben für dieselbe
  `repo_url` bleiben bei Auflösungs-/Fetch-Fehlern, Timeout, zu großer
  HEAD-Antwort und anderen Git-Prüffehlern erhalten; eine Warnung enthält
  weder URL noch Git-Fehlertext. Nur Exit 1 der Vorfahren-Prüfung widerruft
  mit `not_on_default_branch`. Ein Repository-Wechsel leert weiter alle
  Freigaben. Regressionstests zuerst im eigenen Commit `f2ece44`, einschließlich
  Fetcher → Mediator → weiterhin `ready` und angebotener Werkzeuge.
- Gespeicherte und früher gültige Commits werden erneut geprüft: Entfernt
  ein Force-Push einen Pin aus der Abstammung, verliert er seine Freigabe.
  Der Mediator prüft die an die Repository-URL gebundene Freigabe vor seinem
  Cache und vor jeder Codeübergabe. Alte Store-Objekte ohne Freigabe bleiben
  zunächst `pending`; laufende Skripte werden nicht abgebrochen.
- Dieselbe Härtung für jeden Git-Aufruf; `ls-remote` nutzt dieselbe temporäre
  Deploy-Key-Kopie und denselben TOFU-Weg wie `fetch`. Keine Tags, kein
  `FETCH_HEAD`, keine zusätzlichen Protokolle oder Netz-Ziele.
- Tests zuerst in eigenem Commit: echte lokale Bare-Remotes für Standard- und
  Seitenzweig, PR-Referenz, `trunk`, Auflösungsfehler und Force-Push. Die
  Regression `TestPRCommitNeverExecutable` prüft Fetcher → Mediator →
  verweigerten Aufruf ohne Sandbox-Job.
- Docker-Sonden erlauben `P2_PREFIX` für getrennte Container- und Netznamen;
  auch kurzlebige Hilfscontainer werden benannt. Bestehende, zuvor ignorierte
  Rückgabewerte beim Schließen und Schreiben in Hashes sind ausdrücklich
  ignoriert, damit `golangci-lint` 2.9.0 ohne Befunde läuft.
- Keine Veröffentlichung, kein Tag und kein Release- oder Scan-Nachweis
  aus der Release-Strecke.

**Lokale Erstverifikation am 11.10.2026:** Test-Commit `6aaa745` vor der Umsetzung.
`go build ./...`, `go test -count=1 -v ./...` (alle zehn internen Pakete grün,
Root ohne Tests) und `golangci-lint run` 2.9.0 (0 Befunde) im Container
`golang:1.25.13`. Wegen `go.mod` lädt `GOTOOLCHAIN=auto` darin Go 1.26.9;
der Linter wurde mit dieser Toolchain gebaut. 20 Docker-Sonden unter runc
gegen das endgültige lokale Image grün (219,485 s, Exit 0). Keine
`--- SKIP`-Zeile in Unit-Tests oder Sonden.

**Lokaler Erstscan:** Trivy 0.74.0, Image als Datei, Scanner `vuln`:
1 CRITICAL, 60 HIGH, 94 MEDIUM, 117 LOW, 15 UNKNOWN.
Kein HIGH/CRITICAL mit verfügbarer Fix-Fassung. Der CRITICAL-Fund bleibt
`openssh-client` / CVE-2026-60002 ohne Fix (siehe 0.1.1). Config-Digest des
gescannten und durch die Sonden geprüften Images:
`sha256:524836bb1baa5ca1d0483eba0b24b85d0f1dc17e24c063997c728b28683cb089`.
Dies ist kein Kuratierungs-Nachweis aus der Release-Strecke.

**LEAD-Nachbesserung, erneut verifiziert am 11.10.2026:** Test-Commit
`f2ece44` enthält ausschließlich Tests; die Ausfall-Regressionen scheiterten
vor der Umsetzung gezielt. Danach `go build ./...` und
`go test -count=1 -v ./...` im Container `golang:1.25.13` mit
`GOTOOLCHAIN=auto` (effektiv Go 1.26.9): alle zehn internen Pakete grün,
Root ohne Tests. `golangci-lint run` 2.9.0: 0 Befunde;
`seccomp/derive.py --check`: unverändert. Alle 20 Docker-Sonden unter runc
gegen das neu gebaute Image grün (218,700 s, Exit 0). Keine
`--- SKIP`-Zeile in Unit-Tests oder Sonden. Trivy 0.74.0, Scanner `vuln`,
erneut gegen dieses Image: unverändert 1 CRITICAL, 60 HIGH, 94 MEDIUM,
117 LOW, 15 UNKNOWN; kein HIGH/CRITICAL mit verfügbarer Fix-Fassung.
Docker-Image-ID des geprüften und gescannten Images:
`sha256:4691ea16a8eb5cee49887c1b157b1ef3ba486f7a6f4e99934582c819d9369dcc`.
Fassung bleibt 0.1.2, unveröffentlicht; kein Nachweis aus der Release-Strecke.

## Nur Test nach 0.1.1, ohne eigenes Release (JanuaPort/januaport#1056)

- Nur Test: `TestNewConnectionOnlyAfterEOF` wartet nach dem Schließen des
  Fake-Wächters auf das vom Mediator gelesene EOF und die Freigabe des Slots.
  Die V1-Prüfung vor EOF bleibt erhalten; kein Produktcode geändert, kein
  Release (JanuaPort/januaport#1056).

## 0.1.1 — Neubau gegen Trivy-Funde mit Fix (JanuaPort/januaport#928)

Getaggt als `script-runner-v0.1.1` (Release-Lauf `38051307623`).
**0.1.0 wird nicht katalogisiert:** Der Trivy-Bericht des Release-Laufs
`38044703224` (Digest `sha256:ee9ae6b07472aeb42271c75f4e0703a251e4dcb272b4c2602ed38e94c7c1c0ef`)
zeigt hohe Funde, für die es einen Fix gibt; nach der K-Scan-Regel wird ein
solcher Fund durch einen Neubau behoben. Kein Verhalten des Läufers ist
geändert.

- Go-Builder `golang:1.25.13` → `golang:1.26.9`
  (`sha256:d7722066f0b60ceccb6c0643cbed1f5f9e15506ac237146c95333504d7805d89`),
  `go.mod` `go 1.26.9` (die CI testet mit `go-version-file: go.mod`), der
  Test-Läufer der Sonden ebenso. Behebt in der Go-Standardbibliothek
  CVE-2026-78667, CVE-2026-78669 und CVE-2026-97031 (HIGH).
- Laufzeit-Basis `python:3.12-slim` neu gepinnt auf
  `sha256:a6e34c598f2467ed0e9a8d349809fcd8b5c603269512df273a0bb1784edc11b1`
  (Debian 13.7, Python 3.12.15). Darin `openssl`/`libssl3t64`
  `3.5.7-1~deb13u3` (CVE-2026-84782) und `libpcre2-8-0` `10.46-1~deb13u3`
  (CVE-2026-103111). Ein eigenes `apt-get install --only-upgrade` ist dafür
  nicht nötig.
- Wächter `TestDockerfile`: Go-Builder 1.26 per Digest, und `go.mod` nennt
  dieselbe Go-Fassung wie der Builder.
- **openssh-client CVE-2026-60002 (CRITICAL):** Use-after-free im Client,
  nicht blockierend, kein Fix verfügbar; Neubau, sobald Debian 13 openssh
  ≥ 10.4p1 liefert. Der abholer nutzt den Client für git über ssh.

**Scan** mit Trivy 0.74.0 (wie die Release-Strecke: Image als Datei,
`--scanners vuln`), Zählung HIGH und CRITICAL:

| | mit Fix | ohne Fix |
|---|---|---|
| 0.1.0, Release-Lauf `38044703224`, Datenbank vom 10.10.2026 | 7 HIGH (stdlib ×3, openssl ×3 Pakete, libpcre2) | 1 CRITICAL, 60 HIGH |
| 0.1.1, lokaler Build `linux/amd64`, Datenbank vom 10.10.2026 | **0** | 1 CRITICAL, 60 HIGH |

Die 61 Funde ohne Fix (Status `affected`, bei Perl `fix_deferred`) liegen in
Debian-Paketen der Basis: openssh-client (CVE-2026-60002 CRITICAL,
CVE-2026-59999, CVE-2026-60000), util-linux und seine Bibliotheken, libcurl
(gnutls), libgnutls, libexpat, ncurses, Perl, libsystemd/libudev, libacl. Die
Funde mit Fix in MEDIUM und LOW sind ebenfalls weg (0.1.0: 35 bzw. 7, 0.1.1:
0). Offen mit Fix, nur UNKNOWN: `liblzma5` `5.8.1-1+deb13u2` (DSA-6549-1) und
`golang.org/x/sys` v0.44.0 (CVE-2026-39824). Der Kuratierungs-Nachweis folgt
nach dem Release-Lauf mit dem Digest aus der Strecke.

## 0.1.0 — 2026-10-10 (getaggt, nicht katalogisiert; JanuaPort/januaport#928, P2)

Tag `script-runner-v0.1.0` → `db298d2`, Release-Lauf `38044703224`. Abgelöst durch 0.1.1 (Funde mit Fix, siehe oben).

### Auslieferung (P4a)

- Release-Strecke `.github/workflows/script-runner-release.yml`, Auslöser nur
  der Tag `script-runner-vX.Y.Z`: Unit-Tests → Seccomp-Herleitung → ein Build
  (nur `linux/amd64`, SBOM, Provenance `mode=max`) per Digest nach
  `ghcr.io/januaport/script-runner` ohne Tag → Docker-Sonden gegen genau
  diesen Digest mit Vergleich des Config-Digests (SEC C1) → Tag `X.Y.Z`.
  Trivy-Bericht als Artefakt im Job ohne Schreibrecht (SEC C2), kein Gate.
- CI: `script-runner.yml` und `kartei-rpa.yml` laufen nur noch bei Push auf
  `main` und per Hand; `secret-scan.yml` unverändert auf jedem Push.
- Deploy-Beispiele: Deskriptor mit echtem `IMAGE_REPO` und dem runtime-key in
  `REQUIRE` (ohne Schlüssel `unprovisioned` statt `failed`); neues
  `deploy/.env.example` mit `runc` als Standard.
- runsc-Sonde mit gepinnter gVisor-Fassung `release-20260928.0` und SHA-512
  statt `release/latest`.
- E1 belegt: Compose löst den relativen Profilpfad gegen das
  Projektverzeichnis auf, auch mit cwd `/` (Sonde
  `TestE1SeccompProfileFromProjectDirWithCwdRoot`). Die Compose bleibt
  unverändert.

### Nachzug Vertrag Fassung 4 (englische Namen)

- Der Manifest-Prüfer sperrt das Werkzeugpräfix `script_` statt `skript_`: Das
  Gateway registriert je Pin `script_<name>`, ein Skript darf also kein
  Skript-Werkzeug deklarieren (keine Rekursion). `skript_` hat keine
  Sonderrolle mehr. Golden-Dateien unverändert.

### Nachzug SEC-Zweitprüfung (Bedingung vor dem Merge)

- Compose-Wächter deny by default: `KnownFields(true)`, dazu ausdrückliche
  Verbote für `volumes_from`, `userns_mode`/`pid`/`uts`/`cgroup` `host`,
  `ipc` `host`/`shareable`, `network_mode` `host`/`container:*`, `devices`,
  `sysctls`, `extra_hosts`; Negativproben je Dienst.
- `ipc`, `pid` und `network_mode`: neben `container:<name>` ist auch
  `service:<name>` verboten (SEC-Abnahme, Nachzug).
- Symlink im Pin-Pfad meldet `symlink` — mit Tabellenfällen (SHA-1/SHA-256,
  jeder Pfadbestandteil, auch im vermittler) belegt; der bisherige Code tat
  das bereits.

### Nachzug Vertrag Fassung 3 (B1, B2, SEC-Auflagen)

- **B2:** Der Wächter prüft vor `hello` selbst, dass unser Seccomp-Profil
  greift (`process_vm_readv` → genau EPERM), und meldet `"profile": "jnpt"`
  oder `"other"`. Der vermittler bedient nur bei Seccomp-Modus 2 **und**
  `profile: "jnpt"`; sonst `isolation: invalid`. Sonden: Docker-Standardprofil
  und `unconfined` → invalid, unser Profil → bedient.
- **B1:** gVisor als eigener Runtime-Eintrag `runsc-jnpt` mit
  `runtimeArgs: ["--oci-seccomp", "--host-uds=open"]`
  (`deploy/daemon.json.example`, `JNPT_SCRIPT_RUNNER_RUNTIME=runsc-jnpt`);
  Rezept der runsc-Sonde in `probe/run-runsc.sh`.
- `/healthz`: nur `ok`/`not ready`, jeder andere Pfad 404; Grenze in
  `CLAUDE.md` benannt. Ratifizierte Auslegungen in `CLAUDE.md` markiert.

### Erste Fassung

Erste Fassung, **im Bau**; noch nicht auf einer Anlage gelaufen, kein
Katalogeintrag, kein veröffentlichtes Image.

- Ein Go-Binary mit drei Unterbefehlen: `mediator` (vermittler), `guard`
  (Wächter, PID 1 der Sandbox), `fetcher` (abholer); MCP über
  `github.com/modelcontextprotocol/go-sdk` v1.7.0.
- Draht-Vertrag Fassung 1 + 2 + Nachtrag 2a; Golden-JSON für `tools/list`, die
  Aufruf-Header und jede Ende-Klasse unter `contract/golden/`.
- Image: `python:3.12-slim` und `golang:1.25.13` per Digest gepinnt, git
  (Debian 2.47.3, sha1dc) und ssh-Client, pip und ensurepip entfernt.
- Seccomp-Profil `seccomp/jnpt-sandbox-seccomp.json`, hergeleitet aus dem
  Docker-Standardprofil (moby/profiles @ a2187282) per `seccomp/derive.py`.
- Compose `jnpt-plugin-script-runner` nach Vertrag §6, Beispiele für
  tmpfiles-Zeilen und Host-Deskriptor unter `deploy/`.
- Verifiziert: Unit-Tests und 15 Docker-Sonden unter runc (Docker Desktop,
  WSL2-Kernel 5.15) und unter runsc (gVisor release latest, Wegwerf-dind).

## Vorlage: Kuratierungs-Nachweis je Release

Je Release ein Abschnitt nach diesem Muster, **über** „Unveröffentlicht“,
geschrieben nach dem grünen Lauf der Release-Strecke und vor dem
Katalogeintrag. Ein Katalogeintrag ist keine Aussage, dass das Image frei von
bekannten Schwachstellen ist; er sagt, dass genau dieser Digest gemessen,
gescannt und mit der genannten JanuaPort-Fassung gelaufen ist.

```markdown
## vX.Y.Z — <Datum>

| Feld | Wert |
|---|---|
| Image | `ghcr.io/januaport/script-runner` |
| Tag | `script-runner-vX.Y.Z` (Image-Tag `X.Y.Z`) |
| Digest (OCI-Index) | `sha256:…` |
| Plattform | `linux/amd64` (`sha256:…`), sonst keine |
| Art | `service`, eigener Build (Plugins-Repo) |
| Geprüft mit | JanuaPort x.y |
| Benötigter Commit von Compose und Profil | `<Commit im Plugins-Repo>` (`docker-compose.yml`, `seccomp/jnpt-sandbox-seccomp.json`) |
| Lauf der Sonden | Release-Lauf `<Lauf-ID>` — hat genau diesen Digest geprüft (SEC C1) |

**Digest** zweimal gemessen am <Datum>: Zusammenfassung des Release-Laufs und
`docker buildx imagetools inspect`. Beide stimmen überein. SBOM und Provenance
hängen als Attestation am Index.

**Sonden:** grün im Lauf `<Lauf-ID>` gegen genau diesen Digest, darunter R12
(kein pip), sha1dc und B2 (fail-closed). Der Config-Digest des geprüften
Images ist gleich dem der amd64-Plattform des gepushten.

**Scan:** Trivy <Fassung> (Artefakt des Laufs), Datenbank vom <Datum>, Scanner
`vuln`: **<n> critical · <n> high** · <n> medium · <n> low · <n> unknown.
Hohe Funde einzeln, je mit behobener Fassung oder Begründung.

**Inhalt:** git <Fassung> (Debian-Build mit sha1dc), ssh-Client, Perl (als
Abhängigkeit von git), Python <Fassung> ohne pip und ensurepip, das
Go-Binary `script-runner`.

**Isolation:** Unter runc begrenzen das Seccomp-Profil, AppArmor
`docker-default` und `cap_drop: ALL` die Sandbox. Unter runsc ist der
gVisor-Kern die Grenze. Den Sandbox-Prozess auf dem Host begrenzt runsc mit
seinem eigenen Seccomp-Filter. AppArmor wirkt nur unter runc.

**Grenzen v1:** etwa 30 Prozesse unter gVisor (runsc); aus bei PostgreSQL
(`JNPT_DB_DRIVER=postgres`), auch bei nur einer Gateway-Instanz; die Ausgabe
des Skripts selbst wird nicht pseudonymisiert; nur `linux/amd64`; ein Image,
das eine neue Compose oder ein neues Profil braucht, ist ein Wartungsschritt
mit Runbook-Eintrag, kein stilles Katalog-Update.

**Entscheid:** <katalogisiert / nicht katalogisiert>, weil <Begründung>.

Quelle: JanuaPort/januaport#928.
```
