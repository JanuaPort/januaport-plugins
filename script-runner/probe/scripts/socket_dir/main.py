# M4: der Socket-Ordner ist read-only eingehängt — kein Anlegen, kein Löschen.
import json
import os
import sys

d = "/run/script-runner"
res = {"entries": sorted(os.listdir(d))}
try:
    open(os.path.join(d, "evil"), "w").close()
    res["create"] = "ok"
except Exception as e:
    res["create"] = type(e).__name__
try:
    os.unlink(os.path.join(d, "sandbox.sock"))
    res["unlink"] = "ok"
except Exception as e:
    res["unlink"] = type(e).__name__
try:
    os.rename(os.path.join(d, "sandbox.sock"), os.path.join(d, "moved"))
    res["rename"] = "ok"
except Exception as e:
    res["rename"] = type(e).__name__
json.dump(res, sys.stdout)
