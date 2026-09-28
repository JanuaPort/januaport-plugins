# Changelog — catalog entry `tunnel`

The plugin `tunnel` is the OpenAI Secure MCP Tunnel client, a third-party image
(`ghcr.io/openai/tunnel-client`) that JanuaPort runs as a neighbour process next to the
gateway. JanuaPort does not build this image; it curates which digest the signed plugin
catalog offers. Each entry below records one curated digest: what was checked, with which
JanuaPort version, and what the scan found at that date.

A catalog entry is not a statement that the image is free of known vulnerabilities. It
states that this exact digest was measured, scanned and seen running with the named
JanuaPort version.

## v0.0.15 — 28 September 2026

| Field | Value |
|---|---|
| Image | `ghcr.io/openai/tunnel-client` |
| Upstream version | `v0.0.15` |
| Digest (OCI index) | `sha256:119799b778ba8411a124f53588f9dc837fd62ba03e5fadf77d675123c92ab58e` |
| Platforms | `linux/amd64` (`sha256:4194e1b0…`), `linux/arm64` (`sha256:57b274af…`) |
| Kind | `service`, third party |
| Checked with | JanuaPort 0.67 |

**Digest** measured twice on 28 September 2026: registry manifest header and
`docker buildx imagetools inspect`. Both agree.

**Running proof:** not yet. Unlike `v0.0.14`, this digest ran nowhere before it was
catalogued. The update of the showcase installation from `v0.0.14` to this entry, through
the signed catalog and the updater, is the proof. If the update fails, the updater keeps
`v0.0.14`, and this entry is amended with the date and the finding.

**Scan** at the digest, per platform: Trivy 0.74.0, vulnerability database v2 of
28 September 2026, scanner `vuln`, base Alpine 3.22.6. Both platforms identical:
**0 critical · 5 high** · 4 medium · 3 low · 1 unknown (`v0.0.14`: 0 · 11 · 15 · 15 · 2).
Fixed compared with `v0.0.14`: Alpine `libcrypto3` / `libssl3` (CVE-2026-14456) and four of
the five `golang.org/x/net` findings in `tunnel-client`. Remaining high findings, each with
a fixed version upstream:

- `usr/bin/cloudflared`: `golang.org/x/crypto` v0.53.0 (CVE-2026-56854);
  `google.golang.org/grpc` v1.83.0 (CVE-2026-84304, CVE-2026-84445)
- `usr/bin/tunnel-client`: `go.opentelemetry.io/otel/sdk` v1.41.0 (CVE-2026-39883);
  `golang.org/x/net` v0.55.0 (CVE-2026-46600)

**Decision:** catalogued, because it is strictly better than `v0.0.14` on every scan
severity and all remaining findings are upstream. This entry is the first real plugin
update of the catalog.

Source: JanuaPort/januaport#885.

## v0.0.14 — 27 September 2026

| Field | Value |
|---|---|
| Image | `ghcr.io/openai/tunnel-client` |
| Upstream version | `v0.0.14` |
| Digest (OCI index) | `sha256:41d7c85dab37797a3eaa17c41b94a7206dd0bc186fd361034c9ec3863596ff6c` |
| Platforms | `linux/amd64` (`sha256:77a76fb9…`), `linux/arm64` (`sha256:9fa9acab…`) |
| Kind | `service`, third party |
| Checked with | JanuaPort 0.66 |

**Digest** measured three times on 27 September 2026: registry manifest header,
`docker buildx imagetools inspect`, and the running container on the showcase installation.
All three agree.

**Running proof:** the showcase installation has run this digest since 18 September 2026
without a restart; poll cycles every 30 seconds, no errors in a 30-minute sample on
27 September 2026.

**Scan** at the digest, per platform: Trivy 0.74.0, vulnerability database v2 of
27 September 2026, scanner `vuln`, base Alpine 3.22.5. Both platforms identical:
**0 critical · 11 high** · 15 medium · 15 low · 2 unknown. All high findings have a fixed
version upstream:

- Alpine `libcrypto3` / `libssl3` 3.5.7-r0: CVE-2026-14456 (fixed in 3.5.8-r0)
- `usr/bin/cloudflared`: `golang.org/x/crypto` v0.53.0 (CVE-2026-56854);
  `google.golang.org/grpc` v1.83.0 (CVE-2026-84304, CVE-2026-84445)
- `usr/bin/tunnel-client`: `go.opentelemetry.io/otel/sdk` v1.41.0 (CVE-2026-39883);
  `golang.org/x/net` v0.43.0 (CVE-2026-25681, CVE-2026-27136, CVE-2026-33814,
  CVE-2026-39821, CVE-2026-46600)

**Decision:** catalogued as the current state, because this digest already runs on the
installations the catalog reaches and the catalog does not make it worse. Upstream
`v0.0.15` (5 high in the same scan) is planned as the next entry and as the first real
plugin update, with its own proof on the showcase, **by 10 October 2026**. If `v0.0.15`
fails there, `v0.0.14` stays and this entry is amended with the date.

Source: JanuaPort/januaport#885.
