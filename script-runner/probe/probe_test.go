//go:build docker

// Docker-Sonden des Skript-Läufers (JanuaPort/januaport#928, P2).
//
// Laufen nur über probe/run.sh: Das Skript baut das Image, legt das Netz
// p2-928-mcp an (Stellvertreter für jnpt_default) und startet diesen Test in
// einem Go-Container, der an diesem Netz hängt, den Alias „jnpt“ trägt und
// den Docker-Socket des Hosts nutzt. Der Test selbst ist der Stub-Gateway
// (Weg B, :8484) und fährt die ausgelieferte Compose mit einer Test-Überlagerung
// hoch. Alle Namen tragen das Präfix p2-928-.
package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	project    = "p2-928-sr"
	projectDP  = "p2-928-dp"
	projectUC  = "p2-928-uc"
	sshdName   = "p2-928-sshd"
	runtimeKey = "probe-runtime-key-not-a-secret"
	runToken   = "jnptrun_PROBE_PROBE_PROBE_PROBE_PROBE_PROBE_PROBE_P"
	runID      = "0b1e4c8a-6f2d-4b7e-9a31-2c5d7e8f9a10"
)

var (
	image   = envOr("P2_IMAGE", "p2-928-script-runner:test")
	sshdImg = envOr("P2_SSHD_IMAGE", "p2-928-sshd:test")
	commit  string
	gw      *stubGateway
)

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

type stubGateway struct {
	calls atomic.Int64
	mu    sync.Mutex
	auth  []string
}

func (g *stubGateway) authHeaders() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.auth...)
}

func startStubGateway() *stubGateway {
	g := &stubGateway{}
	server := mcp.NewServer(&mcp.Implementation{Name: "stub-gateway", Version: "0"}, nil)
	server.AddTool(&mcp.Tool{Name: "kartei_find", InputSchema: map[string]any{"type": "object"}},
		func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			g.calls.Add(1)
			return &mcp.CallToolResult{
				Content:           []mcp.Content{&mcp.TextContent{Text: `{"found":2}`}},
				StructuredContent: map[string]any{"found": 2},
			}, nil
		})
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	go func() {
		_ = http.ListenAndServe(":8484", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			g.mu.Lock()
			g.auth = append(g.auth, r.Header.Get("Authorization"))
			g.mu.Unlock()
			h.ServeHTTP(w, r)
		}))
	}()
	return g
}

func run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

func must(t testing.TB, name string, args ...string) string {
	t.Helper()
	out, err := run(name, args...)
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return out
}

func compose(proj string, files []string, args ...string) (string, error) {
	full := []string{"compose", "-p", proj}
	for _, f := range files {
		full = append(full, "-f", f)
	}
	return run("docker", append(full, args...)...)
}

var (
	mainFiles = []string{"../docker-compose.yml", "compose.probe.yml"}
	dpFiles   = []string{"../docker-compose.yml", "compose.probe.yml", "compose.default-seccomp.yml"}
	ucFiles   = []string{"../docker-compose.yml", "compose.probe.yml", "compose.unconfined.yml"}
)

func TestMain(m *testing.M) {
	os.Setenv("JNPT_SCRIPT_RUNNER_IMAGE", image)
	gw = startStubGateway()
	code := func() int {
		defer cleanup()
		if err := setup(); err != nil {
			fmt.Fprintln(os.Stderr, "Aufbau gescheitert:", err)
			dumpLogs(project)
			return 1
		}
		c := m.Run()
		if c != 0 {
			dumpLogs(project)
		}
		return c
	}()
	os.Exit(code)
}

func dumpLogs(proj string) {
	out, _ := compose(proj, mainFiles, "logs", "--no-color", "--tail", "80")
	fmt.Fprintln(os.Stderr, out)
}

func cleanup() {
	if os.Getenv("P2_KEEP") != "" {
		return
	}
	_, _ = compose(project, mainFiles, "down", "-v", "--remove-orphans", "-t", "1")
	_, _ = compose(projectDP, dpFiles, "down", "-v", "--remove-orphans", "-t", "1")
	_, _ = compose(projectUC, ucFiles, "down", "-v", "--remove-orphans", "-t", "1")
	_, _ = run("docker", "rm", "-f", sshdName)
}

// setup: Skript-Repo bauen, SSH-Server mit Bare-Repo starten, Stack hoch,
// Schlüssel und pins.json ablegen, warten bis alle Pins ready sind.
func setup() error {
	cleanup()
	repo, err := os.MkdirTemp("", "p2-928-repo")
	if err != nil {
		return err
	}
	if out, err := run("cp", "-r", "scripts/.", repo); err != nil {
		return fmt.Errorf("cp: %v %s", err, out)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main", "."},
		{"add", "-A"},
		{"-c", "user.email=probe@example.com", "-c", "user.name=probe", "commit", "-q", "-m", "Sonden"},
	} {
		if out, err := run("git", append([]string{"-C", repo}, args...)...); err != nil {
			return fmt.Errorf("git %v: %v %s", args, err, out)
		}
	}
	out, err := run("git", "-C", repo, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	commit = strings.TrimSpace(out)
	if out, err := run("git", "clone", "-q", "--bare", repo, repo+".git"); err != nil {
		return fmt.Errorf("clone: %v %s", err, out)
	}

	if out, err := run("docker", "run", "-d", "--name", sshdName, sshdImg); err != nil {
		return fmt.Errorf("sshd: %v %s", err, out)
	}
	if out, err := run("docker", "cp", repo+".git", sshdName+":/srv/scripts.git"); err != nil {
		return fmt.Errorf("docker cp: %v %s", err, out)
	}
	if out, err := run("docker", "exec", sshdName, "sh", "-c",
		"chown -R git:git /srv/scripts.git && su git -s /bin/sh -c 'ssh-keygen -q -t ed25519 -N \"\" -f /tmp/id && cat /tmp/id.pub > /home/git/.ssh/authorized_keys && chmod 600 /home/git/.ssh/authorized_keys'"); err != nil {
		return fmt.Errorf("keygen: %v %s", err, out)
	}
	deployKey, err := run("docker", "exec", sshdName, "cat", "/tmp/id")
	if err != nil {
		return err
	}
	_, _ = run("docker", "exec", sshdName, "rm", "-f", "/tmp/id")

	if out, err := compose(project, mainFiles, "up", "-d"); err != nil {
		return fmt.Errorf("compose up: %v %s", err, out)
	}
	if out, err := run("docker", "network", "connect", "--alias", "git.example.com", project+"_egress", sshdName); err != nil {
		return fmt.Errorf("network connect: %v %s", err, out)
	}
	if err := seed(project+"_probe-keys", "runtime-key", runtimeKey+"\n"); err != nil {
		return err
	}
	if err := seed(project+"_probe-gitkey", "runtime-key", deployKey); err != nil {
		return err
	}
	if err := seed(project+"_probe-keys", "pins.json", pinsJSON()); err != nil {
		return err
	}
	return waitAllReady(3 * time.Minute)
}

var pinNames = []string{"ok_echo", "proc_probe", "sleeper", "residue", "seccomp_probe", "net_probe",
	"socket_dir", "second_conn", "forkbomb", "raise_canary", "memhog", "quick"}

func pinsJSON() string {
	var rows []map[string]string
	for _, n := range pinNames {
		rows = append(rows, map[string]string{"name": n, "path": n, "commit": commit})
	}
	b, _ := json.Marshal(map[string]any{"version": 1, "repo_url": "ssh://git@git.example.com/srv/scripts.git", "pins": rows})
	return string(b)
}

// seed schreibt eine Datei in ein Volume, so wie der Materialisierer des
// Gateways es auf dem Host tut: Ordner 0750, Eigentümer 65532.
func seed(volume, name, content string) error {
	cmd := exec.Command("docker", "run", "--rm", "-i", "--user", "0", "--entrypoint", "sh", "-v", volume+":/k", image, "-c",
		"umask 077 && cat > /k/"+name+".tmp && mv /k/"+name+".tmp /k/"+name+" && chown -R 65532:65532 /k && chmod 0750 /k")
	cmd.Stdin = strings.NewReader(content)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("seed %s/%s: %v %s", volume, name, err, out)
	}
	return nil
}

func mediatorURL(proj string) string {
	return "http://" + proj + "-script-runner-1:8090/mcp"
}

type headerRT struct{ h http.Header }

func (rt headerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range rt.h {
		r.Header[k] = v
	}
	return http.DefaultTransport.RoundTrip(r)
}

func session(proj string, h http.Header) (*mcp.ClientSession, error) {
	if h == nil {
		h = http.Header{}
	}
	h.Set("Authorization", "Bearer "+runtimeKey)
	client := mcp.NewClient(&mcp.Implementation{Name: "probe-gateway", Version: "0"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   mediatorURL(proj),
		HTTPClient: &http.Client{Transport: headerRT{h: h}, Timeout: 40 * time.Second},
	}, nil)
}

func listTools(proj string) (*mcp.ListToolsResult, error) {
	s, err := session(proj, nil)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	return s.ListTools(context.Background(), nil)
}

func waitAllReady(max time.Duration) error {
	deadline := time.Now().Add(max)
	var last string
	for time.Now().Before(deadline) {
		res, err := listTools(project)
		if err == nil {
			if len(res.Tools) == len(pinNames) {
				return nil
			}
			b, _ := json.Marshal(res.Meta)
			last = string(b)
		} else {
			last = err.Error()
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("nicht alle Pins ready: %s", last)
}

func call(t *testing.T, name string, args map[string]any) (*mcp.CallToolResult, map[string]any) {
	t.Helper()
	res, meta, err := tryCall(project, name, args)
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	return res, meta
}

func tryCall(proj, name string, args map[string]any) (*mcp.CallToolResult, map[string]any, error) {
	h := http.Header{}
	h.Set("X-Jnpt-Run-Token", runToken)
	h.Set("X-Jnpt-Run-Id", runID)
	h.Set("X-Jnpt-Pin-Commit", commit)
	s, err := session(proj, h)
	if err != nil {
		return nil, nil, err
	}
	defer s.Close()
	if args == nil {
		args = map[string]any{}
	}
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, nil, err
	}
	b, _ := json.Marshal(res.Meta["januaport.ai/run"])
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return res, m, nil
}

// callReady wiederholt bei busy, wie es eine KI nach dem festen Satz täte.
// lastCall ist die Dauer des letzten Aufrufs in callReady (ohne Wartezeit
// auf eine freie Sandbox).
var lastCall time.Duration

func callReady(t *testing.T, name string, args map[string]any) (*mcp.CallToolResult, map[string]any) {
	t.Helper()
	deadline := time.Now().Add(40 * time.Second)
	for {
		start := time.Now()
		res, m := call(t, name, args)
		lastCall = time.Since(start)
		if m["end"] != "busy" || time.Now().After(deadline) {
			return res, m
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func text(res *mcp.CallToolResult) string {
	if len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(*mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

func okJSON(t *testing.T, name string, args map[string]any) map[string]any {
	t.Helper()
	res, m := callReady(t, name, args)
	if m["end"] != "ok" {
		t.Fatalf("%s: Ende %v/%v, error_type %v", name, m["end"], m["detail"], m["error_type"])
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text(res)), &out); err != nil {
		t.Fatalf("%s: Ausgabe kein JSON: %q", name, text(res))
	}
	t.Logf("%s: %s", name, text(res))
	return out
}

func mediatorAlive(t *testing.T) {
	t.Helper()
	if _, err := listTools(project); err != nil {
		t.Fatalf("vermittler antwortet nicht mehr: %v", err)
	}
}

func TestOKRunWithToolCallAndVendor(t *testing.T) {
	before := gw.calls.Load()
	out := okJSON(t, "ok_echo", map[string]any{"q": "offen"})
	if out["greeting"] != "hallo" {
		t.Fatalf("vendor/ nicht importiert: %v", out)
	}
	if fmt.Sprint(out["found"]) != "map[found:2]" {
		t.Fatalf("Werkzeugergebnis = %v", out["found"])
	}
	if gw.calls.Load() != before+1 {
		t.Fatal("Stub-Gateway nicht gerufen")
	}
	for _, a := range gw.authHeaders() {
		if a != "Bearer "+runToken {
			t.Fatalf("Weg B mit %q", a)
		}
	}
}

func TestM2ProcOfGuardNotReadable(t *testing.T) {
	out := okJSON(t, "proc_probe", nil)
	// M2-Wortlaut: environ, mem und die Einträge unter /proc/1/fd sind nicht
	// öffenbar. Das bloße Auflisten von /proc/1/fd (Nummern, kein Inhalt)
	// lässt gVisor trotz PR_SET_DUMPABLE 0 zu; runc nicht. Das wird
	// protokolliert, nicht verlangt.
	for _, p := range []string{"/proc/1/environ", "/proc/1/mem", "/proc/1/maps", "/proc/1/root", "/proc/1/cwd"} {
		v, _ := out[p].(string)
		if v == "readable" || v == "listable" {
			t.Errorf("%s: %s", p, v)
		}
	}
	t.Logf("/proc/1/fd auflisten: %v, /proc/1/map_files: %v", out["/proc/1/fd"], out["/proc/1/map_files"])
	if fds, _ := out["/proc/1/fd/*"].([]any); len(fds) != 0 {
		t.Errorf("/proc/1/fd/* öffenbar: %v", fds)
	}
	if out["uid"] != float64(65533) {
		t.Errorf("uid = %v", out["uid"])
	}
}

func TestM2TimeoutByMediatorClock(t *testing.T) {
	res, m := callReady(t, "sleeper", nil)
	d := lastCall
	if m["end"] != "timeout" {
		t.Fatalf("Ende %v", m["end"])
	}
	if strings.Contains(text(res), "ok") && !strings.Contains(text(res), "Wandzeit") {
		t.Fatalf("Ausgabe trotz timeout: %q", text(res))
	}
	t.Logf("sleeper: timeout nach %v (Wandzeit 3 s, Uhr des vermittlers)", d)
	if d < 2900*time.Millisecond || d > 4500*time.Millisecond {
		t.Errorf("timeout nach %v, want ≈ 3 s", d)
	}
	mediatorAlive(t)
}

func TestM3G2ResidueAcrossRuns(t *testing.T) {
	first := okJSON(t, "residue", map[string]any{"mode": "write"})
	t.Logf("beschreibbar in Lauf 1: %v", first["writable"])
	second := okJSON(t, "residue", map[string]any{"mode": "check"})
	if found, _ := second["found"].([]any); len(found) != 0 {
		t.Fatalf("Rückstand aus Lauf 1: %v", found)
	}
	for _, out := range []map[string]any{first, second} {
		if out["work_writable"] != true {
			t.Fatalf("/work nicht beschreibbar: %v", out["work_writable"])
		}
		for _, k := range []string{"exec/work", "exec/tmp", "so/work", "so/tmp"} {
			if out[k] == "ran" || out[k] == "loaded" {
				t.Errorf("%s: %v", k, out[k])
			}
		}
	}
}

func TestS1SeccompDenyList(t *testing.T) {
	out := okJSON(t, "seccomp_probe", nil)
	for k, v := range out {
		if k == "seccomp_mode" {
			if v != "2" {
				t.Errorf("Seccomp-Modus %v", v)
			}
			continue
		}
		if v != "EPERM" {
			t.Errorf("%s: %v, want EPERM", k, v)
		}
	}
}

func TestR3R9NoNetworkNoJnptMounts(t *testing.T) {
	out := okJSON(t, "net_probe", map[string]any{"needle": runToken})
	if ifs, _ := out["ifaces"].([]any); fmt.Sprint(ifs) != "[lo]" && out["ifaces"] != "n/a" {
		t.Errorf("Interfaces %v", out["ifaces"])
	}
	for k, v := range out["net"].(map[string]any) {
		if v == "CONNECTED" {
			t.Errorf("Verbindung nach %s", k)
		}
	}
	if envs, _ := out["env_jnpt"].([]any); len(envs) != 0 {
		t.Errorf("jnpt in der Umgebung: %v", envs)
	}
	if out["run_jnpt"] != false || out["docker_sock"] != false {
		t.Errorf("run_jnpt=%v docker_sock=%v", out["run_jnpt"], out["docker_sock"])
	}
	// Die Nadel ist der Wert des Lauf-Tokens. Er kommt nur als Eingabe in den
	// Prozess (stdin), nie in eine Datei — jeder Fund im Dateisystem wäre ein
	// Leck, ebenso ein Fund des echten Tokens, das nur der vermittler hält.
	if hits, _ := out["needle_hits"].([]any); len(hits) != 0 {
		t.Errorf("Lauf-Token in Dateien: %v", hits)
	}
	allowed := map[string]bool{"/": true, "/work": true, "/tmp": true, "/run/script-runner": true, "/proc": true,
		"/dev": true, "/dev/pts": true, "/dev/mqueue": true, "/sys": true, "/sys/fs/cgroup": true,
		"/etc/hosts": true, "/etc/hostname": true, "/etc/resolv.conf": true, "/dev/console": true}
	for _, mnt := range out["mounts"].([]any) {
		s := mnt.(string)
		if !allowed[s] && !strings.HasPrefix(s, "/proc/") && !strings.HasPrefix(s, "/sys/") && !strings.HasPrefix(s, "/dev/") {
			t.Errorf("unerwarteter Mount %s", s)
		}
	}
}

func TestM4SocketDirReadOnly(t *testing.T) {
	out := okJSON(t, "socket_dir", nil)
	// SEC: der Socket-Ordner /run/script-runner enthält genau den Socket.
	if fmt.Sprint(out["entries"]) != "[sandbox.sock]" {
		t.Errorf("/run/script-runner enthält %v, want genau [sandbox.sock]", out["entries"])
	}
	for _, k := range []string{"create", "unlink", "rename"} {
		if out[k] == "ok" {
			t.Errorf("%s im Socket-Ordner gelungen", k)
		}
	}
}

func instanceRegistrations(t *testing.T) []string {
	t.Helper()
	out := must(t, "docker", "logs", project+"-script-runner-1")
	var ids []string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "sandbox registered") {
			if i := strings.Index(line, "instance="); i >= 0 {
				ids = append(ids, strings.Fields(line[i+len("instance="):])[0])
			}
		}
	}
	return ids
}

func TestV1SecondConnection(t *testing.T) {
	before := instanceRegistrations(t)
	_, m := callReady(t, "second_conn", nil)
	if m["end"] != "limit" || m["detail"] != "sandbox_lost" {
		t.Fatalf("Ende %v/%v, want limit/sandbox_lost", m["end"], m["detail"])
	}
	_, m = callReady(t, "quick", nil)
	if m["end"] != "ok" {
		t.Fatalf("Folgelauf %v", m["end"])
	}
	after := instanceRegistrations(t)
	if len(after) <= len(before) {
		t.Fatalf("keine neue Instanz registriert: %v → %v", before, after)
	}
	for _, id := range after {
		if id == "attacker" {
			t.Fatal("die zweite Verbindung wurde als Instanz registriert")
		}
	}
	t.Logf("Instanzen vorher %d, nachher %d, letzte %s", len(before), len(after), after[len(after)-1])
}

func TestG1ForkBombMediatorSurvives(t *testing.T) {
	_, m := callReady(t, "forkbomb", nil)
	t.Logf("Fork-Bombe: Ende %v/%v, error_type %v", m["end"], m["detail"], m["error_type"])
	ok := (m["end"] == "limit" && m["detail"] == "sandbox_lost") || m["end"] == "error"
	if !ok {
		t.Fatalf("Ende %v/%v", m["end"], m["detail"])
	}
	mediatorAlive(t)
	_, m = callReady(t, "quick", nil)
	if m["end"] != "ok" {
		t.Fatalf("Folgelauf nach der Fork-Bombe: %v", m["end"])
	}
}

func TestM6CanaryNeverLeaves(t *testing.T) {
	canary := fmt.Sprintf("KANARIE-%d", time.Now().UnixNano())
	res, m := callReady(t, "raise_canary", map[string]any{"canary": canary})
	if m["end"] != "error" || m["detail"] != "exception" {
		t.Fatalf("Ende %v/%v", m["end"], m["detail"])
	}
	if m["error_type"] != "ValueError" {
		t.Errorf("error_type %v", m["error_type"])
	}
	if at, _ := m["error_at"].(string); !strings.HasPrefix(at, "raise_canary/main.py:") {
		t.Errorf("error_at %v", m["error_at"])
	}
	b, _ := json.Marshal(res)
	if strings.Contains(string(b), canary) {
		t.Fatalf("Kanarie in der Antwort: %s", b)
	}
	for _, svc := range []string{"script-runner", "script-runner-sandbox", "script-runner-fetcher"} {
		out, _ := run("docker", "logs", project+"-"+svc+"-1")
		if strings.Contains(out, canary) {
			t.Errorf("Kanarie im Log von %s", svc)
		}
	}
}

func TestMemoryLimit(t *testing.T) {
	_, m := callReady(t, "memhog", nil)
	t.Logf("memhog: Ende %v/%v", m["end"], m["detail"])
	if m["end"] != "limit" || (m["detail"] != "memory" && m["detail"] != "sandbox_lost") {
		t.Fatalf("Ende %v/%v", m["end"], m["detail"])
	}
	mediatorAlive(t)
}

// B1: kurze Läufe in Folge — der zweite startet nach ≤ ~12 s, ohne
// wachsenden Backoff.
func TestB1BackToBackRuns(t *testing.T) {
	var gaps []time.Duration
	last := time.Now()
	for i := 0; i < 5; i++ {
		_, m := callReady(t, "quick", nil)
		if m["end"] != "ok" {
			t.Fatalf("Lauf %d: %v", i, m["end"])
		}
		now := time.Now()
		if i > 0 {
			gaps = append(gaps, now.Sub(last))
		}
		last = now
	}
	t.Logf("B1: Abstände zwischen fertigen Läufen %v", gaps)
	for i, g := range gaps {
		if g > 13*time.Second {
			t.Errorf("Lauf %d nach %v", i+1, g)
		}
	}
	if len(gaps) >= 3 && gaps[len(gaps)-1] > gaps[0]+3*time.Second {
		t.Errorf("wachsender Abstand: %v", gaps)
	}
	out := must(t, "docker", "inspect", "-f", "{{.RestartCount}}", project+"-script-runner-sandbox-1")
	t.Logf("B1: RestartCount der Sandbox %s", strings.TrimSpace(out))
}

func TestRefusedCommitOnWrongHeader(t *testing.T) {
	h := http.Header{}
	h.Set("X-Jnpt-Run-Token", runToken)
	h.Set("X-Jnpt-Run-Id", runID)
	h.Set("X-Jnpt-Pin-Commit", strings.Repeat("9", 40))
	s, err := session(project, h)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: "quick", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res.Meta)
	if !strings.Contains(string(b), `"detail":"commit"`) || !strings.Contains(string(b), `"end":"refused"`) {
		t.Fatalf("_meta = %s", b)
	}
}

func TestR13NoCredential401(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, mediatorURL(project), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Status %d", resp.StatusCode)
	}
}

// R12: kein pip im Image; git ist ein sha1dc-Build.
func TestR12ImageHasNoPipAndSha1dcGit(t *testing.T) {
	for _, c := range []string{"command -v pip", "command -v pip3", "python3 -m pip --version", "python3 -m ensurepip --version"} {
		if out, err := run("docker", "run", "--rm", "--entrypoint", "sh", image, "-c", c); err == nil {
			t.Errorf("%q gelungen: %s", c, out)
		}
	}
	opts := must(t, "docker", "run", "--rm", "--entrypoint", "git", image, "version", "--build-options")
	t.Logf("git version --build-options:\n%s", opts)
	if strings.Contains(strings.ToLower(opts), "sha1dc") || strings.Contains(opts, "SHA1_DC") {
		return
	}
	// Ältere git-Fassungen nennen das SHA-1-Backend nicht. Dann gilt die
	// Meldung, die nur der sha1dc-Code enthält, als Beleg im Binary.
	out, err := run("docker", "run", "--rm", "--entrypoint", "sh", image, "-c",
		"grep -c 'SHA-1 appears to be part of a collision attack' /usr/lib/git-core/git")
	if err != nil || strings.TrimSpace(out) == "0" {
		t.Fatalf("kein Beleg für sha1dc: %s %v", out, err)
	}
	t.Logf("sha1dc-Beleg: Kollisionsmeldung im git-Binary (%s Treffer)", strings.TrimSpace(out))
}

// F3 B2, Gegenprobe zum ganzen Lauf oben: mit unserem Profil meldet der
// Wächter profile "jnpt" und wird bedient.
func TestB2OwnProfileRegistered(t *testing.T) {
	out := must(t, "docker", "logs", project+"-script-runner-1")
	if !strings.Contains(out, "seccomp=2 profile=jnpt usable=true") {
		t.Fatalf("keine Registrierung mit unserem Profil:\n%s", out)
	}
}

// F3 B2: Sandbox mit dem Docker-Standardprofil (unser Profil weggelassen) →
// profile "other" → isolation invalid, keine Werkzeuge, kein Lauf. Unter
// Docker Desktop beweist das mehr als unconfined (dort Modus 2).
func TestB2DockerDefaultProfileFailsClosed(t *testing.T) {
	b2FailsClosed(t, projectDP, dpFiles)
}

// F3 B2 (SEC-Gate): Sandbox ganz ohne Seccomp → ebenfalls invalid.
func TestB2UnconfinedFailsClosed(t *testing.T) {
	b2FailsClosed(t, projectUC, ucFiles)
}

func b2FailsClosed(t *testing.T, proj string, files []string) {
	t.Helper()
	if out, err := compose(proj, files, "up", "-d", "--no-deps", "script-runner", "script-runner-sandbox"); err != nil {
		t.Fatalf("compose up: %v %s", err, out)
	}
	defer func() { _, _ = compose(proj, files, "down", "-v", "-t", "1") }()
	if err := seed(proj+"_probe-keys", "runtime-key", runtimeKey+"\n"); err != nil {
		t.Fatal(err)
	}
	if err := seed(proj+"_probe-keys", "pins.json", pinsJSON()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(60 * time.Second)
	var meta string
	var tools int
	for time.Now().Before(deadline) {
		out, _ := run("docker", "logs", proj+"-script-runner-1")
		if res, err := listTools(proj); err == nil && strings.Contains(out, "sandbox registered") {
			b, _ := json.Marshal(res.Meta)
			meta, tools = string(b), len(res.Tools)
			break
		}
		time.Sleep(time.Second)
	}
	logs, _ := run("docker", "logs", proj+"-script-runner-1")
	t.Logf("Registrierung: %s", grepLine(logs, "sandbox registered"))
	if !strings.Contains(logs, "profile=other usable=false") {
		t.Fatalf("Wächter meldet nicht profile=other:\n%s", logs)
	}
	if !strings.Contains(meta, `"isolation":"invalid"`) || tools != 0 || strings.Contains(meta, `"state":"ready"`) {
		t.Fatalf("nicht fail-closed: tools=%d %s", tools, meta)
	}
	_, m, err := tryCall(proj, "quick", nil)
	if err != nil {
		t.Fatal(err)
	}
	if m["end"] != "refused" || m["detail"] != "state" {
		t.Fatalf("Aufruf: %v/%v", m["end"], m["detail"])
	}
	t.Logf("B2: tools/list isolation invalid, 0 Werkzeuge, Aufruf %v/%v", m["end"], m["detail"])
}

// SEC zu /healthz: aus dem Netz erreichbar, Antwort nur „ok“, andere Pfade 404.
func TestHealthzOnlyOK(t *testing.T) {
	resp, err := http.Get("http://" + project + "-script-runner-1:8091/healthz")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(b) != "ok\n" {
		t.Fatalf("/healthz: %d %q", resp.StatusCode, b)
	}
	resp2, err := http.Get("http://" + project + "-script-runner-1:8091/mcp")
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("/mcp auf dem Health-Port: %d", resp2.StatusCode)
	}
}

func grepLine(s, needle string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, needle) {
			return l
		}
	}
	return ""
}
