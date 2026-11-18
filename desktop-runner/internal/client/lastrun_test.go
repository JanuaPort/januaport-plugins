package client

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	panel "github.com/januaport/januaport-plugins/desktop-runner/internal/pipe"
)

func TestLetzterLaufAusLokalemStand(t *testing.T) {
	at := time.Date(2026, 11, 17, 13, 5, 0, 0, time.UTC)
	for _, tc := range []struct {
		name                        string
		rec                         *lastRun
		prepared                    bool
		zuletzt, ergebnis, codeWant string
	}{
		{"nie gelaufen", nil, true, "", "nie", ""},
		{"ok", &lastRun{At: at, End: "ok"}, true, "2026-11-17T13:05:00Z", "ok", ""},
		{"Fehler error", &lastRun{At: at, End: "error"}, true, "2026-11-17T13:05:00Z", "fehler", "error"},
		{"Fehler timeout", &lastRun{At: at, End: "timeout"}, true, "2026-11-17T13:05:00Z", "fehler", "timeout"},
		{"Fehler limit", &lastRun{At: at, End: "limit"}, true, "2026-11-17T13:05:00Z", "fehler", "limit"},
		{"Fehler cancelled", &lastRun{At: at, End: "cancelled"}, true, "2026-11-17T13:05:00Z", "fehler", "cancelled"},
		{"Fehler internal", &lastRun{At: at, End: "internal"}, true, "2026-11-17T13:05:00Z", "fehler", "internal"},
		{"Fehler refused/runtime", &lastRun{At: at, End: "refused", Detail: "runtime"}, true, "2026-11-17T13:05:00Z", "fehler", "refused/runtime"},
		{"Fehler refused/idle", &lastRun{At: at, End: "refused", Detail: "idle"}, true, "2026-11-17T13:05:00Z", "fehler", "refused/idle"},
		{"refused mit fremdem Detail", &lastRun{At: at, End: "refused", Detail: `C:\Users\jemand\x`}, true, "2026-11-17T13:05:00Z", "fehler", "refused"},
		{"fremdes Ende wird intern", &lastRun{At: at, End: "Passwort geändert"}, true, "2026-11-17T13:05:00Z", "fehler", "internal"},
		{"Detail nur bei refused", &lastRun{At: at, End: "error", Detail: "runtime"}, true, "2026-11-17T13:05:00Z", "fehler", "error"},
		{"nicht bereit ohne Lauf", nil, false, "", "nicht_bereit", ""},
		{"nicht bereit schlägt alten Lauf", &lastRun{At: at, End: "ok"}, false, "", "nicht_bereit", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			z, e, c := describeLastRun(tc.rec, tc.prepared)
			if z != tc.zuletzt || e != tc.ergebnis || c != tc.codeWant {
				t.Fatalf("%q %q %q", z, e, c)
			}
		})
	}
}

func TestLetzterLaufMerkenNimmtNurDenLetztenAuf(t *testing.T) {
	var l lastRuns
	if l.get("demo", "raum") != nil {
		t.Fatal("leerer Stand")
	}
	l.note("demo", "raum", Result{End: "error", FinishedAt: "2026-11-17T10:00:00Z"})
	l.note("demo", "raum", Result{End: "ok", FinishedAt: "2026-11-17T14:05:00+01:00"})
	l.note("demo", "anderes", Result{End: "timeout", FinishedAt: "2026-11-17T09:00:00Z"})
	got := l.get("demo", "raum")
	if got == nil || got.End != "ok" || !got.At.Equal(time.Date(2026, 11, 17, 13, 5, 0, 0, time.UTC)) || got.At.Location() != time.UTC {
		t.Fatalf("%+v", got)
	}
	if other := l.get("demo", "anderes"); other == nil || other.End != "timeout" {
		t.Fatalf("%+v", other)
	}
	if l.get("fremd", "raum") != nil {
		t.Fatal("Pin getrennt")
	}
	// Ein unlesbarer Zeitpunkt macht aus dem Lauf keinen Nicht-Lauf.
	l.note("demo", "kaputt", Result{End: "ok", FinishedAt: "gestern"})
	if k := l.get("demo", "kaputt"); k == nil || k.At.IsZero() {
		t.Fatalf("%+v", k)
	}
}

func rowOf(t *testing.T, m panel.Message, pin, setup string) panel.Setup {
	t.Helper()
	for _, s := range m.Setups {
		if s.Pin == pin && s.Setup == setup {
			return s
		}
	}
	t.Fatal("Zeile fehlt", pin, setup)
	return panel.Setup{}
}

func TestLetzterLaufUeberDiePipe(t *testing.T) {
	b := pipeBed(t)
	b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Nonce string `json:"nonce"`
		}
		json.NewDecoder(r.Body).Decode(&v)
		json.NewEncoder(w).Encode(SetupStartReply{RunKey: "setup/demo/raum/" + v.Nonce, Input: json.RawMessage(`{}`), RunToken: "synthetic", ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339), MCPPath: "/api/device/v1/mcp"})
	})
	if row := rowOf(t, panelRaw(t, b.engine, `{"type":"status"}`), "demo", "raum"); row.Ergebnis != "nie" || row.Zuletzt != "" || row.Code != "" {
		t.Fatalf("vor dem ersten Lauf: %+v", row)
	}
	before := time.Now().UTC().Add(-2 * time.Second)
	if got := panelRaw(t, b.engine, `{"type":"setup_start","pin":"demo","setup":"raum"}`); got.Type != "zustand" {
		t.Fatal(got)
	}
	b.engine.WaitPanelRuns()
	row := rowOf(t, panelRaw(t, b.engine, `{"type":"status"}`), "demo", "raum")
	at, err := time.Parse(time.RFC3339, row.Zuletzt)
	if row.Ergebnis != "ok" || row.Code != "" || err != nil || at.Before(before) || at.After(time.Now().Add(2*time.Second)) {
		t.Fatalf("nach dem Lauf: %+v", row)
	}
	// Ein Pin ohne Umgebung ist nicht vorbereitet (#1391), auch wenn er schon einmal lief.
	b.engine.mu.Lock()
	b.engine.bundle.Environments = map[string]Environment{}
	b.engine.mu.Unlock()
	if row = rowOf(t, panelRaw(t, b.engine, `{"type":"status"}`), "demo", "raum"); row.Ergebnis != "nicht_bereit" || row.Zuletzt != "" || row.Startbar {
		t.Fatalf("nicht vorbereitet: %+v", row)
	}
}

func TestLetzterLaufFehlerUeberDiePipe(t *testing.T) {
	b := pipeBed(t)
	b.spawnErr = ErrRuntime
	b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Nonce string `json:"nonce"`
		}
		json.NewDecoder(r.Body).Decode(&v)
		json.NewEncoder(w).Encode(SetupStartReply{RunKey: "setup/demo/raum/" + v.Nonce, Input: json.RawMessage(`{}`), RunToken: "synthetic", ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339), MCPPath: "/api/device/v1/mcp"})
	})
	panelRaw(t, b.engine, `{"type":"setup_start","pin":"demo","setup":"raum"}`)
	b.engine.WaitPanelRuns()
	row := rowOf(t, panelRaw(t, b.engine, `{"type":"status"}`), "demo", "raum")
	if row.Ergebnis != "fehler" || row.Code == "" || row.Zuletzt == "" {
		t.Fatalf("%+v", row)
	}
}

// Nur Zeitpunkt und Status verlassen den Läufer: weder Log noch Ergebnis noch Detailtext, noch Eingabe.
func TestLetzterLaufFremdeFelderGelangenNichtInDiePipe(t *testing.T) {
	b := pipeBed(t)
	b.engine.lastRuns.note("demo", "raum", Result{
		End:        "refused",
		Detail:     "GEHEIMES-DETAIL",
		Log:        "GEHEIMES-LOG",
		Result:     json.RawMessage(`{"ausgabe":"GEHEIME-AUSGABE"}`),
		RunKey:     "GEHEIMER-LAUFSCHLUESSEL",
		JobID:      "GEHEIME-JOB-ID",
		Commit:     "GEHEIMER-COMMIT",
		FinishedAt: "2026-11-17T13:05:00Z",
	})
	raw, err := json.Marshal(panelRaw(t, b.engine, `{"type":"status"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"GEHEIM", "ausgabe"} {
		if strings.Contains(string(raw), bad) {
			t.Fatal(bad, string(raw))
		}
	}
	var frame struct {
		Setups []map[string]json.RawMessage `json:"setups"`
	}
	if json.Unmarshal(raw, &frame) != nil || len(frame.Setups) != 1 {
		t.Fatal(string(raw))
	}
	for k := range frame.Setups[0] {
		switch k {
		case "pin", "setup", "label", "startbar", "braucht_eingabe", "zuletzt", "ergebnis", "code":
		default:
			t.Fatal("fremdes Feld in der Zeile", k)
		}
	}
	if row := rowOf(t, panelRaw(t, b.engine, `{"type":"status"}`), "demo", "raum"); row.Code != "refused" || row.Ergebnis != "fehler" {
		t.Fatalf("%+v", row)
	}
}

// Die Anzeige gilt „seit Start“: Der Stand liegt nur im Arbeitsspeicher. Er schreibt nichts in den State
// (kein neues Persistenzformat), und ein neu gestarteter Läufer kennt den Lauf des alten nicht.
func TestLetzterLaufUeberlebtKeinenNeustart(t *testing.T) {
	b := pipeBed(t)
	namen := func() string {
		entries, err := os.ReadDir(b.st.Root)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range entries {
			info, _ := e.Info()
			out = append(out, e.Name()+"/"+info.ModTime().String())
		}
		return strings.Join(out, ",")
	}
	vorher, journal := namen(), string(encode(b.st.Journal()))
	b.engine.lastRuns.note("demo", "raum", Result{End: "ok", FinishedAt: "2026-11-17T13:05:00Z"})
	if rowOf(t, panelRaw(t, b.engine, `{"type":"status"}`), "demo", "raum").Ergebnis != "ok" {
		t.Fatal("Vorbedingung")
	}
	if namen() != vorher || string(encode(b.st.Journal())) != journal {
		t.Fatal("Der letzte Lauf darf nichts im State ablegen")
	}
	neu := pipeBed(t)
	if row := rowOf(t, panelRaw(t, neu.engine, `{"type":"status"}`), "demo", "raum"); row.Ergebnis != "nie" || row.Zuletzt != "" {
		t.Fatalf("%+v", row)
	}
}
