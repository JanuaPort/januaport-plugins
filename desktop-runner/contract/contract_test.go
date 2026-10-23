package contract_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"
	"unicode"
	"unicode/utf8"

	"github.com/pjbgf/sha1cd"
)

// Ausschließlich Artefaktprüfer und Generator; kein HTTP-/Geräteadapter.
var update = flag.Bool("update", false, "Beispielpakete und SHA256SUMS deterministisch erzeugen")

const sumsSHA256 = "b92e8407ab306a16d2bffc7706d5652c2f800147fcb10a4bb38602a9598353ec"

type wire struct {
	Method   string            `json:"method,omitempty"`
	Path     string            `json:"path,omitempty"`
	Status   int               `json:"status,omitempty"`
	Headers  map[string]string `json:"headers"`
	Body     json.RawMessage   `json:"body,omitempty"`
	BodyFile string            `json:"body_file,omitempty"`
}
type job struct {
	JobID       string          `json:"job_id"`
	Pin         string          `json:"pin"`
	Commit      string          `json:"commit"`
	Path        string          `json:"path"`
	Algo        string          `json:"algo"`
	ManifestSeq uint64          `json:"manifest_seq"`
	Input       json.RawMessage `json:"input"`
	Limits      struct {
		WallS     int `json:"wall_s"`
		ToolCalls int `json:"tool_calls"`
	} `json:"limits"`
	StartBy string `json:"start_by"`
}
type config struct {
	Contract int `json:"contract"`
	Device   struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Kind string `json:"kind"`
	} `json:"device"`
	ExpectedAccount string `json:"expected_account"`
	StartPolicy     struct {
		Mode        string `json:"mode"`
		IdleMinutes int    `json:"idle_minutes"`
	} `json:"start_policy"`
	HeartbeatS int `json:"heartbeat_s"`
	PollHoldS  int `json:"poll_hold_s"`
}
type heartbeat struct {
	Contract      int     `json:"contract"`
	Ready         *bool   `json:"ready"`
	RunnerVersion string  `json:"runner_version"`
	ManifestSeq   *uint64 `json:"manifest_seq"`
}
type result struct {
	End       string  `json:"end"`
	Detail    string  `json:"detail,omitempty"`
	ErrorType string  `json:"error_type,omitempty"`
	ErrorAt   string  `json:"error_at,omitempty"`
	Log       *string `json:"log"`
}
type packageHead struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
	Algo    string `json:"algo"`
	Commit  string `json:"commit"`
	Path    string `json:"path"`
	Objects int    `json:"objects"`
}
type object struct {
	kind byte
	data []byte
}

func strict(b []byte, v any) error {
	if !utf8.Valid(b) {
		return fmt.Errorf("UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var rest any
	if err := d.Decode(&rest); err != io.EOF {
		return fmt.Errorf("Daten nach JSON")
	}
	return nil
}
func read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("golden", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var fixtures = map[string]string{
	"error_409_busy.json": "error",
	"pair_request.json":   "pair", "pair_response.json": "pair_response",
	"manifest_request.json": "empty", "manifest_response.json": "manifest",
	"runs_start_request.json": "runs_start", "runs_start_response.json": "start",
	"scheduled_result_request.json": "report", "offline_result_request.json": "report",
	"refused_offline_result_request.json": "report",
	"error_401_revoked.json":              "error",
	"error_409_manifest.json":             "error",
	"config_request.json":                 "empty", "config_response.json": "config",
	"heartbeat_request.json": "heartbeat", "heartbeat_response.json": "cancel",
	"claim_request.json": "empty", "claim_response.json": "job", "claim_empty_response.json": "empty",
	"package_request.json": "empty", "package_response.json": "binary",
	"start_request.json": "empty", "start_response.json": "start",
	"mcp_request.json": "mcp_request", "mcp_response.json": "mcp_response",
	"result_request.json": "report", "result_response.json": "empty", "result_repeat_response.json": "empty",
	"error_401.json": "error", "error_404_unknown.json": "error", "error_404_foreign.json": "error",
	"error_409_already_started.json": "error", "error_409_not_claimed.json": "error", "error_409_ended.json": "error",
	"error_413.json": "error", "error_400_contract.json": "error", "error_400_invalid.json": "error",
	"error_503.json": "error", "error_429.json": "error",
}

func checkBody(kind string, b []byte) error {
	switch kind {
	case "pair":
		var v struct {
			Contract      int    `json:"contract"`
			Code          string `json:"code"`
			RunnerVersion string `json:"runner_version"`
		}
		if err := strict(b, &v); err != nil {
			return err
		}
		if v.Contract != 1 || !regexp.MustCompile(`^[0-9a-f]{16}\.[0-9A-HJKMNP-TV-Z]{4}(-[0-9A-HJKMNP-TV-Z]{4}){2}$`).MatchString(v.Code) || v.RunnerVersion == "" {
			return fmt.Errorf("pair")
		}
	case "pair_response":
		var v pairing
		if err := strict(b, &v); err != nil {
			return err
		}
		pub, err := base64.StdEncoding.Strict().DecodeString(v.AnlagePubkey)
		if err != nil || len(pub) != ed25519.PublicKeySize || v.DeviceID == "" || !strings.HasPrefix(v.Token, "jnpt_") || !utcTime(v.ExpiresAt) {
			return fmt.Errorf("pair response")
		}
	case "manifest":
		var v signedManifest
		if err := strict(b, &v); err != nil {
			return err
		}
		_, err := parseManifest(v.Manifest)
		return err
	case "runs_start":
		var v struct {
			RunKey string `json:"run_key"`
		}
		if err := strict(b, &v); err != nil {
			return err
		}
		if !validRunKey(v.RunKey) {
			return fmt.Errorf("run_key")
		}
	case "report":
		return checkReport(b)
	case "empty", "binary":
		if len(b) != 0 {
			return fmt.Errorf("Körper muss fehlen")
		}
	case "config":
		var v config
		if err := strict(b, &v); err != nil {
			return err
		}
		if v.Contract != 1 || v.HeartbeatS != 60 || v.PollHoldS != 25 || v.Device.ID == "" || v.Device.Name == "" || v.ExpectedAccount == "" || (v.Device.Kind != "dedicated" && v.Device.Kind != "workstation") || (v.StartPolicy.Mode != "always" && v.StartPolicy.Mode != "idle") || v.StartPolicy.IdleMinutes < 0 {
			return fmt.Errorf("config")
		}
	case "heartbeat":
		var v heartbeat
		if len(b) > 1024 {
			return fmt.Errorf("heartbeat zu groß")
		}
		if err := strict(b, &v); err != nil {
			return err
		}
		if v.Contract != 1 || v.Ready == nil || v.RunnerVersion == "" || v.ManifestSeq == nil {
			return fmt.Errorf("heartbeat")
		}
	case "cancel":
		var v struct {
			Cancel             []string `json:"cancel"`
			ManifestSeqCurrent *uint64  `json:"manifest_seq_current"`
		}
		if err := strict(b, &v); err != nil {
			return err
		}
		if v.Cancel == nil || v.ManifestSeqCurrent == nil {
			return fmt.Errorf("cancel fehlt")
		}
	case "job":
		var v struct {
			Job job `json:"job"`
		}
		if err := strict(b, &v); err != nil {
			return err
		}
		j := v.Job
		if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(j.JobID) || j.Pin == "" || !safePath(j.Path) || !validID(j.Algo, j.Commit) || j.ManifestSeq == 0 || j.Limits.WallS < 1 || j.Limits.WallS > 86400 || j.Limits.ToolCalls < 0 || j.StartBy == "" {
			return fmt.Errorf("job")
		}
		if len(j.Input) > 256*1024 || len(bytes.TrimSpace(j.Input)) == 0 || bytes.TrimSpace(j.Input)[0] != '{' {
			return fmt.Errorf("input")
		}
	case "start":
		var v struct {
			RunToken  string `json:"run_token"`
			ExpiresAt string `json:"expires_at"`
			MCPPath   string `json:"mcp_path"`
		}
		if err := strict(b, &v); err != nil {
			return err
		}
		if v.RunToken == "" || v.ExpiresAt == "" || v.MCPPath != "/api/device/v1/mcp" {
			return fmt.Errorf("start")
		}
	case "result":
		var v result
		if len(b) > 80*1024 {
			return fmt.Errorf("result zu groß")
		}
		if err := strict(b, &v); err != nil {
			return err
		}
		if v.Log == nil || len(*v.Log) > 64*1024 {
			return fmt.Errorf("log")
		}
		allowed := map[string][]string{"ok": {}, "error": {}, "timeout": {}, "limit": {"tool_calls"}, "internal": {}, "cancelled": {}, "refused": {"hash", "runtime", "account", "manifest", "offline"}, "interrupted": {}}
		details, ok := allowed[v.End]
		if !ok {
			return fmt.Errorf("end")
		}
		if len(details) == 0 && v.Detail != "" {
			return fmt.Errorf("detail")
		}
		if len(details) > 0 {
			found := false
			for _, d := range details {
				found = found || d == v.Detail
			}
			if !found {
				return fmt.Errorf("detail")
			}
		}
	case "mcp_request":
		if len(b) > 8*1024*1024 {
			return fmt.Errorf("MCP zu groß")
		}
		var v struct {
			JSONRPC string `json:"jsonrpc"`
			ID      int    `json:"id"`
			Method  string `json:"method"`
			Params  struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if err := strict(b, &v); err != nil {
			return err
		}
		if v.JSONRPC != "2.0" || v.Method != "tools/call" || v.Params.Name == "" {
			return fmt.Errorf("MCP")
		}
	case "mcp_response":
		var v struct {
			JSONRPC string `json:"jsonrpc"`
			ID      int    `json:"id"`
			Result  struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
				IsError bool `json:"isError"`
			} `json:"result"`
		}
		return strict(b, &v)
	case "error":
		var v struct {
			Error struct {
				Class   string          `json:"class"`
				Message json.RawMessage `json:"message"`
			} `json:"error"`
		}
		if err := strict(b, &v); err != nil {
			return err
		}
		if v.Error.Class == "revoked" {
			if len(v.Error.Message) != 0 {
				return fmt.Errorf("revoked ohne message")
			}
			return nil
		}
		var message string
		if err := strict(v.Error.Message, &message); err != nil {
			return err
		}
		if errorMessages[v.Error.Class] != message || message == "" {
			return fmt.Errorf("fester Fehlersatz")
		}
	default:
		return fmt.Errorf("unbekannte Golden-Art")
	}
	return nil
}

var errorMessages = map[string]string{
	"busy":         "Das Gerät ist belegt.",
	"manifest":     "Das Manifest ist nicht aktuell.",
	"unauthorized": "Die Anmeldung ist fehlgeschlagen.", "not_found": "Der Auftrag oder das Paket wurde nicht gefunden.",
	"already_started": "Der Auftrag wurde bereits gestartet.", "not_claimed": "Der Auftrag wurde nicht abgeholt.", "ended": "Der Auftrag ist bereits beendet.",
	"too_large": "Die Anfrage überschreitet die zulässige Größe.", "contract": "Die Vertragsfassung wird nicht unterstützt.", "invalid": "Die Anfrage ist ungültig.",
	"unavailable": "Der Dienst ist vorübergehend nicht verfügbar.", "rate_limited": "Es sind zu viele Anfragen eingegangen.",
}

func checkWire(kind string, b []byte) error {
	var w wire
	if err := strict(b, &w); err != nil {
		return err
	}
	if len(w.Body) > 0 && w.Headers["Content-Type"] != "application/json" {
		return fmt.Errorf("JSON-Medientyp")
	}
	if _, ok := w.Headers["Mcp-Session-Id"]; ok {
		return fmt.Errorf("Sitzungszustand")
	}
	if w.Status == 0 {
		if !strings.HasPrefix(w.Path, "/api/device/v1/") || (w.Method != "GET" && w.Method != "POST") || w.Headers["X-Jnpt-Desktop-Contract"] != "1" {
			return fmt.Errorf("Anfragerahmen")
		}
		if w.Path == "/api/device/v1/pair" {
			if _, present := w.Headers["Authorization"]; present {
				return fmt.Errorf("pair ohne Bearer")
			}
		} else if w.Headers["Authorization"] != "Bearer jnpt_example_<secret>" {
			return fmt.Errorf("Läufer-Token")
		}
		if w.Path == "/api/device/v1/mcp" && w.Headers["X-Jnpt-Run-Token"] != "<lauf-token>" {
			return fmt.Errorf("Lauf-Token")
		}
		if w.Path == "/api/device/v1/runs/start" {
			s := w.Headers["X-Jnpt-Manifest-Seq"]
			seq, err := strconv.ParseUint(s, 10, 64)
			if err != nil || seq == 0 || strconv.FormatUint(seq, 10) != s {
				return fmt.Errorf("Manifest-seq")
			}
		}
	} else {
		if w.Headers["Cache-Control"] != "no-store" || w.Method != "" || w.Path != "" {
			return fmt.Errorf("Antwortrahmen")
		}
		if (w.Status == 429 || w.Status == 503) && w.Headers["Retry-After"] != "60" {
			return fmt.Errorf("Retry-After")
		}
		if w.Status == 204 && (len(w.Body) != 0 || w.BodyFile != "") {
			return fmt.Errorf("204 mit Körper")
		}
	}
	if kind == "binary" {
		if w.BodyFile != "package_sha1.bin" || w.Headers["Content-Type"] != "application/vnd.jnpt.gitobjects.v1" {
			return fmt.Errorf("Paketverweis")
		}
	} else if w.BodyFile != "" {
		return fmt.Errorf("unerwartete Binärdaten")
	}
	return checkBody(kind, w.Body)
}

func TestGoldenJSON(t *testing.T) {
	h, _ := sample("sha1", "100644", "hello.txt")
	routes := map[string][2]string{
		"pair_request.json": {"POST", "pair"}, "manifest_request.json": {"GET", "manifest"},
		"runs_start_request.json":       {"POST", "runs/start"},
		"scheduled_result_request.json": {"POST", "runs/result"}, "offline_result_request.json": {"POST", "runs/result"},
		"refused_offline_result_request.json": {"POST", "runs/result"},
		"config_request.json":                 {"GET", "config"}, "heartbeat_request.json": {"POST", "heartbeat"},
		"claim_request.json": {"POST", "jobs/claim"}, "package_request.json": {"GET", "packages/" + h.Commit + "?path=demo"},
		"start_request.json": {"POST", "jobs/7d95f836-ded8-4a37-9017-3cebc3c4ee27/start"},
		"mcp_request.json":   {"POST", "mcp"}, "result_request.json": {"POST", "runs/result"},
	}
	for name, kind := range fixtures {
		t.Run(name, func(t *testing.T) {
			b := read(t, name)
			if err := checkWire(kind, b); err != nil {
				t.Fatal(err)
			}
			var w wire
			if err := strict(b, &w); err != nil {
				t.Fatal(err)
			}
			if route, ok := routes[name]; ok {
				if w.Status != 0 || w.Method != route[0] || w.Path != "/api/device/v1/"+route[1] {
					t.Fatal("Methode/Pfad")
				}
			} else {
				status := 200
				if kind == "empty" {
					status = 204
				}
				if kind == "error" {
					status, _ = strconv.Atoi(strings.TrimSuffix(strings.Split(name, "_")[1], ".json"))
				}
				if w.Status != status {
					t.Fatal("HTTP-Status")
				}
			}
		})
	}
	if !bytes.Equal(read(t, "error_404_unknown.json"), read(t, "error_404_foreign.json")) {
		t.Fatal("404 muss byte-gleich sein")
	}
}

func TestForbiddenJSON(t *testing.T) {
	for _, field := range []string{"session_status", "idle_minutes", "account", "unexpected"} {
		t.Run(field, func(t *testing.T) {
			b := []byte(`{"contract":1,"ready":true,"runner_version":"0.0.0","manifest_seq":0,` + fmt.Sprintf("%q", field) + `:0}`)
			if checkBody("heartbeat", b) == nil {
				t.Fatal("verbotenes Heartbeat-Feld akzeptiert")
			}
		})
	}
	for _, b := range []string{
		`{"end":"ok","log":"","output":"verboten"}`, `{"end":"ok","log":"","detail":"frei"}`,
		`{"end":"expired","log":""}`, `{"end":"lost","log":""}`, `{"end":"denied","log":""}`,
		`{"end":"refused","detail":"frei","log":""}`, `{"end":"limit","detail":"output","log":""}`,
		`{"end":"neu","log":""}`, `{"end":"ok","log":""} {}`,
	} {
		if checkBody("result", []byte(b)) == nil {
			t.Fatalf("verbotenes Ergebnis akzeptiert: %s", b)
		}
	}
	if checkBody("heartbeat", []byte(`{"contract":2,"ready":true,"runner_version":"0.0.0","manifest_seq":0}`)) == nil {
		t.Fatal("andere Kennung akzeptiert")
	}
	log := strings.Repeat("x", 64*1024)
	b, _ := json.Marshal(map[string]any{"end": "ok", "log": log})
	if err := checkBody("result", b); err != nil {
		t.Fatal("64 KiB inklusiv", err)
	}
	b, _ = json.Marshal(map[string]any{"end": "ok", "log": log + "x"})
	if checkBody("result", b) == nil {
		t.Fatal("Log über Grenze")
	}
	b, _ = json.Marshal(map[string]any{"end": "ok", "log": strings.Repeat("\x00", 14000)})
	if checkBody("result", b) == nil {
		t.Fatal("Gesamtkörper über Grenze trotz kurzem Log")
	}
	if checkBody("heartbeat", []byte(`{"contract":1,"ready":true,"manifest_seq":0,"runner_version":"`+strings.Repeat("x", 1024)+`"}`)) == nil {
		t.Fatal("Heartbeat über Grenze")
	}
	for _, name := range []string{"config_request.json", "mcp_request.json", "error_429.json", "error_503.json", "result_response.json"} {
		var w wire
		if err := strict(read(t, name), &w); err != nil {
			t.Fatal(err)
		}
		keys := []string{"Cache-Control"}
		if w.Status == 0 {
			keys = []string{"Authorization", "X-Jnpt-Desktop-Contract"}
			if strings.Contains(name, "mcp") {
				keys = append(keys, "X-Jnpt-Run-Token")
			}
		}
		if w.Status == 429 || w.Status == 503 {
			keys = append(keys, "Retry-After")
		}
		for _, key := range keys {
			old := w.Headers[key]
			delete(w.Headers, key)
			b, _ := json.Marshal(w)
			if checkWire(fixtures[name], b) == nil {
				t.Fatalf("fehlender Header %s in %s", key, name)
			}
			w.Headers[key] = old
		}
	}
}

func validID(algo, id string) bool {
	n := 40
	if algo == "sha256" {
		n = 64
	} else if algo != "sha1" {
		return false
	}
	b, err := hex.DecodeString(id)
	return err == nil && len(b)*2 == n && id == strings.ToLower(id)
}
func safeName(name string) bool {
	if name == "" || name == "." || name == ".." || !utf8.ValidString(name) || strings.TrimRight(name, ". ") != name {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) || strings.ContainsRune(`<>:"/\|?*`, r) {
			return false
		}
	}
	base := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" {
		return false
	}
	for i := 1; i <= 9; i++ {
		if base == fmt.Sprint("COM", i) || base == fmt.Sprint("LPT", i) {
			return false
		}
	}
	return true
}
func safePath(p string) bool {
	for _, s := range strings.Split(p, "/") {
		if !safeName(s) {
			return false
		}
	}
	return true
}
func objectID(algo string, o object) (string, error) {
	types := map[byte]string{'c': "commit", 't': "tree", 'b': "blob"}
	typ, ok := types[o.kind]
	if !ok {
		return "", fmt.Errorf("Objekttyp")
	}
	b := append([]byte(fmt.Sprintf("%s %d\x00", typ, len(o.data))), o.data...)
	return digest(algo, b)
}

func digest(algo string, b []byte) (string, error) {
	if algo == "sha256" {
		h := sha256.Sum256(b)
		return hex.EncodeToString(h[:]), nil
	}
	if algo != "sha1" {
		return "", fmt.Errorf("Algorithmus")
	}
	h, collision := sha1cd.Sum(b) // SHA-1DC statt crypto/sha1 (SEC C3).
	if collision {
		return "", fmt.Errorf("SHA-1-Kollision")
	}
	return hex.EncodeToString(h[:]), nil
}

func TestSHA1Collision(t *testing.T) {
	// SHA-mbles, 640-Byte-Kollisionsprobe aus sha1cd v0.7.0:
	// https://github.com/pjbgf/sha1cd/blob/v0.7.0/test/testdata/files/sha-mbles-1.bin
	const collision = "mQQNBH/oF4ABIAD/S2V5IGlzIHBhcnQgb2YgYSBjb2xsaXNpb24hIEl0J3MgYSB0cmFwIXnGGvCvzAVFFdknTnMHYksdx/sjmIu43otXXbp7nqsxwWdLbZdDeKgncy/1hRx2ouYHcrWkfOHqxAu5k8EtjHDiSk+NX83twbMsnPGeMa8kKXWdQuTf2zFxn1h2I+5VKTm23NxFn8pTVTtw+H7eMKJH6jr2x1mi8gsyDXYNtk/0eQhP08yzzdSDYtlqnEMGF8r/bDbGN+U/3ihBf2Jv7FTteUOkbl9XMPK7OPsd9uAJABDQDiSteL+SZBmTYI6NFYp4nzTEb+HmAn81pMv7gnB2xQ7KDot8ymm7LCt5Aln5v5Vw3Y1EN6MRX6/3w8rAmtJSZgVcJxBHVReOrv+CWiyqKs+13mTOdkHcWaVBqfycdWdW4uI9xxPIwkyXkKprDjin9V8URSocooUN3ZVi/ZoYrUJJaqlwCPdGcvaO9GHriLCZM9YmtPkYdJzAJ/3dbEJfxCFoNdATTRUoW6sst4Sk98u0+1FNS/D2I3zwCp6fEyuaBm5v0X9sQph0eFhv9lGvlnR/tCa5hyuaiOQGP1m7M0zABlD4OoDEJ1G3GXTTAPwoGaLo8eMsG1HLGOa/xNubrvZ11Kr1sVdKBH+PbdLsFTqTQSKTl02Sj4jO2TY8/vl84udCvzTJa47zh1Z2/qXMqOX33qC6skE9TeAO5x7gHxYr220er9kl5q66rmo1TvF88gWkBPvbEvxFTUH92VzyRZZkoq0DLR2mCnMmQHXX8eDWwUA656DYYd8/5XBxiN1eB9FYm5+LZjBVP4/DUrPgwn2oC926TGQCDQ=="
	b, err := base64.StdEncoding.DecodeString(collision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := digest("sha1", b); err == nil {
		t.Fatal("SHA-1-Kollision akzeptiert")
	}
	if _, err := digest("sha256", []byte("harmlos")); err != nil {
		t.Fatal(err)
	}
}

func TestForbiddenSizesAndSession(t *testing.T) {
	mcp := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"example_read","arguments":{"padding":"` + strings.Repeat("x", 8*1024*1024) + `"}}}`)
	if checkBody("mcp_request", mcp) == nil {
		t.Fatal("MCP über 8 MiB akzeptiert")
	}
	var w wire
	if err := strict(read(t, "claim_response.json"), &w); err != nil {
		t.Fatal(err)
	}
	var v struct {
		Job job `json:"job"`
	}
	if err := strict(w.Body, &v); err != nil {
		t.Fatal(err)
	}
	v.Job.Input = []byte(`{"padding":"` + strings.Repeat("x", 256*1024) + `"}`)
	b, _ := json.Marshal(v)
	if checkBody("job", b) == nil {
		t.Fatal("Eingabe über 256 KiB akzeptiert")
	}
	if err := strict(read(t, "mcp_response.json"), &w); err != nil {
		t.Fatal(err)
	}
	w.Headers["Mcp-Session-Id"] = "verboten"
	b, _ = json.Marshal(w)
	if checkWire("mcp_response", b) == nil {
		t.Fatal("MCP-Sitzungszustand akzeptiert")
	}
}

func TestForbiddenEnvelopes(t *testing.T) {
	var w wire
	if err := strict(read(t, "config_request.json"), &w); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"Authorization", "X-Jnpt-Desktop-Contract"} {
		old := w.Headers[key]
		w.Headers[key] = "falsch"
		b, _ := json.Marshal(w)
		if checkWire("empty", b) == nil {
			t.Fatalf("falscher Header %s akzeptiert", key)
		}
		w.Headers[key] = old
	}
	w.Path = "/api/plugin/other/config"
	b, _ := json.Marshal(w)
	if checkWire("empty", b) == nil {
		t.Fatal("falsches Präfix akzeptiert")
	}
	w = wire{}
	if err := strict(read(t, "error_401.json"), &w); err != nil {
		t.Fatal(err)
	}
	if err := checkWire("error", read(t, "error_401.json")); err != nil {
		t.Fatal("Positivkontrolle", err)
	}
	w.Body = []byte(`{"error":{"class":"unauthorized","message":"interner Text"}}`)
	b, _ = json.Marshal(w)
	if checkWire("error", b) == nil {
		t.Fatal("freier Fehlersatz akzeptiert")
	}
	w = wire{}
	if err := strict(read(t, "result_response.json"), &w); err != nil {
		t.Fatal(err)
	}
	w.Body = []byte(`{}`)
	w.Headers["Content-Type"] = "application/json"
	b, _ = json.Marshal(w)
	if checkWire("empty", b) == nil {
		t.Fatal("204 mit Körper akzeptiert")
	}
	if checkBody("result", []byte("{\"end\":\"ok\",\"log\":\""+string([]byte{0xff})+"\"}")) == nil {
		t.Fatal("ungültiges UTF-8 akzeptiert")
	}
}

func TestPackageSize(t *testing.T) {
	// Auch ohne Größensperre wäre dieses Paket vollständig und hashgültig.
	h, o := sampleWithBlob("sha256", "100644", "large.bin", make([]byte, 128*1024*1024))
	if decodePackage(encodePackage(h, o), h.Algo, h.Commit, h.Path) == nil {
		t.Fatal("Paket über 128 MiB akzeptiert")
	}
}
func frame(w *bytes.Buffer, b []byte) {
	_ = binary.Write(w, binary.BigEndian, uint32(len(b)))
	w.Write(b)
}
func encodePackage(h packageHead, objects []object) []byte {
	var w bytes.Buffer
	b, _ := json.Marshal(h)
	frame(&w, b)
	for _, o := range objects {
		frame(&w, append([]byte{o.kind}, o.data...))
	}
	return w.Bytes()
}
func decodePackage(b []byte, algo, commit, pinPath string) error {
	if len(b) > 128*1024*1024 {
		return fmt.Errorf("Paketgröße")
	}
	r := bytes.NewReader(b)
	next := func() ([]byte, error) {
		var n uint32
		if err := binary.Read(r, binary.BigEndian, &n); err != nil {
			return nil, err
		}
		if n == 0 || uint64(n) > uint64(r.Len()) {
			return nil, fmt.Errorf("Rahmenlänge")
		}
		b := make([]byte, int(n))
		_, err := io.ReadFull(r, b)
		return b, err
	}
	b, err := next()
	if err != nil {
		return err
	}
	var h packageHead
	if err := strict(b, &h); err != nil {
		return err
	}
	if h.Format != "gitobjects" || h.Version != 1 || h.Algo != algo || h.Commit != commit || h.Path != pinPath || !validID(algo, commit) || !safePath(pinPath) || h.Objects < 1 || h.Objects > r.Len()/5 {
		return fmt.Errorf("Paketkopf")
	}
	objects := map[string]object{}
	for i := 0; i < h.Objects; i++ {
		b, err := next()
		if err != nil {
			return err
		}
		o := object{b[0], b[1:]}
		id, err := objectID(algo, o)
		if err != nil {
			return err
		}
		if _, ok := objects[id]; ok {
			return fmt.Errorf("doppeltes Objekt")
		}
		objects[id] = o
	}
	if r.Len() != 0 {
		return fmt.Errorf("Daten nach Paket")
	}
	used := map[string]bool{}
	get := func(id string, kind byte) (object, error) {
		o, ok := objects[id]
		if !ok || o.kind != kind {
			return o, fmt.Errorf("fehlendes/falsches Objekt")
		}
		used[id] = true
		return o, nil
	}
	c, err := get(commit, 'c')
	if err != nil {
		return err
	}
	first, _, _ := strings.Cut(string(c.data), "\n")
	if !strings.HasPrefix(first, "tree ") {
		return fmt.Errorf("Commitbaum")
	}
	root := strings.TrimPrefix(first, "tree ")
	if !validID(algo, root) {
		return fmt.Errorf("Baum-ID")
	}
	parts := strings.Split(pinPath, "/")
	var walk func(string, []string, int) error
	walk = func(id string, remaining []string, depth int) error {
		if depth > 256 {
			return fmt.Errorf("Baumtiefe")
		}
		o, err := get(id, 't')
		if err != nil {
			return err
		}
		data := o.data
		names := map[string]bool{}
		found := false
		width := 20
		if algo == "sha256" {
			width = 32
		}
		for len(data) > 0 {
			n := bytes.IndexByte(data, 0)
			if n < 0 || len(data) < n+1+width {
				return fmt.Errorf("Baumrahmen")
			}
			mode, name, ok := strings.Cut(string(data[:n]), " ")
			if !ok || !safeName(name) {
				return fmt.Errorf("Name")
			}
			fold := strings.Map(func(r rune) rune { return unicode.ToLower(unicode.ToUpper(r)) }, name)
			if names[fold] {
				return fmt.Errorf("Namenskollision")
			}
			names[fold] = true
			if mode != "40000" && mode != "100644" && mode != "100755" {
				return fmt.Errorf("Modus")
			}
			child := hex.EncodeToString(data[n+1 : n+1+width])
			data = data[n+1+width:]
			if len(remaining) > 0 {
				if name != remaining[0] {
					continue
				}
				if mode != "40000" {
					return fmt.Errorf("Pin ist kein Baum")
				}
				found = true
				if err := walk(child, remaining[1:], depth+1); err != nil {
					return err
				}
			} else if mode == "40000" {
				if err := walk(child, nil, depth+1); err != nil {
					return err
				}
			} else if _, err := get(child, 'b'); err != nil {
				return err
			}
		}
		if len(remaining) > 0 && !found {
			return fmt.Errorf("Pin fehlt")
		}
		return nil
	}
	if err := walk(root, parts, 0); err != nil {
		return err
	}
	if len(used) != len(objects) {
		return fmt.Errorf("überzählige Objekte")
	}
	return nil
}
func sample(algo, mode, name string) (packageHead, []object) {
	return sampleWithBlob(algo, mode, name, []byte("Desktop-Läufer golden v1\n"))
}

func sampleWithBlob(algo, mode, name string, content []byte) (packageHead, []object) {
	blob := object{'b', content}
	blobID, _ := objectID(algo, blob)
	rawBlob, _ := hex.DecodeString(blobID)
	pin := object{'t', append([]byte(mode+" "+name+"\x00"), rawBlob...)}
	pinID, _ := objectID(algo, pin)
	rawPin, _ := hex.DecodeString(pinID)
	root := object{'t', append([]byte("40000 demo\x00"), rawPin...)}
	rootID, _ := objectID(algo, root)
	commit := object{'c', []byte("tree " + rootID + "\nauthor Golden <golden@example.com> 0 +0000\ncommitter Golden <golden@example.com> 0 +0000\n\nDesktop-Läufer v1\n")}
	commitID, _ := objectID(algo, commit)
	return packageHead{"gitobjects", 1, algo, commitID, "demo", 4}, []object{commit, root, pin, blob}
}
func TestGenerateGolden(t *testing.T) {
	if !*update {
		return
	}
	if err := os.MkdirAll("golden", 0755); err != nil {
		t.Fatal(err)
	}
	for _, algo := range []string{"sha1", "sha256"} {
		h, o := sample(algo, "100644", "hello.txt")
		if err := os.WriteFile("golden/package_"+algo+".bin", encodePackage(h, o), 0644); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s commit %s", algo, h.Commit)
	}
	var w wire
	if err := strict(read(t, "manifest_response.json"), &w); err != nil {
		t.Fatal(err)
	}
	var v signedManifest
	if err := strict(w.Body, &v); err != nil {
		t.Fatal(err)
	}
	canonicalBytes, err := canonical(v.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	v.Sig = base64.StdEncoding.EncodeToString(ed25519.Sign(testKey(t), canonicalBytes))
	w.Body, err = json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("golden/manifest_response.json", append(b, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("golden/manifest_canonical.bin", canonicalBytes, 0644); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir("golden")
	if err != nil {
		t.Fatal(err)
	}
	var sums strings.Builder
	for _, e := range entries {
		if e.Name() == "SHA256SUMS" {
			continue
		}
		b := read(t, e.Name())
		fmt.Fprintf(&sums, "%x *%s\n", sha256.Sum256(b), e.Name())
	}
	if err := os.WriteFile("golden/SHA256SUMS", []byte(sums.String()), 0644); err != nil {
		t.Fatal(err)
	}
	t.Logf("SHA256SUMS: %x", sha256.Sum256([]byte(sums.String())))
}
func TestPackages(t *testing.T) {
	for _, algo := range []string{"sha1", "sha256"} {
		t.Run(algo, func(t *testing.T) {
			h, o := sample(algo, "100644", "hello.txt")
			b := read(t, "package_"+algo+".bin")
			if !bytes.Equal(b, encodePackage(h, o)) {
				t.Fatal("Paket nicht deterministisch")
			}
			if err := decodePackage(b, algo, h.Commit, h.Path); err != nil {
				t.Fatal(err)
			}
		})
	}
	var claim wire
	if err := strict(read(t, "claim_response.json"), &claim); err != nil {
		t.Fatal(err)
	}
	var v struct {
		Job job `json:"job"`
	}
	if err := strict(claim.Body, &v); err != nil {
		t.Fatal(err)
	}
	if err := decodePackage(read(t, "package_sha1.bin"), v.Job.Algo, v.Job.Commit, v.Job.Path); err != nil {
		t.Fatal("Auftrag passt nicht zum Paket", err)
	}
}
func TestForbiddenPackages(t *testing.T) {
	for _, algo := range []string{"sha1", "sha256"} {
		t.Run(algo, func(t *testing.T) {
			h, o := sample(algo, "100644", "hello.txt")
			reject := func(name string, b []byte) {
				t.Helper()
				t.Run(name, func(t *testing.T) {
					if decodePackage(b, algo, h.Commit, h.Path) == nil {
						t.Fatal("verbotenes Paket akzeptiert")
					}
				})
			}
			b := encodePackage(h, o)
			reject("truncated", b[:len(b)-1])
			reject("trailing", append(b, 0))
			bad := append([]byte(nil), b...)
			bad[len(bad)-1] ^= 1
			reject("blob_hash", bad)
			bad = append([]byte(nil), b...)
			binary.BigEndian.PutUint32(bad, 0xffffffff)
			reject("length", bad)
			for _, version := range []int{0, 2} {
				hh := h
				hh.Version = version
				reject(fmt.Sprint("version", version), encodePackage(hh, o))
			}
			hh := h
			hh.Objects--
			reject("missing", encodePackage(hh, o[:len(o)-1]))
			extra := append(append([]object(nil), o...), object{'b', []byte("extra")})
			hh = h
			hh.Objects++
			reject("extra", encodePackage(hh, extra))
			extra[len(extra)-1] = o[0]
			reject("duplicate", encodePackage(hh, extra))
			for _, mode := range []string{"120000", "160000", "100600"} {
				mh, mo := sample(algo, mode, "hello.txt")
				if decodePackage(encodePackage(mh, mo), algo, mh.Commit, mh.Path) == nil {
					t.Fatalf("Modus %s akzeptiert", mode)
				}
			}
			for _, name := range []string{"..", ".", "", "a/b", "a\\b", "C:foo", "CON", "nul.txt", "x.", "x ", "a\n", string([]byte{0xff})} {
				mh, mo := sample(algo, "100644", name)
				if decodePackage(encodePackage(mh, mo), algo, mh.Commit, mh.Path) == nil {
					t.Fatalf("Name %q akzeptiert", name)
				}
			}
			if decodePackage(b, algo, strings.Repeat("0", len(h.Commit)), h.Path) == nil {
				t.Fatal("fremder Commit akzeptiert")
			}
			if decodePackage(b, algo, h.Commit, "other") == nil {
				t.Fatal("fremder Pfad akzeptiert")
			}
		})
	}
}
func verifySums(dir string, manifest []byte) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	want := map[string]bool{}
	for _, e := range entries {
		if e.Name() != "SHA256SUMS" {
			if e.IsDir() {
				return fmt.Errorf("Unterordner")
			}
			want[e.Name()] = true
		}
	}
	lines := strings.Split(strings.TrimSuffix(string(manifest), "\n"), "\n")
	previous := ""
	for _, line := range lines {
		fields := strings.Split(line, " *")
		if len(fields) != 2 || len(fields[0]) != 64 || !want[fields[1]] || fields[1] <= previous {
			return fmt.Errorf("Summenformat/Inventar")
		}
		previous = fields[1]
		delete(want, fields[1])
		b, err := os.ReadFile(filepath.Join(dir, fields[1]))
		if err != nil {
			return err
		}
		if fmt.Sprintf("%x", sha256.Sum256(b)) != fields[0] {
			return fmt.Errorf("Summe: %s", fields[1])
		}
	}
	if len(want) != 0 {
		return fmt.Errorf("Datei ohne Summe")
	}
	return nil
}
func TestSHA256SUMS(t *testing.T) {
	manifest := read(t, "SHA256SUMS")
	if fmt.Sprintf("%x", sha256.Sum256(manifest)) != sumsSHA256 {
		t.Fatal("gepinntes SHA256SUMS geändert")
	}
	if err := verifySums("golden", manifest); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir("golden")
	if err != nil {
		t.Fatal(err)
	}
	var got, want []string
	for name := range fixtures {
		want = append(want, name)
	}
	want = append(want, "package_sha1.bin", "package_sha256.bin", "SHA256SUMS", "manifest_test_key.json", "manifest_canonical.bin")
	for _, e := range entries {
		got = append(got, e.Name())
	}
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatal("Golden-Inventar weicht ab")
	}
}
func TestForbiddenSHA256SUMS(t *testing.T) {
	dir := t.TempDir()
	b := []byte("original")
	if err := os.WriteFile(filepath.Join(dir, "a.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(fmt.Sprintf("%x *a.json\n", sha256.Sum256(b)))
	if err := verifySums(dir, manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.json"), []byte("verändert"), 0600); err != nil {
		t.Fatal(err)
	}
	if verifySums(dir, manifest) == nil {
		t.Fatal("veränderte Datei akzeptiert")
	}
	if err := os.WriteFile(filepath.Join(dir, "a.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "extra.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if verifySums(dir, manifest) == nil {
		t.Fatal("Datei ohne Summe akzeptiert")
	}
}
func TestPythonAllowlist(t *testing.T) {
	b, err := os.ReadFile("python-allowlist.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Version int `json:"version"`
		Entries []struct {
			Version  string `json:"version"`
			Filename string `json:"filename"`
			SHA256   string `json:"sha256"`
		} `json:"entries"`
	}
	if err := strict(b, &v); err != nil {
		t.Fatal(err)
	}
	if v.Version != 1 || len(v.Entries) != 2 {
		t.Fatal("kuratierte Liste mit zwei Versionen erwartet")
	}
	known := map[string]string{"3.12.10": "4acbed6dd1c744b0376e3b1cf57ce906f9dc9e95e68824584c8099a63025a3c3", "3.13.9": "91d828c2da3a029b41699e918674a0cb379c02cf20dab9c501306885f837402a"}
	for _, e := range v.Entries {
		if e.Filename != "python-"+e.Version+"-embed-amd64.zip" || known[e.Version] != e.SHA256 || !validID("sha256", e.SHA256) {
			t.Fatal("offizieller Laufzeiteintrag geändert")
		}
		delete(known, e.Version)
	}
	if len(known) != 0 {
		t.Fatal("Laufzeit fehlt")
	}
}

type pairing struct {
	DeviceID     string `json:"device_id"`
	Token        string `json:"token"`
	ExpiresAt    string `json:"expires_at"`
	AnlagePubkey string `json:"anlage_pubkey"`
}
type signedManifest struct {
	Manifest json.RawMessage `json:"manifest"`
	Sig      string          `json:"sig"`
}
type deviceManifest struct {
	Contract   int           `json:"contract"`
	DeviceID   string        `json:"device_id"`
	Seq        uint64        `json:"seq"`
	IssuedAt   string        `json:"issued_at"`
	GraceHours int           `json:"grace_hours"`
	Pins       []manifestPin `json:"pins"`
}
type manifestPin struct {
	Pin           string             `json:"pin"`
	Commit        string             `json:"commit"`
	Path          string             `json:"path"`
	Algo          string             `json:"algo"`
	DeclaredTools []string           `json:"declared_tools"`
	Schedules     []manifestSchedule `json:"schedules"`
}
type manifestSchedule struct {
	Name  string          `json:"name"`
	Cron  string          `json:"cron"`
	TZ    string          `json:"tz"`
	Input json.RawMessage `json:"input"`
}
type runReport struct {
	result
	RunKey      string  `json:"run_key,omitempty"`
	JobID       string  `json:"job_id,omitempty"`
	ManifestSeq *uint64 `json:"manifest_seq"`
	Commit      string  `json:"commit"`
	RanOffline  *bool   `json:"ran_offline"`
	FinishedAt  string  `json:"finished_at"`
}

func utcTime(s string) bool {
	_, err := time.Parse(time.RFC3339, s)
	return err == nil && strings.HasSuffix(s, "Z")
}
func validRunKey(s string) bool {
	parts := strings.Split(s, "/")
	return len(parts) == 3 && safeName(parts[0]) && safeName(parts[1]) && utcTime(parts[2]) && strings.HasSuffix(parts[2], ":00Z")
}
func checkReport(b []byte) error {
	if len(b) > 80*1024 {
		return fmt.Errorf("result zu groß")
	}
	var v runReport
	if err := strict(b, &v); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := strict(b, &fields); err != nil {
		return err
	}
	_, hasJob := fields["job_id"]
	_, hasRun := fields["run_key"]
	if hasJob == hasRun {
		return fmt.Errorf("genau ein Identitätsfeld")
	}
	if (v.JobID == "") == (v.RunKey == "") || v.ManifestSeq == nil || *v.ManifestSeq == 0 || (!validID("sha1", v.Commit) && !validID("sha256", v.Commit)) || v.RanOffline == nil || !utcTime(v.FinishedAt) {
		return fmt.Errorf("Ergebnisbindung")
	}
	if v.RunKey != "" && !validRunKey(v.RunKey) {
		return fmt.Errorf("run_key")
	}
	if v.JobID != "" && !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(v.JobID) {
		return fmt.Errorf("job_id")
	}
	resultFields, err := json.Marshal(v.result)
	if err != nil {
		return err
	}
	return checkBody("result", resultFields)
}

// Der Kanonisierer ist ausschließlich ein Leser für signierte Testartefakte.
// Zahlentoken bleiben verlustfrei; niemals float64 für seq oder Eingaben.
func canonical(b []byte) ([]byte, error) {
	if !utf8.Valid(b) || !validUnicodeEscapes(b) {
		return nil, fmt.Errorf("UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var value func() (any, error)
	value = func() (any, error) {
		tok, err := d.Token()
		if err != nil {
			return nil, err
		}
		switch tok {
		case json.Delim('{'):
			obj := map[string]any{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return nil, err
				}
				k, ok := key.(string)
				if !ok {
					return nil, fmt.Errorf("Schlüssel")
				}
				if _, exists := obj[k]; exists {
					return nil, fmt.Errorf("doppelter Schlüssel")
				}
				v, err := value()
				if err != nil {
					return nil, err
				}
				obj[k] = v
			}
			end, err := d.Token()
			if err != nil || end != json.Delim('}') {
				return nil, fmt.Errorf("Objektende")
			}
			return obj, nil
		case json.Delim('['):
			arr := []any{}
			for d.More() {
				v, err := value()
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			end, err := d.Token()
			if err != nil || end != json.Delim(']') {
				return nil, fmt.Errorf("Listenende")
			}
			return arr, nil
		default:
			switch tok.(type) {
			case nil, bool, string, json.Number:
				return tok, nil
			}
			return nil, fmt.Errorf("JSON-Wert")
		}
	}
	v, err := value()
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, fmt.Errorf("Daten nach JSON")
	}
	var out bytes.Buffer
	quote := func(s string) {
		var q bytes.Buffer
		e := json.NewEncoder(&q)
		e.SetEscapeHTML(false)
		_ = e.Encode(s)
		out.Write(bytes.TrimSuffix(q.Bytes(), []byte{'\n'}))
	}
	var emit func(any)
	emit = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			out.WriteByte('{')
			for i, k := range keys {
				if i > 0 {
					out.WriteByte(',')
				}
				quote(k)
				out.WriteByte(':')
				emit(x[k])
			}
			out.WriteByte('}')
		case []any:
			out.WriteByte('[')
			for i, item := range x {
				if i > 0 {
					out.WriteByte(',')
				}
				emit(item)
			}
			out.WriteByte(']')
		case string:
			quote(x)
		case json.Number:
			out.WriteString(x.String())
		case bool:
			if x {
				out.WriteString("true")
			} else {
				out.WriteString("false")
			}
		case nil:
			out.WriteString("null")
		}
	}
	emit(v)
	return out.Bytes(), nil
}
func validUnicodeEscapes(b []byte) bool {
	inString := false
	for i := 0; i < len(b); i++ {
		if b[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || b[i] != '\\' {
			continue
		}
		if i+1 >= len(b) {
			return false
		}
		if b[i+1] != 'u' {
			i++
			continue
		}
		if i+6 > len(b) {
			return false
		}
		n, err := strconv.ParseUint(string(b[i+2:i+6]), 16, 16)
		if err != nil {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+12 > len(b) || b[i+6] != '\\' || b[i+7] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(b[i+8:i+12]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 11
		} else {
			if n >= 0xdc00 && n <= 0xdfff {
				return false
			}
			i += 5
		}
	}
	return true
}

func parseManifest(b []byte) (deviceManifest, error) {
	var m deviceManifest
	if _, err := canonical(b); err != nil {
		return m, err
	}
	if err := strict(b, &m); err != nil {
		return m, err
	}
	if m.Contract != 1 || m.DeviceID == "" || m.Seq == 0 || !utcTime(m.IssuedAt) || m.GraceHours < 1 || m.GraceHours > 72 || m.Pins == nil {
		return m, fmt.Errorf("manifest")
	}
	pins := map[string]bool{}
	for _, p := range m.Pins {
		if !safeName(p.Pin) || pins[p.Pin] || !validID(p.Algo, p.Commit) || !safePath(p.Path) || p.DeclaredTools == nil || p.Schedules == nil {
			return m, fmt.Errorf("manifest pin")
		}
		pins[p.Pin] = true
		names := map[string]bool{}
		for _, s := range p.Schedules {
			if !safeName(s.Name) || names[s.Name] || len(strings.Fields(s.Cron)) != 5 || s.TZ == "" || s.TZ == "Local" || len(s.Input) > 256*1024 || len(bytes.TrimSpace(s.Input)) == 0 || bytes.TrimSpace(s.Input)[0] != '{' {
				return m, fmt.Errorf("manifest schedule")
			}
			names[s.Name] = true
			if _, err := time.LoadLocation(s.TZ); err != nil {
				return m, err
			}
		}
	}
	return m, nil
}

type manifestState struct {
	Seq      uint64
	Bytes    []byte
	LastSync time.Time
}

func acceptManifest(state *manifestState, envelope signedManifest, pub ed25519.PublicKey, device string, now time.Time) error {
	m, err := parseManifest(envelope.Manifest)
	if err != nil {
		return fmt.Errorf("manifest")
	}
	b, err := canonical(envelope.Manifest)
	if err != nil {
		return fmt.Errorf("manifest")
	}
	sig, err := base64.StdEncoding.Strict().DecodeString(envelope.Sig)
	if err != nil || len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, b, sig) {
		return fmt.Errorf("manifest")
	}
	if m.DeviceID != device {
		return fmt.Errorf("manifest")
	}
	if m.Seq <= state.Seq {
		return fmt.Errorf("manifest")
	}
	*state = manifestState{m.Seq, append([]byte(nil), b...), now}
	return nil
}
func offlineAllowed(p manifestPin, m deviceManifest, lastSync, now time.Time) bool {
	return len(p.DeclaredTools) == 0 && !lastSync.IsZero() && !now.Before(lastSync) && now.Sub(lastSync) < time.Duration(m.GraceHours)*time.Hour
}
func testKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	var k struct {
		TestOnly  bool   `json:"test_only"`
		Note      string `json:"note"`
		SeedHex   string `json:"seed_hex"`
		PublicKey string `json:"public_key_base64"`
	}
	if err := strict(read(t, "manifest_test_key.json"), &k); err != nil {
		t.Fatal(err)
	}
	seed, err := hex.DecodeString(k.SeedHex)
	if err != nil || len(seed) != ed25519.SeedSize || !k.TestOnly || !strings.Contains(k.Note, "TEST") {
		t.Fatal("klar markierter Testschlüssel erforderlich")
	}
	key := ed25519.NewKeyFromSeed(seed)
	if base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)) != k.PublicKey {
		t.Fatal("Testschlüssel passt nicht")
	}
	return key
}
func manifestFixture(t *testing.T) (signedManifest, ed25519.PrivateKey) {
	t.Helper()
	var w wire
	if err := strict(read(t, "manifest_response.json"), &w); err != nil {
		t.Fatal(err)
	}
	var v signedManifest
	if err := strict(w.Body, &v); err != nil {
		t.Fatal(err)
	}
	return v, testKey(t)
}
func signedForTest(t *testing.T, m deviceManifest, key ed25519.PrivateKey) signedManifest {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	b, err := canonical(raw)
	if err != nil {
		t.Fatal(err)
	}
	return signedManifest{raw, base64.StdEncoding.EncodeToString(ed25519.Sign(key, b))}
}
func TestSignedManifestGolden(t *testing.T) {
	v, key := manifestFixture(t)
	pub := key.Public().(ed25519.PublicKey)
	b, err := canonical(v.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, read(t, "manifest_canonical.bin")) {
		t.Fatal("kanonische Bytes")
	}
	var pair wire
	if err := strict(read(t, "pair_response.json"), &pair); err != nil {
		t.Fatal(err)
	}
	var p pairing
	if err := strict(pair.Body, &p); err != nil {
		t.Fatal(err)
	}
	if p.AnlagePubkey != base64.StdEncoding.EncodeToString(pub) {
		t.Fatal("TOFU-Schlüssel passt nicht zur Signatur")
	}
	var state manifestState
	now := time.Date(2026, 10, 23, 9, 59, 0, 0, time.UTC)
	if err := acceptManifest(&state, v, pub, p.DeviceID, now); err != nil {
		t.Fatal(err)
	}
	if state.Seq != 42 || !state.LastSync.Equal(now) {
		t.Fatal("Manifest nicht übernommen")
	}
	m, err := parseManifest(v.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	m.Seq = ^uint64(0)
	if err := acceptManifest(&state, signedForTest(t, m, key), pub, p.DeviceID, now); err != nil || state.Seq != ^uint64(0) {
		t.Fatal("uint64 ohne Rundung", err)
	}
}
func TestManifestRejectionsPreserveOld(t *testing.T) {
	v, key := manifestFixture(t)
	pub := key.Public().(ed25519.PublicKey)
	m, err := parseManifest(v.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 23, 9, 59, 0, 0, time.UTC)
	var original manifestState
	if err := acceptManifest(&original, v, pub, m.DeviceID, now); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"old_seq", "equal_seq", "foreign_device", "bad_signature", "changed_payload", "foreign_key"} {
		t.Run(name, func(t *testing.T) {
			candidate := m
			candidate.Seq++
			changed := signedForTest(t, candidate, key)
			switch name {
			case "old_seq":
				candidate.Seq = m.Seq - 1
				changed = signedForTest(t, candidate, key)
			case "equal_seq":
				candidate.Seq = m.Seq
				changed = signedForTest(t, candidate, key)
			case "foreign_device":
				candidate.DeviceID = "foreign-device"
				changed = signedForTest(t, candidate, key)
			case "bad_signature":
				sig, _ := base64.StdEncoding.DecodeString(changed.Sig)
				sig[0] ^= 1
				changed.Sig = base64.StdEncoding.EncodeToString(sig)
			case "changed_payload":
				changed.Manifest = bytes.ReplaceAll(changed.Manifest, []byte(`"grace_hours":24`), []byte(`"grace_hours":48`))
			case "foreign_key":
				changed = signedForTest(t, candidate, ed25519.NewKeyFromSeed(make([]byte, 32)))
			}
			state := original
			if err := acceptManifest(&state, changed, pub, m.DeviceID, now.Add(time.Hour)); err == nil || err.Error() != "manifest" {
				t.Fatal("verbotenes Manifest akzeptiert", err)
			}
			if state.Seq != original.Seq || !bytes.Equal(state.Bytes, original.Bytes) || !state.LastSync.Equal(original.LastSync) {
				t.Fatal("altes Manifest oder letzter Sync verändert")
			}
		})
	}
}
func TestCanonicalManifestRules(t *testing.T) {
	input := []byte(" {\"z\":18446744073709551615, \"ä\":\"<>&\", \"a\": [ {\"b\":1.0,\"a\":true},null,\"\\u00e4\"] } ")
	want := []byte(`{"a":[{"a":true,"b":1.0},null,"ä"],"z":18446744073709551615,"ä":"<>&"}`)
	got, err := canonical(input)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("Kanonisierung: %s %v", got, err)
	}
	for _, bad := range []string{`{"x":1,"x":2}`, `{"x":{"a":1,"a":2}}`, `{} {}`, `{"x":"\ud800"}`, `{"x":"\udc00"}`, string([]byte{'"', 0xff, '"'})} {
		if _, err := canonical([]byte(bad)); err == nil {
			t.Fatal("ungültige kanonische Eingabe akzeptiert")
		}
	}
}
func TestOfflineBoundaries(t *testing.T) {
	v, _ := manifestFixture(t)
	m, err := parseManifest(v.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)
	var offline, tools manifestPin
	for _, p := range m.Pins {
		if len(p.DeclaredTools) == 0 {
			offline = p
		} else {
			tools = p
		}
	}
	if offline.Pin == "" || tools.Pin == "" {
		t.Fatal("beide Offline-Fälle im Golden erforderlich")
	}
	if !offlineAllowed(offline, m, now.Add(-24*time.Hour+time.Nanosecond), now) {
		t.Fatal("innerhalb der Gnadenfrist")
	}
	if offlineAllowed(offline, m, now.Add(-24*time.Hour), now) || offlineAllowed(offline, m, now.Add(-25*time.Hour), now) || offlineAllowed(tools, m, now, now) || offlineAllowed(offline, m, time.Time{}, now) || offlineAllowed(offline, m, now.Add(time.Minute), now) {
		t.Fatal("verbotener Offline-Start akzeptiert")
	}
}
func TestDeltaForbiddenWire(t *testing.T) {
	var w wire
	if err := strict(read(t, "pair_request.json"), &w); err != nil {
		t.Fatal(err)
	}
	if err := checkWire("pair", read(t, "pair_request.json")); err != nil {
		t.Fatal("Positivkontrolle", err)
	}
	w.Headers["Authorization"] = "Bearer jnpt_example_<secret>"
	b, _ := json.Marshal(w)
	if checkWire("pair", b) == nil {
		t.Fatal("Bearer auf pair akzeptiert")
	}
	for _, bad := range []string{"ABCD-EFGH-JKMN", "0123456789abcdef.ABCD-EFGH-IJKL", "0123456789abcdef.ABCD-EFGH-OOOO", "0123456789abcdef.ABCD-EFGH-123", "0123456789abcdef.abcd-efgh-jkmn", "0123456789ABCDEF.ABCD-EFGH-JKMN", "0123456789abcde.ABCD-EFGH-JKMN"} {
		body, _ := json.Marshal(map[string]any{"contract": 1, "code": bad, "runner_version": "0.0.0"})
		if checkBody("pair", body) == nil {
			t.Fatal("ungültiger Kopplungscode akzeptiert")
		}
	}
	w = wire{}
	if err := strict(read(t, "runs_start_request.json"), &w); err != nil {
		t.Fatal(err)
	}
	if err := checkWire("runs_start", read(t, "runs_start_request.json")); err != nil {
		t.Fatal("Positivkontrolle", err)
	}
	for _, s := range []string{"", "0", "01", "-1", "+42", "18446744073709551616"} {
		w.Headers["X-Jnpt-Manifest-Seq"] = s
		b, _ := json.Marshal(w)
		if checkWire("runs_start", b) == nil {
			t.Fatal("ungültige Manifest-seq akzeptiert")
		}
	}
	w = wire{}
	if err := strict(read(t, "result_request.json"), &w); err != nil {
		t.Fatal(err)
	}
	if err := checkReport(w.Body); err != nil {
		t.Fatal("Positivkontrolle", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(w.Body, &obj); err != nil {
		t.Fatal(err)
	}
	obj["run_key"] = "demo/daily/2026-10-23T10:00:00Z"
	b, _ = json.Marshal(obj)
	if checkReport(b) == nil {
		t.Fatal("job_id und run_key gemeinsam akzeptiert")
	}
	delete(obj, "job_id")
	delete(obj, "run_key")
	b, _ = json.Marshal(obj)
	if checkReport(b) == nil {
		t.Fatal("Ergebnis ohne Identität akzeptiert")
	}
	if checkBody("error", []byte(`{"error":{"class":"revoked","message":"Text"}}`)) == nil {
		t.Fatal("revoked mit Text akzeptiert")
	}
	if checkBody("error", []byte(`{"error":{"class":"revoked","message":null}}`)) == nil {
		t.Fatal("revoked mit null-message akzeptiert")
	}
}

func TestManifestShapeRejections(t *testing.T) {
	v, key := manifestFixture(t)
	m, err := parseManifest(v.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	pub := key.Public().(ed25519.PublicKey)
	for _, hours := range []int{0, 73} {
		bad := m
		bad.GraceHours = hours
		state := manifestState{Seq: 1, Bytes: []byte("alter Stand")}
		if acceptManifest(&state, signedForTest(t, bad, key), pub, m.DeviceID, time.Now()) == nil {
			t.Fatal("grace_hours außerhalb 1–72 akzeptiert")
		}
		if state.Seq != 1 || string(state.Bytes) != "alter Stand" {
			t.Fatal("ungültiger Stand ersetzt altes Manifest")
		}
	}
	for _, tz := range []string{"", "Local", "Unknown/Zone"} {
		bad := m
		bad.Pins = append([]manifestPin(nil), m.Pins...)
		bad.Pins[0].Schedules = append([]manifestSchedule(nil), m.Pins[0].Schedules...)
		bad.Pins[0].Schedules[0].TZ = tz
		if _, err := parseManifest(signedForTest(t, bad, key).Manifest); err == nil {
			t.Fatal("unzulässige Zeitzone akzeptiert")
		}
	}
}

// Cross-Check der gelieferten Freigabe-/Ergebnisartefakte, kein Box-Adapter.
func reportBinding(m deviceManifest, r runReport, j job) bool {
	if r.ManifestSeq == nil || *r.ManifestSeq != m.Seq {
		return false
	}
	pin := ""
	if r.RunKey != "" {
		if !validRunKey(r.RunKey) {
			return false
		}
		pin = strings.Split(r.RunKey, "/")[0]
	} else {
		if r.JobID != j.JobID || r.Commit != j.Commit || *r.ManifestSeq != j.ManifestSeq {
			return false
		}
		pin = j.Pin
	}
	for _, p := range m.Pins {
		if p.Pin != pin || p.Commit != r.Commit {
			continue
		}
		if r.RunKey == "" {
			return true
		}
		schedule := strings.Split(r.RunKey, "/")[1]
		for _, s := range p.Schedules {
			if s.Name == schedule {
				return true
			}
		}
	}
	return false
}
func TestReportManifestBindings(t *testing.T) {
	v, _ := manifestFixture(t)
	m, err := parseManifest(v.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	var w wire
	if err := strict(read(t, "claim_response.json"), &w); err != nil {
		t.Fatal(err)
	}
	var claimed struct {
		Job job `json:"job"`
	}
	if err := strict(w.Body, &claimed); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"result_request.json", "scheduled_result_request.json", "offline_result_request.json", "refused_offline_result_request.json"} {
		w = wire{}
		if err := strict(read(t, name), &w); err != nil {
			t.Fatal(err)
		}
		var r runReport
		if err := strict(w.Body, &r); err != nil {
			t.Fatal(err)
		}
		if !reportBinding(m, r, claimed.Job) {
			t.Fatal("Freigabe passt nicht", name)
		}
		bad := r
		bad.Commit = strings.Repeat("0", 40)
		if reportBinding(m, bad, claimed.Job) {
			t.Fatal("nicht freigegebenen Commit akzeptiert")
		}
		seq := m.Seq + 1
		bad = r
		bad.ManifestSeq = &seq
		if reportBinding(m, bad, claimed.Job) {
			t.Fatal("falsche Manifest-seq akzeptiert")
		}
		if r.RunKey != "" {
			bad = r
			bad.RunKey = "foreign/daily/2026-10-23T10:00:00Z"
			if reportBinding(m, bad, claimed.Job) {
				t.Fatal("fremden Pin akzeptiert")
			}
		}
	}
	// Stand 42 bleibt für späte Ergebnisse maßgeblich, auch wenn 43 aktuell ist.
	w = wire{}
	if err := strict(read(t, "offline_result_request.json"), &w); err != nil {
		t.Fatal(err)
	}
	var r runReport
	if err := strict(w.Body, &r); err != nil {
		t.Fatal(err)
	}
	current := m
	current.Seq++
	if reportBinding(current, r, claimed.Job) {
		t.Fatal("spätes Ergebnis dem aktuellen Stand zugeordnet")
	}
	if !reportBinding(m, r, claimed.Job) {
		t.Fatal("damaliger Stand nicht verwendbar")
	}
}
