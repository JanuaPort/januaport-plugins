package mediator

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/januaport/januaport-plugins/script-runner/internal/pins"
)

// A new wanted marker must not be visible while publishing its repository
// URL fails. Otherwise a fetcher cycle can reject the old/empty URL before
// it ever reaches Git, permanently hiding the expected ancestry verdict
// from a caller that runs only one cycle.
func TestWantedWaitsForRepositoryURLPublication(t *testing.T) {
	for _, blocked := range []string{"repo_url.tmp", "repo_url"} {
		t.Run(blocked, func(t *testing.T) {
			dir := t.TempDir()
			blocker := filepath.Join(dir, blocked)
			if err := os.Mkdir(blocker, 0o755); err != nil {
				t.Fatal(err)
			}
			m := &Mediator{cfg: Config{StoreDir: dir}}
			sha := strings.Repeat("a", 40)
			f := pins.File{
				RepoURL: "ssh://git@git.example.com/org/scripts.git",
				Pins:    []pins.Pin{{Name: "quick", Path: "quick", Commit: sha}},
			}
			m.writeWanted(f)
			wanted := filepath.Join(dir, "wanted", sha)
			if _, err := os.Stat(wanted); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("wanted marker published before repository URL: %v", err)
			}
			// The next refresh can publish the request once the I/O fault is gone.
			if err := os.Remove(blocker); err != nil {
				t.Fatal(err)
			}
			m.writeWanted(f)
			if b, err := os.ReadFile(filepath.Join(dir, "repo_url")); err != nil || string(b) != f.RepoURL+"\n" {
				t.Fatalf("repository URL = %q, %v", b, err)
			}
			if _, err := os.Stat(wanted); err != nil {
				t.Fatalf("wanted marker missing after repository URL publication: %v", err)
			}
		})
	}
}
