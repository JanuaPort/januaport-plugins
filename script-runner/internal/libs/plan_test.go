package libs

import (
	"testing"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
)

const (
	numpyWhl  = "numpy-2.5.4-cp312-cp312-manylinux_2_27_x86_64.manylinux_2_28_x86_64.whl"
	pandasWhl = "pandas-3.0.6-cp312-cp312-manylinux_2_24_x86_64.manylinux_2_28_x86_64.whl"
	sixWhl    = "six-1.17.0-py2.py3-none-any.whl"
)

func lockOf(entries ...string) []byte {
	var out string
	for _, e := range entries {
		out += e
	}
	return []byte(out)
}

func entryLine(name, version, hash string) string {
	return name + "==" + version + " \\\n    --hash=sha256:" + hash + "\n"
}

func TestNewPlanMatchesOneToOne(t *testing.T) {
	lock := lockOf(entryLine("pandas", "3.0.6", h1), entryLine("numpy", "2.5.4", h2), entryLine("six", "1.17.0", h1))
	p, err := NewPlan(lock, []string{sixWhl, pandasWhl, numpyWhl})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Wheels) != 3 || p.Lock.Hash == "" {
		t.Fatalf("Plan = %+v", p)
	}
	// feste Reihenfolge: nach Dateiname, unabhängig von der Eingabe
	want := []string{numpyWhl, pandasWhl, sixWhl}
	for i, w := range p.Wheels {
		if w.File != want[i] {
			t.Errorf("Wheel %d = %s, want %s", i, w.File, want[i])
		}
	}
	if p.Wheels[0].Entry.Name != "numpy" || p.Wheels[1].Entry.Name != "pandas" {
		t.Errorf("Einträge falsch zugeordnet: %+v", p.Wheels)
	}
}

func TestNewPlanRejects(t *testing.T) {
	both := lockOf(entryLine("numpy", "2.5.4", h1), entryLine("six", "1.17.0", h2))
	tests := []struct {
		name   string
		lock   []byte
		wheels []string
		reason string
	}{
		{"Lock-Eintrag ohne Wheel", both, []string{numpyWhl}, contract.ReasonLibsInvalid},
		{"Wheel ohne Lock-Eintrag", lockOf(entryLine("numpy", "2.5.4", h1)), []string{numpyWhl, sixWhl}, contract.ReasonLibsInvalid},
		{"gar keine Wheels", both, nil, contract.ReasonLibsInvalid},
		{"andere Version", lockOf(entryLine("numpy", "2.5.3", h1)), []string{numpyWhl}, contract.ReasonLibsInvalid},
		{"zwei Wheels für einen Eintrag", lockOf(entryLine("numpy", "2.5.4", h1)),
			[]string{numpyWhl, "numpy-2.5.4-cp312-cp312-manylinux_2_17_x86_64.whl"}, contract.ReasonLibsInvalid},
		{"sdist", lockOf(entryLine("numpy", "2.5.4", h1)), []string{"numpy-2.5.4.tar.gz"}, contract.ReasonLibsInvalid},
		{"sdist neben Wheel", lockOf(entryLine("numpy", "2.5.4", h1)), []string{numpyWhl, "numpy-2.5.4.tar.gz"}, contract.ReasonLibsInvalid},
		{"falsche Plattform", lockOf(entryLine("numpy", "2.5.4", h1)), []string{"numpy-2.5.4-cp312-cp312-manylinux_2_28_aarch64.whl"}, contract.ReasonLibsPlatform},
		// Format und Plattform eines Wheels werden vor dem Abgleich geprüft.
		{"falsche Plattform vor Abgleich", both, []string{"numpy-2.5.4-cp312-cp312-win_amd64.whl"}, contract.ReasonLibsPlatform},
		{"Lock kaputt", []byte("numpy>=1\n"), []string{numpyWhl}, contract.ReasonLibsInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewPlan(tt.lock, tt.wheels)
			mustHaveReason(t, err, tt.reason)
		})
	}
}

func TestNewPlanNormalizesNames(t *testing.T) {
	lock := lockOf(entryLine("Python_Dateutil", "2.9.0.post0", h1))
	p, err := NewPlan(lock, []string{"python_dateutil-2.9.0.post0-py2.py3-none-any.whl"})
	if err != nil || len(p.Wheels) != 1 {
		t.Fatalf("Plan = %+v, err = %v", p, err)
	}
}
