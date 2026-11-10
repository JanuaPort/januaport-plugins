package panel

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/pipe"
)

// #1240 F-T: Ein Tastenkürzel tut genau das, was der Klick auf den Setup-Eintrag tut. Das Modell kennt weder
// Windows noch eine Uhr; der Tastenzustand kommt als Attrappe.

type keyProbe struct{ down bool }

func (k *keyProbe) Down() bool { return k.down }

var hotkeyDemo = Hotkey{Key: "Strg+Alt+F5", Pin: "demo", Setup: "ansicht"}

// hotkeyModel bringt ein Modell in den gewünschten Zustand, mit dem Setup demo/ansicht und einem Kürzel darauf.
func hotkeyModel(t *testing.T, scenario func(*pipe.Message)) (*Model, *keyProbe) {
	t.Helper()
	m := New()
	m.Connection(Connected)
	state := pipe.Message{Type: "zustand", Zustand: "arbeitet", Netz: true, Setups: []pipe.Setup{{Pin: "demo", Setup: "ansicht", Label: "Ansicht", Startbar: true}}}
	if scenario != nil {
		scenario(&state)
	}
	if state.Zustand != "" {
		receive(t, m, state)
	}
	keys := &keyProbe{}
	m.SetModifierProbe(keys.Down)
	m.SetHotkeys([]Hotkey{hotkeyDemo})
	m.HotkeyRegistered(0, true)
	return m, keys
}

// press führt einen Druck aus, lässt die Umschalttasten los und tickt einmal.
func press(m *Model, keys *keyProbe) Effects {
	m.PressHotkey(1, now)
	keys.down = false
	return m.PollHotkey(now.Add(200 * time.Millisecond))
}

// Das Kernversprechen: Taste und Klick sind für jeden Zustand dasselbe Ergebnis. Das Modell hat nur den Weg über
// Choose; die wahrscheinlichste Fehlbauart wäre ein Tastenweg, der die Freigabe selbst prüft oder gar nicht.
func TestTasteGleichKlick(t *testing.T) {
	tests := []struct {
		name     string
		scenario func(*pipe.Message)
		fault    bool
		start    bool
		code     string
	}{
		{name: "online", start: true},
		{name: "offline", scenario: func(m *pipe.Message) { m.Netz = false }, code: "K4"},
		{name: "beschäftigt", scenario: func(m *pipe.Message) { m.Beschaeftigt = true }, code: "K5"},
		{name: "pausiert", scenario: func(m *pipe.Message) { m.Zustand = "pausiert" }, code: "K3"},
		{name: "angehalten", scenario: func(m *pipe.Message) { m.Zustand = "angehalten" }, code: "K2"},
		{name: "braucht Eingabe", scenario: func(m *pipe.Message) { m.Setups[0].BrauchtEingabe = true }, code: "K6"},
		{name: "Setup nicht startbar", scenario: func(m *pipe.Message) { m.Setups[0].Startbar = false }, code: "K7"},
		{name: "Setup nicht im Manifest", scenario: func(m *pipe.Message) { m.Setups = nil }, code: "K7"},
		{name: "anderes Setup im Manifest", scenario: func(m *pipe.Message) { m.Setups[0].Setup = "andere" }, code: "K7"},
		{name: "Störung", scenario: func(m *pipe.Message) { m.Zustand = "stoerung" }, fault: true, code: "K1"},
		{name: "noch kein Zustand", scenario: func(m *pipe.Message) { m.Zustand = "" }, fault: true, code: "K1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			click, _ := hotkeyModel(t, tt.scenario)
			want := click.Choose("setup:demo/ansicht", now)
			byKey, keys := hotkeyModel(t, tt.scenario)
			got := press(byKey, keys)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Taste %+v, Klick %+v", got, want)
			}
			if tt.start {
				if len(got.Commands) != 1 || got.Commands[0].Type != "setup_start" || got.Commands[0].Pin != "demo" || got.Commands[0].Setup != "ansicht" {
					t.Fatalf("erlaubter Fall startet nicht: %+v", got)
				}
				if byKey.TakeBeep() {
					t.Fatal("Ton ohne Abweisung")
				}
				return
			}
			if len(got.Commands) != 0 || got.Quit || got.OpenApp != "" || got.Reconnect {
				t.Fatalf("abgewiesener Fall wirkt: %+v", got)
			}
			if !byKey.TakeBeep() {
				t.Fatal("abgewiesen ohne Ton")
			}
			// Eine Störung bleibt violett mit ihrem festen Text (§6.6), die Abweisung überdeckt sie nicht.
			if tooltip := byKey.View().Tooltip; !tt.fault && (!strings.Contains(tooltip, "("+tt.code+")") || !strings.Contains(tooltip, "abgewiesen")) {
				t.Fatalf("Tooltip ohne festen Code %s: %q", tt.code, tooltip)
			}
		})
	}
}

// Die Abweisung ist stumm für den Rest des Systems: kein Hinweis, kein Dialog, kein Menüeintrag, kein Befehl.
func TestAbweisungOhneSprechblaseOderFenster(t *testing.T) {
	m, keys := hotkeyModel(t, func(s *pipe.Message) { s.Beschaeftigt = true })
	menuBefore := m.View().Menu
	press(m, keys)
	if m.TakeNotice() {
		t.Fatal("Abweisung löst die Sprechblase aus")
	}
	if v := m.View(); v.Dialog != nil || !reflect.DeepEqual(v.Menu, menuBefore) {
		t.Fatalf("Abweisung verändert Dialog oder Menü: %+v", v)
	}
}

func TestAbweisungSymbolFuenfSekundenUndEinTon(t *testing.T) {
	m, keys := hotkeyModel(t, func(s *pipe.Message) { s.Beschaeftigt = true })
	normal := m.View()
	m.PressHotkey(1, now)
	keys.down = false
	m.PollHotkey(now.Add(200 * time.Millisecond))
	v := m.View()
	if v.Color == normal.Color || v.Color != "orange" || !strings.Contains(v.Tooltip, "Kürzel abgewiesen (K5)") {
		t.Fatalf("Symbol zeigt keine Abweisung: %+v", v)
	}
	if !m.TakeBeep() || m.TakeBeep() {
		t.Fatal("Ton muss genau einmal je Abweisung kommen")
	}
	m.Tick(now.Add(200*time.Millisecond + RejectShow - time.Millisecond))
	if m.View().Color != "orange" {
		t.Fatal("Symbol zu früh zurück")
	}
	m.Tick(now.Add(200*time.Millisecond + RejectShow))
	if got := m.View(); got.Color != normal.Color || got.Tooltip != normal.Tooltip {
		t.Fatalf("Symbol bleibt nach 5 s: %+v", got)
	}
}

// Eine Störung bleibt violett; die Abweisung überdeckt sie nicht (§6.6).
func TestAbweisungUeberdecktStoerungNicht(t *testing.T) {
	m, keys := hotkeyModel(t, func(s *pipe.Message) { s.Zustand = "stoerung" })
	press(m, keys)
	if v := m.View(); v.Color != "purple" || v.Tooltip != FaultText {
		t.Fatalf("Störung überdeckt: %+v", v)
	}
	if !m.TakeBeep() {
		t.Fatal("trotzdem ein Ton")
	}
}

// S3: Warten über den Zeitgeber, ein zweiter Druck währenddessen wird verworfen und nicht vorgemerkt.
func TestZweiterDruckWaehrendDesWartensIstVerworfen(t *testing.T) {
	m, keys := hotkeyModel(t, nil)
	keys.down = true
	if e := m.PressHotkey(1, now); len(e.Commands) != 0 {
		t.Fatalf("Druck startet sofort: %+v", e)
	}
	if e := m.PressHotkey(1, now.Add(300*time.Millisecond)); len(e.Commands) != 0 {
		t.Fatalf("zweiter Druck wirkt: %+v", e)
	}
	if e := m.PollHotkey(now.Add(500 * time.Millisecond)); len(e.Commands) != 0 {
		t.Fatal("Start bei gedrückten Tasten", e)
	}
	keys.down = false
	starts := 0
	for i := 1; i <= 10; i++ {
		starts += len(m.PollHotkey(now.Add(time.Second + time.Duration(i)*200*time.Millisecond)).Commands)
	}
	if starts != 1 {
		t.Fatalf("%d setup_start statt genau einem, es gibt keine Warteschlange", starts)
	}
}

// Nach dem Start (oder der Abweisung) ist der nächste Druck wieder ein neuer Druck.
func TestDruckNachAbschlussWirdWiederAngenommen(t *testing.T) {
	m, keys := hotkeyModel(t, nil)
	if len(press(m, keys).Commands) != 1 {
		t.Fatal("erster Druck")
	}
	keys.down = true
	m.PressHotkey(1, now.Add(time.Minute))
	keys.down = false
	if e := m.PollHotkey(now.Add(time.Minute + 200*time.Millisecond)); len(e.Commands) != 1 {
		t.Fatalf("zweiter Druck nach dem ersten: %+v", e)
	}
}

// Das Warten ist ein Zeitgeber, kein Blockieren: Der Druck kehrt sofort zurück, der Start kommt erst, wenn
// Strg, Alt, Shift und Win losgelassen sind.
func TestStartErstNachLoslassenDerUmschalttasten(t *testing.T) {
	m, keys := hotkeyModel(t, nil)
	keys.down = true
	m.PressHotkey(1, now)
	for i := 1; i <= 8; i++ {
		if e := m.PollHotkey(now.Add(time.Duration(i) * 200 * time.Millisecond)); len(e.Commands) != 0 {
			t.Fatalf("Start nach %d ms bei gedrückten Tasten", i*200)
		}
	}
	keys.down = false
	if e := m.PollHotkey(now.Add(1900 * time.Millisecond)); len(e.Commands) != 1 || e.Commands[0].Type != "setup_start" {
		t.Fatalf("kein Start nach dem Loslassen: %+v", e)
	}
	if m.TakeBeep() {
		t.Fatal("Ton beim Start")
	}
}

func TestUmschalttastenNachZweiSekundenWeisenAb(t *testing.T) {
	m, keys := hotkeyModel(t, nil)
	keys.down = true
	m.PressHotkey(1, now)
	if e := m.PollHotkey(now.Add(HotkeyWait - time.Millisecond)); len(e.Commands) != 0 || m.TakeBeep() {
		t.Fatal("zu früh abgewiesen")
	}
	if e := m.PollHotkey(now.Add(HotkeyWait)); len(e.Commands) != 0 || e.Quit || e.OpenApp != "" {
		t.Fatalf("Abweisung wirkt: %+v", e)
	}
	if !m.TakeBeep() || !strings.Contains(m.View().Tooltip, "(K8)") || m.View().Color != "orange" {
		t.Fatalf("keine Abweisung K8: %+v", m.View())
	}
	// Auch später gelöst: Der abgewiesene Druck startet nie nachträglich.
	keys.down = false
	for i := 0; i < 5; i++ {
		if e := m.PollHotkey(now.Add(HotkeyWait + time.Duration(i+1)*200*time.Millisecond)); len(e.Commands) != 0 {
			t.Fatalf("abgewiesener Druck startet später: %+v", e)
		}
	}
}

// Fehlt der Tastenzustand, gilt er als gedrückt: lieber abweisen als blind tippen.
func TestOhneTastenzustandWirdAbgewiesen(t *testing.T) {
	m, _ := hotkeyModel(t, nil)
	m.SetModifierProbe(nil)
	m.PressHotkey(1, now)
	if e := m.PollHotkey(now.Add(200 * time.Millisecond)); len(e.Commands) != 0 {
		t.Fatalf("Start ohne Tastenzustand: %+v", e)
	}
	if e := m.PollHotkey(now.Add(HotkeyWait)); len(e.Commands) != 0 || !m.TakeBeep() {
		t.Fatalf("keine Abweisung: %+v", e)
	}
}

// PollHotkey ohne Druck tut nichts.
func TestPollOhneDruckTutNichts(t *testing.T) {
	m, _ := hotkeyModel(t, nil)
	for i := 0; i < 20; i++ {
		if e := m.PollHotkey(now.Add(time.Duration(i) * time.Second)); !reflect.DeepEqual(e, Effects{}) || m.TakeBeep() {
			t.Fatalf("Effekt ohne Druck: %+v", e)
		}
	}
}

func TestUnbekannteKennungIstStumm(t *testing.T) {
	m, keys := hotkeyModel(t, nil)
	for _, id := range []int{-1, 0, 2, 99} {
		m.PressHotkey(id, now)
		keys.down = false
		if e := m.PollHotkey(now.Add(200 * time.Millisecond)); len(e.Commands) != 0 || m.TakeBeep() {
			t.Fatalf("Kennung %d wirkt: %+v", id, e)
		}
	}
}

// Fällt die App weg, wird das Kürzel frei: Es wird nicht mehr angemeldet und löst nichts mehr aus.
func TestAppWegfallGibtKuerzelFrei(t *testing.T) {
	m, keys := hotkeyModel(t, nil)
	other := Hotkey{Key: "Strg+Alt+F6", Pin: "demo", Setup: "ansicht"}
	m.SetHotkeys([]Hotkey{hotkeyDemo, other})
	if got := m.Hotkeys(); len(got) != 2 {
		t.Fatalf("Kürzel: %v", got)
	}
	m.SetHotkeys([]Hotkey{other})
	if got := m.Hotkeys(); len(got) != 1 || got[0] != other {
		t.Fatalf("entzogenes Kürzel bleibt angemeldet: %v", got)
	}
	m.HotkeyRegistered(0, true)
	// Die Kennung 2 gehörte zum entzogenen Kürzel.
	m.PressHotkey(2, now)
	keys.down = false
	if e := m.PollHotkey(now.Add(200 * time.Millisecond)); len(e.Commands) != 0 {
		t.Fatalf("entzogenes Kürzel startet: %+v", e)
	}
	m.SetHotkeys(nil)
	if len(m.Hotkeys()) != 0 {
		t.Fatal("Kürzel ohne Apps")
	}
}

// Ein wartender Druck auf ein inzwischen entzogenes Kürzel startet nichts.
func TestWartenderDruckUeberlebtEntzugNicht(t *testing.T) {
	m, keys := hotkeyModel(t, nil)
	keys.down = true
	m.PressHotkey(1, now)
	m.SetHotkeys(nil)
	keys.down = false
	if e := m.PollHotkey(now.Add(200 * time.Millisecond)); len(e.Commands) != 0 {
		t.Fatalf("entzogenes Kürzel startet nach dem Warten: %+v", e)
	}
}

// Ein Abgleich ohne Änderung nimmt einen wartenden Druck nicht weg.
func TestWartenderDruckUeberlebtUnveraenderteListe(t *testing.T) {
	m, keys := hotkeyModel(t, nil)
	keys.down = true
	m.PressHotkey(1, now)
	m.SetHotkeys([]Hotkey{hotkeyDemo})
	m.HotkeyRegistered(0, true)
	keys.down = false
	if e := m.PollHotkey(now.Add(200 * time.Millisecond)); len(e.Commands) != 1 {
		t.Fatalf("Druck beim Abgleich verloren: %+v", e)
	}
}

// Schlägt die Anmeldung fehl (zum Beispiel 1409), steht das Kürzel sichtbar als „belegt“ im Menü. Nie still.
func TestAnmeldefehlerZeigtBelegtImMenue(t *testing.T) {
	m, _ := hotkeyModel(t, nil)
	if _, ok := findItem(m, "hotkey:Strg+Alt+F5"); ok {
		t.Fatal("belegt ohne Fehler")
	}
	m.HotkeyRegistered(0, false)
	it, ok := findItem(m, "hotkey:Strg+Alt+F5")
	if !ok || it.Enabled || !strings.Contains(it.Text, "belegt") || !strings.Contains(it.Text, "Strg+Alt+F5") {
		t.Fatalf("Hinweis fehlt oder ist falsch: %+v %v", it, ok)
	}
	if e := m.Choose("hotkey:Strg+Alt+F5", now); !reflect.DeepEqual(e, Effects{}) {
		t.Fatalf("Hinweis ist klickbar: %+v", e)
	}
	// Keine Sprechblase für „belegt“.
	if m.TakeNotice() {
		t.Fatal("Sprechblase")
	}
	// Ein neuer Abgleich meldet neu; ein Erfolg nimmt den Hinweis weg.
	m.SetHotkeys([]Hotkey{hotkeyDemo})
	m.HotkeyRegistered(0, true)
	if _, ok := findItem(m, "hotkey:Strg+Alt+F5"); ok {
		t.Fatal("belegt nach erfolgreicher Anmeldung")
	}
}

// Ein Kürzel, das nicht angemeldet ist, löst auch bei einer verirrten Meldung nichts aus.
func TestBelegtesKuerzelStartetNichts(t *testing.T) {
	m, keys := hotkeyModel(t, nil)
	m.HotkeyRegistered(0, false)
	m.PressHotkey(1, now)
	keys.down = false
	if e := m.PollHotkey(now.Add(200 * time.Millisecond)); len(e.Commands) != 0 {
		t.Fatalf("belegtes Kürzel startet: %+v", e)
	}
}

func findItem(m *Model, id string) (Item, bool) {
	for _, i := range m.View().Menu {
		if i.ID == id {
			return i, true
		}
	}
	return Item{}, false
}

// S2: Pin und Setup im Namensmuster, im Bedienfeld noch einmal geprüft. Kein Manifest erzeugt so je einen
// anderen Befehl als setup_start (kein exit, keine App, keine Pause, kein Anhalten).
func TestHotkeyNamenAusserhalbDesMustersErzeugenKeinenBefehl(t *testing.T) {
	long := strings.Repeat("a", 49)
	tests := []struct {
		name string
		h    Hotkey
		ok   bool
	}{
		{"gültig", Hotkey{"Strg+Alt+F1", "demo", "ansicht"}, true},
		{"Pin Großbuchstabe", Hotkey{"Strg+Alt+F1", "Demo", "ansicht"}, false},
		{"Pin mit Schrägstrich", Hotkey{"Strg+Alt+F1", "a/b", "ansicht"}, false},
		{"Pin mit Doppelpunkt", Hotkey{"Strg+Alt+F1", "app:x", "ansicht"}, false},
		{"Pin leer", Hotkey{"Strg+Alt+F1", "", "ansicht"}, false},
		{"Pin zu lang", Hotkey{"Strg+Alt+F1", long, "ansicht"}, false},
		{"Setup mit Schrägstrich", Hotkey{"Strg+Alt+F1", "demo", "x/y"}, false},
		{"Setup mit Leerzeichen", Hotkey{"Strg+Alt+F1", "demo", "x y"}, false},
		{"Setup leer", Hotkey{"Strg+Alt+F1", "demo", ""}, false},
		{"Setup zu lang", Hotkey{"Strg+Alt+F1", "demo", long}, false},
		{"Setup mit Zeilenumbruch", Hotkey{"Strg+Alt+F1", "demo", "a\nb"}, false},
		{"Ziffernkürzel", Hotkey{"Strg+Alt+7", "demo", "ansicht"}, false},
		{"Buchstabenkürzel", Hotkey{"Strg+Alt+Q", "demo", "ansicht"}, false},
		{"F13", Hotkey{"Strg+Alt+F13", "demo", "ansicht"}, false},
		{"ohne Strg", Hotkey{"Alt+F1", "demo", "ansicht"}, false},
		{"leere Taste", Hotkey{"", "demo", "ansicht"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, keys := hotkeyModel(t, nil)
			m.SetHotkeys([]Hotkey{tt.h})
			if got := m.Hotkeys(); (len(got) == 1) != tt.ok {
				t.Fatalf("Hotkeys() = %v, erwartet gültig=%v", got, tt.ok)
			}
			m.HotkeyRegistered(0, true)
			m.PressHotkey(1, now)
			keys.down = false
			e := m.PollHotkey(now.Add(200 * time.Millisecond))
			if !tt.ok {
				if !reflect.DeepEqual(e, Effects{}) {
					t.Fatalf("ungültiger Eintrag wirkt: %+v", e)
				}
				return
			}
			if e.Quit || e.OpenApp != "" || e.Reconnect {
				t.Fatalf("Kürzel wirkt über setup_start hinaus: %+v", e)
			}
			for _, c := range e.Commands {
				if c.Type != "setup_start" {
					t.Fatalf("Kürzel erzeugt %q", c.Type)
				}
			}
		})
	}
}

// Auch ein gültiger Name, der wie eine Menü-Kennung klingt, bleibt ein Setup des Pins.
func TestHotkeyNameWieMenueKennungBleibtSetup(t *testing.T) {
	for _, h := range []Hotkey{{"Strg+Alt+F1", "pause0", "exit"}, {"Strg+Alt+F1", "anhalten", "fortsetzen"}, {"Strg+Alt+F1", "app", "kasse"}} {
		m, keys := hotkeyModel(t, func(s *pipe.Message) {
			s.Setups = []pipe.Setup{{Pin: h.Pin, Setup: h.Setup, Label: "x", Startbar: true}}
		})
		m.SetHotkeys([]Hotkey{h})
		m.HotkeyRegistered(0, true)
		m.PressHotkey(1, now)
		keys.down = false
		e := m.PollHotkey(now.Add(200 * time.Millisecond))
		if len(e.Commands) != 1 || e.Commands[0].Type != "setup_start" || e.Commands[0].Pin != h.Pin || e.Commands[0].Setup != h.Setup || e.Quit || e.OpenApp != "" {
			t.Fatalf("%+v: %+v", h, e)
		}
	}
}

// SEC D1: Dieselbe Taste in zwei Apps desselben Geräts sperrt beide. Es gewinnt nicht die App, die zuerst angemeldet
// wird: Sonst startete der gewohnte Griff das Setup einer neu zugewiesenen App. Keine der beiden wird angemeldet,
// das Menü nennt die Taste „gesperrt (doppelt)“, nie still.
func TestDoppeltesKuerzelAnZweiAppsWirdWieFremdesBehandelt(t *testing.T) {
	m, keys := hotkeyModel(t, func(s *pipe.Message) {
		s.Setups = []pipe.Setup{{Pin: "demo", Setup: "ansicht", Label: "A", Startbar: true}, {Pin: "zwei", Setup: "ansicht", Label: "B", Startbar: true}}
	})
	second := Hotkey{Key: "Strg+Alt+F5", Pin: "zwei", Setup: "ansicht"}
	other := Hotkey{Key: "Strg+Alt+F6", Pin: "demo", Setup: "ansicht"}
	sys := &fakeOS{taken: map[string]bool{}, holding: map[int]string{}}
	m.SetHotkeys([]Hotkey{hotkeyDemo, second, other})
	m.SyncHotkeys(sys.unregister, sys.register)
	if !reflect.DeepEqual(sys.calls, []string{"an3"}) || !reflect.DeepEqual(sys.holding, map[int]string{3: "Strg+Alt+F6"}) {
		t.Fatalf("doppelte Taste wurde angemeldet: %v %v", sys.calls, sys.holding)
	}
	it, ok := findItem(m, "hotkey:Strg+Alt+F5")
	if !ok || it.Enabled || !strings.Contains(it.Text, "gesperrt (doppelt)") || !strings.Contains(it.Text, "Strg+Alt+F5") {
		t.Fatalf("Hinweis fehlt oder ist falsch: %+v %v", it, ok)
	}
	if n := countItems(m, "hotkey:"); n != 1 {
		t.Fatalf("%d Hinweise statt einem", n)
	}
	// Auch eine verirrte Erfolgsmeldung macht keine der beiden startbar.
	m.HotkeyRegistered(0, true)
	m.HotkeyRegistered(1, true)
	for _, id := range []int{1, 2} {
		m.PressHotkey(id, now)
		keys.down = false
		if e := m.PollHotkey(now.Add(200 * time.Millisecond)); len(e.Commands) != 0 {
			t.Fatalf("Kennung %d startet trotz doppelter Taste: %+v", id, e)
		}
	}
	// Die unbeteiligte Taste bleibt nutzbar.
	m.PressHotkey(3, now.Add(time.Second))
	if e := m.PollHotkey(now.Add(time.Second + 200*time.Millisecond)); len(e.Commands) != 1 {
		t.Fatalf("unbeteiligte Taste: %+v", e)
	}
}

// Gegenfall zu D1: Wird eine der beiden Apps entzogen, meldet sich die verbliebene beim nächsten Abgleich an.
func TestDoppeltesKuerzelNachEntzugEinerAppWirdAngemeldet(t *testing.T) {
	m, keys := hotkeyModel(t, func(s *pipe.Message) {
		s.Setups = []pipe.Setup{{Pin: "demo", Setup: "ansicht", Label: "A", Startbar: true}, {Pin: "zwei", Setup: "ansicht", Label: "B", Startbar: true}}
	})
	second := Hotkey{Key: "Strg+Alt+F5", Pin: "zwei", Setup: "ansicht"}
	sys := &fakeOS{taken: map[string]bool{}, holding: map[int]string{}}
	m.SetHotkeys([]Hotkey{hotkeyDemo, second})
	m.SyncHotkeys(sys.unregister, sys.register)
	if len(sys.holding) != 0 {
		t.Fatalf("angemeldet trotz Doppel: %v", sys.holding)
	}
	// Die erste App wird entzogen.
	sys.calls = nil
	m.SetHotkeys([]Hotkey{second})
	m.SyncHotkeys(sys.unregister, sys.register)
	if !reflect.DeepEqual(sys.holding, map[int]string{1: "Strg+Alt+F5"}) {
		t.Fatalf("verbliebene App nicht angemeldet: %v", sys.holding)
	}
	if _, ok := findItem(m, "hotkey:Strg+Alt+F5"); ok {
		t.Fatal("Hinweis bleibt nach dem Entzug")
	}
	m.PressHotkey(1, now)
	keys.down = false
	if e := m.PollHotkey(now.Add(200 * time.Millisecond)); len(e.Commands) != 1 || e.Commands[0].Pin != "zwei" {
		t.Fatalf("verbliebenes Kürzel: %+v", e)
	}
}

func countItems(m *Model, prefix string) int {
	n := 0
	for _, i := range m.View().Menu {
		if strings.HasPrefix(i.ID, prefix) {
			n++
		}
	}
	return n
}

// Das Kernversprechen gilt auch, wenn die Taste ein Setup meint, dessen Klick der Mensch gerade nicht sieht.
func TestHotkeyKeinKlickWegGebraucht(t *testing.T) {
	m, keys := hotkeyModel(t, nil)
	press(m, keys)
	if m.View().Dialog != nil {
		t.Fatal("Dialog durch Kürzel")
	}
}

// Die Nummer der F-Taste kommt aus der Erlaubnisliste; der Windows-Adapter rechnet daraus den Tastencode.
func TestHotkeyNumber(t *testing.T) {
	for i := 1; i <= 12; i++ {
		n, ok := HotkeyNumber(fmt.Sprintf("Strg+Alt+F%d", i))
		if !ok || n != i {
			t.Errorf("F%d: %d %v", i, n, ok)
		}
	}
	for _, bad := range []string{"", "Strg+Alt+F0", "Strg+Alt+F13", "Strg+Alt+F01", "Strg+Alt+7", "Strg+Alt+Q", "Alt+F1", "Strg+Shift+F1", "strg+alt+f1", " Strg+Alt+F1", "Strg+Alt+F1 "} {
		if n, ok := HotkeyNumber(bad); ok || n != 0 {
			t.Errorf("%q akzeptiert: %d", bad, n)
		}
	}
}

type fakeOS struct {
	calls   []string
	taken   map[string]bool
	holding map[int]string
}

func (f *fakeOS) unregister(id int) {
	f.calls = append(f.calls, fmt.Sprintf("ab%d", id))
	delete(f.holding, id)
}
func (f *fakeOS) register(id int, key string) error {
	f.calls = append(f.calls, fmt.Sprintf("an%d", id))
	if f.taken[key] {
		return errHotkeyTaken
	}
	f.holding[id] = key
	return nil
}

var errHotkeyTaken = &testError{"1409"}

type testError struct{ s string }

func (e *testError) Error() string { return e.s }

// Bei jedem Abgleich werden alle Kürzel ab- und neu angemeldet; Entzug und Änderung laufen denselben Weg.
func TestAbgleichMeldetAllesAbUndNeuAn(t *testing.T) {
	m := New()
	os := &fakeOS{taken: map[string]bool{}, holding: map[int]string{}}
	f6 := Hotkey{Key: "Strg+Alt+F6", Pin: "demo", Setup: "ansicht"}
	m.SetHotkeys([]Hotkey{hotkeyDemo, f6})
	m.SyncHotkeys(os.unregister, os.register)
	if !reflect.DeepEqual(os.calls, []string{"an1", "an2"}) {
		t.Fatalf("erster Abgleich: %v", os.calls)
	}
	os.calls = nil
	m.SetHotkeys([]Hotkey{hotkeyDemo, f6})
	m.SyncHotkeys(os.unregister, os.register)
	if !reflect.DeepEqual(os.calls, []string{"ab1", "ab2", "an1", "an2"}) {
		t.Fatalf("zweiter Abgleich ohne Änderung muss trotzdem alles ab- und neu anmelden: %v", os.calls)
	}
	// Entzug der App: nichts bleibt hängen, nur das Übrige wird angemeldet.
	os.calls = nil
	m.SetHotkeys([]Hotkey{f6})
	m.SyncHotkeys(os.unregister, os.register)
	if !reflect.DeepEqual(os.calls, []string{"ab1", "ab2", "an1"}) {
		t.Fatalf("Abgleich nach Entzug: %v", os.calls)
	}
	if !reflect.DeepEqual(os.holding, map[int]string{1: "Strg+Alt+F6"}) {
		t.Fatalf("im System angemeldet: %v", os.holding)
	}
	m.SetHotkeys(nil)
	m.SyncHotkeys(os.unregister, os.register)
	if len(os.holding) != 0 {
		t.Fatalf("Kürzel ohne App bleiben angemeldet: %v", os.holding)
	}
}

// Beim Beenden gibt das Bedienfeld alle Kürzel frei.
func TestFreigebenBeimBeenden(t *testing.T) {
	m := New()
	os := &fakeOS{taken: map[string]bool{}, holding: map[int]string{}}
	m.SetHotkeys([]Hotkey{hotkeyDemo, {Key: "Strg+Alt+F6", Pin: "demo", Setup: "ansicht"}})
	m.SyncHotkeys(os.unregister, os.register)
	m.ReleaseHotkeys(os.unregister)
	if len(os.holding) != 0 {
		t.Fatalf("Kürzel nach dem Beenden: %v", os.holding)
	}
	m.ReleaseHotkeys(os.unregister) // zweimal ist harmlos
}

// Ein Anmeldefehler des Systems wird dem Modell gemeldet und erscheint als „belegt“; die anderen Kürzel bleiben.
func TestAbgleichMeldetAnmeldefehlerAnsModell(t *testing.T) {
	m := New()
	os := &fakeOS{taken: map[string]bool{"Strg+Alt+F5": true}, holding: map[int]string{}}
	m.SetHotkeys([]Hotkey{hotkeyDemo, {Key: "Strg+Alt+F6", Pin: "demo", Setup: "ansicht"}})
	m.SyncHotkeys(os.unregister, os.register)
	if _, ok := findItem(m, "hotkey:Strg+Alt+F5"); !ok {
		t.Fatal("Fehler der Anmeldung nicht sichtbar")
	}
	if _, ok := findItem(m, "hotkey:Strg+Alt+F6"); ok {
		t.Fatal("angemeldetes Kürzel als belegt")
	}
	// Das Kürzel wird später frei: Der nächste Abgleich meldet erneut an und nimmt den Hinweis weg.
	os.taken = map[string]bool{}
	m.SetHotkeys([]Hotkey{hotkeyDemo, {Key: "Strg+Alt+F6", Pin: "demo", Setup: "ansicht"}})
	m.SyncHotkeys(os.unregister, os.register)
	if _, ok := findItem(m, "hotkey:Strg+Alt+F5"); ok {
		t.Fatal("Hinweis bleibt nach erfolgreicher Anmeldung")
	}
}

// Der verworfene zweite Druck verlängert das Warten nicht und tauscht das Ziel nicht aus: Es gilt der erste Druck,
// mit seiner Frist (Mutant: zweiter Druck überschreibt den ersten).
func TestZweiterDruckAendertWederFristNochZiel(t *testing.T) {
	m, keys := hotkeyModel(t, func(s *pipe.Message) {
		s.Setups = []pipe.Setup{{Pin: "demo", Setup: "ansicht", Label: "A", Startbar: true}, {Pin: "demo", Setup: "drucken", Label: "B", Startbar: true}}
	})
	second := Hotkey{Key: "Strg+Alt+F6", Pin: "demo", Setup: "drucken"}
	m.SetHotkeys([]Hotkey{hotkeyDemo, second})
	m.HotkeyRegistered(0, true)
	m.HotkeyRegistered(1, true)
	keys.down = true
	m.PressHotkey(1, now)
	m.PressHotkey(2, now.Add(1500*time.Millisecond))
	// Die Frist des ersten Drucks läuft nach zwei Sekunden ab, nicht 2 s nach dem zweiten Druck.
	if e := m.PollHotkey(now.Add(HotkeyWait)); len(e.Commands) != 0 || !m.TakeBeep() {
		t.Fatalf("Frist durch den zweiten Druck verlängert: %+v", e)
	}
	// Löst der Mensch rechtzeitig, startet das Setup des ersten Drucks, nie das des zweiten.
	m2, keys2 := hotkeyModel(t, func(s *pipe.Message) {
		s.Setups = []pipe.Setup{{Pin: "demo", Setup: "ansicht", Label: "A", Startbar: true}, {Pin: "demo", Setup: "drucken", Label: "B", Startbar: true}}
	})
	m2.SetHotkeys([]Hotkey{hotkeyDemo, second})
	m2.HotkeyRegistered(0, true)
	m2.HotkeyRegistered(1, true)
	keys2.down = true
	m2.PressHotkey(1, now)
	m2.PressHotkey(2, now.Add(300*time.Millisecond))
	keys2.down = false
	e := m2.PollHotkey(now.Add(600 * time.Millisecond))
	if len(e.Commands) != 1 || e.Commands[0].Setup != "ansicht" {
		t.Fatalf("zweiter Druck hat das Ziel getauscht: %+v", e)
	}
}
