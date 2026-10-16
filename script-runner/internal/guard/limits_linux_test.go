//go:build linux

package guard

import (
	"bytes"
	"encoding/json"
	"net"
	"strings"
	"testing"

	"github.com/januaport/januaport-plugins/script-runner/internal/frame"
)

// Exercise the actual stdout and bootstrap adapters: the guard must forward
// beyond both v1 aggregate limits. Frames remain a separate protocol limit.
func TestRunLimitsGuardStreamsBeyondV1(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	r := &runState{conn: a}
	done := make(chan struct{})
	var output, calls int
	go func() {
		defer close(done)
		for {
			p, err := frame.Read(b, frame.MaxFromSandbox)
			if err != nil {
				return
			}
			var msg struct{ Type, Data string }
			if err := json.Unmarshal(p, &msg); err != nil {
				t.Error(err)
				return
			}
			switch msg.Type {
			case frame.TypeOutput:
				output += len(msg.Data)
			case frame.TypeToolCall:
				calls++
			}
		}
	}()
	// Escaping expands every byte sixfold on the wire, still within each frame.
	r.pumpOutput(strings.NewReader(strings.Repeat("\x01", 16777216)))
	var bootstrap bytes.Buffer
	for i := 1; i <= 61; i++ {
		if err := frame.Write(&bootstrap, frame.ToolCall{Type: frame.TypeToolCall, ID: int64(i), Name: "kartei_find", Arguments: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	r.pumpBootstrap(&bootstrap)
	_ = a.Close()
	<-done
	if output != 16777216 || calls != 61 {
		t.Fatalf("forwarded output=%d calls=%d", output, calls)
	}
}
