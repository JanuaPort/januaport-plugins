# S1: jeder Syscall der Sperrliste endet mit EPERM, der Seccomp-Modus ist 2.
# Syscall-Nummern für x86_64.
import ctypes
import errno
import json
import sys

libc = ctypes.CDLL(None, use_errno=True)
NR = {"ptrace": 101, "process_vm_readv": 310, "process_vm_writev": 311, "unshare": 272,
      "mount": 165, "umount2": 166, "keyctl": 250, "add_key": 248, "request_key": 249,
      "bpf": 321, "userfaultfd": 323, "perf_event_open": 298, "io_uring_setup": 425,
      "io_uring_enter": 426, "io_uring_register": 427, "setns": 308, "pivot_root": 155}
res = {}
for name, nr in NR.items():
    ctypes.set_errno(0)
    if name == "io_uring_setup":
        buf = ctypes.create_string_buffer(120)
        r = libc.syscall(nr, 1, buf)
    elif name == "unshare":
        r = libc.syscall(nr, 0x20000000)
    elif name == "ptrace":
        r = libc.syscall(nr, 16, 1, 0, 0)
    else:
        r = libc.syscall(nr, 0, 0, 0, 0, 0, 0)
    e = ctypes.get_errno()
    res[name] = "allowed" if r >= 0 else errno.errorcode.get(e, str(e))
status = open("/proc/self/status").read()
res["seccomp_mode"] = [line.split()[1] for line in status.splitlines() if line.startswith("Seccomp:")][0]
json.dump(res, sys.stdout)
