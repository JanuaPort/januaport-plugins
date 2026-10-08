# Changelog — script-runner

## Unveröffentlicht (JanuaPort/januaport#928, P2)

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
