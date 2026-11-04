package fensterio

import (
	"errors"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/fenster"
)

// Link ist ein externer https-Link, der nur nach einem nativen Ja-Dialog mit Host im Browser öffnet.
type Link struct{ Host, URL string }

// NavVerdict ist die Entscheidung über ein Ereignis des Browsers. Cancel heißt: abbrechen, nie ausführen
// (bei NewWindowRequested: Handled=true). Link ist nur ein Vorschlag für den Dialog, nie eine Ausführung.
type NavVerdict struct {
	Cancel bool
	Link   *Link
}

// NavigationVerdict gilt für NavigationStarting (auch Redirects und meta refresh) und
// FrameNavigationStarting: Nur .html-Seiten der eigenen App laufen weiter, sonst wird vor der Anfrage abgebrochen.
func NavigationVerdict(app, uri string) NavVerdict {
	d, host := fenster.Decide(app, uri)
	switch d {
	case fenster.DecisionAllow:
		return NavVerdict{}
	case fenster.DecisionPrompt:
		return NavVerdict{Cancel: true, Link: &Link{Host: host, URL: uri}}
	}
	return NavVerdict{Cancel: true}
}

// NewWindowVerdict gilt für NewWindowRequested: Es entsteht nie ein Fenster. Ein externer https-Link
// wird als Dialogvorschlag weitergegeben; eigene Seiten öffnen nicht in einem zweiten Fenster.
func NewWindowVerdict(app, uri string) NavVerdict {
	v := NavigationVerdict(app, uri)
	v.Cancel = true
	return v
}

// DownloadVerdict gilt für DownloadStarting: Es wird nie heruntergeladen.
func DownloadVerdict() NavVerdict { return NavVerdict{Cancel: true} }

// MessageSource macht aus der vom Browser gemeldeten Quelle einer Nachricht den Absender für MessageAllowed.
// WebMessageReceived der Seite kommt nur vom obersten Rahmen (Rahmen haben eigene Ereignisse, die nicht
// abonniert sind); die URL entscheidet.
func MessageSource(url string) fenster.Source { return fenster.Source{MainFrame: true, URL: url} }

// RequiredEvents nennt die Ereignisse, die der Adapter nativ binden muss. Fehlt eine Schnittstelle,
// öffnet das Fenster nicht (RunSteps).
func RequiredEvents() []string {
	return []string{"NavigationStarting", "FrameNavigationStarting", "NewWindowRequested", "DownloadStarting", "WebMessageReceived", "PermissionRequested", "ProcessFailed"}
}

// ErrWebView ist der feste Fehler, wenn die WebView nicht vollständig und sicher einzurichten ist.
var ErrWebView = errors.New("webview_nicht_nutzbar")

// StepError benennt den fehlgeschlagenen Schritt, nie die Ursache.
type StepError struct{ Step string }

func (e *StepError) Error() string        { return ErrWebView.Error() }
func (e *StepError) Is(target error) bool { return target == ErrWebView }

// Step ist ein Einrichtungsschritt des Fensters.
type Step struct {
	Name string
	Run  func() error
}

// RunSteps führt die Schritte in Reihenfolge aus und hält beim ersten Fehler an. Es gibt keinen
// Wiederholungs- und keinen „trotzdem weiter“-Pfad: Ein Fenster ohne eine Sperre wird nicht gezeigt.
func RunSteps(steps []Step) error {
	for _, s := range steps {
		if err := s.Run(); err != nil {
			return &StepError{Step: s.Name}
		}
	}
	return nil
}
