#!/bin/sh
# Docker-Sonden des Skript-Läufers (JanuaPort/januaport#928, P2).
#
# Baut das Image, einen SSH-Git-Anbieter und einen Test-Läufer und startet
# die Sonden in einem Go-Container am Netz p2-928-mcp (Stellvertreter für
# jnpt_default, Alias „jnpt“ = Stub-Gateway). Alle Container, Netze und
# Volumes tragen das Präfix p2-928- und werden am Ende entfernt.
#
# Aufruf aus beliebigem Ordner:  sh script-runner/probe/run.sh [go-test-Argumente]
set -eu
here=$(cd "$(dirname "$0")/.." && pwd)
# Git Bash auf Windows: Docker braucht den Windows-Pfad und keine
# Pfadumschreibung für /var/run/docker.sock.
src=$(cd "$here" && (pwd -W 2>/dev/null || pwd))
export MSYS_NO_PATHCONV=1

img=p2-928-script-runner:test
docker build -q -t "$img" "$src" >/dev/null
docker build -q -t p2-928-sshd:test "$src/probe/sshd" >/dev/null
docker build -q -t p2-928-probe-runner:test -f "$src/probe/runner.Dockerfile" "$src/probe" >/dev/null
docker network create p2-928-mcp >/dev/null 2>&1 || true

rc=0
docker run --rm --name p2-928-probe-runner --network p2-928-mcp --network-alias jnpt \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v "$src":/src -w /src \
  -v p2-928-gomod:/go/pkg/mod \
  -e P2_IMAGE="$img" -e P2_SSHD_IMAGE=p2-928-sshd:test \
  p2-928-probe-runner:test \
  go test -tags docker -count=1 -v -timeout 30m "$@" ./probe/ || rc=$?

docker network rm p2-928-mcp >/dev/null 2>&1 || true
exit "$rc"
