"""Spike #1044 P0 Teil 2: Bootstrap der Embeddable (ohne site). Wird als Erstes importiert."""
import os
import sys

_sp = os.path.join(os.path.dirname(sys.executable), "Lib", "site-packages")
_dll = os.path.join(_sp, "pywin32_system32")
if os.path.isdir(_dll):
    os.add_dll_directory(_dll)


def probe(outfile, name, ok, detail):
    line = f"PROBE {name} {'PASS' if ok else 'FAIL'} {' '.join(str(detail).split())}"
    with open(outfile, "a", encoding="utf-8") as f:
        f.write(line + "\n")
