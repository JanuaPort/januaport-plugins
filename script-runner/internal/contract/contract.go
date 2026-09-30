// Package contract ist der Draht-Vertrag Gateway ↔ Skript-Läufer
// (JanuaPort/januaport#928, Vertrag Fassung 1 + 2 + Nachtrag 2a): Namen,
// Ende-Klassen, feste Sätze und die Form von tools/list und tools/call.
// Die Golden-Dateien unter contract/golden/ sind die Referenz; eine Änderung
// hier ist eine Vertragsänderung und braucht eine neue Fassung im Ticket.
package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Version ist `contract` in `_meta["januaport.ai/runner"]`.
const Version = 1

// Schlüssel in `_meta`.
const (
	MetaRun    = "januaport.ai/run"
	MetaScript = "januaport.ai/script"
	MetaRunner = "januaport.ai/runner"
)

// Header, die das Gateway bei tools/call setzt (Vertrag §3).
const (
	HeaderRunToken  = "X-Jnpt-Run-Token"
	HeaderRunID     = "X-Jnpt-Run-Id"
	HeaderPinCommit = "X-Jnpt-Pin-Commit"
)

// Ende-Klassen (Vertrag §4).
const (
	EndOK       = "ok"
	EndError    = "error"
	EndLimit    = "limit"
	EndTimeout  = "timeout"
	EndBusy     = "busy"
	EndRefused  = "refused"
	EndInternal = "internal"
)

// detail-Codes (Vertrag §4).
const (
	DetailExit        = "exit"
	DetailException   = "exception"
	DetailOutput      = "output"
	DetailFrame       = "frame"
	DetailToolCalls   = "tool_calls"
	DetailMemory      = "memory"
	DetailSandboxLost = "sandbox_lost"
	DetailPin         = "pin"
	DetailCommit      = "commit"
	DetailState       = "state"
	DetailInput       = "input"
)

// Isolation der Sandbox (Vertrag §2, §5). `invalid` heißt: keine Sandbox
// mit Seccomp-Modus 2 gemeldet, der Läufer bedient nicht.
const (
	IsolationGVisor   = "gvisor"
	IsolationStandard = "standard"
	IsolationInvalid  = "invalid"
)

// Zustände und Gründe eines Pins (Vertrag §2).
const (
	StatePending = "pending"
	StateReady   = "ready"
	StateInvalid = "invalid"

	ReasonFetchFailed      = "fetch_failed"
	ReasonHashMismatch     = "hash_mismatch"
	ReasonSymlink          = "symlink"
	ReasonGitlink          = "gitlink"
	ReasonBadPath          = "bad_path"
	ReasonManifestMissing  = "manifest_missing"
	ReasonManifestInvalid  = "manifest_invalid"
	ReasonToolNotQualified = "tool_not_qualified"
)

// Grenzen v1, soweit der vermittler sie durchsetzt.
const (
	MaxInputBytes  = 256 << 10
	MaxOutputBytes = 1 << 20
	MaxToolCalls   = 50
)

// Run ist das Ergebnis eines Laufs, wie es in `_meta["januaport.ai/run"]`
// steht.
type Run struct {
	End       string `json:"end"`
	Detail    string `json:"detail,omitempty"`
	ErrorType string `json:"error_type,omitempty"`
	ErrorAt   string `json:"error_at,omitempty"`
	BytesOut  int    `json:"bytes_out"`
	ToolCalls int    `json:"tool_calls"`
	Isolation string `json:"isolation"`
}

var messages = map[string]string{
	EndError + "/" + DetailExit:        "Das Skript hat mit einem Fehlercode geendet.",
	EndLimit + "/" + DetailOutput:      "Die Ausgabe des Skripts überschreitet die Grenze von 1 MiB.",
	EndLimit + "/" + DetailFrame:       "Das Skript hat eine zu große Nachricht gesendet.",
	EndLimit + "/" + DetailToolCalls:   "Das Skript hat mehr als 50 Werkzeugaufrufe versucht.",
	EndLimit + "/" + DetailMemory:      "Das Skript hat die Speichergrenze überschritten.",
	EndLimit + "/" + DetailSandboxLost: "Die Sandbox ist während des Laufs abgebrochen, zum Beispiel wegen zu vieler Prozesse.",
	EndTimeout + "/":                   "Das Skript hat die Wandzeit überschritten und wurde beendet.",
	EndBusy + "/":                      "Der Skript-Läufer ist gerade belegt. In etwa 10 Sekunden erneut versuchen.",
	EndRefused + "/" + DetailPin:       "Dieses Skript ist nicht gepinnt.",
	EndRefused + "/" + DetailCommit:    "Der gepinnte Stand passt nicht zum Stand im Skript-Läufer. Später erneut versuchen.",
	EndRefused + "/" + DetailState:     "Dieses Skript ist nicht ausführbereit.",
	EndRefused + "/" + DetailInput:     "Die Eingabe überschreitet die Grenze von 256 KiB.",
	EndInternal + "/":                  "Interner Fehler im Skript-Läufer.",
}

// Message ist der feste Satz an die KI. Nie ein Traceback, nie stderr (M6).
func Message(r Run) string {
	if r.End == EndError && r.Detail == DetailException {
		if r.ErrorAt != "" {
			return fmt.Sprintf("Das Skript ist mit einer Ausnahme abgebrochen: %s in %s.", r.ErrorType, r.ErrorAt)
		}
		return fmt.Sprintf("Das Skript ist mit einer Ausnahme abgebrochen: %s.", r.ErrorType)
	}
	if m, ok := messages[r.End+"/"+r.Detail]; ok {
		return m
	}
	return messages[EndInternal+"/"]
}

// Result baut das CallToolResult (Vertrag §4). Die Ausgabe des Skripts
// erscheint nur bei `ok`.
func Result(r Run, output []byte) *mcp.CallToolResult {
	res := &mcp.CallToolResult{Meta: mcp.Meta{MetaRun: r}}
	if r.End != EndOK {
		res.IsError = true
		res.Content = []mcp.Content{&mcp.TextContent{Text: Message(r)}}
		return res
	}
	text := strings.ToValidUTF8(string(output), "�")
	res.Content = []mcp.Content{&mcp.TextContent{Text: text}}
	var obj map[string]any
	if json.Unmarshal(output, &obj) == nil && obj != nil {
		res.StructuredContent = obj
	}
	return res
}

var errorTypeRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]{0,100}$`)

// SanitizeErrorType lässt nur einen Klassennamen durch (Vertrag §4, M6).
func SanitizeErrorType(s string) string {
	if errorTypeRe.MatchString(s) {
		return s
	}
	return "Exception"
}

// ScriptMeta ist `_meta["januaport.ai/script"]` je Werkzeug.
type ScriptMeta struct {
	Commit        string   `json:"commit"`
	Path          string   `json:"path"`
	DeclaredTools []string `json:"declared_tools"`
	TimeoutS      int      `json:"timeout_s"`
	ManifestSHA   string   `json:"manifest_sha"`
}

// PinStatus ist ein Eintrag in `_meta["januaport.ai/runner"].pins`.
type PinStatus struct {
	Name   string `json:"name"`
	Commit string `json:"commit"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

// RunnerMeta ist `_meta["januaport.ai/runner"]` auf dem tools/list-Ergebnis.
type RunnerMeta struct {
	Contract  int         `json:"contract"`
	Isolation string      `json:"isolation"`
	Pins      []PinStatus `json:"pins"`
}

// ListedPin ist ein Pin, wie ihn tools/list braucht. Script, Description
// und InputSchema sind nur bei `ready` gesetzt.
type ListedPin struct {
	Name        string
	Commit      string
	State       string
	Reason      string
	Script      *ScriptMeta
	Description string
	InputSchema map[string]any
}

// ToolsList baut das tools/list-Ergebnis (Vertrag §2). Werkzeuge gibt es
// nur für Pins im Zustand `ready` und nie bei isolation `invalid`.
func ToolsList(pins []ListedPin, isolation string) *mcp.ListToolsResult {
	meta := RunnerMeta{Contract: Version, Isolation: isolation, Pins: []PinStatus{}}
	tools := []*mcp.Tool{}
	for _, p := range pins {
		state := p.State
		if state == StateReady && isolation == IsolationInvalid {
			// Vertrag §5: ohne Sandbox mit Seccomp 2 ist kein Pin ready.
			state = StatePending
		}
		meta.Pins = append(meta.Pins, PinStatus{Name: p.Name, Commit: p.Commit, State: state, Reason: p.Reason})
		if state != StateReady || p.Script == nil {
			continue
		}
		sm := *p.Script
		sm.Commit = p.Commit
		if sm.DeclaredTools == nil {
			sm.DeclaredTools = []string{}
		}
		tools = append(tools, &mcp.Tool{
			Meta:        mcp.Meta{MetaScript: sm},
			Name:        p.Name,
			Description: p.Description,
			InputSchema: p.InputSchema,
		})
	}
	// Die Werkzeugliste hängt an den Pins; kein Zwischenspeicher über den
	// Abgleich des Gateways hinaus.
	return &mcp.ListToolsResult{
		Meta:      mcp.Meta{MetaRunner: meta},
		Cacheable: mcp.Cacheable{TTLMs: 0, CacheScope: "private"},
		Tools:     tools,
	}
}

// RunHeaders sind die geprüften Header eines tools/call.
type RunHeaders struct {
	RunToken  string
	RunID     string
	PinCommit string
}

var (
	runTokenRe = regexp.MustCompile(`^jnptrun_[A-Za-z0-9_-]{43}$`)
	uuidV4Re   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

// ParseRunHeaders prüft Lauf-Token und Lauf-ID. Fehlen sie oder sind sie
// falsch geformt, ist das ein Protokollverstoß (JSON-RPC-Fehler). Der
// Pin-Commit wird hier nur gelesen; ob er passt, entscheidet der Aufrufer
// (refused/commit).
func ParseRunHeaders(h http.Header) (RunHeaders, error) {
	rh := RunHeaders{RunToken: h.Get(HeaderRunToken), RunID: h.Get(HeaderRunID), PinCommit: h.Get(HeaderPinCommit)}
	if !runTokenRe.MatchString(rh.RunToken) {
		return RunHeaders{}, errors.New("contract: Lauf-Token fehlt oder ist falsch geformt")
	}
	if !uuidV4Re.MatchString(rh.RunID) {
		return RunHeaders{}, errors.New("contract: Lauf-ID ist keine UUIDv4")
	}
	if !utf8.ValidString(rh.PinCommit) {
		rh.PinCommit = ""
	}
	return rh, nil
}
