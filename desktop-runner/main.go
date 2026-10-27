// Desktop-Läufer, JanuaPort #1167. Windows/amd64, CGO disabled.
package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/platform"
)

var version = "0.1.0-dev"

func main() {
	if run(os.Args[1:], os.Stdout) != nil {
		fmt.Fprintln(os.Stderr, "Desktop-Läufer: Vorgang fehlgeschlagen (E1).")
		os.Exit(1)
	}
}
func run(args []string, out io.Writer) error {
	if runtime.GOOS == "linux" {
		return runServer(args, out)
	}
	if len(args) == 0 {
		return client.ErrProtocol
	}
	root, err := platform.Root()
	if err != nil {
		return err
	}
	if args[0] == "status" && len(args) == 1 {
		_, err := os.Stat(filepath.Join(root, "credential"))
		fmt.Fprintf(out, "Desktop-Läufer: gekoppelt=%t installiert=%t\n", err == nil, platform.Installed())
		return nil
	}
	if args[0] == "uninstall" && len(args) == 1 {
		if err = platform.Uninstall(); err != nil {
			return err
		}
		fmt.Fprintln(out, "Desktop-Läufer: Autostart entfernt.")
		return nil
	}
	if args[0] == "install" && len(args) == 1 {
		fallback, err := platform.Install()
		if err != nil {
			return err
		}
		if fallback {
			fmt.Fprintln(out, "Desktop-Läufer: Aufgabenplanung verweigert den Benutzerzugriff. Rückfall auf HKCU Run; keine automatische Wiederaufnahme nach Absturz.")
		} else {
			fmt.Fprintln(out, "Desktop-Läufer: Benutzeraufgabe eingerichtet.")
		}
		return nil
	}
	unlock, err := platform.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err = platform.SecureRoot(root); err != nil {
		return err
	}
	state, err := client.OpenState(root, platform.DPAPI{})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	switch args[0] {
	case "pair":
		fs := flag.NewFlagSet("pair", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		address := fs.String("url", "", "")
		code := fs.String("code", "", "")
		ca := fs.String("ca", "", "")
		fingerprint := fs.String("fingerprint", "", "")
		if fs.Parse(args[1:]) != nil || fs.NArg() != 0 || (*ca != "" && *fingerprint != "") {
			return client.ErrProtocol
		}
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
		reply, err := api.Pair(ctx, *code, version)
		if err != nil {
			return err
		}
		key, err := base64.StdEncoding.Strict().DecodeString(reply.AnlagePubkey)
		if err != nil {
			return err
		}
		// A successful re-pair starts a new trust domain, never mixing old state.
		if err = state.Revoke(); err != nil {
			return err
		}
		if err = platform.SecureRoot(root); err != nil {
			return err
		}
		state, err = client.OpenState(root, platform.DPAPI{})
		if err != nil {
			return err
		}
		if err = state.Save("credential", client.Credential{Trust: trust, DeviceID: reply.DeviceID, Token: reply.Token, ExpiresAt: reply.ExpiresAt, Pubkey: key}); err != nil {
			return err
		}
		fmt.Fprintln(out, "Desktop-Läufer: gekoppelt.")
		return nil
	case "run":
		if len(args) != 1 {
			return client.ErrProtocol
		}
		var credential client.Credential
		if err = state.Load("credential", &credential); err != nil {
			return err
		}
		engine := &client.Engine{State: state, Credential: credential, Version: version, Session: platform.Session, Spawn: platform.Spawn, ChildEnv: platform.WindowsEnv()}
		api, err := client.NewAPI(credential.Trust, credential.Token, engine.Revoke)
		if err != nil {
			return err
		}
		defer api.Close()
		engine.API = api
		log := client.Logger{Root: root}
		_ = log.Write(client.LogStarted, 1)
		err = engine.Run(ctx)
		if err == context.Canceled {
			return nil
		}
		return err
	default:
		return client.ErrProtocol
	}
}
