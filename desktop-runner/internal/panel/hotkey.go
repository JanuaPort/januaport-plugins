package panel

import (
	"strconv"
	"time"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/pipe"
)

// Tastenkürzel für Setups (#1240 F-T). Ein Kürzel tut genau das, was der Klick auf den Setup-Eintrag im
// Infobereich-Menü tut: Es ruft Choose("setup:<pin>/<setup>") auf, also dieselbe Prüfung. Einen zweiten Weg
// zum Läufer gibt es nicht. Das Modell kennt weder Windows noch eine Uhr; Anmeldung und Tastenzustand
// kommen als Funktionen von außen.

const (
	// HotkeyWait ist die längste Zeit, die ein Druck auf das Loslassen von Strg, Alt, Shift und Win wartet.
	HotkeyWait = 2 * time.Second
	// RejectShow ist die Zeit, in der das Symbol „abgewiesen“ zeigt.
	RejectShow = 5 * time.Second
)

// Feste Codes der Abweisung (Tooltip, kein Inhalt). K8 ist nur beim Kürzel möglich.
const (
	rejectFault     = "K1"
	rejectHalted    = "K2"
	rejectPaused    = "K3"
	rejectOffline   = "K4"
	rejectBusy      = "K5"
	rejectInput     = "K6"
	rejectNoSetup   = "K7"
	rejectModifiers = "K8"
)

// Hotkey bindet eine Taste an das Setup eines Pins.
type Hotkey struct{ Key, Pin, Setup string }

// HotkeyNumber liefert die Nummer der F-Taste. Erlaubt ist nur Strg+Alt+F1 bis F12 in genau dieser Schreibweise:
// Strg+Alt ist auf deutscher Tastatur AltGr, jedes Buchstaben- oder Ziffernkürzel würde beim Tippen auslösen.
func HotkeyNumber(key string) (int, bool) {
	for n := 1; n <= 12; n++ {
		if key == "Strg+Alt+F"+strconv.Itoa(n) {
			return n, true
		}
	}
	return 0, false
}

type registration int

const (
	unregistered registration = iota
	registered
	failed
)

type hotkeyEntry struct {
	Hotkey
	state registration
}

type pendingPress struct {
	Hotkey
	deadline time.Time
}

type hotkeys struct {
	entries []hotkeyEntry
	osIDs   []int
	probe   func() bool
	pending *pendingPress

	now         time.Time
	rejectUntil time.Time
	rejectCode  string
	rejectText  string
	beep        bool
}

// SetModifierProbe setzt die Abfrage „ist Strg, Alt, Shift oder Win gedrückt“. Fehlt sie, gilt alles als gedrückt:
// Lieber abweisen als blind in das Fachprogramm tippen.
func (m *Model) SetModifierProbe(down func() bool) { m.hk.probe = down }

func (m *Model) modifiersDown() bool { return m.hk.probe == nil || m.hk.probe() }

// SetHotkeys ersetzt die Liste der Kürzel. Einträge außerhalb der Erlaubnisliste oder des Namensmusters werden
// verworfen (SEC S2): Aus einem Manifest entsteht so nie ein anderer Befehl als der Start eines Setups.
// Die Anmeldung beim System ist danach unbekannt, bis SyncHotkeys sie neu vornimmt.
func (m *Model) SetHotkeys(list []Hotkey) {
	m.hk.entries = m.hk.entries[:0:0]
	for _, h := range list {
		if _, ok := HotkeyNumber(h.Key); ok && appName.MatchString(h.Pin) && appName.MatchString(h.Setup) {
			m.hk.entries = append(m.hk.entries, hotkeyEntry{Hotkey: h})
		}
	}
	if p := m.hk.pending; p != nil && !m.hasHotkey(p.Hotkey) {
		m.hk.pending = nil
	}
}

func (m *Model) hasHotkey(h Hotkey) bool {
	for _, e := range m.hk.entries {
		if e.Hotkey == h {
			return true
		}
	}
	return false
}

// Hotkeys liefert die gültigen Kürzel in der Reihenfolge ihrer Kennung: Die Kennung i+1 gehört zum Eintrag i.
func (m *Model) Hotkeys() []Hotkey {
	out := make([]Hotkey, len(m.hk.entries))
	for i, e := range m.hk.entries {
		out[i] = e.Hotkey
	}
	return out
}

// HotkeyRegistered meldet das Ergebnis der Anmeldung beim System. Ein Fehler (zum Beispiel 1409) macht das Kürzel
// zum sichtbaren Menühinweis „belegt“; es löst dann nichts aus.
func (m *Model) HotkeyRegistered(i int, ok bool) {
	if i < 0 || i >= len(m.hk.entries) {
		return
	}
	m.hk.entries[i].state = failed
	if ok {
		m.hk.entries[i].state = registered
	}
}

// SyncHotkeys meldet bei jedem Abgleich alle Kürzel ab und danach alle gültigen neu an. Entzug und Änderung laufen
// damit denselben Weg; einen eigenen Entzugspfad gibt es nicht.
func (m *Model) SyncHotkeys(unregister func(id int), register func(id int, key string) error) {
	m.ReleaseHotkeys(unregister)
	for i, e := range m.hk.entries {
		id := i + 1
		err := register(id, e.Key)
		m.HotkeyRegistered(i, err == nil)
		if err == nil {
			m.hk.osIDs = append(m.hk.osIDs, id)
		}
	}
}

// ReleaseHotkeys meldet alle angemeldeten Kürzel ab (Abgleich, Beenden). Zweimal aufrufen ist harmlos.
func (m *Model) ReleaseHotkeys(unregister func(id int)) {
	for _, id := range m.hk.osIDs {
		unregister(id)
	}
	m.hk.osIDs = nil
	for i := range m.hk.entries {
		m.hk.entries[i].state = unregistered
	}
}

// PressHotkey nimmt WM_HOTKEY mit der Kennung der Anmeldung entgegen. Es wartet nicht: Der Start folgt in
// PollHotkey, sobald Strg, Alt, Shift und Win losgelassen sind. Ein zweiter Druck während des Wartens
// wird verworfen, es gibt keine Warteschlange.
func (m *Model) PressHotkey(id int, now time.Time) Effects {
	m.Tick(now)
	if m.hk.pending != nil || id < 1 || id > len(m.hk.entries) || m.hk.entries[id-1].state != registered {
		return Effects{}
	}
	m.hk.pending = &pendingPress{Hotkey: m.hk.entries[id-1].Hotkey, deadline: now.Add(HotkeyWait)}
	return Effects{}
}

// PollHotkey läuft im Zeitgeber der Oberfläche. Es startet das wartende Setup, sobald keine Umschalttaste mehr
// gedrückt ist, über Choose, also mit derselben Prüfung wie der Menüklick. Nach HotkeyWait mit gedrückten
// Tasten wird abgewiesen und nie nachträglich gestartet.
func (m *Model) PollHotkey(now time.Time) Effects {
	m.Tick(now)
	p := m.hk.pending
	if p == nil {
		return Effects{}
	}
	if m.modifiersDown() {
		if now.Before(p.deadline) {
			return Effects{}
		}
		m.hk.pending = nil
		m.reject(now, rejectModifiers, "Umschalttasten noch gedrückt")
		return Effects{}
	}
	m.hk.pending = nil
	e := m.Choose("setup:"+p.Pin+"/"+p.Setup, now)
	if len(e.Commands) == 0 {
		code, text := m.rejectReason(p.Pin, p.Setup)
		m.reject(now, code, text)
	}
	return e
}

// TakeBeep meldet genau einmal je Abweisung, dass ein Systemton fällig ist. Kein Hinweis, kein Fenster.
func (m *Model) TakeBeep() bool {
	due := m.hk.beep
	m.hk.beep = false
	return due
}

func (m *Model) reject(now time.Time, code, text string) {
	m.hk.rejectUntil = now.Add(RejectShow)
	m.hk.rejectCode, m.hk.rejectText = code, text
	m.hk.beep = true
}

// rejectReason benennt nur, warum Choose nichts getan hat. Entschieden hat Choose.
func (m *Model) rejectReason(pin, setup string) (code, text string) {
	if !(m.connected && m.ready && !m.fault) {
		return rejectFault, "Störung"
	}
	var found *pipe.Setup
	for i := range m.status.Setups {
		if s := &m.status.Setups[i]; s.Pin == pin && s.Setup == setup {
			found = s
		}
	}
	switch {
	case found == nil:
		return rejectNoSetup, "Setup nicht verfügbar"
	case m.status.Zustand == "angehalten":
		return rejectHalted, "angehalten"
	case m.status.Zustand == "pausiert":
		return rejectPaused, "pausiert"
	case !m.status.Netz:
		return rejectOffline, "offline"
	case m.status.Beschaeftigt:
		return rejectBusy, "beschäftigt"
	case found.BrauchtEingabe:
		return rejectInput, "braucht Eingabe"
	}
	return rejectNoSetup, "Setup nicht startbar"
}

func (m *Model) rejecting() bool { return m.hk.now.Before(m.hk.rejectUntil) }

// belegtItems sind die Menühinweise zu Kürzeln, die das System nicht angemeldet hat.
func (m *Model) belegtItems() []Item {
	var out []Item
	seen := map[string]bool{}
	for _, e := range m.hk.entries {
		if e.state == failed && !seen[e.Key] {
			seen[e.Key] = true
			out = append(out, Item{"hotkey:" + e.Key, "Kürzel belegt: " + e.Key, false})
		}
	}
	return out
}
