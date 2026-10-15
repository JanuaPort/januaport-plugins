#!/bin/sh
# P0-Messsonde „Python-Bibliotheken im Skript-Läufer“ (JanuaPort/januaport#1041).
#
# Misst numpy und pandas als Wheels (cp312, manylinux x86_64) in einem
# Nur-Lese-Volume im echten Sandbox-Image mit den Grenzen der Compose: Größe
# installiert, Import ohne und mit vorab erzeugtem pyc, maximaler RSS,
# Threads und cgroup-PIDs mit und ohne OPENBLAS_NUM_THREADS=1, glibc des
# Images, Arbeitslast (10 000 Zeilen groupby, JSON-Ausgabe).
#
# Die Wheels werden zur Laufzeit per pip in einem Hilfscontainer geladen (nicht
# im Repo, nicht im Produkt) und mit Pythons zipfile entpackt — nur für die
# Messung. Das gehärtete Entpacken des Produkts ist internal/libs.
#
# Aufruf:           sh script-runner/probe/measure-libs.sh
# Eigener Präfix:   P0_PREFIX=lead1041p1-p0 sh script-runner/probe/measure-libs.sh
# Unter gVisor:     P0_RUNTIME=runsc-jnpt ... (nur wenn die Runtime lokal
#                   eingerichtet ist, siehe probe/run-runsc.sh; sonst meldet
#                   das Skript „runsc nicht gemessen“ und endet mit 0)
# Aufräumen:        Container laufen mit --rm; das Volume <präfix>-w bleibt als
#                   Cache der Wheels, `docker volume rm <präfix>-w` entfernt es.
set -eu
here=$(cd "$(dirname "$0")/.." && pwd)
src=$(cd "$here" && (pwd -W 2>/dev/null || pwd))
export MSYS_NO_PATHCONV=1
prefix=${P0_PREFIX:-p0-1041}
rt=${P0_RUNTIME:-runc}
reps=${P0_REPS:-5}
vol=$prefix-w

if ! docker info --format '{{json .Runtimes}}' | grep -q "\"$rt\""; then
  echo "Runtime $rt ist hier nicht eingerichtet: nicht gemessen"
  exit 0
fi
if [ -n "${P0_IMAGE:-}" ]; then
  img=$P0_IMAGE
else
  img=$prefix-script-runner:test
  docker build -q -t "$img" "$src" >/dev/null
fi

sandbox() { # sandbox <libs-unterordner> <modus> [docker-Optionen…]
  sub=$1; mode=$2; shift 2
  docker run --rm -i --name "$prefix-m-$$" --runtime "$rt" --read-only --cap-drop ALL \
    --security-opt no-new-privileges:true --security-opt "seccomp=$src/seccomp/jnpt-sandbox-seccomp.json" \
    --pids-limit 128 --memory 512m --memory-swap 512m --cpus 1 --network none --user 65533:65533 \
    --tmpfs /work:size=64m,noexec,nosuid,nodev,uid=65533,gid=65533,mode=0700 \
    --tmpfs /tmp:size=16m,noexec,nosuid,nodev,uid=65533,gid=65533,mode=0700 \
    -v "$vol:/libs:ro" "$@" --entrypoint python3 "$img" -I - "$mode" "/libs/$sub" <"$src/probe/p0/measure.py"
}
as_root() { # as_root <befehl…> im Image, Volume beschreibbar unter /w
  docker run --rm --name "$prefix-r-$$" --user 0 --entrypoint sh -v "$vol:/w" "$img" -c "$*"
}

docker volume create "$vol" >/dev/null
if ! as_root 'ls /w/wheels/*.whl >/dev/null 2>&1'; then
  echo "== Wheels laden (pip download, nur für die Messung)"
  docker run --rm --name "$prefix-dl-$$" --user 0 -v "$vol:/w" python:3.12-slim sh -c \
    'pip download -q --only-binary=:all: --implementation cp --python-version 3.12 \
       --platform manylinux2014_x86_64 --platform manylinux_2_17_x86_64 --platform manylinux_2_27_x86_64 --platform manylinux_2_28_x86_64 \
       -d /w/wheels pandas numpy && ls -l /w/wheels'
fi
echo "== Wheels"
as_root 'ls -l /w/wheels'

echo "== Entpacken (Messung) nach nopyc/ und pyc/"
as_root 'rm -rf /w/nopyc /w/pyc' 
docker run --rm -i --name "$prefix-u-$$" --user 0 -v "$vol:/w" --entrypoint python3 "$img" -I - /w/wheels /w/nopyc <"$src/probe/p0/prepare.py" >/dev/null
as_root 'cp -a /w/nopyc /w/pyc && chown -R 65532:65532 /w/pyc'
start=$(date +%s.%N 2>/dev/null || date +%s)
docker run --rm --name "$prefix-c-$$" --user 65532:65532 -v "$vol:/w" --entrypoint python3 "$img" -I -m compileall -q /w/pyc
end=$(date +%s.%N 2>/dev/null || date +%s)
echo "compileall (65532, inkl. Container-Start): $(echo "$end $start" | awk '{printf "%.1f s", $1-$2}')"
as_root 'chown -R root:root /w/nopyc /w/pyc && find /w/nopyc /w/pyc -type d -exec chmod 0555 {} + && find /w/nopyc /w/pyc -type f -exec chmod 0444 {} +'

echo "== Größe installiert"
as_root 'for d in nopyc pyc; do printf "%s: " $d; du -sb /w/$d | cut -f1 | tr "\n" " "; printf "Bytes, Dateien: "; find /w/$d -type f | wc -l; done; printf "Wheels: "; du -sb /w/wheels | cut -f1'
echo "== glibc des Images"
docker run --rm --name "$prefix-g-$$" --entrypoint sh "$img" -c 'ldd --version | head -1; python3 -V'
echo "== Laufzeit $rt, Grenzen wie die Compose (512 MiB, 128 PIDs, 1 CPU, read-only, ohne Netz)"

echo "-- Basis (nur Interpreter)"
sandbox nopyc baseline
for sub in nopyc pyc; do
  for mod in numpy pandas; do
    echo "-- Import $mod, $sub, $reps Läufe"
    i=0; while [ "$i" -lt "$reps" ]; do sandbox "$sub" "$mod"; i=$((i + 1)); done
  done
done
echo "-- Threads/PIDs: Arbeitslast ohne und mit OPENBLAS_NUM_THREADS=1 OMP_NUM_THREADS=1"
sandbox pyc work
sandbox pyc work -e OPENBLAS_NUM_THREADS=1 -e OMP_NUM_THREADS=1
echo "-- Arbeitslast (10 000 Zeilen groupby, JSON), ohne pyc, mit Thread-Deckel, 3 Läufe"
for i in 1 2 3; do sandbox nopyc work -e OPENBLAS_NUM_THREADS=1 -e OMP_NUM_THREADS=1; done
echo "-- Arbeitslast mit pyc und Thread-Deckel, 3 Läufe"
for i in 1 2 3; do sandbox pyc work -e OPENBLAS_NUM_THREADS=1 -e OMP_NUM_THREADS=1; done
