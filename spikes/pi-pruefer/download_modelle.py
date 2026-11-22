"""Einmaliger anonymer Download fester Revisionen ins benannte Docker-Volume."""

import argparse
from adapter import MODELL_REVISION, STRANDS_REVISION, BASIS_REVISION, ProtectAI, StrandsDecider


def main():
    from huggingface_hub import snapshot_download
    p = argparse.ArgumentParser()
    p.add_argument("kandidat", choices=("protectai", "strands_decider"))
    args = p.parse_args()
    modelle = [(ProtectAI.modell_id, MODELL_REVISION)] if args.kandidat == "protectai" else [
        (StrandsDecider.modell_id, STRANDS_REVISION), (StrandsDecider.basis_id, BASIS_REVISION)]
    for name, revision in modelle:
        path = snapshot_download(name, revision=revision, token=False, max_workers=2,
            allow_patterns=["config.json", "tokenizer*", "special_tokens_map.json", "added_tokens.json",
                            "spm.model", "vocab.*", "merges.txt", "chat_template.jinja", "model*.safetensors", "model*.json",
                            "strands_decider_config.json", "head.safetensors", "lora/*.json",
                            "lora/*.safetensors", "provenance.json", "LICENSE*"])
        print(f"{name} @ {revision}: {path}", flush=True)


if __name__ == "__main__":
    main()
