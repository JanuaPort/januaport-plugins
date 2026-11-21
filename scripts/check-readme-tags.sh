#!/bin/sh
# Local check only: sh scripts/check-readme-tags.sh (git + POSIX awk, no network).
set -eu

cd "$(CDPATH='' cd "$(dirname "$0")/.." && pwd)"
tags=$(git tag -l)

printf '%s\n' "$tags" | awk '
    FILENAME == "-" {
        if ($0 ~ /(-v|\/v)[0-9]+\.[0-9]+\.[0-9]+([-+].*)?$/) {
            module = $0
            sub(/(-v|\/v)[0-9]+\.[0-9]+\.[0-9]+([-+].*)?$/, "", module)
            tagged[module] = 1
        }
        next
    }
    tolower($0) ~ /noch nicht getaggt|not yet tagged|noch kein release/ {
        # In a table, only the folder cell identifies the module. Other cells
        # may reference an already released dependency of an untagged module.
        table = ($0 ~ /^[[:space:]]*\|/)
        split($0, cells, /[|]/)
        target = table ? cells[2] : $0
        for (module in tagged) {
            if (target ~ ("(^|[^[:alnum:]_-])" module "([^[:alnum:]_-]|$)")) {
                printf "%s:%d: %s has a tag; README says it has none.\n", FILENAME, FNR, module
                failed = 1
            }
        }
    }
    END { exit failed }
' - README.md README.de.md >&2
