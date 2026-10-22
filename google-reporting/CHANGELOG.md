# Änderungen — Google Reporting

## 1.0.0 — 2026-10-22

**Erste Release-Fassung.** Sie umfasst JanuaPort/januaport#1130 (Berichte),
#1148 (Schreibhandgriffe) und #1150 (Statusprüfung, spätes `end_date`). Die Release-Strecke
(JanuaPort/januaport#1138) ist `.github/workflows/google-reporting-release.yml`: Tag
`google-reporting-vX.Y.Z` → Hash-Lock, Unit-Tests und Mutationsproben → ein Build `linux/amd64`
mit SBOM/Provenance per Digest nach `ghcr.io/januaport/google-reporting` → Config-Digest-Vergleich
(SEC C1) und Sonden gegen genau diesen Digest → Image-Tag `X.Y.Z` auf denselben Digest. Trivy-Bericht
als Artefakt, kein Gate. SHA-Pins und Schreibrechte nach dem agent-runner-/script-runner-Vorbild
(SEC C2), Wächter in `tests/test_release_workflow.py`. Kein Katalogeintrag (VENDOR, nach Live-Beleg).

- JanuaPort/januaport#1150: GA4 `get_status` meldete auf echten Properties immer `unavailable`.
  `checkCompatibility` antwortet für alle Dimensionen und Metriken der Property; die Prüfung
  verglich die Länge mit den angefragten. Jetzt zählen nur die angefragten `apiName`s: jeder
  muss vorkommen und `COMPATIBLE` sein (`report_definition_unverified` bzw. `_incompatible`
  sonst). Ein wohlgeformtes, aber zu spätes `end_date` endet mit dem festen Code
  `end_date_not_available` (400) statt pauschal `invalid_input`; kein Echo, kein Klemmen.
  Tabellentests, REST-Test mit Ganz-Property-Antwort, drei neue Mutanten.
- JanuaPort/januaport#1148: drei Schreibhandgriffe für das Inbound-ATAS-Team: GA4-Annotation
  (anlegen, ändern, auflisten), GSC-Sitemap (einreichen, auflisten, lesen), GA4-Kanalgruppe
  „KI-Assistenten“ (anlegen, ändern, auflisten), dazu die URL-Inspektion (lesend). Feste Routen
  je Operation (`/v1/gsc/…`, `/v1/ga4/…`, kein `delete`), Site und Property nur aus der
  Betreiberkonfiguration, je Schreibscope (`webmasters`, `analytics.edit`) ein eigenes
  Credential-Objekt, `dry_run` mit `before`/`after`, Tagesdeckel 20 Schreibversuche je Quelle
  im Serverprozess, Änderungszeile im Log (Freitext nur als Länge und Hash), Titelpräfix `[JP]`,
  Privacy-Scan der Annotationstexte, Kanalgruppe nie `primary` und mit den Standardkanälen.
  Neuer optionaler Konfigurationswert `ga4_channel_group_name`. Keine neue Abhängigkeit: Admin
  API v1alpha über den vorhandenen, gepinnten Discovery-Client. SETUP.md: GSC „Uneingeschränkt“,
  GA4 „Bearbeiter“, dritte API. Ändert die Log-Invariante „Route/Code/Dauer“ um die
  Änderungszeile bei angewendeten Schreibaufrufen. Gebaut/synthetisch geprüft; Live-Abnahme
  (Staging, dann mit PO-Go) offen.
- JanuaPort/januaport#1137: Einrichtungsanleitung für den Admin in `SETUP.md` (Google-Seite mit
  minimalen Rechten nach SEC V1, Eintrag des Servicekonto-JSON, Nutzer-Scopes, Prüfung,
  Schlüsselwechsel). Sie geht als `setup_guide` in den signierten Plugin-Katalog; das README
  verweist darauf. `tests/test_setup_guide.py` prüft 32 KiB, UTF-8 und den V1-Inhalt.
- #1130: privater Python-REST-Beisteller mit acht festen GSC-/GA4-Berichten,
  getrennten Readonly-Scopes, öffentlichen Seitenfiltern,
  killbaren begrenzten Prozessen und flüchtigen Token-Caches.
- Gepinnte offizielle Clients und Hash-Lock, abgesicherte Container-/Hostvorlage,
  Runbook sowie synthetische HTTP-/SDK-/Abbruch-/Mutationstests.
- Query-Freitext standardmäßig gesperrt (Betreiberschalter). Auditmetadaten, minimale Grants,
  GA4-Mess-/Consentvertrag, Zielhost-Netz und Live-Durchstich bleiben Freigabegates.
  Keine Veröffentlichung, Katalogsignatur oder produktive Abnahme in diesem Paket.
- #1130 B1 (SEC-Zweitprüfung): Secret-Ablage nach Hausmuster #887. Ein Bridge-Key für
  beide Quellen (Stelle `plugin-google-reporting`, Datei `runtime-key`) statt zwei
  getrennter Keys; `bridge_keys_must_differ` und die Quellentrennung im Bridge-Auth
  entfallen. Der Bridge-Key ist ein Zufallswert und wird per Betreiber-CLI (stdin) in Stelle und
  Connector-Credential `google-reporting` gesetzt. Das Servicekonto-JSON kommt aus der
  Stelle `plugin-google-reporting-google` (Admin-GUI, Basis-Einrichtung). Compose mountet zwei Ordner read-only
  (`/run/secrets/jnpt`, `/run/secrets/google`) statt dreier Datei-Mounts; Deskriptor-
  REQUIRE, tmpfiles-Beispiel und ein eigenes Unit-Paar für Neustart bei Schlüsselwechsel
  und nach Boot sind neu. `load_identity` akzeptiert das JSON auch ohne Zeilenumbrüche
  (GUI-Passwortfeld). Entfernen ist kein Widerruf: Der Google-Schlüssel gilt bis zum
  Löschen in der Cloud Console. Alt: `google-service-account.json`, `gsc-bridge-key`,
  `ga4-bridge-key` (nie veröffentlicht).

Die folgende Vorlage ist kein Release-Nachweis; GATE trägt die Werte nach dem grünen Release-Lauf
und vor dem Katalogeintrag ein, nach dem Muster von agent-runner und script-runner.

### Kuratierung

```markdown
### v1.0.0 — <Datum>

| Feld | Wert |
|---|---|
| Image | `ghcr.io/januaport/google-reporting` |
| Tag | `google-reporting-v1.0.0` (Image-Tag `1.0.0`) |
| Digest (OCI-Index) | `sha256:<nach Release ergänzen>` |
| Plattform | `linux/amd64` (`sha256:<nach Release ergänzen>`), sonst keine |
| Art | `service`, eigener Build (Plugins-Repo) |
| Geprüft mit | <JanuaPort-Fassung nach Live-Beleg bestätigen> |
| Benötigter Commit von Compose | `<Commit im Plugins-Repo>` (`docker-compose.yml`) |
| Lauf der Sonden | Release-Lauf `<Lauf-ID>` — hat genau diesen Digest geprüft (SEC C1) |

**Digest** zweimal gemessen am <Datum>: Zusammenfassung des Release-Laufs (`digest.txt` im
Artefakt) und `docker buildx imagetools inspect`. Beide stimmen überein. SBOM und Provenance
hängen als Attestation am Index.

**Sonden:** grün im Lauf `<Lauf-ID>` gegen genau diesen Digest: Benutzer 65532:65532,
Unit-Tests im Container ohne Netz, read-only, alle Capabilities entfernt; Start ohne
Konfiguration endet mit Exit 1 und genau `startup_failed`. Der Config-Digest des geprüften
Images ist gleich dem der amd64-Plattform des gepushten.

**Scan:** Trivy <Fassung> (Artefakt des Laufs), Datenbank vom <Datum>, Scanner `vuln`:
**<n> critical · <n> high** · <n> medium · <n> low · <n> unknown. Hohe Funde einzeln, je mit
behobener Fassung oder Begründung (Python-Pakete aus dem Hash-Lock, Debian-Basis).

**Inhalt:** `python:3.12-slim` (im Dockerfile per Digest gepinnt), Pakete nur aus
`requirements.lock` mit Hashes, Dienst `google_reporting`, Benutzer 65532.

**Isolation:** UID/GID 65532, read-only Root-FS, `cap_drop: ALL`, no-new-privileges,
Schlüsselordner nur lesend, internes Plugin-Netz plus eigenes Egress-Netz (Zielhost-Firewall
beim Betreiber, Compose erzwingt keine Domain-Allowlist).

**Grenzen v1:** nur `linux/amd64`; Live-Beleg der Berichte und Schreibhandgriffe steht aus
(README, Live-Gates). Ein Image, das eine neue Compose braucht, ist ein Wartungsschritt mit
Runbook-Eintrag, kein stilles Katalog-Update.

**Entscheid:** <katalogisiert / nicht katalogisiert>, weil <Begründung>.

Quelle: JanuaPort/januaport#1138, #1130, #1148, #1150.
```
