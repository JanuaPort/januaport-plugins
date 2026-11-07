package client

import (
	"bytes"
	"encoding/json"
)

// firstSSEReply liefert die Daten des ersten Ereignisses, das keine Benachrichtigung ist
// (Objekt mit method, ohne id). Alles Weitere, auch eine fremde id, prüft der Aufrufer so
// streng wie bei einer JSON-Antwort. Ein Strom ohne vollständiges Ereignis ist ein
// Protokollfehler; ein Ereignis ohne abschließende Leerzeile ist nicht vollständig (WHATWG).
func firstSSEReply(stream []byte) ([]byte, error) {
	stream = bytes.ReplaceAll(stream, []byte("\r\n"), []byte("\n"))
	stream = bytes.ReplaceAll(stream, []byte("\r"), []byte("\n"))
	var data [][]byte
	for {
		nl := bytes.IndexByte(stream, '\n')
		if nl < 0 {
			return nil, ErrProtocol
		}
		line := stream[:nl]
		stream = stream[nl+1:]
		if len(line) > 0 {
			// Kommentare sowie event-, id- und retry-Zeilen tragen keine Antwort.
			if value, ok := bytes.CutPrefix(line, []byte("data:")); ok {
				data = append(data, bytes.TrimPrefix(value, []byte(" ")))
			}
			continue
		}
		if len(data) == 0 {
			continue
		}
		payload := bytes.Join(data, []byte("\n"))
		data = nil
		var probe struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(payload, &probe) != nil {
			return nil, ErrProtocol
		}
		if probe.Method != "" && len(probe.ID) == 0 {
			continue
		}
		return payload, nil
	}
}
