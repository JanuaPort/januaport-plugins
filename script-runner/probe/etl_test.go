//go:build docker

package probe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Docker-Sonde ETL-Rohstrom (JanuaPort/januaport#1215 P2): echter Wächter und
// echter Python-Bootstrap in der Sandbox ohne Netz, der Test ist der Kern
// (Stub-Gateway auf :8484: /healthz und /api/etl/stream).

const etlProbeRows = 5000

var etlStreamHits atomic.Int64

// etlProbeBody sind etlProbeRows Zeilen (über 256 KiB, also mehrere Stücke).
func etlProbeBody(withEnd bool) []byte {
	var b bytes.Buffer
	for i := 0; i < etlProbeRows; i++ {
		fmt.Fprintf(&b, `{"t":"row","d":{"nr":%d,"pad":"%s"}}`+"\n", i, strings.Repeat("x", 60))
	}
	if withEnd {
		fmt.Fprintf(&b, `{"t":"end","grund":"fertig","zeilen":%d,"bytes":%d}`+"\n", etlProbeRows, b.Len())
	}
	return b.Bytes()
}

func serveETLProbe(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case "/healthz":
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "version": "0.100.0", "saturated": false})
		return true
	case "/api/etl/stream":
		etlStreamHits.Add(1)
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+runToken {
			w.WriteHeader(http.StatusUnauthorized)
			return true
		}
		var req struct {
			Integration string `json:"integration"`
			QueryID     string `json:"query_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write(etlProbeBody(req.QueryID != "cut"))
		return true
	}
	return false
}

func waitContract3(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		res, err := listTools(project)
		if err == nil {
			b, _ := json.Marshal(res.Meta["januaport.ai/runner"])
			var m struct {
				Contract int `json:"contract"`
			}
			_ = json.Unmarshal(b, &m)
			if m.Contract == 3 {
				return
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatal("der Läufer meldet keine Vertragsfassung 3, obwohl der Kern 0.100.0 meldet")
}

func TestETLStreamEndToEnd(t *testing.T) {
	waitContract3(t)
	body := etlProbeBody(true)
	res, m := callReady(t, "etl_probe", map[string]any{"q": "full"})
	if m["end"] != "ok" {
		t.Fatalf("Ende %v/%v, error_type %v", m["end"], m["detail"], m["error_type"])
	}
	var out struct {
		Rows  int    `json:"rows"`
		Sum   int    `json:"sum"`
		Grund string `json:"grund"`
	}
	if err := json.Unmarshal([]byte(text(res)), &out); err != nil {
		t.Fatalf("Ausgabe kein JSON: %q", text(res))
	}
	if out.Rows != etlProbeRows || out.Sum != etlProbeRows*(etlProbeRows-1)/2 || out.Grund != "fertig" {
		t.Fatalf("Ausgabe = %+v", out)
	}
	stream, _ := m["stream"].(map[string]any)
	if stream["status"] != "fertig" || stream["zeilen"] != float64(etlProbeRows) || stream["bytes"] != float64(len(body)) {
		t.Fatalf("stream = %v, bytes want %d", stream, len(body))
	}
	if m["bytes_out"].(float64) > 200 || m["tool_calls"] != float64(0) {
		t.Fatalf("Strom zählt in bytes_out/tool_calls: %v", m)
	}
	if etlStreamHits.Load() == 0 {
		t.Fatal("der Kern wurde nicht gerufen")
	}
	mediatorAlive(t)
}

// Ein Strom ohne Endsatz endet für das Skript als StreamEnded("abgebrochen");
// schluckt das Skript die Ausnahme und endet mit 0, meldet der Läufer ok mit
// stream abgebrochen, und der Kern wertet error/stream.
func TestETLStreamWithoutEndLineIsAbgebrochen(t *testing.T) {
	waitContract3(t)
	res, m := callReady(t, "etl_probe", map[string]any{"q": "cut"})
	if m["end"] != "ok" {
		t.Fatalf("Ende %v/%v", m["end"], m["detail"])
	}
	var out struct {
		Rows  int    `json:"rows"`
		Grund string `json:"grund"`
	}
	_ = json.Unmarshal([]byte(text(res)), &out)
	if out.Grund != "abgebrochen" || out.Rows != etlProbeRows {
		t.Fatalf("Ausgabe = %q", text(res))
	}
	if stream, _ := m["stream"].(map[string]any); stream["status"] != "abgebrochen" {
		t.Fatalf("stream = %v", stream)
	}
}
