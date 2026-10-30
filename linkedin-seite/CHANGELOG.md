# Changelog

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
