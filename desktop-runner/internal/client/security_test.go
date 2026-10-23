package client

import (
 "bytes"
 "context"
 "encoding/binary"
 "encoding/json"
 "os"
 "path/filepath"
 "testing"
 "time"
)

func goldenPackage(t *testing.T) (packageHead, []byte) {
 t.Helper()
 b,e:=os.ReadFile("../../contract/golden/package_sha256.bin"); if e!=nil {t.Fatal(e)}
 var h packageHead
 n:=binary.BigEndian.Uint32(b[:4]); if e=json.Unmarshal(b[4:4+n],&h);e!=nil {t.Fatal(e)}
 return h,b
}

func TestFalscherHashBrichtVorDemStartAb(t *testing.T) {
 h,b:=goldenPackage(t)
 for _,kind:=range []string{"blob","commit","extra","symlink","missing"} {
 t.Run(kind,func(t *testing.T){
 raw:=append([]byte(nil),b...); want:=h.Commit
 switch kind {
 case "blob": raw[len(raw)-2]^=1
 case "commit": want=string(bytes.Repeat([]byte("0"),64))
 case "extra": raw=append(raw,0,0,0,2,'b','x')
 case "symlink": raw=bytes.Replace(raw,[]byte("100644"),[]byte("120000"),1)
 case "missing": raw=raw[:len(raw)-20]
 }
 starts:=0
 gate:=Gate{Spawn:func(context.Context,Launch)(Process,error){starts++;return nil,nil}}
 err:=gate.CheckPackageAndStart(context.Background(),raw,Pin{Algo:h.Algo,Commit:want,Path:h.Path},Launch{})
 if err!=ErrHash || starts!=0 {t.Fatalf("error=%v starts=%d",err,starts)}
 })
 }
}

func TestUmgebungVorJedemStart(t *testing.T) {
 root:=t.TempDir(); p:=filepath.Join(root,"main.py")
 if e:=os.WriteFile(p,[]byte("pass"),0600);e!=nil {t.Fatal(e)}
 seal,e:=Snapshot(root);if e!=nil {t.Fatal(e)}
 if e=VerifyEnvironment(root,seal);e!=nil {t.Fatal(e)}
 if e=os.WriteFile(p,[]byte("evil"),0600);e!=nil {t.Fatal(e)}
 if VerifyEnvironment(root,seal)!=ErrHash {t.Fatal("manipulierte Umgebung akzeptiert")}
}

func TestPythonAllowlistUndWheel(t *testing.T) {
 if _,e:=CheckPython("python-3.12.10-embed-amd64.zip",[]byte("evil"));e!=ErrRuntime {t.Fatal(e)}
 if e:=CheckWheels([]byte("demo==1.0 --hash=sha256:"+string(bytes.Repeat([]byte("0"),64))),map[string][]byte{"demo-1.0-cp312-none-any.whl":[]byte("evil")},"3.12.10");e!=ErrHash {t.Fatal(e)}
}

func TestOfflineRegel(t *testing.T) {
 now:=time.Date(2026,10,23,10,0,0,0,time.UTC)
 for _,tt:=range []struct{tools []string; last time.Time; want bool}{
 {nil,now.Add(-time.Hour),true},{[]string{"tool"},now.Add(-time.Hour),false},
 {nil,now.Add(-2*time.Hour),false},{nil,time.Time{},false},{nil,now.Add(time.Second),false},
 } {if got:=OfflineAllowed(Pin{DeclaredTools:tt.tools},2,tt.last,now);got!=tt.want {t.Fatalf("got %v want %v",got,tt.want)}}
}
