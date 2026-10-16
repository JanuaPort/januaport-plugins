// Package manifest liest jnpt-script.yaml, das Manifest jedes Skriptordners.
// Die Felder sind in script-runner/CLAUDE.md festgeschrieben (Vertrag §10).
package manifest

import (
	"bytes"
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/gitref"
)

// FileName ist der feste Name des Manifests im Skriptordner.
const FileName = "jnpt-script.yaml"

// RequirementsFile ist der feste Name des Locks im Skriptordner (#1041). Das
// optionale Manifest-Feld `requirements` darf nur diesen Wert tragen; die
// Wheels liegen in `wheels/` daneben.
const RequirementsFile = "requirements.lock"

// MaxTimeoutS bleibt in P3 die Manifest-Obergrenze (Pflichtfeld 1..25).
// Längere/optionale Manifestzeiten gehören zu P4/P5 (#1040, SEC L6).
const MaxTimeoutS = 25

// Manifest beschreibt ein Skript.
type Manifest struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Entry       string         `yaml:"entry"`
	InputSchema map[string]any `yaml:"input_schema"`
	Tools       []string       `yaml:"tools"`
	TimeoutS    int            `yaml:"timeout_s"`
	// Requirements ist der Knoten des optionalen Felds; Kind 0 heißt: nicht
	// vorhanden. Ein Knoten statt eines Strings, damit ein leeres oder null-Feld
	// von einem fehlenden unterscheidbar bleibt.
	Requirements yaml.Node `yaml:"requirements"`
}

// Invalid ist ein abgewiesenes Manifest mit Grund aus Vertrag §2.
type Invalid struct {
	Reason string
	Msg    string
}

func (e *Invalid) Error() string { return "manifest: " + e.Msg }

var toolRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,127}$`)

// ValidToolName: ein qualifizierter Werkzeugname, wie ihn das Gateway auf
// /mcp zeigt (Kleinbuchstaben, Ziffern, `_`, `-`). `script_*` und `admin_*`
// sind nie zulässig (keine Rekursion, keine Admin-Fläche). Ob das Werkzeug
// existiert, prüft nur das Gateway (Vertrag §2c).
func ValidToolName(s string) bool {
	return toolRe.MatchString(s) && !strings.HasPrefix(s, "script_") && !strings.HasPrefix(s, "admin_")
}

// Parse liest und prüft ein Manifest. pinName ist der Name aus pins.json;
// das Manifest muss denselben Namen tragen.
func Parse(data []byte, pinName string) (Manifest, error) {
	var m Manifest
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, invalid("kein gültiges YAML")
	}
	if err := check(m, pinName); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func check(m Manifest, pinName string) error {
	switch {
	case m.Name != pinName:
		return invalid("name weicht vom Pin ab")
	case m.Description == "" || utf8.RuneCountInString(m.Description) > 1024:
		return invalid("description leer oder länger als 1024 Zeichen")
	case !validEntry(m.Entry):
		return invalid("entry ist kein relativer .py-Pfad")
	case m.InputSchema == nil || m.InputSchema["type"] != "object":
		return invalid("input_schema ist kein Objekt-Schema")
	case !validRequirements(m.Requirements):
		return invalid("requirements ist nicht \"" + RequirementsFile + "\"")
	case m.TimeoutS < 1 || m.TimeoutS > MaxTimeoutS:
		return invalid(fmt.Sprintf("timeout_s nicht in 1..%d", MaxTimeoutS))
	}
	seen := map[string]bool{}
	for _, t := range m.Tools {
		if !ValidToolName(t) {
			return &Invalid{Reason: contract.ReasonToolNotQualified, Msg: "Werkzeugname nicht qualifiziert"}
		}
		if seen[t] {
			return invalid("Werkzeug doppelt deklariert")
		}
		seen[t] = true
	}
	return nil
}

func validEntry(e string) bool {
	return gitref.ValidPinPath(e) && path.Ext(e) == ".py"
}

func invalid(msg string) error {
	return &Invalid{Reason: contract.ReasonManifestInvalid, Msg: msg}
}

// UsesLibs sagt, ob das Manifest Python-Bibliotheken verlangt (#1041).
func (m Manifest) UsesLibs() bool { return m.Requirements.Kind != 0 }

// validRequirements: fehlt das Feld, ist alles wie bisher; sonst muss es
// genau die Zeichenkette requirements.lock sein.
func validRequirements(n yaml.Node) bool {
	if n.Kind == 0 {
		return true
	}
	return n.Kind == yaml.ScalarNode && n.Tag == "!!str" && n.Value == RequirementsFile
}
