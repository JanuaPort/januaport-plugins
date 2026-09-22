# Image des EBICS-Abholers (#549) — Beisteller NEBEN dem Gateway.
#
# ⚠️ Dies ist ausdruecklich NICHT das Gateway-Image. Das Gateway bleibt ein
# CGO-freies statisches Binary in einem distroless-Container (Root-CLAUDE.md
# §4); hier drin steckt ein PHP-Interpreter, weil die einzige gepflegte
# EBICS-Bibliothek mit H003–H005 in PHP geschrieben ist (#548). Die Trennung
# IST die Architektur-Entscheidung — kein PHP im Gateway, kein Gateway hier.
#
# bcmath: die RSA-Arithmetik der Bibliothek · zip: der camt-Container der Bank
# (und das Entpacken der Composer-Pakete).
FROM php:8.5-cli

RUN apt-get update && apt-get install -y --no-install-recommends libzip-dev \
    && rm -rf /var/lib/apt/lists/* \
    && docker-php-ext-install bcmath zip

COPY --from=composer:latest /usr/bin/composer /usr/bin/composer

WORKDIR /app

# Erst die Abhaengigkeiten (eigene Schicht — sie aendern sich selten), und
# zwar aus der composer.lock: dieselben Fassungen, die am 20.08.2026 live
# gegen eine echte Bank gelaufen sind.
COPY composer.json composer.lock ./
RUN composer install --no-dev --no-interaction --prefer-dist --no-progress \
    && rm -rf /root/.composer /usr/bin/composer

COPY logik.php start.php version.php fetch.php init.php hpb.php letter.php ./
COPY tests/ ./tests/

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
RUN useradd --uid 65532 --create-home --shell /usr/sbin/nologin abholer
USER 65532

# Der Normalfall ist der Abruf. Die Initialisierungs-Skripte werden von Hand
# aufgerufen (`docker compose run --rm ebics-abholer php /app/init.php`).
CMD ["php", "/app/fetch.php"]
