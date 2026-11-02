//go:build docker

package probe

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/script-runner/internal/gitobjects"
	"github.com/januaport/januaport-plugins/script-runner/internal/storagepins"
)

var storageGateway struct {
	sync.Mutex
	list, pkg []byte
}

func serveStorageProbe(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/api/plugin/script-runner/storage/") {
		return false
	}
	storageGateway.Lock()
	defer storageGateway.Unlock()
	if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer "+runtimeKey {
		w.WriteHeader(http.StatusUnauthorized)
		return true
	}
	if storageGateway.list == nil {
		w.WriteHeader(http.StatusNotFound)
		return true
	}
	if r.URL.Path == "/api/plugin/script-runner/storage/pins" && r.URL.RawQuery == "" {
		_, _ = w.Write(storageGateway.list)
	} else {
		_, _ = w.Write(storageGateway.pkg)
	}
	return true
}

type storageProbe struct {
	commit  string
	expires time.Time
	key     ed25519.PrivateKey
}

func (p storageProbe) signed(t *testing.T, seq uint64, issued time.Time, pins []storagepins.Pin) storagepins.Signed {
	t.Helper()
	b, e := json.Marshal(storagepins.Manifest{Purpose: storagepins.Purpose, Version: 1, Source: "speicher", Seq: seq, IssuedAt: issued.UTC().Format(time.RFC3339), Pins: pins})
	if e != nil {
		t.Fatal(e)
	}
	b, e = gitobjects.Canonical(b)
	if e != nil {
		t.Fatal(e)
	}
	return storagepins.Signed{Manifest: b, Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(p.key, b))}
}

func installStorageProbe(t *testing.T) storageProbe {
	t.Helper()
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	pkg, e := os.ReadFile("../internal/mediator/testdata/storage.pkg")
	if e != nil {
		t.Fatal(e)
	}
	var head struct{ Commit string }
	if json.Unmarshal(pkg[4:4+binary.BigEndian.Uint32(pkg)], &head) != nil {
		t.Fatal("fixture header")
	}
	p := storageProbe{commit: head.Commit, key: key}
	// Model an existing initialized installation, with a genuinely signed empty
	// accepted snapshot. Bootstrap quarantine is covered at the real file adapter
	// in fetcher/storage_test.go; the probe does not weaken its 12-minute rule.
	must(t, "docker", "stop", project+"-script-runner-1", project+"-script-runner-fetcher-1")
	t.Cleanup(func() {
		storageGateway.Lock()
		storageGateway.list = nil
		storageGateway.pkg = nil
		storageGateway.Unlock()
		_, _ = run("docker", "stop", project+"-script-runner-1", project+"-script-runner-fetcher-1")
		_, _ = run("docker", "run", "--rm", "--user", "0", "--entrypoint", "sh", "-v", project+"_probe-gitkey:/k", image, "-c", "rm -f /k/runner-pins.pub")
		_, _ = run("docker", "run", "--rm", "--user", "0", "--entrypoint", "sh", "-v", project+"_store:/k", image, "-c", "rm -f /k/speicher/state.json /k/speicher/inbox/list.json /k/speicher/inbox/pkg/demo-"+p.commit+".pkg")
		_, _ = run("docker", "start", project+"-script-runner-fetcher-1", project+"-script-runner-1")
	})
	if e = seed(project+"_probe-gitkey", "runner-pins.pub", base64.StdEncoding.EncodeToString(pub)+"\n"); e != nil {
		t.Fatal(e)
	}
	empty := p.signed(t, 1, time.Now(), []storagepins.Pin{})
	fpr := sha256.Sum256(pub)
	state := storagepins.State{Version: 1, KeyFpr: hex.EncodeToString(fpr[:]), HighestSeq: 1, Accepted: &empty, Approved: []storagepins.Approval{}, Failed: []storagepins.Failure{}}
	raw, _ := json.Marshal(state)
	must(t, "docker", "run", "--rm", "--user", "0", "--entrypoint", "sh", "-v", project+"_store:/k", image, "-c", "mkdir -p /k/speicher/inbox/pkg")
	if e = seed(project+"_store", "speicher/state.json", string(raw)); e != nil {
		t.Fatal(e)
	}
	issued := time.Now().UTC().Truncate(time.Second).Add(-8*time.Minute - 30*time.Second)
	p.expires = issued.Add(storagepins.MaxAge)
	list := p.signed(t, 2, issued, []storagepins.Pin{{Name: "demo", Commit: p.commit, Path: "scripts/demo", Algo: "sha256"}})
	raw, _ = json.Marshal(list)
	storageGateway.Lock()
	storageGateway.list = raw
	storageGateway.pkg = pkg
	storageGateway.Unlock()
	must(t, "docker", "start", project+"-script-runner-fetcher-1", project+"-script-runner-1")
	return p
}

func (p storageProbe) withdraw(t *testing.T) {
	t.Helper()
	must(t, "docker", "stop", project+"-script-runner-fetcher-1")
	list := p.signed(t, 3, time.Now(), []storagepins.Pin{})
	raw, _ := json.Marshal(list)
	storageGateway.Lock()
	storageGateway.list = raw
	storageGateway.Unlock()
	t.Log("fetcher stopped; waiting for accepted.manifest.issued_at + 10 minutes")
}
