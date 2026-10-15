# #1041, Invariante 4: numpy und pandas lassen sich aus dem Nur-Lese-Volume
# importieren, ohne an pids_limit (128) oder 512 MiB zu stoßen; die Thread-
# Deckel des Wächters greifen (ein Thread statt eines je Host-CPU).
import json
import resource
import sys

import numpy as np
import pandas as pd

n = 10000
df = pd.DataFrame({"k": ["k%d" % (i % 50) for i in range(n)], "v": np.arange(n, dtype="float64") % 97})
g = df.groupby("k").agg(v_sum=("v", "sum"), rows=("v", "size")).reset_index()


def status(field):
    with open("/proc/self/status") as f:
        for line in f:
            if line.startswith(field + ":"):
                return int(line.split()[1])
    return -1


json.dump({
    "numpy": np.__version__,
    "pandas": pd.__version__,
    "groups": len(g),
    "rows_total": int(g["rows"].sum()),
    "v_sum": float(g["v_sum"].sum()),
    "threads": status("Threads"),
    "max_rss_mib": round(resource.getrusage(resource.RUSAGE_SELF).ru_maxrss / 1024, 1),
    "libs_on_path": [p for p in sys.path if p.startswith("/libs/")],
    "wrote_bytecode": sys.dont_write_bytecode,
}, sys.stdout)
