package fenster

// Setting ist eine boolesche WebView-Einstellung (ICoreWebView2Settings).
type Setting struct {
	Name string
	An   bool
}

// Settings ist die Sollliste, die der WebView2-Adapter vollständig anwendet.
// Nur Skripte und Webnachrichten sind an; Entwicklerwerkzeuge, Kontextmenü,
// Dialoge, Hostobjekte, Autofill, Zoom und Wischgesten sind aus.
func Settings() []Setting {
	return []Setting{
		{"AreBrowserAcceleratorKeysEnabled", false},
		{"AreDefaultContextMenusEnabled", false},
		{"AreDefaultScriptDialogsEnabled", false},
		{"AreDevToolsEnabled", false},
		{"AreHostObjectsAllowed", false},
		{"IsBuiltInErrorPageEnabled", false},
		{"IsGeneralAutofillEnabled", false},
		{"IsPasswordAutosaveEnabled", false},
		{"IsPinchZoomEnabled", false},
		{"IsScriptEnabled", true},
		{"IsStatusBarEnabled", false},
		{"IsSwipeNavigationEnabled", false},
		{"IsWebMessageEnabled", true},
		{"IsZoomControlEnabled", false},
	}
}
