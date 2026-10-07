# Image des EBICS-Abholers (#549, schlank seit JanuaPort/januaport#1005) —
# Beisteller NEBEN dem Gateway.
#
# ⚠️ Dies ist ausdruecklich NICHT das Gateway-Image. Das Gateway bleibt ein
# CGO-freies statisches Binary in einem distroless-Container; hier drin steckt
# ein PHP-Interpreter, weil die einzige gepflegte EBICS-Bibliothek mit
# H003–H005 in PHP geschrieben ist (#548). Die Trennung IST die
# Architektur-Entscheidung — kein PHP im Gateway, kein Gateway hier.
#
# Drei Stufen, damit zur Laufzeit nur PHP-CLI mit den Erweiterungen liegt:
#   vendor        Composer holt die Bibliothek aus der composer.lock
#   erweiterungen bcmath und zip werden gegen dieselbe PHP-Basis gebaut
#   (Laufzeit)    PHP-CLI, die beiden Erweiterungen, die Skripte — sonst nichts
#
# Alle Basen sind per Digest gepinnt: Ein Neubau mit demselben Dockerfile zieht
# dieselben Schichten. Angehoben wird bewusst (Tag UND Digest tauschen), etwa
# wenn der K-Scan einen Befund mit Fix meldet (README, „Release").

# ── vendor ──────────────────────────────────────────────────────────────────
# Laeuft auf der Bau-Plattform: vendor/ ist reines PHP und fuer amd64 und
# arm64 dasselbe — so laeuft Composer nie unter Emulation.
#
# --ignore-platform-reqs, weil diese Stufe ein anderes PHP mitbringt als die
# Laufzeit. Die Pruefung der Erweiterungen holt die Laufzeit-Stufe nach.
FROM --platform=$BUILDPLATFORM composer:2.10.3@sha256:af98f42dfff7c68ba8d53c2164fd9fde1087b7d449514baa38c418b1f6bc4bac AS vendor

WORKDIR /app

# Aus der composer.lock: dieselben Fassungen, die am 20.08.2026 live gegen
# eine echte Bank gelaufen sind.
COPY composer.json composer.lock ./
RUN composer install --no-dev --no-interaction --prefer-dist --no-progress \
    --no-scripts --ignore-platform-reqs

# ── erweiterungen ───────────────────────────────────────────────────────────
# bcmath: die RSA-Arithmetik der Bibliothek · zip: der camt-Container der Bank.
# Compiler und Header bleiben in dieser Stufe zurueck.
FROM php:8.5.11-cli-alpine3.24@sha256:93684051146ec037620855feb77f278090bde45ddc030801cd3f2a7685bc4deb AS erweiterungen

RUN apk add --no-cache $PHPIZE_DEPS libzip-dev \
    && docker-php-ext-install bcmath zip

# ── Laufzeit ────────────────────────────────────────────────────────────────
FROM php:8.5.11-cli-alpine3.24@sha256:93684051146ec037620855feb77f278090bde45ddc030801cd3f2a7685bc4deb

# libzip ist die einzige Laufzeit-Bibliothek, die die zip-Erweiterung braucht.
RUN apk add --no-cache libzip

COPY --from=erweiterungen /usr/local/lib/php/extensions/ /usr/local/lib/php/extensions/
COPY --from=erweiterungen /usr/local/etc/php/conf.d/ /usr/local/etc/php/conf.d/

# Die Pruefung, die Composer in der vendor-Stufe ausgelassen hat: Fehlt eine
# Erweiterung, die die Bibliothek verlangt, scheitert der Bau — nicht der
# erste Abruf an der Bank.
RUN php -r 'foreach (["bcmath","curl","dom","json","libxml","openssl","zip","zlib"] as $e) { if (!extension_loaded($e)) { fwrite(STDERR, "Erweiterung fehlt: $e\n"); exit(1); } }'

WORKDIR /app

COPY --from=vendor /app/vendor ./vendor
# composer.json bleibt neben vendor/, weil die Teststrecke darin die erprobten
# Pakete prueft; das Werkzeug selbst ist nicht im Image.
COPY composer.json composer.lock ./

COPY logik.php start.php version.php fetch.php init.php hpb.php letter.php ./
COPY tests/run.php ./tests/

# Die Vorlage liegt im Image, damit sie auf der Anlage greifbar ist:
#   docker compose run --rm ebics-abholer cat /app/config.example.php > config.php
# Die .gitignore kommt mit, weil die Teststrecke sie prueft (Geheimnis-Disziplin
# ist eine Regel des Ordners, und die Strecke soll im Image vollstaendig sein).
COPY config.example.php .gitignore ./

# Nicht-root, mit derselben UID wie das Gateway (distroless `nonroot`, 65532).
# Damit gehoeren die abgelegten Auszuege dem Nutzer, der sie danach lesen soll.
# ⚠️ Die gemounteten Verzeichnisse `/geheim` und `/ablage` muessen dieser UID
# gehoeren — sonst scheitert der Lauf beim Lesen der Schluesseldatei bzw. beim
# Schreiben der Datei (Runbook, Abschnitt „Verzeichnisse anlegen").
RUN adduser -D -u 65532 -s /sbin/nologin abholer
USER 65532

# Der Normalfall ist der Abruf. Die Initialisierungs-Skripte werden von Hand
# aufgerufen (`docker compose run --rm ebics-abholer php /app/init.php`).
CMD ["php", "/app/fetch.php"]
