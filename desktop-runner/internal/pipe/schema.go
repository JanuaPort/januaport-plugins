// Package pipe defines the local, closed control protocol. It has no credentials,
// HTTP adapter or script input types. The codec and question relay run on Linux.
package pipe

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxFrame = 4096

var ErrProtocol = errors.New("pipe_protocol")
var ErrDisturbance = errors.New("Störung")
var ErrOccupied = errors.New("pipe_occupied")

type Setup struct {
	Pin      string `json:"pin"`
	Setup    string `json:"setup"`
	Label    string `json:"label"`
	Startbar bool   `json:"startbar"`
}

// Message is a closed union. Decode checks the exact required/allowed fields of
// each variant before decoding; MarshalJSON emits only that variant's fields.
// Wahl is a zero-based option index. IDs are opaque local correlation IDs.
type Message struct {
	Type            string   `json:"type"`
	Minuten         *int     `json:"minuten"`
	Pin             string   `json:"pin"`
	Setup           string   `json:"setup"`
	ID              string   `json:"id"`
	Wahl            *int     `json:"wahl"`
	Zustand         string   `json:"zustand"`
	Bis             string   `json:"bis"`
	Test            bool     `json:"test"`
	Netz            bool     `json:"netz"`
	Beschaeftigt    bool     `json:"beschaeftigt"`
	FristAbgelaufen bool     `json:"frist_abgelaufen"`
	Setups          []Setup  `json:"setups"`
	Text            string   `json:"text"`
	Optionen        []string `json:"optionen"`
	FristS          int      `json:"frist_s"`
	Code            string   `json:"code"`
}

func ReadFrame(r io.Reader) ([]byte, error) {
	var head [4]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(head[:])
	if n == 0 || n > MaxFrame {
		return nil, ErrProtocol
	}
	b := make([]byte, n)
	_, err := io.ReadFull(r, b)
	return b, err
}

func WriteFrame(w io.Writer, m Message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return ErrProtocol
	}
	if len(b) == 0 || len(b) > MaxFrame {
		return ErrProtocol
	}
	var head [4]byte
	binary.BigEndian.PutUint32(head[:], uint32(len(b)))
	for _, part := range [][]byte{head[:], b} {
		n, err := w.Write(part)
		if err != nil {
			return err
		}
		if n != len(part) {
			return io.ErrShortWrite
		}
	}
	return nil
}

func DecodeClient(b []byte) (Message, error) { return decode(b, false) }
func DecodeServer(b []byte) (Message, error) { return decode(b, true) }

func fields(kind string, server bool) (required, optional string) {
	if !server {
		switch kind {
		case "status", "weiter", "anhalten", "fortsetzen":
			return "type", ""
		case "pause":
			return "type minuten", ""
		case "setup_start":
			return "type pin setup", ""
		case "antwort":
			return "type id wahl", ""
		}
	} else {
		switch kind {
		case "zustand":
			return "type zustand test netz beschaeftigt setups", "bis frist_abgelaufen"
		case "frage":
			return "type id text optionen frist_s", ""
		case "frage_weg":
			return "type id", ""
		case "fehler":
			return "type code", ""
		}
	}
	return "", ""
}

// Reject duplicate keys (also nested), nulls, invalid UTF-8 and trailing values.
// encoding/json otherwise accepts case aliases and last-key-wins documents.
func uniqueValue(d *json.Decoder, depth int) error {
	if depth > 16 {
		return ErrProtocol
	}
	v, err := d.Token()
	if err != nil || v == nil {
		return ErrProtocol
	}
	if delim, ok := v.(json.Delim); ok {
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				k, err := d.Token()
				if err != nil {
					return ErrProtocol
				}
				key, ok := k.(string)
				if !ok || seen[key] {
					return ErrProtocol
				}
				seen[key] = true
				if uniqueValue(d, depth+1) != nil {
					return ErrProtocol
				}
			}
			end, err := d.Token()
			if err != nil || end != json.Delim('}') {
				return ErrProtocol
			}
		case '[':
			for d.More() {
				if uniqueValue(d, depth+1) != nil {
					return ErrProtocol
				}
			}
			end, err := d.Token()
			if err != nil || end != json.Delim(']') {
				return ErrProtocol
			}
		default:
			return ErrProtocol
		}
	}
	return nil
}

func exactKeys(raw map[string]json.RawMessage, required, optional string) bool {
	allowed := map[string]bool{}
	for _, k := range strings.Fields(required + " " + optional) {
		allowed[k] = true
	}
	for k := range raw {
		if !allowed[k] {
			return false
		}
	}
	for _, k := range strings.Fields(required) {
		if _, ok := raw[k]; !ok {
			return false
		}
	}
	return true
}

func decode(b []byte, server bool) (Message, error) {
	var m Message
	if len(b) == 0 || len(b) > MaxFrame || !utf8.Valid(b) {
		return m, ErrProtocol
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if uniqueValue(d, 0) != nil {
		return m, ErrProtocol
	}
	if _, err := d.Token(); err != io.EOF {
		return m, ErrProtocol
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(b, &raw) != nil || json.Unmarshal(raw["type"], &m.Type) != nil {
		return Message{}, ErrProtocol
	}
	req, opt := fields(m.Type, server)
	if req == "" || !exactKeys(raw, req, opt) {
		return Message{}, ErrProtocol
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&m) != nil {
		return Message{}, ErrProtocol
	}
	valid := true
	switch m.Type {
	case "pause":
		valid = m.Minuten != nil && (*m.Minuten == 0 || *m.Minuten == 15 || *m.Minuten == 60)
	case "setup_start":
		valid = name(m.Pin) && name(m.Setup)
	case "antwort":
		valid = name(m.ID) && m.Wahl != nil && *m.Wahl >= 0 && *m.Wahl < 4
	case "frage":
		valid = name(m.ID) && bounded(m.Text, 500) && len(m.Optionen) >= 2 && len(m.Optionen) <= 4 && m.FristS >= 5 && m.FristS <= 3600
		for _, s := range m.Optionen {
			valid = valid && bounded(s, 40)
		}
	case "frage_weg":
		valid = name(m.ID)
	case "fehler":
		valid = m.Code == "protokoll" || m.Code == "stoerung" || m.Code == "abgewiesen"
	case "zustand":
		valid = m.Zustand == "arbeitet" || m.Zustand == "pausiert" || m.Zustand == "angehalten" || m.Zustand == "stoerung"
		if m.Bis != "" {
			at, err := time.Parse(time.RFC3339Nano, m.Bis)
			valid = valid && err == nil && at.Location() == time.UTC && strings.HasSuffix(m.Bis, "Z")
		}
		var rows []map[string]json.RawMessage
		if json.Unmarshal(raw["setups"], &rows) != nil {
			return Message{}, ErrProtocol
		}
		for _, r := range rows {
			valid = valid && exactKeys(r, "pin setup label startbar", "")
		}
		for _, s := range m.Setups {
			valid = valid && name(s.Pin) && name(s.Setup) && bounded(s.Label, 40)
		}
	}
	if !valid {
		return Message{}, ErrProtocol
	}
	return m, nil
}

func name(s string) bool {
	if len(s) < 1 || len(s) > 128 || strings.Contains(s, "..") {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}
func bounded(s string, n int) bool {
	return s != "" && utf8.ValidString(s) && utf8.RuneCountInString(s) <= n && !strings.ContainsRune(s, 0)
}

func (m Message) MarshalJSON() ([]byte, error) {
	v := map[string]any{"type": m.Type}
	server := false
	switch m.Type {
	case "status", "weiter", "anhalten", "fortsetzen":
	case "pause":
		v["minuten"] = m.Minuten
	case "setup_start":
		v["pin"] = m.Pin
		v["setup"] = m.Setup
	case "antwort":
		v["id"] = m.ID
		v["wahl"] = m.Wahl
	case "zustand":
		server = true
		v["zustand"] = m.Zustand
		v["test"] = m.Test
		v["netz"] = m.Netz
		v["beschaeftigt"] = m.Beschaeftigt
		if m.Setups == nil {
			m.Setups = []Setup{}
		}
		v["setups"] = m.Setups
		if m.Bis != "" {
			v["bis"] = m.Bis
		}
		if m.FristAbgelaufen {
			v["frist_abgelaufen"] = true
		}
	case "frage":
		server = true
		v["id"] = m.ID
		v["text"] = m.Text
		v["optionen"] = m.Optionen
		v["frist_s"] = m.FristS
	case "frage_weg":
		server = true
		v["id"] = m.ID
	case "fehler":
		server = true
		v["code"] = m.Code
	default:
		return nil, ErrProtocol
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if _, err = decode(b, server); err != nil {
		return nil, err
	}
	return b, nil
}
