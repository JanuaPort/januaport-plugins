# G1: Fork über den PID-Deckel. Unter runc gibt es EAGAIN, unter runsc
# stirbt die Sandbox. Beides ist in Ordnung, solange der vermittler lebt.
import os
import time

while True:
    pid = os.fork()
    if pid == 0:
        time.sleep(30)
        os._exit(0)
