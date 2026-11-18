//go:build linux

package guard

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/script-runner/internal/frame"
)

// Echter Wächter-Pfad mit echtem Python-Bootstrap: runState.run startet
// `python -I bootstrap.py`, die Pumpen reichen etl_open/etl_next hinaus und
// etl_chunk/etl_end hinein. Die Gegenstelle (vermittler) spielt der Test über
// eine Pipe. Gemessen wird der Weg mit einem vollen Stück (256 KiB roh) durch
// den echten Sockelpaar-Kanal zwischen Wächter und Bootstrap.
func TestETLRealGuardAndBootstrapFullChunks(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("kein python3: Wächter-Bootstrap-Test nicht ausgeführt")
	}
	boot, err := filepath.Abs("../../sandbox/bootstrap.py")
	if err != nil {
		t.Fatal(err)
	}
	const rows = 6000
	var body bytes.Buffer
	for i := 0; i < rows; i++ {
		fmt.Fprintf(&body, `{"t":"row","d":{"nr":%d,"pad":"%s"}}`+"\n", i, strings.Repeat("ü", 40))
	}
	fmt.Fprintf(&body, `{"t":"end","grund":"fertig","zeilen":%d,"bytes":%d}`+"\n", rows, body.Len())
	data := body.Bytes()
	if len(data) < 2*frame.MaxETLChunkRaw {
		t.Fatalf("Testmaterial zu klein: %d", len(data))
	}
	script := `import json, sys
import januaport
n = 0; s = 0
for row in januaport.etl_stream("erp", "q", {"a": 1}):
    n += 1; s += row["nr"]
json.dump({"n": n, "s": s}, sys.stdout)
`
	work := t.TempDir()
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	r := &runState{conn: a}
	cfg := Config{WorkDir: work, Python: py, Bootstrap: boot}
	job := frame.Job{Type: frame.TypeJob, Entry: "main.py", Input: json.RawMessage(`{}`),
		Files: []frame.File{{Path: "main.py", DataB64: base64.StdEncoding.EncodeToString([]byte(script))}}}
	runDone := make(chan struct{})
	go func() { r.run(cfg, job); r.SendExited(); close(runDone) }() // SendExited wie finish()

	// vermittler-Seite: etl_open lesen, auf jedes etl_next ein Stück, dann etl_end.
	var out strings.Builder
	var chunks, maxRaw int
	pos := 0
	deadline := time.Now().Add(60 * time.Second)
	_ = b.SetReadDeadline(deadline)
loop:
	for {
		p, err := frame.Read(b, frame.MaxFromSandbox)
		if err != nil {
			t.Fatalf("Lesen vom Wächter: %v", err)
		}
		typ, _ := frame.TypeOf(p)
		switch typ {
		case frame.TypeETLOpen:
			var o frame.ETLOpen
			_ = json.Unmarshal(p, &o)
			if o.Integration != "erp" || o.QueryID != "q" || string(o.Params) != `{"a": 1}` && string(o.Params) != `{"a":1}` {
				t.Errorf("etl_open: %s", p)
			}
		case frame.TypeETLNext:
			var n frame.ETLNext
			_ = json.Unmarshal(p, &n)
			if pos < len(data) {
				end := min(pos+frame.MaxETLChunkRaw, len(data))
				chunks++
				maxRaw = max(maxRaw, end-pos)
				if err := frame.Write(b, frame.ETLChunk{Type: frame.TypeETLChunk, ID: n.ID, DataB64: base64.StdEncoding.EncodeToString(data[pos:end])}); err != nil {
					t.Fatal(err)
				}
				pos = end
			} else if err := frame.Write(b, frame.ETLEnd{Type: frame.TypeETLEnd, ID: n.ID, Status: frame.ETLStatusEOF}); err != nil {
				t.Fatal(err)
			}
		case frame.TypeOutput:
			var o frame.Output
			_ = json.Unmarshal(p, &o)
			out.WriteString(o.Data)
		case frame.TypeExited:
			var ex frame.Exited
			_ = json.Unmarshal(p, &ex)
			if ex.Code != 0 || ex.ErrorType != "" {
				t.Fatalf("Skript endete mit %+v", ex)
			}
			break loop
		}
	}
	<-runDone
	want := fmt.Sprintf(`{"n": %d, "s": %d}`, rows, rows*(rows-1)/2)
	if out.String() != want {
		t.Fatalf("Ausgabe = %q, want %q", out.String(), want)
	}
	if chunks < 2 || maxRaw != frame.MaxETLChunkRaw {
		t.Fatalf("Stücke: %d, größtes %d (muss ein volles Stück sein)", chunks, maxRaw)
	}
	_ = os.RemoveAll(work)
}
