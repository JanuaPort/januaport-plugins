# Bing Webmaster 0.1.0 — #1292

Stufe 1 „Sicher verbinden“. Gebaut und synthetisch geprüft; **noch nicht live abgenommen oder
produktiv provisioniert**. Fassung 0.1.0; den Tag `bing-webmaster-vX.Y.Z` setzt GATE über den
Release-Workflow. Apache 2.0.
Ein privater Python-Beisteller übersetzt sechs feste Routen in Aufrufe der Bing Webmaster API
(JSON-Endpunkt) und in IndexNow. JanuaPort behält Rechte und Audit.

**Warum ein Beisteller:** Die Bing Webmaster API nimmt den API-Schlüssel nur als `?apikey=` in
der URL an. Die Connector-Specs des Gateways setzen Credentials nur in Header. Der Beisteller hält
den Bing-Schlüssel, das Gateway spricht ihn mit einem Bridge-Bearer an. Der Schlüssel steht damit
nur in der ausgehenden TLS-Verbindung zu `ssl.bing.com`, nie im Gateway, im Audit oder im Log.

Keine Seitenstatistik und keine Suchbegriffe (dafür liefert `google-reporting` Google-Zahlen),
kein Crawl, keine Konto- oder Seitensuche, kein Entfernen oder Blockieren von Adressen. AI
Performance in Bing Webmaster Tools hat keine API.

## Vertrag

`Authorization: Bearer <Bridge-Key>` auf allen Routen unter `/v1/`. Die Rechte je Werkzeug prüft das
Gateway, nicht der Dienst.

| Route | Methode | Bing-/IndexNow-Aufruf | Eingabe |
|---|---|---|---|
| `bing/url_info` | GET | `GetUrlInfo` | Query `url` |
| `bing/sitemaps` | GET | `GetFeeds` | keine |
| `bing/submission_quota` | GET | `GetUrlSubmissionQuota` | keine |
| `bing/submit_urls` | POST | `GetUrlSubmissionQuota`, dann `SubmitUrlBatch` | `urls`, `dry_run` |
| `bing/submit_sitemap` | POST | `SubmitFeed` | `sitemap_url`, `dry_run` |
| `indexnow/submit_urls` | POST | `POST https://api.indexnow.org/indexnow` | `urls`, `dry_run` |

- **Eingaben:** Body ein JSON-Objekt, höchstens 8 KiB, doppelte und unbekannte Schlüssel gleich
  400. `urls` ist **ein** String, die Adressen durch Komma getrennt (höchstens 100 je Aufruf,
  Wiederholungen fallen weg); `dry_run` ein JSON-Boolean (Vorgabe `false`).
- **Adressen** (Seiten, Sitemap, `url`): nur `https`, Host exakt aus `hosts` der Konfiguration
  (kein Suffix-Vergleich, Kleinbuchstaben), keine Zugangsdaten, kein Port, kein Fragment, keine
  Query, kein `@`, `:`, `,`, Backslash, Leerzeichen oder Nicht-ASCII, kein `..` (auch
  prozentkodiert), keine doppelten Schrägstriche, höchstens 512 Zeichen. Eine Sitemap endet auf
  `.xml`. Bei IndexNow tragen alle Adressen den Host der Schlüsseldatei. Die Prüfung gilt auch
  für `url_info` und läuft **vor** jedem Herstelleraufruf.
- **Site:** `siteUrl`, die Host-Allowlist, der IndexNow-Schlüssel und `keyLocation` kommen nur aus
  `config.json`, nie vom Aufrufer.
- **Antworten:** `schema_version` 1.0, `source` (`bing`/`indexnow`), `operation`, `state`, Felder,
  `fetched_at`. Ausgegeben wird eine **feste Feldliste**; weitere Herstellerfelder werden
  verworfen, die Adresse in `url_info` ist die geprüfte Eingabe, nicht das Herstellerfeld.
  - `url_info`: `item` mit `url`, `discovery_date`, `last_crawled_date` (ISO UTC; Bings
    `/Date(ms±hhmm)/` wird gelesen, Werte bis zur Epoche sind `null`), `http_status`,
    `document_size`, `is_page`; `state` `ok` oder `no_data`.
  - `sitemaps`: `items` (`url`, `type`, `status`, `url_count`, `file_size`, `compressed`,
    `submitted`, `last_crawled`), höchstens 100, `quality.truncated`; `state` `ok` oder `no_rows`.
  - `submission_quota`: `item` mit `daily_quota`, `monthly_quota`, wie von Bing gemeldet.
  - Schreibrouten: immer dieselben Felder `dry_run`, `target`, `submitted_count`, `urls`,
    `quota_before` (nur `bing/submit_urls`), `vendor_status` (nur IndexNow: 200 oder 202);
    `state` ist `applied` oder `preview`. IndexNow antwortet 202, solange die Prüfung der
    Schlüsseldatei aussteht; das ist ein Erfolg.
- **Vorschau:** `dry_run` validiert und antwortet `preview`. Es ruft weder Bing noch IndexNow auf
  und zählt nicht gegen den Tagesdeckel.
- **Quote:** `bing/submit_urls` liest vor dem Batch die gemeldete Tagesquote und reicht nie mehr
  Adressen ein, als sie erlaubt (`source_quota_exceeded`, 429, ohne Batch-Aufruf). Bing nennt
  500 Adressen je Batch als Grenze; hier gilt 100.
- **Tagesdeckel:** höchstens 20 Versuche je Schreibroute und UTC-Tag (`write_cap_per_day` in der
  Konfiguration kann nur nach unten abweichen). Der Zähler liegt im Serverprozess, zählt den
  Versuch nach der Validierung und vor dem Herstelleraufruf, also auch Herstellerfehler und
  Quoten-Abweisung, nicht aber `dry_run` oder abgewiesene Eingaben. Der 21. Versuch gibt 429
  `write_cap_reached`. Ein Neustart setzt den Zähler auf null (dokumentierte Grenze). Keine Retries.
- **IndexNow nicht konfiguriert:** `not_configured` (503), kein Aufruf, auch bei `dry_run`.

Fehler: nur `{"error": code}`. Eigene Codes: 400 `invalid_input`, `too_many_urls`,
`request_too_large`, `unexpected_body`; 401 `bridge_unauthorized`; 403 `bridge_forbidden`; 404
`route_not_found`; 405 `method_not_allowed`; 429 `write_cap_reached`, `source_quota_exceeded`;
503 `not_configured`, `internal_error`; 504 `budget_exceeded`. Herstellerfehler (immer 502):
`bing_api_key_invalid` (HTTP 400 mit ErrorCode 3), `indexnow_key_invalid` (403),
`indexnow_url_rejected` (422), `source_rejected` (übrige 4xx), `source_quota_limited` (429),
`source_redirect` (3xx, nicht verfolgt), `source_unavailable` (5xx, Verbindungsfehler, Abbruch),
`unexpected_source_shape` (Antwort nicht in der erwarteten Form), `response_too_large`. Die
Fehlermeldung und der Body des Herstellers werden nie weitergereicht.

## Der Schlüssel verlässt den Dienst nicht (SEC A1)

Bibliotheken schreiben die volle URL samt `?apikey=` in ihre Ausnahmetexte, und ein Fehler-Body kann
sie zurückspiegeln. Deshalb gilt strukturell:

- Ausnahmen werden im `except` nur einem festen Code zugeordnet und **danach** geworfen; es bleibt
  weder `__cause__` noch `__context__` der Bibliotheksausnahme erreichbar. Weder `str(exc)` noch
  `repr` noch der Body einer Fehlerantwort gelangt in Log oder Antwort.
- Das Log enthält je Aufruf genau `route=… code=… duration_ms=…`. Keine URL, keine Adressen, keine
  Änderungszeile (das Audit liegt im Gateway). Die Bibliotheks-Logger (`aiohttp`, `asyncio`,
  `urllib3`) stehen auf `CRITICAL`; HTTP-Parserfehler laufen über die gepinnte Naht `SafeAppRunner`.
- Keine Umleitungen (`allow_redirects=False`), `trust_env=False` (kein Proxy aus der Umgebung),
  TLS an, kein Cookie-Speicher, keine Retries, Antwortgrenze 256 KiB je Herstellerantwort.
- Der Dienst erzeugt den Schlüssel-Zugriff nur aus der Datei `/run/secrets/bing/runtime-key`
  (16–128 Zeichen aus `[A-Za-z0-9_-]`, nicht gleich dem Bridge-Key); ein Fehler beim Laden nennt den
  Wert nie.
- Tests: `tests/test_adapter.py::KeyNeverInExceptions` (Ausnahmen mit URL im Text),
  `tests/test_http.py::KeyCanary` (Erfolg, Bing-400, 500 mit gespiegelter URL, 3xx, HTML,
  Timeout, Verbindungsfehler) mit einem lauten synthetischen Schlüssel.

## Konfiguration

`config.example.json` ist vollständig synthetisch. Pflicht: `site_url` (`https://<Host>/`, so wie in
Bing Webmaster Tools eingetragen) und `hosts` (1–5 exakte Kleinschreib-Hostnamen; `site_url` muss
dazugehören). Optional `write_cap_per_day` (1–20, Vorgabe 20) und `indexnow` mit `key`
(8–128 Zeichen `[a-zA-Z0-9-]`) und `key_location` (`https://<Host>/<key>.txt`, Host aus `hosts`).
Unbekannte Felder und jede Abweichung lassen den Start fehlschlagen (`startup_failed`).
Der IndexNow-Schlüssel ist per Definition öffentlich (er liegt als Datei im Root der Website); er
steht deshalb hier und nicht im Tresor. Wird er getauscht, ist er an allen Stellen gleichzeitig zu
tauschen, die mit demselben Schlüssel melden.

**Audit-Betriebsbedingung: metadaten für SSO UND Token.** Alle deklarierten Parameter der
Produktvorlagen stehen in `redact_params`. Bei abfragen/antworten speichert das Gateway bewusst
deklarierte Inhalte trotz dieser Liste; die Einstellung gilt je Zugangsklasse, nicht je
Integration. Keine Datenbank, kein Cache, keine geplanten Abfragen.

## Laufzeitgrenzen

512 KiB JSON vor Ausgabe, 8 KiB URL/Query und Body, Pfad 512 Zeichen. **20 Sekunden**
Gesamtbudget je Aufruf (`bing/submit_urls` mit Quotenlesung und Batch inklusive), 10 Sekunden je
Herstellerverbindung; Connector-Timeout **25 s**. Ein Prozess, kein Worker: Der Dienst hat kein
Google-SDK und keine Token. Client-Abbruch bricht den laufenden Herstelleraufruf ab.

## Einrichtung für den Admin

Die Schritte für den Admin (Bing-Seite, Eintrag des Schlüssels, IndexNow, Scopes des Nutzers,
Prüfung, Schlüsselwechsel) stehen in [SETUP.md](SETUP.md). Die Datei wird je Plugin-Version als
`setup_guide` in den signierten Plugin-Katalog übernommen (JanuaPort/januaport#1137). Höchstens
32 KiB, gültiges UTF-8, keine relativen Links; `tests/test_setup_guide.py` prüft das.

## Einrichtung, Betrieb und Rückbau

1. Bing Webmaster Tools: Website verifizieren, API-Schlüssel erzeugen (SETUP.md Abschnitt 1).
2. Zwei Stellen in der Basis-Einrichtung (das Gateway legt sie an, sobald die Ordner aus Schritt 3
   existieren), mit unterschiedlichen Wegen:

   | Stelle | Ordner auf der Anlage | Inhalt von `runtime-key` |
   |---|---|---|
   | `plugin-bing-webmaster` | `/run/jnpt/plugins/bing-webmaster/` | der **Bridge-Key**, ein Zufallswert; derselbe Wert steht in der Connector-Credential `bing-webmaster`. Setzt der Betreiber per CLI (siehe unten) |
   | `plugin-bing-webmaster-bing` | `/run/jnpt/plugins/bing-webmaster-bing/` | der Bing-API-Schlüssel. Trägt der Admin/PO in der Admin-GUI unter Basis-Einrichtung ein |

   **Der Bridge-Key ist ein Zufallswert, den kein Mensch kennen muss.** Der Betreiber erzeugt ihn
   auf der Anlage und setzt ihn über stdin per Betreiber-CLI (`jnpt credential set`, Wert nie als
   Argument) in die Stelle **und** mit demselben Wert in die Connector-Credential. Danach wird der
   Wert verworfen:

   ```bash
   K=$(openssl rand -hex 32)            # 64 Zeichen, erfüllt 32–256 druckbare ASCII
   printf %s "$K" | jnpt credential set --name plugin-bing-webmaster
   printf %s "$K" | jnpt credential set --name bing-webmaster
   unset K
   ```

   Nur den Bing-Schlüssel trägt der Admin/PO selbst in die Admin-GUI ein (ein menschlicher
   Geheimnisweg, kein Admin-MCP).
3. Auf der Anlage: `config.json` unter `/opt/jnpt/plugins/bing-webmaster/` (kein Secret). Die zwei
   Ordner unter `/run/jnpt/plugins/` legt systemd-tmpfiles an
   (`deploy/jnpt-plugins-bing-webmaster.conf.example`, zwei `d`-Zeilen `0750 65532 65532`); das
   Gateway schreibt `runtime-key` hinein, nach jedem Boot neu. Compose/.env und Deskriptor aus
   `deploy/` installieren. Der Deskriptor-Eintrag `REQUIRE` nennt `config.json` und beide
   `runtime-key`; fehlt eine Datei, bucht der Aktuator `unprovisioned`. Compose hängt die
   **Ordner** read-only ein (`…/bing-webmaster` → `/run/secrets/jnpt`, `…/bing-webmaster-bing` →
   `/run/secrets/bing`), damit ein atomar ersetzter Schlüssel ankommt.
   **Neustart bei Schlüsselwechsel und nach dem Boot:** der Dienst liest beide Dateien nur beim
   Start. Das Unit-Paar `deploy/jnpt-bing-webmaster-key.path` (zwei `PathChanged=`) und `.service`
   (`docker compose … up -d --force-recreate`) nach `/etc/systemd/system/` legen und **nur die
   Path-Unit** aktivieren: `systemctl daemon-reload && systemctl enable --now
   jnpt-bing-webmaster-key.path`. Die Vorlage `jnpt-plugin-key@` des Produkts genügt hier nicht
   (Lehre #1130): sie beobachtet nur eine Stelle und startet `/opt/jnpt/plugins/%i/docker-compose.yml`;
   für `bing-webmaster-bing` gibt es keine Compose, ein zweiter Ordner braucht ein eigenes
   path/service-Paar. Das Image `ghcr.io/januaport/bing-webmaster` entsteht nur aus dem Tag
   `bing-webmaster-vX.Y.Z`; Tag und signierter Katalogeintrag liegen bei GATE/VENDOR.
4. **Netz vor Aktivierung nachweisen:** bestehendes `jnpt_plugins` muss internal=true und passende
   IPAM besitzen. Betreiber reserviert eine freie IPv4 in diesem Netz als `BING_WEBMASTER_PLUGIN_IP`.
   Der Listener bindet ausschließlich diese IP (Port 8092), niemals 0.0.0.0 oder Loopback. Eigenes
   Compose-Egress-Netz ohne andere Workloads; Host-Firewall/DNS-/Egress-Policy auf genau zwei
   Ziele: `ssl.bing.com:443` und `api.indexnow.org:443`. Compose allein setzt keine
   Domain-Allowlist durch. Kein TLS-/Proxy-Bypass. Ohne diesen Nachweis ist der Host nicht fertig
   provisioniert.
5. Signed Image-Digest einsetzen. Non-root 65532, RootFS read-only, 512 MiB/1 CPU, Capabilities
   entfernt, minimale tmpfs, kein Host-Port/Docker-Socket/Vault-Mount, keine Reverse-Proxy-Route.
   Lokal `/healthz` prüft nur Startkonfiguration/Dienst, keine Herstellerprobe.
6. Die Connector-Credential `bing-webmaster` im JanuaPort-Vault ist schon aus Schritt 2 gesetzt.
   Bytegleiche Vorlagen `bing-webmaster` und `bing-webmaster-manage` (je 1.0.0) aus dem Produkt als
   **corp**-Instanzen mit `http://bing-webmaster:8092` erstellen. Die Schreibwerkzeuge bekommt nur
   der vorgesehene Zugang, je Werkzeug einzeln (tool-genaues Recht).
7. Auditbedingung prüfen; `submission_quota`, Negativproben, dann `url_info`.

Rotation: Beim Bridge-Key einen neuen Zufallswert wie in Schritt 2 erzeugen und im selben Zug in
`plugin-bing-webmaster` und `bing-webmaster` setzen; beim Bing-Schlüssel den neuen Wert in der
Admin-GUI eintragen (Stelle `plugin-bing-webmaster-bing`). Das Gateway schreibt die Datei, das
Unit-Paar erzeugt den Container neu (≤ 60 s plus Recreate). Danach positive Probe. Den alten
Schlüssel in Bing Webmaster Tools widerrufen. **Benannte Grenze: Entfernen ist kein Widerruf.** Ein
in der GUI entfernter oder ersetzter Bing-Schlüssel bleibt bei Bing gültig, bis er dort widerrufen
ist; ein abgeflossener Bridge-Key bleibt gültig, bis er ersetzt ist.

## Reproduzierbare Prüfung und offene Live-Gates

Python 3.12 (3.14 bricht schon im Vorbild): venv, `pip install --require-hashes -r requirements.lock`,
`python -m unittest discover -s tests -v`, `python tests/mutations.py`. Nur synthetische Daten,
keine echten Schlüssel, keine Aufrufe an Bing oder IndexNow (Loopback-Stub in `tests/support.py`).
Produkt: `go test ./internal/connector/... ./cmd/jnpt/...` prüft Spec, Goldens, MCP- und
Rechtepfade. Optionaler repoübergreifender Wire-Test: in diesem Verzeichnis
`python tests/wire_server.py --port 18093` starten (echter HTTP-Dienst und echter Adapter gegen
einen Loopback-Stub), in der Produkt-Worktree
`TEST_BING_WEBMASTER_URL=http://127.0.0.1:18093 go test ./internal/connector/contracttest -run 'ActualPythonService' -count=1`.
Release: `.github/workflows/bing-webmaster-release.yml` läuft nur auf den Tag `bing-webmaster-vX.Y.Z`
(setzt GATE), Ablauf wie `google-reporting`; den Nachweis je Digest trägt GATE in den Abschnitt
„Kuratierung“ von `CHANGELOG.md` ein.

**Offene Herstellerunsicherheiten (live zu belegen, Team atas-inbound mit dem Bing-Schlüssel des
PO-Kontos):**

- `GetUrlInfo`: Das Doku-Beispiel der JSON-Variante setzt `url` in Anführungszeichen
  (`%22example.com%22`), das XML-Beispiel nicht. Der Dienst sendet die Adresse ohne Anführungszeichen
  (Konstante an einer Stelle). Liefert Bing damit keine Daten, ist das die erste Stelle zum Prüfen.
- `GetUrlSubmissionQuota`: Die Doku nennt `DailyQuota` und `MonthlyQuota` ohne Erklärung. Der Dienst
  behandelt `DailyQuota` als verbleibende Tageskontingente und reicht nie mehr Adressen ein. Ist
  der Wert eine Obergrenze statt eines Rests, bleibt die Prüfung harmlos, schützt aber nicht.
- `ErrorCode` 3 (InvalidApiKey) stammt aus dem Auftrag; die übrigen ErrorCodes werden bewusst
  nicht einzeln ausgewertet.
- Das Format der Datums-Werte (`/Date(ms±hhmm)/`) und die Feldnamen von `Feed` stammen aus den
  Doku-Beispielen; Bings Wert für „nie“ ist unbekannt, Werte bis zur Epoche gelten als `null`.
- IndexNow: Antwortcodes 200/202/400/403/422/429 laut indexnow.org/documentation.

Quellen: Microsoft Learn „bing-webmaster-dotnet“, `IWebmasterApi` (`GetUrlInfo`, `GetFeeds`,
`GetUrlSubmissionQuota`, `SubmitUrlBatch`, `SubmitFeed`, `ApiFault`), geprüft am 02.11.2026;
indexnow.org/documentation.
