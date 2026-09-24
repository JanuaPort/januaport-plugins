# box/tunnel — das Profil des Tunnel-Clients für die Box

Hier liegt **ausschließlich** die Profil-Vorlage für den OpenAI-Tunnel-Client
auf der Box (`profiles/box.yaml.example`). Einbau und Prüfung:
[`../RUNBOOK.md`](../RUNBOOK.md), Abschnitt „Anbindung".

**Nicht hier, mit Absicht:** Die Compose des Tunnel-Stacks, die systemd-Units
für den Schlüsselwechsel und die tmpfiles-Regel für `/run/jnpt/tunnel` gelten
für jede JanuaPort-Anlage, nicht nur für die Box. Sie werden mit JanuaPort
ausgeliefert und liegen (noch) nicht in diesem Repository. Zwei Vorlagen für
dieselbe Sache wären eine zu viel — und die falsche wird irgendwann kopiert.

Was der Stack auf der Box leisten muss, damit dieses Profil passt:

| Was | Erwartung |
|---|---|
| Stack | eigener Compose-Projektname, Ablage `/opt/jnpt/tunnel/` |
| Netz | tritt dem bestehenden Netz `jnpt_default` bei (Gateway zuerst starten) |
| Profil | `/opt/jnpt/tunnel/profiles/box.yaml` read-only im Container |
| Laufzeit-Schlüssel | das **Verzeichnis** `/run/jnpt/tunnel` read-only nach `/run/secrets/jnpt` |
| Vertrauen zur Box-CA | `CA_BUNDLE` auf die Wurzel der Box (additiv zu den System-Wurzeln) |
| Health | im Container `0.0.0.0:8080`, auf dem Host nur `127.0.0.1:8081` |

Warum das Verzeichnis und nicht die Datei: Ein Datei-Bind-Mount hält den alten
Inode fest; ein erneuerter Schlüssel käme im Container nie an.
