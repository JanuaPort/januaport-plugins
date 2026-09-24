#!/usr/bin/env python3
"""sp-einrichten.py <sicherung|ablage> — SharePoint-Zugang einer App messen und den
rclone-Remote schreiben. Kein Produktcode; laeuft auf dem Host.

Liest  /etc/jnpt/rclone/<name>.app     (CLIENT_ID=, TENANT_ID=; Kennungen, nicht geheim)
       /etc/jnpt/rclone/<name>.secret  (Client Secret, 0600 — von zugang-sp.sh)
       /etc/jnpt/rclone/<name>.site    (SITE_URL=https://<mandant>.sharepoint.com/sites/<Site>)
Misst  Token-Bezug (401 = Secret/App), Site (200 / 403 = Grant fehlt / 404 = Adresse),
       Drives der Site, und die 403-Gegenprobe gegen die Wurzel-Site (Sites.Selected greift).
Schreibt [<name>] nach /etc/jnpt/rclone/rclone-<name>.conf (ohne Secret — das kommt zur Laufzeit
       aus der Secret-Datei per Env RCLONE_CONFIG_<NAME>_CLIENT_SECRET).
Gibt NUR Status, Zaehler und Drive-IDs aus — keine Dateinamen, keine Inhalte.
"""
import json, os, sys, urllib.parse, urllib.request, urllib.error

name = sys.argv[1]
D = "/etc/jnpt/rclone"


def env_file(p):
    out = {}
    for line in open(p, encoding="utf-8"):
        line = line.strip()
        if "=" in line and not line.startswith("#"):
            k, v = line.split("=", 1)
            out[k.strip()] = v.strip()
    return out


app = env_file(f"{D}/{name}.app")
secret = open(f"{D}/{name}.secret", encoding="utf-8").read().strip()
site_url = env_file(f"{D}/{name}.site")["SITE_URL"].rstrip("/")
tenant, client = app["TENANT_ID"], app["CLIENT_ID"]


def http(method, url, data=None, token=None, ctype=None):
    req = urllib.request.Request(url, data=data, method=method)
    if token:
        req.add_header("Authorization", "Bearer " + token)
    if ctype:
        req.add_header("Content-Type", ctype)
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            return r.status, json.loads(r.read().decode("utf-8") or "{}")
    except urllib.error.HTTPError as e:
        body = e.read().decode("utf-8", "replace")
        try:
            body = json.loads(body)
        except Exception:
            body = {"raw": body[:200]}
        return e.code, body


# --- 1. Token (Client Credentials) ------------------------------------------
st, tok = http("POST", f"https://login.microsoftonline.com/{tenant}/oauth2/v2.0/token",
               data=urllib.parse.urlencode({"client_id": client, "client_secret": secret,
                                            "scope": "https://graph.microsoft.com/.default",
                                            "grant_type": "client_credentials"}).encode(),
               ctype="application/x-www-form-urlencoded")
if st != 200:
    print(f"ROT  Token-Bezug: HTTP {st} — {tok.get('error')}: {str(tok.get('error_description', ''))[:120]}")
    print("     401/400 invalid_client = Secret oder App-ID falsch/abgelaufen; unauthorized_client = App nicht im Mandanten")
    sys.exit(1)
access = tok["access_token"]
print(f"OK   Token-Bezug (Mandant {tenant[:8]}…, App {client[:8]}…, gueltig {tok.get('expires_in')} s)")
del secret

# --- 2. Site aufloesen ------------------------------------------------------
u = urllib.parse.urlparse(site_url)
st, site = http("GET", f"https://graph.microsoft.com/v1.0/sites/{u.hostname}:{u.path}", token=access)
if st != 200:
    print(f"ROT  Site: HTTP {st} — {site.get('error', {}).get('code')}: {str(site.get('error', {}).get('message', ''))[:120]}")
    print("     403 = Site-Grant fehlt (Sites.Selected ohne Grant) · 404 = Site-Adresse · 401 = Token")
    sys.exit(2)
site_id = site["id"]
print(f"OK   Site erreichbar: HTTP 200 (Site-ID {site_id[:12]}…, erstellt {site.get('createdDateTime', '?')[:10]})")

# --- 3. Drives (Bibliotheken) der Site ---------------------------------------
st, drives = http("GET", f"https://graph.microsoft.com/v1.0/sites/{site_id}/drives", token=access)
if st != 200:
    print(f"ROT  Drives: HTTP {st}"); sys.exit(3)
libs = [d for d in drives.get("value", []) if d.get("driveType") == "documentLibrary"]
print(f"OK   Bibliotheken: {len(libs)}")
for d in libs:
    print(f"     - {d.get('name')}: drive_id {d['id']}")
if not libs:
    sys.exit(3)
drive = next((d for d in libs if d.get("name") in ("Documents", "Dokumente", "Freigegebene Dokumente", "Shared Documents")), libs[0])

# --- 4. Schreibrecht messen (Grant write) — leerer Probe-Ordner, danach weg ---
st, mk = http("POST", f"https://graph.microsoft.com/v1.0/drives/{drive['id']}/root/children",
              data=json.dumps({"name": ".jnpt-probe", "folder": {}, "@microsoft.graph.conflictBehavior": "fail"}).encode(),
              token=access, ctype="application/json")
if st in (200, 201):
    http("DELETE", f"https://graph.microsoft.com/v1.0/drives/{drive['id']}/items/{mk['id']}", token=access)
    print("OK   Schreiben in die Bibliothek: Ordner angelegt und wieder entfernt (Grant write greift)")
elif st == 409:
    print("OK   Schreiben: Probe-Ordner existiert schon (Grant write greift) — von Hand entfernen: .jnpt-probe")
else:
    print(f"ROT  Schreiben: HTTP {st} — {mk.get('error', {}).get('code')} (403 = Grant nur read)")

# --- 5. Gegenprobe: eine NICHT gegrantete Site muss 403 geben -----------------
st, root = http("GET", f"https://graph.microsoft.com/v1.0/sites/root", token=access)
print(f"{'OK ' if st in (403, 401) else 'ROT'}  Gegenprobe Wurzel-Site: HTTP {st} ({'Sites.Selected greift — nur gegrantete Sites' if st in (403, 401) else 'die App sieht MEHR als die eine Site — Berechtigung pruefen'})")

# --- 6. rclone-Remote schreiben (ohne Secret) --------------------------------
conf = f"{D}/rclone-{name}.conf" if name != "ablage" else "/var/lib/jnpt/rclone-ablage/rclone.conf"
# je Bringer eine Datei; die des Abgleichs liegt in einem jnpt-eigenen, BESCHREIBBAREN Verzeichnis:
# rclone legt das kurzlebige Zugriffs-Token in die Config und versucht das sonst 10x vergeblich (~7 s je Aufruf).
os.makedirs(os.path.dirname(conf), exist_ok=True)
alt = open(conf, encoding="utf-8").read() if os.path.exists(conf) else ""
if f"[{name}]" in alt:
    print(f"INFO [{name}] steht schon in rclone.conf — nicht ueberschrieben; drive_id oben vergleichen")
else:
    with open(conf, "a", encoding="utf-8") as f:
        f.write(f"\n[{name}]\ntype = onedrive\nclient_id = {client}\ntenant = {tenant}\n"
                f"client_credentials = true\ndrive_id = {drive['id']}\ndrive_type = documentLibrary\n")
    os.chmod(conf, 0o600)
    os.chown(conf, 65532 if name == "ablage" else 0, 65532 if name == "ablage" else 0)
    print(f"OK   rclone.conf: [{name}] mit drive_id der Bibliothek '{drive.get('name')}' geschrieben")
print("Naechster Schritt: sh rclone-probe.sh", name)
