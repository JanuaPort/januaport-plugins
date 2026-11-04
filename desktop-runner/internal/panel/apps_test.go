package panel

import "testing"

// K6b-2: Das Modell kennt nur Appnamen. Es hält weder Pfad noch Token noch Netz.
func TestPanelAppMenue(t *testing.T) {
	m := ready(t, "arbeitet")
	if len(m.View().Menu) == 0 {
		t.Fatal("Menü leer")
	}
	for _, i := range m.View().Menu {
		if len(i.ID) > 4 && i.ID[:4] == "app:" {
			t.Fatal("App-Eintrag ohne Apps", i)
		}
	}
	m.SetApps([]string{"kasse", "lager_2"})
	first := item(t, m, "app:kasse")
	if first.Text != "App öffnen: kasse" || !first.Enabled {
		t.Fatal(first)
	}
	if got := item(t, m, "app:lager_2"); got.Text != "App öffnen: lager_2" {
		t.Fatal(got)
	}
	// Die Einträge stehen vor „Beenden“, nach den Setups.
	menu := m.View().Menu
	if menu[len(menu)-1].ID != "exit" {
		t.Fatal("exit nicht zuletzt")
	}
	e := m.Choose("app:kasse", now)
	if e.OpenApp != "kasse" || len(e.Commands) != 0 || e.Quit || e.Reconnect {
		t.Fatalf("Effekt: %+v", e)
	}
}

func TestPanelAppNurBekannteNamen(t *testing.T) {
	m := ready(t, "arbeitet")
	m.SetApps([]string{"kasse"})
	for _, id := range []string{"app:", "app:unbekannt", "app:Kasse", "app:../x", "kasse", "app:kasse ", "setup:kasse"} {
		if e := m.Choose(id, now); e.OpenApp != "" || len(e.Commands) != 0 {
			t.Errorf("%q löste %+v aus", id, e)
		}
	}
	// Ungültige Namen werden nie Menüeintrag.
	m.SetApps([]string{"ok", "Böse", "a-b", "", "x y", "../z"})
	for _, i := range m.View().Menu {
		if len(i.ID) > 4 && i.ID[:4] == "app:" && i.ID != "app:ok" {
			t.Errorf("ungültiger Name im Menü: %q", i.ID)
		}
	}
	// Ersetzen statt Anhängen; Doppelte nur einmal.
	m.SetApps([]string{"a", "a", "b"})
	n := 0
	for _, i := range m.View().Menu {
		if len(i.ID) > 4 && i.ID[:4] == "app:" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("%d App-Einträge", n)
	}
}

// Eine Störung des Läufers sperrt das Öffnen nicht; Halt und Pipe bleiben davon unberührt.
func TestPanelAppUnabhaengigVonStoerung(t *testing.T) {
	m := New()
	m.SetApps([]string{"kasse"})
	if !item(t, m, "app:kasse").Enabled {
		t.Fatal("Störung sperrt das Öffnen der App")
	}
	if e := m.Choose("app:kasse", now); e.OpenApp != "kasse" {
		t.Fatal(e)
	}
	// Anhalten bleibt in der Störung gesperrt wie bisher.
	if item(t, m, "anhalten").Enabled {
		t.Fatal("Anhalten trotz Störung")
	}
}
