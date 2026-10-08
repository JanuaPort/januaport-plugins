# M2: der Wächter (PID 1) ist nicht dumpable; das Skript mit derselben UID
# erreicht weder seine Umgebung noch seine Dateideskriptoren noch seinen
# Speicher.
import json
import os
import sys

res = {}
for path in ["/proc/1/environ", "/proc/1/mem", "/proc/1/maps"]:
    try:
        with open(path, "rb") as f:
            f.read(16)
        res[path] = "readable"
    except Exception as e:
        res[path] = type(e).__name__
for path in ["/proc/1/fd", "/proc/1/root", "/proc/1/cwd", "/proc/1/map_files"]:
    try:
        os.listdir(path)
        res[path] = "listable"
    except Exception as e:
        res[path] = type(e).__name__
fds = []
for fd in range(0, 8):
    try:
        with open("/proc/1/fd/%d" % fd, "rb") as f:
            f.read(1)
        fds.append(fd)
    except Exception:
        pass
res["/proc/1/fd/*"] = fds
res["uid"] = os.getuid()
json.dump(res, sys.stdout)
