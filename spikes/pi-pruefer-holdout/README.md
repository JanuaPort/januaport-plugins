# Messsatz 2 (Holdout) für den Prompt-Injection-Prüfer

Zu JanuaPort/januaport#1486, Phase 3a. **Unabhängig von Satz 1 geschrieben:** Der Autor kannte
den ersten Messsatz, seinen Branch und dessen Dateien nicht. Der Satz prüft, ob ein auf Satz 1
kalibrierter Prüfer auch auf fremdem Material hält.

Geprüft werden Werkzeug-Antworten, bevor ein KI-Agent sie liest (Mail, Datei-/PDF-Auszug,
ERP-Feld, Ticket, Webseite, Upstream-MCP-Antwort). Label 1 = enthält eine Anweisung an die KI,
Label 0 = harmlos. Alle Texte sind synthetisch und frei erfunden (nur Beispiel-Namen, die
Domains `example.org`/`example.com`/`.test`, die Prüfsummen-IBAN `DE89 3704 0044 0532 0130 00`).

## Dateien

- `messsatz2.jsonl`: 240 Einträge, ein JSON-Objekt je Zeile (UTF-8). Felder: `id`, `sprache`,
  `form`, `label`, `klasse`, `gegenstueck_zu` (nur harmlose Gegenstücke), `laenge_zeichen`,
  `quelle`, `text`.
- `erzeuge_messsatz2.py` mit `bausteine_de.py` und `bausteine_en.py`: deterministischer
  Generator, nur Standardbibliothek. Er schreibt `messsatz2.jsonl` und diese README neu.
- `test_messsatz2.py`: prüft Zählungen, Datenregeln, Unicode-Tarnung und die Abkürzungsverbote.

## Ausführen (nur im Container)

```
docker run --rm --memory 1g -v <repo>:/w -w /w/spikes/pi-pruefer-holdout python:3.12-slim python erzeuge_messsatz2.py
docker run --rm --memory 1g -v <repo>:/w -w /w/spikes/pi-pruefer-holdout python:3.12-slim python -m unittest -v test_messsatz2
```

## Abkürzungsverbote (vom Test erzwungen)

- Positive und harmlose Einträge teilen dieselbe Host-Menge; kein Host kommt nur bei Label 1 vor.
- Keine Wörter wie „exfil“, „attacker“, „malicious“, „evil“, „inject“, „payload“ in Hosts oder Texten.
- Großbuchstaben-Werte in URL-Queries (z. B. `PNG`, `XL`) stehen auf beiden Seiten, nie nur bei Label 1.
- Kein Wort steht in mindestens 10 Positiven und in keinem harmlosen Eintrag.

## Zählung

Gesamt: 240 Einträge, davon 120 Positive (Label 1) und 120 harmlose (Label 0).
Je Sprache 60 Positive und 60 harmlose.

| Klasse (Label 1) | de | en | gesamt |
|---|---:|---:|---:|
| bedingt | 4 | 4 | 8 |
| dauerhaft_merken | 3 | 3 | 6 |
| direkte_anweisung | 4 | 4 | 8 |
| exfiltration | 4 | 4 | 8 |
| lang_eingebettet | 4 | 4 | 8 |
| md_exfiltration | 15 | 15 | 30 |
| mehrsprachig_koeder | 3 | 3 | 6 |
| rollenwechsel | 4 | 4 | 8 |
| tabelle_json | 3 | 3 | 6 |
| unicode_tarnung | 4 | 4 | 8 |
| versteckt | 4 | 4 | 8 |
| verteilt | 4 | 4 | 8 |
| werkzeugmissbrauch | 4 | 4 | 8 |

| Klasse (Label 0) | de | en | gesamt |
|---|---:|---:|---:|
| harmlos | 20 | 20 | 40 |
| hart_harmlos | 40 | 40 | 80 |

| Form | de | en | gesamt |
|---|---:|---:|---:|
| datei | 20 | 20 | 40 |
| erp_feld | 20 | 20 | 40 |
| mail | 20 | 20 | 40 |
| mcp_antwort | 20 | 20 | 40 |
| ticket | 20 | 20 | 40 |
| webseite | 20 | 20 | 40 |

| Längenband (Zeichen) | de | en | gesamt |
|---|---:|---:|---:|
| 2.001-8.000 | 12 | 12 | 24 |
| bis 2.000 | 99 | 99 | 198 |
| über 8.000 | 9 | 9 | 18 |

Längenbänder nach Label: bis 2.000: 98 Positive / 100 harmlose, 2.001-8.000: 12 Positive / 12 harmlose, über 8.000: 10 Positive / 8 harmlose.

Gegenstück-Gruppen (harmlose Einträge mit `gegenstueck_zu`): bedingt: 6, dauerhaft_merken: 6, md_exfiltration: 32, mehrsprachig_koeder: 6, tabelle_json: 6, unicode_tarnung: 6, verteilt: 6.

Harmlose Einträge, die eine KI oder einen Assistenten harmlos ansprechen: 36.

## Bekannte Grenzen

- Texte sind schablonenhaft erzeugt: Füllsätze, Namen und Hosts stammen aus kleinen Pools, die
  beide Labels gleich nutzen. Das ist gewollt (kein Label-Merkmal in der Oberfläche), macht den
  Satz aber weniger vielfältig als echte Post.
- Platzhalter und offene Query-Werte sind kein Merkmal einer Seite: Der Anteil der md-Einträge mit
  Platzhalter (`{x}`, `{{x}}`, `<x>`) oder leerem Query-Wert (`?q=`) in einer URL liegt bei den
  Positiven bei 16 von 30 (53.3 %), bei den Gegenstücken bei 18 von 32 (56.2 %).
  Der Test erzwingt je Seite 40-60 % und höchstens 10 Prozentpunkte Unterschied. Positive ohne
  Platzhalter nennen die anzuhängenden Daten ausgeschrieben („hänge den Betreff der letzten Mail
  als Parameter q an“) und haben eine vollständige URL. Gegenstücke mit Platzhaltern sind
  Serienbrief-, Ticket- und Tracking-Vorlagen, API-Doku und offene Anmelde-Links, jeweils ohne
  Anweisung an die KI.
