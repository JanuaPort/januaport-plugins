//go:build windows

package fensterio

import (
	"testing"
)

// #1537 Punkt 4, Verdrahtung: Die echte Surface-Methode UpdateApps vergisst beim Wechsel b -> a die persönliche
// Sitzung. Ohne WebView endet der Übergang früh (Quiesce scheitert, das Fenster schließt); belegt ist hier nur, dass
// der Wechsel das Gateway erreicht. Der Ablauf selbst belegt login_moduswechsel_test.go.
func moduswechselSurface(t *testing.T, mode string) (*Surface, *Gateway) {
	t.Helper()
	api := &fakeAppAPI{}
	g := NewGateway(api, "demo")
	g.install(personForTest(t).Session)
	h, err := NewHost(HostConfig{App: "demo", Gateway: g, Lokal: &countLokal{}})
	if err != nil {
		t.Fatal(err)
	}
	l := NewLogin(g, h, nil, nil)
	t.Cleanup(l.Close)
	o := &Opened{Name: "demo", Mode: mode, ManifestSeq: 1, Host: h, login: l}
	return &Surface{o: o}, g
}

func TestUpdateAppsBNachAVergisstDiePersoenlicheSitzung(t *testing.T) {
	for _, from := range []string{"b", ""} {
		t.Run("von "+from, func(t *testing.T) {
			s, g := moduswechselSurface(t, from)
			if g.personalExpired() {
				t.Fatal("Ausgangslage: Sitzung fehlt")
			}
			s.UpdateApps(AppList{Modes: map[string]string{"demo": "a"}, ManifestSeq: 2})
			if s.o.Mode != "a" {
				t.Fatalf("Modus %q", s.o.Mode)
			}
			if !g.personalExpired() {
				t.Fatal("persönliche Sitzung überlebt den Wechsel nach a")
			}
		})
	}
}

// Kein Wechsel, kein Vergessen: Bleibt der Modus a, rührt UpdateApps die Sitzung nicht an (der Watch läuft bei jedem
// Manifestabgleich).
func TestUpdateAppsBleibtInAFasstDieSitzungNichtAn(t *testing.T) {
	s, g := moduswechselSurface(t, "a")
	s.UpdateApps(AppList{Modes: map[string]string{"demo": "a"}, ManifestSeq: 2})
	if g.personalExpired() {
		t.Fatal("UpdateApps a -> a vergaß die Sitzung")
	}
}
