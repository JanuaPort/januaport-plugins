package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/anmeldung"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/fenster"
)

// PersonSession bleibt im Fensterprozess. Name ist ausschließlich für die native Leiste.
type PersonSession struct {
	Session AppSession
	Name    string
	Changed bool
}

func (p PersonSession) Format(s fmt.State, _ rune) { _, _ = fmt.Fprint(s, "PersonSession(***)") }

func (a *API) AppLogin(ctx context.Context, name, challenge string, port int, state string) (string, error) {
	if !fenster.ValidAppName(name) || a.Contract() != WindowsContract || !anmeldung.ValidChallenge(challenge) || !anmeldung.ValidState(state) || port < 1024 || port > 65535 {
		return "", ErrProtocol
	}
	body := struct {
		Challenge string `json:"challenge"`
		Port      int    `json:"port"`
		State     string `json:"state"`
	}{challenge, port, state}
	b, status, e := a.request(ctx, "POST", "apps/"+name+"/login", body, 0, "", 16<<10)
	if e != nil {
		return "", e
	}
	var out struct {
		URL string `json:"authorize_url"`
	}
	if status != 200 || strict(b, &out) != nil || !anmeldung.ValidAuthorizeURL(out.URL) {
		return "", ErrProtocol
	}
	return out.URL, nil
}

func (a *API) AppSession(ctx context.Context, name, code, verifier string) (PersonSession, error) {
	if !fenster.ValidAppName(name) || a.Contract() != WindowsContract || !anmeldung.ValidCode(code) || !anmeldung.ValidVerifier(verifier) {
		return PersonSession{}, ErrProtocol
	}
	body := struct {
		Code     string `json:"code"`
		Verifier string `json:"verifier"`
	}{code, verifier}
	b, status, e := a.request(ctx, "POST", "apps/"+name+"/session", body, 0, "", 16<<10)
	if e != nil {
		return PersonSession{}, e
	}
	var out struct {
		appTokenReply
		Name    *string `json:"anzeigename"`
		Changed *bool   `json:"person_gewechselt"`
	}
	_, canonicalErr := canonical(b)
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if status != 200 || canonicalErr != nil || decoder.Decode(&out) != nil || out.Name == nil || !strings.HasPrefix(out.AppToken, appTokenPrefix) || len(out.AppToken) == len(appTokenPrefix) || strings.ContainsAny(out.AppToken, " \r\n\t") || !utc(out.ExpiresAt) || out.MCPPath != "/api/device/v1/mcp" {
		return PersonSession{}, ErrProtocol
	}
	expires, _ := time.Parse(time.RFC3339, out.ExpiresAt)
	return PersonSession{Session: AppSession{token: out.AppToken, expires: expires}, Name: anmeldung.DisplayName(*out.Name), Changed: out.Changed == nil || *out.Changed}, nil
}
