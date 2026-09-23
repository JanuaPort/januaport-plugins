# januaport-plugins

Beisteller neben dem JanuaPort-Gateway: EBICS-Abholer, Box-Provisionierung u. a. (Apache 2.0)

**Status:** privat bis zum GoLive von JanuaPort (Flip in der GoLive-Checkliste JanuaPort/januaport#788). Teil des Open-Core-Pivots (JanuaPort/januaport#773).

**Lizenz:** Apache License 2.0 (`LICENSE`), Copyright 2026 JanuaPort GmbH (`NOTICE`). Beitraege: `CONTRIBUTING.md`.

**Zustaendig:** Lead + BOX (#778, #781) — Ownership je Unterordner; Inhalte kommen mit den genannten Tickets.

## Enthaltene Beisteller

| Ordner | Was es tut | Status |
|---|---|---|
| [`ebics-abholer/`](ebics-abholer/) | Holt taeglich den Kontoauszug (camt.053) per EBICS bei der Bank ab und legt ihn als Datei in einem gemeinsamen Verzeichnis ab, aus dem JanuaPort ihn liest. | beim Hersteller produktiv belegt (eigenes Firmenkonto, EBICS H005, seit 07.09.2026) |
| [`kartei-rpa/`](kartei-rpa/) | PowerShell-Modul und vier Aktions-Skripte, mit denen ein RPA-Werkzeug (Power Automate Desktop) eine Kartei-Arbeitsliste abarbeitet: freigegebene Saetze holen, sperren, im Fachsystem buchen, Ergebnis zurueckschreiben. Feldnamen per Einstellungsdatei, Voreinstellung = Rezept "Zahlungseingang". | mit gemocktem Transport getestet (Pester, Windows PowerShell 5.1); diese Fassung noch nicht gegen eine echte Anlage gelaufen (JanuaPort/januaport#806) |
| `box/` | Provisionierung der JanuaPort Box (on-prem-Appliance). | geplant (JanuaPort/januaport#781) |

Keine Kundendaten, keine Schluessel, keine Betreiberwerte in diesem Repository.
