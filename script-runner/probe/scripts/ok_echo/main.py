import json
import sys

import januaport
import probe_helper

found = januaport.call("kartei_find", {"q": januaport.input.get("q", "")})
json.dump({"greeting": probe_helper.GREETING, "echo": januaport.input, "found": found.get("structuredContent")}, sys.stdout)
