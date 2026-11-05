//go:build windows

package fensterio

import (
	"context"
	"errors"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/anmeldung"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
)

// runLogin wird nur nach dem nativen Klick in einer Goroutine gerufen. Die URL
// geht ausschließlich in ShellExecuteW, nie in die WebView oder die Brücke.
func (o *Opened) runLogin(ctx context.Context) (client.PersonSession, error) {
	return loginDevice(ctx, o.api, o.Name, OpenInBrowser)
}

func loginDevice(ctx context.Context, api *client.API, app string, open func(string) error) (client.PersonSession, error) {
	ctx, cancel := context.WithTimeout(ctx, loginLifetime)
	defer cancel()
	flow, e := anmeldung.New()
	if e != nil {
		return client.PersonSession{}, e
	}
	listener, e := newLoopback(ctx, flow)
	if e != nil {
		return client.PersonSession{}, e
	}
	defer listener.Close()
	raw, e := api.AppLogin(ctx, app, flow.Challenge, listener.Port(), flow.State)
	if e != nil {
		return client.PersonSession{}, e
	}
	if ctx.Err() != nil {
		return client.PersonSession{}, ctx.Err()
	}
	if e = open(raw); e != nil {
		return client.PersonSession{}, e
	}
	select {
	case r := <-listener.result:
		if r.Fehler != "" {
			return client.PersonSession{}, loginError(r.Fehler)
		}
		person, err := api.AppSession(ctx, app, r.Code, flow.Verifier)
		var he *client.HTTPError
		// Der Code kann während der Bestätigung ablaufen. Nur diese Antwort
		// aus /session umdeuten; Rücksprungfehler behalten ihren Zugriffstext.
		if errors.As(err, &he) && he.Status == 403 && he.Class == "kein_zugriff" {
			return client.PersonSession{}, loginError("zeitablauf")
		}
		return person, err
	case <-ctx.Done():
		return client.PersonSession{}, ctx.Err()
	}
}
