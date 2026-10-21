# Komponenten

| Modul | Verantwortung |
|---|---|
| [google-reporting](google-reporting/CLAUDE.md) | #1130, Stufe 1: privater Python-REST-Beisteller für acht feste GSC-/GA4-Berichte. Google-Auth außerhalb des Gatewaykerns; Schlüssel nur aus Tresor-Stellen (`plugin-google-reporting` = ein Bridge-Key, `plugin-google-reporting-google` = Servicekonto-JSON, Ordner-Mounts, eigenes Unit-Paar für den Neustart); zwei Prozessworker mit RAM-Token-Caches und tatsächlicher Abbruchgrenze, feste öffentliche Seitenfilter. Runbook/Netz-/Audit-/SEC-Livegates im Modul. Gebaut/synthetisch geprüft; kein Release/Livebeleg. |
