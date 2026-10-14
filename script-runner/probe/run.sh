#!/bin/sh
# Docker-Sonden des Skript-Läufers (JanuaPort/januaport#928, P2).
#
# Baut das Image, einen SSH-Git-Anbieter und einen Test-Läufer und startet
# die Sonden in einem Go-Container. Er hängt am internen Netz p2-928-plugins
# (--internal, Stellvertreter für jnpt_plugins, Alias „jnpt“ = Stub-Gateway,
# JanuaPort/januaport#1069) und für den Modul-Download am Netz p2-928-out.
# Alle Container, Netze und Volumes tragen P2_PREFIX (Standard p2-928);
# Teststacks werden am Ende entfernt.
#
# Aufruf aus beliebigem Ordner:  sh script-runner/probe/run.sh [go-test-Argumente]
# Mit P2_PREFIX=jnpt1083p2 werden Container und Netz für den Auftrag isoliert.
# Mit P2_IMAGE=<Referenz> laufen die Sonden gegen ein vorhandenes Image statt
# gegen einen eigenen Build — so prüft die Release-Strecke genau den
# gepushten Digest (SEC C1).
set -eu
here=$(cd "$(dirname "$0")/.." && pwd)
# Git Bash auf Windows: Docker braucht den Windows-Pfad und keine
# Pfadumschreibung für /var/run/docker.sock.
src=$(cd "$here" && (pwd -W 2>/dev/null || pwd))
export MSYS_NO_PATHCONV=1
prefix=${P2_PREFIX:-p2-928}

if [ -n "${P2_IMAGE:-}" ]; then
  img=$P2_IMAGE
else
  img="$prefix-script-runner:test"
  docker build -q -t "$img" "$src" >/dev/null
fi
docker build -q -t "$prefix-sshd:test" "$src/probe/sshd" >/dev/null
docker build -q -t "$prefix-probe-runner:test" -f "$src/probe/runner.Dockerfile" "$src/probe" >/dev/null
docker network create "$prefix-out" >/dev/null 2>&1 || true
docker network create --internal "$prefix-plugins" >/dev/null 2>&1 || true

# Anlegen, ans interne Netz hängen, dann starten: Der Alias „jnpt“ gilt nur im
# internen Netz, wie das Gateway in jnpt_plugins.
rc=0
docker create --rm --name "$prefix-probe-runner" --network "$prefix-out" \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v "$src":/src -w /src \
  -v "$prefix-gomod":/go/pkg/mod \
  -e P2_PREFIX="$prefix" -e P2_IMAGE="$img" -e P2_SSHD_IMAGE="$prefix-sshd:test" \
  "$prefix-probe-runner:test" \
  go test -tags docker -count=1 -v -timeout 30m "$@" ./probe/ >/dev/null
docker network connect --alias jnpt "$prefix-plugins" "$prefix-probe-runner"
docker start -a "$prefix-probe-runner" || rc=$?

docker network rm "$prefix-plugins" "$prefix-out" >/dev/null 2>&1 || true
exit "$rc"
