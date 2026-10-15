"""Spike #1044 P0 Teil 2, Sonde motw-smartscreen: startet eine Exe auf zwei Wegen in der Benutzersitzung und
meldet neue sichtbare Fenster (Sicherheitsdialoge) und ob das Programm lief (hello-ran.txt waechst).
Aufruf (in der Sitzung): pythonw.exe -I motw_run.py <label> <exe> <createprocess|shellexecute|explorer> <ausgabedatei> [wartesekunden]"""
import ctypes
import os
import subprocess
import sys
import threading
import time
from ctypes import wintypes

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import boot  # noqa: E402

u32 = ctypes.WinDLL("user32", use_last_error=True)
MARKER = r"C:\spike\out\hello-ran.txt"
WNDENUMPROC = ctypes.WINFUNCTYPE(wintypes.BOOL, wintypes.HWND, wintypes.LPARAM)


def windows():
    found = {}

    def cb(hwnd, _):
        if u32.IsWindowVisible(hwnd):
            t = ctypes.create_unicode_buffer(200)
            c = ctypes.create_unicode_buffer(100)
            u32.GetWindowTextW(hwnd, t, 200)
            u32.GetClassNameW(hwnd, c, 100)
            if t.value:
                found[hwnd] = (c.value, t.value)
        return True

    u32.EnumWindows(WNDENUMPROC(cb), 0)
    return found


def lines():
    return len(open(MARKER).read().splitlines()) if os.path.exists(MARKER) else 0


def main():
    label, exe, method, out = sys.argv[1:5]
    wait = int(sys.argv[5]) if len(sys.argv) > 5 else 8
    before_w, before_l = windows(), lines()
    errs = []

    def launch():
        try:
            if method == "createprocess":
                subprocess.Popen([exe, label], creationflags=0x08000000)
            elif method == "explorer":
                subprocess.Popen(["explorer.exe", exe])  # der Shell den Start uebergeben; ein Dialog gehoert dann dem Explorer
            else:
                os.startfile(exe)  # ShellExecute wie ein Doppelklick im Explorer; blockiert bei einem Dialog
        except OSError as e:
            errs.append(f"{type(e).__name__}: {e}")

    t = threading.Thread(target=launch, daemon=True)
    t.start()
    time.sleep(wait)
    err = errs[0] if errs else ("ShellExecute blockiert (Dialog offen)" if t.is_alive() else "")
    new = [f"{c}:{t[:60]!r}" for h, (c, t) in windows().items() if h not in before_w]
    ran = lines() - before_l
    for h in [h for h in windows() if h not in before_w]:
        u32.PostMessageW(h, 0x0010, 0, 0)  # WM_CLOSE fuer Dialoge
    subprocess.run(["taskkill", "/F", "/IM", os.path.basename(exe)], capture_output=True, creationflags=0x08000000)
    # PASS = Programm lief ohne neues Fenster (kein Dialog), egal ob die Datei die Zone traegt
    boot.probe(out, f"motw-{label}-{method}", ran > 0 and not new,
               f"gelaufen={'ja' if ran > 0 else 'nein'} neue-fenster={new} fehler={err or '-'}")


if __name__ == "__main__":
    main()
