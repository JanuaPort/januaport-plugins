# Changelog

## Unreleased

- Manueller Installationstest des signierten Windows-Releases in einem frischen
  Standardkonto, mit SHA-256, Authenticode/Herausgeber und GitHub-Provenance,
  Showcase-Kopplung, Installation, fünf Minuten Anlagenkontakt und Deinstallation
  einschließlich Prüfung beider Autostartwege (#1233).
- Offline-Adaptertests und isolierte Mutationsproben für die Testsperren;
  separate Provenance-Negativprobe samt Mutation mit echten Release-Assets und `gh`;
  Dokumentation der Grenzen: Windows-Code nur per argv, kein zuverlässig lokal
  abrufbarer Auftragsabschluss, WTS/Kontobindung auf Hosted-CI und
  `cancelled` beim Admin-Abbruch gegenüber `interrupted` beim Wiederanlauf.

## 0.1.0 — 2026-10-28

Erstes Release: `desktop-runner.exe` für Windows amd64, Authenticode-signiert
(JanuaPort/januaport#1163 D4). Der Linux-Server-Modus ist im Code, wird aber nicht als
Artefakt ausgeliefert; sein Release braucht den Live-Beleg mit rootless Docker (#1195).

- Fester Container-ENTRYPOINT `/bin/sh`, auch bei eigenem Image-ENTRYPOINT;
  Swap-Limit gleich RAM-Limit und Anleitung zur Lesbarkeit von Geheimnissen
  für UID 65532 einschließlich rootless Sub-UID/Sub-GID-Beispiel
  (JanuaPort/januaport#1195).

- Linux-Server-Modus mit Vertrag 2 und signierten Container-Pins
  (JanuaPort/januaport#1195); Windows und Vertrag-1-Goldens bleiben unverändert.
- Docker-CLI über eine testbare Naht, feste Sandbox-Argumente, Digestvergleich,
  tmpfs-Token über privaten stdin, read-only Betreiber-Geheimnisse und Rootless-Meldung.
- Linux-Dateispeicher 0600/0700 mit HMAC-Integrität, bisherige Journaldeckel,
  Abbruch-/Wandzeit-Bereinigung und Wiederanlauf als interrupted.
- Gehärtete systemd-Vorlage, verpflichtende echte Docker-Proben in Linux-CI
  und reproduzierbare Mutationsproben.
- K1 muss noch `refused/secret` und `limit/wall` im Ergebnisadapter akzeptieren.
  Release-Pakete, Signierung, C3, Objekt-URLs und cosign sind nicht enthalten.
