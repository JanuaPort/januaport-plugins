# Ein Image für drei Dienste des Skript-Läufers (JanuaPort/januaport#928):
# vermittler, Sandbox (Wächter + Python) und abholer. Welcher Dienst läuft,
# entscheidet der Unterbefehl in docker-compose.yml.
#
# Beide Basis-Images sind per Digest gepinnt (Index-Digest, gültig für jede
# Architektur). Ein Wechsel ist eine bewusste Änderung mit Changelog-Eintrag.

FROM golang:1.25.13@sha256:cbff9d1a9041b316010f2da6b701b6c0d597718cb90928c85eb597334a0d23d4 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
COPY internal ./internal
# Statisch, ohne CGO: dasselbe Binary läuft in allen drei Diensten.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/script-runner .

FROM python:3.12-slim@sha256:f77ac9e44ae96ef2c90b8053ea08c31f8be030f824196b0ae4db6d462c84e51f
# git (Debian-Build, SHA-1 über sha1dc) und der ssh-Client für den abholer.
# pip und ensurepip werden entfernt: im Lauf gibt es keine Installation (R12).
# Zwei Nutzer ohne Login: 65532 für vermittler und abholer (Eigentümer der
# Plugin-Ordner auf dem Host), 65533 für die Sandbox.
RUN apt-get update \
 && apt-get install -y --no-install-recommends git openssh-client \
 && rm -rf /var/lib/apt/lists/* \
 && rm -rf /usr/local/lib/python3.12/ensurepip \
           /usr/local/lib/python3.12/site-packages/pip \
           /usr/local/lib/python3.12/site-packages/pip-* \
           /usr/local/bin/pip /usr/local/bin/pip3 /usr/local/bin/pip3.12 \
 && groupadd -g 65532 scriptrunner && useradd -u 65532 -g 65532 -M -d /nonexistent -s /usr/sbin/nologin scriptrunner \
 && groupadd -g 65533 sandbox && useradd -u 65533 -g 65533 -M -d /work -s /usr/sbin/nologin sandbox \
 && install -d -o 65532 -g 65532 -m 0755 /run/script-runner /var/lib/script-runner
COPY --from=build /out/script-runner /usr/local/bin/script-runner
COPY sandbox/bootstrap.py /usr/local/lib/script-runner/bootstrap.py
ENTRYPOINT ["/usr/local/bin/script-runner"]
