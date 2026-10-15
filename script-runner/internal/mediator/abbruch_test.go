package mediator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/januaport/januaport-plugins/script-runner/internal/frame"
)

// SEC L1 (JanuaPort/januaport#1040): Bricht das Gateway den Relay-Aufruf ab
// (seine POST-Anfrage endet), endet der Lauf sofort: Der Wächter bekommt EOF
// und räumt ab, der vermittler meldet end=internal. Ohne das liefe ein
// entzogener Lauf bis zu seiner Wandzeit weiter und hielte den einzigen Platz.
func TestAbbruchDesRelaysBeendetDenLauf(t *testing.T) {
	e := newEnv(t, nil)
	e.writePins(e.quickPin())
	jobSeen := make(chan struct{})
	eof := make(chan error, 1)
	done := e.oneRun("i1", func(g *fakeGuard, _ frame.Job) {
		close(jobSeen)
		eof <- g.waitEOF()
	})
	e.waitReady("quick")
	s, err := e.session(runHeaders(e.fx.commit))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _, _ = s.CallTool(ctx, &mcp.CallToolParams{Name: "quick", Arguments: map[string]any{}}) }()
	<-jobSeen
	start := time.Now()
	cancel()
	if err := <-eof; err != nil {
		t.Fatalf("Wächter: %v", err)
	}
	<-done
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("Lauf erst nach %v beendet", d)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(e.logs.String(), "msg=run end=internal") {
		if time.Now().After(deadline) {
			t.Fatalf("kein end=internal im Log:\n%s", e.logs.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
