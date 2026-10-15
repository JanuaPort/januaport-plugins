"""Spike #1044 P0 Teil 2, Sonde uia-in-states: pywinauto (win32 und uia) gegen das klassische Notepad.
Aufruf (in der Benutzersitzung): pythonw.exe -I uia_states.py <zustand> <ausgabedatei>
Je Backend und Ziel (bestehend = vor dem Zustandswechsel gestartet, neu = im Zustand gestartet) drei Schritte:
finden, setzen (set_edit_text, braucht keine Eingabe), tippen (type_keys, braucht SendInput = Eingabedesktop)."""
import os
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import boot  # noqa: E402

PRE_PID = r"C:\spike\out\notepad_pre.pid"


def err(e):
    return f"{type(e).__name__}: {' '.join(str(e).split())[:160]}"


def edit_control(win):
    for d in win.descendants():
        if d.friendly_class_name() in ("Edit", "Document"):
            return d
    return None


def read_text(ctrl, backend):
    if backend == "uia" and hasattr(ctrl, "iface_value"):
        return str(ctrl.iface_value.CurrentValue)
    return ctrl.window_text()


def measure(state, backend, target, pid, out):
    from pywinauto import Application
    base = f"uia-{state}-{backend}-{target}"
    try:
        app = Application(backend=backend).connect(process=pid, timeout=5)
        win = app.window(class_name="Notepad") if backend == "win32" else app.top_window()
        win.wait("exists", timeout=5)
        n = len(win.descendants())
        ctrl = edit_control(win)
        boot.probe(out, base + "-finden", n > 0 and ctrl is not None,
                   f"titel={win.window_text()!r} descendants={n} edit={'ja' if ctrl else 'nein'}")
    except Exception as e:  # noqa: BLE001 - jeder Fehlertext ist Messwert
        boot.probe(out, base + "-finden", False, err(e))
        return
    if ctrl is None:
        return
    try:
        ctrl.set_edit_text("setzen-" + state)
        text = read_text(ctrl, backend)
        boot.probe(out, base + "-setzen", ("setzen-" + state) in text, f"gelesen={text[:40]!r}")
    except Exception as e:  # noqa: BLE001
        boot.probe(out, base + "-setzen", False, err(e))
    try:
        ctrl.set_edit_text("")
        ctrl.type_keys("januaport", with_spaces=True)
        time.sleep(1)
        text = read_text(ctrl, backend)
        boot.probe(out, base + "-tippen", "januaport" in text, f"gelesen={text[:40]!r}")
    except Exception as e:  # noqa: BLE001
        boot.probe(out, base + "-tippen", False, err(e))
    try:
        win.set_focus()
        boot.probe(out, base + "-fokus", True, "set_focus ohne Fehler")
    except Exception as e:  # noqa: BLE001
        boot.probe(out, base + "-fokus", False, err(e))


def main():
    state, out = sys.argv[1], sys.argv[2]
    pre = None
    if os.path.exists(PRE_PID):
        pre = int(open(PRE_PID).read().strip())
    new = subprocess.Popen(["notepad.exe"])
    time.sleep(3)
    for backend in ("win32", "uia"):
        if pre:
            measure(state, backend, "bestehend", pre, out)
        measure(state, backend, "neu", new.pid, out)
    subprocess.run(["taskkill", "/F", "/IM", "notepad.exe"], capture_output=True, creationflags=0x08000000)
    if os.path.exists(PRE_PID):
        os.remove(PRE_PID)


if __name__ == "__main__":
    main()
