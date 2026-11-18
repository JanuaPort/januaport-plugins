package frame

import "encoding/json"

// Grenzen des ETL-Rohstroms (Vertragsfassung 3, #1215 P2).
//
// Gemessen (TestETLFullChunkFitsEveryFrameLimit): Ein volles Stück von 256 KiB
// roh ist Base64 349 528 Byte und als Rahmen 349 584 Byte (341,4 KiB). Die
// kleinste Leserschranke auf dem Weg ist MaxFromSandbox (1 MiB + 4 KiB) beim
// vermittler, die des Wächters ist MaxJobFrame (8 MiB): Es passt mit reichlich
// Luft, das Stück bleibt bei 256 KiB.
const (
	// MaxETLChunkRaw ist die Größe eines Stücks in Rohbytes, an beliebiger
	// Bytegrenze geschnitten.
	MaxETLChunkRaw = 256 << 10
	// MaxETLChunkFrame ist die Obergrenze der Rahmen-Nutzlast eines vollen
	// Stücks: Base64 (4/3) plus 4 KiB Umschlag.
	MaxETLChunkFrame = MaxETLChunkRaw/3*4 + 4<<10 + 4
	// MaxETLOpen ist die Obergrenze der Nutzlast eines etl_open-Rahmens.
	MaxETLOpen = 64 << 10
)

// Status in etl_end. eof heißt: Der HTTP-Körper ist zu Ende; ob der Strom
// vollständig war, entscheidet der Endsatz in den Daten, nicht dieser Status.
const (
	ETLStatusEOF         = "eof"
	ETLStatusAbgewiesen  = "abgewiesen"
	ETLStatusAbgebrochen = "abgebrochen"
	ETLStatusGrenze      = "grenze"
	ETLStatusVertrag     = "vertrag"
	ETLStatusGeschlossen = "geschlossen"
)

// ETLOpen öffnet den Strom einer deklarierten Abfrage (Sandbox → vermittler).
// Der vermittler schickt daraus POST /api/etl/stream, das Skript nie selbst.
type ETLOpen struct {
	Type        string          `json:"type"`
	ID          int64           `json:"id"`
	Integration string          `json:"integration"`
	QueryID     string          `json:"query_id"`
	Params      json.RawMessage `json:"params,omitempty"`
}

// ETLNext fordert das nächste Stück an (Sandbox → vermittler). Mit Close
// schließt der Strom; auch darauf antwortet der vermittler mit etl_end.
type ETLNext struct {
	Type  string `json:"type"`
	ID    int64  `json:"id"`
	Close bool   `json:"close,omitempty"`
}

// ETLChunk ist ein Stück des Stroms (vermittler → Sandbox), roh höchstens
// MaxETLChunkRaw Byte, Base64-kodiert.
type ETLChunk struct {
	Type    string `json:"type"`
	ID      int64  `json:"id"`
	DataB64 string `json:"data_b64"`
}

// ETLEnd beendet den Strom (vermittler → Sandbox). Code und RetryAfterS
// stehen nur bei „abgewiesen“ und sind der HTTP-Status und die Zahl der
// Wartesekunden, nie ein Text des Kerns.
type ETLEnd struct {
	Type        string `json:"type"`
	ID          int64  `json:"id"`
	Status      string `json:"status"`
	Code        int    `json:"code,omitempty"`
	RetryAfterS int    `json:"retry_after_s,omitempty"`
}
