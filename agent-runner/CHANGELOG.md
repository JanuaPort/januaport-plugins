# Changelog

## 0.2.1 — 2026-10-28

#1228.

- OpenAI-Anfragen senden `max_completion_tokens`; die eigene Prüfung weist
  `max_tokens` auch zusammen mit dem neuen Feld ab. Anthropic nutzt weiterhin
  `max_tokens`. Kein automatischer Feldwechsel im Lauf bei Anbieterfehlern.
- Der Deckel `min(1024, limits.tokens_out)` bleibt bei jedem Modellaufruf
  wirksam; Adapter- und Lauf-Tests prüfen beide Protokolle samt Folgeaufruf.
- MCP-Implementierungsversion 0.2.1. Anbieterquellen und Grenzen im
  Draht-Vertrag; bisherige Kern-Goldens unverändert.

## 0.2.0 — 2026-10-28

JanuaPort/januaport#1193 (Epic #1191).

- OpenAI Chat Completions neben Anthropic Messages: System-Nachricht,
  Funktionswerkzeuge, mehrere korrelierte Werkzeugergebnisse, angehängte Historie.
- SEC-R4 H1: eigene OpenAI-Prüfung auf erzeugte Felder und Rollen eingeengt;
  keine developer-Rolle, benannten Nachrichten oder Inhaltsteile, nur Funktionswerkzeuge.
- SEC-R4 H2: `content_filter` endet mit bestehender Klasse `limit` und eigenem
  Detail `content_filter`; das Lauf-Log trägt optional das feste Ende-Detail.
  Der aktuelle Kern filtert dieses neue Detail bei der Übernahme noch heraus.
- Optionales `model.protocol`; fehlend bleibt Anthropic. Unbekannte oder
  ungültige Werte scheitern vor dem Lauf mit festem Fehler ohne Echo.
- `tools/list` ergänzt `protocols: ["anthropic","openai"]`; Contract bleibt 1,
  MCP-Implementierungsversion 0.2.0.
- `stop` endet erfolgreich, `length` als `limit/tokens_out`; ungültige
  JSON-Argumente erhalten ein lokales Werkzeug-Fehlerergebnis.
- Drei OpenAI-Goldens und erweitertes SHA256SUMS; alte Goldens byte-identisch.
- Adaptertabellen, Fake-Kern-Schleife mit zwei Werkzeugaufrufen, Negativ-
  und Mutationsproben; keine Live-Anbieterabnahme.

OpenAI-Format braucht JanuaPort mit `model.protocol` (Version folgt); ein älterer Kern schickt kein Protokoll, dann gilt Anthropic.

## 0.1.2 — 2026-10-23

**Go 1.26.10.** JanuaPort/januaport#1099.

- Build-Image `golang:1.26.10`, per Digest gepinnt, `go.mod` auf 1.26.10. Damit
  ist CVE-2026-111943 (crypto/x509 in der stdlib 1.26.9, Trivy HIGH) behoben.
  Der Läufer nutzt kein TLS, die Lücke war deshalb nicht erreichbar. Laut
  Kuratierung muss der Bump aber vor dem Katalogeintrag kommen.
- MCP-Implementierungsversion 0.1.2. Sonst keine Änderung.

## 0.1.1 — 2026-10-23

**Fix für den Live-Durchstich.** JanuaPort/januaport#1099.

- Anthropic liefert seit Oktober 2026 in jedem `tool_use`-Block das Feld
  `caller`. 0.1.0 wies es ab, und jeder Lauf endete im ersten Schritt mit
  `error/gateway`. Zugelassen ist genau `{"type":"direct"}`. Andere Aufrufer
  (Code-Ausführung) und weitere Felder scheitern weiterhin. Der Block bleibt
  unverändert in der Historie.
- Neue Felder oben in der Antwort (`container`, `stop_details`) liest der Läufer
  nicht. Ein Test hält das fest.
- MCP-Implementierungsversion 0.1.1.

## 0.1.0 — 2026-10-17

**Gebaut, Live-Durchstich ausstehend.** JanuaPort/januaport#1099 B1.

- MCP-Beisteller mit genau `run_agent`, runtime-key und Lauf-Token.
- Anthropic-Messages-Schleife über Modell- und MCP-Durchgang des Kerns,
  angehängte Thinking-Historie, Cache-Marken und sichere Werkzeugprojektion.
- Lokale Schritt-/Aufrufgrenzen, Context-Wandzeit und Abbruch, Meldung 300 s.
- Feste Fehlertexte und inhaltfreies Lauf-Protokoll.
- Sieben unveränderte Kern-Goldens sowie separate Vorschläge für Fehlerenden.
- Internes Netz, read-only, distroless/static als Non-Root.
- Fake-Kern-Tests, Negativ- und Mutationsproben; Prüfbericht im Gate-Bericht auf JanuaPort/januaport#1099.

Kein veröffentlichtes Image, kein Live-Modellaufruf, kein Katalogeintrag.
Nur Anthropic: Ein Profil auf einer openai-kompatiblen Modell-Anbindung scheitert in 0.1.0 (der Kern weist die Anfrage ab). OpenAI-Adapter, Zeitplanung und GUI sind nicht Bestandteil von B1.

### B1.1 — Release-Strecke und SEC R4 H1–H5 (JanuaPort/januaport#1099)

- Release-Strecke nur für `agent-runner-vX.Y.Z`: vet und Race-Tests → ein
  Build `linux/amd64` mit SBOM/Provenance per Digest nach GHCR →
  Config-Digest-Vergleich und PowerShell-Sonde → Image-Tag `X.Y.Z` auf
  denselben Digest. Trivy-Bericht als Artefakt, kein Gate. SHA-Pins und
  Schreibrechte nach dem script-runner-Vorbild (SEC C1/C2), Hygiene-Tests.
- H1: Eigener Adaptertest sichert `cancel` vor `session.Close()` auch bei
  Erfolg ab; eine vertauschte Reihenfolge wird ohne Fristablauf erkannt.
- H2: Eigener benannter Test für den 4-MiB-Antwortdeckel auf Modell- und
  MCP-Pfad; der Größenfehler wird beim Lesen ohne Lauf-Frist verlangt.
- H3: Fester Satz für `rejected`: „Der Modell-Durchgang hat die Anfrage des
  Agenten-Läufers abgewiesen.“ Separater Vertragsvorschlag und Tests angepasst;
  die Kern-Goldens bleiben unverändert.
- H4: Bilder nur base64 mit `image/png`, `image/jpeg`, `image/gif` oder
  `image/webp`. Andere Medientypen werden in MCP-Werkzeugergebnissen zum
  festen Platzhalter wie URL-Bilder; dieselbe Prüfung schützt Versand und
  Modellantworten. Tabellentest am echten HTTP-/MCP-Adapter.
- H5: Temporärer Sonden-Zustand `probe/.smoke/` in `.gitignore`.

Getaggt als `agent-runner-v0.1.0` (Release-Lauf 38983345807, 17.10.2026).

### Kuratierung

### v0.1.2 — 2026-10-23

| Feld | Wert |
|---|---|
| Image | `ghcr.io/januaport/agent-runner` |
| Tag | `agent-runner-v0.1.2` (Image-Tag `0.1.2`) |
| Digest (OCI-Index) | `sha256:f5cb7c700943e1dc764a916b50856e2f41eb5245fb4a3f218729ca4140e900cc` |
| Plattform | `linux/amd64` (`sha256:4553e83e5fe02fe97d4519a917b1a703ee1395bef2a3b6401874070cd16be3ed`), sonst keine |
| Art | `service`, eigener Build (Plugins-Repo) |
| Geprüft mit | JanuaPort v0.82.0, live auf dem Showcase (Lauf `end=ok`, 5,1 s, 3 Werkzeugaufrufe) |
| Benötigter Commit von Compose | `19ea160` (`docker-compose.yml`, unverändert seit 0.1.0) |
| Lauf der Sonden | Release-Lauf `39945504947`, hat genau diesen Digest geprüft (SEC C1) |

**Änderung gegenüber 0.1.1:** nur Go 1.26.10 (Build-Image per Digest gepinnt, derselbe Pin wie in
`deploy/keycloak-probe`). Das Verhalten ist unverändert.

**Scan:** Trivy, Scan vom 2026-10-23T11:23Z: **0 critical · 0 high** · 2 unknown. Die beiden Funde
sind dieselben wie in 0.1.1: CVE-2026-39824 in `golang.org/x/sys/windows`, im `linux/amd64`-Binary
nicht gebaut, und DSA-6572-1 in `ca-certificates` der Basis, vom Läufer nicht benutzt.
CVE-2026-111943 ist mit Go 1.26.10 behoben.

**Entscheid:** löst 0.1.1 auf dem Showcase ab (per Digest, Sicherung `.env.vor-0.1.2`). Katalogfähig
nach dem Sub-Agent-Beleg (#1099). Den Eintrag setzt VENDOR mit PO-Go.

### v0.1.1 — 2026-10-23

| Feld | Wert |
|---|---|
| Image | `ghcr.io/januaport/agent-runner` |
| Tag | `agent-runner-v0.1.1` (Image-Tag `0.1.1`) |
| Digest (OCI-Index) | `sha256:2918dec223505557438f46f1032e46e4a7a5b4542307476f278b7825897c271d` |
| Plattform | `linux/amd64` (`sha256:f8ab1c6f10e371643d4dc80ba80026aeb96eeedba3252e822ee2947d617a5325`), sonst keine |
| Art | `service`, eigener Build (Plugins-Repo) |
| Geprüft mit | JanuaPort v0.82.0, **live auf dem Showcase** (Durchstich #1099, 23.10.2026) |
| Benötigter Commit von Compose | `19ea160` (`docker-compose.yml`, unverändert seit 0.1.0) |
| Lauf der Sonden | Release-Lauf `39943955357`, hat genau diesen Digest geprüft (SEC C1) |

**Digest:** aus `digest.txt` im Artefakt des Release-Laufs und aus der Zusammenfassung „Tag 0.1.1 → …“
desselben Laufs; beide stimmen überein. Auf dem Showcase per Digest gezogen und gestartet
(Container healthy, nur `jnpt_plugins`, kein Host-Port).

**Live-Prüfung (Showcase, Anthropic, Profil `demo`):** Lauf mit einem Token von außen: `end=ok`,
8,1 s, 2 Schritte, 3 Werkzeugaufrufe durch den Kern, Audit nur mit Metadaten. Entzug mitten im
Lauf: Lauf etwa 1 s nach dem Widerruf beendet, kein innerer Aufruf danach, Aufrufer bekommt die
Absage ohne Orakel. Der Sub-Agent aus claude.ai steht noch aus (#1099).

**Scan:** Trivy (Artefakt des Laufs), Scan vom 2026-10-23T11:09Z, Scanner `vuln`:
**0 critical · 1 high** · 2 unknown.
- HIGH CVE-2026-111943 (Go-stdlib `crypto/x509`, Go 1.26.9, behoben in 1.26.10): Denylist für
  zurückgezogene Zwischenzertifikate. Im Läufer nicht erreichbar, weil er keine TLS-Verbindung
  aufbaut: Er spricht nur unverschlüsseltes HTTP mit dem Kern im internen Netz `jnpt_plugins` und
  hat keinen Internetzugang. Der Kern steht seit #1136 auf Go 1.26.10. Nachzug: Toolchain-Sprung
  des Läufers auf Go 1.26.10 als 0.1.2, **vor dem Katalogeintrag**.
- unknown CVE-2026-39824 (`golang.org/x/sys/windows`, im `linux/amd64`-Binary nicht gebaut) und
  DSA-6572-1 (`ca-certificates` der Basis, vom Läufer nicht benutzt).

**Entscheid:** auf dem Showcase im Betrieb. Noch nicht katalogisiert: Erst kommt 0.1.2 mit Go 1.26.10,
dann der Sub-Agent-Beleg, dann der Katalogeintrag (VENDOR, mit PO-Go).

### v0.1.0 — 2026-10-17

| Feld | Wert |
|---|---|
| Image | `ghcr.io/januaport/agent-runner` |
| Tag | `agent-runner-v0.1.0` (Image-Tag `0.1.0`) |
| Digest (OCI-Index) | `sha256:c9357185a4ea988fcbe1ae31f1bded4f6218388d04cd5c2905897803b0f993bd` |
| Plattform | `linux/amd64` (`sha256:577077565cdb6cee0437bd537eb0c3cf4aa31b6b0f6bb1b37d5912c9725f38d4`), sonst keine |
| Art | `service`, eigener Build (Plugins-Repo) |
| Geprüft mit | JanuaPort v0.80.0, **Live-Prüfung steht aus** (Durchstich #1099) |
| Benötigter Commit von Compose | `19ea160` (`docker-compose.yml`) |
| Lauf der Sonden | Release-Lauf `38983345807` — hat genau diesen Digest geprüft (SEC C1) |

**Digest** zweimal gemessen am 2026-10-17: Zusammenfassung des Release-Laufs
(`digest.txt` im Artefakt) und `docker buildx imagetools inspect`. Beide stimmen
überein. SBOM und Provenance hängen als Attestation am Index.

**Sonden:** grün im Lauf `38983345807` gegen genau diesen Digest: UID 65532,
read-only Root-FS, Gesundheitsprüfung ok, genau ein internes Netz, kein Host-Port,
Testserver im fremden Netz unerreichbar (erreichbare Positivkontrolle).

**Scan:** Trivy 0.74.0 (Artefakt des Laufs), Scan vom 2026-10-16T23:50Z, Scanner
`vuln`: **0 critical · 0 high** · 0 medium · 0 low · 1 unknown. Der eine Fund
(CVE-2026-39824, `golang.org/x/sys` v0.41.0, behoben in 0.44.0) betrifft
`golang.org/x/sys/windows`. Das Paket ist im `linux/amd64`-Binary nicht gebaut,
Nachzug mit dem nächsten Abhängigkeits-Update.

**Inhalt:** distroless/static:nonroot, statisches Go-Binary `agent-runner`
(Go 1.26.9).

**Isolation:** UID/GID 65532, read-only Root-FS, `cap_drop: ALL`,
no-new-privileges, Schlüsselordner nur lesend, ausschließlich internes Netz.

**Grenzen v1:** nur Anthropic Messages, nur `linux/amd64`, kein direkter
Modellanbieter-Zugang. Ein Image, das eine neue Compose braucht, ist ein
Wartungsschritt mit Runbook-Eintrag, kein stilles Katalog-Update.

**Entscheid:** noch nicht katalogisiert, weil die Live-Prüfung gegen JanuaPort
v0.80.0 aussteht (Durchstich mit Anthropic). Danach wird „Geprüft mit“ bestätigt
und der Katalogeintrag folgt.

Quelle: JanuaPort/januaport#1099.
