#!/usr/bin/env bash
# Pruefstrecke fuer Image, Compose und Release-Workflow des EBICS-Abholers
# (JanuaPort/januaport#1005). Ergaenzt tests/run.php: Dort steht die Logik,
# hier steht, was das AUSGELIEFERTE Image zusagt.
#
# Aufruf (aus dem Repo-Wurzelverzeichnis):
#   bash ebics-abholer/tests/image.sh                 # nur die statischen Pruefungen
#   bash ebics-abholer/tests/image.sh <image-ref>     # zusaetzlich das gebaute Image
#
# ⚠️ Kein Fall hier spricht mit einer Bank oder liest Zugangsdaten. Die
# Laufzeit-Faelle starten den Container mit `--network none` und ohne Mounts.
#
# Exit 0 = alles gruen, 1 = mindestens ein Fall rot.

set -u

ORDNER="$(cd "$(dirname "$0")/.." && pwd)"
REPO="$(cd "$ORDNER/.." && pwd)"
WORKFLOW="$REPO/.github/workflows/ebics-abholer.yml"
IMAGE="${1:-}"

# Obergrenze fuer das entpackte Dateisystem des Images (`du` im Container).
# Bewusst nicht `docker image inspect .Size`: Das meldet je nach Image-Store
# entpackt (klassisch) oder komprimiert (containerd) — zwei verschiedene
# Zahlen fuer dasselbe Image. Vorher (php:8.5-cli, Debian, eine Stufe): 601 MB.
GROESSE_MAX_MB=200

GRUEN=0
ROT=0

pruefe() {
  local name="$1"
  shift
  local meldung
  if meldung="$("$@" 2>&1)"; then
    GRUEN=$((GRUEN + 1))
    echo "  ok   $name"
  else
    ROT=$((ROT + 1))
    echo "  ROT  $name"
    [ -n "$meldung" ] && printf '       %s\n' "$meldung"
  fi
}

# Code-Zeilen ohne Kommentare — Waechter pruefen Anweisungen, nicht Prosa.
code() { grep -v '^[[:space:]]*#' "$1"; }

# ── Statisch: Dockerfile ────────────────────────────────────────────────────

dockerfile_basis_per_digest() {
  local offen
  offen="$(code "$ORDNER/Dockerfile" | grep -E '^FROM ' | grep -v '@sha256:[0-9a-f]\{64\}')"
  [ -z "$offen" ] || { echo "ungepinnt: $offen"; return 1; }
  code "$ORDNER/Dockerfile" | grep -E '^COPY --from=[^ ]*[:/]' | grep -v '@sha256:' \
    && { echo 'COPY --from aus einem Image ohne Digest'; return 1; }
  return 0
}

dockerfile_mehrstufig() {
  local stufen
  stufen="$(code "$ORDNER/Dockerfile" | grep -cE '^FROM ')"
  [ "$stufen" -ge 2 ] || { echo "nur $stufen Stufe(n)"; return 1; }
}

# Composer darf nur in einer Bau-Stufe vorkommen, nie in der letzten.
dockerfile_composer_nur_im_bau() {
  local laufzeit
  laufzeit="$(code "$ORDNER/Dockerfile" | awk '/^FROM /{buf=""} {buf=buf"\n"$0} END{print buf}')"
  if printf '%s' "$laufzeit" | grep -qi 'composer'; then
    echo 'die Laufzeit-Stufe erwaehnt composer'
    return 1
  fi
}

dockerfile_nutzer_und_befehl() {
  code "$ORDNER/Dockerfile" | grep -qE '^USER 65532$' || { echo 'USER 65532 fehlt'; return 1; }
  code "$ORDNER/Dockerfile" | grep -qF 'CMD ["php", "/app/fetch.php"]' || { echo 'CMD fehlt'; return 1; }
}

# ── Statisch: Compose-Vorlage ───────────────────────────────────────────────

compose_bezieht_image_per_variable() {
  code "$ORDNER/docker-compose.ebics-abholer.yml" | grep -qE '^[[:space:]]+image: \$\{JNPT_EBICS_IMAGE:\?' \
    || { echo 'image: ${JNPT_EBICS_IMAGE:?…} fehlt'; return 1; }
  if code "$ORDNER/docker-compose.ebics-abholer.yml" | grep -qE '^[[:space:]]+build:'; then
    echo 'build: steht noch aktiv in der Compose'
    return 1
  fi
}

compose_projektname() {
  code "$ORDNER/docker-compose.ebics-abholer.yml" | grep -qE '^name: jnpt-plugin-ebics$' \
    || { echo 'name: jnpt-plugin-ebics fehlt'; return 1; }
}

compose_mounts_unveraendert() {
  local c
  c="$(code "$ORDNER/docker-compose.ebics-abholer.yml")"
  printf '%s' "$c" | grep -qF ':/ablage' || { echo '/ablage fehlt'; return 1; }
  printf '%s' "$c" | grep -qF ':/geheim:ro' || { echo '/geheim:ro fehlt'; return 1; }
}

env_vorlage_mit_platzhalter_pin() {
  local f="$ORDNER/.env.example"
  [ -f "$f" ] || { echo '.env.example fehlt'; return 1; }
  grep -qE '^JNPT_EBICS_IMAGE=ghcr\.io/januaport/ebics@sha256:' "$f" \
    || { echo 'JNPT_EBICS_IMAGE mit Digest-Pin fehlt'; return 1; }
}

# ── Statisch: Workflow ──────────────────────────────────────────────────────

workflow_vorhanden() { [ -f "$WORKFLOW" ] || { echo "fehlt: $WORKFLOW"; return 1; }; }

workflow_actions_per_sha() {
  [ -f "$WORKFLOW" ] || return 1
  local offen
  offen="$(code "$WORKFLOW" | grep -E '^[[:space:]-]*uses:' \
    | grep -vE 'uses: [^@ ]+@[0-9a-f]{40} # v[0-9]')"
  [ -z "$offen" ] || { echo "nicht per SHA mit Versionskommentar: $offen"; return 1; }
}

workflow_ausloeser() {
  [ -f "$WORKFLOW" ] || return 1
  grep -qF "'ebics-abholer/**'" "$WORKFLOW" || { echo 'Pfad-Ausloeser fehlt'; return 1; }
  grep -qF "'ebics-abholer/v*.*.*'" "$WORKFLOW" || { echo 'Tag-Ausloeser fehlt'; return 1; }
}

workflow_k_scan_je_plattform() {
  [ -f "$WORKFLOW" ] || return 1
  local c
  c="$(code "$WORKFLOW")"
  printf '%s' "$c" | grep -qF -- '--severity CRITICAL,HIGH' || { echo 'Schwellen fehlen'; return 1; }
  printf '%s' "$c" | grep -qF -- '--ignore-unfixed' || { echo '--ignore-unfixed fehlt'; return 1; }
  printf '%s' "$c" | grep -qF -- '--exit-code 1' || { echo '--exit-code 1 fehlt'; return 1; }
  printf '%s' "$c" | grep -qF -- '--ignorefile ebics-abholer/.trivyignore' || { echo 'Ausnahmedatei fehlt'; return 1; }
  printf '%s' "$c" | grep -qF 'linux/amd64' || { echo 'amd64 fehlt'; return 1; }
  printf '%s' "$c" | grep -qF 'linux/arm64' || { echo 'arm64 fehlt'; return 1; }
}

workflow_minimale_rechte() {
  [ -f "$WORKFLOW" ] || return 1
  grep -qE '^permissions:$' "$WORKFLOW" || { echo 'permissions: auf Workflow-Ebene fehlt'; return 1; }
  grep -A1 -E '^permissions:$' "$WORKFLOW" | grep -qE '^  contents: read$' \
    || { echo 'Workflow-Ebene ist nicht nur contents: read'; return 1; }
}

workflow_attestationen() {
  [ -f "$WORKFLOW" ] || return 1
  grep -qE '^[[:space:]]+sbom: true$' "$WORKFLOW" || { echo 'sbom: true fehlt'; return 1; }
  grep -qE '^[[:space:]]+provenance: mode=max$' "$WORKFLOW" || { echo 'provenance: mode=max fehlt'; return 1; }
}

# ── Statisch: Ausnahmen des K-Scans ─────────────────────────────────────────

# Jede Ausnahme: `CVE-… exp:JJJJ-MM-TT # Grund`, Ablauf hoechstens 90 Tage.
trivyignore_regel() {
  local f="$ORDNER/.trivyignore"
  [ -f "$f" ] || { echo '.trivyignore fehlt'; return 1; }
  local heute grenze zeile exp
  heute="$(date -u +%Y-%m-%d)"
  grenze="$(date -u -d "$heute + 90 days" +%Y-%m-%d)"
  while IFS= read -r zeile; do
    case "$zeile" in '' | '#'*) continue ;; esac
    if ! printf '%s' "$zeile" | grep -qE '^CVE-[0-9]{4}-[0-9]+ exp:[0-9]{4}-[0-9]{2}-[0-9]{2} # .{10,}$'; then
      echo "Form falsch: $zeile"
      return 1
    fi
    exp="$(printf '%s' "$zeile" | sed -E 's/.* exp:([0-9-]{10}).*/\1/')"
    if [[ "$exp" > "$grenze" ]]; then
      echo "Ablauf $exp liegt mehr als 90 Tage nach $heute: $zeile"
      return 1
    fi
  done <"$f"
}

echo 'Statisch'
pruefe 'Dockerfile: jede Basis per Digest gepinnt' dockerfile_basis_per_digest
pruefe 'Dockerfile: mehrstufig' dockerfile_mehrstufig
pruefe 'Dockerfile: Composer nur in der Bau-Stufe' dockerfile_composer_nur_im_bau
pruefe 'Dockerfile: Nutzer 65532 und Abruf als Befehl' dockerfile_nutzer_und_befehl
pruefe 'Compose: Image per JNPT_EBICS_IMAGE, kein build:' compose_bezieht_image_per_variable
pruefe 'Compose: Projektname jnpt-plugin-ebics' compose_projektname
pruefe 'Compose: Mounts /ablage und /geheim:ro unveraendert' compose_mounts_unveraendert
pruefe '.env.example traegt einen Digest-Pin als Platzhalter' env_vorlage_mit_platzhalter_pin
pruefe 'Workflow vorhanden' workflow_vorhanden
pruefe 'Workflow: Actions per voller SHA mit Versionskommentar' workflow_actions_per_sha
pruefe 'Workflow: Ausloeser Pfad und Tag' workflow_ausloeser
pruefe 'Workflow: K-Scan je Plattform nach SEC-Regel' workflow_k_scan_je_plattform
pruefe 'Workflow: minimale Rechte' workflow_minimale_rechte
pruefe 'Workflow: SBOM und Provenance' workflow_attestationen
pruefe '.trivyignore: CVE, Grund, exp <= 90 Tage' trivyignore_regel

# ── Laufzeit: das gebaute Image ─────────────────────────────────────────────

if [ -n "$IMAGE" ]; then
  lauf() { docker run --rm --network none --read-only --tmpfs /tmp:mode=1777,size=64m "$@"; }

  image_nutzer() {
    local u
    u="$(docker image inspect "$IMAGE" --format '{{.Config.User}}')"
    [ "$u" = '65532' ] || { echo "Config.User=$u"; return 1; }
    u="$(lauf --entrypoint id "$IMAGE" -u)"
    [ "$u" = '65532' ] || { echo "id -u=$u"; return 1; }
  }

  image_befehl() {
    local c
    c="$(docker image inspect "$IMAGE" --format '{{json .Config.Cmd}}')"
    [ "$c" = '["php","/app/fetch.php"]' ] || { echo "Cmd=$c"; return 1; }
  }

  image_ohne_composer() {
    if lauf --entrypoint sh "$IMAGE" -c 'command -v composer || ls /usr/bin/composer' >/dev/null 2>&1; then
      echo 'composer liegt im Image'
      return 1
    fi
  }

  image_erweiterungen() {
    local m fehlt=''
    m="$(lauf --entrypoint php "$IMAGE" -m)"
    for e in bcmath zip curl dom json libxml openssl zlib; do
      printf '%s\n' "$m" | grep -qx "$e" || fehlt="$fehlt $e"
    done
    [ -z "$fehlt" ] || { echo "fehlt:$fehlt"; return 1; }
  }

  image_dateien() {
    lauf --entrypoint sh "$IMAGE" -c '
      for f in fetch.php hpb.php init.php letter.php logik.php start.php version.php \
               config.example.php .gitignore vendor/autoload.php tests/run.php; do
        [ -f "/app/$f" ] || { echo "fehlt: /app/$f"; exit 1; }
      done'
  }

  image_teststrecke() {
    local aus
    aus="$(lauf --entrypoint php "$IMAGE" /app/tests/run.php)" || { printf '%s\n' "$aus" | tail -5; return 1; }
    if printf '%s' "$aus" | grep -q 'vendor/ fehlt'; then
      echo 'die vendor-Faelle wurden uebersprungen'
      return 1
    fi
  }

  # Ohne Konfiguration endet der Abruf sauber mit Exit 1 — nichts abgerufen.
  image_abbruch_ohne_konfig() {
    local rc
    lauf "$IMAGE" >/dev/null 2>&1
    rc=$?
    [ "$rc" -eq 1 ] || { echo "Exit $rc statt 1"; return 1; }
  }

  image_groesse() {
    local kb mb
    kb="$(docker run --rm --network none --user 0 --entrypoint du "$IMAGE" -sxk / 2>/dev/null | tail -1 | cut -f1)"
    [ -n "$kb" ] || { echo 'du lieferte nichts'; return 1; }
    mb=$((kb * 1024 / 1000000))
    echo "$mb MB"
    [ "$mb" -le "$GROESSE_MAX_MB" ] || { echo "$mb MB > $GROESSE_MAX_MB MB"; return 1; }
  }

  echo "Image $IMAGE"
  pruefe 'Image: laeuft als 65532' image_nutzer
  pruefe 'Image: Befehl ist der Abruf' image_befehl
  pruefe 'Image: kein Composer zur Laufzeit' image_ohne_composer
  pruefe 'Image: PHP-Erweiterungen der Bibliothek geladen' image_erweiterungen
  pruefe 'Image: Skripte, Vorlage und vendor/ vorhanden' image_dateien
  pruefe 'Image: Teststrecke vollstaendig gruen, ohne Netz' image_teststrecke
  pruefe 'Image: ohne Konfiguration sauberer Abbruch mit Exit 1' image_abbruch_ohne_konfig
  pruefe "Image: hoechstens $GROESSE_MAX_MB MB" image_groesse
fi

echo
echo "$GRUEN gruen, $ROT rot"
[ "$ROT" -eq 0 ]
