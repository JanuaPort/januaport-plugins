package main

import (
	"net/http"
	"testing"
	"time"

	"github.com/januaport/januaport-plugins/agent-runner/internal/runner"
)

// JanuaPort/januaport#1431: Die Verdrahtung von Umgebung über Wandzeit zu den
// Fristen des HTTP-Servers, ohne main() zu starten.
func TestNewHTTPServerTimeouts(t *testing.T) {
	for _, tc := range []struct {
		env      string
		wantWall time.Duration
	}{
		{"", 300 * time.Second},
		{"0", 300 * time.Second},
		{"1", 1 * time.Second},
		{"300", 300 * time.Second},
	} {
		t.Run(tc.env, func(t *testing.T) {
			wall, err := runner.ParseWallS(tc.env)
			if err != nil {
				t.Fatal(err)
			}
			s := newHTTPServer(http.NewServeMux(), wall)
			if s.WriteTimeout < s.ReadTimeout+tc.wantWall+runner.CleanupMargin {
				t.Fatalf("WriteTimeout %v kürzer als Lesen %v + Wandzeit %v + Aufräumen %v", s.WriteTimeout, s.ReadTimeout, tc.wantWall, runner.CleanupMargin)
			}
			if s.ReadTimeout != runner.ServerReadTimeout {
				t.Fatalf("ReadTimeout %v weicht von ServerReadTimeout ab", s.ReadTimeout)
			}
		})
	}
}

func TestInvalidWallFailsBeforeServer(t *testing.T) {
	for _, env := range []string{"301", "-3", "x"} {
		if _, err := runner.ParseWallS(env); err == nil {
			t.Errorf("%q wurde angenommen", env)
		}
	}
}
