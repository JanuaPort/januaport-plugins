package fensterio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/fenster"
)

// AppAPI ist der Ausschnitt des HTTP-Adapters, den das Gateway braucht.
type AppAPI interface {
	AppToken(ctx context.Context, name string) (client.AppSession, error)
	AppRPC(ctx context.Context, s client.AppSession, id int64, method string, params any) (json.RawMessage, error)
}

// Gateway ruft tools/list und tools/call für genau eine App. Das App-Token wird
// bei Bedarf geholt, lebt nur hier im RAM und wird bei 401 einmal erneuert. Es
// verlässt das Objekt nie; Fehler sind ausschließlich feste fenster-Codes.
type Gateway struct {
	api AppAPI
	app string
	seq atomic.Int64

	mu      sync.Mutex
	session client.AppSession
	gen     uint64
}

func NewGateway(api AppAPI, app string) *Gateway { return &Gateway{api: api, app: app} }

// Format verhindert, dass ein Druck des Gateways (auch %+v/%#v) den Zustand zeigt.
func (g *Gateway) Format(f fmt.State, _ rune) { _, _ = fmt.Fprint(f, "Gateway(***)") }

func (g *Gateway) Werkzeuge(ctx context.Context) (json.RawMessage, error) {
	return g.call(ctx, "tools/list", nil)
}

func (g *Gateway) Werkzeug(ctx context.Context, name string, arguments json.RawMessage) (json.RawMessage, error) {
	return g.call(ctx, "tools/call", struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}{name, arguments})
}

func (g *Gateway) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	for attempt := 0; ; attempt++ {
		s, gen, err := g.token(ctx)
		if err != nil {
			return nil, abbilden(err)
		}
		res, err := g.api.AppRPC(ctx, s, g.seq.Add(1), method, params)
		if err == nil {
			return res, nil
		}
		if attempt == 0 && unauthorized(err) {
			g.verwerfen(gen)
			continue
		}
		return nil, abbilden(err)
	}
}

// token liefert die RAM-Sitzung und holt sie bei Bedarf unter derselben Sperre,
// damit parallele Aufrufe höchstens ein Token anfordern.
func (g *Gateway) token(ctx context.Context) (client.AppSession, uint64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.session.IsZero() {
		s, err := g.api.AppToken(ctx, g.app)
		if err != nil {
			return client.AppSession{}, 0, err
		}
		g.session = s
		g.gen++
	}
	return g.session, g.gen, nil
}

// verwerfen löscht die Sitzung nur, wenn niemand sie inzwischen erneuert hat.
func (g *Gateway) verwerfen(gen uint64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.gen == gen {
		g.session = client.AppSession{}
	}
}

func unauthorized(err error) bool {
	var he *client.HTTPError
	return errors.As(err, &he) && he.Status == 401 && he.Class == "unauthorized"
}

// abbilden macht aus jedem Adapterfehler einen festen Code, nie einen Text.
func abbilden(err error) error {
	var he *client.HTTPError
	var rpc *client.RPCError
	switch {
	case errors.Is(err, client.ErrRevoked):
		return fenster.ErrNichtAngemeldet
	case errors.As(err, &he):
		switch {
		case he.Status == 401:
			return fenster.ErrNichtAngemeldet
		case he.Status == 429:
			return fenster.ErrZuViele
		case he.Status == 413:
			return fenster.ErrZuGross
		case he.Status == 404:
			return fenster.ErrAbgelehnt
		case he.Status >= 500:
			return fenster.ErrNichtErreichbar
		}
		return fenster.ErrFehler
	case errors.As(err, &rpc):
		return fenster.ErrAbgelehnt
	case client.IsOffline(err), errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return fenster.ErrNichtErreichbar
	}
	return fenster.ErrFehler
}
