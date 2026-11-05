//go:build windows

package fensterio

import (
	"testing"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/fenster"
)

// Echte Surface-Methoden ohne Fenster/GUI: Layout und der frühe Abbruch vor Win32.
func TestSurfaceAnmeldeleisteNachModus(t *testing.T) {
	for _, testDevice := range []bool{false, true} {
		for _, mode := range []string{"a", "b", ""} {
			o := &Opened{Mode: mode, TestDevice: testDevice, login: NewLogin(nil, nil, nil, nil)}
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
			if mode == "a" {
				if err := s.createLoginControls(0); err != nil || s.loginButton != 0 || s.logoutButton != 0 || s.accountLabel != 0 {
					t.Fatal("Modus a erzeugt Knopf oder Meldung", err)
				}
			}
			o.login.Close()
		}
	}
}
