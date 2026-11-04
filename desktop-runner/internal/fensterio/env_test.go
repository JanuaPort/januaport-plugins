package fensterio

import (
	"reflect"
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

// SEC Lauf 5: Jede gesetzte WEBVIEW2_*-Umgebungsvariable gilt als Eingriff (Browserordner, Argumente, Datenordner
// ...); das Fenster öffnet dann nicht. Nur Namen, nie Werte.
func TestWebView2Namen(t *testing.T) {
	got := webview2Names([]string{
		"PATH=/bin",
		"WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS=--x",
		"webview2_user_data_folder=/x",
		"WEBVIEW2=1",
		"MY_WEBVIEW2_X=1",
		"WEBVIEW2_RELEASE_CHANNEL_PREFERENCE=1",
		"=C:=C:/Users",
	})
	want := []string{"WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", "webview2_user_data_folder", "WEBVIEW2_RELEASE_CHANNEL_PREFERENCE"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v, erwartet %v", got, want)
	}
}
