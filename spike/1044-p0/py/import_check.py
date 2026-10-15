"""Spike #1044 P0 (embeddable-pywinauto): Importprobe; Aufruf mit python.exe -I import_check.py [dll_dir]."""
import sys

if len(sys.argv) > 1 and sys.argv[1]:
    import os
    os.add_dll_directory(sys.argv[1])

import pywinauto, win32api, comtypes  # noqa: E401,E402
from pywinauto import Application  # noqa: F401,E402
import pywintypes, pythoncom  # noqa: E401,E402

print("OK pywinauto", pywinauto.__version__, "| comtypes", comtypes.__version__,
      "| site geladen:", "site" in sys.modules, "| isolated:", sys.flags.isolated)
print("win32api:", win32api.__file__)
print("pywintypes:", pywintypes.__file__)
