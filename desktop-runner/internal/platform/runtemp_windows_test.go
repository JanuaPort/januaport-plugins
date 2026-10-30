package platform

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/januaport/januaport-plugins/desktop-runner/internal/client"
	"github.com/januaport/januaport-plugins/desktop-runner/internal/testfixture"
)

func TestWindowsEnvOhneProfilUndTemp(t *testing.T) { // L6
	for _, name := range []string{"LOCALAPPDATA", "USERPROFILE", "APPDATA"} {
		t.Setenv(name, `C:\Users\beispiel\Profil-`+name)
	}
	for _, kv := range WindowsEnv() {
		key, value, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(key) {
		case "LOCALAPPDATA", "USERPROFILE", "APPDATA", "HOME", "HOMEPATH", "HOMEDRIVE", "TEMP", "TMP", "TMPDIR", "PATH":
			t.Fatalf("Variable %s darf nicht aus dem Läufer ins Kind", key)
		}
		if strings.Contains(value, "beispiel") {
			t.Fatalf("Wert zeigt ins Profil: %s", kv)
		}
	}
}

func TestMkdirPrivateExklusivUndGeprueft(t *testing.T) { // L4
	base := filepath.Join(t.TempDir(), "tmp")
	if err := SecureRoot(filepath.Dir(base)); err != nil {
		t.Fatal(err)
	}
	if err := MkdirPrivate(base, false); err != nil {
		t.Fatal(err)
	}
	if err := MkdirPrivate(base, false); err != nil {
		t.Fatal("idempotent für die Basis", err)
	}
	if err := CheckPrivateACL(base); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, client.RunID("a"))
	if err := MkdirPrivate(dir, true); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivateACL(dir); err != nil {
		t.Fatal("Lauf-ACL nicht wie der Läufer-Ordner", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := MkdirPrivate(dir, true); err == nil {
		t.Fatal("vorhandenes Verzeichnis übernommen")
	}
	if _, err := os.Stat(filepath.Join(dir, "x")); err != nil {
		t.Fatal("vorhandenes Verzeichnis verändert", err)
	}
}

func junction(t *testing.T, link, target string) {
	t.Helper()
	// Verzeichnis-Junctions brauchen keine Symlink-Berechtigung.
	if err := exec.Command("cmd.exe", "/c", "mklink", "/J", link, target).Run(); err != nil {
		t.Fatal("Test-Junction", err)
	}
}

func TestRunTempJunctionKanarienvogel(t *testing.T) { // L5
	root := t.TempDir()
	if err := SecureRoot(root); err != nil {
		t.Fatal(err)
	}
	temps := client.RunTemps{Base: filepath.Join(root, "tmp"), Mkdir: MkdirPrivate}
	outside := t.TempDir()
	canary := filepath.Join(outside, "kanarienvogel.txt")
	if err := os.WriteFile(canary, []byte("lebt"), 0600); err != nil {
		t.Fatal(err)
	}
	for name, run := range map[string]func(dir string) error{
		"nach dem Lauf": func(dir string) error { return temps.Remove(dir) },
		"Besen":         func(string) error { return temps.Sweep() },
	} {
		dir, err := temps.Create(client.RunID(name))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.MkdirAll(filepath.Join(dir, "sub"), 0700); err != nil {
			t.Fatal(err)
		}
		junction(t, filepath.Join(dir, "sub", "verweis"), outside)
		junction(t, filepath.Join(temps.Base, "direkt-in-tmp-"+client.RunID(name)[:8]), outside)
		if err = run(dir); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err = os.Stat(canary); err != nil {
			t.Fatalf("%s: Aufräumen ist der Junction gefolgt", name)
		}
		if _, err = os.Stat(dir); name == "nach dem Lauf" && !os.IsNotExist(err) {
			t.Fatalf("%s: Verzeichnis bleibt", name)
		}
		temps.Sweep()
		if entries, _ := os.ReadDir(temps.Base); len(entries) != 0 {
			t.Fatalf("%s: Rest in tmp: %d", name, len(entries))
		}
		if _, err = os.Stat(canary); err != nil {
			t.Fatalf("%s: Junction beim Besen gefolgt", name)
		}
	}
}

func TestMkdirPrivateLehntJunctionInDerBasisAb(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	junction(t, filepath.Join(root, "tmp"), outside)
	temps := client.RunTemps{Base: filepath.Join(root, "tmp"), Mkdir: MkdirPrivate}
	if _, err := temps.Create(client.RunID("a")); err == nil {
		t.Fatal("Basis hinter Junction akzeptiert")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatal("hinter der Junction angelegt")
	}
}

func TestWindowsPythonTempProLauf(t *testing.T) { // Finding 6 am echten Prozess
	py, e := os.ReadFile("../../.probe/python-3.12.10-embed-amd64.zip")
	if os.IsNotExist(e) {
		t.Skip("kuratierte ZIP in .probe erforderlich")
	}
	if e != nil {
		t.Fatal(e)
	}
	tmp, e := os.MkdirTemp("../../.probe", "temp-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(tmp)
	root, _ := filepath.Abs(tmp)
	if e = SecureRoot(root); e != nil {
		t.Fatal(e)
	}
	st, e := client.OpenState(root, DPAPI{})
	if e != nil {
		t.Fatal(e)
	}
	script := []byte(`import os, sys, tempfile, _winapi, januaport
cfg = januaport.input
temp = os.environ["TEMP"]
assert os.environ["TMP"] == temp
assert tempfile.gettempdir().lower() == temp.lower(), tempfile.gettempdir()
assert os.path.realpath(os.getcwd()).lower() == os.path.realpath(temp).lower(), os.getcwd()
assert os.listdir(temp) == [], os.listdir(temp)
for name in ("LOCALAPPDATA", "USERPROFILE", "APPDATA", "HOME", "HOMEPATH"):
    assert name not in os.environ, name
assert temp.lower().startswith(cfg["erwartet"].lower()), temp
with open("relativ.txt", "w") as f:
    f.write("x")
with tempfile.NamedTemporaryFile(delete=False) as f:
    f.write(b"x")
os.makedirs(os.path.join(temp, "a", "b"))
_winapi.CreateJunction(cfg["ziel"], os.path.join(temp, "a", "verweis"))
`)
	manifest := []byte("name: demo\ndescription: test\nentry: main.py\ninput_schema: {type: object}\ntools: []\ntimeout_s: 20\npython: python-3.12.10-embed-amd64.zip\n")
	commit, raw := testfixture.Package(map[string][]byte{"main.py": script, "jnpt-desktop.yaml": manifest, "python-3.12.10-embed-amd64.zip": py}, "")
	pin := client.Pin{Pin: "demo", Commit: commit, Path: "demo", Algo: "sha256", DeclaredTools: []string{}, Schedules: []client.Schedule{}}
	env, e := client.Prepare(filepath.Join(root, "envs"), raw, pin)
	if e != nil {
		t.Fatal(e)
	}
	os.Mkdir(filepath.Join(root, "packages"), 0700)
	os.WriteFile(filepath.Join(root, "packages", env.Dir), raw, 0600)
	outside := t.TempDir()
	canary := filepath.Join(outside, "kanarienvogel.txt")
	os.WriteFile(canary, []byte("lebt"), 0600)
	cfg := client.Config{ExpectedAccount: "test"}
	cfg.StartPolicy.Mode = "always"
	base := filepath.Join(root, "tmp")
	engine := client.Engine{State: st, Config: cfg, Session: func() (client.Session, error) { return client.Session{Active: true, Account: "test"}, nil },
		ChildEnv: WindowsEnv(), Spawn: Spawn, Temps: &client.RunTemps{Base: base, Mkdir: MkdirPrivate}}
	input, _ := json.Marshal(map[string]string{"erwartet": base, "ziel": outside})
	for i, key := range []string{"demo/daily/2026-10-23T10:00:00Z", "demo/daily/2026-10-23T10:01:00Z"} {
		got := engine.Execute(context.Background(), client.Result{RunKey: key, ManifestSeq: 1, Commit: commit}, env, input, "", 20, 0)
		if got.End != "ok" {
			t.Fatalf("Lauf %d: %+v", i+1, got)
		}
		if e = st.Append(got); e != nil {
			t.Fatal(e)
		}
		if entries, _ := os.ReadDir(base); len(entries) != 0 {
			t.Fatalf("Lauf %d: Temp überlebt den Lauf", i+1)
		}
		if _, err := os.Stat(canary); err != nil {
			t.Fatalf("Lauf %d: Aufräumen ist der Junction des Kindes gefolgt", i+1)
		}
	}
	if err := client.VerifyEnvironment(filepath.Join(root, "envs", env.Dir), env.Files); err != nil {
		t.Fatal("Umgebung durch den Lauf verändert", err)
	}
}

func TestRevokeFolgtKeinerJunctionInTmp(t *testing.T) { // SEC R1 c-6519227223
	root := filepath.Join(t.TempDir(), "state")
	if err := SecureRoot(root); err != nil {
		t.Fatal(err)
	}
	st, err := client.OpenState(root, DPAPI{})
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	canary := filepath.Join(outside, "kanarienvogel.txt")
	if err = os.WriteFile(canary, []byte("lebt"), 0600); err != nil {
		t.Fatal(err)
	}
	run := filepath.Join(root, "tmp", client.RunID("a"))
	if err = os.MkdirAll(run, 0700); err != nil {
		t.Fatal(err)
	}
	junction(t, filepath.Join(run, "verweis"), outside)
	junction(t, filepath.Join(root, "tmp", "direkt"), outside)
	if err = st.Revoke(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("Zustand nicht gelöscht", err)
	}
	if _, err = os.Stat(canary); err != nil {
		t.Fatal("Widerruf ist der Junction gefolgt")
	}
}
