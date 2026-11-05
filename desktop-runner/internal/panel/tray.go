package panel

// TrayUpdate bringt das Symbol in den Zustand der Anzeige und liefert, ob es danach sicher existiert.
// tray ist nur die Annahme des Bedienfelds: Nach Autostart oder Explorer-Neustart kann das Symbol schon
// da sein (NIM_ADD scheitert) oder fehlen (NIM_MODIFY scheitert). Deshalb versucht die Entscheidung zuerst
// die zur Annahme passende Operation und nur nach deren Scheitern die andere.
// attempt(true) ist NIM_MODIFY, attempt(false) NIM_ADD und meldet den Erfolg.
func TrayUpdate(tray bool, attempt func(modify bool) bool) bool {
	if attempt(tray) {
		return true
	}
	return attempt(!tray)
}
