# Sicherheitsrichtlinie / Security Policy

Dieses Repository gehört zu JanuaPort, hergestellt von der JanuaPort GmbH.
Schwachstellen nehmen wir über einen koordinierten Offenlegungsprozess
entgegen. *(English below.)*

## Meldeweg

- **E-Mail: `security@januaport.ai`** — bitte **nicht** über öffentliche
  Issues oder Pull Requests.
- Verschlüsselung auf Anfrage: kurz per E-Mail fragen, wir stellen einen
  Schlüssel bereit.
- Maschinenlesbar: <https://januaport.ai/.well-known/security.txt>

## Was in eine Meldung gehört

Betroffene Datei, Version oder Commit · Angriffsweg, Voraussetzungen und
Wirkung, am besten mit kleinem Nachweis · ob die Schwachstelle nach deiner
Kenntnis schon ausgenutzt wird · wie wir dich erreichen. Bitte keine echten
Kunden- oder Personendaten mitschicken.

## Was wir zusagen

- **Eingangsbestätigung binnen 3 Werktagen**, eine erste Einschätzung binnen
  10 Werktagen.
- Den Zeitpunkt der Veröffentlichung stimmen wir mit dir ab; Ziel ist ein Fix
  vor der Veröffentlichung. Auf Wunsch nennen wir dich.
- Aktiv ausgenutzte Schwachstellen melden wir zusätzlich nach dem EU Cyber
  Resilience Act an die zuständigen Stellen.
- Kein Bug-Bounty.

## Geheimnisse im Repository

Jeder Push und jeder Pull Request durchläuft einen Scan der gesamten
Historie auf Zugangsdaten, IBAN und E-Mail-Adressen (`secret-scan`,
Regeln in `.gitleaks.toml`). Ein Treffer lässt die Prüfung scheitern. Hast du
versehentlich ein echtes Geheimnis eingereicht: bitte sofort ungültig machen
und uns an die Adresse oben schreiben.

## In English

Report vulnerabilities to **security@januaport.ai**, not via public issues.
We confirm receipt within 3 business days, give a first assessment within
10 business days and coordinate disclosure with you. Encrypted reporting on
request. No bug bounty. Every push and pull request runs a full-history
secret scan.
