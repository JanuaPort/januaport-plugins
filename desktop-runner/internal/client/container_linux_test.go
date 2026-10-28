//go:build linux

package client

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func realDocker(t *testing.T) (*DockerExecutor, Pin) {
	t.Helper()
	cli := ExecDocker{Path: "docker"}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err := cli.Call(ctx, nil, "info", "--format", "{{json .}}"); err != nil {
		if os.Getenv("JNPT_DOCKER_REQUIRED") == "1" {
			t.Fatal("Docker in Linux-CI Pflicht", err)
		}
		t.Skip("Echter Docker nicht verfügbar")
	}
	image := os.Getenv("JNPT_TEST_IMAGE")
	if image == "" {
		if _, err := cli.Call(ctx, nil, "pull", "alpine:3.22"); err != nil {
			t.Fatal(err)
		}
		b, err := cli.Call(ctx, nil, "image", "inspect", "--format", "{{index .RepoDigests 0}}", "alpine:3.22")
		if err != nil {
			t.Fatal(err)
		}
		image = strings.TrimSpace(string(b))
	}
	p := containerPin()
	p.Container.Image = image
	_, p.Commit, _ = strings.Cut(image, "@sha256:")
	p.Container.WallS = 30
	return &DockerExecutor{CLI: cliFunction(func(ctx context.Context, in io.Reader, args ...string) ([]byte, error) {
		b, err := cli.Call(ctx, in, args...)
		if err != nil && args[0] != "inspect" && args[0] != "kill" && args[0] != "rm" {
			t.Logf("Docker-Probe: %s: %v", args[0], err)
		}
		return b, err
	})}, p
}

func TestTokenNichtInInspect(t *testing.T) {
	d, p := realDocker(t)
	p.Container.MemoryBytes = 64 << 20 // Deliberately differs from the argv golden.
	key := "inspect-" + filepath.Base(t.TempDir())
	token := "SYNTHETIC_RUN_TOKEN_1195"
	// The job waits for delivery and verifies real ownership/mode, not a fake tar.
	p.Container.Command = []string{"/bin/sh", "-c", `while [ ! -f /run/jnpt/ready ]; do sleep 0.05; done; test "$(stat -c '%u:%g:%a' /run/jnpt/token)" = 65532:65532:400 || exit 19; test -s /run/jnpt/token || exit 20; sleep 20`}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, e := d.Run(ctx, key, p, nil, []byte(`{"synthetic":true}`), token); done <- e }()
	defer func() { cancel(); <-done }()
	var inspect []byte
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		b, e := d.CLI.Call(context.Background(), nil, "inspect", containerName(key))
		if e == nil {
			inspect = b
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(inspect) == 0 {
		t.Fatal("Container nicht gestartet")
	}
	if strings.Contains(string(inspect), token) {
		t.Fatal("Lauf-Token in docker inspect")
	}
	// Test-only exec reads the live tmpfs; production never calls docker exec.
	var contents []byte
	for time.Now().Before(deadline) {
		b, e := d.CLI.Call(context.Background(), nil, "exec", "--user", "65532:65532", containerName(key), "cat", "/run/jnpt/token")
		if e == nil {
			contents = b
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if string(contents) != token {
		t.Fatal("Token nicht im Job-tmpfs")
	}
	mode, e := d.CLI.Call(context.Background(), nil, "exec", "--user", "65532:65532", containerName(key), "stat", "-c", "%u:%g:%a", "/run/jnpt/token")
	if e != nil || strings.TrimSpace(string(mode)) != "65532:65532:400" {
		t.Fatal("Token-Dateirechte", string(mode), e)
	}
	var objects []struct {
		HostConfig struct {
			Memory         int64
			MemorySwap     int64
			ReadonlyRootfs bool
			Privileged     bool
			CapDrop        []string
			Tmpfs          map[string]string
		}
		Config struct{ User string }
	}
	if json.Unmarshal(inspect, &objects) != nil || len(objects) != 1 || !objects[0].HostConfig.ReadonlyRootfs || objects[0].HostConfig.Privileged || objects[0].Config.User != "65532:65532" || len(objects[0].HostConfig.Tmpfs) != 2 {
		t.Fatal("Sandbox fehlt")
	}
	if objects[0].HostConfig.Memory != p.Container.MemoryBytes || objects[0].HostConfig.MemorySwap != p.Container.MemoryBytes {
		t.Fatal("RAM und RAM-plus-Swap müssen dem Pin-Limit entsprechen")
	}
}

func TestDockerEigenenEntrypointUeberschreiben(t *testing.T) {
	d, p := realDocker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	key := "entrypoint-" + sum([]byte(t.TempDir()))[:16]
	dockerfile := "FROM " + p.Container.Image + "\nLABEL ai.januaport.test=" + key + "\nENTRYPOINT [\"/bin/sh\",\"-c\",\"exit 73\"]\n"
	var buildContext bytes.Buffer
	w := tar.NewWriter(&buildContext)
	if err := w.WriteHeader(&tar.Header{Name: "Dockerfile", Mode: 0600, Size: int64(len(dockerfile))}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, dockerfile); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := d.CLI.Call(ctx, &buildContext, "build", "--quiet", "--network", "none", "--pull=false", "-")
	if err != nil {
		t.Fatal("Mini-Image mit eigenem ENTRYPOINT bauen", err)
	}
	imageID := strings.TrimSpace(string(b))
	if !strings.HasPrefix(imageID, "sha256:") || !validID("sha256", strings.TrimPrefix(imageID, "sha256:")) {
		t.Fatal("Ungültige Build-Image-ID")
	}
	defer func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if _, err := d.CLI.Call(cleanupCtx, nil, "image", "rm", imageID); err != nil {
			t.Error("Mini-Image entfernen", err)
		}
	}()
	defer func() {
		if err := d.cleanup(containerName(key)); err != nil {
			t.Error(err)
		}
	}()

	p.Container.Command = []string{"/bin/sh", "-c", `test "$(cat /run/jnpt/token)" = SYNTHETIC_ENTRYPOINT_TOKEN && test "$(cat /run/jnpt/input.json)" = '{"synthetic":true}' && test "$#" -eq 2 && test "$1" = 'a b' && test "$2" = '$(exit 72); --entrypoint /bad'`, "probe", "a b", "$(exit 72); --entrypoint /bad"}
	args, err := dockerArguments(key, *p.Container, nil)
	if err != nil {
		t.Fatal(err)
	}
	// This unpublished test image has no registry digest. Exercise the real
	// argv builder and exec adapter with its immutable local ID; Run's separate
	// pull/digest verification remains covered by TestDigestVergleichNachPull.
	for i := range args {
		if args[i] == p.Container.Image {
			args[i] = imageID
		}
	}
	archive, err := jobArchive([]byte(`{"synthetic":true}`), "SYNTHETIC_ENTRYPOINT_TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.CLI.Call(ctx, bytes.NewReader(archive), args...); err != nil {
		t.Fatal("Eigener Image-ENTRYPOINT verhinderte Startcode oder unveränderten Pin-Befehl", err)
	}
}

func TestJobLiestGeraetetokenNicht(t *testing.T) {
	d, p := realDocker(t)
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	device := filepath.Join(root, "credential")
	os.WriteFile(device, []byte("SYNTHETIC_DEVICE_TOKEN"), 0600)
	// This path exists on the runner, but must not be mounted in the job.
	p.Container.Command = []string{"/bin/sh", "-c", `test ! -e /var/run/docker.sock && test ! -e /run/podman/podman.sock && ! cat "$1" && test ! -e /var/lib/jnpt-runner/credential`, "probe", device}
	if _, e := d.Run(context.Background(), "isolation-"+filepath.Base(root), p, nil, nil, ""); e != nil {
		t.Fatal("Job konnte Gerätetoken oder Socket lesen", e)
	}
}

// A subprocess fake exercises the real exec adapter, argument boundaries and
// stdin. Production code never invokes a shell or inherits arbitrary job env.
func TestDockerCLIHelper(t *testing.T) {
	if os.Getenv("JNPT_CLI_HELPER") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) < 2 {
		os.Exit(2)
	}
	if args[1] == "echo" {
		b, _ := io.ReadAll(os.Stdin)
		json.NewEncoder(os.Stdout).Encode(struct {
			Args  []string
			Input string
		}{args[2:], string(b)})
		os.Exit(0)
	}
	os.Exit(3)
}
func TestDockerCLIArgumenteOhneShell(t *testing.T) {
	t.Setenv("JNPT_CLI_HELPER", "1")
	path, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	d := ExecDocker{Path: path, prefix: []string{"-test.run=^TestDockerCLIHelper$", "--"}}
	args := []string{"echo", "a b", "$(touch /tmp/jnpt-unwanted)", "; exit 9", "--privileged"}
	b, e := d.Call(context.Background(), strings.NewReader("PRIVATE_STDIN"), args...)
	if e != nil {
		t.Fatal(e)
	}
	var got struct {
		Args  []string
		Input string
	}
	if json.Unmarshal(b, &got) != nil || strings.Join(got.Args, "|") != strings.Join(args[1:], "|") || got.Input != "PRIVATE_STDIN" {
		t.Fatal(string(b))
	}
}

func TestDockerAbbruchWandzeitUndNeustart(t *testing.T) {
	d, p := realDocker(t)
	for _, wall := range []bool{false, true} {
		key := "cleanup-" + filepath.Base(t.TempDir())
		p.Container.Command = []string{"sleep", "60"}
		p.Container.WallS = 20
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { _, e := d.Run(ctx, key, p, nil, nil, ""); done <- e }()
		deadline := time.Now().Add(15 * time.Second)
		started := false
		for time.Now().Before(deadline) {
			b, e := d.CLI.Call(context.Background(), nil, "inspect", "--format", "{{.State.Running}}", containerName(key))
			if e == nil && strings.TrimSpace(string(b)) == "true" {
				started = true
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !wall {
			cancel()
		}
		err := <-done
		cancel()
		if !started || wall && err != ErrWall || !wall && err != ErrInterrupted {
			t.Fatal("Lauf/Abbruch", started, err)
		}
		b, e := d.CLI.Call(context.Background(), nil, "ps", "--all", "--quiet", "--filter", "name="+containerName(key))
		if e != nil || len(strings.TrimSpace(string(b))) != 0 {
			t.Fatal("Container verblieben", e)
		}
	}
	// The test daemon must be dedicated, just like the production runner.
	key := "orphan-" + filepath.Base(t.TempDir())
	name := containerName(key)
	_, err := d.CLI.Call(context.Background(), nil, "run", "--detach", "--rm", "--label", "ai.januaport.run="+key, "--name", name, p.Container.Image, "sleep", "60")
	if err != nil {
		t.Fatal(err)
	}
	defer d.cleanup(name)
	st, _ := OpenState(t.TempDir(), testCipher{})
	r := containerResult(Result{JobID: key, Commit: p.Commit, ManifestSeq: 42})
	st.Mark(r, "started")
	e := Engine{State: st, Container: d}
	if err = e.recoverContainers(context.Background()); err != nil {
		t.Fatal(err)
	}
	b, err := d.CLI.Call(context.Background(), nil, "ps", "--all", "--quiet", "--filter", "name="+name)
	if err != nil || len(strings.TrimSpace(string(b))) != 0 || len(st.Journal().Results) != 1 || st.Journal().Results[0].End != "interrupted" {
		t.Fatal("Verwaister Lauf nicht beendet", err)
	}
}

func TestDockerGeheimnisMountReadOnlyUndInspect(t *testing.T) {
	d, p := realDocker(t)
	// In socket-based local CI /tmp is mounted at the same path in the test
	// container and daemon host. GitHub's native Linux runner shares it already.
	root, err := os.MkdirTemp("/tmp", "jnpt-secret-1195-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	secret := "SYNTHETIC_OPERATOR_SECRET_1195" // gitleaks:allow synthetische Kanarie, kein Zugangsdatum
	if err = os.WriteFile(filepath.Join(root, "alpha"), []byte(secret), 0444); err != nil {
		t.Fatal(err)
	}
	d.SecretsDir = root
	p.Container.SecretNames = []string{"alpha"}
	key := "secret-" + filepath.Base(root)
	p.Container.Command = []string{"/bin/sh", "-c", `test -s /run/secrets/alpha && ! (echo forbidden > /run/secrets/alpha) && sleep 20`}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := d.Run(ctx, key, p, []string{"alpha"}, nil, ""); done <- e }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(12 * time.Second)
	var b []byte
	for time.Now().Before(deadline) {
		v, e := d.CLI.Call(context.Background(), nil, "inspect", containerName(key))
		if e == nil {
			b = v
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(b) == 0 || strings.Contains(string(b), secret) {
		t.Fatal("Mount fehlt oder Geheimniswert in Inspect")
	}
	var objects []struct {
		Mounts []struct {
			Source, Destination string
			RW                  bool
		}
	}
	if json.Unmarshal(b, &objects) != nil || len(objects) != 1 {
		t.Fatal("Inspect-Form")
	}
	found := false
	for _, m := range objects[0].Mounts {
		if m.Destination == "/run/secrets/alpha" {
			found = true
			if m.RW || m.Source != filepath.Join(root, "alpha") {
				t.Fatal("Geheimnis nicht read-only")
			}
		}
	}
	if !found {
		t.Fatal("Deklarierter Mount fehlt")
	}
	value, e := d.CLI.Call(context.Background(), nil, "exec", "--user", "65532:65532", containerName(key), "cat", "/run/secrets/alpha")
	if e != nil || string(value) != secret {
		t.Fatal("Geheimnis im Job nicht lesbar", e)
	}
	if _, e = d.CLI.Call(context.Background(), nil, "exec", "--user", "65532:65532", containerName(key), "touch", "/run/secrets/alpha"); e == nil {
		t.Fatal("Geheimnis im Job schreibbar")
	}
}
