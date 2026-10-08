#!/usr/bin/env python3
"""Leitet das Seccomp-Profil der Sandbox aus dem Docker-Standardprofil ab.

Quelle: moby-default.json = seccomp/default.json aus
https://github.com/moby/profiles, Commit a21872828a8e5745d79e2fc1a07ee6dad8aedffa
(Apache License 2.0, siehe NOTICE). Herleitung (JanuaPort/januaport#928,
Messkommentar P0 §1): Docker-Standard MINUS der gesperrten Syscalls in allen
Allow-Regeln (leer gewordene Regeln entfallen) PLUS eine vorangestellte
ERRNO-Regel (EPERM), die dieselbe Liste ausdrücklich sperrt — unabhängig
davon, was eine spätere Docker-Fassung erlaubt.

  python3 derive.py           schreibt jnpt-sandbox-seccomp.json neu
  python3 derive.py --check   prüft, dass die Datei der Herleitung entspricht
"""
import json
import os
import sys

DENY = ["ptrace", "process_vm_readv", "process_vm_writev", "unshare", "mount", "umount2",
        "keyctl", "add_key", "request_key", "bpf", "userfaultfd", "perf_event_open",
        "io_uring_setup", "io_uring_enter", "io_uring_register", "setns", "pivot_root"]

HERE = os.path.dirname(os.path.abspath(__file__))
SOURCE = os.path.join(HERE, "moby-default.json")
TARGET = os.path.join(HERE, "jnpt-sandbox-seccomp.json")


def derive(moby):
    deny = set(DENY)
    rules = []
    for rule in moby["syscalls"]:
        names = rule["names"]
        if rule["action"] == "SCMP_ACT_ALLOW":
            names = [n for n in names if n not in deny]
        if not names:
            continue
        rule = dict(rule)
        rule["names"] = names
        rules.append(rule)
    head = {"names": DENY, "action": "SCMP_ACT_ERRNO", "errnoRet": 1,
            "comment": "jnpt script-runner #928: explicit deny, independent of Docker version"}
    profile = dict(moby)
    profile["syscalls"] = [head] + rules
    return json.dumps(profile, indent=1)


def main():
    with open(SOURCE, encoding="utf-8") as f:
        text = derive(json.load(f))
    if "--check" in sys.argv[1:]:
        with open(TARGET, encoding="utf-8") as f:
            if f.read() != text:
                print("jnpt-sandbox-seccomp.json weicht von der Herleitung ab", file=sys.stderr)
                return 1
        print("jnpt-sandbox-seccomp.json entspricht der Herleitung")
        return 0
    with open(TARGET, "w", encoding="utf-8", newline="\n") as f:
        f.write(text)
    return 0


if __name__ == "__main__":
    sys.exit(main())
