package client

// Canonical macht die Vertrags-Kanonisierung für Signatur-Fixturen anderer Pakete nutzbar (nur Tests).
func Canonical(b []byte) ([]byte, error) { return canonical(b) }
