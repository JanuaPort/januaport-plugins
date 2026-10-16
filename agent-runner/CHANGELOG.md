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

Version bleibt 0.1.0, noch nicht getaggt. Die folgende Vorlage ist kein
Release-Nachweis; das Gate trägt die Werte nach dem grünen Release-Lauf
und vor dem Katalogeintrag ein, nach dem Muster des script-runners.

### Kuratierung

```markdown
### v0.1.0 — <Datum>

| Feld | Wert |
|---|---|
| Image | `ghcr.io/januaport/agent-runner` |
| Tag | `agent-runner-v0.1.0` (Image-Tag `0.1.0`) |
| Digest (OCI-Index) | `sha256:<nach Release ergänzen>` |
| Plattform | `linux/amd64` (`sha256:<nach Release ergänzen>`), sonst keine |
| Art | `service`, eigener Build (Plugins-Repo) |
| Geprüft mit | <JanuaPort v0.80.0 nach Live-Prüfung bestätigen> |
| Benötigter Commit von Compose | `<Commit im Plugins-Repo>` (`docker-compose.yml`) |
| Lauf der Sonden | Release-Lauf `<Lauf-ID>` — hat genau diesen Digest geprüft (SEC C1) |

**Digest** zweimal gemessen am <Datum>: Zusammenfassung des Release-Laufs und
`docker buildx imagetools inspect`. Beide stimmen überein. SBOM und Provenance
hängen als Attestation am Index.

**Sonden:** grün im Lauf `<Lauf-ID>` gegen genau diesen Digest: UID 65532,
read-only Root-FS, `/healthz` ok, genau ein internes Netz, kein Host-Port,
Testserver im fremden Netz unerreichbar (erreichbare Positivkontrolle).
Der Config-Digest des geprüften Images ist gleich dem der amd64-Plattform
des gepushten.

**Scan:** Trivy <Fassung> (Artefakt des Laufs), Datenbank vom <Datum>, Scanner
`vuln`: **<n> critical · <n> high** · <n> medium · <n> low · <n> unknown.
Hohe Funde einzeln, je mit behobener Fassung oder Begründung.

**Inhalt:** distroless/static:nonroot, statisches Go-Binary `agent-runner`
(Go <Fassung>).

**Isolation:** UID/GID 65532, read-only Root-FS, `cap_drop: ALL`,
no-new-privileges, Schlüsselordner nur lesend, ausschließlich internes Netz.

**Grenzen v1:** nur Anthropic Messages, nur `linux/amd64`, kein direkter
Modellanbieter-Zugang. Ein Image, das eine neue Compose braucht, ist ein
Wartungsschritt mit Runbook-Eintrag, kein stilles Katalog-Update.

**Entscheid:** <katalogisiert / nicht katalogisiert>, weil <Begründung>.

Quelle: JanuaPort/januaport#1099.
```
