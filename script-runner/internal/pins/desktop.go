package pins

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
	"github.com/januaport/januaport-plugins/script-runner/internal/gitref"
)

// DesktopFile is independent of pins.json: older readers safely ignore it.
type DesktopFile struct {
	RepoURL string
	Pins    []DesktopPin
}

type DesktopPin struct {
	Pin
	TargetDevice string
	Algo         string
}

var deviceID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var errDesktopFile = errors.New("pins: Desktop-Datei ungültig")

func ParseDesktop(b []byte) (DesktopFile, error) {
	var raw struct {
		Version int    `json:"version"`
		RepoURL string `json:"repo_url"`
		Pins    []struct {
			Name         string `json:"name"`
			Commit       string `json:"commit"`
			Path         string `json:"path"`
			TargetDevice string `json:"target_device"`
			Algo         string `json:"algo"`
		} `json:"pins"`
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&raw) != nil || d.Decode(new(any)) != io.EOF || raw.Version != 1 || raw.Pins == nil {
		return DesktopFile{}, errDesktopFile
	}
	f := DesktopFile{RepoURL: raw.RepoURL}
	seen := map[string]int{}
	for _, r := range raw.Pins {
		p := DesktopPin{Pin: Pin{Name: r.Name, Commit: r.Commit, Path: r.Path}, TargetDevice: r.TargetDevice, Algo: r.Algo}
		switch {
		case !gitref.ValidPinName(r.Name), !gitref.ValidCommit(r.Commit), !deviceID.MatchString(r.TargetDevice),
			!(r.Algo == "sha1" && len(r.Commit) == 40 || r.Algo == "sha256" && len(r.Commit) == 64), gitref.CheckRepoURL(raw.RepoURL) != nil:
			p.Invalid = contract.ReasonManifestInvalid
		case !gitref.DesktopPath(r.Path):
			p.Invalid = contract.ReasonBadPath
		}
		seen[p.Name]++
		f.Pins = append(f.Pins, p)
	}
	for i := range f.Pins {
		if seen[f.Pins[i].Name] > 1 {
			f.Pins[i].Invalid = contract.ReasonManifestInvalid
		}
	}
	return f, nil
}
