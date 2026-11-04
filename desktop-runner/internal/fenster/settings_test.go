package fenster

import (
	"reflect"
	"testing"
)

func TestSettingsSollliste(t *testing.T) {
	want := []Setting{
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
	if got := Settings(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Settings weichen ab:\n%v\n%v", got, want)
	}
}

func TestSettingsNurSkriptUndNachrichtenAn(t *testing.T) {
	an := map[string]bool{}
	for _, s := range Settings() {
		if s.An {
			an[s.Name] = true
		}
	}
	if !reflect.DeepEqual(an, map[string]bool{"IsScriptEnabled": true, "IsWebMessageEnabled": true}) {
		t.Fatal("zusätzlich aktivierte Einstellung", an)
	}
}

func TestSettingsIstKopie(t *testing.T) {
	s := Settings()
	s[0].An = true
	if Settings()[0].An {
		t.Fatal("Settings teilt internen Zustand")
	}
}
