package panel

import (
	"time"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/pipe"
)

// lastRunPrefix sagt ehrlich, wie weit die Angabe reicht: Der Läufer hält sie nur im Arbeitsspeicher und
// legt nichts ab (#1398). Fällt das einmal weg, weil der Stand einen Neustart überlebt, entfällt der Zusatz.
const lastRunPrefix = "zuletzt gelaufen (seit Start): "

// LastRunText bildet die Menüzeile zum letzten Lauf eines Setups. Sie trägt nur Zeitpunkt und Status; der
// zweite Wert ist false, wenn der Läufer nichts oder etwas Unlesbares meldet (dann gibt es keine Zeile).
func LastRunText(s pipe.Setup, loc *time.Location) (string, bool) {
	switch s.Ergebnis {
	case "nie":
		return lastRunPrefix + "nie", true
	case "nicht_bereit":
		return lastRunPrefix + "nicht bereit", true
	case "ok", "fehler":
		at, err := time.Parse(time.RFC3339Nano, s.Zuletzt)
		if err != nil {
			return "", false
		}
		text := lastRunPrefix + at.In(loc).Format("02.01.2006 15:04") + " · "
		if s.Ergebnis == "ok" {
			return text + "ok", true
		}
		return text + "Fehler " + s.Code, true
	}
	return "", false
}
