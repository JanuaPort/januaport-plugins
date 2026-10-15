"""Spike #1044 P0 Teil 2, Sonde logon-task: laeuft als Aufgabe "bei Anmeldung" und haelt fest, wo und wann.
Aufruf: pythonw.exe -I logon_probe.py <ausgabedatei>"""
import ctypes
import datetime
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import boot  # noqa: E402
import session_check as sc  # noqa: E402


def main():
    out = sys.argv[1]
    s = sc.signals()
    uptime = int(ctypes.WinDLL("kernel32").GetTickCount64() / 1000)
    ok = s["sitzung"] != 0 and s["eingabedesktop_switch"].startswith("Default/ok")
    boot.probe(out, "logon-task", ok,
               f"zeit={datetime.datetime.now():%H:%M:%S} uptime_s={uptime} benutzer={os.environ.get('USERNAME')} "
               f"sessionname={os.environ.get('SESSIONNAME')} " + " ".join(f"{k}={v}" for k, v in s.items()))


if __name__ == "__main__":
    main()
