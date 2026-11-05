package anmeldung

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPKCEUndState(t *testing.T) {
	a, err := New()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := New()
	sum := sha256.Sum256([]byte(a.Verifier))
	if a.Challenge != base64.RawURLEncoding.EncodeToString(sum[:]) || !ValidState(a.State) || len(a.Verifier) != 43 || a.State == b.State || a.Verifier == b.Verifier || a.State == a.Verifier {
		t.Fatal("PKCE/Entropie")
	}
	if strings.Contains(fmt.Sprintf("%+v %#v", a, a), a.Verifier) {
		t.Fatal("Verifier gedruckt")
	}
}

func TestRueckrufStrikt(t *testing.T) {
	f, _ := New()
	host := "127.0.0.1:12345"
	query := "state=" + f.State + "&code=synthetic-code"
	if r, ok := f.Callback("GET", host, host, "/jnpt/callback", query); !ok || r.Code != "synthetic-code" {
		t.Fatal("gültiger Code")
	}
	for _, c := range []struct{ method, host, path, query string }{
		{"POST", host, "/jnpt/callback", query}, {"GET", "localhost:12345", "/jnpt/callback", query}, {"GET", "127.0.0.1:12346", "/jnpt/callback", query}, {"GET", host, "/other", query},
		{"GET", host, "/jnpt/callback", "state=" + strings.Repeat("x", 43) + "&code=x"},
		{"GET", host, "/jnpt/callback", query + "&state=" + f.State}, {"GET", host, "/jnpt/callback", query + "&code=y"},
		{"GET", host, "/jnpt/callback", query + "&fehler=abgebrochen"}, {"GET", host, "/jnpt/callback", query + "&person=x"},
		{"GET", host, "/jnpt/callback", "state=" + f.State + "&fehler=freier-text"}, {"GET", host, "/jnpt/callback", "state=" + f.State + "&code="},
		{"GET", host, "/jnpt/callback", query + ";%xx"}, {"GET", host, "/jnpt/callback", "state=" + f.State + "&code=%0a"},
	} {
		if _, ok := f.Callback(c.method, c.host, host, c.path, c.query); ok {
			t.Errorf("zugelassen: %+v", c)
		}
	}
	for _, code := range []string{"kein_zugriff", "platz_gesperrt", "abgebrochen"} {
		r, ok := f.Callback("GET", host, host, "/jnpt/callback", "state="+f.State+"&fehler="+code)
		if !ok || r.Code != "" || r.Fehler != code || Message(code) == Message("unknown") {
			t.Fatal(code)
		}
	}
}

func TestNameUndAuthorizeURL(t *testing.T) {
	if got := DisplayName("A\n\t\x00\u202e\u2066\u200b" + strings.Repeat("ä", 200)); utf8.RuneCountInString(got) != 128 || !strings.HasPrefix(got, "Aä") {
		t.Fatal("Name nicht bereinigt")
	}
	for _, s := range []string{"http://idp.example/a", "https://user:pw@idp.example.org/a", "javascript:alert(1)", "file:///a", "https://idp.example/\n", "https://idp.example/\\a", "https:///a"} {
		if ValidAuthorizeURL(s) {
			t.Fatal(s)
		}
	}
	if !ValidAuthorizeURL("https://idp.example/authorize?a=b&state=c") {
		t.Fatal("https mit &")
	}
}
