"""CPU-Messgerüst. Ausgabe: Markdown; jeder Kandidat in frischem Linux-Prozess."""

import argparse
import hashlib
import json
import multiprocessing
import os
from pathlib import Path
import platform
import resource
import sys
import time

from adapter import KANDIDATEN
from datenregeln import lade


def fenster(text, adapter, groesse, ueberlappung):
    if groesse <= 0 or ueberlappung < 0 or ueberlappung >= groesse:  # MUTATION fensterparameter
        raise ValueError("fensterparameter: 0 <= Überlappung < Fenstergröße")
    spannen = adapter.token_spannen(text)
    if len(spannen) <= groesse:
        return [text]
    teile = []
    for start in range(0, len(spannen), groesse - ueberlappung):
        ende = min(start + groesse, len(spannen))
        links = 0 if start == 0 else spannen[start][0]
        rechts = len(text) if ende == len(spannen) else spannen[ende][0]
        teile.append(text[links:rechts])
        if ende == len(spannen):
            break
    return teile


def vorhersage(adapter, text, groesse, ueberlappung):
    ergebnis = adapter.klassifiziere(fenster(text, adapter, groesse, ueberlappung))
    return max(ergebnis)


def quantil(werte, p):
    werte = sorted(werte)
    position = (len(werte) - 1) * p
    links = int(position)
    rechts = min(links + 1, len(werte) - 1)
    return werte[links] + (werte[rechts] - werte[links]) * (position - links)


def metriken(daten, prognosen, dauern):
    tp = sum(z["label"] == 1 and p == 1 for z, p in zip(daten, prognosen))
    fp = sum(z["label"] == 0 and p == 1 for z, p in zip(daten, prognosen))
    positiv = sum(z["label"] == 1 for z in daten)
    negativ = len(daten) - positiv
    hart = sum(z["klasse"] == "hart_harmlos" for z in daten)
    fp_hart = sum(z["klasse"] == "hart_harmlos" and p == 1 for z, p in zip(daten, prognosen))
    normiert = [ms * 10000 / len(z["text"]) for ms, z in zip(dauern, daten)]
    return {"recall": tp / positiv if positiv else None,
            "fpr": fp / negativ if negativ else None,
            "fpr_hart": fp_hart / hart if hart else None,
            "precision": tp / (tp + fp) if tp + fp else None,
            "p50": quantil(dauern, .5), "p95": quantil(dauern, .95),
            "p50_10k": quantil(normiert, .5), "p95_10k": quantil(normiert, .95)}


def arbeiter(kanal, name, daten, schwellwert, groesse, ueberlappung):
    try:
        # Phase-2-Adapter müssen zusätzlich device='cpu' explizit setzen.
        os.environ["CUDA_VISIBLE_DEVICES"] = ""
        os.environ["OMP_NUM_THREADS"] = "1"
        os.environ["MKL_NUM_THREADS"] = "1"
        start = time.perf_counter()
        adapter = KANDIDATEN[name](schwellwert)
        laden_ms = (time.perf_counter() - start) * 1000
        adapter.klassifiziere(["Aufwärmen / warm-up"])
        prognosen, dauern = [], []
        for z in daten:
            start = time.perf_counter_ns()
            prognosen.append(vorhersage(adapter, z["text"], groesse, ueberlappung))
            dauern.append((time.perf_counter_ns() - start) / 1e6)
        ram_mib = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss / 1024
        kanal.send({"prognosen": prognosen, "dauern": dauern, "ram": ram_mib,
                    "laden_ms": laden_ms})
    except Exception as fehler:
        kanal.send({"fehler": f"{type(fehler).__name__}: {fehler}"})
    finally:
        kanal.close()


def prozess_messen(name, daten, schwellwert, groesse, ueberlappung):
    ctx = multiprocessing.get_context("spawn")
    empfang, senden = ctx.Pipe(duplex=False)
    prozess = ctx.Process(target=arbeiter,
                          args=(senden, name, daten, schwellwert, groesse, ueberlappung))
    prozess.start()
    senden.close()
    try:
        ergebnis = empfang.recv()
    finally:
        empfang.close()
        prozess.join()
    if "fehler" in ergebnis:
        raise RuntimeError(ergebnis["fehler"])
    return ergebnis


def tabelle(name, daten, result):
    gruppen = [("gesamt", list(range(len(daten))))]
    for feld in ("sprache", "form"):
        for wert in sorted({z[feld] for z in daten}):
            gruppen.append((f"{feld}:{wert}", [i for i, z in enumerate(daten) if z[feld] == wert]))
    zeilen = []
    for gruppe, indizes in gruppen:
        teil = [daten[i] for i in indizes]
        m = metriken(teil, [result["prognosen"][i] for i in indizes],
                     [result["dauern"][i] for i in indizes])
        werte = ["n/a" if m[k] is None else f"{m[k] * 100:.2f}%" for k in
                 ("recall", "fpr", "fpr_hart", "precision")]
        werte += [f"{m[k]:.4f}" for k in ("p50", "p95", "p50_10k", "p95_10k")]
        zeilen.append("| " + " | ".join([name, gruppe, str(len(teil)), *werte,
                                         f"{result['ram']:.2f}"]) + " |")
    return zeilen


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--messsatz", type=Path, default=Path(__file__).with_name("messsatz.jsonl"))
    parser.add_argument("--kandidat", action="append", choices=KANDIDATEN)
    parser.add_argument("--schwellwert", action="append", default=[], metavar="KANDIDAT=WERT")
    parser.add_argument("--fenster-tokens", type=int, default=256)
    parser.add_argument("--ueberlappung-tokens", type=int, default=32)
    args = parser.parse_args()
    try:
        daten = lade(args.messsatz)
        schwellen = {name: .5 for name in KANDIDATEN}
        for angabe in args.schwellwert:
            name, wert = angabe.split("=", 1)
            if name not in KANDIDATEN:
                parser.error(f"Unbekannter Kandidat: {name}")
            schwellen[name] = float(wert)
        ergebnisse = []
        for name in args.kandidat or ["schlagwort"]:
            ergebnisse.append((name, prozess_messen(name, daten, schwellen[name],
                                                   args.fenster_tokens, args.ueberlappung_tokens)))
    except (ValueError, RuntimeError) as fehler:
        parser.exit(2, f"Messung abgebrochen: {fehler}\n")
    print(f"Messsatz SHA-256: {hashlib.sha256(args.messsatz.read_bytes()).hexdigest()}")
    print(f"Python {platform.python_version()}, {platform.platform()}, CPU {platform.machine()}, "
          f"sichtbare CPUs {os.cpu_count()}, Threads 1, Warm-up 1, Texte {len(daten)}.")
    print(f"Fenster {args.fenster_tokens} Tokens, Überlappung {args.ueberlappung_tokens} Tokens; OR.")
    for name, result in ergebnisse:
        print(f"{name}: Modell {KANDIDATEN[name].modell_id}, Schwelle {schwellen[name]}, "
              f"Adapterinitialisierung {result['laden_ms']:.4f} ms.")
    print()
    print("| Kandidat | Gruppe | n | Recall | FPR | FPR hart | Precision | p50 ms | p95 ms | "
          "p50 ms/10k | p95 ms/10k | RAM MiB |")
    print("|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|")
    for name, result in ergebnisse:
        print("\n".join(tabelle(name, daten, result)))


if __name__ == "__main__":
    main()
