package fenster

import "testing"

// Das Literal steht hier ein zweites Mal ausgeschrieben: Jede Lockerung der
// Richtlinie muss bewusst an zwei Stellen geschehen (Plan 5, exakter CSP-String).
const sollCSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; font-src 'self'; connect-src 'none'; base-uri 'none'; object-src 'none'; form-action 'none'"

func TestCSPExakt(t *testing.T) {
	if CSP != sollCSP {
		t.Fatalf("CSP weicht ab: %q", CSP)
	}
	want := `<meta http-equiv="Content-Security-Policy" content="` + sollCSP + `">`
	if CSPMeta() != want {
		t.Fatalf("Meta-Element weicht ab: %q", CSPMeta())
	}
}
