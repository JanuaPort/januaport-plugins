# R3 + R9: nur lo, kein Weg nach außen, kein jnpt in Umgebung oder
# Dateisystem, nur die erwarteten Mounts.
import json
import os
import socket
import sys

import januaport

needle = januaport.input["needle"].encode()
res = {}
res["ifaces"] = sorted(os.listdir("/sys/class/net")) if os.path.isdir("/sys/class/net") else "n/a"


def conn(host, port):
    try:
        s = socket.create_connection((host, port), timeout=2)
        s.close()
        return "CONNECTED"
    except Exception as e:
        return type(e).__name__


res["net"] = {"1.1.1.1:443": conn("1.1.1.1", 443), "169.254.169.254:80": conn("169.254.169.254", 80),
              "jnpt:8484": conn("jnpt", 8484), "script-runner:8090": conn("script-runner", 8090)}
res["env_jnpt"] = [k for k, v in os.environ.items() if "jnpt" in (k + v).lower()]
res["run_jnpt"] = os.path.exists("/run/jnpt")
res["docker_sock"] = os.path.exists("/var/run/docker.sock") or os.path.exists("/run/docker.sock")
hits = []
for root in ["/work", "/tmp", "/etc", "/run", "/home", "/root", "/var", "/usr/local/lib/script-runner"]:
    for dirpath, dirnames, filenames in os.walk(root):
        for fn in filenames:
            p = os.path.join(dirpath, fn)
            try:
                if os.path.getsize(p) > 4 << 20:
                    continue
                with open(p, "rb") as f:
                    if needle in f.read():
                        hits.append(p)
            except Exception:
                pass
res["needle_hits"] = hits
res["mounts"] = sorted({line.split()[1] for line in open("/proc/self/mounts")})
json.dump(res, sys.stdout)
