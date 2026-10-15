# #1041, Invariante 1: Kein pip im Lauf. import pip scheitert mit
# ModuleNotFoundError, es gibt keine pip-Programme und kein ensurepip.
import importlib.util
import json
import os
import shutil
import sys

res = {}
try:
    import pip  # noqa: F401
    res["import_pip"] = "ok"
except ModuleNotFoundError:
    res["import_pip"] = "ModuleNotFoundError"
except Exception as e:
    res["import_pip"] = type(e).__name__
res["spec_pip"] = importlib.util.find_spec("pip") is not None
res["spec_ensurepip"] = importlib.util.find_spec("ensurepip") is not None
res["which"] = [shutil.which(n) for n in ("pip", "pip3", "pip3.12")]
res["bins"] = sorted(n for d in ("/usr/local/bin", "/usr/bin", "/bin") for n in os.listdir(d) if n.startswith("pip"))
res["libs_on_path"] = len([p for p in sys.path if p.startswith("/libs/")])
json.dump(res, sys.stdout)
