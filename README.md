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

Status is based on local Git tags and each module CHANGELOG. **Released** identifies the tagged
version; running proof is listed separately and applies only to the version verified.
For `tunnel`, the curated upstream version in the CHANGELOG is authoritative. **Built** without
a version identifies untagged components. Each entry says what exactly has been verified.

| Folder | What it does | Status |
|---|---|---|
| [`ebics-abholer/`](ebics-abholer/) | Fetches the daily bank statement (camt.053) from the bank via EBICS and places it as a file in a shared directory that JanuaPort reads from. It cannot submit payments. | **Released, running proof recorded (v1.0.1).** Tags `ebics-abholer/v1.0.0` and `ebics-abholer/v1.0.1`; image `ghcr.io/januaport/ebics` (amd64, arm64). The [CHANGELOG](ebics-abholer/CHANGELOG.md) records a run of that exact 1.0.1 digest on the pilot installation on 19 November 2026, exit 0. Productive EBICS H005 timer operation since 7 September 2026; verified against one bank so far. |
| [`kartei-rpa/`](kartei-rpa/) | A PowerShell module and four action scripts that let an RPA tool (Power Automate Desktop) work through a work list in the JanuaPort Kartei (structured record store): fetch approved records, lock them one by one, book them in the business system, write the result back. Field names come from a settings file; the defaults match the recipe "Zahlungseingang" (incoming payments) in `januaport-recipes`. | **Built.** Tested with a mocked transport (Pester, Windows PowerShell 5.1). This version has not yet run against a real installation. |
| [`box/`](box/) | Provisioning for the JanuaPort Box, a fanless mini PC that runs the gateway on the customer's premises: Compose stack, host hardening, boot order, Caddy surfaces, OpenAI tunnel profile, optional backup to SharePoint or SMB, and a runbook. No inbound port from the internet; remote maintenance only on the customer's opt-in. | **Built.** Running on a pilot installation since August 2026. This repository holds a cleaned-up, generalised copy (neutral names, install path `/opt/jnpt/box`) that has not yet run on a device in exactly this form. |
| [`tunnel/`](tunnel/) | Curation record for the catalog entry `tunnel`: the OpenAI Secure MCP Tunnel client, a third-party image that JanuaPort offers in its signed plugin catalog. No code here; the changelog records each curated digest, its scan and the JanuaPort version it was checked with. | **Released, no running proof recorded (v0.0.15).** The [CHANGELOG](tunnel/CHANGELOG.md) records curation and cataloguing for JanuaPort 0.67 on 28 September 2026; running proof for this digest is pending. Upstream version, no local tunnel tag. |
| [`script-runner/`](script-runner/) | Runs pinned Python scripts from the operator's Git repository or approved storage packages when an AI calls them through JanuaPort. A script may use JanuaPort tools itself, with the caller's permissions and only the tools it declares, and optional Python libraries (numpy, pandas, …) shipped as hash-checked wheels in the operator's own repository, mounted read-only (no pip, no package-index download). Up to four services from one image: a mediator next to the gateway, a sandbox without network (one run per instance, gVisor where available), an ssh fetcher with a read-only deploy key and, behind the Compose profile `apps`, an app host that serves live browser apps from approved storage packages. | **Released, running proof recorded (v0.4.2).** Tag `script-runner-v0.4.2`; the [CHANGELOG](script-runner/CHANGELOG.md) records showcase operation with JanuaPort 0.94.2 since 5 November 2026: successful `script_echo`, apphost with `monitor` and `steuer`, app login and invocation recorded in the audit log. Unit tests and Docker probes under runc and gVisor (runsc). |
| [`google-reporting/`](google-reporting/) | Fixed GSC/GA4 reports plus three write handles (GA4 annotation, GSC sitemap, GA4 channel group, #1148) through a private Python REST service, one credential object per scope, approved public pages, bounded process workers. | **Released, no running proof recorded (v1.1.0).** Tag `google-reporting-v1.1.0`; image `ghcr.io/januaport/google-reporting`. Synthetic SDK/HTTP/cancellation/privacy/audit contracts verified. Several sites per service (#1379), site only from the instance header, rights per instance; query text disabled by default (operator switch). The [CHANGELOG](google-reporting/CHANGELOG.md) records showcase operation of 1.0.1 since 28 October 2026, no run of 1.1.0. |
| [`linkedin-seite/`](linkedin-seite/) | Stage A: fixed GET reports for one configured LinkedIn page; aggregate statistics and up to ten posts, OAuth in the vault, bounded workers and 50 HTTP attempts per UTC day. Stage B1: posting after human hash approval in the core. Stage B3 (0.4.0): reading comments under own posts and replying as the organization within fixed live checks, plus a daily link list. | **Released, no running proof recorded (v0.4.1).** Tag `linkedin-seite-v0.4.1`; synthetic HTTP and negative tests. The [CHANGELOG](linkedin-seite/CHANGELOG.md) records no live proof for the extended comment forms and diagnostics; manufacturer contract and live acceptance remain open. |
| [`bing-webmaster/`](bing-webmaster/) | Six fixed routes for Bing Webmaster and IndexNow through a private Python REST service: index status of one page, sitemap list and submission quota (read); URL batch, sitemap and IndexNow submission (write, `dry_run`, 20 attempts per route and UTC day). The Bing key lives only in the sidecar and in the outgoing request to `ssl.bing.com`; configured site and host allowlist only. | **Released, no running proof recorded (v0.1.1).** Tag `bing-webmaster-v0.1.1`; image `ghcr.io/januaport/bing-webmaster`. Synthetic HTTP, key-canary and negative tests against a loopback stub. The [CHANGELOG](bing-webmaster/CHANGELOG.md) mentions live findings from 0.1.0, no run of 0.1.1; manufacturer contract (quotes around `url`, quota fields) and live acceptance remain open. No page statistics, no crawl; AI Performance has no API. |
| [`agent-runner/`](agent-runner/) | Runs an agent's model/tool loop through JanuaPort's model and MCP gateways. Anthropic Messages and OpenAI Chat Completions, local step/call/time limits, content-free run log; internal network, no provider key. | **Released, no running proof recorded (v0.2.2).** Tag `agent-runner-v0.2.2`; contract and negative tests against a local fake core. The [CHANGELOG](agent-runner/CHANGELOG.md) records live provider calls with 0.2.1 on the showcase, no run of 0.2.2. |
| [`desktop-runner/`](desktop-runner/) | Desktop-Läufer for `geraet:<id>` pins: the device pulls everything (path B), pairs and verifies a signed manifest, schedules locally, uses contract 5 on Windows (4 plus assigned PC apps; server: 2), runs offline without tools within the pin grace period with a persistent clock watermark, and reports later from a DPAPI journal. | **Released, no running proof recorded (v0.5.4).** Tag `desktop-runner-v0.5.4`; signed Windows releases, native COM/DPAPI/Job Object probes, Linux race tests and a fake core. Earlier versions: manual [installation test](desktop-runner/README.md#installationstest) without a PO device (#1233) and VM countercheck for 0.5.1. The [CHANGELOG](desktop-runner/CHANGELOG.md) records no device proof for the 0.5.4 hotkeys; real core integration needs separate verification. |

## Releases

Images that this repository builds itself are released only from a version tag, never from a branch.
For `script-runner` the tag is `script-runner-vX.Y.Z`: the workflow runs the unit tests, builds one
`linux/amd64` image with SBOM and provenance, pushes it by digest to `ghcr.io/januaport/script-runner`,
runs the Docker probes against exactly that digest, and only then sets the image tag `X.Y.Z`. A
vulnerability report (Trivy) is attached to the run; it is not a gate. Installations obtain the image
only by digest from JanuaPort's signed plugin catalog. What was checked for each digest is recorded in
the sidecar's `CHANGELOG.md` before it is catalogued.
The current tag is `script-runner-v0.4.2`, with showcase proof in the CHANGELOG.
`google-reporting` follows the same path with the tag `google-reporting-vX.Y.Z` and the image
`ghcr.io/januaport/google-reporting` (probes: config digest equals pushed digest, user 65532, unit
tests in a hardened container without network, start without configuration fails closed).
Current tag: `google-reporting-v1.1.0`; the CHANGELOG records running proof for the earlier 1.0.1; proof for 1.1.0 is pending.

`linkedin-seite` uses the same release pattern: `linkedin-seite-vX.Y.Z`, image
`ghcr.io/januaport/linkedin-seite`; currently `linkedin-seite-v0.4.1`, stages A, B1 and B3.
Running proof for 0.4.1 and cataloguing are not documented in the CHANGELOG.

`bing-webmaster` uses the same release pattern: `bing-webmaster-vX.Y.Z`, image
`ghcr.io/januaport/bing-webmaster`; currently `bing-webmaster-v0.1.1`.
Running proof for 0.1.1 and curation data are not documented in the CHANGELOG.

Further tags: `ebics-abholer/v1.0.1` (previously `ebics-abholer/v1.0.0`), `agent-runner-v0.2.2`
and `desktop-runner-v0.5.4`. `tunnel` is a third-party image: its CHANGELOG lists the curated
upstream version `v0.0.15`, no local release tag. The status table separates releases from
running proof. Local guard without network: `sh scripts/check-readme-tags.sh`; no CI integration.

## Google Reporting (#1130)

[google-reporting](google-reporting/README.md) provides fixed GSC/GA4 reports and, since #1148, three bounded write handles (annotation, sitemap, channel group; no delete) using official Google clients and bounded, cancellable process workers. Released as `google-reporting-v1.1.0`, with several sites per service (#1379). The release workflow (#1138) and fixes for the GA4 status check and a too-late `end_date` (#1150) are included. The CHANGELOG records reports on the showcase with 1.0.1; operation and live acceptance of 1.1.0 are not documented.
