# Wegwerf-Entpacker der P0-Sonde: entpackt alle Wheels aus <wheels> flach nach
# <ziel>. NUR für die Messung (die Sonde braucht echte numpy/pandas-Dateien);
# das gehärtete Entpacken des Produkts ist internal/libs. Aufruf im Image:
#   python3 -I prepare.py <wheels> <ziel>
import os
import sys
import zipfile

src, dst = sys.argv[1], sys.argv[2]
os.makedirs(dst, exist_ok=True)
for name in sorted(os.listdir(src)):
    if name.endswith(".whl"):
        with zipfile.ZipFile(os.path.join(src, name)) as z:
            z.extractall(dst)
        print("entpackt", name)
