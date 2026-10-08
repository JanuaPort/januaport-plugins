package frame

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"testing"
)

func TestWriteReadRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, Hello{Type: TypeHello, Instance: "abc", Isolation: "standard", Seccomp: 2}); err != nil {
		t.Fatal(err)
	}
	if got := binary.BigEndian.Uint32(buf.Bytes()[:4]); int(got) != buf.Len()-4 {
		t.Fatalf("Längenpräfix %d, Nutzlast %d", got, buf.Len()-4)
	}
	payload, err := Read(&buf, MaxFromSandbox)
	if err != nil {
		t.Fatal(err)
	}
	typ, err := TypeOf(payload)
	if err != nil || typ != TypeHello {
		t.Fatalf("TypeOf = %q, %v", typ, err)
	}
}

func TestReadLimits(t *testing.T) {
	tests := []struct {
		name    string
		length  uint32
		body    []byte
		max     int
		wantErr error
	}{
		{"genau an der Grenze", 4, []byte(`{"a"`), 4, nil},
		{"ein Byte zu groß", 5, []byte(`{"a":`), 4, ErrTooLarge},
		{"riesiges Präfix ohne Körper", 0xFFFFFFFF, nil, MaxFromSandbox, ErrTooLarge},
		{"leerer Rahmen", 0, nil, MaxFromSandbox, ErrEmpty},
		{"abgerissen im Körper", 10, []byte(`{"a"`), 100, io.ErrUnexpectedEOF},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			_ = binary.Write(&buf, binary.BigEndian, tt.length)
			buf.Write(tt.body)
			_, err := Read(&buf, tt.max)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestReadEOFBeforeHeader(t *testing.T) {
	_, err := Read(bytes.NewReader(nil), MaxFromSandbox)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want io.EOF", err)
	}
}

func TestMaxFromSandboxMatchesContract(t *testing.T) {
	// Vertrag §7: Rahmen ≤ 1 MiB + 4 KiB.
	if MaxFromSandbox != 1<<20+4<<10 {
		t.Fatalf("MaxFromSandbox = %d", MaxFromSandbox)
	}
}

func TestTypeOfRejectsNonObject(t *testing.T) {
	for _, in := range []string{`[]`, `"hello"`, `{"type":1}`, `not json`} {
		if _, err := TypeOf([]byte(in)); err == nil {
			t.Errorf("TypeOf(%s) ohne Fehler", in)
		}
	}
}

// F3 B2: der Rahmen hello trägt das Profil aus dem Selbsttest. Die Form steht
// als Golden-Datei neben den Vertragsdateien.
func TestHelloGolden(t *testing.T) {
	got, err := json.Marshal(Hello{Type: TypeHello, Instance: "0123456789abcdef0123456789abcdef", Isolation: "gvisor", Seccomp: 2, Profile: "jnpt"})
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("../../contract/golden/frame_hello.json")
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	_ = json.Unmarshal(got, &a)
	if err := json.Unmarshal(want, &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("hello = %s, Golden = %s", got, want)
	}
}
