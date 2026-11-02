# Änderungen — Bing Webmaster

## 0.1.0 — 2026-11-02

**Erste Fassung** (JanuaPort/januaport#1292, Stufe 1, gebaut/synthetisch geprüft, Live-Abnahme offen).
Privater Python-Beisteller für Bing Webmaster (JSON-Endpunkt `ssl.bing.com`) und IndexNow
(`api.indexnow.org`): drei lesende Routen (`bing/url_info`, `bing/sitemaps`, `bing/submission_quota`)
und drei schreibende (`bing/submit_urls`, `bing/submit_sitemap`, `indexnow/submit_urls`).

- Grund für den Beisteller: Bing nimmt den API-Schlüssel nur als `?apikey=` in der URL; die
  Connector-Specs setzen Credentials nur in Header. Der Schlüssel steht nur in der ausgehenden
  Verbindung des Beistellers.
- Zwei Schlüsselstellen: `plugin-bing-webmaster` (Bridge-Key, zugleich Connector-Credential) und
  `plugin-bing-webmaster-bing` (Bing-Schlüssel), je ein Ordner-Mount und ein eigenes
  Unit-Paar für den Neustart.
- SEC A1: Ausnahmen werden nur als feste Codes weitergegeben (nie Ausnahmetext, nie Body), keine
  Umleitungen, `trust_env=False`, keine Retries, Log nur `route/code/duration_ms`, Bibliotheks-Logger
  auf `CRITICAL`. Tests mit einem lauten synthetischen Schlüssel auf allen Fehlerpfaden.
- SEC A2: Host-Prüfung (https, exakter Host aus der Allowlist, kein Port, keine Zugangsdaten, kein
  Fragment) auch für `url_info`; Antworten bestehen nur aus einer festen Feldliste.
- Schreibrouten: höchstens 100 Adressen je Aufruf, nie über der gemeldeten Bing-Tagesquote,
  `dry_run` ohne Herstelleraufruf, Tagesdeckel 20 je Route und UTC-Tag (nach unten konfigurierbar).
  IndexNow ohne Konfiguration: `not_configured`.
- Release-Strecke `.github/workflows/bing-webmaster-release.yml` (Tag `bing-webmaster-vX.Y.Z`, Image
  `ghcr.io/januaport/bing-webmaster`) und CI `bing-webmaster.yml`, nach dem Muster von
  `google-reporting`. Kein Katalogeintrag (VENDOR, nach Live-Beleg).
- Offen (live zu belegen): Anführungszeichen um `url` bei `GetUrlInfo`, Bedeutung von
  `DailyQuota`/`MonthlyQuota`, Bings Datumswert für „nie“.

## Kuratierung

Noch keine veröffentlichte Fassung. Je Digest trägt GATE hier Scan und Prüfstand ein.
