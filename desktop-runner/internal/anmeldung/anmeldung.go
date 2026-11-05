// Package anmeldung enthält nur PKCE, Rückrufprüfung und feste native Meldungen.
// Es öffnet weder Netz noch Browser und kennt keine Träger.
package anmeldung

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/fenster"
)

type Flow struct{ Verifier, Challenge, State string }

func (f Flow) Format(s fmt.State, _ rune) { _, _ = fmt.Fprint(s, "Anmeldung(***)") }
func New() (Flow, error) {
	var a, b [32]byte
	if _, e := rand.Read(a[:]); e != nil {
		return Flow{}, e
	}
	if _, e := rand.Read(b[:]); e != nil {
		return Flow{}, e
	}
	verifier := base64.RawURLEncoding.EncodeToString(a[:])
	sum := sha256.Sum256([]byte(verifier))
	return Flow{verifier, base64.RawURLEncoding.EncodeToString(sum[:]), base64.RawURLEncoding.EncodeToString(b[:])}, nil
}
func ValidState(s string) bool { return len(s) == 43 && opaque(s) && !strings.ContainsAny(s, ".~") }
func ValidChallenge(s string) bool {
	b, e := base64.RawURLEncoding.Strict().DecodeString(s)
	return e == nil && len(b) == 32
}
func opaque(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.' || r == '~') {
			return false
		}
	}
	return true
}
func ValidCode(s string) bool     { return len(s) > 0 && len(s) <= 512 && opaque(s) }
func ValidVerifier(s string) bool { return len(s) >= 43 && len(s) <= 128 && opaque(s) }

type Result struct{ Code, Fehler string }

func (r Result) Format(s fmt.State, _ rune) { _, _ = fmt.Fprint(s, "Rueckruf(***)") }

// Callback verbraucht selbst nichts. Nur ein gültiger Rückruf darf den Listener beenden.
func (f Flow) Callback(method, host, wantHost, path, query string) (Result, bool) {
	if method != "GET" || host != wantHost || path != "/jnpt/callback" || len(query) > 2048 {
		return Result{}, false
	}
	q, e := url.ParseQuery(query)
	if e != nil || len(q) != 2 || len(q["state"]) != 1 || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(f.State)) != 1 {
		return Result{}, false
	}
	if len(q["code"]) == 1 && ValidCode(q.Get("code")) {
		return Result{Code: q.Get("code")}, true
	}
	if len(q["fehler"]) == 1 {
		switch q.Get("fehler") {
		case "kein_zugriff", "platz_gesperrt", "abgebrochen":
			return Result{Fehler: q.Get("fehler")}, true
		}
	}
	return Result{}, false
}
func ValidAuthorizeURL(s string) bool { _, ok := fenster.ExternalLink(s); return ok }
func DisplayName(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		if n == 128 {
			break
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}
func Message(code string) string {
	switch code {
	case "kein_zugriff":
		return "Kein Zugriff auf diese App. Bitte die Verwaltung kontaktieren."
	case "platz_gesperrt":
		return "Der Zugang ist gesperrt. Bitte die Lizenzverwaltung kontaktieren."
	case "abgebrochen":
		return "Die Anmeldung wurde abgebrochen."
	case "zeitablauf":
		return "Die Anmeldung ist abgelaufen. Bitte erneut anmelden."
	case "angemeldet":
		return "Angemeldet."
	case "abgemeldet":
		return "Abgemeldet."
	case "wartet":
		return "Bitte die Anmeldung im Browser abschließen."
	default:
		return "Die Anmeldung ist fehlgeschlagen. Bitte erneut versuchen."
	}
}
