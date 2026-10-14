package mediator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/schedules"
	"github.com/januaport/januaport-plugins/script-runner/internal/ticker"
)

// Feste Log-Codes für schedules.json (M6: nie Inhalt der Datei).
const (
	codeSchedulesInvalid   = "schedules_invalid"
	codeSchedulesOverLimit = "schedules_over_limit"
)

// triggerTimeout ist die HTTP-Frist des Auslöse-Aufrufs (Kernvertrag:
// 35 s, über der Lauf-Frist des Gateways von höchstens 30 s).
const triggerTimeout = 35 * time.Second

// tickSchedules liest schedules.json bei jedem Abgleich neu (wie pins.json)
// und löst fällige Termine aus, jeden in einer eigenen Goroutine: Der
// Abgleich wartet nie auf das Gateway. Nur aus pollLoop aufrufen.
func (m *Mediator) tickSchedules(ctx context.Context) {
	entries := m.readSchedules()
	ready := m.readyPins()
	isReady := func(pin, commit string) bool { return ready[pin] == commit }
	for _, d := range m.ticker.Tick(m.cfg.Now(), entries, isReady) {
		go func(d ticker.Due) {
			m.ticker.Record(d.Name, d.Slot, m.trigger.fire(ctx, d.Name))
		}(d)
	}
}

// readSchedules liefert die Zeitpläne; fehlt die Datei oder ist sie
// ungültig, gibt es keine (fail-closed). Geloggt wird nur ein Wechsel.
func (m *Mediator) readSchedules() []schedules.Entry {
	b, err := readLimited(filepath.Join(m.cfg.KeyDir, "schedules.json"), schedules.MaxFileBytes)
	if errors.Is(err, os.ErrNotExist) {
		m.noteSchedules("", 0)
		return nil
	}
	var f schedules.File
	if err == nil {
		f, err = schedules.Parse(b)
	}
	if err != nil {
		m.noteSchedules(codeSchedulesInvalid, 0)
		return nil
	}
	if f.Ignored > 0 {
		m.noteSchedules(codeSchedulesOverLimit, f.Ignored)
	} else {
		m.noteSchedules("", 0)
	}
	return f.Entries
}

// readLimited liest höchstens limit Bytes; eine größere Datei ist ein Fehler
// (fail-closed, SEC R4 H1), nie ein abgeschnittener Inhalt.
func readLimited(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errSchedulesTooLarge
	}
	return b, nil
}

var errSchedulesTooLarge = errors.New("schedules.json zu groß")

func (m *Mediator) noteSchedules(code string, ignored int) {
	state := fmt.Sprintf("%s/%d", code, ignored)
	if state == m.schedState {
		return
	}
	m.schedState = state
	switch code {
	case codeSchedulesInvalid:
		m.log.Warn(codeSchedulesInvalid)
	case codeSchedulesOverLimit:
		m.log.Warn(codeSchedulesOverLimit, "ignored", ignored)
	}
}

// readyPins: Pin-Name → Commit für jeden Pin, der wie in tools/list
// bereit ist (ohne gültige Isolation ist keiner bereit).
func (m *Mediator) readyPins() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ready := map[string]string{}
	if m.isolation == contract.IsolationInvalid {
		return ready
	}
	for _, p := range m.pins {
		if p.pin.Invalid == "" && p.state == contract.StateReady {
			ready[p.pin.Name] = p.pin.Commit
		}
	}
	return ready
}

// gatewayTrigger bittet das Gateway, einen fälligen Termin zu prüfen
// (#1039). Kein Body, Bearer = runtime-key, je Aufruf frisch gelesen. Er
// hält keinen Slot und spricht nie mit der Sandbox: Den Lauf startet das
// Gateway über Weg A (V1). Er loggt nichts; Fehlertexte enthalten die URL.
type gatewayTrigger struct {
	base    string
	keyPath string
	client  *http.Client
}

func newGatewayTrigger(base, keyPath string, timeout time.Duration) *gatewayTrigger {
	return &gatewayTrigger{base: base, keyPath: keyPath, client: &http.Client{
		Timeout: timeout,
		// Der Schlüssel geht an genau eine Adresse, keiner Umleitung nach.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (g *gatewayTrigger) fire(ctx context.Context, name string) ticker.Outcome {
	key := readKey(g.keyPath)
	if key == "" {
		return ticker.Outcome{Kind: ticker.Failed}
	}
	target := g.base + "/api/plugin/script-runner/schedules/" + url.PathEscape(name) + "/run"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, nil)
	if err != nil {
		return ticker.Outcome{Kind: ticker.Failed}
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := g.client.Do(req)
	if err != nil {
		return ticker.Outcome{Kind: ticker.Failed}
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
		return ticker.Outcome{Kind: ticker.Ran, End: readEnd(resp.Body)}
	case http.StatusConflict:
		return ticker.Outcome{Kind: ticker.Refused}
	}
	return ticker.Outcome{Kind: ticker.Failed, Status: resp.StatusCode}
}

// readEnd liest höchstens 1 KiB; was keine Ende-Klasse ist, macht der
// Taktgeber zu „unknown“.
func readEnd(r io.Reader) string {
	var body struct {
		End string `json:"end"`
	}
	if json.NewDecoder(io.LimitReader(r, 1024)).Decode(&body) != nil {
		return ""
	}
	return body.End
}
