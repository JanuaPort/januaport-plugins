# Changelog — script-runner

## Unveröffentlicht (JanuaPort/januaport#928, P2)

### Nachzug SEC-Zweitprüfung (Bedingung vor dem Merge)

- Compose-Wächter deny by default: `KnownFields(true)`, dazu ausdrückliche
  Verbote für `volumes_from`, `userns_mode`/`pid`/`uts`/`cgroup` `host`,
  `ipc` `host`/`shareable`, `network_mode` `host`/`container:*`, `devices`,
  `sysctls`, `extra_hosts`; Negativproben je Dienst.
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
