#!/usr/bin/env python3
"""Lastsuite für die JanuaPort Box — reproduzierbar.

Fährt Stufen paralleler MCP-Sessions (je Session: initialize → tools/list →
tools/call in Endlosschleife) gegen https://<Box-Name>/mcp und danach eine
Dauerlast. Mischung: --read-share Anteil `*_read_statement` (Datei-Integration),
Rest `*_list_statements`. Es werden NUR Zähler und Latenz-Perzentile protokolliert,
nie Antwortinhalte — Kundendaten bleiben auf der Box. Standardbibliothek, kein pip.

Aufruf vom Admin-Gerät im selben LAN (Beispiel, Werte aus der .env der Box):
  python3 lasttest.py --host <LAN-IP> --name <Box-Name> --ca box-ca.crt \
      --token-file .tok --box-ssh root@<box> --log lasttest.log
  box-ca.crt = /usr/local/share/ca-certificates/jnpt-box-caddy-ca.crt der Box;
  .tok = Tool-Token (0600, per `ssh … cat > .tok` mit umask 077 holen, nie ins
  Terminal), Scopes nur auf die Datei-Integration; nach dem Lauf widerrufen.
  --probe zeigt nur Handshake + Werkzeugnamen. Die Mischung erwartet eine Datei-Integration
  mit `*_list_statements` (optional `*_read_statement`); ohne sie zählt jeder Aufruf als
  Fehler — vorher mit --probe prüfen.
"""
import argparse, http.client, json, os, random, socket, ssl, statistics, subprocess, sys, threading, time

def parse_body(resp):
    ct = resp.getheader("Content-Type", "")
    raw = resp.read()
    if "text/event-stream" in ct:
        datas = [l[5:].strip() for l in raw.decode("utf-8", "replace").splitlines() if l.startswith("data:")]
        return json.loads(datas[-1]) if datas else None, ct
    return (json.loads(raw) if raw else None), ct

class Client:
    def __init__(self, a):
        self.a = a; self.ctx = ssl.create_default_context(cafile=a.ca)
        self.token = open(a.token_file).read().strip(); self.sid = None; self.conn = None
    def connect(self):
        sock = socket.create_connection((self.a.host, 443), timeout=30)
        ssock = self.ctx.wrap_socket(sock, server_hostname=self.a.name)
        self.conn = http.client.HTTPSConnection(self.a.name, 443, context=self.ctx); self.conn.sock = ssock
    def post(self, payload, expect_body=True):
        if self.conn is None: self.connect()
        h = {"Authorization": "Bearer " + self.token, "Content-Type": "application/json",
             "Accept": "application/json, text/event-stream"}
        if self.sid: h["Mcp-Session-Id"] = self.sid
        self.conn.request("POST", "/mcp", body=json.dumps(payload), headers=h)
        r = self.conn.getresponse()
        sid = r.getheader("Mcp-Session-Id")
        if sid: self.sid = sid
        body, ct = parse_body(r)
        return r.status, body, ct
    def init(self):
        st, body, ct = self.post({"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"box-lasttest","version":"1"}}})
        if st != 200 or not body or "result" not in body: raise RuntimeError(f"initialize {st}")
        self.post({"jsonrpc":"2.0","method":"notifications/initialized"})
        return ct
    def tools(self):
        st, body, _ = self.post({"jsonrpc":"2.0","id":2,"method":"tools/list"})
        return [t["name"] for t in body["result"]["tools"]]
    def call(self, name, args):
        st, body, _ = self.post({"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":name,"arguments":args}})
        ok = st == 200 and body and "result" in body and not body["result"].get("isError")
        return ok, st

def find_file(c, list_tool):
    st, body, _ = c.post({"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":list_tool,"arguments":{}}})
    txt = "".join(x.get("text","") for x in body["result"].get("content", []))
    try: data = json.loads(txt)
    except Exception: return None
    def walk(o):
        if isinstance(o, dict):
            for k in ("file","name","path","datei"):
                if k in o and isinstance(o[k], str): return o[k]
            for v in o.values():
                r = walk(v)
                if r: return r
        if isinstance(o, list):
            for v in o:
                r = walk(v)
                if r: return r
    return walk(data)

def worker(a, tools, stop, out, idx):
    c = Client(a); lat = []; err = 0; n = 0
    try: c.init()
    except Exception: out[idx] = (0, 1, []); return
    lt, rt, f = tools
    while not stop.is_set():
        use_read = rt and f and random.random() < a.read_share
        t0 = time.perf_counter()
        try:
            ok, st = c.call(rt if use_read else lt, {"file": f} if use_read else {})
        except Exception:
            ok = False; c.conn = None; c.sid = None
            try: c.init()
            except Exception: pass
        dt = time.perf_counter() - t0; n += 1; lat.append(dt)
        if not ok: err += 1
    out[idx] = (n, err, lat)

def box_sample(a):
    if not a.box_ssh: return ""
    try:
        o = subprocess.run(["ssh","-n","-o","BatchMode=yes","-o","ConnectTimeout=8",a.box_ssh,
            "cut -d' ' -f1-3 /proc/loadavg; for z in /sys/class/thermal/thermal_zone*/temp; do cat $z; done | sort -n | tail -1"],
            capture_output=True, text=True, timeout=20).stdout.split()
        return f"load={o[0]}/{o[1]}/{o[2]} tmax={int(o[3])//1000}C" if len(o) >= 4 else " ".join(o)
    except Exception as e: return f"box-sample-fehler"

def stage(a, tools, parallel, seconds, label, log):
    stop = threading.Event(); out = [None]*parallel
    th = [threading.Thread(target=worker, args=(a, tools, stop, out, i), daemon=True) for i in range(parallel)]
    t0 = time.time(); [t.start() for t in th]; time.sleep(seconds); stop.set(); [t.join(timeout=60) for t in th]
    dur = time.time() - t0; lat = [x for o in out if o for x in o[2]]; n = sum(o[0] for o in out if o); err = sum(o[1] for o in out if o)
    q = lambda p: statistics.quantiles(lat, n=100)[p-1]*1000 if len(lat) >= 100 else (max(lat)*1000 if lat else 0)
    line = (f"{time.strftime('%FT%T')} {label} parallel={parallel} dauer={dur:.0f}s n={n} rps={n/dur:.1f} fehler={err} "
            f"p50={q(50):.0f}ms p95={q(95):.0f}ms p99={q(99):.0f}ms max={(max(lat)*1000 if lat else 0):.0f}ms {box_sample(a)}")
    print(line, flush=True); log.write(line+"\n"); log.flush()

def main():
    p = argparse.ArgumentParser()
    p.add_argument("--host", required=True, help="LAN-IP der Box"); p.add_argument("--name", required=True, help="Box-Name (JNPT_DOMAIN)")
    p.add_argument("--ca", required=True); p.add_argument("--token-file", required=True)
    p.add_argument("--stages", default="1,5,10,25,50"); p.add_argument("--stage-seconds", type=int, default=180)
    p.add_argument("--soak-parallel", type=int, default=10); p.add_argument("--soak-seconds", type=int, default=1800)
    p.add_argument("--read-share", type=float, default=0.3); p.add_argument("--log", default="lasttest.log")
    p.add_argument("--box-ssh", default=""); p.add_argument("--probe", action="store_true")
    a = p.parse_args()
    c = Client(a); ct = c.init(); names = c.tools()
    lt = next((t for t in names if t.endswith("_list_statements")), None)
    rt = next((t for t in names if t.endswith("_read_statement")), None)
    f = find_file(c, lt) if (lt and rt) else None
    print(f"probe: init-content-type={ct} session={'ja' if c.sid else 'nein'} tools={len(names)} namen={','.join(names)} list={lt} read={rt} lesedatei={'gefunden' if f else 'KEINE'}", flush=True)
    if a.probe: return
    with open(a.log, "a") as log:
        log.write(f"start {time.strftime('%FT%T')} host={a.host} name={a.name} read_share={a.read_share} tools={lt},{rt}\n")
        for s in [int(x) for x in a.stages.split(",") if x]:
            stage(a, (lt, rt, f), s, a.stage_seconds, f"stufe", log)
        if a.soak_seconds > 0:
            stage(a, (lt, rt, f), a.soak_parallel, a.soak_seconds, "dauerlast", log)
        log.write(f"ende {time.strftime('%FT%T')}\n")

if __name__ == "__main__": main()
