// Package pins liest pins.json, die der Materialisierer des Gateways in den
// Plugin-Ordner schreibt (Vertrag, Nachtrag 2a). Eine ungültige Zeile macht
// nur diesen Pin ungültig; eine falsche version macht alle ungültig.
package pins

import (
	"encoding/json"
	"fmt"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/gitref"
)

// Pin ist eine Zeile aus pins.json. Invalid trägt den Grund (Vertrag §2),
// wenn die Zeile selbst schon ungültig ist.
type Pin struct {
	Name    string
	Path    string
	Commit  string
	Invalid string
}

// File ist der gelesene Inhalt von pins.json.
type File struct {
	RepoURL string
	Pins    []Pin
}

type rawFile struct {
	Version json.RawMessage `json:"version"`
	RepoURL string          `json:"repo_url"`
	Pins    []rawPin        `json:"pins"`
}

type rawPin struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Commit string `json:"commit"`
}

// Parse liest pins.json. Ein Fehler kommt nur bei nicht lesbarem JSON; dann
// kennt der Leser keinen Pin und führt nichts aus.
func Parse(data []byte) (File, error) {
	var raw rawFile
	if err := json.Unmarshal(data, &raw); err != nil {
		return File{}, fmt.Errorf("pins: %w", err)
	}
	versionOK := string(raw.Version) == "1"
	f := File{RepoURL: raw.RepoURL, Pins: make([]Pin, 0, len(raw.Pins))}
	seen := map[string]int{}
	for _, r := range raw.Pins {
		p := Pin{Name: r.Name, Path: r.Path, Commit: r.Commit, Invalid: rowReason(r)}
		if !versionOK {
			p.Invalid = contract.ReasonManifestInvalid
		}
		seen[r.Name]++
		f.Pins = append(f.Pins, p)
	}
	for i := range f.Pins {
		if seen[f.Pins[i].Name] > 1 {
			f.Pins[i].Invalid = contract.ReasonManifestInvalid
		}
	}
	return f, nil
}

func rowReason(r rawPin) string {
	switch {
	case !gitref.ValidPinName(r.Name), !gitref.ValidCommit(r.Commit):
		return contract.ReasonManifestInvalid
	case !gitref.ValidPinPath(r.Path):
		return contract.ReasonBadPath
	}
	return ""
}
