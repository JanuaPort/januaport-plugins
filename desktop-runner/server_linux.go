//go:build linux

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/platform"
)

type serverConfig struct {
	Mode       string `json:"mode"`
	DockerPath string `json:"docker_path"`
	StateDir   string `json:"state_dir"`
	SecretsDir string `json:"secrets_dir"`
}

func loadServerConfig(path string) (serverConfig, error) {
	var c serverConfig
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 16<<10))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Mode != "container" || !filepath.IsAbs(c.DockerPath) || !filepath.IsAbs(c.StateDir) || c.StateDir == "/" || strings.ContainsAny(c.DockerPath, "\r\n") {
		return c, client.ErrProtocol
	}
	c.StateDir = filepath.Clean(c.StateDir)
	if c.StateDir == "/" {
		return c, client.ErrProtocol
	}
	if c.SecretsDir != "" {
		c.SecretsDir = filepath.Clean(c.SecretsDir)
		if !filepath.IsAbs(c.SecretsDir) || pathsOverlap(c.StateDir, c.SecretsDir) {
			return c, client.ErrProtocol
		}
	}
	return c, nil
}
func pathsOverlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}
func runServer(args []string, out io.Writer) error {
	if len(args) == 0 || os.Geteuid() == 0 || os.Geteuid() == 65532 {
		return client.ErrProtocol
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	path := fs.String("config", "/etc/jnpt-runner/runner.json", "")
	address := fs.String("url", "", "")
	ca := fs.String("ca", "", "")
	fingerprint := fs.String("fingerprint", "", "")
	if fs.Parse(args[1:]) != nil || fs.NArg() != 0 || (*ca != "" && *fingerprint != "") {
		return client.ErrProtocol
	}
	cfg, err := loadServerConfig(*path)
	if err != nil {
		return err
	}
	// Dedicated daemon per runner: recovery removes all ai.januaport.run labels.
	parent := filepath.Dir(cfg.StateDir)
	if err = platform.SecureRoot(parent); err != nil {
		return err
	}
	unlock, err := platform.LockFile(filepath.Join(parent, "runner.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	cipher, err := platform.NewFileIntegrity(cfg.StateDir)
	if err != nil {
		return err
	}
	state, err := client.OpenState(cfg.StateDir, cipher)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	executor := &client.DockerExecutor{CLI: client.ExecDocker{Path: cfg.DockerPath}, SecretsDir: cfg.SecretsDir}
	runtime, err := executor.Runtime(ctx)
	if err != nil {
		return err
	}
	switch args[0] {
	case "pair":
		trust := client.Trust{URL: *address, Fingerprint: *fingerprint}
		if *ca != "" {
			trust.CA, err = os.ReadFile(*ca)
			if err != nil {
				return err
			}
		}
		api, err := client.NewAPI(trust, "", nil)
		if err != nil {
			return err
		}
		defer api.Close()
		if err = api.Server(runtime); err != nil {
			return err
		}
		// Pairing codes are read once from stdin, never persisted or placed in argv.
		code, err := io.ReadAll(io.LimitReader(os.Stdin, 128))
		if err != nil {
			return err
		}
		reply, err := api.Pair(ctx, strings.TrimSpace(string(code)), version)
		if err != nil {
			return err
		}
		key, err := base64.StdEncoding.Strict().DecodeString(reply.AnlagePubkey)
		if err != nil {
			return err
		}
		if err = executor.Recover(ctx); err != nil {
			return err
		}
		if err = state.Revoke(); err != nil {
			return err
		}
		cipher, err = platform.NewFileIntegrity(cfg.StateDir)
		if err != nil {
			return err
		}
		state, err = client.OpenState(cfg.StateDir, cipher)
		if err != nil {
			return err
		}
		if err = state.Save("credential", client.Credential{Trust: trust, DeviceID: reply.DeviceID, Token: reply.Token, ExpiresAt: reply.ExpiresAt, Pubkey: key}); err != nil {
			return err
		}
		fmt.Fprintln(out, "Server-Läufer: gekoppelt.")
		return nil
	case "run":
		var credential client.Credential
		if err = state.Load("credential", &credential); err != nil {
			return err
		}
		engine := &client.Engine{State: state, Credential: credential, Version: version, Container: executor}
		api, err := client.NewAPI(credential.Trust, credential.Token, engine.Revoke)
		if err != nil {
			return err
		}
		defer api.Close()
		if err = api.Server(runtime); err != nil {
			return err
		}
		engine.API = api
		err = engine.Run(ctx)
		if err == context.Canceled {
			return nil
		}
		return err
	case "status":
		_, err := os.Stat(filepath.Join(cfg.StateDir, "credential"))
		fmt.Fprintf(out, "Server-Läufer: gekoppelt=%t container_runtime=%s\n", err == nil, runtime)
		return nil
	}
	return client.ErrProtocol
}
