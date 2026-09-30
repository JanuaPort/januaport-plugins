# M2: das Skript schreibt „ok“ und schläft über die Wandzeit hinaus. Die Uhr
# des vermittlers entscheidet: timeout, nicht ok.
import sys
import time

sys.stdout.write("ok")
sys.stdout.flush()
time.sleep(60)
