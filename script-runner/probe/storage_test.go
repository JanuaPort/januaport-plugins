//go:build docker

package probe

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStorageReadyRunAndRevocationWithStoppedFetcher(t *testing.T) {
	p := installStorageProbe(t)
	deadline := time.Now().Add(40 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		res, e := listTools(project)
		if e == nil {
			for _, tool := range res.Tools {
				if tool.Name == "demo" {
					ready = true
				}
			}
		}
		if ready {
			break
		}
		time.Sleep(time.Second)
	}
	if !ready {
		t.Fatal("verified storage pin did not become ready")
	}
	h := http.Header{"X-Jnpt-Run-Token": {runToken}, "X-Jnpt-Run-Id": {runID}, "X-Jnpt-Pin-Commit": {p.commit}}
	callStorage := func() map[string]any {
		t.Helper()
		s, e := session(project, h)
		if e != nil {
			t.Fatal(e)
		}
		defer s.Close()
		r, e := s.CallTool(context.Background(), &mcp.CallToolParams{Name: "demo", Arguments: map[string]any{}})
		if e != nil {
			t.Fatal(e)
		}
		b, _ := json.Marshal(r.Meta["januaport.ai/run"])
		var meta map[string]any
		_ = json.Unmarshal(b, &meta)
		return meta
	}
	if m := callStorage(); m["end"] != "ok" {
		t.Fatalf("storage execution: %v", m)
	}
	// Stop only our own fetcher. The unobserved withdrawal must still expire at
	// the signed timestamp, without depending on another fetcher publication.
	p.withdraw(t)
	for time.Now().Before(p.expires.Add(2 * time.Second)) {
		time.Sleep(time.Second)
	}
	if m := callStorage(); m["end"] != "refused" {
		t.Fatalf("expired storage code handed off: %v", m)
	}
	res, e := listTools(project)
	if e != nil {
		t.Fatal(e)
	}
	for _, tool := range res.Tools {
		if tool.Name == "demo" {
			t.Fatal("expired pin still ready")
		}
	}

	// R8: inspect the actual mediator mounts; the public/deploy key volume is
	// exclusive to the fetcher, even though the storage volume is shared.
	mounts := must(t, "docker", "inspect", "--format", "{{json .Mounts}}", project+"-script-runner-1")
	if strings.Contains(mounts, "probe-gitkey") || strings.Contains(mounts, "script-runner-git") {
		t.Fatal("mediator has deploy/public key mount")
	}
	must(t, "docker", "exec", project+"-script-runner-1", "sh", "-c", "test ! -e /run/jnpt/plugins/script-runner-git/runner-pins.pub && test ! -e /run/jnpt/plugins/script-runner-git/runtime-key")
}
