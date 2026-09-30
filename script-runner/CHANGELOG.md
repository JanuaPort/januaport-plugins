# Changelog — script-runner

## Unveröffentlicht (JanuaPort/januaport#928, P2)

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
