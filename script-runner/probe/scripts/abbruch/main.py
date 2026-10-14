# L1 (JanuaPort/januaport#1040): ein langer Lauf, den das Gateway mitten drin
# abbricht. „schlafen“: ein innerer Aufruf, dann 20 s Schlaf. „haengen“: ein
# innerer Aufruf, der im Gateway hängt, bis der Lauf abgebrochen wird.
import time

import januaport

if januaport.input.get("mode") == "haengen":
    januaport.call("kartei_hang", {})
else:
    januaport.call("kartei_find", {"q": "abbruch"})
    time.sleep(20)
print("ok")
