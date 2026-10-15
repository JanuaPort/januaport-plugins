"""Spike #1044 P0 Teil 2, Sonde session-check.
Aufruf (in der Benutzersitzung): pythonw.exe -I session_check.py <zustand> <erwartung:nutzbar|nicht-nutzbar> <ausgabedatei>
Misst die Rohsignale und das Urteil "angemeldet, entsperrt, Eingabedesktop erreichbar"."""
import ctypes
import os
import struct
import sys
from ctypes import wintypes

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import boot  # noqa: E402

k32 = ctypes.WinDLL("kernel32", use_last_error=True)
u32 = ctypes.WinDLL("user32", use_last_error=True)
wts = ctypes.WinDLL("wtsapi32", use_last_error=True)

WTS_CURRENT_SESSION = 0xFFFFFFFF
WTS_CONNECT_STATE = 8
WTS_SESSION_INFO_EX = 25
CONNECT_NAMES = {0: "Active", 1: "Connected", 2: "ConnectQuery", 3: "Shadow", 4: "Disconnected",
                 5: "Idle", 6: "Listen", 7: "Reset", 8: "Down", 9: "Init"}
DESKTOP_READOBJECTS = 0x0001
DESKTOP_SWITCHDESKTOP = 0x0100
UOI_NAME = 2

u32.OpenInputDesktop.restype = ctypes.c_void_p
u32.GetForegroundWindow.restype = ctypes.c_void_p
u32.GetThreadDesktop.restype = ctypes.c_void_p
k32.OpenProcess.restype = ctypes.c_void_p
wts.WTSQuerySessionInformationW.argtypes = [ctypes.c_void_p, wintypes.DWORD, ctypes.c_int,
                                            ctypes.POINTER(ctypes.c_void_p), ctypes.POINTER(wintypes.DWORD)]
wts.WTSFreeMemory.argtypes = [ctypes.c_void_p]


def _wts_buffer(info_class):
    ptr, size = ctypes.c_void_p(), wintypes.DWORD()
    if not wts.WTSQuerySessionInformationW(None, WTS_CURRENT_SESSION, info_class, ctypes.byref(ptr), ctypes.byref(size)):
        return None, ctypes.get_last_error()
    data = ctypes.string_at(ptr, size.value)
    wts.WTSFreeMemory(ptr)
    return data, 0


def connect_state():
    data, err = _wts_buffer(WTS_CONNECT_STATE)
    if data is None:
        return f"fehler{err}"
    return CONNECT_NAMES.get(struct.unpack("<i", data[:4])[0], "?")


def lock_flag():
    """WTSINFOEX: Level (4) + Pad (4), dann SessionId, SessionState, SessionFlags (je 4).
    Seit Windows 8 laut Doku 0=WTS_SESSIONSTATE_LOCK, 1=UNLOCK; in der Sitzung gemessen: entsperrt liefert 0x1."""
    data, err = _wts_buffer(WTS_SESSION_INFO_EX)
    if data is None:
        return f"fehler{err}"
    flags = struct.unpack_from("<I", data, 16)[0]
    return {0: "gesperrt", 1: "entsperrt"}.get(flags, f"unbekannt{flags:#x}")


def _desk_name(h):
    buf = ctypes.create_unicode_buffer(256)
    n = wintypes.DWORD()
    if u32.GetUserObjectInformationW(ctypes.c_void_p(h), UOI_NAME, buf, ctypes.sizeof(buf), ctypes.byref(n)):
        return buf.value
    return f"namefehler{ctypes.get_last_error()}"


def input_desktop(access):
    h = u32.OpenInputDesktop(0, False, access)
    if not h:
        return None, f"err{ctypes.get_last_error()}"
    name = _desk_name(h)
    u32.CloseDesktop(ctypes.c_void_p(h))
    return name, "ok"


def foreground():
    hwnd = u32.GetForegroundWindow()
    if not hwnd:
        return "keins"
    buf = ctypes.create_unicode_buffer(256)
    u32.GetWindowTextW(ctypes.c_void_p(hwnd), buf, 256)
    pid = wintypes.DWORD()
    u32.GetWindowThreadProcessId(ctypes.c_void_p(hwnd), ctypes.byref(pid))
    exe = "?"
    h = k32.OpenProcess(0x1000, False, pid.value)
    if h:
        b = ctypes.create_unicode_buffer(520)
        n = wintypes.DWORD(520)
        if k32.QueryFullProcessImageNameW(ctypes.c_void_p(h), 0, b, ctypes.byref(n)):
            exe = os.path.basename(b.value)
        k32.CloseHandle(ctypes.c_void_p(h))
    return f"{exe}:{buf.value[:30]!r}"


def own_desktop():
    h = u32.GetThreadDesktop(k32.GetCurrentThreadId())
    return _desk_name(h)


def signals():
    sid = wintypes.DWORD()
    k32.ProcessIdToSessionId(os.getpid(), ctypes.byref(sid))
    sw_name, sw_state = input_desktop(DESKTOP_SWITCHDESKTOP)
    rd_name, rd_state = input_desktop(DESKTOP_READOBJECTS)
    return {
        "sitzung": sid.value,
        "konsole": k32.WTSGetActiveConsoleSessionId(),
        "wts": connect_state(),
        "sperrflag": lock_flag(),
        "eingabedesktop_switch": f"{sw_name or '-'}/{sw_state}",
        "eingabedesktop_read": f"{rd_name or '-'}/{rd_state}",
        "eigener_desktop": own_desktop(),
        "vordergrund": foreground(),
    }


def verdict(s):
    """Urteil des Laeufers (v2): WTS aktiv UND entsperrt (WTSInfoEx) UND der Eingabedesktop ist per SWITCHDESKTOP
    oeffenbar und heisst Default. v1 ohne das Sperrflag lag in der Sperre falsch: der Sperrbildschirm (LockApp.exe)
    laeuft auf dem Desktop Default, OpenInputDesktop gelingt also auch gesperrt."""
    return (s["wts"] == "Active" and s["sperrflag"] == "entsperrt"
            and s["eingabedesktop_switch"].startswith("Default/ok"))


def main():
    state, expect, out = sys.argv[1], sys.argv[2], sys.argv[3]
    s = signals()
    usable = verdict(s)
    ok = usable == (expect == "nutzbar")
    boot.probe(out, f"session-check-{state}", ok,
               f"urteil={'nutzbar' if usable else 'nicht-nutzbar'} erwartet={expect} " +
               " ".join(f"{k}={v}" for k, v in s.items()))


if __name__ == "__main__":
    main()
