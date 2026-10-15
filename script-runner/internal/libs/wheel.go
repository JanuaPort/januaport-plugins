package libs

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
)

// MaxGlibcMinor ist die höchste glibc-Fassung 2.x, die ein manylinux-Tag
// verlangen darf: die glibc des Sandbox-Images, gemessen in P0 (Debian 13,
// glibc 2.41; script-runner/CLAUDE.md „Messung #1041 P0“). Ein Wechsel des
// Basis-Images braucht eine neue Messung; die Docker-Sonde
// TestLibsGlibcCeilingMatchesImage vergleicht die Konstante mit dem Image.
const MaxGlibcMinor = 41

// Wheel ist ein Wheel-Dateiname (PEP 427), geprüft. Name ist nach PEP 503
// normalisiert.
type Wheel struct {
	File    string
	Name    string
	Version string
}

var (
	wheelCharsRe = regexp.MustCompile(`^[A-Za-z0-9._+!-]+$`)
	wheelNameRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._]*$`)
	wheelVerRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+!]*$`)
	wheelBuildRe = regexp.MustCompile(`^[0-9][A-Za-z0-9_.]*$`)
	wheelTagRe   = regexp.MustCompile(`^[A-Za-z0-9_]+(?:\.[A-Za-z0-9_]+)*$`)

	pyTagRe      = regexp.MustCompile(`^py3(?:0|[1-9]|1[0-2])?$`) // py3, py30 … py312
	cpAbi3Re     = regexp.MustCompile(`^cp3(?:[2-9]|1[0-2])$`)    // cp32 … cp312 (abi3)
	manylinuxRe  = regexp.MustCompile(`^manylinux_([0-9]+)_([0-9]+)_x86_64$`)
	manylinuxOld = map[string]bool{"manylinux1_x86_64": true, "manylinux2010_x86_64": true, "manylinux2014_x86_64": true}
)

// ParseWheelName prüft den Dateinamen eines Wheels und seine Tags. Zulässig
// sind genau: cp312 mit ABI cp312, abi3 oder none sowie py3/py3N mit ABI none,
// jeweils auf manylinux x86_64 (bis zur glibc des Images) oder `any` (nur ABI
// none). Wie bei pip genügt ein zulässiges Tag eines komprimierten Satzes.
// Andere Dateien (sdist, egg, Zip) sind libs_invalid, andere Tags
// libs_platform.
func ParseWheelName(file string) (Wheel, error) {
	stem, ok := strings.CutSuffix(file, ".whl")
	if !ok || file == "" || len(file) > 255 || !wheelCharsRe.MatchString(stem) {
		return Wheel{}, invalid("Datei ist kein Wheel")
	}
	parts := strings.Split(stem, "-")
	if len(parts) != 5 && len(parts) != 6 {
		return Wheel{}, invalid("Wheel-Name hat nicht die Form name-version-python-abi-plattform")
	}
	if !wheelNameRe.MatchString(parts[0]) || !wheelVerRe.MatchString(parts[1]) {
		return Wheel{}, invalid("Name oder Version des Wheels unzulässig")
	}
	if len(parts) == 6 && !wheelBuildRe.MatchString(parts[2]) {
		return Wheel{}, invalid("Build-Tag des Wheels unzulässig")
	}
	tags := parts[len(parts)-3:]
	for _, t := range tags {
		if !wheelTagRe.MatchString(t) {
			return Wheel{}, invalid("Tag des Wheels unzulässig")
		}
	}
	if !anyTagAllowed(strings.Split(tags[0], "."), strings.Split(tags[1], "."), strings.Split(tags[2], ".")) {
		return Wheel{}, &Error{Reason: contract.ReasonLibsPlatform, Msg: "Wheel-Tags passen nicht zu Python 3.12 auf manylinux x86_64"}
	}
	return Wheel{File: file, Name: normalizeName(parts[0]), Version: parts[1]}, nil
}

func anyTagAllowed(pys, abis, plats []string) bool {
	for _, py := range pys {
		for _, abi := range abis {
			for _, plat := range plats {
				if tagAllowed(py, abi, plat) {
					return true
				}
			}
		}
	}
	return false
}

// tagAllowed ist die feste Menge aus dem Plan: cp312/abi3/py3/none ×
// manylinux*_x86_64/any.
func tagAllowed(py, abi, plat string) bool {
	if plat == "any" {
		return abi == "none" && (py == "cp312" || pyTagRe.MatchString(py))
	}
	if !manylinuxAllowed(plat) {
		return false
	}
	switch abi {
	case "cp312":
		return py == "cp312"
	case "abi3":
		return cpAbi3Re.MatchString(py)
	case "none":
		return py == "cp312" || pyTagRe.MatchString(py)
	}
	return false
}

func manylinuxAllowed(plat string) bool {
	if manylinuxOld[plat] {
		return true
	}
	m := manylinuxRe.FindStringSubmatch(plat)
	if m == nil || m[1] != "2" || (len(m[2]) > 1 && m[2][0] == '0') {
		return false
	}
	minor, err := strconv.Atoi(m[2])
	return err == nil && minor <= MaxGlibcMinor
}
