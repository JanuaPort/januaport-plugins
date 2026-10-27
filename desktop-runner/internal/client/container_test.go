package client

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func containerPin() Pin {
	return Pin{Pin: "probe", Commit: strings.Repeat("a", 64), Algo: "sha256", DeclaredTools: []string{}, Schedules: []Schedule{}, Container: &Container{Image: "registry.example/jobs/import@sha256:" + strings.Repeat("a", 64), Command: []string{"/app/import", "--synthetic"}, Network: "none", CPUMillis: 1000, MemoryBytes: 268435456, WallS: 1, SecretNames: []string{}}}
}

type dockerFake struct {
	mu      sync.Mutex
	calls   [][]string
	input   [][]byte
	digest  string
	wait    bool
	started chan struct{}
}

func (f *dockerFake) Call(ctx context.Context, in io.Reader, args ...string) ([]byte, error) {
	var b []byte
	if in != nil {
		b, _ = io.ReadAll(in)
	}
	f.mu.Lock()
	f.calls = append(f.calls, append([]string{}, args...))
	f.input = append(f.input, b)
	f.mu.Unlock()
	switch args[0] {
	case "info":
		return []byte(`{"SecurityOptions":["name=rootless"]}`), nil
	case "version":
		return []byte(`{"ApiVersion":"1.41"}`), nil
	case "image":
		return []byte("[" + f.digest + "]"), nil
	case "ps":
		return []byte("orphan\n"), nil
	case "wait":
		if f.started != nil {
			close(f.started)
		}
		if f.wait {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return []byte("0\n"), nil
	}
	return nil, nil
}
func (f *dockerFake) has(op string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.calls {
		if a[0] == op {
			return true
		}
	}
	return false
}
func fakeExecutor() (*DockerExecutor, *dockerFake) {
	f := &dockerFake{digest: containerPin().Container.Image}
	return &DockerExecutor{CLI: f}, f
}

func TestDockerArgumenteGolden(t *testing.T) {
	p := containerPin()
	a, err := dockerArguments("probe", *p.Container, nil)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/docker_arguments.json")
	if err != nil {
		t.Fatal(err)
	}
	var expected []string
	if json.Unmarshal(want, &expected) != nil || !reflect.DeepEqual(a, expected) {
		t.Fatalf("Argumente:\n%s", encode(a))
	}
}

func TestDockerVerboteneOptionen(t *testing.T) {
	for _, option := range []string{"--privileged", "--cap-add", "--device", "-v", "--mount", "--pid", "--ipc", "--uts", "--userns", "--dns", "--add-host", "--ip", "-e", "--env", "--entrypoint", "--security-opt"} {
		t.Run(option, func(t *testing.T) {
			raw := encode(containerPin().Container)
			raw = append(raw[:len(raw)-1], []byte(`,"`+option+`":"host"}`)...)
			var c Container
			if strict(raw, &c) == nil {
				t.Fatal("Zusatzschalter akzeptiert")
			}
			// An option in a command remains behind the image, never a daemon option.
			c = *containerPin().Container
			c.Command = []string{"/app/import", option}
			a, e := dockerArguments("probe", c, nil)
			if e != nil {
				t.Fatal(e)
			}
			imageAt := 0
			for i, s := range a {
				if s == c.Image {
					imageAt = i
				}
			}
			if a[len(a)-1] != option || imageAt != len(a)-3 {
				t.Fatal(a)
			}
		})
	}
	for _, network := range []string{"host", "bridge", "default", "private", "pasta", "slirp4netns", "container:abc", "ns:abc", "--privileged", "net --dns x"} {
		t.Run(network, func(t *testing.T) {
			d, f := fakeExecutor()
			p := containerPin()
			p.Container.Network = network
			_, e := d.Run(context.Background(), "probe", p, nil, nil, "secret")
			if e == nil || f.has("run") {
				t.Fatal("Netz akzeptiert", network, e)
			}
		})
	}
	for _, image := range []string{"repo:latest", "repo:tag@sha256:" + strings.Repeat("a", 64), "--privileged", "repo@sha256:" + strings.Repeat("b", 63)} {
		d, f := fakeExecutor()
		p := containerPin()
		p.Container.Image = image
		_, e := d.Run(context.Background(), "probe", p, nil, nil, "")
		if e == nil || f.has("run") {
			t.Fatal("Image akzeptiert", image)
		}
	}
}

func TestDigestVergleichNachPull(t *testing.T) {
	for _, digest := range []string{containerPin().Container.Image, "registry.example/jobs/import@sha256:" + strings.Repeat("b", 64), "[]", "broken"} {
		d, f := fakeExecutor()
		f.digest = digest
		_, e := d.Run(context.Background(), "probe", containerPin(), nil, nil, "")
		good := digest == containerPin().Container.Image
		if good && e != nil || !good && (!errors.Is(e, ErrHash) || f.has("run")) || !f.has("pull") {
			t.Fatal(digest, e, f.calls)
		}
	}
}

func TestGeheimnisseNurDeklariertUndOhneSymlink(t *testing.T) {
	root := t.TempDir()
	if e := os.WriteFile(filepath.Join(root, "alpha"), []byte("SYNTHETIC_SECRET"), 0400); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		declared, allowed []string
		want              int
		bad               bool
	}{
		{[]string{"alpha"}, nil, 0, false}, {nil, []string{"alpha"}, 0, false}, {[]string{"alpha"}, []string{"alpha", "other"}, 1, false},
		{[]string{"missing"}, []string{"missing"}, 0, true}, {[]string{"../alpha"}, []string{"../alpha"}, 0, true}, {[]string{"alpha"}, []string{"../alpha"}, 0, true},
	} {
		d, f := fakeExecutor()
		d.SecretsDir = root
		p := containerPin()
		p.Container.SecretNames = tc.declared
		names, e := d.Run(context.Background(), "probe", p, tc.allowed, nil, "")
		if (e != nil) != tc.bad || (!tc.bad && len(names) != tc.want) || tc.bad && f.has("run") {
			t.Fatal(tc, names, e)
		}
	}
	if err := os.Symlink(filepath.Join(root, "alpha"), filepath.Join(root, "link")); err != nil {
		t.Skip("Symlink benötigt lokale Berechtigung; Linux-CI prüft zwingend")
	}
	d, f := fakeExecutor()
	d.SecretsDir = root
	p := containerPin()
	p.Container.SecretNames = []string{"link"}
	_, err := d.Run(context.Background(), "probe", p, []string{"link"}, nil, "")
	if !errors.Is(err, ErrSecret) || f.has("run") {
		t.Fatal("Symlink akzeptiert", err)
	}
}

func TestGeheimnisseNieAlsEnv(t *testing.T) {
	d, f := fakeExecutor()
	d.SecretsDir = t.TempDir()
	os.WriteFile(filepath.Join(d.SecretsDir, "alpha"), []byte("SYNTHETIC_SECRET"), 0400)
	p := containerPin()
	p.Container.SecretNames = []string{"alpha"}
	names, e := d.Run(context.Background(), "probe", p, []string{"alpha"}, []byte(`{}`), "SYNTHETIC_RUN_TOKEN")
	if e != nil || len(names) != 1 {
		t.Fatal(names, e)
	}
	for _, a := range f.calls {
		s := strings.Join(a, " ")
		if strings.Contains(s, "SYNTHETIC_") || strings.Contains(s, "--env") || strings.Contains(s, " -e ") {
			t.Fatal("Geheimnis in Argumenten", a)
		}
	}
	found := false
	for i, a := range f.calls {
		if a[0] != "cp" {
			continue
		}
		tr := tar.NewReader(bytes.NewReader(f.input[i]))
		for {
			h, e := tr.Next()
			if e == io.EOF {
				break
			}
			if e != nil {
				t.Fatal(e)
			}
			b, _ := io.ReadAll(tr)
			if h.Name == "token" {
				found = true
				if h.Uid != 65532 || h.Gid != 65532 || h.Mode != 0400 || string(b) != "SYNTHETIC_RUN_TOKEN" {
					t.Fatal(h, string(b))
				}
			}
		}
	}
	if !found {
		t.Fatal("Token nicht über privaten Tar-Stream übertragen")
	}
}

func TestAbbruchUndWandzeitKillRm(t *testing.T) {
	for _, cancelRun := range []bool{true, false} {
		d, f := fakeExecutor()
		f.wait = true
		f.started = make(chan struct{})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { _, e := d.Run(ctx, "probe", containerPin(), nil, nil, ""); done <- e }()
		<-f.started
		if cancelRun {
			cancel()
		}
		err := <-done
		cancel()
		if cancelRun && !errors.Is(err, ErrInterrupted) || !cancelRun && !errors.Is(err, ErrWall) || !f.has("kill") || !f.has("rm") {
			t.Fatal(err, f.calls)
		}
	}
	d, f := fakeExecutor()
	if _, err := d.Run(context.Background(), "probe", containerPin(), nil, nil, ""); err != nil || !f.has("kill") || !f.has("rm") {
		t.Fatal("Laufende", err, f.calls)
	}
}

func TestVerwaisteContainerNachNeustart(t *testing.T) {
	d, f := fakeExecutor()
	st, err := OpenState(t.TempDir(), testCipher{})
	if err != nil {
		t.Fatal(err)
	}
	empty := []string{}
	r := Result{JobID: "probe", Commit: containerPin().Commit, ManifestSeq: 42, MountedSecretNames: &empty}
	if err = st.Mark(r, "started"); err != nil {
		t.Fatal(err)
	}
	e := Engine{State: st, Container: d}
	if err = e.recoverContainers(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !f.has("ps") || !f.has("kill") || !f.has("rm") || f.has("run") {
		t.Fatal(f.calls)
	}
	j := st.Journal()
	if j.Marker != nil || len(j.Results) != 1 || j.Results[0].End != "interrupted" {
		t.Fatal(j)
	}
	if err = e.recoverContainers(context.Background()); err != nil || len(st.Journal().Results) != 1 {
		t.Fatal("Neustart wiederholt Lauf", err)
	}
}

func TestRootlessErkennung(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{`{"SecurityOptions":["name=rootless","name=seccomp,profile=builtin"]}`, "rootless"},
		{`{"SecurityOptions":["name=seccomp,profile=builtin"]}`, "rootful"},
		{`{"host":{"security":{"rootless":true}}}`, "rootless"}, {`{"host":{"security":{"rootless":false}}}`, "rootful"},
		{`{}`, ""}, {`{"SecurityOptions":["not-rootless"]}`, "rootful"}, {`{"host":{"security":{"rootless":"true"}}}`, ""},
	} {
		got, e := parseContainerRuntime([]byte(tc.raw))
		if got != tc.want || (e != nil) != (tc.want == "") {
			t.Fatal(tc, got, e)
		}
	}
}

func TestVertrag2ManifestGolden(t *testing.T) {
	b, e := os.ReadFile("../../contract/golden/manifest_container_canonical.bin")
	if e != nil {
		t.Fatal(e)
	}
	m, e := ParseManifest(b)
	if e != nil || m.Contract != 2 || m.Pins[0].Container == nil {
		t.Fatal(m, e)
	}
	c, e := canonical(encode(m))
	if e != nil || !bytes.Equal(b, c) {
		t.Fatal("K1-Golden abweichend", string(c), e)
	}
	pub, key, _ := ed25519.GenerateKey(nil)
	env := SignedManifest{b, base64.StdEncoding.EncodeToString(ed25519.Sign(key, b))}
	if _, e = VerifyManifest(env, pub, m.DeviceID, 41); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*Manifest){func(m *Manifest) { m.Contract = 1 }, func(m *Manifest) { m.Pins[0].Commit = strings.Repeat("b", 64) }, func(m *Manifest) { m.Pins[0].Path = "main.py" }, func(m *Manifest) { m.Pins[0].DeclaredTools = []string{"tool"} }, func(m *Manifest) { m.Pins[0].Container = nil }} {
		var bad Manifest
		json.Unmarshal(b, &bad)
		change(&bad)
		if _, e = ParseManifest(encode(bad)); e == nil {
			t.Fatal("Ungültiger Container-Pin akzeptiert", bad)
		}
	}
	env.Manifest = bytes.ReplaceAll(b, []byte(`"wall_s":300`), []byte(`"wall_s":301`))
	if _, e = VerifyManifest(env, pub, m.DeviceID, 41); e == nil {
		t.Fatal("Manipulierte Signatur akzeptiert")
	}
}

func TestVertrag1WindowsUnveraendert(t *testing.T) {
	b, e := os.ReadFile("../../contract/golden/manifest_canonical.bin")
	if e != nil {
		t.Fatal(e)
	}
	m, e := ParseManifest(b)
	if e != nil {
		t.Fatal(e)
	}
	c, e := canonical(encode(m))
	if e != nil || !bytes.Equal(b, c) {
		t.Fatal("Vertrag 1 geändert")
	}
	h := Heartbeat{Contract: 1, Ready: true, RunnerVersion: "test", ManifestSeq: 1}
	if string(encode(h)) != `{"contract":1,"ready":true,"runner_version":"test","manifest_seq":1}` {
		t.Fatal(string(encode(h)))
	}
	r := Result{}
	if strings.Contains(string(encode(r)), "mounted_secret_names") {
		t.Fatal("Desktop-Ergebnis geändert")
	}
}

var _ = time.Second
