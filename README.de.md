# januaport-plugins

[English](README.md) · **Deutsch**

Beisteller für JanuaPort: kleine, eigenständige Dienste, die neben dem Gateway laufen, nicht in ihm.

JanuaPort ist ein self-hosted MCP-Gateway. Es verbindet KI-Assistenten fein berechtigt mit den
bestehenden Systemen eines Unternehmens und protokolliert die Zugriffe in einem append-only Audit-Log. Der
Kern von JanuaPort ist proprietäre Software der JanuaPort GmbH und nicht Teil dieses Repositorys. Dieses
Repository ist einer der offenen Ränder darum herum.

- **Lizenz:** Apache License 2.0 ([`LICENSE`](LICENSE), [`NOTICE`](NOTICE))
- **Links:** [januaport.ai](https://januaport.ai) · [Sicherheitsrichtlinie](SECURITY.md) · [Beiträge](CONTRIBUTING.md)

Keine Kundendaten, keine Schlüssel, keine Betreiberwerte in diesem Repository.

---

## Warum Beisteller

Das JanuaPort-Gateway ist ein einziges statisches Binary in einem schlanken Container. Was eine andere
Laufzeit, einen eigenen Zeitplan oder eigene Zugangsdaten braucht, läuft als eigener Beisteller daneben.
Ein Beisteller erreicht JanuaPort nur über dokumentierte Schnittstellen: ein gemeinsames Verzeichnis
oder den MCP-Endpunkt mit eigenem Token. Er lässt sich abschalten, ohne das Gateway anzufassen.

## Enthaltene Beisteller

Kennzeichnung: **Gebaut** · **Im Bau** · **Geplant**. Jeder Eintrag sagt, was genau belegt ist.

| Ordner | Was es tut | Stand |
|---|---|---|
| [`ebics-abholer/`](ebics-abholer/) | Holt täglich den Kontoauszug (camt.053) per EBICS bei der Bank ab und legt ihn als Datei in einem gemeinsamen Verzeichnis ab, aus dem JanuaPort ihn liest. Zahlungen einreichen kann er nicht. | **Gebaut.** Bei der JanuaPort GmbH produktiv im Einsatz für das eigene Firmenkonto (EBICS H005, täglicher Timer-Betrieb seit 07.09.2026). Bisher gegen eine Bank belegt. |
| [`kartei-rpa/`](kartei-rpa/) | Ein PowerShell-Modul und vier Aktions-Skripte, mit denen ein RPA-Werkzeug (Power Automate Desktop) eine Arbeitsliste aus der JanuaPort-Kartei abarbeitet: freigegebene Sätze holen, einzeln sperren, im Fachsystem buchen, das Ergebnis zurückschreiben. Feldnamen per Einstellungsdatei, Voreinstellung passend zum Rezept „Zahlungseingang“ in `januaport-recipes`. | **Gebaut.** Mit gemocktem Transport getestet (Pester, Windows PowerShell 5.1). Diese Fassung ist noch nicht gegen eine echte Anlage gelaufen. |
| `box/` (noch nicht in diesem Repository) | Provisionierung der JanuaPort Box (On-Premises-Gerät). | **Geplant.** |
