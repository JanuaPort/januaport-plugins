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
- server.py: feste GET-Routen, **ein** Bridge-Key für beide Quellen
  (`/run/secrets/jnpt/runtime-key`) und das Servicekonto-JSON
  (`/run/secrets/google/runtime-key`), beide beim Start via `load_runtime_files`;
  strikte Vorprüfung, sichere Codes und Route/Code/Dauer-Log. Die Rechte je
  Werkzeug prüft das Gateway, nicht der Dienst. /healthz lokal ohne Googlezugriff.

Invarianten an allen Code-/Dokuorten nachziehen:512 KiB Antwort,8 KiB Anfrage,
Pfad 512,100 Rangzeilen/90 Tage,50 öffentliche Pfade/4096Regex,20 s/25 s,
2 Worker/30 pro Quelle pro Minute/keine Retries,query_enabled=false als Auslieferungsvorgabe (Betreiberschalter, SEC V5),
GA-Kleingruppenschwelle aus Messvertrag,Auditmetadaten je SSO/Token.
Privacy-Scan heißt field:query, weil Gateway JSON-Schlüssel rekursiv scannt.
Keine stillen Ersatzmetriken, Null-Fallbacks, freien Googleobjekte oder PII-Logs.

Secret-Ablage (Hausmuster #887, SEC B1): Stellen `plugin-google-reporting` (Bridge-Key,
Zufallswert, vom Betreiber per `jnpt credential set` über stdin gesetzt — zugleich als
Connector-Credential `google-reporting`, danach verworfen) und
`plugin-google-reporting-google` (Servicekonto-JSON, nur Admin-GUI) im Gateway-Tresor; die Ordner
unter `/run/jnpt/plugins/` kommen aus tmpfiles (`deploy/jnpt-plugins-google-reporting.
conf.example`), Compose hängt sie als Ordner read-only ein. Nie Klartext unter `/etc`
oder `/opt`. Der Dienst liest beim Start; Neustart nach Schlüsselwechsel und Boot macht
das eigene Unit-Paar `deploy/jnpt-google-reporting-key.{path,service}` (die Produkt-
Vorlage `jnpt-plugin-key@` kennt nur eine Stelle je Compose). Entfernen ist kein
Widerruf: Der Google-Schlüssel gilt bis zum Löschen in der Cloud Console.
`tests/test_deploy_files.py` hält Compose, Deskriptor, tmpfiles und Units zusammen.

Compose bindet ausschließlich reservierte Plugin-Netz-IP, kein Hostport.
jnpt_plugins bleibt internal; eigenes Egress-Netz benötigt Zielhost-Firewall.
Keine Behauptung, Compose erzwinge die Domain-Allowlist. Lokale Gesundheit und
Google-Readiness getrennt. Vollständiges Runbook und Live-Gates: README.md.

Tests enthalten nur synthetische Daten; Signer flüchtig generieren. Lock
mit pip-compile --generate-hashes erstellen, install --require-hashes.
Mutationstests müssen Bridge-Key-Auth, Vorprüfung, Filter, Retries und Kill abdecken.
Signatur/Katalog-Release/Deployment nicht Teil der Implementierung.
