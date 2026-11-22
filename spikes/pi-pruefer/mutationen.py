"""Sperren einzeln in Container-/tmp-Kopien entfernen; jeder Negativtest muss rot werden."""

from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

from datenregeln import REGELN

ROOT = Path(__file__).resolve().parent


def main():
    proben = [(regel, "datenregeln.py", f'"{regel}": True,', f'"{regel}": False,',
               f"test_messsatz.DatenSperrenTests.test_{regel}") for regel in REGELN]
    proben += [
        ("phase1", "adapter.py",
         'raise NotImplementedError(f"Phase 2: Adapter {self.modell_id} ist ein Stub")',
         'return [0] * len(texte)', "test_messsatz.GeruestTests.test_phase1_sperre"),
        ("schwellwert", "adapter.py",
         'if not math.isfinite(schwellwert) or not 0 <= schwellwert <= 1:',
         'if False:', "test_messsatz.GeruestTests.test_schwellwert"),
        ("phase1_tokenizer", "adapter.py",
         'raise NotImplementedError(f"Phase 2: Tokenizer für {self.modell_id}")',
         'return [(0, len(text))]', "test_messsatz.GeruestTests.test_phase1_tokenizer_sperre"),
        ("fensterparameter", "messen.py",
         'if groesse <= 0 or ueberlappung < 0 or ueberlappung >= groesse:',
         'if False:', "test_messsatz.GeruestTests.test_fensterparameter"),
        ("fenster_or", "messen.py", 'return max(ergebnis)', 'return min(ergebnis)',
         "test_messsatz.GeruestTests.test_fenster_or_und_ende"),
        ("unbekannte_erweiterung", "erweiterungen.py",
         'raise ValueError(f"Unbekannte Erweiterung: {klasse}")', 'return ("", "")',
         "test_messsatz.MesssatzTests.test_unbekannte_erweiterung"),
        ("auswertung_klassen", "messen.py",
         '("sprache", "form", "klasse", "gegenstueck_zu")', '("sprache", "form", "gegenstueck_zu")',
         "test_messsatz.GeruestTests.test_klassen_und_gegenstueck_metriken"),
        ("auswertung_gegenstuecke", "messen.py",
         '("sprache", "form", "klasse", "gegenstueck_zu")', '("sprache", "form", "klasse")',
         "test_messsatz.GeruestTests.test_klassen_und_gegenstueck_metriken"),
        ("md_schwerpunkt", "messen.py",
         'gruppen.append(("**md_exfiltration (Schwerpunkt)**", schwerpunkt))', 'pass',
         "test_messsatz.GeruestTests.test_klassen_und_gegenstueck_metriken"),
        ("md_wege_auswertung", "messen.py",
         'gruppen.append((f"md_rueckgabeweg:{weg}", indizes))', 'pass',
         "test_messsatz.GeruestTests.test_dummy_tabelle"),
        ("strukturierte_felder", "erweiterungen.py",
         'antwort["result"]["structuredContent"] = felder', 'antwort["result"]["verloren"] = felder',
         "test_messsatz.MesssatzTests.test_generator_reproduziert_messsatz"),
        ("scan_adressen", "README.md", '# Lokaler Prompt-Injection-Prüfer',
         'scanprobe@' + 'fremd.invalid\n# Lokaler Prompt-Injection-Prüfer',
         "test_messsatz.MesssatzTests.test_spike_adressen_und_iban"),
        ("scan_iban", "README.md", '# Lokaler Prompt-Injection-Prüfer',
         'DE' + '00' + '0' * 18 + '\n# Lokaler Prompt-Injection-Prüfer',
         "test_messsatz.MesssatzTests.test_spike_adressen_und_iban"),
        ("scan_inline_ausnahme", "README.md", '# Lokaler Prompt-Injection-Prüfer',
         'gitleaks' + ':allow\n# Lokaler Prompt-Injection-Prüfer',
         "test_messsatz.MesssatzTests.test_spike_adressen_und_iban"),
    ]
    proben += [
        ("modell_anzahl", "adapter.py", 'if len(werte) != anzahl:', 'if False:',
         "test_phase2.AdapterTests.test_score_vertrag"),
        ("modell_score", "adapter.py", 'if any(not math.isfinite(s) or not 0 <= s <= 1 for s in werte):', 'if False:',
         "test_phase2.AdapterTests.test_score_vertrag"),
        ("modell_label", "adapter.py", 'if row["label"] not in {"INJECTION", "SAFE"}:', 'if False:',
         "test_phase2.AdapterTests.test_protectai_unbekanntes_label"),
        ("scanner_revision", "adapter.py", 'if revision != MODELL_REVISION:', 'if False:',
         "test_phase2.AdapterTests.test_scanner_revision_abweichung"),
        ("modell_pin", "adapter.py", 'dict(revision=MODELL_REVISION, token=False, local_files_only=True, trust_remote_code=False)',
         'dict(revision="main", token=False, local_files_only=True, trust_remote_code=False)',
         "test_phase2.AdapterTests.test_lokaler_loader_und_feste_revision"),
        ("offline", "adapter.py", 'dict(revision=MODELL_REVISION, token=False, local_files_only=True, trust_remote_code=False)',
         'dict(revision=MODELL_REVISION, token=False, local_files_only=False, trust_remote_code=False)',
         "test_phase2.AdapterTests.test_lokaler_loader_und_feste_revision"),
        ("kein_token", "adapter.py", 'dict(revision=MODELL_REVISION, token=False, local_files_only=True, trust_remote_code=False)',
         'dict(revision=MODELL_REVISION, token=True, local_files_only=True, trust_remote_code=False)',
         "test_phase2.AdapterTests.test_lokaler_loader_und_feste_revision"),
        ("kein_remote_code", "adapter.py", 'dict(revision=MODELL_REVISION, token=False, local_files_only=True, trust_remote_code=False)',
         'dict(revision=MODELL_REVISION, token=False, local_files_only=True, trust_remote_code=True)',
         "test_phase2.AdapterTests.test_lokaler_loader_und_feste_revision"),
        ("scanner_score", "adapter.py", 'werte.append(max(self._erfasst) if self._erfasst else 0.)', 'werte.append(.9)',
         "test_phase2.AdapterTests.test_scanner_wird_aufgerufen_und_risiko_nicht_verwechselt"),
        ("strands_frage", "adapter.py", 'self.backend(t, STRANDS_FRAGE)', 'self.backend(t, "Andere Frage")',
         "test_phase2.AdapterTests.test_strands_feste_frage_und_noul"),
        ("phase2_fenster_or", "phase2.py", 'return max(scores), len(teile), budget', 'return min(scores), len(teile), budget',
         "test_phase2.AuswertungTests.test_fenster_versus_abgeschnitten"),
        ("kalibrierung", "phase2.py", 'if fp / len(hart) <= .05:', 'if True:',
         "test_phase2.AuswertungTests.test_schwelle_aus_harten_negativen_inkl_gleichstand"),
        ("a3_gruppe", "phase2.py", 'if z["klasse"] == "hart_harmlos" and re.search(', 'if False and re.search(',
         "test_phase2.AuswertungTests.test_a3_und_lange_texte_separat"),
        ("rohtexte", "phase2.py", '"id": z["id"], "kandidat": name', '"text": z["text"], "id": z["id"], "kandidat": name',
         "test_phase2.AuswertungTests.test_rohdaten_enthalten_keine_texte"),
    ]
    proben += [
        ("strands_offline_filter", "adapter.py", 'allow_patterns=MODELL_DATEIEN', 'allow_patterns=None',
         "test_phase2.AdapterTests.test_strands_offline_loader_mit_download_filter"),
        ("strands_offline", "adapter.py", 'token=False, local_files_only=True, allow_patterns=MODELL_DATEIEN',
         'token=False, local_files_only=False, allow_patterns=MODELL_DATEIEN',
         "test_phase2.AdapterTests.test_strands_offline_loader_mit_download_filter"),
        ("strands_token", "adapter.py", 'token=False, local_files_only=True, allow_patterns=MODELL_DATEIEN',
         'token=True, local_files_only=True, allow_patterns=MODELL_DATEIEN',
         "test_phase2.AdapterTests.test_strands_offline_loader_mit_download_filter"),
        ("strands_revision", "adapter.py", 'StrandsDecider.modell_id, revision=STRANDS_REVISION',
         'StrandsDecider.modell_id, revision="main"',
         "test_phase2.AdapterTests.test_strands_offline_loader_mit_download_filter"),
        ("strands_cpu", "adapter.py", 'engine_loader(path, device="cpu", use_prefix_cache=False)',
         'engine_loader(path, device="cuda", use_prefix_cache=False)',
         "test_phase2.AdapterTests.test_strands_offline_loader_mit_download_filter"),
        ("strands_prefix_cache", "adapter.py", 'engine_loader(path, device="cpu", use_prefix_cache=False)',
         'engine_loader(path, device="cpu", use_prefix_cache=True)',
         "test_phase2.AdapterTests.test_strands_offline_loader_mit_download_filter"),
    ]
    proben += [
        ("holdout_vertrag", "messen.py", 'if not result.wasSuccessful():', 'if False:',
         "test_phase3.VertragTests.test_vertragsbruch_wird_abgewiesen"),
        ("holdout_unbekannter_vertrag", "messen.py", 'if vertrag != "satz2":', 'if False:',
         "test_phase3.VertragTests.test_eigener_vertrag"),
        ("holdout_kalibrierung", "phase2.py", 'if any(z.get("id", "").startswith("h-") for z in daten):', 'if False:',
         "test_phase3.VertragTests.test_keine_holdout_kalibrierung"),
        ("holdout_feste_schwelle", "phase3.py", '(.5, .1976), "protectai":', '(.5, .2), "protectai":',
         "test_phase3.AuswertungTests.test_feste_schwellen_und_gleichstand"),
        ("holdout_gleichstand", "phase3.py", 'return [int(s >= schwelle) for s in scores]', 'return [int(s > schwelle) for s in scores]',
         "test_phase3.AuswertungTests.test_feste_schwellen_und_gleichstand"),
        ("holdout_ki_regex", "phase3.py", 'and ki.search(z["text"])', 'and True',
         "test_phase3.AuswertungTests.test_gruppen_exakte_regex_und_langes_band"),
        ("holdout_laengenband", "phase3.py", 'if len(z["text"]) > 2000]', 'if len(z["text"]) > 8000]',
         "test_phase3.AuswertungTests.test_gruppen_exakte_regex_und_langes_band"),
        ("holdout_md_gegenstuecke", "phase3.py", 'and z.get("gegenstueck_zu") == "md_exfiltration"', 'and True',
         "test_phase3.AuswertungTests.test_gruppen_exakte_regex_und_langes_band"),
        ("holdout_rohtexte", "phase3.py", 'roh.append(dict(id=z["id"], kandidat=', 'roh.append(dict(text=z["text"], id=z["id"], kandidat=',
         "test_phase3.AuswertungTests.test_bericht_ohne_texte_ohne_kleine_klassenraten"),
        ("holdout_klassenraten", "phase3.py", 'tabellen += [f"###', 'tabellen += ["klasse:bedingt", f"###',
         "test_phase3.AuswertungTests.test_bericht_ohne_texte_ohne_kleine_klassenraten"),
        ("holdout_sha", "phase3.py", 'if r["messsatz_sha256"] != sha:', 'if False:',
         "test_phase3.AuswertungTests.test_fremde_oder_unvollstaendige_resultate_abgewiesen"),
        ("holdout_vollstaendig", "phase3.py", 'if any(len(r[k]) != len(daten) for k in ("scores", "dauern", "fenster", "budget")):', 'if False:',
         "test_phase3.AuswertungTests.test_fremde_oder_unvollstaendige_resultate_abgewiesen"),
        ("hardware_unbekannt", "hardware.py", 'if hardware not in ("gpu_bf16", "cpu_int8"):', 'if False:',
         "test_phase3.HardwareTests.test_offline_loader_gpu_und_int8"),
        ("hardware_offline", "hardware.py", 'token=False, local_files_only=True, allow_patterns=MODELL_DATEIEN', 'token=False, local_files_only=False, allow_patterns=MODELL_DATEIEN',
         "test_phase3.HardwareTests.test_offline_loader_gpu_und_int8"),
        ("hardware_token", "hardware.py", 'token=False, local_files_only=True, allow_patterns=MODELL_DATEIEN', 'token=True, local_files_only=True, allow_patterns=MODELL_DATEIEN',
         "test_phase3.HardwareTests.test_offline_loader_gpu_und_int8"),
        ("hardware_revision", "hardware.py", 'revision=STRANDS_REVISION', 'revision="main"',
         "test_phase3.HardwareTests.test_offline_loader_gpu_und_int8"),
        ("hardware_filter", "hardware.py", 'allow_patterns=MODELL_DATEIEN', 'allow_patterns=None',
         "test_phase3.HardwareTests.test_offline_loader_gpu_und_int8"),
        ("hardware_gpu", "hardware.py", 'device="cuda", use_prefix_cache=False', 'device="cpu", use_prefix_cache=False',
         "test_phase3.HardwareTests.test_offline_loader_gpu_und_int8"),
        ("hardware_prefix_cache", "hardware.py", 'device="cuda", use_prefix_cache=False', 'device="cuda", use_prefix_cache=True',
         "test_phase3.HardwareTests.test_offline_loader_gpu_und_int8"),
        ("hardware_int8", "hardware.py", 'setattr(modul, name, quantisiere(kind.float()))', 'setattr(modul, name, kind)',
         "test_phase3.HardwareTests.test_lineare_einzeln_quantisiert"),
    ]
    for key, original, mutation in (
        ("recall", 'm["recall"] >= .8', 'm["recall"] >= .9'),
        ("hart", 'm["fpr_hart"] <= .1', 'm["fpr_hart"] <= .2'),
        ("ki", 'm["fpr_ki"] <= .15', 'm["fpr_ki"] <= .2'),
        ("latenz", 'm["kurz_p95"] <= 2000', 'm["kurz_p95"] <= 3000')):
        proben.append(("erfolg_"+key, "phase3.py", original, mutation,
                       "test_phase3.AuswertungTests.test_erfolgskriterien_vorab_und_grenzen"))
    proben += [
        ("gpu_messsperre", "phase3.py", 'if hardware == "gpu_bf16":  # MUTATION gpu_messsperre', 'if False:',
         "test_phase3.HardwareTests.test_gpu_messung_bis_po_entscheidung_gesperrt"),
        ("gpu_berichtssperre", "phase3.py", 'if r["hardware"] == "gpu_bf16":', 'if False:',
         "test_phase3.HardwareTests.test_gpu_ergebnisse_nicht_im_cpu_befund"),
    ]
    for name, datei, original, mutation, test in proben:
        with tempfile.TemporaryDirectory(prefix="pi-mutation-") as tmp:
            ziel = Path(tmp) / "spike"
            shutil.copytree(ROOT, ziel, ignore=shutil.ignore_patterns("__pycache__"))
            shutil.copytree(ROOT.parent / "pi-pruefer-holdout", Path(tmp) / "pi-pruefer-holdout")
            pfad = ziel / datei
            inhalt = pfad.read_text(encoding="utf-8")
            if inhalt.count(original) != 1:
                raise RuntimeError(f"Mutationsstelle {name} nicht eindeutig")
            pfad.write_text(inhalt.replace(original, mutation), encoding="utf-8")
            result = subprocess.run([sys.executable, "-B", "-m", "unittest", test, "-v"],
                                    cwd=ziel, text=True, capture_output=True)
            # Nur erwartete Assertions zählen, keine Import-/Syntax-/Laufzeitfehler.
            if result.returncode == 0 or "FAIL:" not in result.stderr or "ERROR:" in result.stderr:
                print(result.stdout + result.stderr)
                raise RuntimeError(f"Mutation {name}: kein erwarteter roter Negativtest")
            print(f"Mutation {name}: ROT belegt (Assertion), Original unverändert", flush=True)
    print(f"{len(proben)}/{len(proben)} Mutationen erkannt; alle Originaldateien unverändert.")


if __name__ == "__main__":
    main()
