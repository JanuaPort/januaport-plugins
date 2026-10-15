"""Spike #1044 P0 (pywinauto-backends-headless): Notepad starten, mit win32 und uia verbinden.
Aufruf: python.exe -I backends.py [dll_dir]. Gibt PROBE-Zeilen aus."""
import ctypes
import os
import subprocess
import sys
import time

if len(sys.argv) > 1 and sys.argv[1]:
    os.add_dll_directory(sys.argv[1])


def probe(name, ok, detail):
    print(f"PROBE {name} {'PASS' if ok else 'FAIL'} {' '.join(str(detail).split())}", flush=True)


def session_info():
    k32, u32 = ctypes.windll.kernel32, ctypes.windll.user32
    sid = ctypes.c_ulong(0)
    k32.ProcessIdToSessionId(os.getpid(), ctypes.byref(sid))
    console = k32.WTSGetActiveConsoleSessionId()
    u32.OpenInputDesktop.restype = ctypes.c_void_p
    hdesk = u32.OpenInputDesktop(0, False, 0x0001)  # DESKTOP_READOBJECTS
    err = ctypes.GetLastError()
    if hdesk:
        u32.CloseDesktop(ctypes.c_void_p(hdesk))
    probe("session-info", True,
          f"sitzung={sid.value} konsolensitzung={console} eingabedesktop={'offen' if hdesk else f'verwehrt(err={err})'} "
          f"user={os.environ.get('USERNAME')} bildschirm={u32.GetSystemMetrics(0)}x{u32.GetSystemMetrics(1)}")


def one_backend(backend):
    name = f"pywinauto-backends-headless-{backend}"
    steps = []
    proc = None
    try:
        from pywinauto import Application, Desktop
        proc = subprocess.Popen(["notepad.exe"])
        time.sleep(3)
        try:
            app = Application(backend=backend).connect(process=proc.pid, timeout=15)
            steps.append(f"connect(process={proc.pid}) ok")
        except Exception as e:  # moderner Notepad startet ggf. einen anderen Prozess
            steps.append(f"connect(process) {type(e).__name__}: {str(e)[:120]}")
            app = Application(backend=backend).connect(path="notepad.exe", timeout=15)
            steps.append("connect(path) ok")
        win = app.window(class_name="Notepad") if backend == "win32" else app.top_window()
        win.wait("exists", timeout=15)
        title = win.window_text()
        steps.append(f"fenster class={win.friendly_class_name()} titel={title!r}")
        n = len(win.descendants())
        steps.append(f"descendants={n}")
        probe(name, n > 0, "; ".join(steps))
    except Exception as e:
        steps.append(f"{type(e).__name__}: {str(e)[:200]}")
        probe(name, False, "; ".join(steps))
        return
    try:
        ctrl = None
        for d in win.descendants():
            if d.friendly_class_name() in ("Edit", "Document"):
                ctrl = d
                break
        if ctrl is None:
            probe(name + "-tippen", False, "kein Edit/Document-Steuerelement gefunden")
        else:
            ctrl.type_keys("januaport", with_spaces=True)
            time.sleep(1)
            text = ctrl.window_text() if backend == "win32" else (ctrl.iface_value.CurrentValue if hasattr(ctrl, "iface_value") else ctrl.window_text())
            probe(name + "-tippen", "januaport" in str(text), f"gelesen={str(text)[:40]!r}")
    except Exception as e:
        probe(name + "-tippen", False, f"{type(e).__name__}: {str(e)[:200]}")
    finally:
        subprocess.run(["taskkill", "/F", "/IM", "notepad.exe"], capture_output=True)
        if proc:
            proc.kill()


if __name__ == "__main__":
    try:
        session_info()
    except Exception as e:
        probe("session-info", False, f"{type(e).__name__}: {e}")
    for b in ("win32", "uia"):
        one_backend(b)
