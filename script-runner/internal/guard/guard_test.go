package guard

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/script-runner/internal/frame"
)

func TestParseSeccomp(t *testing.T) {
	tests := []struct {
		status string
		want   int
	}{
		{"Name:\tpython\nSeccomp:\t2\nSeccomp_filters:\t1\n", 2},
		{"Seccomp:\t0\n", 0},
		{"Seccomp:  1\n", 1},
		{"Name:\tx\n", -1},
		{"Seccomp:\tzwei\n", -1},
	}
	for _, tt := range tests {
		if got := parseSeccomp(tt.status); got != tt.want {
			t.Errorf("parseSeccomp(%q) = %d, want %d", tt.status, got, tt.want)
		}
	}
}

func TestIsolationFromRelease(t *testing.T) {
	tests := []struct{ release, version, want string }{
		{"5.15.167.4-microsoft-standard-WSL2", "#1 SMP", "standard"},
		{"6.8.0-45-generic", "#45-Ubuntu SMP", "standard"},
		{"4.4.0-gvisor", "#1 SMP", "gvisor"},
		{"4.4.0", "#1 SMP Sun Jan 10 15:06:54 PST 2016", "gvisor"},
	}
	for _, tt := range tests {
		if got := isolation(tt.release, tt.version); got != tt.want {
			t.Errorf("isolation(%q, %q) = %q, want %q", tt.release, tt.version, got, tt.want)
		}
	}
}

func TestInstanceIDRandom(t *testing.T) {
	a, b := newInstanceID(), newInstanceID()
	if a == b || len(a) != 32 {
		t.Fatalf("Instanz-IDs %q / %q", a, b)
	}
}

func TestWriteFiles(t *testing.T) {
	root := t.TempDir()
	files := []frame.File{
		{Path: "main.py", DataB64: b64("print(1)\n")},
		{Path: "lib/helper.py", DataB64: b64("X=1\n")},
	}
	if err := writeFiles(root, files); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "lib", "helper.py"))
	if err != nil || string(b) != "X=1\n" {
		t.Fatalf("helper.py = %q, %v", b, err)
	}
	st, _ := os.Stat(filepath.Join(root, "main.py"))
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("Dateirechte %v, want 0600 (nie ausführbar)", st.Mode().Perm())
	}
}

func TestWriteFilesRejectsEscapes(t *testing.T) {
	for _, p := range []string{"../x.py", "/etc/x", "a/../../x", "", ".", "a//b", "a\\b"} {
		root := t.TempDir()
		err := writeFiles(root, []frame.File{{Path: p, DataB64: b64("x")}})
		if err == nil {
			t.Errorf("Pfad %q angenommen", p)
		}
	}
	root := t.TempDir()
	if err := writeFiles(root, []frame.File{{Path: "a.py", DataB64: "%%%"}}); err == nil {
		t.Error("kaputtes base64 angenommen")
	}
}

func TestJobLimits(t *testing.T) {
	// Der Wächter liest den job mit eigener Grenze; ein riesiger job ist ein
	// Fehler, kein Speicherproblem.
	if MaxJobFrame < 4<<20 || MaxJobFrame > 16<<20 {
		t.Fatalf("MaxJobFrame = %d", MaxJobFrame)
	}
}

// V1 (Fassung 2): die Reihenfolge nach dem Lauf ist fest.
type recOps struct {
	calls  []string
	others bool
}

func (r *recOps) SendExited()          { r.calls = append(r.calls, "exited") }
func (r *recOps) KillAll()             { r.calls = append(r.calls, "kill") }
func (r *recOps) ReapAll()             { r.calls = append(r.calls, "waitpid") }
func (r *recOps) OthersAlive() bool    { r.calls = append(r.calls, "check"); return r.others }
func (r *recOps) Close()               { r.calls = append(r.calls, "close") }
func (r *recOps) SleepUntil(time.Time) { r.calls = append(r.calls, "wait") }

func TestFinishOrder(t *testing.T) {
	r := &recOps{}
	exitNow := finish(r, time.Now(), MinLifetime)
	if exitNow {
		t.Fatal("exitNow ohne Restprozesse")
	}
	want := []string{"exited", "kill", "waitpid", "check", "close", "wait"}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("Reihenfolge %v, want %v", r.calls, want)
	}
}

// V3: Restprozesse nach kill(-1) → sofort beenden, nicht warten.
func TestFinishExitsImmediatelyOnSurvivors(t *testing.T) {
	r := &recOps{others: true}
	if !finish(r, time.Now(), MinLifetime) {
		t.Fatal("kein sofortiges Ende trotz Restprozessen")
	}
	for _, c := range r.calls {
		if c == "wait" {
			t.Fatalf("gewartet trotz Restprozessen: %v", r.calls)
		}
	}
}

func TestMinLifetime(t *testing.T) {
	// B1: 10,5 s ab Start, damit dockerd den Backoff zurücksetzt.
	if MinLifetime != 10500*time.Millisecond {
		t.Fatalf("MinLifetime = %v", MinLifetime)
	}
}

func TestProcessesOtherThanSelf(t *testing.T) {
	proc := t.TempDir()
	for _, d := range []string{"1", "self", "sys", "net"} {
		_ = os.MkdirAll(filepath.Join(proc, d), 0o755)
	}
	if others(proc) {
		t.Fatal("nur PID 1, trotzdem Restprozess gemeldet")
	}
	_ = os.MkdirAll(filepath.Join(proc, "57"), 0o755)
	if !others(proc) {
		t.Fatal("PID 57 nicht erkannt")
	}
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func TestChildEnvHasNoJnpt(t *testing.T) {
	for _, kv := range childEnv() {
		if strings.Contains(strings.ToLower(kv), "jnpt") {
			t.Fatalf("Umgebung des Skripts enthält %q", kv)
		}
	}
}
