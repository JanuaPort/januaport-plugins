# M3 + G2: Lauf 1 schreibt eine Marke in jeden beschreibbaren Ort, Lauf 2
# findet keine davon und kann /work trotzdem beschreiben. Dazu: eine Datei
# in /work oder /tmp lässt sich nicht ausführen (noexec), auch nicht als .so.
import ctypes
import json
import os
import subprocess
import sys

import januaport

mode = januaport.input.get("mode")
roots = ["/work", "/tmp", "/dev/shm", "/run", "/var/tmp", "/home", os.path.expanduser("~"), "/"]
res = {"mode": mode, "writable": [], "found": []}
for root in roots:
    p = os.path.join(root, "marke-928")
    if mode == "write":
        try:
            with open(p, "w") as f:
                f.write("x")
            res["writable"].append(p)
        except Exception:
            pass
    elif os.path.exists(p):
        res["found"].append(p)
try:
    with open("/work/probe-write", "w") as f:
        f.write("x")
    res["work_writable"] = True
except Exception as e:
    res["work_writable"] = type(e).__name__
for d in ["/work", "/tmp"]:
    exe = os.path.join(d, "x.sh")
    try:
        with open(exe, "w") as f:
            f.write("#!/bin/sh\necho hi\n")
        os.chmod(exe, 0o755)
        subprocess.run([exe], check=True, capture_output=True)
        res["exec" + d] = "ran"
    except Exception as e:
        res["exec" + d] = type(e).__name__
    so = os.path.join(d, "x.so")
    try:
        with open("/usr/lib/x86_64-linux-gnu/libc.so.6", "rb") as src, open(so, "wb") as dst:
            dst.write(src.read())
        ctypes.CDLL(so)
        res["so" + d] = "loaded"
    except Exception as e:
        res["so" + d] = type(e).__name__
json.dump(res, sys.stdout)
