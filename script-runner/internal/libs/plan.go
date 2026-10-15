package libs

import (
	"sort"
	"strings"
)

// PlannedWheel ist ein Wheel des Pins mit seinem Eintrag im Lock.
type PlannedWheel struct {
	File  string
	Entry Entry
}

// Plan ist ein geprüfter Lock samt der Wheel-Dateien dazu: Dateien und
// Einträge entsprechen sich genau 1:1. Die Hashes der Dateien sind damit
// noch nicht geprüft; das geschieht beim Lesen der Bytes.
type Plan struct {
	Lock   Lock
	Wheels []PlannedWheel
}

// NewPlan liest das Lock und gleicht es mit den Dateinamen aus wheels/ ab.
// Format und Plattform jedes Wheels werden vor dem Abgleich geprüft.
// Zusätzliche Wheels, Einträge ohne Wheel, zwei Wheels für einen Eintrag oder
// eine andere Version sind libs_invalid; sdists sind keine Wheels.
func NewPlan(lockData []byte, files []string) (Plan, error) {
	lock, err := ParseLock(lockData)
	if err != nil {
		return Plan{}, err
	}
	wheels := make([]Wheel, 0, len(files))
	for _, f := range files {
		w, err := ParseWheelName(f)
		if err != nil {
			return Plan{}, err
		}
		wheels = append(wheels, w)
	}
	sort.Slice(wheels, func(i, j int) bool { return wheels[i].File < wheels[j].File })
	byName := make(map[string]Entry, len(lock.Entries))
	for _, e := range lock.Entries {
		byName[e.Name] = e
	}
	used := make(map[string]bool, len(wheels))
	plan := Plan{Lock: lock}
	for _, w := range wheels {
		e, ok := byName[w.Name]
		switch {
		case !ok:
			return Plan{}, invalid("Wheel ohne Eintrag im Lock")
		case used[w.Name]:
			return Plan{}, invalid("zwei Wheels für einen Lock-Eintrag")
		case !strings.EqualFold(e.Version, w.Version):
			return Plan{}, invalid("Version des Wheels weicht vom Lock ab")
		}
		used[w.Name] = true
		plan.Wheels = append(plan.Wheels, PlannedWheel{File: w.File, Entry: e})
	}
	for _, e := range lock.Entries {
		if !used[e.Name] {
			return Plan{}, invalid("Lock-Eintrag ohne Wheel")
		}
	}
	return plan, nil
}
