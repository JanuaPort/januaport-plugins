package client

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// JanuaPort/januaport#1354: Ein Setup, das der Mensch im Bedienfeld startet, ist von der
// Leerlaufbedingung ausgenommen. Konto, Sitzung, Halt und Pause bleiben.

func setupIdleBed(t *testing.T, s Session) *bed {
	t.Helper()
	b := pipeBed(t)
	b.engine.Config.StartPolicy.Mode = "idle"
	b.engine.Config.StartPolicy.IdleMinutes = 10
	b.engine.bundle.Config = b.engine.Config
	b.session = s
	return b
}

func setupAPI(t *testing.T, b *bed, hits *atomic.Int32) {
	t.Helper()
	b.engine.API = l1API(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		var v map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&v)
		var nonce string
		json.Unmarshal(v["nonce"], &nonce)
		json.NewEncoder(w).Encode(SetupStartReply{RunKey: "setup/demo/raum/" + nonce, Input: json.RawMessage(`{}`), RunToken: "synthetic", ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339), MCPPath: "/api/device/v1/mcp"})
	})
}

func TestSetupAusBedienfeldStartetOhneLeerlauf(t *testing.T) {
	for name, idle := range map[string]time.Duration{"Leerlauf null": 0, "Leerlauf zu kurz": 9 * time.Minute, "Leerlauf erreicht": 10 * time.Minute} {
		t.Run(name, func(t *testing.T) {
			b := setupIdleBed(t, Session{Active: true, Account: "own", Idle: idle})
			var hits atomic.Int32
			setupAPI(t, b, &hits)
			if st := panelRaw(t, b.engine, `{"type":"status"}`); len(st.Setups) != 1 || !st.Setups[0].Startbar {
				t.Fatalf("Setup im Leerlaufmodus nicht startbar: %+v", st)
			}
			if got := panelRaw(t, b.engine, `{"type":"setup_start","pin":"demo","setup":"raum"}`); got.Type != "zustand" {
				t.Fatal(got)
			}
			b.engine.WaitPanelRuns()
			j := b.st.Journal()
			if hits.Load() != 1 || len(b.launches) != 1 || len(j.Results) != 1 || j.Results[0].End != "ok" {
				t.Fatal(hits.Load(), len(b.launches), j)
			}
		})
	}
}

func TestSetupAusBedienfeldBleibtAnKontoUndSitzungGebunden(t *testing.T) {
	for name, s := range map[string]Session{
		"falsches Konto":      {Active: true, Account: "fremd"},
		"Sitzung inaktiv":     {Active: false, Account: "own", Idle: time.Hour},
		"kein Konto":          {Active: true},
		"inaktiv, kein Konto": {},
	} {
		t.Run(name, func(t *testing.T) {
			b := setupIdleBed(t, s)
			var hits atomic.Int32
			setupAPI(t, b, &hits)
			if st := panelRaw(t, b.engine, `{"type":"status"}`); len(st.Setups) != 1 || st.Setups[0].Startbar {
				t.Fatalf("Setup trotz fehlendem Konto/Sitzung startbar: %+v", st)
			}
			if got := panelRaw(t, b.engine, `{"type":"setup_start","pin":"demo","setup":"raum"}`); got.Type != "fehler" {
				t.Fatal(got)
			}
			b.engine.WaitPanelRuns()
			if hits.Load() != 0 || len(b.launches) != 0 || len(b.st.Journal().Results) != 0 {
				t.Fatal("Start trotz fehlendem Konto/Sitzung", hits.Load())
			}
		})
	}
}

// Wechselt das Konto zwischen Zulassung und Spawn, lautet die Ablehnung account, nie idle.
func TestSetupKontoWechselNachZulassungWirdAccountNichtIdle(t *testing.T) {
	b := setupIdleBed(t, Session{Active: true, Account: "own"})
	var hits atomic.Int32
	setupAPI(t, b, &hits)
	var calls atomic.Int32
	b.engine.Session = func() (Session, error) {
		if calls.Add(1) == 1 {
			return Session{Active: true, Account: "own"}, nil
		}
		return Session{Active: true, Account: "fremd"}, nil
	}
	if got := panelRaw(t, b.engine, `{"type":"setup_start","pin":"demo","setup":"raum"}`); got.Type != "zustand" {
		t.Fatal(got)
	}
	b.engine.WaitPanelRuns()
	j := b.st.Journal()
	if len(b.launches) != 0 || len(j.Results) != 1 || j.Results[0].End != "refused" || j.Results[0].Detail != "account" {
		t.Fatal(len(b.launches), j)
	}
}

func TestSetupAusBedienfeldHaltUndPauseSperrenWeiter(t *testing.T) {
	for _, state := range []string{"angehalten", "pausiert"} {
		t.Run(state, func(t *testing.T) {
			b := setupIdleBed(t, Session{Active: true, Account: "own"})
			var hits atomic.Int32
			setupAPI(t, b, &hits)
			mustHalt(t, b.engine, state, time.Time{})
			if got := panelRaw(t, b.engine, `{"type":"setup_start","pin":"demo","setup":"raum"}`); got.Type != "fehler" {
				t.Fatal(got)
			}
			b.engine.WaitPanelRuns()
			if hits.Load() != 0 || len(b.launches) != 0 {
				t.Fatal("Start trotz Halt/Pause")
			}
		})
	}
}

// Zeitplan und Ad hoc laufen über Execute ohne Bedienfeld-Merkmal und bleiben unter idle.
func TestZeitplanUndAdHocBleibenUnterLeerlauf(t *testing.T) {
	for name, s := range map[string]Session{
		"Leerlauf null":    {Active: true, Account: "own"},
		"Leerlauf zu kurz": {Active: true, Account: "own", Idle: 9 * time.Minute},
	} {
		t.Run(name, func(t *testing.T) {
			b := setupIdleBed(t, s)
			for _, key := range []string{"demo/daily/2026-10-23T10:00:00Z", ""} {
				b.result.RunKey, b.result.JobID = key, ""
				if key == "" {
					b.result.JobID = "11111111-1111-4111-8111-111111111111"
				}
				if got := b.execute(); got.End != "refused" || got.Detail != "idle" {
					t.Fatalf("%q: %+v", key, got)
				}
			}
			if len(b.launches) != 0 {
				t.Fatal("Spawn trotz Leerlauf")
			}
			if st := panelRaw(t, b.engine, `{"type":"status"}`); len(st.Setups) != 1 || !st.Setups[0].Startbar {
				t.Fatal("Bedienfeld-Setup sollte trotzdem startbar sein", st)
			}
		})
	}
}

// Wächter: Das Bedienfeld-Merkmal (panelPermitKey im Kontext) setzt allein startPanelSetup in pipe.go;
// die Prüfung ohne Leerlauf nutzen allein die Pipe-Zulassung und executeReserved.
func TestWaechterNurPipeWegSetztBedienfeldMerkmal(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, 0)
	if err != nil {
		t.Fatal(err)
	}
	setters := map[string]int{}
	localUse := map[string]int{}
	for _, pkg := range pkgs {
		for fname, f := range pkg.Files {
			for _, d := range f.Decls {
				fn, ok := d.(*ast.FuncDecl)
				if !ok {
					continue
				}
				ast.Inspect(fn, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					switch fun := call.Fun.(type) {
					case *ast.SelectorExpr:
						if fun.Sel.Name == "WithValue" && len(call.Args) == 3 {
							if lit, ok := call.Args[1].(*ast.CompositeLit); ok {
								if id, ok := lit.Type.(*ast.Ident); ok && id.Name == "panelPermitKey" {
									setters[fname+":"+fn.Name.Name]++
								}
							}
						}
					case *ast.Ident:
						if fun.Name == "localStartRefusal" {
							localUse[fname+":"+fn.Name.Name]++
						}
					}
					return true
				})
			}
		}
	}
	if len(setters) != 1 || setters["pipe.go:startPanelSetup"] != 1 {
		t.Fatalf("Bedienfeld-Merkmal wird außerhalb von startPanelSetup gesetzt: %v", setters)
	}
	want := map[string]bool{"pipe.go:startPanelSetup": true, "pipe.go:panelStatus": true, "engine.go:executeReserved": true}
	for k := range localUse {
		if !want[k] {
			t.Fatalf("localStartRefusal an unerwarteter Stelle: %s", k)
		}
	}
	for k := range want {
		if localUse[k] == 0 {
			t.Fatalf("localStartRefusal fehlt in %s (gefunden: %v)", k, localUse)
		}
	}
}

// SEC A1: Das Merkmal lebt nur im Arbeitsspeicher. Kein Zustands-, Journal- oder Drahttyp trägt es,
// und panelPermit hat weder exportierte Felder noch Tags.
func TestWaechterBedienfeldMerkmalNieSerialisierbar(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, 0)
	if err != nil {
		t.Fatal(err)
	}
	mentions := func(n ast.Node) bool {
		found := false
		ast.Inspect(n, func(x ast.Node) bool {
			if id, ok := x.(*ast.Ident); ok && (id.Name == "panelPermit" || id.Name == "panelPermitKey") {
				found = true
			}
			return !found
		})
		return found
	}
	seen := false
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			ast.Inspect(f, func(n ast.Node) bool {
				ts, ok := n.(*ast.TypeSpec)
				if !ok {
					return true
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					return true
				}
				if ts.Name.Name == "panelPermit" {
					seen = true
				}
				for _, fld := range st.Fields.List {
					if ts.Name.Name == "panelPermit" {
						if fld.Tag != nil {
							t.Errorf("panelPermit trägt ein Tag: %s", fld.Tag.Value)
						}
						for _, name := range fld.Names {
							if name.IsExported() {
								t.Errorf("panelPermit hat ein exportiertes Feld: %s", name.Name)
							}
						}
						continue
					}
					if mentions(fld.Type) {
						t.Errorf("Typ %s trägt das Bedienfeld-Merkmal als Feld", ts.Name.Name)
					}
				}
				return true
			})
		}
	}
	if !seen {
		t.Fatal("panelPermit nicht gefunden")
	}
}

// SEC A1: Nach einem Neustart gibt es kein Merkmal mehr. Ein Setup-Schlüssel, der ohne
// Bedienfeld-Kontext ankommt (Reservierung, Claim), läuft wieder unter startRefusal.
func TestNeustartNachSetupGiltWiederIdle(t *testing.T) {
	b := setupIdleBed(t, Session{Active: true, Account: "own"})
	var hits atomic.Int32
	setupAPI(t, b, &hits)
	panelRaw(t, b.engine, `{"type":"setup_start","pin":"demo","setup":"raum"}`)
	b.engine.WaitPanelRuns()
	if len(b.launches) != 1 {
		t.Fatal("Vorbedingung: Bedienfeld-Lauf startet", len(b.launches))
	}
	st, err := OpenState(b.st.Root, testCipher{})
	if err != nil {
		t.Fatal(err)
	}
	restarted := &Engine{State: st, Config: b.engine.Config, Session: func() (Session, error) { return b.session, nil }, Spawn: b.spawn, ChildEnv: b.engine.ChildEnv}
	r := Result{RunKey: "setup/demo/raum/NeuerLaufNachNeustart", ManifestSeq: 1, Commit: b.env.Origin.Commit}
	got := restarted.Execute(context.Background(), r, b.env, json.RawMessage(`{}`), "", 10, 0)
	if got.End != "refused" || got.Detail != "idle" || len(b.launches) != 1 {
		t.Fatalf("nach Neustart: %+v (Starts %d)", got, len(b.launches))
	}
}
