# Changelog

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
