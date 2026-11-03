# bing-webmaster 0.1.0 — Modulvertrag (#1292)

Privater Python-REST-Beisteller, Apache2.0. Stufe 1, gebaut/synthetisch geprüft, Live-Abnahme
offen. Kein eigener MCP-Server oder Gateway-Auth-Typ. Grund für den Beisteller: Bing nimmt den
API-Schlüssel nur als `?apikey=` in der URL, die Connector-Specs setzen Credentials nur in Header.

- domain.py: rein (kein Netz, kein Log). `Config.from_dict` (fail-closed, `ValueError` ohne Wert),
  `validate_url` (https, exakter Host aus `hosts`, kein Port/Userinfo/Fragment/Query/`@`/`..`),
  `parse_get`/`parse_post` (Routenmengen, unbekannte und doppelte Schlüssel → 400),
  `parse_bing_date` (`/Date(ms±hhmm)/` → ISO UTC; ≤ Epoche = `None`), Projektionen als
  **Positivliste** (`project_url_info`, `project_feeds`, `project_quota`), `envelope`/`write_envelope`.
- adapter.py: Herstellertransport (aiohttp-Client). `BING_BASE` und `INDEXNOW_URL` sind
  Modulkonstanten; nur Tests übergeben andere Basen. `_send` ordnet jede Ausnahme im `except`
  einem festen Code zu und wirft **nach** dem `except` (kein `__context__`). Lesen von
  `GetUrlInfo`/`GetFeeds`/`GetUrlSubmissionQuota`, Schreiben von `SubmitUrlBatch`/`SubmitFeed`
  (Antwort muss `{"d": null}` sein) und IndexNow. `bing/submit_urls` liest zuerst die Quote.
- server.py: feste Routen `/v1/bing/{url_info,sitemaps,submission_quota,submit_urls,submit_sitemap}`
  und `/v1/indexnow/submit_urls`, Bridge-Bearer (`hmac.compare_digest`), strikte Vorprüfung vor
  jedem Herstelleraufruf, `WriteCap`, Budget 20 s, `/healthz` lokal. Log je Aufruf genau
  `route=… code=… duration_ms=…`, nie mehr.
- safe_http.py: gepinnte aiohttp-Protokollnaht (`SafeAppRunner`), wie `google-reporting`.
  Upgrade verlangt Canary-Proben.

Invarianten (an allen Code-/Dokuorten nachziehen):
- **A1 Der Bing-Schlüssel erscheint nie:** nicht in Log, Antwort, Fehlertext, Exception-Repr,
  Testfixture (nur synthetische Werte). Kein `str(exc)`/`repr(exc)`, kein Herstellerbody in einer
  Meldung, `allow_redirects=False`, `trust_env=False`, keine Retries, Bibliotheks-Logger
  `CRITICAL`, `DummyCookieJar`. Wer `adapter.py` oder die Middleware ändert, fährt
  `KeyNeverInExceptions` und `KeyCanary` und die Mutanten `exception-text-in-code`,
  `redirects-followed`, `proxy-from-environment`.
- **A2 Eingaben:** `siteUrl`, Host-Allowlist, IndexNow-Schlüssel und `keyLocation` nur aus
  `config.json`. Jede Aufrufer-URL (auch `url_info`) durchläuft `validate_url` vor dem ersten
  Herstelleraufruf. Antworten bestehen nur aus der festen Feldliste; Herstellerfelder werden
  verworfen, `url` in `url_info` ist die geprüfte Eingabe.
- **Schreibrouten:** höchstens 100 Adressen je Aufruf, `bing/submit_urls` nie über der gemeldeten
  Tagesquote, Tagesdeckel 20 je Route und UTC-Tag (`write_cap_per_day` nur nach unten), gezählt im
  Serverprozess nach der Validierung und vor dem Herstelleraufruf (Herstellerfehler zählen),
  `dry_run` validiert, ruft nicht auf und zählt nicht, keine Retries. IndexNow ohne Konfiguration:
  `not_configured` (503), kein Aufruf.
- Fehler nur `{"error": code}`. Herstellerfehler sind immer 502 mit festen Codes
  (`bing_api_key_invalid`, `indexnow_key_invalid`, `indexnow_url_rejected`, `source_rejected`,
  `source_quota_limited`, `source_redirect`, `source_unavailable`, `unexpected_source_shape`);
  Timeout ist 504 `budget_exceeded`.
- Egress nur `ssl.bing.com:443` und `api.indexnow.org:443` (Host-Firewall; Compose erzwingt keine
  Domain-Allowlist).
- Keine Behauptung, eine Einreichung garantiere Indexierung; AI Performance hat keine API.

Secret-Ablage (Hausmuster #887, wie `google-reporting`, #1130-Lehre): zwei Stellen,
`plugin-bing-webmaster` (Bridge-Key, Zufallswert, vom Betreiber per `jnpt credential set` über
stdin gesetzt, zugleich Connector-Credential `bing-webmaster`) und `plugin-bing-webmaster-bing`
(Bing-Schlüssel, nur Admin-GUI). Die Ordner unter `/run/jnpt/plugins/` kommen aus tmpfiles
(`deploy/jnpt-plugins-bing-webmaster.conf.example`), Compose hängt sie als Ordner read-only ein
(`/run/secrets/jnpt`, `/run/secrets/bing`). `jnpt-plugin-key@` des Produkts passt nur bei **einem**
Ordner; der zweite braucht ein eigenes path/service-Paar `deploy/jnpt-bing-webmaster-key.{path,service}`.
Der Dienst liest beim Start; Neustart bei Schlüsselwechsel und Boot macht das Unit-Paar. Entfernen
ist kein Widerruf (Bing-Schlüssel bis zum Widerruf bei Bing gültig). `tests/test_deploy_files.py`
hält Compose, Deskriptor, tmpfiles und Units zusammen.

`SETUP.md` ist die Einrichtungsanleitung für den Admin und wird je Plugin-Version als
`setup_guide` in den signierten Katalog übernommen (JanuaPort/januaport#1137): höchstens 32 KiB,
gültiges UTF-8, keine relativen Links, nur die Handgriffe des Admins. `tests/test_setup_guide.py`
prüft das.

Tests enthalten nur synthetische Daten; der Bing-Schlüssel der Tests ist ein lauter Canary. Lock mit
`pip-compile --generate-hashes` (derselbe Lock wie `linkedin-seite`: nur aiohttp), install mit
`--require-hashes`. `tests/mutations.py` deckt Bridge-Auth, Vorprüfung, Host-Allowlist,
Schreib-/Dry-run-/Deckel-/Quoten-Regeln und die A1-Mutanten ab. `tests/wire_server.py` ist der
Server für den Cross-Repo-Test des Produkts (echter Dienst, echter Adapter, Loopback-Stub).
Signatur/Katalog/Deployment sind nicht Teil der Implementierung.

Release (#1292): Version 0.1.0 steht im Kopf dieser Datei, im README-Titel und im CHANGELOG (sonst
nirgends). Das Image baut nur `.github/workflows/bing-webmaster-release.yml` auf den Tag
`bing-webmaster-vX.Y.Z` (Tag setzt GATE, nie ein Branch); Aufbau und Wächter
(`tests/test_release_workflow.py`) wie bei `google-reporting`.

Offene Herstellerunsicherheiten (live zu belegen, siehe README): Anführungszeichen um `url` bei
`GetUrlInfo`, Bedeutung von `DailyQuota`/`MonthlyQuota`, Bings Datumswert für „nie“.
