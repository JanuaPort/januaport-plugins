# syntax=docker/dockerfile:1.7
FROM python:3.12-slim@sha256:2259debdc6af85626058a15a7bf44ad6a68b8c98c9ae82a02cf5cdb92f096d47
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1
WORKDIR /app
COPY requirements.lock ./
# Proxy/CA secret is build-only; absent on ordinary operator builds.
RUN --mount=type=secret,id=jnpt_ca,target=/run/secrets/build-ca.pem \
    if [ -f /run/secrets/build-ca.pem ]; then export PIP_CERT=/run/secrets/build-ca.pem; fi; \
    pip install --no-cache-dir --require-hashes -r requirements.lock
COPY --chown=65532:65532 google_reporting ./google_reporting
USER 65532:65532
ENTRYPOINT ["python", "-m", "google_reporting.server"]
