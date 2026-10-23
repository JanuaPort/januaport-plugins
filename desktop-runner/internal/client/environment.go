package client

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

//go:embed python-allowlist.json
var pythonAllowlist []byte

//go:embed bootstrap.py
var bootstrap []byte

type FileSeal struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type Environment struct {
	Origin  Pin        `json:"origin"`
	Dir     string     `json:"dir"`
	Files   []FileSeal `json:"files"`
	Entry   string     `json:"entry"`
	Timeout int        `json:"timeout"`
	Tools   []string   `json:"tools"`
}
type PackageManifest struct {
	Name         string         `yaml:"name"`
	Description  string         `yaml:"description"`
	Entry        string         `yaml:"entry"`
	InputSchema  map[string]any `yaml:"input_schema"`
	Tools        []string       `yaml:"tools"`
	Timeout      int            `yaml:"timeout_s"`
	Requirements string         `yaml:"requirements,omitempty"`
	Python       string         `yaml:"python"`
}

func sum(b []byte) string { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }
func fold(s string) string {
	return strings.Map(func(r rune) rune { return unicode.ToLower(unicode.ToUpper(r)) }, s)
}

func CheckPython(name string, b []byte) (string, error) {
	var a struct {
		Version int `json:"version"`
		Entries []struct {
			Version  string `json:"version"`
			Filename string `json:"filename"`
			SHA256   string `json:"sha256"`
		} `json:"entries"`
	}
	if strict(pythonAllowlist, &a) != nil || a.Version != 1 {
		return "", ErrRuntime
	}
	for _, e := range a.Entries {
		if e.Filename == name && e.Filename == "python-"+e.Version+"-embed-amd64.zip" && sum(b) == e.SHA256 {
			return e.Version, nil
		}
	}
	return "", ErrRuntime
}
func CheckWheels(lock []byte, wheels map[string][]byte, py string) error {
	l, err := ParseLock(lock)
	if err != nil {
		return ErrHash
	}
	if len(wheels) != len(l.Entries) {
		return ErrHash
	}
	seen := map[string]bool{}
	for name, b := range wheels {
		parts := strings.Split(strings.TrimSuffix(name, ".whl"), "-")
		if !strings.HasSuffix(name, ".whl") || (len(parts) != 5 && len(parts) != 6) || !safeName(name) {
			return ErrHash
		}
		pkg, ver := normalizeName(parts[0]), parts[1]
		if seen[pkg] {
			return ErrHash
		}
		seen[pkg] = true
		tags := parts[len(parts)-3:]
		matched := false
		for _, python := range strings.Split(tags[0], ".") {
			for _, abi := range strings.Split(tags[1], ".") {
				for _, plat := range strings.Split(tags[2], ".") {
					interpreter := "cp312"
					if strings.HasPrefix(py, "3.13.") {
						interpreter = "cp313"
					}
					okPy := python == interpreter || (python == "cp312" && interpreter == "cp313" && abi == "abi3")
					if okPy && (abi == "none" || abi == "abi3" || abi == interpreter) && (plat == "win_amd64" || (plat == "any" && abi == "none")) {
						matched = true
					}
				}
			}
		}
		if !matched {
			return ErrHash
		}
		found := false
		for _, e := range l.Entries {
			if e.Name == pkg && e.Version == ver && slices.Contains(e.Hashes, sum(b)) {
				found = true
			}
		}
		if !found {
			return ErrHash
		}
	}
	return nil
}

func readPackageManifest(files map[string][]byte, p Pin) (PackageManifest, error) {
	var m PackageManifest
	b, ok := files["jnpt-desktop.yaml"]
	if !ok || len(b) > 1<<20 {
		return m, ErrManifest
	}
	var doc yaml.Node
	if yaml.Unmarshal(b, &doc) != nil || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return m, ErrManifest
	}
	fields := map[string]bool{}
	nodes := doc.Content[0].Content
	for i := 0; i < len(nodes); i += 2 {
		key := nodes[i].Value
		if fields[key] || nodes[i+1].Tag == "!!null" {
			return m, ErrManifest
		}
		fields[key] = true
		if key == "requirements" && nodes[i+1].Value != "requirements.lock" {
			return m, ErrManifest
		}
	}
	for _, key := range []string{"name", "description", "entry", "input_schema", "tools", "timeout_s", "python"} {
		if !fields[key] {
			return m, ErrManifest
		}
	}
	d := yaml.NewDecoder(bytes.NewReader(b))
	d.KnownFields(true)
	if d.Decode(&m) != nil {
		return m, ErrManifest
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return m, ErrManifest
	}
	if m.Name != p.Pin || m.Description == "" || !safePath(m.Entry) || !strings.HasSuffix(m.Entry, ".py") || m.InputSchema == nil || m.Tools == nil || m.Timeout < 1 || m.Timeout > 86400 || !safePath(m.Python) || (m.Requirements != "" && m.Requirements != "requirements.lock") {
		return m, ErrManifest
	}
	if _, ok := files[m.Entry]; !ok {
		return m, ErrManifest
	}
	a, btools := slices.Clone(m.Tools), slices.Clone(p.DeclaredTools)
	slices.Sort(a)
	slices.Sort(btools)
	if !slices.Equal(a, btools) || len(slices.Compact(a)) != len(m.Tools) {
		return m, ErrManifest
	}
	return m, nil
}

// extract uses preflight directory bounds from script-runner/libs; actual bytes
// are limited while reading. One registry covers all wheels, including parents.
func extract(root string, b []byte, seen map[string]string, total *int64) error {
	if err := checkStructure(bytes.NewReader(b), int64(len(b)), 40000); err != nil {
		return ErrHash
	}
	z, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return ErrHash
	}
	var unpacked int64
	for _, f := range z.File {
		rel := strings.TrimSuffix(f.Name, "/")
		dir := strings.HasSuffix(f.Name, "/")
		typ := f.ExternalAttrs >> 16 & 0170000
		if len(rel) > 1024 || !safePath(rel) || f.Flags&0x41 != 0 || (f.Method != zip.Store && f.Method != zip.Deflate) || (typ != 0 && typ != 0100000 && typ != 0040000) || f.Mode()&fs.ModeType & ^fs.ModeDir != 0 || (typ == 0040000 && !dir) || (typ == 0100000 && dir) || f.UncompressedSize64 > 128<<20 {
			return ErrHash
		}
		if dir && f.UncompressedSize64 != 0 {
			return ErrHash
		}
		segs := strings.Split(rel, "/")
		for i := range segs {
			orig := strings.Join(segs[:i+1], "/")
			isDir := i < len(segs)-1 || dir
			key := fold(orig)
			value := orig
			if isDir {
				value += "/"
			}
			if old, ok := seen[key]; ok && (old != value || !isDir) {
				return ErrHash
			}
			seen[key] = value
		}
		if len(seen) > 40000 {
			return ErrHash
		}
		dst := filepath.Join(root, filepath.FromSlash(rel))
		if dir {
			if os.MkdirAll(dst, 0700) != nil {
				return ErrHash
			}
			continue
		}
		if os.MkdirAll(filepath.Dir(dst), 0700) != nil {
			return ErrHash
		}
		in, e := f.Open()
		if e != nil {
			return ErrHash
		}
		out, e := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			in.Close()
			return ErrHash
		}
		n, e := io.Copy(out, io.LimitReader(in, 128<<20+1))
		ce := in.Close()
		se := out.Sync()
		oe := out.Close()
		unpacked += n
		*total += n
		if e != nil || ce != nil || se != nil || oe != nil || n > 128<<20 || *total > 300<<20 || (unpacked > 1<<20 && unpacked > int64(len(b))*64) {
			return ErrHash
		}
	}
	return nil
}

func Snapshot(root string) ([]FileSeal, error) {
	var out []FileSeal
	var total int64
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return ErrHash
		}
		info, e := d.Info()
		if e != nil {
			return ErrHash
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return ErrHash
		}
		if d.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() || info.Size() > 128<<20 {
			return ErrHash
		}
		rel, e := filepath.Rel(root, p)
		if e != nil {
			return ErrHash
		}
		rel = filepath.ToSlash(rel)
		if !safePath(rel) {
			return ErrHash
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return ErrHash
		}
		total += int64(len(b))
		if total > 300<<20 || len(out) >= 30000 {
			return ErrHash
		}
		out = append(out, FileSeal{rel, int64(len(b)), sum(b)})
		return nil
	})
	if err != nil {
		return nil, ErrHash
	}
	return out, nil
}
func VerifyEnvironment(root string, want []FileSeal) error {
	got, e := Snapshot(root)
	if e != nil || !slices.Equal(got, want) {
		return ErrHash
	}
	return nil
}

func Prepare(root string, raw []byte, p Pin) (Environment, error) {
	var env Environment
	files, e := PackageFiles(raw, p)
	if e != nil {
		return env, e
	}
	m, e := readPackageManifest(files, p)
	if e != nil {
		return env, e
	}
	py, e := CheckPython(path.Base(m.Python), files[m.Python])
	if e != nil {
		return env, e
	}
	wheels := map[string][]byte{}
	for k, b := range files {
		if strings.HasPrefix(k, "wheels/") {
			if strings.Contains(strings.TrimPrefix(k, "wheels/"), "/") {
				return env, ErrHash
			}
			wheels[path.Base(k)] = b
		}
	}
	if m.Requirements != "" {
		if e = CheckWheels(files[m.Requirements], wheels, py); e != nil {
			return env, e
		}
	} else if len(wheels) != 0 {
		return env, ErrHash
	}
	if e = os.MkdirAll(root, 0700); e != nil {
		return env, e
	}
	stage, e := os.MkdirTemp(root, ".prepare-")
	if e != nil {
		return env, e
	}
	defer os.RemoveAll(stage)
	runtimeDir := filepath.Join(stage, "runtime")
	if e = os.Mkdir(runtimeDir, 0700); e != nil {
		return env, e
	}
	var total int64
	if e = extract(runtimeDir, files[m.Python], map[string]string{}, &total); e != nil {
		return env, ErrRuntime
	}
	libs := filepath.Join(stage, "libs")
	if e = os.Mkdir(libs, 0700); e != nil {
		return env, e
	}
	seen := map[string]string{}
	for _, b := range wheels {
		if e = extract(libs, b, seen, &total); e != nil {
			return env, e
		}
	}
	for rel, b := range files {
		dst := filepath.Join(stage, "pin", filepath.FromSlash(rel))
		if e = os.MkdirAll(filepath.Dir(dst), 0700); e != nil {
			return env, e
		}
		if e = os.WriteFile(dst, b, 0600); e != nil {
			return env, e
		}
	}
	if e = os.WriteFile(filepath.Join(runtimeDir, "bootstrap.py"), bootstrap, 0600); e != nil {
		return env, e
	}
	minor := "312"
	if strings.HasPrefix(py, "3.13.") {
		minor = "313"
	}
	// Explicit module paths; importing site or processing .pth is never enabled.
	pth := []byte("python" + minor + ".zip\n.\n../libs\n")
	if e = os.WriteFile(filepath.Join(runtimeDir, "python"+minor+"._pth"), pth, 0600); e != nil {
		return env, e
	}
	if _, e = os.Stat(filepath.Join(runtimeDir, "python.exe")); e != nil {
		return env, ErrRuntime
	}
	seal, e := Snapshot(stage)
	if e != nil {
		return env, e
	}
	dir := p.Commit + "-" + sum([]byte(p.Path))
	dst := filepath.Join(root, dir)
	// A pre-existing directory is never silently repaired after tampering.
	if _, e = os.Lstat(dst); e == nil {
		return env, ErrHash
	}
	if e = os.Rename(stage, dst); e != nil {
		return env, e
	}
	return Environment{Origin: p, Dir: dir, Files: seal, Entry: m.Entry, Timeout: m.Timeout, Tools: slices.Clone(m.Tools)}, nil
}

// The seal is stored in the encrypted state, outside the script environment.
func encode(v any) []byte { b, _ := json.Marshal(v); return b }
