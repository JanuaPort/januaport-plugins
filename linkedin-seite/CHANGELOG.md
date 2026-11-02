# Changelog

## 0.2.0 — 2026-11-02

- Stufe B1, Posten nach Freigabe (JanuaPort/januaport#1237; PO-Zuruf 02.11., SEC-PASS c-6575359082
  Bedingungen 1–5 und c-6575837332 Umfrage/Video V1–V3). Fünf neue Werkzeuge unter dem eigenen
  Präfix `/v1/linkedin-post/`: `vorschau` (lesend, liefert den Hash), `create_post`, `edit_post`,
  `delete_post`, `create_comment` (nehmen nur die Kartei-ID). Formate Text, Bild, 2–20 Bilder, PDF,
  Umfrage, Video. Kein Entwurf: LinkedIn nimmt beim Anlegen nur `PUBLISHED` an.
- Freigabe nur aus der zweiten Kartei `redaktion_freigabe`: Satz mit gleicher `eintrag_id`, `aktion`
  und `hash`, angelegt und zuletzt geändert per SSO (`via`/`updated_via`). Hash = SHA-256 über die
  kanonische Form der gepostet-relevanten Felder und den SHA-256 der Bytes jeder Mediendatei,
  eingefrorene Vektoren. Freigabe vor jedem LinkedIn-Aufruf, auch vor `initializeUpload`.
- Rückverbindung W2: eigener MCP-Client an `http://jnpt:8484/mcp` (nur `/mcp`), geschlossene
  Werkzeugliste `redaktionsplan_get`, `redaktion_freigabe_search`, `<ablage>_get_file`; eigene Stelle
  `plugin-linkedin-seite-gateway`.
- Eigener Schreib-Grant `plugin-linkedin-seite-post-oauth` mit genau `w_organization_social` und
  `w_organization_social_feed` (Gate-Befund VENDOR: Comments API 202610, Tabelle „Permissions“,
  `w_organization_social_feed` „Post, comment, and react on posts on behalf of an organization“; die App
  trägt beide), `tools/zustimmung.py --schreiben`; das Schreib-JSON trägt `scope`, die Stufe-A-Prüfung
  weist es ab. Token-Antwort: Trenner tolerant, Menge exakt. Lese- und Schreib-Token getrennt, auch bei
  der Erneuerung.
- Geschlossene Endpunktliste, Autor/owner/actor nur die konfigurierte Organisation, Upload nur an
  `https://www.linkedin.com/dms-uploads/…` ohne Umleitung, Token nur beim Bild-/Dokument-Upload,
  Video-Teile lückenlos, Status höchstens 15 Minuten, Post nur bei `AVAILABLE`.
- Je Aktion ein eigener Prozess mit privater RAM-Kopie der Medien (einmal gelesen, immer gelöscht).
  5 schreibende Aktionen je UTC-Tag über alle Aktionen, 45 LinkedIn-Schreibversuche je Tag,
  Idempotenz `eintrag_id` + `hash` im Serverprozess, ein Auftrag zugleich, `202 in_arbeit` für lange
  Aufträge. Scheitert ein Auftrag nach dem gesendeten Post-/Kommentar-Aufruf, sperrt
  `409 ergebnis_unbekannt` dieselbe Freigabe (kein Doppelpost). Spur je Auftrag nur IDs, URN, Hash, Größe.
- Compose: zwei weitere read-only Ordner-Mounts (optional befüllt), tmpfiles vier Ordner, Path-Unit
  vier Schlüssel, REQUIRE unverändert. `config.json` mit optionalem Block `schreiben`.
- Stufe A unverändert in Verhalten und Scopes; drei Mutations-Anker an die gemeinsame Bridge-Prüfung
  und den Scope-Parameter angepasst. Test-first: 93 neue Tests (115 → 208), 67 neue Mutationsproben (90 → 157).

## 0.1.2 — 2026-11-02

- Korrektur nach dem ersten Abruf auf der Showcase (02.11. 09:06Z, 502 `source_rejected`,
  JanuaPort/januaport#1237). Die Query wurde vollständig URL-kodiert, auch die Rest.li-2.0-Struktur
  von `timeIntervals=(…)` und `shares=List(…)`. Die LinkedIn-Doku 202610 verlangt dort rohe
  Klammern, Doppelpunkte und Kommas; nur die URN wird kodiert. `restli_query` baut die Query jetzt
  so. Header (`LinkedIn-Version`, `X-Restli-Protocol-Version: 2.0.0`) und Scopes unverändert.
  Regression zuerst rot gegen die rohe Query, dann grün; neue Mutation `restli-structure-raw`.

## 0.1.1 — 2026-11-02

- Korrektur nach der echten LinkedIn-Zustimmung vom 02.11.2026 (JanuaPort/januaport#1237,
  c-6571493150), die zweimal abgewiesen wurde. Regression zuerst rot, dann grün.
- `scope` kommt von LinkedIn kommagetrennt (`r_organization_social,rw_organization_admin`).
  Trenner sind jetzt Komma und/oder Leerraum; die Menge muss weiter genau den zwei gepinnten
  Scopes entsprechen (nichts Zusätzliches, nichts Fehlendes, andere Trenner und Nicht-Strings
  fail-closed).
- `refresh_token_expires_in` kam mit 31536059 (59 s über 365 Tagen) und wurde abgewiesen. Eine
  Überschreitung bis zu einem Tag über dem Höchstwert wird auf genau 365 Tage gedeckelt, beim
  Access-Token auf 60 Tage; die Frist wird nie verlängert und die Erstzustimmung bleibt die
  Obergrenze. Darüber, nicht ganzzahlig oder nicht positiv bleibt `unexpected_source_shape`.
  Gilt für Zustimmung und Erneuerung.
- Zustimmungsskript: Ein Client-Secret mit Steuerzeichen (unter `cmd` fügt Strg+V eines ein)
  wird mit fester deutscher Meldung verworfen, bevor Browser oder LinkedIn berührt werden. Die
  Meldung nennt den Wert nicht. SETUP.md: unter Windows PowerShell 7 `Read-Host -MaskInput`
  und `--secret-stdin`.
- Das Tresor-JSON ist unverändert (gleiche Felder, gleiche Prüfung): bereits gespeicherte
  Zugangsdaten bleiben lesbar. Zwölf neue Mutationsproben, Anker von `expanded-grant` nachgezogen.
  Kein Release-Tag in diesem Stand.

## 0.1.0 — 2026-10-29

- Abschlusskorrektur nach SEC-PASS 658bf9a..77830b0 (#1237 c-6509587143): gültiges
  Paging mit fehlenden/leeren links zulassen, next-Link/unbekannte Form weiter abweisen.
  Belegter IPv6-Port bricht Zustimmung vor Browserstart ab; IPv4-Fallback ausschließlich
  bei fehlendem IPv6. Windows-Sockets exklusiv binden. Je Punkt Rot-/Grün- und Mutationsprobe.

- SEC-Präzisierung 29.10. nach 08fdf61: gemeinsam genau eine Callback-Anfrage, auch
  fehlerhafte erste Anfrage beendet beide Loopbacks. Nullinteraktion nur bei
  gültiger leerer Einzelbeitragsantwort ohne next-Link; fremde Org/ID und zweite Zeile sind Fehler.
  Nur likeCount darf negativ sein: Summe vor Kappung; andere negative Zähler abweisen.
  Personenbezug im gesamten Beitragsobjekt unterdrückt den Text, Kennzahlen bleiben.
  networkSizes bleibt bei den zwei gepinnten Scopes; Wissensbeleg im Prüfbericht, live offen.
- Nach SEC-PASS auf 658bf9a (#1237 c-6509155914): Callback auf beiden Loopbacks
  127.0.0.1/::1, fehlendes IPv6 mit Hinweis; Redirect unverändert.
  stdout ausschließlich direkt in die Tresor-Pipe, ausdrückliches Datei-Umleitungsverbot.
- Herstellerbefunde 2026-10 (weiter live zu belegen): fünfter fester GET-Endpunkt
  networkSizes für Follower-Gesamtzahl, Follower-Ende höchstens heute−2 UTC; fehlende
  gültige leere Einzel-Share-Statistik bedeutet markierte Nullzähler, negative Like-Summen sichtbar korrigieren,
  Personen-Erwähnungen unterdrücken den Text statt den gesamten Bericht abzubrechen.
  Regression zuerst rot, dann grün; zusätzliche echte HTTP-/Loopback-/Log- und Mutationsproben.

- #1237, PO Option (a): Stufe A, drei feste GET-Werkzeuge für Seitenstatistik,
  Follower-Summen und höchstens zehn Beiträge. Organisation ausschließlich konfiguriert.
- Zentrales unbelegtes Hersteller-Vokabular; fail-closed Summenprojektion, keine Personenlisten.
- Zwei gepinnte Scopes, OAuth mit/ohne Refresh, RAM-Erneuerung und 14-Tage-Warnung;
  lokales Zustimmungsskript mit geprüftem State und optionalem belegpflichtigem PKCE.
- Härtung, safe_http, Prozessworker, Deploy/Key-Units/tmpfiles und Release-Muster aus
  google-reporting. Zusätzlicher Tagesdeckel 50 im Serverprozess vor jedem HTTP-Aufruf.
- Synthetische echte HTTP-Adaptertests, Negativkontrollen, Mutationstests, Hash-Lock,
  setup_guide- und Release-Vertragstests. Stufe B ist nicht enthalten.

## Kuratierung

Noch kein Release-Tag, Registry-Digest, signierter Katalogeintrag oder Live-Nachweis.
Vor Kuratierung: Release-Lauf mit Unit-/Mutationssuite und gehärteten Sonden dokumentieren;
Config-Digest muss dem gepushten amd64-Image entsprechen. SBOM/Provenance und Trivy-Bericht
am geprüften Digest hinterlegen, Befunde bewerten. Aktionen ausschließlich Pins aus dem
Google-Reporting-Vorbild; Schreibjobs nur actions/* und docker/*.
Herstellerfeldnamen, Scopes/Grants, Refresh, API-Version und Netz-/Audit-Gates separat live belegen.
