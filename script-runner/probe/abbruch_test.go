//go:build docker

// SEC L1 (JanuaPort/januaport#1040): Abbruch heißt Platz frei. Das Gateway
// bricht einen laufenden Lauf ab, indem es den Relay-Aufruf an den vermittler
// abbricht (Entzug beim Aufrufer). Gemessen am echten Stack: wann der
// vermittler den Lauf beendet, wann eine neue Instanz den Platz frei meldet und
// wann ein zweiter Lauf startet und durchläuft.
package probe

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Obere Schranken, begründet aus der Guard-Ordnung (guard.finish): Nach dem
// Abbruch tötet der Wächter alle Prozesse und hält die Instanz bis
// MinLifetime = 10,5 s ab ihrem Start. Die Instanz war vor dem Lauf schon
// registriert, also endet sie spätestens 10,5 s nach dem Abbruch. Danach
// startet Docker den Container neu (restart: always, ohne Backoff, weil er
// länger als 10 s lief), Wächter und Handshake brauchen gemessen 1–3 s (B1:
// Läufe in Folge ≤ ~12 s). Mit Rand:
//   - Platz frei (neue Instanz registriert) ≤ 18 s nach dem Abbruch,
//   - zweiter Lauf fertig ≤ 22 s nach dem Abbruch (Platz frei + ein kurzer
//     Lauf, Wiederholung bei busy alle 0,5 s).
const (
	l1PlatzFrei     = 18 * time.Second
	l1ZweiterLauf   = 22 * time.Second
	l1LaufBeendet   = 3 * time.Second
	l1WartenAufLauf = 60 * time.Second
)

func TestL1AbbruchGibtPlatzFrei(t *testing.T) {
	for _, tc := range []struct {
		mode, inner string
		// pause: Zeit nach dem inneren Aufruf bis zum Abbruch — beim Schlafen
		// liegt das Skript dann in time.sleep, beim Hängen wartet der innere
		// Aufruf noch auf das Gateway.
		pause time.Duration
	}{
		{"schlafen", "kartei_find", time.Second},
		{"haengen", "kartei_hang", 500 * time.Millisecond},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			l1Messen(t, tc.mode, tc.inner, tc.pause)
		})
	}
}

func l1Messen(t *testing.T, mode, inner string, pause time.Duration) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ended := l1StartenBisInnererAufruf(t, ctx, mode, inner)
	time.Sleep(pause)

	regsBefore := len(instanceRegistrations(t))
	runsBefore := strings.Count(must(t, "docker", "logs", project+"-script-runner-1"), "msg=run ")
	restartsBefore := sandboxRestarts(t)
	t0 := time.Now()
	cancel() // das Gateway bricht den Relay ab
	<-ended

	second := make(chan time.Duration, 1)
	secondEnd := make(chan string, 1)
	go func() {
		end := quickNachAbbruch()
		secondEnd <- end
		second <- time.Since(t0)
	}()

	var laufEnde, platzFrei time.Duration
	var endLine string
	for laufEnde == 0 || platzFrei == 0 {
		if time.Since(t0) > l1WartenAufLauf {
			t.Fatalf("nach %v: Lauf beendet %v, Platz frei %v", l1WartenAufLauf, laufEnde, platzFrei)
		}
		logs := must(t, "docker", "logs", project+"-script-runner-1")
		if line := internalRunAfter(logs, runsBefore); laufEnde == 0 && line != "" {
			laufEnde = time.Since(t0)
			endLine = line
		}
		if platzFrei == 0 && len(instanceRegistrations(t)) > regsBefore {
			platzFrei = time.Since(t0)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if end := <-secondEnd; end != "ok" {
		t.Errorf("zweiter Lauf: Ende %s", end)
	}
	zweiter := <-second
	restarts := sandboxRestarts(t) - restartsBefore

	t.Logf("L1 %s: vermittler beendet den Lauf nach %.1f s (%s), Platz frei (neue Instanz registriert) nach %.1f s, zweiter Lauf fertig nach %.1f s, Sandbox-Neustarts %d",
		mode, laufEnde.Seconds(), endLine, platzFrei.Seconds(), zweiter.Seconds(), restarts)
	if !strings.Contains(endLine, "end=internal") {
		t.Errorf("Ende des abgebrochenen Laufs: %q, erwartet end=internal", endLine)
	}
	if laufEnde > l1LaufBeendet {
		t.Errorf("vermittler beendet den Lauf erst nach %v (Schranke %v)", laufEnde, l1LaufBeendet)
	}
	if platzFrei > l1PlatzFrei {
		t.Errorf("Platz frei erst nach %v (Schranke %v)", platzFrei, l1PlatzFrei)
	}
	if zweiter > l1ZweiterLauf {
		t.Errorf("zweiter Lauf erst nach %v (Schranke %v)", zweiter, l1ZweiterLauf)
	}
	if restarts < 1 {
		t.Errorf("Sandbox nicht neu gestartet: der Prozess des abgebrochenen Laufs lebt womöglich weiter")
	}
}

// l1StartenBisInnererAufruf startet den Lauf mit abbrechbarem ctx und kehrt
// zurück, sobald sein innerer Aufruf das Gateway erreicht hat. Ein busy (Platz
// noch belegt) wiederholt es wie callReady.
func l1StartenBisInnererAufruf(t *testing.T, ctx context.Context, mode, inner string) <-chan struct{} {
	t.Helper()
	deadline := time.Now().Add(l1WartenAufLauf)
	for time.Now().Before(deadline) {
		for len(gw.entered) > 0 {
			<-gw.entered
		}
		ended := make(chan struct{})
		busy := make(chan bool, 1)
		go func() {
			defer close(ended)
			_, m, err := tryCallCtx(ctx, project, "abbruch", map[string]any{"mode": mode})
			busy <- err == nil && m["end"] == "busy"
		}()
		select {
		case name := <-gw.entered:
			if name != inner {
				t.Fatalf("innerer Aufruf %q, erwartet %q", name, inner)
			}
			return ended
		case b := <-busy:
			if !b {
				t.Fatal("Lauf endete vor seinem inneren Aufruf")
			}
			time.Sleep(500 * time.Millisecond)
		case <-time.After(l1WartenAufLauf):
			t.Fatal("kein innerer Aufruf")
		}
	}
	t.Fatal("Lauf nicht gestartet (dauernd busy)")
	return nil
}

func tryCallCtx(ctx context.Context, proj, name string, args map[string]any) (*mcp.CallToolResult, map[string]any, error) {
	h := http.Header{}
	h.Set("X-Jnpt-Run-Token", runToken)
	h.Set("X-Jnpt-Run-Id", runID)
	h.Set("X-Jnpt-Pin-Commit", commit)
	s, err := session(proj, h)
	if err != nil {
		return nil, nil, err
	}
	defer s.Close()
	res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, nil, err
	}
	b, _ := json.Marshal(res.Meta["januaport.ai/run"])
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return res, m, nil
}

func internalRunAfter(logs string, n int) string {
	i := 0
	for _, l := range strings.Split(logs, "\n") {
		if !strings.Contains(l, "msg=run ") {
			continue
		}
		if i >= n && strings.Contains(l, "end=internal") {
			return strings.TrimSpace(l[strings.Index(l, "msg=run "):])
		}
		i++
	}
	return ""
}

func sandboxRestarts(t *testing.T) int {
	t.Helper()
	out := strings.TrimSpace(must(t, "docker", "inspect", "-f", "{{.RestartCount}}", project+"-script-runner-sandbox-1"))
	n := 0
	for _, c := range out {
		if c < '0' || c > '9' {
			t.Fatalf("RestartCount %q", out)
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// quickNachAbbruch ist der zweite Lauf: wiederholt bei busy alle 0,5 s, wie
// eine KI nach dem festen Satz, und liefert sein Ende.
func quickNachAbbruch() string {
	deadline := time.Now().Add(l1WartenAufLauf)
	for {
		_, m, err := tryCallCtx(context.Background(), project, "quick", map[string]any{})
		end, _ := m["end"].(string)
		if err != nil {
			end = "fehler: " + err.Error()
		}
		if end != "busy" || time.Now().After(deadline) {
			return end
		}
		time.Sleep(500 * time.Millisecond)
	}
}
