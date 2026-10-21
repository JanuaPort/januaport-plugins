# Änderungen — Google Reporting

## Unveröffentlicht

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
  entfallen. Das Servicekonto-JSON kommt aus der Stelle `plugin-google-reporting-google`
  (Admin-GUI, Basis-Einrichtung). Compose mountet zwei Ordner read-only
  (`/run/secrets/jnpt`, `/run/secrets/google`) statt dreier Datei-Mounts; Deskriptor-
  REQUIRE, tmpfiles-Beispiel und ein eigenes Unit-Paar für Neustart bei Schlüsselwechsel
  und nach Boot sind neu. `load_identity` akzeptiert das JSON auch ohne Zeilenumbrüche
  (GUI-Passwortfeld). Entfernen ist kein Widerruf: Der Google-Schlüssel gilt bis zum
  Löschen in der Cloud Console. Alt: `google-service-account.json`, `gsc-bridge-key`,
  `ga4-bridge-key` (nie veröffentlicht).
