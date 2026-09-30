# M6: die Ausnahme trägt eine Kanarie. Weder die Antwort noch ein Log darf
# sie enthalten; die KI sieht nur error_type und error_at.
import sys

import januaport


def fail(secret):
    raise ValueError("Kanarie im Fehlertext: " + secret)


sys.stderr.write("stderr " + januaport.input["canary"] + "\n")
fail(januaport.input["canary"])
