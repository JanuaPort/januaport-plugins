package manifest

import (
	"bytes"
	"encoding/json"
	"io"
	"path"

	"github.com/januaport/januaport-plugins/script-runner/internal/gitref"
	"gopkg.in/yaml.v3"
)

const DesktopFileName = "jnpt-desktop.yaml"

type Desktop struct {
	Manifest `yaml:",inline"`
	Python   string `yaml:"python"`
}

// yaml.v3 otherwise coerces booleans to strings and fractional numbers to
// ints. The desktop wire contract requires text and whole seconds.
type desktopText string

func (s *desktopText) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode || n.Tag != "!!str" {
		return invalid("Desktop-Textfeld ungültig")
	}
	*s = desktopText(n.Value)
	return nil
}

type desktopSeconds int

func (s *desktopSeconds) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode || n.Tag != "!!int" {
		return invalid("Desktop-Zeitfeld ungültig")
	}
	var seconds int
	if n.Decode(&seconds) != nil {
		return invalid("Desktop-Zeitfeld ungültig")
	}
	*s = desktopSeconds(seconds)
	return nil
}

// ParseDesktop validates metadata only. Runtime, lock and wheel preparation
// belong to the device; this reader never invokes internal/libs.
func ParseDesktop(b []byte, name string) (Desktop, error) {
	var raw struct {
		Name         desktopText    `yaml:"name"`
		Description  desktopText    `yaml:"description"`
		Entry        desktopText    `yaml:"entry"`
		InputSchema  map[string]any `yaml:"input_schema"`
		Tools        []desktopText  `yaml:"tools"`
		TimeoutS     desktopSeconds `yaml:"timeout_s"`
		Requirements yaml.Node      `yaml:"requirements"`
		Python       desktopText    `yaml:"python"`
	}
	d := yaml.NewDecoder(bytes.NewReader(b))
	d.KnownFields(true)
	if d.Decode(&raw) != nil || d.Decode(new(any)) != io.EOF {
		return Desktop{}, invalid("kein gültiges Desktop-YAML")
	}
	m := Desktop{Manifest: Manifest{Name: string(raw.Name), Description: string(raw.Description), Entry: string(raw.Entry), InputSchema: raw.InputSchema, TimeoutS: int(raw.TimeoutS), Requirements: raw.Requirements}, Python: string(raw.Python)}
	if raw.Tools != nil {
		m.Tools = make([]string, len(raw.Tools))
		for i, t := range raw.Tools {
			m.Tools[i] = string(t)
		}
	}
	if err := checkWithTimeout(m.Manifest, name, 86400); err != nil {
		return Desktop{}, err
	}
	if m.Tools == nil || !gitref.DesktopPath(m.Entry) || !gitref.DesktopPath(m.Python) || path.Ext(m.Python) != ".zip" {
		return Desktop{}, invalid("Desktop-Pflichtfeld oder Pfad ungültig")
	}
	if _, err := json.Marshal(m.InputSchema); err != nil {
		return Desktop{}, invalid("input_schema ist kein JSON-Schema")
	}
	return m, nil
}
