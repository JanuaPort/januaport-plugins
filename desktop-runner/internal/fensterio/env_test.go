package fensterio

import (
	"reflect"
	"strings"
	"testing"
)

// B4 (SEC a): Die Browserprozesse erben die Umgebung des Bedienfelds. Alles mit Namen JNPT* wird vor dem Start
// der WebView aus der Umgebung des Bedienfelds entfernt; der Test legt fest, welche Namen das trifft.
func TestEnvNamenZumEntfernen(t *testing.T) {
	got := jnptNames([]string{
		"PATH=C:/Windows",
		"JNPT_NATIVE_WEBVIEW_PROBE=1",
		"jnpt_state=x",
		"JNPTX=1",
		"=C:=C:/Users",
		"MY_JNPT=1",
		"Jnpt",
		"HOME=/home/x",
	})
	want := []string{"JNPT_NATIVE_WEBVIEW_PROBE", "jnpt_state", "JNPTX", "Jnpt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v, erwartet %v", got, want)
	}
}

// VM-Befund Lauf 6: Der Go-Lader (webviewloader, Paket-Init) setzt WEBVIEW2_PIPE_FOR_SCRIPT_DEBUGGER,
// ..._ADDITIONAL_BROWSER_ARGUMENTS, ..._BROWSER_EXECUTABLE_FOLDER und ..._USER_DATA_FOLDER auf leer und
// ..._RELEASE_CHANNEL_PREFERENCE auf 0, damit weder Umgebung noch Registry etwas überschreiben. Das ist der
// erwartete Zustand nach dem Lader-Init und kein Eingriff. Ein Eingriff ist: irgendein WEBVIEW2_* schon beim
// Start des Prozesses (Momentaufnahme vor dem Lader-Init), ein anderer Name, oder ein abweichender Wert. Die
// Meldungen tragen nur Name und Wertlänge, nie den Wert.
func TestWebView2Eingriffe(t *testing.T) {
	neutral := []string{
		"WEBVIEW2_PIPE_FOR_SCRIPT_DEBUGGER=",
		"WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS=",
		"WEBVIEW2_RELEASE_CHANNEL_PREFERENCE=0",
		"WEBVIEW2_BROWSER_EXECUTABLE_FOLDER=",
		"webview2_user_data_folder=",
		"PATH=/bin",
	}
	for _, tc := range []struct {
		name           string
		start, current []string
		want           []string
	}{
		{"nurLaderZustand", []string{"PATH=/bin"}, neutral, nil},
		{"laufendFremderWert", []string{"PATH=/bin"}, append(append([]string(nil), neutral...), "WEBVIEW2_X=1"), []string{"WEBVIEW2_X(laenge=1,laufend)"}},
		{"argumenteGesetzt", []string{}, []string{"WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS=--geheim-wert"}, []string{"WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS(laenge=13,laufend)"}},
		{"kanalAbweichend", []string{}, []string{"WEBVIEW2_RELEASE_CHANNEL_PREFERENCE=1"}, []string{"WEBVIEW2_RELEASE_CHANNEL_PREFERENCE(laenge=1,laufend)"}},
		{"neutralerWertBeimStart", []string{"WEBVIEW2_USER_DATA_FOLDER="}, neutral, []string{"WEBVIEW2_USER_DATA_FOLDER(laenge=0,start)"}},
		{"startUndLaufend", []string{"webview2_browser_executable_folder=C:/x"}, neutral, []string{"webview2_browser_executable_folder(laenge=4,start)"}},
		{"keineWebview2Namen", []string{"MY_WEBVIEW2_X=1", "PATH=/bin"}, []string{"MY_WEBVIEW2_X=1"}, nil},
		{"webview2OhneUnterstrich", []string{"WEBVIEW2=1"}, []string{"WEBVIEW2=1"}, nil},
		{"kleinschreibungLaufend", []string{}, []string{"webview2_additional_browser_arguments=--x"}, []string{"webview2_additional_browser_arguments(laenge=3,laufend)"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := webview2Findings(tc.start, tc.current)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("%v, erwartet %v", got, tc.want)
			}
			for _, g := range got {
				if strings.Contains(g, "geheim") || strings.Contains(g, "C:/x") {
					t.Fatalf("Wert in der Meldung: %q", g)
				}
			}
		})
	}
}
