package mediator

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/frame"
)

// ETL-Rohstrom (Vertragsfassung 3, JanuaPort/januaport#1215 P2).
//
// Ein Skript in der Sandbox (ohne Netz) ruft januaport.etl_stream() auf. Der
// vermittler macht den HTTP-Aufruf POST /api/etl/stream des Kerns mit dem
// Lauf-Token und reicht den Körper stückweise (Pull) über Weg C weiter.
//
// Er ist inhaltsblind: Er parst keine Zeilen, kennt nur Zeilenumbrüche und
// die letzte Zeile, und das auch nur auf genau das Feld des Endsatzes. Er
// protokolliert nur Zähler. Den Deckel hält allein das Gateway; der vermittler
// zählt Bytes und Zeilen als Beleg und bricht nie selbst nach Budget ab.

const (
	// etlPath ist fest; er kommt nie aus der Eingabe (SEC E5).
	etlPath    = "/api/etl/stream"
	healthPath = "/healthz"
	// etlScope ist das Strom-Recht im Manifest (`tools`). Es ist nie ein
	// Werkzeug des Gateways.
	etlScope = "etl_stream"

	maxEndLine      = 512     // der Endsatz des Gateways ist deutlich kürzer
	maxQueryName    = 256     // integration und query_id
	maxRetryAfterS  = 86400   // größere Wartezeiten sind ein Fehler im Kopf
	healthMaxBytes  = 4 << 10 // /healthz ist knapp
	etlHeaderWait   = 60 * time.Second
	etlWriteTimeout = 10 * time.Second // kleine Rahmen der Schleife; Stücke bremst die Wandzeit
	maxRefusedOpens = 8
)

var errRedirect = errors.New("etl: Umleitung abgewiesen")

// etlClient ist der einzige HTTP-Client für den Strom und die Versionsabfrage
// (Hygiene: TestNoDownloadClientInSource). SEC E5: keine Umleitung, keine
// Proxy-Umgebung, fester Pfad, das Token nur im Authorization-Header.
type etlClient struct {
	base string
	http *http.Client
}

func newETLClient(base string) *etlClient {
	transport := &http.Transport{
		Proxy:                 nil,
		DisableCompression:    true,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		ResponseHeaderTimeout: etlHeaderWait,
		MaxIdleConns:          2,
		IdleConnTimeout:       30 * time.Second,
	}
	return &etlClient{
		base: strings.TrimRight(base, "/"),
		http: &http.Client{
			Transport:     transport,
			CheckRedirect: func(*http.Request, []*http.Request) error { return errRedirect },
		},
	}
}

func (c *etlClient) openStream(ctx context.Context, token string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+etlPath, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/x-ndjson")
	return c.http.Do(req)
}

// coreVersion liest `version` aus /healthz des Kerns. Alles, was nicht eindeutig
// ein Text ist, gilt als unlesbar (ok=false); die Versionsfassung wählt dann
// contract.ForCoreVersion (Rückfall, E4).
func (c *etlClient) coreVersion(ctx context.Context) (string, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+healthPath, nil)
	if err != nil {
		return "", false
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, healthMaxBytes))
	if err != nil {
		return "", false
	}
	var h struct {
		Version json.RawMessage `json:"version"`
	}
	if json.Unmarshal(body, &h) != nil {
		return "", false
	}
	var v string
	if json.Unmarshal(h.Version, &v) != nil {
		return "", false
	}
	return v, true
}

// coreProbe hält die gewählte Vertragsfassung. Er fragt den Kern regelmäßig;
// bis zur ersten lesbaren Antwort und bei jedem Fehler gilt Fassung 2.
type coreProbe struct {
	client   *etlClient
	interval time.Duration
	log      *slog.Logger

	mu       sync.Mutex
	selected int
}

func newCoreProbe(client *etlClient, interval time.Duration, log *slog.Logger) *coreProbe {
	return &coreProbe{client: client, interval: interval, log: log, selected: contract.LegacyVersion}
}

func (p *coreProbe) version() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.selected
}

func (p *coreProbe) probe(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	want := contract.LegacyVersion
	if v, ok := p.client.coreVersion(ctx); ok {
		want = contract.ForCoreVersion(v)
	}
	p.mu.Lock()
	changed := p.selected != want
	p.selected = want
	p.mu.Unlock()
	if changed {
		// Nur die gewählte Fassung, nie der Text des Kerns.
		p.log.Info("vertragsfassung", "contract", want)
	}
}

func (p *coreProbe) run(ctx context.Context) {
	t := time.NewTicker(p.interval)
	defer t.Stop()
	for {
		p.probe(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (m *Mediator) contractVersion() int { return m.core.version() }

// --- Zeilenzähler ----------------------------------------------------------------

// lineTracker zählt Zeilenumbrüche und merkt sich den Grund des Endsatzes, wenn
// die letzte vollständige Zeile einer war. Mehr liest der vermittler nicht:
// Eine Zeile wird nur dann angesehen, wenn sie die Form des Endsatzes hat
// (`{"t":"end"`), und nie länger als maxEndLine Byte.
type lineTracker struct {
	lines     int64
	cur       []byte
	curLen    int
	lastGrund string
}

var endPrefix = []byte(`{"t":"end"`)

func (t *lineTracker) write(p []byte) {
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		seg := p
		if i >= 0 {
			seg = p[:i]
		}
		t.curLen += len(seg)
		if t.curLen <= maxEndLine {
			t.cur = append(t.cur, seg...)
		}
		if i < 0 {
			return
		}
		t.lines++
		t.lastGrund = ""
		if t.curLen <= maxEndLine {
			t.lastGrund = endGrund(t.cur)
		}
		t.cur, t.curLen = t.cur[:0], 0
		p = p[i+1:]
	}
}

// endGrund liest den Grund einer Endsatz-Zeile, sonst "". Der Grund ist ein
// kurzer Text aus einer geschlossenen Menge des Gateways; unbekannte Werte
// zählen später als nicht fertig.
func endGrund(line []byte) string {
	if !bytes.HasPrefix(line, endPrefix) {
		return ""
	}
	var e struct {
		T     string `json:"t"`
		Grund string `json:"grund"`
	}
	if json.Unmarshal(line, &e) != nil || e.T != "end" || e.Grund == "" || len(e.Grund) > 32 {
		return ""
	}
	return e.Grund
}

// partial heißt: Die letzte Zeile ist nicht mit einem Umbruch abgeschlossen.
func (t *lineTracker) partial() bool { return t.curLen > 0 }

// rows sind die Zeilen ohne den Endsatz, wie das Gateway sie selbst zählt.
func (t *lineTracker) rows() int64 {
	if t.lastGrund != "" && !t.partial() {
		return t.lines - 1
	}
	return t.lines
}

// statusOfEnd bildet den Grund des Endsatzes auf den Strom-Status ab.
func statusOfEnd(grund string) string {
	switch grund {
	case "fertig":
		return contract.StreamFertig
	case "deckel", "wandzeit":
		return contract.StreamGrenze
	}
	return contract.StreamAbgebrochen
}

// --- Ein Strom ---------------------------------------------------------------------

type etlStream struct {
	id     int64
	client *etlClient
	token  string // nur für den Header; nie in Log, Fehler oder Ergebnis
	body   []byte // Anfragekörper, höchstens 64 KiB
	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	resp    io.ReadCloser
	started bool
	closed  bool
	final   string // Ausgang, sobald entschieden
	reply   *frame.ETLEnd
	done    bool // Körper zu Ende (oder Lesefehler)
	failed  bool
	bytes   int64
	lines   lineTracker
}

// endedLocked legt den festen Endsatz an, den jedes weitere etl_next wiederholt.
func (s *etlStream) endedLocked(status string, code, retry int) *frame.ETLEnd {
	e := &frame.ETLEnd{Type: frame.TypeETLEnd, ID: s.id, Status: status, Code: code, RetryAfterS: retry}
	s.reply = e
	return e
}

// shutdown schließt die Anfrage, mit und ohne laufenden Lesevorgang (der
// Kontext bricht ihn ab). Mehrfaches Aufrufen schadet nicht.
func (s *etlStream) shutdown() {
	s.mu.Lock()
	s.closed = true
	resp := s.resp
	s.mu.Unlock()
	s.cancel()
	if resp != nil {
		_ = resp.Close()
	}
}

// closeByScript ist etl_next{close}: Das Skript beendet den Strom selbst.
func (s *etlStream) closeByScript() frame.ETLEnd {
	s.mu.Lock()
	if s.final == "" {
		s.final = contract.StreamGeschlossen
	}
	e := frame.ETLEnd{Type: frame.TypeETLEnd, ID: s.id, Status: frame.ETLStatusGeschlossen}
	s.mu.Unlock()
	s.shutdown()
	return e
}

// violation beendet den Strom wegen eines Protokollverstoßes des Skripts.
func (s *etlStream) violation() frame.ETLEnd {
	s.mu.Lock()
	if s.final == "" {
		s.final = contract.StreamVertrag
	}
	e := frame.ETLEnd{Type: frame.TypeETLEnd, ID: s.id, Status: frame.ETLStatusVertrag}
	s.mu.Unlock()
	s.shutdown()
	return e
}

// report ist der Ausgang für das Lauf-Ergebnis. Ist er noch nicht entschieden,
// endet das Skript (scriptEnded) mit offenem Strom: Hat es den Endsatz schon
// gelesen, gilt dieser; sonst hat es den Strom verlassen. Ein Abbruch von außen
// (Wandzeit, Sandbox, Kontext) ist abgebrochen.
func (s *etlStream) report(scriptEnded bool) contract.StreamReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := s.final
	if status == "" {
		switch {
		case !scriptEnded:
			status = contract.StreamAbgebrochen
		case !s.lines.partial() && s.lines.lastGrund != "":
			status = statusOfEnd(s.lines.lastGrund)
		default:
			status = contract.StreamGeschlossen
		}
	}
	return contract.StreamReport{Status: status, Bytes: s.bytes, Zeilen: s.lines.rows()}
}

// next beantwortet ein etl_next. Es läuft in einem eigenen Lauf, höchstens
// einer je Strom, und liefert den zu sendenden Rahmen (nil: nichts mehr zu
// senden, weil der Strom inzwischen geschlossen wurde). Es liest höchstens ein
// Stück vom Körper und hält sonst nichts.
func (s *etlStream) next() any {
	s.mu.Lock()
	if s.reply != nil {
		e := *s.reply
		s.mu.Unlock()
		return e
	}
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	started, done := s.started, s.done
	s.mu.Unlock()
	if done {
		return s.finish()
	}
	if !started {
		if e, ok := s.start(); !ok {
			if e == nil {
				return nil
			}
			return *e
		}
	}
	s.mu.Lock()
	resp := s.resp
	s.mu.Unlock()
	buf := make([]byte, frame.MaxETLChunkRaw)
	n, err := readChunk(resp, buf)

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.bytes += int64(n)
	s.lines.write(buf[:n])
	if err != nil {
		s.done = true
		s.failed = !errors.Is(err, io.EOF)
		s.decideAtBodyEndLocked()
	}
	s.mu.Unlock()
	if n == 0 {
		return s.finish()
	}
	return frame.ETLChunk{Type: frame.TypeETLChunk, ID: s.id, DataB64: base64.StdEncoding.EncodeToString(buf[:n])}
}

// readChunk füllt buf, soweit der Körper reicht. Anders als io.ReadFull
// verwechselt es das saubere Ende (io.EOF) nicht mit einem abgerissenen Körper
// (io.ErrUnexpectedEOF des http-Pakets): Nur das saubere Ende ist io.EOF.
func readChunk(r io.Reader, buf []byte) (int, error) {
	n := 0
	for n < len(buf) {
		m, err := r.Read(buf[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// decideAtBodyEndLocked legt den Ausgang fest, wenn der Körper zu Ende ist:
// Nur ein vollständig gelesener Endsatz entscheidet; sonst war der Strom
// abgebrochen (so unterscheidet der Client „fertig“ von jedem Abbruch).
func (s *etlStream) decideAtBodyEndLocked() {
	if s.final != "" {
		return
	}
	if s.failed || s.lines.partial() || s.lines.lastGrund == "" {
		s.final = contract.StreamAbgebrochen
		return
	}
	s.final = statusOfEnd(s.lines.lastGrund)
}

func (s *etlStream) finish() any {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := frame.ETLStatusEOF
	if s.failed {
		status = frame.ETLStatusAbgebrochen
	}
	return *s.endedLocked(status, 0, 0)
}

// start schickt die Anfrage an den Kern. ok=false: Es gibt nichts zu lesen;
// e ist der Endsatz dazu (nil, wenn der Strom schon geschlossen ist).
func (s *etlStream) start() (e *frame.ETLEnd, ok bool) {
	resp, err := s.client.openStream(s.ctx, s.token, s.body)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		if resp != nil {
			_ = resp.Body.Close()
		}
		return nil, false
	}
	if err != nil {
		// Feste Klasse; der Fehlertext enthält die Adresse und bleibt unbenutzt.
		status := contract.StreamAbgebrochen
		wire := frame.ETLStatusAbgebrochen
		if errors.Is(err, errRedirect) {
			status, wire = contract.StreamVertrag, frame.ETLStatusVertrag
		}
		s.final = status
		return s.endedLocked(wire, 0, 0), false
	}
	if out := classify(resp); !out.ok {
		_ = resp.Body.Close() // der Fehlertext des Kerns wird nie gelesen
		s.final = out.status
		return s.endedLocked(out.wire, out.code, out.retry), false
	}
	s.resp, s.started = resp.Body, true
	return nil, true
}

// outcome ist die Klasse einer HTTP-Antwort des Kerns.
type outcome struct {
	ok          bool
	status      string // Strom-Status für das Lauf-Ergebnis
	wire        string // Status in etl_end
	code, retry int
}

// classify bildet den Statuscode auf feste Klassen ab. 409 (Deckel, Freigabe),
// 429 (belegt), 503 (Audit) und die Berechtigungsfehler sind „abgewiesen“ und
// tragen nur Code und Wartezeit; alles Unerwartete ist ein Vertragsbruch.
func classify(resp *http.Response) outcome {
	switch resp.StatusCode {
	case http.StatusOK:
		if mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err == nil && mt == "application/x-ndjson" {
			return outcome{ok: true}
		}
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound,
		http.StatusConflict, http.StatusTooManyRequests, http.StatusServiceUnavailable:
		return outcome{status: contract.StreamAbgewiesen, wire: frame.ETLStatusAbgewiesen, code: resp.StatusCode, retry: retryAfter(resp.Header.Get("Retry-After"))}
	}
	return outcome{status: contract.StreamVertrag, wire: frame.ETLStatusVertrag}
}

// retryAfter liest nur eine Dezimalzahl in Sekunden (die Datumsform kennt der
// Kern nicht); alles andere ist 0 und fehlt im Rahmen.
func retryAfter(v string) int {
	if v == "" || len(v) > 6 {
		return 0
	}
	for _, c := range v {
		if c < '0' || c > '9' {
			return 0
		}
	}
	n, err := strconv.Atoi(v)
	if err != nil || n > maxRetryAfterS {
		return 0
	}
	return n
}

// --- Der Strom eines Laufs ----------------------------------------------------------

// etlSession ist, was der Lauf vom Strom weiß: genau ein Strom je Lauf. Jeder
// weitere Versuch, auch nach dem Ende des ersten, ist abgewiesen und macht das
// Ergebnis nie „fertig“.
type etlSession struct {
	enabled bool // Fassung 3 gilt für diesen Lauf (ab Laufbeginn festgehalten)
	cur     *etlStream
	refused map[int64]frame.ETLEnd
	extra   string // schlechtester Status außerhalb des einen Stroms
	pending bool   // ein etl_next ist in Arbeit
}

func (e *etlSession) report(scriptEnded bool) *contract.StreamReport {
	if e.cur == nil && e.extra == "" {
		return nil
	}
	var rep contract.StreamReport
	if e.cur != nil {
		rep = e.cur.report(scriptEnded)
	} else {
		rep.Status = e.extra
	}
	if e.extra != "" && rep.Status == contract.StreamFertig {
		rep.Status = e.extra
	}
	return &rep
}

func (e *etlSession) note(status string) {
	if e.extra == "" || status == contract.StreamVertrag {
		e.extra = status
	}
}

func (e *etlSession) shutdown() {
	if e.cur != nil {
		e.cur.shutdown()
	}
}

// writeSmall schreibt einen kleinen Rahmen mit Frist: Ein Skript, das nie liest,
// hält die Schleife nicht auf.
func (r *runner) writeSmall(v any) bool {
	_ = r.sc.c.SetWriteDeadline(time.Now().Add(etlWriteTimeout))
	err := frame.Write(r.sc.c, v)
	_ = r.sc.c.SetWriteDeadline(time.Time{})
	return err == nil
}

// etlOpen nimmt ein etl_open an. Es antwortet nicht selbst: Auf jedes etl_next
// folgt genau ein Rahmen, auch bei einem abgewiesenen Öffnen.
func (r *runner) etlOpen(ctx context.Context, payload []byte) {
	var head struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(payload, &head)
	if r.etl.cur != nil {
		if r.etl.refused == nil {
			r.etl.refused = map[int64]frame.ETLEnd{}
		}
		if len(r.etl.refused) < maxRefusedOpens {
			r.etl.refused[head.ID] = frame.ETLEnd{Type: frame.TypeETLEnd, ID: head.ID, Status: frame.ETLStatusAbgewiesen}
		}
		r.etl.note(contract.StreamAbgewiesen)
		return
	}
	s := &etlStream{id: head.ID, client: r.m.etl, token: r.gw.token}
	s.ctx, s.cancel = context.WithCancel(ctx)
	r.etl.cur = s
	status, wire, body := r.checkOpen(payload)
	if status != "" {
		s.final = status
		s.endedLocked(wire, 0, 0)
		return
	}
	s.body = body
}

// checkOpen prüft ein etl_open lokal, vor jedem Netzaufruf. Reihenfolge: Fassung,
// Manifest, Größe, Form.
func (r *runner) checkOpen(payload []byte) (status, wire string, body []byte) {
	switch {
	case !r.etl.enabled:
		return contract.StreamVertrag, frame.ETLStatusVertrag, nil
	case !slices.Contains(r.pin.manifest.Tools, etlScope):
		return contract.StreamAbgewiesen, frame.ETLStatusAbgewiesen, nil
	case len(payload) > frame.MaxETLOpen:
		return contract.StreamGrenze, frame.ETLStatusGrenze, nil
	}
	var o frame.ETLOpen
	if json.Unmarshal(payload, &o) != nil || o.Integration == "" || o.QueryID == "" ||
		len(o.Integration) > maxQueryName || len(o.QueryID) > maxQueryName || !objectOrAbsent(o.Params) {
		return contract.StreamVertrag, frame.ETLStatusVertrag, nil
	}
	body, err := json.Marshal(struct {
		Integration string          `json:"integration"`
		QueryID     string          `json:"query_id"`
		Params      json.RawMessage `json:"params,omitempty"`
	}{o.Integration, o.QueryID, o.Params})
	if err != nil {
		return contract.StreamVertrag, frame.ETLStatusVertrag, nil
	}
	return "", "", body
}

func objectOrAbsent(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || string(t) == "null" || t[0] == '{'
}

// etlNext beantwortet ein etl_next. Die Antwort kommt aus einem eigenen Lauf,
// damit Wandzeit, verlorene Sandbox und Abbruch der Schleife nicht warten.
func (r *runner) etlNext(payload []byte) (lost bool) {
	var n frame.ETLNext
	if json.Unmarshal(payload, &n) != nil {
		r.etl.note(contract.StreamVertrag)
		return !r.writeSmall(frame.ETLEnd{Type: frame.TypeETLEnd, Status: frame.ETLStatusVertrag})
	}
	s := r.etl.cur
	if s == nil || s.id != n.ID {
		end, known := r.etl.refused[n.ID]
		if !known {
			r.etl.note(contract.StreamVertrag)
			end = frame.ETLEnd{Type: frame.TypeETLEnd, ID: n.ID, Status: frame.ETLStatusVertrag}
		}
		return !r.writeSmall(end)
	}
	switch {
	case n.Close:
		return !r.writeSmall(s.closeByScript())
	case r.etl.pending:
		// Zwei offene Anforderungen: Das Skript hält das Pull-Modell nicht ein.
		return !r.writeSmall(s.violation())
	}
	r.etl.pending = true
	go func() {
		var err error
		if f := s.next(); f != nil {
			err = frame.Write(r.sc.c, f)
		}
		r.etlDone <- err
	}()
	return false
}
