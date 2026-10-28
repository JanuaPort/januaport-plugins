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
	case "run":
		if f.started != nil {
			close(f.started)
		}
		if f.wait {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return nil, nil
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
	return &DockerExecutor{CLI: f, cleanupTimeout: 50 * time.Millisecond}, f
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
			var pin map[string]any
			json.Unmarshal(encode(containerPin()), &pin)
			pin["container"] = json.RawMessage(raw)
			manifest := map[string]any{"contract": 2, "device_id": "synthetic", "seq": 1, "issued_at": "2026-10-27T12:00:00Z", "grace_hours": 24, "pins": []any{pin}}
			if _, err := ParseManifest(encode(manifest)); err == nil {
				t.Fatal("Zusatzschalter am Manifestadapter akzeptiert")
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
			if a[len(a)-1] != option || imageAt != len(a)-6 {
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
		f.digest = image // Only the image grammar may reject this input.
		_, e := d.Run(context.Background(), "probe", p, nil, nil, "")
		if e == nil || f.has("run") {
			t.Fatal("Image akzeptiert", image)
		}
	}
}

func TestDigestVergleichNachPull(t *testing.T) {
	for _, digest := range []string{containerPin().Container.Image, "normalized.example/import@sha256:" + strings.Repeat("a", 64), "registry.example/jobs/import@sha256:" + strings.Repeat("b", 64), "[]", "broken"} {
		d, f := fakeExecutor()
		f.digest = digest
		_, e := d.Run(context.Background(), "probe", containerPin(), nil, nil, "")
		good := strings.HasSuffix(digest, "@sha256:"+containerPin().Commit)
		if good && e != nil || !good && (!errors.Is(e, ErrHash) || f.has("run")) || !f.has("pull") {
			t.Fatal(digest, e, f.calls)
		}
	}
}

func TestGeheimnisseNurDeklariertUndOhneSymlink(t *testing.T) {
	root := t.TempDir()
	os.Chmod(root, 0700)
	if err := os.Mkdir(filepath.Join(root, "directory"), 0700); err != nil {
		t.Fatal(err)
	}
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
		{[]string{"directory"}, []string{"directory"}, 0, true},
	} {
		d, f := fakeExecutor()
		d.SecretsDir = root
		p := containerPin()
		p.Container.SecretNames = tc.declared
		names, e := d.Run(context.Background(), "probe", p, tc.allowed, nil, "")
		if (e != nil) != tc.bad || (!tc.bad && len(names) != tc.want) || tc.bad && (f.has("run") || !errors.Is(e, ErrSecret)) {
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
	rootLink := filepath.Join(t.TempDir(), "root-link")
	if err = os.Symlink(root, rootLink); err != nil {
		t.Fatal(err)
	}
	d.SecretsDir = rootLink
	p.Container.SecretNames = []string{"alpha"}
	if _, err = d.Run(context.Background(), "probe", p, []string{"alpha"}, nil, ""); !errors.Is(err, ErrSecret) || f.has("run") {
		t.Fatal("Symlink im Betreiberpfad akzeptiert", err)
	}
}

func TestGeheimnisseNieAlsEnv(t *testing.T) {
	d, f := fakeExecutor()
	d.SecretsDir = t.TempDir()
	os.Chmod(d.SecretsDir, 0700)
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
		if a[0] != "run" {
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
	b, e := os.ReadFile("../../contract/golden-v2/manifest_container_canonical.bin")
	if e != nil {
		t.Fatal(e)
	}
	if sum(b) != "d376d83bdfa271f653a88de5d682e4cbb75a723ffa3a03119c2ea0ab4af88ead" {
		t.Fatal("K1-Fixture verändert")
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

type cliFunction func(context.Context, io.Reader, ...string) ([]byte, error)

func (f cliFunction) Call(ctx context.Context, in io.Reader, args ...string) ([]byte, error) {
	return f(ctx, in, args...)
}

func TestContainerGrenzenAmExecutor(t *testing.T) {
	for _, change := range []func(*Container){
		func(c *Container) { c.CPUMillis = 0 }, func(c *Container) { c.CPUMillis = 16001 }, func(c *Container) { c.MemoryBytes = 0 }, func(c *Container) { c.MemoryBytes = 32<<30 + 1 }, func(c *Container) { c.WallS = 0 }, func(c *Container) { c.WallS = 86401 },
		func(c *Container) { c.Command = nil }, func(c *Container) { c.Command = []string{""} }, func(c *Container) { c.Command = make([]string, 33); c.Command[0] = "x" }, func(c *Container) { c.Command = []string{strings.Repeat("x", 1025)} }, func(c *Container) { c.Command = []string{"a\x00b"} }, func(c *Container) { c.Command = []string{string([]byte{255})} },
		func(c *Container) { c.SecretNames = []string{"x", "x"} }, func(c *Container) { c.SecretNames = []string{"a..b"} }, func(c *Container) { c.SecretNames = []string{"/socket"} }, func(c *Container) { c.SecretNames = []string{strings.Repeat("a", 64)} }, func(c *Container) { c.SecretNames = make([]string, 17) },
	} {
		d, f := fakeExecutor()
		p := containerPin()
		change(p.Container)
		if _, e := d.Run(context.Background(), "probe", p, nil, nil, ""); e == nil || f.has("run") {
			t.Fatal("Ungültiger Pin gestartet", p.Container, e)
		}
	}
}

func TestRuntimeFehltOderZuAlt(t *testing.T) {
	for _, tc := range []struct {
		info, version string
		good          bool
	}{
		{`{"SecurityOptions":[]}`, `{"ApiVersion":"1.40"}`, false},
		{`{"SecurityOptions":[]}`, `{"ApiVersion":"1.41"}`, true},
		{`{"SecurityOptions":[]}`, `{}`, false},
		{`{"host":{"security":{"rootless":true}}}`, `{"Version":"4.0.0"}`, true},
		{`{"host":{"security":{"rootless":true}}}`, `{"Version":"3.4.0"}`, false},
		{`{}`, `{"ApiVersion":"1.41"}`, false},
	} {
		d := DockerExecutor{CLI: cliFunction(func(_ context.Context, _ io.Reader, a ...string) ([]byte, error) {
			if a[0] == "info" {
				return []byte(tc.info), nil
			}
			return []byte(tc.version), nil
		})}
		_, e := d.Runtime(context.Background())
		if (e == nil) != tc.good {
			t.Fatal(tc, e)
		}
	}
	d := DockerExecutor{CLI: ExecDocker{Path: filepath.Join(t.TempDir(), "missing-docker")}}
	if _, e := d.Runtime(context.Background()); e != ErrRuntime {
		t.Fatal(e)
	}
}

func TestContainerFehlgeschlageneBereinigungSperrtNeustart(t *testing.T) {
	state, _ := OpenState(t.TempDir(), testCipher{})
	state.Mark(Result{JobID: "probe"}, "started")
	d := DockerExecutor{cleanupTimeout: 50 * time.Millisecond, CLI: cliFunction(func(_ context.Context, _ io.Reader, a ...string) ([]byte, error) {
		if a[0] == "ps" {
			return []byte("orphan"), nil
		}
		return nil, ErrRuntime
	})}
	e := Engine{State: state, Container: &d}
	if e.recoverContainers(context.Background()) == nil || state.Journal().Marker == nil {
		t.Fatal("Laufplatz trotz verbliebenem Container freigegeben")
	}
}

func TestContainerWiderrufStopptVorLoeschen(t *testing.T) {
	d, f := fakeExecutor()
	f.wait = true
	f.started = make(chan struct{})
	st, _ := OpenState(t.TempDir(), testCipher{})
	e := Engine{State: st, Container: d}
	done := make(chan Result, 1)
	go func() {
		done <- e.executeContainer(context.Background(), Result{JobID: "probe", Commit: containerPin().Commit}, containerPin(), nil, nil, "synthetic", 30)
	}()
	<-f.started
	if err := e.Revoke(); err != nil {
		t.Fatal(err)
	}
	r := <-done
	if r.End != "interrupted" || !f.has("kill") || !f.has("rm") {
		t.Fatal(r, f.calls)
	}
	if _, err := os.Stat(st.Root); !os.IsNotExist(err) {
		t.Fatal("Widerruf behielt Zustand", err)
	}
}

func TestContainerBereinigungsfehlerSperrtFolgelauf(t *testing.T) {
	d, f := fakeExecutor()
	runs := 0
	d.CLI = cliFunction(func(ctx context.Context, in io.Reader, a ...string) ([]byte, error) {
		if a[0] == "run" {
			runs++
		}
		if a[0] == "rm" {
			return nil, ErrRuntime
		}
		return f.Call(ctx, in, a...)
	})
	if _, err := d.Run(context.Background(), "probe", containerPin(), nil, nil, ""); err != ErrRuntime {
		t.Fatal(err)
	}
	if _, err := d.Run(context.Background(), "second", containerPin(), nil, nil, ""); err != ErrRuntime || runs != 1 {
		t.Fatal("Folgelauf trotz Bereinigungsfehler", err, runs)
	}
}

func TestContainerCleanupWartetAufEntfernung(t *testing.T) {
	removals := 0
	d := DockerExecutor{cleanupTimeout: time.Second, CLI: cliFunction(func(_ context.Context, _ io.Reader, a ...string) ([]byte, error) {
		if a[0] == "rm" {
			removals++
			if removals < 3 {
				return nil, ErrRuntime
			}
		}
		if a[0] == "ps" {
			return []byte("still-removing"), nil
		}
		return nil, nil
	})}
	if err := d.cleanup("jnpt-run-probe"); err != nil || removals != 3 {
		t.Fatal("Asynchrone Entfernung nicht abgewartet", err, removals)
	}
}

func TestKeinGeheimnisMountBeiFehlgeschlagenemStart(t *testing.T) {
	d, f := fakeExecutor()
	d.SecretsDir = t.TempDir()
	os.Chmod(d.SecretsDir, 0700)
	os.WriteFile(filepath.Join(d.SecretsDir, "alpha"), []byte("synthetic"), 0444)
	d.CLI = cliFunction(func(ctx context.Context, in io.Reader, a ...string) ([]byte, error) {
		if a[0] == "run" {
			return nil, ErrRuntime
		}
		return f.Call(ctx, in, a...)
	})
	p := containerPin()
	p.Container.SecretNames = []string{"alpha"}
	names, err := d.Run(context.Background(), "probe", p, []string{"alpha"}, nil, "")
	if err != ErrRuntime || len(names) != 0 {
		t.Fatal("Nur geplanter Mount als ausgeführt gemeldet", names, err)
	}
}
