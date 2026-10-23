# januaport-plugins

**English** · [Deutsch](README.de.md)

Sidecars and the Desktop-Läufer for JanuaPort: separate components next to the gateway or on a device.

JanuaPort is a self-hosted MCP gateway. It connects AI assistants to a company's existing systems with
fine-grained permissions and records access in an append-only audit log. The core of JanuaPort is
proprietary software of JanuaPort GmbH and is not part of this repository. This repository is one of the
open edges around it.

- **License:** Apache License 2.0 ([`LICENSE`](LICENSE), [`NOTICE`](NOTICE))
- **Links:** [januaport.ai](https://januaport.ai) · [Security policy](SECURITY.md) · [Contributing](CONTRIBUTING.md)
- **Language:** The guides in the subfolders are currently written in German.

No customer data, no keys, no operator values in this repository.

---

## Why sidecars

The JanuaPort gateway is a single static binary in a minimal container. Anything that needs a different
runtime, its own schedule or its own credentials runs as a separate sidecar next to it, or as the
Desktop-Läufer on a device. These components reach JanuaPort through documented interfaces:
a shared directory, the MCP endpoint with their own token, or the device API. They can be
switched off without touching the gateway.

## Included sidecars

Status labels: **Built** · **In progress** · **Planned**. Each entry says what exactly has been verified.

| Folder | What it does | Status |
|---|---|---|
| [`ebics-abholer/`](ebics-abholer/) | Fetches the daily bank statement (camt.053) from the bank via EBICS and places it as a file in a shared directory that JanuaPort reads from. It cannot submit payments. | **Built.** In productive use at JanuaPort GmbH for its own company account (EBICS H005, daily timer run since 7 September 2026). Verified against one bank so far. Image `ghcr.io/januaport/ebics` (amd64, arm64): **in progress** — release workflow with scan built, no version published yet. |
| [`kartei-rpa/`](kartei-rpa/) | A PowerShell module and four action scripts that let an RPA tool (Power Automate Desktop) work through a work list in the JanuaPort Kartei (structured record store): fetch approved records, lock them one by one, book them in the business system, write the result back. Field names come from a settings file; the defaults match the recipe "Zahlungseingang" (incoming payments) in `januaport-recipes`. | **Built.** Tested with a mocked transport (Pester, Windows PowerShell 5.1). This version has not yet run against a real installation. |
| [`box/`](box/) | Provisioning for the JanuaPort Box, a fanless mini PC that runs the gateway on the customer's premises: Compose stack, host hardening, boot order, Caddy surfaces, OpenAI tunnel profile, optional backup to SharePoint or SMB, and a runbook. No inbound port from the internet; remote maintenance only on the customer's opt-in. | **Built.** Running on a pilot installation since August 2026. This repository holds a cleaned-up, generalised copy (neutral names, install path `/opt/jnpt/box`) that has not yet run on a device in exactly this form. |
| [`tunnel/`](tunnel/) | Curation record for the catalog entry `tunnel`: the OpenAI Secure MCP Tunnel client, a third-party image that JanuaPort offers in its signed plugin catalog. No code here; the changelog records each curated digest, its scan and the JanuaPort version it was checked with. | **Built.** First entry `v0.0.14`, checked with JanuaPort 0.66 on 27 September 2026. |
| [`script-runner/`](script-runner/) | Runs pinned Python scripts from the operator's Git repository when an AI calls them through JanuaPort. A script may use JanuaPort tools itself, with the caller's permissions and only the tools it declares, and optional Python libraries (numpy, pandas, …) shipped as hash-checked wheels in the operator's own repository, mounted read-only (no pip, no download). Three services from one image: a mediator next to the gateway, a sandbox without network (one run per instance, gVisor where available) and an ssh fetcher with a read-only deploy key. | **In progress.** Tested with unit tests and Docker probes against a stub gateway and a local Git server, under runc and under gVisor (runsc). The gateway side (pins, run token, audit), release and catalog entry are not built yet; not yet run on an installation. |
| [`google-reporting/`](google-reporting/) | Fixed GSC/GA4 reports plus three write handles (GA4 annotation, GSC sitemap, GA4 channel group, #1148) through a private Python REST service, one credential object per scope, approved public pages, bounded process workers. | **Built, live integration pending.** Synthetic SDK/HTTP/cancellation/privacy/audit contracts verified. Query text disabled by default (operator switch). Version 1.0.0 is prepared with a release workflow (image `ghcr.io/januaport/google-reporting`), not yet tagged; no signed catalog entry yet. |
| [`agent-runner/`](agent-runner/) | Runs an agent's model/tool loop through JanuaPort's model and MCP gateways. Anthropic Messages, local step/call/time limits, content-free run log; internal network, no provider key. | **Built, live integration pending.** B1 verified with a local fake core, contract and negative tests; no live provider calls. |
| [`desktop-runner/`](desktop-runner/) | Desktop-Läufer for `geraet:<id>` pins: the device pulls everything (path B), pairs and verifies a signed manifest, schedules locally, runs offline without tools within the grace period, and reports later from a DPAPI journal. | **In progress.** #1163 D0: extended contract v1, goldens and a Go artifact test; runtime adapters and live verification are pending. |

## Releases

Images that this repository builds itself are released only from a version tag, never from a branch.
For `script-runner` the tag is `script-runner-vX.Y.Z`: the workflow runs the unit tests, builds one
`linux/amd64` image with SBOM and provenance, pushes it by digest to `ghcr.io/januaport/script-runner`,
runs the Docker probes against exactly that digest, and only then sets the image tag `X.Y.Z`. A
vulnerability report (Trivy) is attached to the run; it is not a gate. Installations obtain the image
only by digest from JanuaPort's signed plugin catalog. What was checked for each digest is recorded in
the sidecar's `CHANGELOG.md` before it is catalogued. No release has been published yet.
`google-reporting` follows the same path with the tag `google-reporting-vX.Y.Z` and the image
`ghcr.io/januaport/google-reporting` (probes: config digest equals pushed digest, user 65532, unit
tests in a hardened container without network, start without configuration fails closed).

## Google Reporting (#1130)

[google-reporting](google-reporting/README.md) provides fixed GSC/GA4 reports and, since #1148, three bounded write handles (annotation, sitemap, channel group; no delete) using official Google clients and bounded, cancellable process workers. Stage 1: built and tested with synthetic data; live acceptance is pending. Version 1.0.0 is prepared with a release workflow (#1138) and fixes for the GA4 status check and a too-late `end_date` (#1150). This change adds no signed catalog entries or release tags.
