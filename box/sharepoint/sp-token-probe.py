#!/usr/bin/env python3
"""sp-token-probe.py <sicherung|ablage> — was sich OHNE Site-URL messen laesst:
Token-Bezug (Secret/App/Tenant stimmen?), die 403-Gegenproben (Wurzel-Site, Site-Suche —
mit Sites.Selected muessen beide zu sein) und die Rollen im Token (welche App-Berechtigungen
der Mandant tatsaechlich erteilt hat). Gibt nur Status aus."""
import base64, json, sys, urllib.parse, urllib.request, urllib.error

name = sys.argv[1]
D = "/etc/jnpt/rclone"
app = dict(l.strip().split("=", 1) for l in open(f"{D}/{name}.app") if "=" in l)
secret = open(f"{D}/{name}.secret").read().strip()
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
        try:
            return e.code, json.loads(e.read().decode("utf-8", "replace") or "{}")
        except Exception:
            return e.code, {}


st, tok = http("POST", f"https://login.microsoftonline.com/{tenant}/oauth2/v2.0/token",
               data=urllib.parse.urlencode({"client_id": client, "client_secret": secret,
                                            "scope": "https://graph.microsoft.com/.default",
                                            "grant_type": "client_credentials"}).encode(),
               ctype="application/x-www-form-urlencoded")
del secret
if st != 200:
    print(f"ROT  [{name}] Token-Bezug: HTTP {st} — {tok.get('error')}: {str(tok.get('error_description', ''))[:140]}")
    sys.exit(1)
access = tok["access_token"]
claims = json.loads(base64.urlsafe_b64decode(access.split(".")[1] + "==").decode())
print(f"OK   [{name}] Token-Bezug: HTTP 200, gueltig {tok.get('expires_in')} s, App-Rollen im Token: {claims.get('roles', [])}")

st, _ = http("GET", "https://graph.microsoft.com/v1.0/sites/root", token=access)
print(f"{'OK ' if st == 403 else 'ROT'}  [{name}] Gegenprobe Wurzel-Site: HTTP {st} ({'zu — Sites.Selected greift' if st == 403 else 'offen?! Berechtigung breiter als Sites.Selected' if st == 200 else 'unerwartet'})")
st, body = http("GET", "https://graph.microsoft.com/v1.0/sites?search=*", token=access)
n = len(body.get("value", [])) if st == 200 else 0
print(f"{'OK ' if st == 403 else 'ROT'}  [{name}] Gegenprobe Site-Suche: HTTP {st}" + (f" — sieht {n} Sites (zu breit!)" if st == 200 else " (zu — richtig)"))
print("INFO Site-URL fehlt noch — mit Sites.Selected kann die App ihre Site nicht selbst finden; sie kommt vom Kunden (Adresszeile im Browser: https://<mandant>.sharepoint.com/sites/<Name>).")
