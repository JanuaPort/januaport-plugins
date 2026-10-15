//go:build linux

package guard

import (
 "bytes"
 "encoding/binary"
 "encoding/json"
 "net"
 "strings"
 "testing"
 "unicode/utf8"

 "github.com/januaport/januaport-plugins/script-runner/internal/frame"
)

// The real bootstrap adapter must cap retained storage before buffering,
// forward while the script is alive, and stop retaining after truncation.
func TestLogBufferBoundedAtBootstrapAdapter(t *testing.T) {
 a,b := net.Pipe()
 defer a.Close(); defer b.Close()
 r := &runState{conn:a}
 var wire bytes.Buffer
 for range 256 { _ = frame.Write(&wire, map[string]any{"type":"log", "data":strings.Repeat("x",4096)}) }
 done := make(chan struct{})
 var forwarded strings.Builder
 go func(){ defer close(done); for { p,err:=frame.Read(b,frame.MaxFromSandbox); if err!=nil{return}; var f struct{Type,Data string}; _=json.Unmarshal(p,&f); if f.Type=="log" { forwarded.WriteString(f.Data) } } }()
 r.pumpBootstrap(&wire)
 _=a.Close(); <-done
 got:=r.log.String()
 if len(got)>64<<10 || r.log.Capacity()>64<<10 {t.Fatalf("retained log: len=%d capacity=%d",len(got),r.log.Capacity())}
 if !strings.HasSuffix(got,"\n[gekürzt]\n") || strings.Count(got,"[gekürzt]")!=1 {t.Fatal("missing or repeated truncation marker")}
 if forwarded.String()!=got {t.Fatal("retained and forwarded log differ")}
 before:=got
 if delta:=r.log.Append(strings.Repeat("z",1<<20));delta!="" || r.log.String()!=before {t.Fatal("retained bytes after truncation")}
}

func TestLogInvalidUTF8AndBoundary(t *testing.T) {
 r:=&runState{}
 r.log.Append("a\xffb\n")
 if r.log.String()!="a\ufffdb\n" {t.Fatalf("invalid UTF-8 not replaced: %q",r.log.String())}
 r.log.Append(strings.Repeat("€", 64<<10))
 if len(r.log.String())>64<<10 || !utf8.ValidString(r.log.String()) {t.Fatal("UTF-8 replacement/cut crossed byte limit")}
}

func TestLogOversizedBootstrapFrameRejectedBeforeBody(t *testing.T) {
 r:=&runState{}
 var header [4]byte
 binary.BigEndian.PutUint32(header[:],uint32(MaxJobFrame+1))
 r.pumpBootstrap(bytes.NewReader(header[:]))
 if r.log.String()!="" {t.Fatal("oversized frame retained")}
}
