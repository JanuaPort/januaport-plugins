//go:build windows

package fensterio

import "github.com/januaport/januaport-plugins/desktop-runner/internal/platform"

// NewWindowsRuntime verdrahtet die Laufzeit mit dem Zustand des Läufers unter Windows: Wurzel unter
// %LOCALAPPDATA%, Entschlüsselung per DPAPI desselben Benutzers. Hier, hinter der Tür, weil platform den
// Client mitbringt, den das Bedienfeld selbst nicht importieren darf (SEC K6b-2 B2).
func NewWindowsRuntime(exchange Exchange, wake func()) (*Runtime, error) {
	root, err := platform.Root()
	if err != nil {
		return nil, ErrOpen
	}
	return NewRuntime(RuntimeConfig{Root: root, Cipher: platform.DPAPI{}, Exchange: exchange, Wake: wake})
}
