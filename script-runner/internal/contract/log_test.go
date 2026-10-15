package contract

import "testing"

func TestRunLogGolden(t *testing.T){
 golden(t,"end_ok_log.json",Result(Run{End:EndOK,Isolation:IsolationStandard,Log:"Prüfung 12\n"},nil))
 golden(t,"end_error_log.json",Result(Run{End:EndError,Detail:DetailExit,Isolation:IsolationStandard,Log:"vor dem Fehler\n"},nil))
 // Existing without-log golden must stay byte-for-byte compatible.
 golden(t,"end_ok_text.json",Result(Run{End:EndOK,BytesOut:12,Isolation:IsolationStandard},[]byte("12 geprüft.\n")))
 if Version!=1{t.Fatal("additive log changed contract version")}
}
