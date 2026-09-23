# januaport-plugins

**English** · [Deutsch](README.de.md)

Sidecars for JanuaPort: small, separate services that run next to the gateway, not inside it.

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
runtime, its own schedule or its own credentials runs as a separate sidecar next to it. A sidecar reaches
JanuaPort only through documented interfaces: a shared directory, or the MCP endpoint with its
own token. It can be switched off without touching the gateway.

## Included sidecars

Status labels: **Built** · **In progress** · **Planned**. Each entry says what exactly has been verified.

| Folder | What it does | Status |
|---|---|---|
| [`ebics-abholer/`](ebics-abholer/) | Fetches the daily bank statement (camt.053) from the bank via EBICS and places it as a file in a shared directory that JanuaPort reads from. It cannot submit payments. | **Built.** In productive use at JanuaPort GmbH for its own company account (EBICS H005, daily timer run since 7 September 2026). Verified against one bank so far. |
| [`kartei-rpa/`](kartei-rpa/) | A PowerShell module and four action scripts that let an RPA tool (Power Automate Desktop) work through a work list in the JanuaPort Kartei (structured record store): fetch approved records, lock them one by one, book them in the business system, write the result back. Field names come from a settings file; the defaults match the recipe "Zahlungseingang" (incoming payments) in `januaport-recipes`. | **Built.** Tested with a mocked transport (Pester, Windows PowerShell 5.1). This version has not yet run against a real installation. |
| `box/` (not in this repository yet) | Provisioning for the JanuaPort Box (on-premises appliance). | **Planned.** |
