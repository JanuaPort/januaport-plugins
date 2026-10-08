#!/bin/sh
# Docker-Sonden des Skript-Läufers unter gVisor (JanuaPort/januaport#928).
#
# Rezept ohne Eingriff in den Docker des Rechners: ein Wegwerf-dind
# (p2-928-dind), darin gVisor aus dem Release-Tarball einer gepinnten Fassung
# (dieselbe wie im Runbook) mit SHA-512-Prüfung gegen die Konstante unten und
# die Runtime aus deploy/daemon.json.example — Name runsc-jnpt,
# runtimeArgs --oci-seccomp und --host-uds=open. Ein eigener Name, damit die
# Argumente nur für den Skript-Läufer gelten und ein vorhandenes runsc
# unberührt bleibt. --host-uds=open (nicht all) erlaubt nur das Öffnen von
# Unix-Sockets, die in die Sandbox eingehängt sind; die Grenze ist die
# Mount-Liste. Am Ende wird der dind samt Datenvolume entfernt.
#
# Aufruf:  sh script-runner/probe/run-runsc.sh [go-test-Argumente]
set -eu
here=$(cd "$(dirname "$0")/.." && pwd)
src=$(cd "$here" && (pwd -W 2>/dev/null || pwd))
export MSYS_NO_PATHCONV=1
dind=p2-928-dind
img=p2-928-script-runner:test
# gVisor-Fassung (Tag release-<Fassung>) und SHA-512 von gvisor.tar.zstd.
# Ein Wechsel ist eine bewusste Änderung mit Changelog-Eintrag.
gvisor_release=20260928.0
gvisor_sha512=4ce35ca83aef7f96b06cde668e0b23aa98b05aa1829508e974196c2a1e02786c95f5bf79315fd7ddcfd88fe7a00f083ed8053e25eff7673d28d5256440caae8b

docker build -q -t "$img" "$src" >/dev/null
docker build -q -t p2-928-sshd:test "$src/probe/sshd" >/dev/null
docker build -q -t p2-928-probe-runner:test -f "$src/probe/runner.Dockerfile" "$src/probe" >/dev/null

docker rm -f -v "$dind" >/dev/null 2>&1 || true
docker run -d --privileged --name "$dind" docker:dind >/dev/null
trap 'docker rm -f -v "$dind" >/dev/null 2>&1 || true' EXIT
i=0
until docker exec "$dind" docker info >/dev/null 2>&1; do
  i=$((i + 1)); [ "$i" -lt 60 ] || { echo "dind startet nicht" >&2; exit 1; }
  sleep 1
done

docker exec -e gvisor_release="$gvisor_release" -e gvisor_sha512="$gvisor_sha512" "$dind" sh -ec '
  apk add -q zstd curl >/dev/null
  cd /tmp
  u=https://storage.googleapis.com/gvisor/releases/release/$gvisor_release/x86_64
  curl -sSfLO "$u/gvisor.tar.zstd"
  echo "$gvisor_sha512  gvisor.tar.zstd" | sha512sum -c -
  zstd -dq gvisor.tar.zstd -o gvisor.tar
  tar -xf gvisor.tar -C /usr/bin runsc gvisor-bin
  mkdir -p /etc/docker'
docker cp "$src/deploy/daemon.json.example" "$dind:/etc/docker/daemon.json"
docker exec "$dind" sh -ec 'kill -HUP "$(pidof dockerd)"; for i in $(seq 1 20); do docker info --format "{{json .Runtimes}}" | grep -q runsc-jnpt && exit 0; sleep 1; done; exit 1'

docker save "$img" p2-928-sshd:test p2-928-probe-runner:test | docker exec -i "$dind" docker load -q >/dev/null
docker exec "$dind" mkdir -p /src
tar -C "$here" -cf - . | docker exec -i "$dind" tar -C /src -xf -
docker exec "$dind" docker network create p2-928-mcp >/dev/null

docker exec "$dind" docker run --rm --name p2-928-probe-runner --network p2-928-mcp --network-alias jnpt \
  -v /var/run/docker.sock:/var/run/docker.sock -v /src:/src -w /src \
  -e P2_IMAGE="$img" -e P2_SSHD_IMAGE=p2-928-sshd:test -e JNPT_SCRIPT_RUNNER_RUNTIME=runsc-jnpt \
  p2-928-probe-runner:test go test -tags docker -count=1 -v -timeout 30m "$@" ./probe/
