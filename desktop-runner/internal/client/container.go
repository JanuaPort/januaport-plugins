package client

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

var (
	ErrSecret        = errors.New("refused/secret")
	ErrWall          = errors.New("limit/wall")
	ErrInterrupted   = errors.New("interrupted")
	errContainerExit = errors.New("container_exit")
)

// Container mirrors K1 internal/scriptpins/container.go. Only signed values,
// never daemon options, are accepted. K1's narrower secret-name grammar wins.
type Container struct {
	Image       string   `json:"image"`
	Command     []string `json:"command"`
	Network     string   `json:"network"`
	CPUMillis   int      `json:"cpu_millis"`
	MemoryBytes int64    `json:"memory_bytes"`
	WallS       int      `json:"wall_s"`
	SecretNames []string `json:"secret_names"`
}

var imageDigest = regexp.MustCompile(`^(?:[a-z0-9]+(?:[.-][a-z0-9]+)*(?::[0-9]{1,5})?/)?[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*@sha256:[a-f0-9]{64}$`)
var containerNetwork = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
var containerSecret = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,62}$`)

func secretNamesValid(names []string) bool {
	if len(names) > 16 {
		return false
	}
	seen := map[string]bool{}
	for _, n := range names {
		if !containerSecret.MatchString(n) || strings.Contains(n, "..") || seen[n] {
			return false
		}
		seen[n] = true
	}
	return true
}
func (c Container) Validate() error {
	if !imageDigest.MatchString(c.Image) || len(c.Command) < 1 || len(c.Command) > 32 || c.Command[0] == "" {
		return ErrHash
	}
	for _, arg := range c.Command {
		if len(arg) > 1024 || !utf8.ValidString(arg) || strings.ContainsRune(arg, 0) {
			return ErrHash
		}
	}
	if !containerNetwork.MatchString(c.Network) || slices.Contains([]string{"host", "bridge", "default", "private", "pasta", "slirp4netns"}, c.Network) {
		return ErrHash
	}
	if c.CPUMillis < 1 || c.CPUMillis > 16000 || c.MemoryBytes < 1 || c.MemoryBytes > 32<<30 || c.WallS < 1 || c.WallS > 86400 {
		return ErrHash
	}
	if !secretNamesValid(c.SecretNames) {
		return ErrSecret
	}
	return nil
}
func validContainerPin(p Pin) bool {
	if p.Container == nil {
		return false
	}
	_, digest, _ := strings.Cut(p.Container.Image, "@sha256:")
	return p.Container.Validate() == nil && p.Algo == "sha256" && p.Path == "" && p.Commit == digest && len(p.DeclaredTools) == 0
}

// DockerCLI is the sole process seam. Production uses exec with argv, no shell.
type DockerCLI interface {
	Call(context.Context, io.Reader, ...string) ([]byte, error)
}
type ExecDocker struct {
	Path   string
	prefix []string
}
type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 1<<20 {
		return 0, ErrRuntime
	}
	return b.Buffer.Write(p)
}
func (d ExecDocker) Call(ctx context.Context, in io.Reader, args ...string) ([]byte, error) {
	if d.Path == "" {
		return nil, ErrRuntime
	}
	cmd := exec.CommandContext(ctx, d.Path, append(slices.Clone(d.prefix), args...)...)
	cmd.Stdin = in
	var out boundedOutput
	cmd.Stdout = &out
	if len(args) > 0 && args[0] == "run" {
		cmd.Stdout = io.Discard
	}
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var exit *exec.ExitError
		if len(args) > 0 && args[0] == "run" && errors.As(err, &exit) && exit.ExitCode() != 125 && exit.ExitCode() != 126 && exit.ExitCode() != 127 {
			return nil, errContainerExit
		}
		return nil, ErrRuntime
	}
	return out.Bytes(), nil
}

type ContainerExecutor interface {
	Run(context.Context, string, Pin, []string, []byte, string) ([]string, error)
	Recover(context.Context) error
	Runtime(context.Context) (string, error)
	MountNames(Pin, []string) ([]string, error)
}
type DockerExecutor struct {
	CLI            DockerCLI
	SecretsDir     string
	blocked        atomic.Bool
	cleanupTimeout time.Duration
}
type secretMount struct{ Name, Path string }

// Fixed bootstrap runs INSIDE the job container. Docker itself is always
// exec'd directly. Signed command elements remain positional arguments ($@),
// never shell source. The private tar stream is extracted before exec.
const containerBootstrap = `tar -xf - -C /run/jnpt || exit 125; exec "$@"`

func containerName(key string) string { return "jnpt-run-" + sum([]byte(key))[:32] }
func dockerArguments(key string, c Container, mounts []secretMount) ([]string, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	a := []string{"run", "--interactive", "--rm", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--user", "65532:65532", "--pids-limit", "128", "--memory", strconv.FormatInt(c.MemoryBytes, 10), "--cpus", fmt.Sprintf("%d.%03d", c.CPUMillis/1000, c.CPUMillis%1000), "--network", c.Network, "--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=67108864", "--tmpfs", "/run/jnpt:rw,noexec,nosuid,nodev,size=1048576,mode=0700,uid=65532,gid=65532", "--pull", "never", "--init", "--log-driver", "none", "--name", containerName(key), "--label", "ai.januaport.run=" + key}
	for _, m := range mounts {
		if !secretNamesValid([]string{m.Name}) || !filepath.IsAbs(m.Path) || strings.ContainsAny(m.Path, ",\n\r") {
			return nil, ErrSecret
		}
		a = append(a, "--mount", "type=bind,src="+m.Path+",dst=/run/secrets/"+m.Name+",readonly")
	}
	return append(append(a, c.Image, "/bin/sh", "-c", containerBootstrap, "jnpt-job"), c.Command...), nil
}

func (d *DockerExecutor) mounts(p Pin, allowed []string) ([]secretMount, error) {
	if p.Container == nil || !secretNamesValid(allowed) || !secretNamesValid(p.Container.SecretNames) {
		return nil, ErrSecret
	}
	var mounts []secretMount
	for _, name := range p.Container.SecretNames {
		if !slices.Contains(allowed, name) {
			continue
		}
		if d.SecretsDir == "" || !filepath.IsAbs(d.SecretsDir) || checkSecretsRoot(d.SecretsDir) != nil {
			return nil, ErrSecret
		}
		path := filepath.Join(d.SecretsDir, name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, ErrSecret
		}
		mounts = append(mounts, secretMount{name, path})
	}
	return mounts, nil
}
func (d *DockerExecutor) MountNames(p Pin, allowed []string) ([]string, error) {
	mounts, err := d.mounts(p, allowed)
	names := []string{}
	for _, m := range mounts {
		names = append(names, m.Name)
	}
	return names, err
}
func parseContainerRuntime(raw []byte) (string, error) {
	var v struct {
		SecurityOptions *[]string
		Host            struct{ Security struct{ Rootless *bool } }
	}
	if json.Unmarshal(raw, &v) != nil {
		return "", ErrRuntime
	}
	if v.Host.Security.Rootless != nil {
		if *v.Host.Security.Rootless {
			return "rootless", nil
		}
		return "rootful", nil
	}
	if v.SecurityOptions == nil {
		return "", ErrRuntime
	}
	for _, option := range *v.SecurityOptions {
		if option == "rootless" || option == "name=rootless" {
			return "rootless", nil
		}
	}
	return "rootful", nil
}
func (d *DockerExecutor) Runtime(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	info, err := d.CLI.Call(ctx, nil, "info", "--format", "{{json .}}")
	if err != nil {
		return "", ErrRuntime
	}
	runtime, err := parseContainerRuntime(info)
	if err != nil {
		return "", err
	}
	var podman struct {
		Host struct{ Security struct{ Rootless *bool } }
	}
	if json.Unmarshal(info, &podman) != nil {
		return "", ErrRuntime
	}
	format := "{{json .Server}}"
	if podman.Host.Security.Rootless != nil {
		format = "{{json .Client}}"
	}
	b, err := d.CLI.Call(ctx, nil, "version", "--format", format)
	if err != nil {
		return "", ErrRuntime
	}
	var v struct {
		APIVersion string `json:"ApiVersion"`
		Version    string
	}
	if json.Unmarshal(b, &v) != nil {
		return "", ErrRuntime
	}
	if podman.Host.Security.Rootless != nil {
		major, _, ok := strings.Cut(v.Version, ".")
		n, e := strconv.Atoi(major)
		if !ok || e != nil || n < 4 {
			return "", ErrRuntime
		}
	} else {
		major, minor, ok := strings.Cut(v.APIVersion, ".")
		n, e := strconv.Atoi(minor)
		if !ok || major != "1" || e != nil || n < 41 {
			return "", ErrRuntime
		}
	}
	return runtime, nil
}
func (d *DockerExecutor) cleanup(name string) error {
	timeout := d.cleanupTimeout
	if timeout <= 0 || timeout > 15*time.Second {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, _ = d.CLI.Call(ctx, nil, "kill", name)
	filter := "id=" + name
	if strings.HasPrefix(name, "jnpt-run-") {
		filter = "name=" + name
	}
	// --rm can race with rm --force: "removal already in progress" is not
	// absence. Keep checking until the daemon confirms disappearance.
	for {
		_, err := d.CLI.Call(ctx, nil, "rm", "--force", name)
		if err == nil {
			return nil
		}
		b, e := d.CLI.Call(ctx, nil, "ps", "--all", "--quiet", "--filter", filter)
		if e == nil && strings.TrimSpace(string(b)) == "" {
			return nil
		}
		if e != nil {
			d.blocked.Store(true)
			return ErrRuntime
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			d.blocked.Store(true)
			return ErrRuntime
		case <-timer.C:
		}
	}
}
func (d *DockerExecutor) Recover(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	b, err := d.CLI.Call(ctx, nil, "ps", "--all", "--quiet", "--filter", "label=ai.januaport.run")
	if err != nil {
		return ErrRuntime
	}
	for _, id := range strings.Fields(string(b)) {
		if !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(id) {
			return ErrRuntime
		}
		if err = d.cleanup(id); err != nil {
			return err
		}
	}
	return nil
}
func jobArchive(input []byte, token string) ([]byte, error) {
	if len(input) == 0 {
		input = []byte(`{}`)
	}
	if !inputOK(input) || len(token) > 16<<10 {
		return nil, ErrHash
	}
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	for _, file := range []struct {
		name string
		data []byte
	}{{"input.json", input}, {"token", []byte(token)}, {"ready", []byte("1")}} {
		if err := w.WriteHeader(&tar.Header{Name: file.name, Mode: 0400, Uid: 65532, Gid: 65532, Size: int64(len(file.data)), Typeflag: tar.TypeReg}); err != nil {
			return nil, err
		}
		if _, err := w.Write(file.data); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func repoDigestMatches(raw []byte, want string) bool {
	for _, ref := range strings.Fields(strings.Trim(strings.TrimSpace(string(raw)), "[]")) {
		_, digest, ok := strings.Cut(ref, "@sha256:")
		if ok && digest == want {
			return true
		}
	}
	return false
}

func (d *DockerExecutor) Run(ctx context.Context, key string, p Pin, allowed []string, input []byte, token string) (names []string, err error) {
	names = []string{}
	if d.blocked.Load() {
		return names, ErrRuntime
	}
	if p.Container == nil {
		return names, ErrHash
	}
	if err = p.Container.Validate(); err != nil {
		return names, err
	}
	if !validContainerPin(p) {
		return names, ErrHash
	}
	mounts, err := d.mounts(p, allowed)
	if err != nil {
		return names, err
	}
	args, err := dockerArguments(key, *p.Container, mounts)
	if err != nil {
		return names, err
	}
	archive, err := jobArchive(input, token)
	if err != nil {
		return names, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(p.Container.WallS)*time.Second)
	defer cancel()
	defer func() {
		if ctx.Err() != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				err = ErrWall
			} else {
				err = ErrInterrupted
			}
		}
	}()
	if _, err = d.Runtime(ctx); err != nil {
		return names, err
	}
	if _, err = d.CLI.Call(ctx, nil, "pull", p.Container.Image); err != nil {
		return names, ErrRuntime
	}
	b, err := d.CLI.Call(ctx, nil, "image", "inspect", "--format", "{{index .RepoDigests}}", p.Container.Image)
	// Docker normalizes repository aliases (docker.io/library/...). The
	// inspected image is selected by the pinned reference; compare its digest.
	if err != nil || !repoDigestMatches(b, p.Commit) {
		return names, ErrHash
	}
	name := containerName(key)
	defer func() {
		if e := d.cleanup(name); e != nil {
			err = e
		}
	}()
	// Attached stdin reaches only the job container. The bootstrap writes the
	// UID-owned 0400 files onto tmpfs, then execs the signed command. Unlike cp,
	// this works with Docker's read-only rootfs and with rootless UID mapping.
	_, err = d.CLI.Call(ctx, bytes.NewReader(archive), args...)
	started := err == nil || errors.Is(err, errContainerExit)
	if !started {
		inspectCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
		b, e := d.CLI.Call(inspectCtx, nil, "inspect", "--format", "{{.State.StartedAt}}", name)
		stop()
		at, parseErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(b)))
		started = e == nil && parseErr == nil && !at.IsZero()
	}
	if started {
		for _, m := range mounts {
			names = append(names, m.Name)
		}
	}
	return names, err
}
