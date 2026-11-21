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
    ]
    for name, datei, original, mutation, test in proben:
        with tempfile.TemporaryDirectory(prefix="pi-mutation-") as tmp:
            ziel = Path(tmp) / "spike"
            shutil.copytree(ROOT, ziel, ignore=shutil.ignore_patterns("__pycache__"))
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
