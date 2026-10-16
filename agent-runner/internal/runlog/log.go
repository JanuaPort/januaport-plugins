// Package runlog records counters and tool names, with no content fields.
package runlog

import (
	"encoding/json"
	"io"
	"sync"
)

type Record struct {
	Steps      int      `json:"steps"`
	ToolCalls  int      `json:"tool_calls"`
	Tools      []string `json:"tools"`
	End        string   `json:"end"`
	DurationMS int64    `json:"duration_ms"`
}
type Writer struct {
	mu  sync.Mutex
	out io.Writer
}

func New(out io.Writer) *Writer {
	if out == nil {
		out = io.Discard
	}
	return &Writer{out: out}
}
func (w *Writer) Write(r Record) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = json.NewEncoder(w.out).Encode(r)
}
