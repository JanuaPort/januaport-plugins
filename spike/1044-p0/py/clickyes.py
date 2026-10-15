"""Spike #1044 P0 (go-tls-box-ca, c3): bestaetigt den Windows-Sicherheitsdialog beim Import einer
Stamm-CA in den Benutzer-Store per UIA. Aufruf: python.exe -I clickyes.py [dll_dir].
Gibt eine Zeile aus: CLICKED ... oder NODIALOG ... (mit den gesehenen Fenstern)."""
import os
import sys
import time

sys.stdout.reconfigure(encoding="utf-8", errors="replace")
if len(sys.argv) > 1 and sys.argv[1]:
    os.add_dll_directory(sys.argv[1])

from pywinauto import Desktop  # noqa: E402

KEYS = ("security warning", "sicherheitswarnung", "sicherheit")
deadline = time.time() + 30
seen = set()
result = None
while time.time() < deadline and result is None:
    for w in Desktop(backend="uia").windows():
        try:
            title, cls = w.window_text(), w.class_name()
        except Exception:
            continue
        seen.add(f"{cls}:{title}")
        if not any(k in title.lower() for k in KEYS):
            continue
        buttons = []
        for b in w.descendants(control_type="Button"):
            name = b.window_text()
            buttons.append(name)
            if name.strip().lower().strip("&") in ("yes", "ja"):
                try:
                    b.invoke()
                    result = f"CLICKED dialog={title!r} button={name!r}"
                except Exception as e:  # noqa: BLE001
                    result = f"DIALOG-GEFUNDEN-ABER-KLICK-FEHLER {type(e).__name__}: {e} buttons={buttons}"
                break
        else:
            result = f"DIALOG-OHNE-JA-KNOPF dialog={title!r} buttons={buttons}"
    time.sleep(0.5)
print(result or f"NODIALOG nach 30 s; gesehene Fenster: {sorted(seen)[:25]}")
