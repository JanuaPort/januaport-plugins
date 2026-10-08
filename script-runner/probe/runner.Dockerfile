# Test-Läufer der Docker-Sonden: Go wie im Build, dazu das Docker-CLI mit
# Compose-Plugin. Er spricht über den eingehängten Socket mit dem Docker des
# Hosts. NICHT ausliefern.
ARG GO_IMAGE=golang:1.25.13
FROM docker:29-cli AS cli
FROM ${GO_IMAGE}
COPY --from=cli /usr/local/bin/docker /usr/local/bin/docker
COPY --from=cli /usr/local/libexec/docker/cli-plugins/docker-compose /usr/local/libexec/docker/cli-plugins/docker-compose
