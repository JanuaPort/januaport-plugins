//go:build linux

package client

import (
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
	return &DockerExecutor{CLI: cli}, p
}

func TestTokenNichtInInspect(t *testing.T) {
	d, p := realDocker(t)
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
	// docker cp reads the actual tmpfs file; archive header and value must match.
	var archive []byte
	for time.Now().Before(deadline) {
		b, e := d.CLI.Call(context.Background(), nil, "cp", containerName(key)+":/run/jnpt/token", "-")
		if e == nil {
			archive = b
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(string(archive), token) {
		t.Fatal("Token nicht im Job-tmpfs")
	}
	var objects []struct {
		HostConfig struct {
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
