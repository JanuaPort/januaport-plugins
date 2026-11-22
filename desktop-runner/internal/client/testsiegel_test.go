package client

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// #1537 Punkt 1: Das Siegel test-<job_id> (Freigabebindung eines Testauftrags) wird gelöscht, sobald der Kern das
// Testergebnis bestätigt hat und das Journal den Eintrag nicht mehr führt. Vorher bleibt es, denn jede Wiederholung
// der Zustellung braucht es (Invariante 13: Replay prüft erneut).


// testSiegelBett legt Siegel und Testergebnis ab und liefert die Engine gegen einen Fake-Kern, der mit status antwortet.
func testSiegelBett(t *testing.T, status int) (*Engine, *State, string) {
	t.Helper()
	c, order := l1Order(t, true, "test_aaaaaaaa")
	st, err := OpenState(t.TempDir(), testCipher{})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Save("test-"+order.JobID, order); err != nil {
		t.Fatal(err)
	}
	r := Result{JobID: order.JobID, ManifestSeq: order.ManifestSeq, Commit: order.Commit, End: "ok", FinishedAt: "2026-11-01T12:01:00Z", Result: json.RawMessage(`{"wert":42}`)}
	if err = st.Append(r); err != nil {
		t.Fatal(err)
	}
	a := l1API(t, func(w http.ResponseWriter, _ *http.Request) {
		if status >= 400 {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"class":"x","message":"Fehler."}}`))
			return
		}
		w.WriteHeader(status)
	})
	return &Engine{State: st, API: a, Credential: c}, st, filepath.Join(st.Root, "test-"+order.JobID)
}

func TestTestSiegelWirdNachBestaetigtemReportTestGeloescht(t *testing.T) {
	e, st, siegel := testSiegelBett(t, 204)
	e.deliverOne(context.Background())
	if len(st.Journal().Results) != 0 {
		t.Fatal("Ergebnis noch im Journal")
	}
	if _, err := os.Lstat(siegel); !os.IsNotExist(err) {
		t.Errorf("Siegel blieb nach bestätigtem ReportTest liegen: %v", err)
	}
}

func TestTestSiegelBleibtSolangeDasErgebnisNichtBestaetigtIst(t *testing.T) {
	for _, status := range []int{400, 409, 429, 500, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			e, st, siegel := testSiegelBett(t, status)
			e.deliverOne(context.Background())
			if len(st.Journal().Results) != 1 {
				t.Fatal("Ergebnis verworfen")
			}
			if _, err := os.Lstat(siegel); err != nil {
				t.Errorf("Siegel fehlt vor der Bestätigung: %v", err)
			}
		})
	}
}

// Eine Wiederholung nach Neustart bleibt möglich, solange das Journal das Ergebnis noch führt: Scheitert das
// Speichern des Journals nach der Bestätigung, bleibt das Siegel, sonst käme der Replay nie mehr durch.
func TestTestSiegelBleibtWennDasJournalNachDerBestaetigungNichtSpeichert(t *testing.T) {
	e, st, siegel := testSiegelBett(t, 204)
	blocker := filepath.Join(st.Root, "journal")
	if err := os.Remove(blocker); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(blocker, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	e.deliverOne(context.Background())
	if _, err := os.Lstat(siegel); err != nil {
		t.Errorf("Siegel gelöscht, obwohl das Journal das Ergebnis noch führt: %v", err)
	}
}
