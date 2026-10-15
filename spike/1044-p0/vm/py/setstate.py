"""Spike #1044 P0 Teil 2: setzt einen Sitzungszustand (in der Benutzersitzung ausfuehren).
Aufruf: pythonw.exe -I setstate.py <lock|disconnect|screensaver|screensaver-secure|resume|monitor-off|prestart-notepad> <ausgabedatei>"""
import ctypes
import os
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import boot  # noqa: E402

u32 = ctypes.WinDLL("user32", use_last_error=True)
NO_WINDOW = 0x08000000
HWND_BROADCAST = 0xFFFF
WM_SYSCOMMAND = 0x0112
SC_SCREENSAVE = 0xF140
SC_MONITORPOWER = 0xF170


def resume(out):
    """Simuliert Benutzereingabe, die den Schoner beendet (bei ScreenSaverIsSecure=1 sperrt Windows dann)."""
    u32.SetCursorPos(5, 5)
    for dx in (20, -20, 20):
        u32.mouse_event(0x0001, dx, dx, 0, 0)  # MOUSEEVENTF_MOVE
        time.sleep(0.3)
    time.sleep(4)
    boot.probe(out, "setzen-resume", True, "Mausbewegung gesendet")


def screensaver(out, secure=False):
    import winreg
    scr = r"C:\Windows\System32\scrnsave.scr"
    with winreg.CreateKey(winreg.HKEY_CURRENT_USER, r"Control Panel\Desktop") as k:
        winreg.SetValueEx(k, "ScreenSaveActive", 0, winreg.REG_SZ, "1")
        winreg.SetValueEx(k, "SCRNSAVE.EXE", 0, winreg.REG_SZ, scr)
        winreg.SetValueEx(k, "ScreenSaverIsSecure", 0, winreg.REG_SZ, "1" if secure else "0")
    # SC_SCREENSAVE startet den Schoner sofort, ohne auf Leerlauf zu warten.
    r = u32.PostMessageW(HWND_BROADCAST, WM_SYSCOMMAND, SC_SCREENSAVE, 0)
    time.sleep(6)
    procs = subprocess.run(["tasklist", "/FO", "CSV", "/NH"], capture_output=True, text=True,
                           creationflags=NO_WINDOW).stdout
    running = [ln.split(",")[0].strip('"') for ln in procs.splitlines() if ".scr" in ln.lower()]
    boot.probe(out, "setzen-screensaver", bool(running),
               f"scrnsave.scr vorhanden={os.path.exists(scr)} post={r} scr-prozesse={running}")


def main():
    action, out = sys.argv[1], sys.argv[2]
    if action == "lock":
        ok = bool(u32.LockWorkStation())
        boot.probe(out, "setzen-lock", ok, f"LockWorkStation={ok}")
    elif action == "disconnect":
        r = subprocess.run(["tsdiscon.exe"], capture_output=True, text=True, creationflags=NO_WINDOW)
        boot.probe(out, "setzen-disconnect", r.returncode == 0, f"tsdiscon exit={r.returncode} {r.stdout} {r.stderr}")
    elif action == "screensaver":
        screensaver(out)
    elif action == "screensaver-secure":
        screensaver(out, secure=True)
    elif action == "resume":
        resume(out)
    elif action == "monitor-off":
        r = u32.SendMessageTimeoutW(HWND_BROADCAST, WM_SYSCOMMAND, SC_MONITORPOWER, 2, 2, 3000, None)
        boot.probe(out, "setzen-monitor-off", bool(r), f"SC_MONITORPOWER=2 gesendet, Rueckgabe={r}")
    elif action == "prestart-notepad":
        p = subprocess.Popen(["notepad.exe"])
        time.sleep(3)
        with open(r"C:\spike\out\notepad_pre.pid", "w") as f:
            f.write(str(p.pid))
        boot.probe(out, "setzen-prestart-notepad", True, f"pid={p.pid}")
    else:
        boot.probe(out, "setzen-unbekannt", False, action)


if __name__ == "__main__":
    main()
