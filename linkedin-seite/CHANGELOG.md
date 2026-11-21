# Changelog

## Unveröffentlicht

- JanuaPort/januaport#1435: **Refresh-Fehlschlag.** Scheitert der Token-Refresh im Fünf-Minuten-Fenster vor
  Ablauf und gilt das Access-Token noch, gibt `Tokens.access()` das vorhandene Token zurück (WARN-Zeile
  `token_erneuerung_fehlgeschlagen code=…`, nie Token-Inhalt); der nächste Abruf versucht den Refresh erneut.
  Nach Ablauf bleibt der Fehler. **Tagesaktion:** `access()` läuft vor `begin_action()`; ohne Token wird keine der
  fünf Schreibaktionen je UTC-Tag verbraucht (README: „unmittelbar vor dem ersten LinkedIn-Aufruf“).
- JanuaPort/januaport#1438: **Upload-Pfad.** `upload_ziel` zerlegt den Pfad vor dem Dekodieren und weist `.`- und
  `..`-Segmente, kodierte Trenner (`%2f`, `%5c`), doppelte Kodierung (`%25`) und Steuerzeichen ab, bevor das
  Präfix `/dms-uploads/` gilt. Host, Schema, Port, Zugangsdaten und Fragment bleiben exakt geprüft.
- JanuaPort/januaport#1438: **Doku.** README und Modulvertrag halten fest: Der Freigabe-Hash ist kein Geheimnis,
  die menschliche Freigabe liegt allein im Kern, und wer den Bridge-Key hat, kann ohne sie posten; der Bridge-Key
  ist wie ein Schreibrecht zu schützen. Geprüft ohne Codeänderung: Der Kern sendet nicht gesetzte optionale
  Query-Parameter nicht (`tagesliste` ohne `datum` ist unbedenklich); eine Sperre je (Verfasser, Post) wird nicht
  gebaut (Begründung im Modulvertrag).
- Keine Versionsnummer geändert, kein Image-Tag.

## 0.4.1 — 2026-11-11

- JanuaPort/januaport#1237, Schnitt c-6800107310, SEC L1/L2 c-6800122813: `KOMMENTAR_URN` kannte nur die
  Activity-Form `urn:li:comment:(urn:li:activity:<n>,<n>)`; jede Kommentarzeile mit anderem Faden fiel
  als `nicht_lesbar` heraus, ohne dass im Log stand, warum. Welche Form LinkedIn live liefert, ist nicht
  belegt; deshalb zwei Teile: weitere Formen und eine Diagnose.
- **Faden-URN:** `KOMMENTAR_URN` nimmt jetzt `urn:li:comment:(urn:li:(activity|ugcPost|share):<ziffern>,<ziffern>)`,
  nur Ziffern, beide Teile höchstens 30 Stellen. Bei `ugcPost` und `share` muss `object` genau der Post des
  Listings sein (und damit der Faden selbst); bei `activity` gilt die Regel von 0.4.0 (Activity oder
  Post). Der Faden steht am Kommentar; Links, `antwort_urn` und der `reply`-Aufruf bauen daraus
  (`feed/update/<faden-urn>`), nie aus einer geratenen Activity. `antworten`: Antworten müssen im selben Faden
  liegen wie der Elternkommentar. Die Links der Formen `ugcPost` und `share` sind **nicht live belegt**.
- **Formdiagnose:** je verworfener Zeile eine WARN-Zeile im Serverprozess
  `route=… grund=<Klasse> schluessel=<Feldnamen> comment_praefix=… object_praefix=… actor_praefix=…`.
  Klassen fest: `urn_form`, `object`, `actor`, `zeit`, `text_typ`, `attribute`, `geloescht_typ`,
  `kein_objekt`; die erste Abweichung zählt. Nie Text, volle Kennung oder Name. **L1:** ein Präfix steht nur,
  wenn `^urn:li:[A-Za-z]{1,30}:` passt, genau dieser Teil; sonst `andere`, fehlend `fehlt`, kein String `typ`.
  **L2:** nur Schlüssel der obersten Ebene nach `^[A-Za-z][A-Za-z0-9_]{0,39}$`, sortiert, höchstens 20; andere
  zählen als `?` und werden nie geschrieben. Höchstens fünf Zeilen je Aufruf (Adapter und Serverprozess prüfen
  je); der Serverprozess lässt nur Zeilen der festen Form durch.
- Unverändert: Antwort an den Aufrufer (`nicht_lesbar: n`), SEC S1–S4 und T1–T3, Tagesdeckel 8 (`daily_cap`),
  Audit-Tiefe, keine neuen Antwortfelder, kein zusätzlicher LinkedIn-Aufruf.
- Ausrollen wie 0.4.0: neues Image, per Digest in `JNPT_LINKEDIN_SEITE_IMAGE`; keine neue Zustimmung. Nach
  dem Rollout zeigt das Log (WARN-Zeilen), welche Formen LinkedIn tatsächlich liefert. Synthetisch geprüft,
  **nicht live belegt**.


## 0.4.0 — 2026-11-10

- JanuaPort/januaport#1237, **Stufe B3**: Kommentare unter eigenen Posts lesen und als Organisation
  beantworten, ohne Einzelfreigabe. Verbindlich: Schnitt c-6747016143, SEC-PASS c-6747087768 (S1–S4,
  K2–K5), SEC T1 c-6759978021 zur Tagesliste, PO-Antworten c-6759929959.
- **Fünfter Scope** `r_organization_social_feed` (`COMMUNITY_SCOPES`); `GRANT_SCOPES` hat jetzt genau
  fünf Werte, `oauth.py` prüft die Menge exakt, `tools/zustimmung.py --gesamt` fordert die fünf an. Ein
  JSON mit den vier Scopes von 0.3.1 startet nicht mehr: **neue Zustimmung nötig** (Ablauf in SETUP.md).
  `SCOPES` (Statistik-Leser) nennt nie einen `*_feed`-Scope (K5); das Access-Token trägt alle fünf.
- **Neue Vorlage `linkedin-seite-community`**, Route `/v1/linkedin-kommentare/`, drei Werkzeuge:
  `comments` (lesen, unbeantwortete Drittkommentare oberster Ebene unter einem eigenen Post, höchstens
  30 Tage alt, neueste zuerst, Fortsetzungsmarke, ohne Verfasser, Name, Personen-URN, Erwähnung, Bild),
  `reply` (schreiben als Organisation, `anlass` aus `dank`, `sachfrage_zum_post`, `gespraech_anbieten`)
  und `tagesliste` (lesen, nur Links aus geprüften IDs, feste Merkmale, T1). Entwurf der Kern-Vorlage
  unter `entwurf/linkedin-seite-community.yaml` für CONNECTOR: `reply` mit `write: true` ohne
  Freigabe-Block, `daily_cap: 8` **Versuche** (S3), `anlass` als `enum` (S2), `text` in `redact_params`.
- `reply` prüft live und ohne Speicher: eigener Post, Kommentar in der Liste genau dieses Posts, oberste
  Ebene (`ebene`), nicht von uns (`von_uns`), nicht gelöscht, keine eigene Antwort (`wiederholt`), keine
  eigene Antwort an denselben Verfasser unter diesem Post (`verfasser_beantwortet`), feste Textregeln.
  Ablehnung als HTTP 200 mit `nicht_ausgefuehrt` und festem Code; nach dem Sendeversuch unklar.
  Warteschlange im Prozess mit 60 s Abstand, Sperre 10 Minuten je Kommentar, Nachlesen nach dem Senden
  mit `doppelt`. **Kein Antwort-Zähler im Plugin**: die Bremsen 20/Tag und 5/Post entfallen, der Deckel
  liegt allein im Kern. Der Community-Pfad hat wie Lesen und Schreiben einen eigenen
  HTTP-Tagesdeckel (60 LinkedIn-Versuche), der das LinkedIn-Kontingent schützt.
- Kommentartexte nur im Arbeitsspeicher eines Aufrufs, nie im Log; Log-, Personen- und
  Tageslisten-Kanarienvögel. Kein Gateway-Zugang und kein Redaktionsplan im Community-Pfad (S1).
- Synthetisch verifiziert; Live-Abnahme offen (Form von `parentComment`, Verhalten auf zwei Ebenen,
  Weblink-Form, Lesen über `r_organization_social_feed` als Seiten-Admin).
- CI: Zeitgrenze des Jobs `unit` von 15 auf 20 Minuten. Suite und Mutationen brauchen jetzt rund
  11:53 ohne Installation (gemessen: 367 Tests in 219 Sekunden, 266 Mutanten in 493 Sekunden).

## 0.3.1 — 2026-11-04

- JanuaPort/januaport#1237, Befund GATE 03.11.: Nach der zweiten Zustimmung (nur Schreib-Scopes)
  lieferte jeder Lesezugriff `source_access_denied`. Die Herstellerdoku nennt das Verhalten; live
  ist die Ursache nicht belegt. Zwei getrennte Grants tragen deshalb nicht.
- **Ein Grant:** `GRANT_SCOPES` = `rw_organization_admin`, `r_organization_social`,
  `w_organization_social`, `w_organization_social_feed`; `SCOPES` und `WRITE_SCOPES` bleiben als
  Bedeutung. `tools/zustimmung.py --gesamt` ist Pflicht und der einzige Modus, `--schreiben` und der
  Lese-Modus entfallen. Die Antwort muss die vier Scopes nennen (Trenner tolerant, Menge exakt), das
  JSON trägt sie im Feld `scope`.
- **Eine Stelle:** `plugin-linkedin-seite-oauth` trägt den Grant, der Dienst prüft die Menge beim
  Start; JSON ohne `scope` oder mit anderer Menge startet nicht. `plugin-linkedin-seite-post-oauth`
  wird nicht mehr gelesen; Mount, Pfad, tmpfiles-Zeile und Path-Beobachtung sind entfallen.
- **Ein Token-Zustand:** `oauth.Tokens` lebt nur im Serverprozess, beide Pools teilen ihn.
  Worker und Jobs fragen über ihre Pipe nach dem Access-Token; Refresh-Token und Client-Secret
  erreichen sie nicht mehr. Eine Thread-Sperre lässt genau einen erneuern, auch über beide Pfade.
  Ein gekillter Worker verliert keine laufende Erneuerung mehr, Folgejobs erneuern nicht erneut.
  Der Tausch läuft im Thread, der Server bleibt bedienbar. Permit und Transport bleiben je Pfad getrennt.
- Unverändert: SEC-Trennung im Gateway, `X-Jnpt-Freigabe`, Hash-Vergleich, Tagesdeckel, Lese-Routen
  nur GET, kein neues Werkzeug. SEC Bed. 4 (eigener Schreib-Grant) ist aufgehoben und braucht eine
  SEC-Entscheidung. Umstieg in SETUP.md: Widerruf, Einspielen, eine Zustimmung, alte Stelle leeren.
- CI: Zeitgrenze des Jobs `unit` von 10 auf 15 Minuten. Suite und Mutationen brauchen jetzt rund
  10:50 (gemessen: 281 Tests zweimal je 190 Sekunden, 194 von 215 Mutanten in 3:45).
- Synthetisch verifiziert; Live-Abnahme offen.

## 0.3.0 — 2026-11-03

- SEC c-6608136792 / LEAD Variante a: Nach gültiger Headerform und bewiesener Nichtausführung
  HTTP 200 mit nicht_ausgefuehrt: true, damit der Kern den Indikator aus 2xx lesen kann.
  Gate-Abweichungen bleiben 409 ohne Ergebnisfelder; Fehler nach erstem Schreibversuch
  bleiben unklar ohne beide Felder. Schreibmarkierung erst nach lokalem HTTP-Permit.
- Beide gemeinsamen JSON-Fixtures durch text eol=lf gesichert; Vorschau-Beispiel von CRLF
  auf LF normalisiert. Datei-SHA-256 gegen Kern-Git-Fassung lokal und feste Pins in CI,
  samt CRLF-/Dateidrift-Mutanten. Kern-Checkout bleibt unverändert.

- LEAD-Nachtrag F2-Anschluss: Vorschau mit direkten ziel_urn/text und medien[].name,
  zusätzliches Wirkungsobjekt entfernt. Exakte Schlüssel/Typen nach der unverändert kopierten
  Kern-Beispieldatei, Hash separat aus direkten Wirkungsfeldern geprüft; H3 bleibt erhalten.
  Wiederholung bestätigt ausgefuehrt UND wiederholt ohne zweiten Post. F2-Fristen dokumentiert.

- JanuaPort/januaport#1306 F4, Plan v2/v2.1, SEC c-6602359446 A1/A3 und c-6604738594 H1–H3.
- Freigabeprüfung über genau `X-Jnpt-Freigabe: hash=<freigabe:v2:sha256:…>` für alle vier
  Schreibaktionen. 409 vor LinkedIn bei fehlendem/abweichendem Header. Kartei-Freigabeprüfung
  und Herkunftslabels entfernt; Gateway-Token nur noch zwei Lesewerkzeuge, alter Config-Key entfällt.
- Vollständige Wirkung aus demselben Eintragsstand wie die Vorschau; Kern-Hash v2 samt
  unveränderten Golden-Vektoren, Bindung sichtbarer Medientitel und Umfrage.
- Eindeutige Ergebnisfelder: ausgefuehrt bei bestätigtem Erfolg, nicht_ausgefuehrt nur bei
  bewiesenem Abbruch vor erstem Schreibversuch, danach unklar ohne beide Felder.
- Dauerhafter Sperrvermerk im Kern; report_hash bleibt RAM-Komfort. HTTP liefert das Endergebnis,
  Zwischenzustand und Nachfrage-Fenster entfallen. Migration/Timeout in README und SETUP.
- Echte Adapter-/HTTP-/Prozesstests und Pflichtmutanten. Synthetisch verifiziert; Live-Abnahme offen.

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
