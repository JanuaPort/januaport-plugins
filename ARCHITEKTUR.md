# Komponenten

| Modul | Verantwortung |
|---|---|
| [google-reporting](google-reporting/CLAUDE.md) | #1130, Stufe 1: privater Python-REST-Beisteller für acht feste GSC-/GA4-Berichte. Google-Auth außerhalb des Gatewaykerns; zwei Prozessworker mit RAM-Token-Caches und tatsächlicher Abbruchgrenze, feste öffentliche Seitenfilter. Runbook/Netz-/Audit-/SEC-Livegates im Modul. Gebaut/synthetisch geprüft; kein Release/Livebeleg. |
