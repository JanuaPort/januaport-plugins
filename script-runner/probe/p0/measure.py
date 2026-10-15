# Messkörper der P0-Sonde (JanuaPort/januaport#1041). Läuft in der Sandbox des
# Skript-Läufers: `python3 -I - <modus> <libs-ordner>` mit diesem Text auf stdin.
# Er gibt genau eine JSON-Zeile aus. Wegwerf-Werkzeug, nicht Teil des Produkts.
import json
import os
import platform
import resource
import sys
import time

mode, libs = sys.argv[1], sys.argv[2]


def status_field(name):
    with open("/proc/self/status") as f:
        for line in f:
            if line.startswith(name + ":"):
                return int(line.split()[1])
    return -1


def cgroup_pids():
    # pids_limit zählt Tasks (Threads) der cgroup; v2 und v1 probieren.
    for p in ("/sys/fs/cgroup/pids.current", "/sys/fs/cgroup/pids/pids.current"):
        try:
            with open(p) as f:
                return int(f.read().strip())
        except OSError:
            continue
    return -1


def snapshot(t0):
    return {
        "seconds": round(time.perf_counter() - t0, 3),
        "max_rss_mib": round(resource.getrusage(resource.RUSAGE_SELF).ru_maxrss / 1024, 1),
        "vm_hwm_mib": round(status_field("VmHWM") / 1024, 1),
        "threads": status_field("Threads"),
        "cgroup_pids": cgroup_pids(),
        "cpu_count": os.cpu_count(),
        "affinity": len(os.sched_getaffinity(0)),
    }


out = {"mode": mode, "libs": libs, "dont_write_bytecode": sys.dont_write_bytecode}
sys.path.append(libs)
t0 = time.perf_counter()
if mode == "baseline":
    import json as _j  # noqa: F401
elif mode == "numpy":
    import numpy  # noqa: F401
    out["numpy"] = numpy.__version__
elif mode == "pandas":
    import pandas  # noqa: F401
    out["pandas"] = pandas.__version__
elif mode == "work":
    import numpy as np
    import pandas as pd
    out["imported"] = round(time.perf_counter() - t0, 3)
    t1 = time.perf_counter()
    n = 10000
    df = pd.DataFrame({
        "k": ["k%d" % (i % 50) for i in range(n)],
        "v": np.arange(n, dtype="float64") % 97,
        "w": np.arange(n, dtype="int64") % 13,
    })
    g = df.groupby("k").agg(v_sum=("v", "sum"), v_mean=("v", "mean"), w_max=("w", "max"), n=("v", "size")).reset_index()
    payload = g.to_json(orient="records")
    out["work_seconds"] = round(time.perf_counter() - t1, 3)
    out["rows_in"], out["groups"], out["json_bytes"] = n, len(g), len(payload)
else:
    raise SystemExit("unbekannter Modus")
out.update(snapshot(t0))
out["glibc"] = " ".join(platform.libc_ver())
out["python"] = platform.python_version()
print(json.dumps(out))
