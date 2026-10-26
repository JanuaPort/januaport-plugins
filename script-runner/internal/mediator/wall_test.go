package mediator

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/januaport/januaport-plugins/script-runner/internal/contract"
)

func TestMaxWallReportAtAdapter(t *testing.T) {
	for _, seconds := range []int{0, 1, 300, 86400} {
		t.Run(strconv.Itoa(seconds), func(t *testing.T) {
			e := newEnv(t, nil, func(c *Config) { c.MaxWallS = seconds })
			// Read the same JSON shape as cmd/jnpt.parseRunnerMeta, including
			// with no sandbox/pins and therefore invalid isolation.
			b, err := json.Marshal(e.listTools())
			if err != nil {
				t.Fatal(err)
			}
			var wire struct {
				Meta map[string]struct {
					Contract int             `json:"contract"`
					MaxWallS json.RawMessage `json:"max_wall_s"`
				} `json:"_meta"`
			}
			if err := json.Unmarshal(b, &wire); err != nil {
				t.Fatal(err)
			}
			runner := wire.Meta[contract.MetaRunner]
			var got int
			if err := json.Unmarshal(runner.MaxWallS, &got); err != nil {
				t.Fatalf("max_wall_s missing or not an integer: %s", b)
			}
			want := seconds
			if want == 0 {
				want = 86400
			}
			if got != want || runner.Contract != 2 {
				t.Fatalf("report=%s want wall=%d contract=2", b, want)
			}
		})
	}
}

func TestInvalidMaxWallConfig(t *testing.T) {
	for _, seconds := range []int{-1, 86401} {
		_, err := New(Config{SocketPath: "unused", KeyDir: t.TempDir(), StoreDir: t.TempDir(), GatewayURL: "http://gateway", MaxWallS: seconds})
		if err == nil || err.Error() != "mediator: max_wall_s nicht in 1..86400" {
			t.Fatalf("max=%d err=%v", seconds, err)
		}
	}
}
