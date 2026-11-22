package contract

import (
	"strconv"
	"strings"
)

// MinCoreVersion ist die erste Kern-Version, die Vertragsfassung 3 zulässt
// (JanuaPort/januaport#1215 K2): die nächste Minor nach dem Tag v0.99.0. Das
// Quality Gate bestätigt sie beim Release des Kerns; ein falscher Wert fällt
// nur zu hoch (Rückfall auf 2) oder zu niedrig (der Kern sperrt den Läufer)
// aus, deshalb ist sie ein benanntes Literal und im Gate-Bericht genannt.
const MinCoreVersion = "0.100.0"

// ForCoreVersion wählt die Vertragsfassung für einen Kern mit der Version aus
// `/healthz` (Rückfallregel, Fassung 6, E4). Fassung 3 gibt es nur bei einer
// streng gelesenen Version ab MinCoreVersion: `X.Y.Z` mit optionalem `v`,
// Dezimalzahlen ohne führende Nullen, sonst nichts (keine Vorabversion, kein
// Build-Anhang, kein Leerraum). Jede andere Angabe, auch jede unlesbare, ist
// Fassung 2: Der Rückfall nimmt nur eine Fähigkeit weg und fügt keine hinzu.
func ForCoreVersion(coreVersion string) int {
	got, ok := parseSemVer(coreVersion)
	min, _ := parseSemVer(MinCoreVersion)
	if !ok || compareSemVer(got, min) < 0 {
		return LegacyVersion
	}
	return Version
}

type semVer [3]uint32

func parseSemVer(s string) (semVer, bool) {
	s = strings.TrimPrefix(s, "v")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return semVer{}, false
	}
	var v semVer
	for i, p := range parts {
		if p == "" || len(p) > 9 || (len(p) > 1 && p[0] == '0') {
			return semVer{}, false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return semVer{}, false
			}
		}
		n, err := strconv.ParseUint(p, 10, 32)
		if err != nil {
			return semVer{}, false
		}
		v[i] = uint32(n)
	}
	return v, true
}

func compareSemVer(a, b semVer) int {
	for i := range a {
		switch {
		case a[i] < b[i]:
			return -1
		case a[i] > b[i]:
			return 1
		}
	}
	return 0
}
