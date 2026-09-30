# Speicher über den Deckel (512 MiB): der OOM-Killer beendet das Skript.
blocks = []
while True:
    blocks.append(bytearray(64 << 20))
