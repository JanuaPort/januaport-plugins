//go:build windows

package fensterio

import (
	"testing"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/fenster"
)

// Echte Surface-Methode ohne Fenster/GUI; auch unter dem Mutanten nur Layout.
func TestSurfaceAnmeldeleisteNachModus(t *testing.T) {
	for _, testDevice := range []bool{false, true} {
		for _, mode := range []string{"a", "b", ""} {
			o := &Opened{Mode: mode, TestDevice: testDevice, login: &Login{}}
			s := &Surface{o: o}
			for _, h := range []int{0, 20, 700} {
				view, bar := s.bounds(1000, h)
				want, wantBar := fenster.Layout(1000, h, testDevice)
				if mode != "a" {
					n := min(loginBarHeight, want.H)
					want.Y += n
					want.H -= n
				}
				if view != want || bar != wantBar {
					t.Errorf("mode=%q test=%v h=%d: %v/%v, erwartet %v/%v", mode, testDevice, h, view, bar, want, wantBar)
				}
			}
		}
	}
}

func TestSurfaceModusAErzeugtKeineControls(t *testing.T) {
	s := &Surface{o: &Opened{Mode: "a", login: &Login{}}}
	if err := s.createLoginControls(0); err != nil || s.loginButton != 0 || s.logoutButton != 0 || s.accountLabel != 0 {
		t.Fatal("Modus a erzeugt Knopf oder Meldung", err)
	}
}

func TestSurfaceAnzeigemodusVerwirftAltenStand(t *testing.T) {
	o := &Opened{Name: "demo", Mode: "b", ManifestSeq: 5, login: &Login{}}
	s := &Surface{o: o}
	s.UpdateApps(AppList{Modes: map[string]string{"demo": "a"}, ManifestSeq: 4})
	if o.Mode != "b" || o.ManifestSeq != 5 {
		t.Fatal("älterer Watch-Stand überschreibt das asynchrone Öffnen")
	}
}
