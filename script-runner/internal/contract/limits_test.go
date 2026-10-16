package contract

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRunLimits(t *testing.T) {
	const valid = "wall_s=600;output_bytes=2097152;tool_calls=60"
	tests := []struct {
		name   string
		values []string // nil means absent; an empty slice means present without a value
		want   RunLimits
		bad    bool
	}{
		{"valid", []string{valid}, RunLimits{600, 2097152, 60}, false},
		{"v1_without_header", nil, RunLimits{25, 1048576, 50}, false},
		{"ceiling_literal", []string{"wall_s=86400;output_bytes=16777216;tool_calls=10000"}, RunLimits{86400, 16777216, 10000}, false},
		{"clamped", []string{"wall_s=86401;output_bytes=16777217;tool_calls=10001"}, RunLimits{86400, 16777216, 10000}, false},
		{"uint64_max_clamped", []string{"wall_s=18446744073709551615;output_bytes=18446744073709551615;tool_calls=18446744073709551615"}, RunLimits{86400, 16777216, 10000}, false},
		{"empty", []string{""}, RunLimits{}, true},
		{"no_value", []string{}, RunLimits{}, true},
		{"order", []string{"output_bytes=2097152;wall_s=600;tool_calls=60"}, RunLimits{}, true},
		{"missing", []string{"wall_s=600;output_bytes=2097152"}, RunLimits{}, true},
		{"duplicate_pair", []string{valid + ";tool_calls=60"}, RunLimits{}, true},
		{"duplicate_instead_of_pair", []string{"wall_s=600;wall_s=600;tool_calls=60"}, RunLimits{}, true},
		{"unknown", []string{strings.Replace(valid, "wall_s", "wall", 1)}, RunLimits{}, true},
		{"comma", []string{strings.ReplaceAll(valid, ";", ",")}, RunLimits{}, true},
		{"trailing_separator", []string{valid + ";"}, RunLimits{}, true},
		{"leading_space", []string{" " + valid}, RunLimits{}, true},
		{"trailing_space", []string{valid + " "}, RunLimits{}, true},
		{"inner_space", []string{strings.Replace(valid, ";", "; ", 1)}, RunLimits{}, true},
		{"tab", []string{strings.Replace(valid, ";", ";\t", 1)}, RunLimits{}, true},
		{"newline", []string{valid + "\n"}, RunLimits{}, true},
		{"multiple_headers", []string{valid, valid}, RunLimits{}, true},
		{"joined_headers", []string{valid + ", " + valid}, RunLimits{}, true},
	}
	// Each number must independently reject bad syntax and overflow.
	for _, key := range []string{"wall_s", "output_bytes", "tool_calls"} {
		for _, value := range []string{"0", "-1", "x", "", "+1", "1.0", "1e2", "18446744073709551616", "99999999999999999999999999999999999999999", "１"} {
			parts := []string{"wall_s=600", "output_bytes=2097152", "tool_calls=60"}
			for i, part := range parts {
				if strings.HasPrefix(part, key+"=") {
					parts[i] = key + "=" + value
				}
			}
			tests = append(tests, struct {
				name   string
				values []string
				want   RunLimits
				bad    bool
			}{key + "/" + value, []string{strings.Join(parts, ";")}, RunLimits{}, true})
		}
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := http.Header{}
			h.Set(HeaderRunToken, "jnptrun_"+strings.Repeat("A", 43))
			h.Set(HeaderRunID, "0b1e4c8a-6f2d-4b7e-9a31-2c5d7e8f9a10")
			if tt.values != nil {
				h[http.CanonicalHeaderKey(HeaderRunLimits)] = tt.values
			}
			rh, err := ParseRunHeaders(h)
			if tt.bad {
				if err == nil || err.Error() != "contract: Lauf-Grenzen sind falsch geformt" {
					t.Fatalf("err = %v", err)
				}
				if rh != (RunHeaders{}) {
					t.Fatalf("partially accepted headers: %+v", rh)
				}
				return
			}
			if err != nil || rh.Limits != tt.want {
				t.Fatalf("limits = %+v, err = %v; want %+v", rh.Limits, err, tt.want)
			}
		})
	}
}

func TestRunnerContractTwoLiteral(t *testing.T) {
	if Version != 2 || ToolsList(nil, IsolationStandard, nil).Meta[MetaRunner].(RunnerMeta).Contract != 2 {
		t.Fatal("runner must advertise contract 2")
	}
	if MaxWallS != 86400 || MaxOutputBytes != 16777216 || MaxToolCalls != 10000 {
		t.Fatal("technical ceilings changed (SEC L8)")
	}
	if MaxInputBytes != 262144 {
		t.Fatal("input ceiling changed")
	}
}

func TestRunHeadersGoldenV1AndV2(t *testing.T) {
	for _, tt := range []struct {
		file string
		want RunLimits
	}{
		{"call_headers_v1.json", RunLimits{25, 1048576, 50}},
		{"call_headers.json", RunLimits{600, 2097152, 60}},
	} {
		t.Run(tt.file, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join(goldenDir, tt.file))
			if err != nil {
				t.Fatal(err)
			}
			var values map[string]string
			if err := json.Unmarshal(b, &values); err != nil {
				t.Fatal(err)
			}
			h := http.Header{}
			for key, value := range values {
				h.Set(key, value)
			}
			rh, err := ParseRunHeaders(h)
			if err != nil || rh.Limits != tt.want {
				t.Fatalf("headers=%+v err=%v", rh, err)
			}
		})
	}
}

func TestRunLimitsRejectDuplicateHeaderCasing(t *testing.T) {
	h := http.Header{}
	h.Set(HeaderRunToken, "jnptrun_"+strings.Repeat("A", 43))
	h.Set(HeaderRunID, "0b1e4c8a-6f2d-4b7e-9a31-2c5d7e8f9a10")
	h.Set(HeaderRunLimits, "wall_s=25;output_bytes=1048576;tool_calls=50")
	h["x-jnpt-run-limits"] = []string{"wall_s=1;output_bytes=10;tool_calls=1"}
	if _, err := ParseRunHeaders(h); err == nil {
		t.Fatal("duplicate casing accepted")
	}
}
