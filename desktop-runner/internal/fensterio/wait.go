package fensterio

import (
	"errors"
	"time"
)

// startFrist begrenzt den Start des WebView (Umgebung und Controller). Danach öffnet das Fenster nicht.
const startFrist = 20 * time.Second

// ErrStartFrist: Der Start hat innerhalb der Frist nicht geantwortet.
var ErrStartFrist = errors.New("webview_start_frist")

// ErrBeendet: Während des Starts wurde das Programm beendet (WM_QUIT).
var ErrBeendet = errors.New("beendet")

// WaitFor wartet auf poll, verteilt dazwischen Nachrichten (step) und endet spätestens nach timeout mit
// ErrStartFrist. Ein Fehler von poll beendet sofort. Es wird immer mindestens einmal gefragt. So blockiert
// der UI-Thread nie ohne Grenze, auch wenn WebView2 nie antwortet (B1).
func WaitFor(timeout time.Duration, step func(), poll func() (bool, error)) error {
	deadline := time.Now().Add(timeout)
	for {
		ready, err := poll()
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		if !time.Now().Before(deadline) {
			return ErrStartFrist
		}
		step()
	}
}
