import json
import sys

import januaport

query = januaport.input.get("q", "full")
rows = 0
total = 0
grund = "fertig"
try:
    for row in januaport.etl_stream("erp_db", query, {"n": 1}):
        rows += 1
        total += row["nr"]
except januaport.StreamEnded as e:
    grund = e.grund
json.dump({"rows": rows, "sum": total, "grund": grund}, sys.stdout)
