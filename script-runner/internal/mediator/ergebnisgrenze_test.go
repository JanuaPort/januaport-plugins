package mediator

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/frame"
	"github.com/januaport/januaport-plugins/script-runner/internal/guard"
)

// #1538 Teil 1: Der Wächter liest höchstens guard.MaxJobFrame. Ein größeres
// Werkzeugergebnis würde den Rahmen sprengen und die Instanz kosten
// (limit/sandbox_lost). Der vermittler macht daraus vorher einen Werkzeugfehler.

// Tabelle der Grenze selbst, ohne Draht: genau die größte zulässige Länge
// passt, ein Byte mehr nicht.
func TestToolResultFitsBoundary(t *testing.T) {
	for _, tc := range []struct {
		name string
		n    int
		want bool
	}{
		{"leer", 0, true},
		{"genau an der Grenze", maxToolResultBytes, true},
		{"ein Byte darüber", maxToolResultBytes + 1, false},
		{"Rahmengrenze des Wächters", guard.MaxJobFrame, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := toolResultFits(make(json.RawMessage, tc.n)); got != tc.want {
				t.Fatalf("toolResultFits(%d) = %v, want %v", tc.n, got, tc.want)
			}
		})
	}
}

// Die Reserve deckt den Umschlag eines tool_result-Rahmens sicher ab: Ein
// Ergebnis an der Grenze ergibt einen Rahmen, den der Wächter noch liest.
func TestToolResultEnvelopeFitsReserve(t *testing.T) {
	env, err := json.Marshal(frame.ToolResult{Type: frame.TypeToolResult, ID: -1 << 63, Result: json.RawMessage(`0`)})
	if err != nil {
		t.Fatal(err)
	}
	if len(env)+maxToolResultBytes > guard.MaxJobFrame {
		t.Fatalf("Umschlag %d Byte + Grenze %d > MaxJobFrame %d", len(env), maxToolResultBytes, guard.MaxJobFrame)
	}
}

// toolResultRun lässt ein Skript genau einen Werkzeugaufruf stellen, an dem
// das Gateway-Ergebnis pad Byte Text trägt, und gibt Rahmen und Lauf zurück.
func toolResultRun(t *testing.T, pad int) (frameLen int, result string, meta map[string]any) {
	t.Helper()
	e := newEnv(t, nil)
	e.writePins(e.quickPin())
	e.gw.pad.Store(int64(pad))
	var raw []byte
	done := e.oneRun("i1", func(g *fakeGuard, _ frame.Job) {
		g.send(frame.ToolCall{Type: frame.TypeToolCall, ID: 7, Name: "kartei_find", Arguments: json.RawMessage(`{}`)})
		typ, b, err := g.read()
		if err != nil || typ != frame.TypeToolResult {
			t.Errorf("tool_result erwartet: %q %v", typ, err)
		}
		raw = b
		g.exited(0)
	})
	e.waitReady("quick")
	call := e.mustCall("quick", map[string]any{})
	<-done
	var tr frame.ToolResult
	if err := json.Unmarshal(raw, &tr); err != nil {
		t.Fatalf("tool_result: %v", err)
	}
	if tr.ID != 7 {
		t.Fatalf("tool_result-ID = %d", tr.ID)
	}
	return len(raw), string(tr.Result), runMeta(t, call)
}

// Knapp unter der Grenze: normal zugestellt, kein Fehler, der Rahmen bleibt
// unter dem, was der Wächter liest.
func TestToolResultJustUnderLimitIsDelivered(t *testing.T) {
	n, result, meta := toolResultRun(t, guard.MaxJobFrame-16<<10)
	if n > guard.MaxJobFrame {
		t.Fatalf("Rahmen %d Byte > MaxJobFrame %d", n, guard.MaxJobFrame)
	}
	if strings.Contains(result, `"isError":true`) || !strings.Contains(result, "xxxxxxxx") {
		t.Fatalf("Ergebnis nicht zugestellt: %.200s", result)
	}
	if meta["end"] != contract.EndOK {
		t.Fatalf("Ende = %v/%v", meta["end"], meta["detail"])
	}
}

// Knapp darüber: Werkzeugfehler mit fester Meldung ohne Inhalt; der Lauf geht
// weiter und endet ok, nicht als limit/sandbox_lost.
func TestToolResultOverLimitBecomesToolError(t *testing.T) {
	n, result, meta := toolResultRun(t, guard.MaxJobFrame+1<<10)
	if n > guard.MaxJobFrame {
		t.Fatalf("Rahmen %d Byte > MaxJobFrame %d", n, guard.MaxJobFrame)
	}
	if !strings.Contains(result, `"isError":true`) || !strings.Contains(result, "Werkzeugergebnis zu groß") {
		t.Fatalf("Werkzeugfehler erwartet: %.200s", result)
	}
	if strings.Contains(result, "xxxxxxxx") {
		t.Fatal("Inhalt des zu großen Ergebnisses in der Meldung")
	}
	if meta["end"] != contract.EndOK || meta["detail"] == contract.DetailSandboxLost {
		t.Fatalf("Ende = %v/%v", meta["end"], meta["detail"])
	}
}

// #1538 Teil 2: Weg B folgt keiner Umleitung. Der zweite Server sieht weder
// einen Request noch das Lauf-Token.
func TestGatewayClientRejectsRedirectWithoutLeakingToken(t *testing.T) {
	var hits atomic.Int64
	var auth atomic.Value
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		auth.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(second.Close)
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, second.URL+"/mcp", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(first.Close)

	g := &gatewayClient{url: first.URL + "/mcp", token: testRunToken}
	defer g.close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res := g.call(ctx, "kartei_find", nil)

	if !strings.Contains(string(res), `"isError":true`) {
		t.Fatalf("Werkzeugfehler erwartet: %s", res)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("zweiter Server bekam %d Request(s), Authorization %v", n, auth.Load())
	}
}

// Keine Proxy-Umgebung auf irgendeinem ausgehenden Weg des vermittlers. Die
// Umgebung (HTTP_PROXY) wird von Go einmal je Prozess gelesen; deshalb prüft
// der Test die Quelle des Verhaltens: Transport.Proxy ist nil.
func TestOutboundTransportsIgnoreProxyEnvironment(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://proxy.invalid:3128")
	t.Setenv("HTTPS_PROXY", "http://proxy.invalid:3128")
	req, _ := http.NewRequest(http.MethodGet, "http://gateway.example/mcp", nil)

	check := func(name string, rt http.RoundTripper) {
		t.Helper()
		tr, ok := rt.(*http.Transport)
		if !ok {
			t.Errorf("%s: Transport ist %T, erwartet *http.Transport mit Proxy nil", name, rt)
			return
		}
		if tr.Proxy != nil {
			t.Errorf("%s: Transport.Proxy gesetzt", name)
		}
	}

	wegB := newGatewayHTTPClient(testRunToken)
	b, ok := wegB.Transport.(bearer)
	if !ok {
		t.Fatalf("Weg B: Transport ist %T, erwartet bearer", wegB.Transport)
	}
	check("Weg B (bearer)", b.base)
	if wegB.CheckRedirect == nil {
		t.Error("Weg B: CheckRedirect fehlt")
	} else if err := wegB.CheckRedirect(req, nil); err == nil {
		t.Error("Weg B: Umleitung nicht abgelehnt")
	}

	courier := newStorageCourier("http://gateway.example", t.TempDir()+"/key", t.TempDir(), time.Now, slog.New(slog.DiscardHandler))
	check("Speicherbote", courier.client.Transport)

	check("Taktgeber", newGatewayTrigger("http://gateway.example", t.TempDir()+"/key", time.Second).client.Transport)
}
