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
