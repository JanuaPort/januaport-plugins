// Package frame ist das Rahmenprotokoll zwischen vermittler und Wächter
// (Vertrag §7, Weg C): 4 Byte Länge big-endian, dann UTF-8-JSON mit `type`.
// Dieselbe Rahmung nutzt der Wächter zum Python-Bootstrap (fd 3).
package frame

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// MaxFromSandbox ist die Grenze je Rahmen, die der vermittler beim Lesen
// durchsetzt (Vertrag §7, M4): 1 MiB Nutzlast plus 4 KiB Umschlag.
const MaxFromSandbox = 1<<20 + 4<<10

var (
	// ErrTooLarge heißt: das Längenpräfix übersteigt die Grenze. Der Körper
	// wurde nicht gelesen; der Strom ist danach nicht mehr synchron.
	ErrTooLarge = errors.New("frame: Rahmen zu groß")
	// ErrEmpty heißt: Länge 0. Ein leerer Rahmen ist nie gültiges JSON.
	ErrEmpty = errors.New("frame: leerer Rahmen")
)

// Rahmentypen. Richtung Wächter → vermittler: hello, tool_call, output,
// exited. Richtung vermittler → Wächter: job, tool_result. Nur zwischen
// Wächter und Bootstrap: error.
const (
	TypeHello      = "hello"
	TypeJob        = "job"
	TypeToolCall   = "tool_call"
	TypeToolResult = "tool_result"
	TypeOutput     = "output"
	TypeExited     = "exited"
	TypeError      = "error"
)

// Hello meldet eine frische Sandbox-Instanz (Vertrag §5, V1).
type Hello struct {
	Type      string `json:"type"`
	Instance  string `json:"instance"`
	Isolation string `json:"isolation"`
	Seccomp   int    `json:"seccomp"`
}

// File ist eine Datei des gepinnten Baums, Pfad relativ zum Pin-Ordner.
type File struct {
	Path    string `json:"path"`
	DataB64 string `json:"data_b64"`
}

// Job ist genau ein Lauf.
type Job struct {
	Type  string          `json:"type"`
	Files []File          `json:"files"`
	Entry string          `json:"entry"`
	Input json.RawMessage `json:"input"`
}

// ToolCall ist ein innerer Werkzeugaufruf des Skripts.
type ToolCall struct {
	Type      string          `json:"type"`
	ID        int64           `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ToolResult ist die Antwort auf einen ToolCall (ein CallToolResult).
type ToolResult struct {
	Type   string          `json:"type"`
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result"`
}

// Output trägt einen Abschnitt der Standardausgabe des Skripts.
type Output struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

// Exited meldet das Ende des Skriptprozesses. Signal ist gesetzt, wenn ein
// Signal den Prozess beendet hat; Code ist dann -1.
type Exited struct {
	Type      string `json:"type"`
	Code      int    `json:"code"`
	Signal    int    `json:"signal,omitempty"`
	ErrorType string `json:"error_type,omitempty"`
	ErrorAt   string `json:"error_at,omitempty"`
}

// ScriptError meldet der Bootstrap vor dem Ende einer Ausnahme.
type ScriptError struct {
	Type      string `json:"type"`
	ErrorType string `json:"error_type"`
	ErrorAt   string `json:"error_at,omitempty"`
}

// Write schreibt v als einen Rahmen.
func Write(w io.Writer, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	buf := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(buf, uint32(len(body)))
	copy(buf[4:], body)
	_, err = w.Write(buf)
	return err
}

// Read liest einen Rahmen mit höchstens max Byte Nutzlast. Die Grenze wird
// vor dem Allozieren geprüft.
func Read(r io.Reader, max int) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 {
		return nil, ErrEmpty
	}
	if uint64(n) > uint64(max) {
		return nil, ErrTooLarge
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return body, nil
}

// TypeOf liest das Feld `type` eines Rahmens.
func TypeOf(payload []byte) (string, error) {
	var env struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		return "", fmt.Errorf("frame: kein JSON-Objekt mit type: %w", err)
	}
	if env.Type == "" {
		return "", errors.New("frame: type fehlt")
	}
	return env.Type, nil
}
