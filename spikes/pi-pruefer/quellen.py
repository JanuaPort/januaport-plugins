"""Öffentlich gelesene Herstellerangaben; keine eigenen Messwerte."""

HERSTELLER = r'''
## Meta Prompt Guard 2: veröffentlichte Herstellerangaben, keine eigene Messung

Öffentlich ohne Anmeldung am 22.11.2026 gelesen. Fremder privater Benchmark,
keine Werte unseres Messsatzes, keine CPU-Latenzmessung und keine Freigabe.
Quelle aller folgenden Benchmarkwerte:
https://raw.githubusercontent.com/meta-llama/PurpleLlama/main/Llama-Prompt-Guard-2/22M/MODEL_CARD.md

Wörtlicher Tabellenkopf und Tabellenzeilen (englischer Recall bei 1 % FPR;
Latenz auf A100-GPU bei 512 Tokens):

> | Model | AUC (English) | Recall @ 1% FPR (English) | AUC (Multilingual) | Latency per classification (A100 GPU, 512 tokens) | Backbone Parameters | Base Model |
> | Llama Prompt Guard 2 86M | **.998** | **97.5%** | **.995** | 92.4 ms | 86M | mdeberta-v3 |
> | Llama Prompt Guard 2 22M | **.995** | **88.7%** | .942 | 19.3 ms | 22M | deberta-v3-xsmall |

Zur Benchmarkherkunft, dieselbe URL, wörtlich:

> To assess Prompt Guard's ability to identify jailbreak techniques in realistic settings, we used a private benchmark built with datasets distinct from those used in training Prompt Guard. This setup was specifically designed to test the generalization of Prompt Guard models to previously unseen attack types and distributions of benign data.

Sprachen und Kontext, dieselbe URL, wörtlich:

> **Context length**: Both Llama Prompt Guard 2 models support a 512-token context window. For longer inputs, split prompts into segments and scan them in parallel to ensure violations are detected.

> **Multilingual support**: Llama Prompt Guard 2 86M uses a multilingual base model and is trained to detect both English and non-English injections and jailbreaks. Both Prompt Guard 2 models have been evaluated for attack detection in English, French, German, Hindi, Italian, Portuguese, Spanish, and Thai.

Eine sprachspezifische DE-FPR oder CPU-Latenz für unsere Box bleibt offen.
Die Sprachliste bezeichnet Evaluation, keine Zusicherung gleicher Qualität.
Der kleine Backbone ist laut Karte nicht multilingual vortrainiert.

Lizenzquelle:
https://raw.githubusercontent.com/meta-llama/PurpleLlama/main/Llama-Prompt-Guard-2/README.md

> The same license as Llama 4 applies: see the [LICENSE](../LICENSE) file, as well as our accompanying [Acceptable Use Policy](86M/USE_POLICY.md).

Der relative LICENSE-Link führt aktuell auf die ältere Root-Lizenz. Der konkrete
Modellordner enthält dagegen die passende Community-Lizenz:
https://raw.githubusercontent.com/meta-llama/PurpleLlama/main/Llama-Prompt-Guard-2/86M/LICENSE

> LLAMA 4 COMMUNITY LICENSE AGREEMENT

Aus Ziffer 1.b.i, wörtliche Auszüge derselben URL:

> provide a copy of this Agreement with any such Llama Materials

> prominently display “Built with Llama” on a related website, user interface, blogpost, about page, or product documentation.

Aus Ziffer 1.b.iv, wörtlicher Auszug derselben URL:

> adhere to the Acceptable Use Policy for the Llama Materials

Aus Ziffer 2, wörtlich, dieselbe URL:

> If, on the Llama 4 version release date, the monthly active users of the products or services made available by or for Licensee, or Licensee’s affiliates, is greater than 700 million monthly active users in the preceding calendar month, you must request a license from Meta, which Meta may grant to you in its sole discretion, and you are not authorized to exercise any of the rights under this Agreement unless or until Meta otherwise expressly grants you such rights.

Die AUP ist öffentlich lesbar unter
https://raw.githubusercontent.com/meta-llama/PurpleLlama/main/Llama-Prompt-Guard-2/86M/USE_POLICY.md
und untersagt unter anderem rechtswidrige/gefährliche Nutzung; ihre EU-Klausel
bezieht sich ausdrücklich auf enthaltene multimodale Modelle. Daraus wird hier
keine rechtliche Einordnung dieses Textklassifikators abgeleitet.
**Offen: PO-Entscheid zur Lizenzannahme.** HF-Zugang ist gesperrt; kein Login,
kein Token, kein Lizenzklick, keine Meta-Gewichte geladen. Beide Adapter bleiben Stubs.

## Weitere Decision-Modelle: nicht gemessen

Cloudflare-Primärquelle, öffentlich ohne Anmeldung gelesen:
https://blog.cloudflare.com/clef-decision-models/

> We’re fully open-sourcing these models on Hugging Face under an Apache 2.0 license for you to run locally and experiment with yourselves.

> By freezing Qwen3.8-27B for Clef and Qwen3.5-9B for Clef-flash, we jointly optimized the routing head alongside rank-256 low-rank adapters.

Die genannten Basisgrößen überschreiten das CPU-/RAM-Ziel dieses Spikes;
kein Download und keine Messung. TypeSafe Jev ist gemäß PO-Auftrag als
geschlossene API ausgeschlossen, weil Inhalte die Anlage verlassen würden.
Öffentliche Anbieterquelle https://www.typesafe.ai nennt wörtlich:

> Try our first System One Model, Jev, in early access.

Die Anbieter-Dokumentation https://docs.typesafe.ai/introduction bezeichnet sich
in den Seitenmetadaten wörtlich als:

> How to use TypeSafe's System One API

Ein lokaler Gewichtsbezug ist damit nicht belegt; es erfolgt kein API-Aufruf mit Messdaten.
Lev/InterfazeAI ist an Primärquellen nicht bestätigt und wird nicht aufgenommen.
'''
