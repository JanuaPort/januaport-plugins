# #1041, Invariante 4: /libs/<hash> ist in der Sandbox nur lesbar. Jeder
# Schreibversuch ergibt EROFS; .pth-Dateien aus dem Wheel werden nicht
# ausgeführt (Invariante 3).
import errno
import json
import os
import sys

libs = [p for p in sys.path if p.startswith("/libs/")]
res = {"libs": libs}
if len(libs) != 1:
    json.dump(res, sys.stdout)
    sys.exit(0)
root = libs[0]


def attempt(label, fn, into=None):
    into = res if into is None else into
    try:
        fn()
        into[label] = "ok"
    except OSError as e:
        into[label] = errno.errorcode.get(e.errno, str(e.errno))


def create():
    with open(os.path.join(root, "neu.txt"), "w") as f:
        f.write("x")


def overwrite():
    with open(os.path.join(root, "probe_pkg", "__init__.py"), "w") as f:
        f.write("x")


def append():
    with open(os.path.join(root, "probe_pkg", "__init__.py"), "a") as f:
        f.write("x")


attempt("create", create)
attempt("overwrite", overwrite)
attempt("append", append)
attempt("mkdir", lambda: os.mkdir(os.path.join(root, "neu")))
attempt("unlink", lambda: os.unlink(os.path.join(root, "probe_pkg", "__init__.py")))
attempt("rename", lambda: os.rename(os.path.join(root, "probe_pkg"), os.path.join(root, "anders")))
attempt("chmod", lambda: os.chmod(os.path.join(root, "probe_pkg", "__init__.py"), 0o777))
attempt("symlink", lambda: os.symlink("/etc/passwd", os.path.join(root, "link")))
attempt("truncate", lambda: os.truncate(os.path.join(root, "probe_pkg", "__init__.py"), 0))


# #1096: the sandbox sees only the ready subdirectory of the libs volume.
import januaport  # noqa: E402

res["root"] = {}
attempt("create", lambda: open("/libs/neu.tmp", "w").close(), res["root"])
attempt("mkdir", lambda: os.mkdir("/libs/neu.wheel"), res["root"])
res["staging"] = {}
for name in januaport.input.get("staging", []):
    p = os.path.join("/libs", name)
    r = res["staging"][name] = {"exists": os.path.lexists(p)}
    attempt("read", lambda: open(p, "rb").close(), r)
    attempt("unlink", lambda: os.unlink(p), r)
    attempt("rename", lambda: os.rename(p, p + "x"), r)
    attempt("chmod", lambda: os.chmod(p, 0o777), r)
    if os.path.isdir(p):
        attempt("create_in", lambda: open(os.path.join(p, "x"), "w").close(), r)
        attempt("mkdir_in", lambda: os.mkdir(os.path.join(p, "d")), r)
    else:
        attempt("write", lambda: open(p, "r+").close() or open(p, "w").close(), r)
res["manifest_present"] = os.path.exists(os.path.join(root, ".jnpt-manifest"))
res["entries_in_libs"] = sorted(os.listdir("/libs"))

# Keep the mount-visibility canary independent of a working library import:
# exposing the whole volume must fail on visibility itself, not ImportError.
if januaport.input.get("staging_only"):
    json.dump(res, sys.stdout)
    sys.exit(0)

import probe_pkg  # noqa: E402

res["import"] = probe_pkg.VALUE
res["pth_ran"] = "PTH_RAN" in os.environ
res["site_in_path"] = [p for p in sys.path if "site-packages" in p and p.startswith("/libs")]
with open(os.path.join(root, "probe_pkg", "__init__.py")) as f:
    res["intact"] = f.read() == "VALUE = 'ok'\n"
json.dump(res, sys.stdout)
