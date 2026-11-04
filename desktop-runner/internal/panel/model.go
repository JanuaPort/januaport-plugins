// Package panel contains the platform-independent display and command policy.
// It knows no credential, network adapter, filesystem or Windows API.
package panel

import (
	"regexp"
	"strings"
	"time"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/pipe"
)

const FaultText = "Störung. Anhalten: abmelden oder über den Admin halten lassen."

type ConnectionState int

const (
	Disconnected ConnectionState = iota
	Connected                    // Only after pipe.Dial has successfully checked the server.
	VerificationFailed
)

type Item struct {
	ID, Text string
	Enabled  bool
}
type Dialog struct {
	ID, Title, Text string
	Options         []string
	Deadline        time.Time
	Visible         bool
}
type Display struct {
	Color, Tooltip string
	Menu           []Item
	Dialog         *Dialog
}
type Effects struct {
	Commands        []pipe.Message
	Reconnect, Quit bool
	// OpenApp nennt die App, die der Mensch im Menü gewählt hat. Das Modell kennt nur den Namen.
	OpenApp string
}
type Model struct {
	connected, checked, ready, fault bool
	status                           pipe.Message
	dialog                           *Dialog
	apps                             []string
}

func New() *Model { return &Model{fault: true} }

func RetryDelay(attempt int) time.Duration {
	d := 5 * time.Second
	for i := 0; i < attempt && d < 60*time.Second; i++ {
		d *= 2
	}
	return min(d, 60*time.Second)
}

func (m *Model) disturbance() Effects {
	m.fault = true
	m.checked = false
	m.ready = false
	m.dialog = nil
	return Effects{Reconnect: true}
}
func (m *Model) Connection(state ConnectionState) Effects {
	m.connected = state == Connected
	m.ready = false
	m.dialog = nil
	if !m.connected {
		return m.disturbance()
	}
	m.checked = true
	return Effects{Commands: []pipe.Message{{Type: "status"}}}
}

func (m *Model) Receive(raw []byte, now time.Time) Effects {
	if !m.connected {
		return Effects{}
	}
	v, err := pipe.DecodeServer(raw)
	if err != nil {
		return m.disturbance()
	}
	m.Tick(now)
	switch v.Type {
	case "zustand":
		if v.Zustand == "stoerung" {
			return m.disturbance()
		}
		m.status = v
		if m.checked {
			m.fault = false
			m.ready = true
		}
		if v.Zustand == "angehalten" {
			m.dialog = nil
		}
	case "fehler":
		if v.Code == "stoerung" || v.Code == "protokoll" {
			return m.disturbance()
		}
	case "frage":
		// A second question is not displayed. The runner also rejects it in Go.
		if m.ready && !m.fault && m.status.Zustand != "angehalten" && m.dialog == nil {
			m.dialog = &Dialog{ID: v.ID, Title: "JanuaPort – " + v.Titel, Text: v.Text, Options: append([]string(nil), v.Optionen...), Deadline: now.Add(time.Duration(v.FristS) * time.Second)}
		}
	case "frage_weg":
		if m.dialog != nil && m.dialog.ID == v.ID {
			m.dialog = nil
		}
	}
	return Effects{}
}
func (m *Model) Tick(now time.Time) Effects {
	if m.dialog != nil && !now.Before(m.dialog.Deadline) {
		m.dialog = nil
	}
	return Effects{}
}

func (m *Model) View() Display {
	v := Display{Color: "purple", Tooltip: FaultText}
	available := m.connected && m.ready && !m.fault
	if !m.fault {
		switch m.status.Zustand {
		case "arbeitet":
			v.Color = "green"
			v.Tooltip = "Arbeitet"
		case "pausiert":
			v.Color = "yellow"
			v.Tooltip = "Pausiert"
		case "angehalten":
			v.Color = "red"
			v.Tooltip = "Angehalten"
		}
		if m.status.Bis != "" {
			if at, err := time.Parse(time.RFC3339Nano, m.status.Bis); err == nil {
				v.Tooltip += " bis " + at.Local().Format("15:04")
			}
		}
		if !m.status.Netz {
			v.Tooltip += " · offline"
			if m.status.FristAbgelaufen {
				v.Tooltip += ", Frist abgelaufen"
			}
		}
		if m.dialog != nil {
			v.Tooltip += " · Rückfrage wartet"
		}
	}
	paused := m.status.Zustand == "pausiert"
	halted := m.status.Zustand == "angehalten"
	v.Menu = []Item{
		{"status", v.Tooltip, false},
		{"pause15", "Pausieren 15 min", available && !halted},
		{"pause60", "Pausieren 1 h", available && !halted},
		{"pause0", "Pausieren bis Weiter", available && !halted},
		{"weiter", "Weiter", available && paused},
		{"anhalten", "Anhalten", available && !halted},
		{"fortsetzen", "Fortsetzen", available && halted},
	}
	for _, s := range m.status.Setups {
		text := s.Label
		if s.BrauchtEingabe {
			text += " (braucht Eingabe)"
		}
		v.Menu = append(v.Menu, Item{"setup:" + s.Pin + "/" + s.Setup, text, available && s.Startbar && !s.BrauchtEingabe && !halted && !paused && m.status.Netz && !m.status.Beschaeftigt})
	}
	if m.status.Test {
		v.Menu = append(v.Menu, Item{"test", "Test", false})
	}
	if m.dialog != nil {
		d := *m.dialog
		d.Options = append([]string(nil), d.Options...)
		v.Dialog = &d
		v.Menu = append(v.Menu, Item{"frage", "Rückfrage anzeigen", available})
	}
	for _, n := range m.apps {
		v.Menu = append(v.Menu, Item{"app:" + n, "App öffnen: " + n, true})
	}
	v.Menu = append(v.Menu, Item{"exit", "Beenden des Bedienfelds", true})
	return v
}

func (m *Model) Choose(id string, now time.Time) Effects {
	m.Tick(now)
	if id == "exit" {
		return Effects{Quit: true}
	}
	if name, ok := strings.CutPrefix(id, "app:"); ok {
		for _, n := range m.apps {
			if n == name {
				return Effects{OpenApp: n}
			}
		}
		return Effects{}
	}
	allowed := false
	for _, i := range m.View().Menu {
		if i.ID == id && i.Enabled {
			allowed = true
		}
	}
	if !allowed {
		return Effects{}
	}
	var command pipe.Message
	switch id {
	case "frage":
		m.dialog.Visible = true
		return Effects{}
	case "pause15", "pause60", "pause0":
		minutes := map[string]int{"pause15": 15, "pause60": 60, "pause0": 0}[id]
		command = pipe.Message{Type: "pause", Minuten: &minutes}
	case "weiter", "anhalten", "fortsetzen":
		command = pipe.Message{Type: id}
		if id == "anhalten" {
			m.dialog = nil
		}
	default:
		for _, s := range m.status.Setups {
			if "setup:"+s.Pin+"/"+s.Setup == id {
				command = pipe.Message{Type: "setup_start", Pin: s.Pin, Setup: s.Setup}
			}
		}
	}
	if command.Type == "" {
		return Effects{}
	}
	return Effects{Commands: []pipe.Message{command}}
}
func (m *Model) Answer(choice int, now time.Time) Effects {
	m.Tick(now)
	if !m.connected || !m.ready || m.fault || m.dialog == nil || !m.dialog.Visible || choice < 0 || choice >= len(m.dialog.Options) {
		return Effects{}
	}
	id := m.dialog.ID
	m.dialog = nil
	return Effects{Commands: []pipe.Message{{Type: "antwort", ID: id, Wahl: &choice}}}
}

// Closing the window is not an answer; it can be reopened until expiry.
func (m *Model) HideDialog() {
	if m.dialog != nil {
		m.dialog.Visible = false
	}
}

var appName = regexp.MustCompile(`^[a-z0-9_]{1,48}$`)

// SetApps ersetzt die Liste der öffnenden Apps. Ungültige und doppelte Namen werden verworfen; das
// Modell hält keinen Pfad, kein Token und keinen Netzweg.
func (m *Model) SetApps(names []string) {
	m.apps = m.apps[:0:0]
	seen := map[string]bool{}
	for _, n := range names {
		if appName.MatchString(n) && !seen[n] {
			seen[n] = true
			m.apps = append(m.apps, n)
		}
	}
}
