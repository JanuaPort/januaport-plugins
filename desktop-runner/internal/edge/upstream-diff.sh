#!/bin/sh
# Zeigt, worin internal/edge vom Original abweicht (nur Lesen, ändert nichts im Repo).
# Aufruf im Modul desktop-runner, mit Netz für den ersten Abruf des Originals:
#   docker run --rm -v "$PWD:/w" -w /w golang:1.26.10 sh internal/edge/upstream-diff.sh
# Ausgabe: erst die Herkunftsprüfung, dann ein unified diff (Original links, Kopie rechts). Das Original wird
# vor dem Vergleich nur mit den mechanischen Schritten der Kopie behandelt: Importpfade, gofmt. Der Rest des
# Diffs ist die gewollte Abweichung (siehe doc.go und desktop-runner/CLAUDE.md).
set -eu

VERSION=v1.0.19
COMMIT=11c6e567b916645d5fe6df006681f988ac52c13d
MODULE=github.com/wailsapp/go-webview2
OURS=github.com/januaport/januaport-plugins/desktop-runner/internal/edge

here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

info=$(go mod download -json "$MODULE@$VERSION")
dir=$(printf '%s\n' "$info" | sed -n 's/.*"Dir": *"\([^"]*\)".*/\1/p')
hash=$(printf '%s\n' "$info" | sed -n 's/.*"Hash": *"\([0-9a-f]*\)".*/\1/p')
echo "Original: $MODULE $VERSION"
echo "Commit laut Go-Modulproxy: ${hash:-unbekannt}"
echo "Commit laut doc.go:        $COMMIT"
if [ -n "$hash" ] && [ "$hash" != "$COMMIT" ]; then
  echo "ACHTUNG: Commit weicht ab" >&2
  exit 2
fi
sumline=$(grep "^$MODULE $VERSION h1:" "$here/../../go.sum" || true)
echo "go.sum: ${sumline:-fehlt}"

mkdir -p "$work/edge/w32"
for f in "$dir"/pkg/edge/*.go; do
  case "$f" in *_test.go) ;; *) cp "$f" "$work/edge/" ;; esac
done
cp "$dir/internal/w32/w32.go" "$work/edge/w32/"
cp "$dir/LICENSE" "$work/edge/LICENSE"
chmod -R u+w "$work"
sed -i "s#$MODULE/internal/w32#$OURS/w32#" "$work"/edge/*.go
gofmt -w "$work/edge"

echo "--- Abweichungen (Original ... Kopie) ---"
diff -ruN -x doc.go -x upstream-diff.sh "$work/edge" "$here" || true
