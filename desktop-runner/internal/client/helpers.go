package client

import (
	"bytes"
	"context"
	"encoding/json"
)

// L3-2: this state belongs to one Execute, never to the Python interpreter.
// The historical signed job grant also binds the exact environment and result.
type runHelpers struct {
	engine   *Engine
	order    TestOrder
	allowed  bool
	result   json.RawMessage
	captures int
}

func (e *Engine) newRunHelpers(r Result, pin Pin) *runHelpers {
	h := &runHelpers{engine: e}
	if r.RunKey != "" || r.RanOffline || !uuidRE.MatchString(r.JobID) || e.Container != nil || e.API == nil || e.API.Contract() != WindowsContract {
		return h
	}
	if e.State.Load("test-"+r.JobID, &h.order) != nil {
		return h
	}
	o := h.order
	h.allowed = o.verified(e.Credential) && r.JobID == o.JobID && r.ManifestSeq == o.ManifestSeq && r.Commit == o.Commit && pin.Pin == o.Pin && pin.Commit == o.Commit && pin.Path == o.Path && pin.Algo == o.Algo
	return h
}

func (h *runHelpers) setResult(raw []byte) error {
	var v struct {
		Type string          `json:"type"`
		Wert json.RawMessage `json:"wert"`
	}
	// Unlike strict's ordinary required fields, wert explicitly permits null.
	// canonical checks duplicate keys, trailing documents and Unicode first.
	if _, err := canonical(raw); err != nil {
		return ErrProtocol
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&v) != nil || v.Type != "ergebnis" || len(v.Wert) == 0 || len(v.Wert) > 64<<10 {
		return ErrProtocol
	}
	// Match the actual JSON representation sent by ReportTest, including HTML
	// escaping in RawMessage. Raw whitespace still counts against the input cap.
	wire, err := json.Marshal(v.Wert)
	if err != nil || len(wire) > 64<<10 {
		return ErrProtocol
	}
	if h.allowed {
		h.result = wire
		if bytes.Equal(bytes.TrimSpace(v.Wert), []byte("null")) {
			h.result = nil
		}
	}
	return nil
}

func (h *runHelpers) screenshot(ctx context.Context, final bool) error {
	// Authorization precedes GDI, including the automatic final capture. Count
	// attempts (also capture/upload errors), so failures cannot bypass the rate.
	limit := 4 // reserve the fifth attempt for automatic completion capture
	if final {
		limit = 5
	}
	if !h.allowed || h.captures >= limit || h.engine.Capture == nil || ctx.Err() != nil {
		return ErrProtocol
	}
	h.captures++
	png, err := h.engine.Capture(ctx)
	if err != nil {
		return err
	}
	if len(png) > 4<<20 || !bytes.HasPrefix(png, []byte{137, 80, 78, 71, 13, 10, 26, 10}) {
		return ErrProtocol
	}
	return h.engine.API.Screenshot(ctx, h.engine.Credential, h.order, png)
}

type helperReply struct {
	Type string `json:"type"`
	OK   bool   `json:"ok"`
}

func (h *runHelpers) handle(ctx context.Context, kind string, raw []byte, write func(any) error) error {
	switch kind {
	case "ergebnis":
		if err := h.setResult(raw); err != nil {
			return err
		}
	case "screenshot":
		var v struct {
			Type string `json:"type"`
		}
		if strict(raw, &v) != nil {
			return ErrProtocol
		}
		if h.screenshot(ctx, false) != nil {
			return write(struct {
				Type string `json:"type"`
				Code string `json:"code"`
			}{"fehler", "abgewiesen"})
		}
	default:
		return ErrProtocol
	}
	return write(helperReply{kind, true})
}
