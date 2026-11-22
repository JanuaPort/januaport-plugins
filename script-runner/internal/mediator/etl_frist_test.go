package mediator

import "testing"

// Ein ETL-Strom darf beliebig lange laufen; die Wandzeit des Laufs begrenzt ihn,
// nicht der HTTP-Client. Eine Gesamtfrist am Client schnitte lange Ströme ab wie
// der 30-s-Fehler im Apphost-Proxy (JanuaPort/januaport#1424).
func TestETLClientOhneGesamtfrist(t *testing.T) {
	c := newETLClient("http://gateway.invalid")
	if c.http.Timeout != 0 {
		t.Fatalf("http.Client.Timeout = %v, erwartet 0 (keine Gesamtfrist am Strom)", c.http.Timeout)
	}
}
