package runner

import (
	"testing"
	"time"
)

// JanuaPort/januaport#1431: Die HTTP-Schreibfrist folgt aus der wirksamen
// Wandzeit des Läufers, nie aus der rohen Umgebungsvariable.
func TestParseWallS(t *testing.T) {
	for _, tc := range []struct {
		env     string
		want    int
		wantErr bool
	}{
		{"", DefaultMaxWallS, false},
		{"0", DefaultMaxWallS, false},
		{"1", 1, false},
		{"300", 300, false},
		{"301", 0, true},
		{"-1", 0, true},
		{"abc", 0, true},
		{"30s", 0, true},
		{" 5", 0, true},
	} {
		t.Run(tc.env, func(t *testing.T) {
			got, err := ParseWallS(tc.env)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("ParseWallS(%q) = %d, %v; want %d, error=%v", tc.env, got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestWriteTimeoutCoversReadWallAndCleanup(t *testing.T) {
	for _, wall := range []int{1, 60, DefaultMaxWallS} {
		got := WriteTimeout(wall)
		want := ServerReadTimeout + time.Duration(wall)*time.Second + CleanupMargin
		if got != want {
			t.Errorf("WriteTimeout(%d) = %v, want %v", wall, got, want)
		}
		if got <= ServerReadTimeout+time.Duration(wall)*time.Second {
			t.Errorf("WriteTimeout(%d) = %v lässt keine Luft zum Aufräumen", wall, got)
		}
	}
}

// Ein Lauf wird nach der Wandzeit abgebrochen; danach muss die Antwort noch
// geschrieben werden können. Das gilt auch, wenn die Umgebung 0 meldet.
func TestWriteTimeoutFromEnvZeroUsesEffectiveWall(t *testing.T) {
	wall, err := ParseWallS("0")
	if err != nil {
		t.Fatal(err)
	}
	if got := WriteTimeout(wall); got < time.Duration(DefaultMaxWallS)*time.Second+ServerReadTimeout {
		t.Fatalf("Schreibfrist %v kürzer als Standard-Wandzeit plus Lesefrist", got)
	}
}
