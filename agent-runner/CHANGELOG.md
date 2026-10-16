# Changelog

## 0.1.0 — 2026-10-17

**Gebaut, Live-Durchstich ausstehend.** JanuaPort/januaport#1099 B1.

- MCP-Beisteller mit genau `run_agent`, runtime-key und Lauf-Token.
- Anthropic-Messages-Schleife über Modell- und MCP-Durchgang des Kerns,
  angehängte Thinking-Historie, Cache-Marken und sichere Werkzeugprojektion.
- Lokale Schritt-/Aufrufgrenzen, Context-Wandzeit und Abbruch, Meldung 300 s.
- Feste Fehlertexte und inhaltfreies Lauf-Protokoll.
- Sieben unveränderte Kern-Goldens sowie separate Vorschläge für Fehlerenden.
- Internes Netz, read-only, distroless/static als Non-Root.
- Fake-Kern-Tests, Negativ- und Mutationsproben; Prüfbericht im Gate-Bericht auf JanuaPort/januaport#1099.

Kein veröffentlichtes Image, kein Live-Modellaufruf, kein Katalogeintrag.
Nur Anthropic: Ein Profil auf einer openai-kompatiblen Modell-Anbindung scheitert in 0.1.0 (der Kern weist die Anfrage ab). OpenAI-Adapter, Zeitplanung und GUI sind nicht Bestandteil von B1.
