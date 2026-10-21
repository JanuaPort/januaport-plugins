# google-reporting — Modulvertrag (#1130)

Privater Python-REST-Beisteller, Apache2.0. Stufe 1, gebaut/synthetisch geprüft,
Live-Abnahme offen. Kein eigener MCP-Server oder Gateway-Auth-Typ.

- domain.py: fail-closed Betreiberkonfiguration, feste Berichtseingaben/-filter,
  source-local28/90 Tage bis heute−3, Datenminimierung und Qualitätsprojektion.
- google_adapter.py: offizielle google-auth-, GSC- und GA-Data-REST-Clients;
  feste Properties, getrennte readonly Scopes und RAM-Caches. Explizite Datei,
  kein ADC. ScopedCredentials.refresh nutzt gepinnte private SDK-Naht, damit
  Tokenretry/Trust-boundary-Lookup aus bleiben; tatsächliche SDK-Tests Pflicht.
- workers.py: zwei persistente spawn-Prozesse, vier Queueplätze,30/min/Quelle,
 20 s inklusive Queue/Token. Timeout/Disconnect tötet Prozess und belegt keinen
 Hintergrundthread weiter; erst nach Reap Ersatz. RAM-Caches gehen dabei verloren.
- safe_http.py: gepinnte aiohttp-Protokollnaht ersetzt Fehler vor der Middleware
  durch feste JSON-Codes ohne rohe Requestbytes/Exception-Logs. SafeAppRunner
  ist Pflicht, Parserlimits bleiben 8192. Upgrade verlangt Canary-Proben.
- server.py: feste GET-Routen, zwei unabhängige Secretdateien, strikte Vorprüfung,
  sichere Codes und Route/Code/Dauer-Log. /healthz lokal ohne Googlezugriff.

Invarianten an allen Code-/Dokuorten nachziehen:512 KiB Antwort,8 KiB Anfrage,
Pfad 512,100 Rangzeilen/90 Tage,50 öffentliche Pfade/4096Regex,20 s/25 s,
2 Worker/30 pro Quelle pro Minute/keine Retries,query_enabled=false bis SEC,
GA-Kleingruppenschwelle aus Messvertrag,Auditmetadaten je SSO/Token.
Privacy-Scan heißt field:query, weil Gateway JSON-Schlüssel rekursiv scannt.
Keine stillen Ersatzmetriken, Null-Fallbacks, freien Googleobjekte oder PII-Logs.

Compose bindet ausschließlich reservierte Plugin-Netz-IP, kein Hostport.
jnpt_plugins bleibt internal; eigenes Egress-Netz benötigt Zielhost-Firewall.
Keine Behauptung, Compose erzwinge die Domain-Allowlist. Lokale Gesundheit und
Google-Readiness getrennt. Vollständiges Runbook und Live-Gates: README.md.

Tests enthalten nur synthetische Daten; Signer flüchtig generieren. Lock
mit pip-compile --generate-hashes erstellen, install --require-hashes.
Mutationstests müssen Authtrennung, Vorprüfung, Filter, Retries und Kill abdecken.
Signatur/Katalog-Release/Deployment nicht Teil der Implementierung.
