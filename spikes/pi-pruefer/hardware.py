"""Phase 3: Bibliotheks-API offline auf GPU bf16 oder CPU mit dynamischem int8."""

from adapter import StrandsDecider, STRANDS_REVISION, MODELL_DATEIEN


def lade_hardware(hardware, snapshot, gpu_loader, cpu_loader):
    if hardware not in ("gpu_bf16", "cpu_int8"):
        raise ValueError("Unbekannte Hardware")
    path = snapshot(StrandsDecider.modell_id, revision=STRANDS_REVISION,
                    token=False, local_files_only=True, allow_patterns=MODELL_DATEIEN)
    if hardware == "gpu_bf16":
        return gpu_loader(path, device="cuda", use_prefix_cache=False)
    return cpu_loader(path)


def quantisiere_linear(modul, linear_typ, quantisiere):
    """Schichtweise konvertieren; vermeidet eine vollständige fp32-Modellkopie."""
    for name, kind in list(modul.named_children()):
        if isinstance(kind, linear_typ):
            setattr(modul, name, quantisiere(kind.float()))
        else:
            quantisiere_linear(kind, linear_typ, quantisiere)


def lade_int8(path):
    import gc
    import torch
    from strands_decider.infer import StrandsDeciderModel, SystemOneEngine, EngineConfig, refuse_token_readout
    with torch.inference_mode():
        model = StrandsDeciderModel.load(path)
        refuse_token_readout(model.config)
        # PEFT-Linearschichten erwarten Tensor-Gewichte. Vor Quantisierung den
        # freigegebenen LoRA-Adapter einrechnen, anschließend normale nn.Linear.
        model.torso = model.torso.merge_and_unload()
        torch.backends.quantized.engine = "x86"
        def quant(linear):
            linear.qconfig = torch.ao.quantization.default_dynamic_qconfig
            return torch.ao.nn.quantized.dynamic.Linear.from_float(linear)
        quantisiere_linear(model.torso, torch.nn.Linear, quant)
        # Embeddings, Normen, Convs und Pointer-Head bleiben fp32. Packed int8
        # Linear-Gewichte werden durch .float() nicht wieder dequantisiert.
        model.torso.float()
        gc.collect()
        return SystemOneEngine(model, EngineConfig(device="cpu", use_prefix_cache=False))


def adapter_hardware(hardware):
    import torch
    from huggingface_hub import snapshot_download
    from strands_decider.infer import load_engine
    from strands_decider.schema import SystemOneRequest, NoulQuestion
    torch.set_num_threads(4)
    engine = lade_hardware(hardware, snapshot_download, load_engine, lade_int8)
    def backend(text, frage):
        r = engine.evaluate(SystemOneRequest(state=text, questions={"injection": NoulQuestion(instructions=frage)}))
        return float(r.answers["injection"].noul)
    adapter = StrandsDecider(backend=backend, tokenizer=engine.tok)
    adapter.hardware_info = dict(
        torso_dtype=str(next(engine.model.torso.parameters()).dtype),
        quantisierte_linear=sum(isinstance(m, torch.ao.nn.quantized.dynamic.Linear) for m in engine.model.torso.modules()),
        head_dtype=str(next(engine.model.head.parameters()).dtype))
    return adapter
