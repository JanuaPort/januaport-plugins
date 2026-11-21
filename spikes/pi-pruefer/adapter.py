"""Phase-1-Adapter: keine Modellimporte, nur Schlagwort ist implementiert."""

import math
import re


class Adapter:
    modell_id = ""

    def __init__(self, schwellwert=.5):
        if not math.isfinite(schwellwert) or not 0 <= schwellwert <= 1:  # MUTATION schwellwert
            raise ValueError("schwellwert muss endlich und zwischen 0 und 1 sein")
        self.schwellwert = schwellwert

    def klassifiziere(self, texte: list[str]) -> list[int]:
        raise NotImplementedError(f"Phase 2: Adapter {self.modell_id} ist ein Stub")  # MUTATION phase1

    def token_spannen(self, text: str) -> list[tuple[int, int]]:
        """Phase 2: Offsets des jeweiligen Modelltokenizers ohne Spezialtokens liefern."""
        raise NotImplementedError(f"Phase 2: Tokenizer für {self.modell_id}")


class PromptGuard22M(Adapter):
    modell_id = "meta-llama/Llama-Prompt-Guard-2-22M"
    # TODO Phase 2 nach SEC: Revision/Lizenz, Labelabbildung und CPU-Inferenz implementieren.


class PromptGuard86M(Adapter):
    modell_id = "meta-llama/Llama-Prompt-Guard-2-86M"
    # TODO Phase 2 nach SEC: Revision/Lizenz, Labelabbildung und CPU-Inferenz implementieren.


class ProtectAI(Adapter):
    modell_id = "protectai/deberta-v3-base-prompt-injection-v2"
    # TODO Phase 2 nach SEC: Revision pinnen, CPU-Inferenz und native Tokenoffsets implementieren.


class LLMGuard(Adapter):
    modell_id = "LLM Guard PromptInjection / protectai/deberta-v3-base-prompt-injection-v2"
    # TODO Phase 2 nach SEC: llm-guard pinnen, Scanner-Score/Schwelle und Tokenizer anbinden.


class Schlagwort(Adapter):
    modell_id = "synthetische-wortliste-v1"
    woerter = ("ignore previous", "ignore all", "ignoriere", "system prompt", "systemprompt",
               "system message", "systemnachricht", "send secrets", "geheimnisse",
               "assistant:", "assistent:", "developer message", "base64")

    def klassifiziere(self, texte: list[str]) -> list[int]:
        # Fester Score .75 bei Treffer, sonst .05; absichtlich keine gute Erkennung.
        return [int((.75 if any(w in t.casefold() for w in self.woerter) else .05)
                    >= self.schwellwert) for t in texte]

    def token_spannen(self, text):
        # Ausschließlich Dummy-Tokenisierung; keine Behauptung über Modelltoken.
        return [m.span() for m in re.finditer(r"\w+|[^\w\s]", text)]


KANDIDATEN = {"schlagwort": Schlagwort, "prompt_guard_22m": PromptGuard22M,
              "prompt_guard_86m": PromptGuard86M, "protectai": ProtectAI, "llm_guard": LLMGuard}
