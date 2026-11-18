package client

import (
	"slices"
	"sync"
	"time"
)

// lastRun ist der letzte Lauf eines Setups, den dieser Läuferprozess beendet hat (#1398). Nur Zeitpunkt und
// Ausgang: weder Log noch Ergebnis noch Eingabe. Der Stand liegt im Arbeitsspeicher und überlebt keinen
// Neustart; dafür gibt es bewusst kein neues Persistenzformat (das Journal verliert ein Ergebnis mit der Zustellung
// und kennt keinen Pin-Namen).
type lastRun struct {
	At     time.Time
	End    string
	Detail string
}

type lastRuns struct {
	mu sync.Mutex
	m  map[string]lastRun
}

func lastRunKey(pin, setup string) string { return pin + "/" + setup }

// note merkt sich den Ausgang eines Setup-Laufs. Ein unlesbarer Zeitpunkt macht aus dem Lauf keinen Nicht-Lauf.
func (l *lastRuns) note(pin, setup string, r Result) {
	at, err := time.Parse(time.RFC3339, r.FinishedAt)
	if err != nil {
		at = time.Now()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[string]lastRun{}
	}
	l.m[lastRunKey(pin, setup)] = lastRun{At: at.UTC(), End: r.End, Detail: r.Detail}
}

func (l *lastRuns) get(pin, setup string) *lastRun {
	l.mu.Lock()
	defer l.mu.Unlock()
	if r, ok := l.m[lastRunKey(pin, setup)]; ok {
		return &r
	}
	return nil
}

var (
	runEnds     = []string{"error", "cancelled", "internal", "interrupted", "limit", "timeout"}
	refuseCause = []string{"hash", "secret", "runtime", "account", "idle", "manifest", "offline"}
)

// describeLastRun bildet den lokalen Stand auf die vier Anzeigen der Pipe ab: ok, fehler mit festem Code,
// nicht_bereit (der Pin hat keine Umgebung, #1391) und nie. Nicht bereit gilt jetzt und schlägt einen älteren
// Lauf. Der Code stammt aus geschlossenen Listen; was der Läufer sonst an Text trägt, verlässt ihn nicht.
func describeLastRun(rec *lastRun, prepared bool) (zuletzt, ergebnis, code string) {
	if !prepared {
		return "", "nicht_bereit", ""
	}
	if rec == nil {
		return "", "nie", ""
	}
	zuletzt = rec.At.UTC().Format(time.RFC3339)
	switch {
	case rec.End == "ok":
		return zuletzt, "ok", ""
	case rec.End == "refused":
		if slices.Contains(refuseCause, rec.Detail) {
			return zuletzt, "fehler", "refused/" + rec.Detail
		}
		return zuletzt, "fehler", "refused"
	case slices.Contains(runEnds, rec.End):
		return zuletzt, "fehler", rec.End
	}
	return zuletzt, "fehler", "internal"
}
