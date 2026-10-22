# Änderungen — Google Reporting

## Unveröffentlicht

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
