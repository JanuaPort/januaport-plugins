package contract

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// RunLimits sind die geprüften und auf die technischen Decken gekürzten
// Vorgaben des Gateways. Wandzeit in Sekunden, Ausgabe in Bytes.
type RunLimits struct {
	WallS       int
	OutputBytes int
	ToolCalls   int
}

var runLimitsRe = regexp.MustCompile(`^wall_s=([0-9]+);output_bytes=([0-9]+);tool_calls=([0-9]+)$`)

func parseRunLimits(h http.Header) (RunLimits, error) {
	limits := RunLimits{DefaultWallS, DefaultOutputBytes, DefaultToolCalls}
	var values []string
	present := false
	invalid := errors.New("contract: Lauf-Grenzen sind falsch geformt")
	// HTTP normalisiert Namen. Auch direkte Adapter mit abweichender
	// Schreibweise dürfen doppelte Kopfzeilen nicht verstecken.
	for key, v := range h {
		if strings.EqualFold(key, HeaderRunLimits) {
			if present {
				return RunLimits{}, invalid
			}
			present = true
			values = append(values, v...)
		}
	}
	if !present {
		return limits, nil
	}
	if len(values) != 1 {
		return RunLimits{}, invalid
	}
	parts := runLimitsRe.FindStringSubmatch(values[0])
	if parts == nil {
		return RunLimits{}, invalid
	}
	ceilings := [...]uint64{MaxWallS, MaxOutputBytes, MaxToolCalls}
	var parsed [3]int
	for i := range parsed {
		n, err := strconv.ParseUint(parts[i+1], 10, 64)
		if err != nil || n == 0 {
			return RunLimits{}, invalid
		}
		// Vor der Umwandlung in int kürzen: auch auf 32 Bit kein Überlauf.
		parsed[i] = int(min(n, ceilings[i]))
	}
	return RunLimits{parsed[0], parsed[1], parsed[2]}, nil
}
