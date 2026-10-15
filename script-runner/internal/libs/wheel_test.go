package libs

import (
	"testing"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
)

func TestParseWheelNameAccepts(t *testing.T) {
	tests := []struct {
		file, name, version string
	}{
		{"numpy-2.5.4-cp312-cp312-manylinux_2_27_x86_64.manylinux_2_28_x86_64.whl", "numpy", "2.5.4"},
		{"pandas-3.0.6-cp312-cp312-manylinux_2_24_x86_64.manylinux_2_28_x86_64.whl", "pandas", "3.0.6"},
		{"six-1.17.0-py2.py3-none-any.whl", "six", "1.17.0"},
		{"python_dateutil-2.9.0.post0-py2.py3-none-any.whl", "python-dateutil", "2.9.0.post0"},
		{"foo-1.0-py3-none-any.whl", "foo", "1.0"},
		{"foo-1.0-1build-py3-none-any.whl", "foo", "1.0"},
		{"foo-1.0-py39-none-any.whl", "foo", "1.0"},
		{"foo-1.0-py312-none-any.whl", "foo", "1.0"},
		{"cryptography-42.0.0-cp39-abi3-manylinux_2_28_x86_64.whl", "cryptography", "42.0.0"},
		{"cryptography-42.0.0-cp312-abi3-manylinux_2_17_x86_64.manylinux2014_x86_64.whl", "cryptography", "42.0.0"},
		{"x-1-cp312-cp312-manylinux2014_x86_64.whl", "x", "1"},
		{"x-1-cp312-cp312-manylinux2010_x86_64.whl", "x", "1"},
		{"x-1-cp312-cp312-manylinux1_x86_64.whl", "x", "1"},
		{"x-1-cp312-cp312-manylinux_2_5_x86_64.whl", "x", "1"},
		// die Obergrenze ist die glibc des Images (P0: 2.41)
		{"x-1-cp312-cp312-manylinux_2_41_x86_64.whl", "x", "1"},
		{"x-1-py3-none-manylinux_2_28_x86_64.whl", "x", "1"},
		{"x-1-cp312-none-any.whl", "x", "1"},
		{"x-1-cp312-none-manylinux_2_28_x86_64.whl", "x", "1"},
		// pip-Semantik: es genügt ein zulässiges Tag des komprimierten Satzes
		{"x-1-cp312-cp312-win_amd64.manylinux_2_28_x86_64.whl", "x", "1"},
		{"x-1+cpu-cp312-cp312-manylinux_2_28_x86_64.whl", "x", "1+cpu"},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			w, err := ParseWheelName(tt.file)
			if err != nil {
				t.Fatal(err)
			}
			if w.File != tt.file || w.Name != tt.name || w.Version != tt.version {
				t.Fatalf("Wheel = %+v", w)
			}
		})
	}
}

func TestMaxGlibcMinorIsMeasuredValue(t *testing.T) {
	// P0 (CLAUDE.md „Messung #1041 P0“): Debian 13, glibc 2.41. Ein Wechsel
	// des Basis-Images braucht eine neue Messung; die Docker-Sonde vergleicht
	// die Konstante mit dem Image.
	if MaxGlibcMinor != 41 {
		t.Fatalf("MaxGlibcMinor = %d, want 41", MaxGlibcMinor)
	}
}

func TestParseWheelNamePlatform(t *testing.T) {
	for _, file := range []string{
		"x-1-cp312-cp312-win_amd64.whl",
		"x-1-cp312-cp312-win32.whl",
		"x-1-cp312-cp312-macosx_11_0_arm64.whl",
		"x-1-cp312-cp312-macosx_10_9_x86_64.whl",
		"x-1-cp312-cp312-manylinux_2_28_aarch64.whl",
		"x-1-cp312-cp312-manylinux2014_aarch64.whl",
		"x-1-cp312-cp312-manylinux_2_28_i686.whl",
		"x-1-cp312-cp312-musllinux_1_2_x86_64.whl",
		"x-1-cp312-cp312-linux_x86_64.whl",
		"x-1-cp312-cp312-manylinux_2_42_x86_64.whl",
		"x-1-cp312-cp312-manylinux_3_0_x86_64.whl",
		"x-1-cp312-cp312-manylinux_2_x_x86_64.whl",
		"x-1-cp312-cp312-manylinux_2_028_x86_64.whl",
		"x-1-cp312-cp312-manylinux_1_5_x86_64.whl",
		"x-1-cp311-cp311-manylinux_2_28_x86_64.whl",
		"x-1-cp313-cp313-manylinux_2_28_x86_64.whl",
		"x-1-cp313-abi3-manylinux_2_28_x86_64.whl",
		"x-1-cp312-cp312t-manylinux_2_28_x86_64.whl",
		"x-1-cp313-cp313t-manylinux_2_28_x86_64.whl",
		"x-1-pp310-pypy310_pp73-manylinux_2_28_x86_64.whl",
		"x-1-cp27-cp27mu-manylinux1_x86_64.whl",
		"x-1-py2-none-any.whl",
		"x-1-py3-none-win_amd64.whl",
		"x-1-py3-abi3-any.whl",
		"x-1-cp312-cp312-any.whl",
		"x-1-cp312-cp311-manylinux_2_28_x86_64.whl",
		"x-1-ip3-none-any.whl",
	} {
		t.Run(file, func(t *testing.T) {
			_, err := ParseWheelName(file)
			mustHaveReason(t, err, contract.ReasonLibsPlatform)
		})
	}
}

func TestParseWheelNameInvalid(t *testing.T) {
	for _, file := range []string{
		"",
		"numpy-2.5.4.tar.gz",
		"numpy-2.5.4.zip",
		"numpy-2.5.4-py3.8.egg",
		"numpy-2.5.4-cp312-cp312-manylinux_2_28_x86_64.WHL",
		"numpy-2.5.4-cp312-cp312-manylinux_2_28_x86_64.whl.txt",
		"numpy-2.5.4-cp312-manylinux_2_28_x86_64.whl",
		"numpy-cp312-cp312-manylinux_2_28_x86_64",
		"numpy-2.5.4-1-2-cp312-cp312-manylinux_2_28_x86_64.whl",
		"-2.5.4-cp312-cp312-manylinux_2_28_x86_64.whl",
		"numpy--cp312-cp312-manylinux_2_28_x86_64.whl",
		"numpy-2.5.4--cp312-manylinux_2_28_x86_64.whl",
		"numpy-2.5.4-cp312-cp312-.whl",
		"../numpy-2.5.4-cp312-cp312-manylinux_2_28_x86_64.whl",
		"a/numpy-2.5.4-cp312-cp312-manylinux_2_28_x86_64.whl",
		"a\\numpy-2.5.4-cp312-cp312-manylinux_2_28_x86_64.whl",
		"nümpy-2.5.4-cp312-cp312-manylinux_2_28_x86_64.whl",
		"numpy-2.5.4-cp312-cp312-manylinux_2_28_x86_64 .whl",
		"num py-2.5.4-cp312-cp312-manylinux_2_28_x86_64.whl",
		"numpy-2.5.4-cp312--manylinux_2_28_x86_64.whl",
		"numpy-2.5.4-c p312-cp312-manylinux_2_28_x86_64.whl",
		"numpy-2.5.4\x00-cp312-cp312-manylinux_2_28_x86_64.whl",
	} {
		t.Run(file, func(t *testing.T) {
			_, err := ParseWheelName(file)
			mustHaveReason(t, err, contract.ReasonLibsInvalid)
		})
	}
}

func TestNormalizeName(t *testing.T) {
	for in, want := range map[string]string{
		"Python_Dateutil": "python-dateutil", "typing.Extensions": "typing-extensions", "a--b__c..d": "a-b-c-d", "NumPy": "numpy",
	} {
		if got := normalizeName(in); got != want {
			t.Errorf("normalizeName(%q) = %q, want %q", in, got, want)
		}
	}
}
