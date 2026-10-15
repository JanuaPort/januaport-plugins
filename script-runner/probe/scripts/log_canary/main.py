import januaport
import time

canary = "LOG-CANARY-1041-private"
mode = januaport.input.get("mode", "normal")
if mode == "normal":
    januaport.log(canary, 42, True)
    januaport.log()
elif mode == "large":
    januaport.log("x" * (100 * 1024))
elif mode == "timeout":
    januaport.log(canary)
    time.sleep(30)
elif mode == "error":
    januaport.log(canary)
    raise ValueError("PRIVATE-EXCEPTION-NOT-LOGGED")
elif mode == "invalid":
    januaport.log("a\udcffb")
