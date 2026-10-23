// Kopie aus script-runner/internal/libs/extract.go: ZIP-Strukturprüfung. Wächter: TestCopiedLibraryFunctions.
package client

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
)

const maxPathBytes = 1024

// Grenzen für das Zip-Verzeichnis: Das Zip-Paket liest das ganze zentrale
// Verzeichnis in den Speicher, bevor ein Eintrag geprüft ist. Eine Größe pro
// Eintrag und die Zahl der Einträge begrenzen den Speicher auf einige zehn MiB.
const (
	zipRecordLen       = 46
	maxRecordExtra     = 256
	maxRecordComment   = 256
	zipEndRecordLen    = 22
	zip64LocatorLen    = 20
	maxEndRecordSearch = 1 << 16
)

// checkStructure prüft das Ende des Zips und sein zentrales Verzeichnis, bevor
// das Zip-Paket es einliest (SEC B4 e): kein Zip64, keine mehrteiligen
// Archive, höchstens maxEntries Einträge, und das Verzeichnis besteht aus
// genau so vielen lückenlosen Einträgen mit begrenzten Feldern, wie das
// Ende angibt — dann sieht auch das Zip-Paket genau diese und keine weiteren.
func checkStructure(ra io.ReaderAt, size int64, maxEntries int) error {
	pos, rec, err := findEndRecord(ra, size)
	if err != nil {
		return err
	}
	diskNo := binary.LittleEndian.Uint16(rec[4:6])
	diskDir := binary.LittleEndian.Uint16(rec[6:8])
	onDisk := binary.LittleEndian.Uint16(rec[8:10])
	entries := binary.LittleEndian.Uint16(rec[10:12])
	dirSize := binary.LittleEndian.Uint32(rec[12:16])
	dirOff := binary.LittleEndian.Uint32(rec[16:20])
	if entries == 0xFFFF || dirSize == 0xFFFFFFFF || dirOff == 0xFFFFFFFF || onDisk == 0xFFFF {
		return invalid("Zip64")
	}
	if pos >= zip64LocatorLen {
		var loc [4]byte
		if _, err := ra.ReadAt(loc[:], pos-zip64LocatorLen); err == nil && bytes.Equal(loc[:], []byte("PK\x06\x07")) {
			return invalid("Zip64")
		}
	}
	if diskNo != 0 || diskDir != 0 || onDisk != entries || entries == 0 {
		return invalid("Zip mehrteilig oder ohne Einträge")
	}
	if int(entries) > maxEntries {
		return tooLarge("zu viele Einträge im Wheel")
	}
	if int64(dirOff)+int64(dirSize) != pos {
		return invalid("Zip-Verzeichnis liegt nicht lückenlos vor dem Ende")
	}
	br := bufio.NewReaderSize(io.NewSectionReader(ra, int64(dirOff), int64(dirSize)), 64<<10)
	var consumed int64
	var hdr [zipRecordLen]byte
	for i := 0; i < int(entries); i++ {
		if _, err := io.ReadFull(br, hdr[:]); err != nil || !bytes.Equal(hdr[:4], []byte("PK\x01\x02")) {
			return invalid("Zip-Verzeichnis beschädigt")
		}
		name := int64(binary.LittleEndian.Uint16(hdr[28:30]))
		extra := int64(binary.LittleEndian.Uint16(hdr[30:32]))
		comment := int64(binary.LittleEndian.Uint16(hdr[32:34]))
		if name > maxPathBytes+1 || extra > maxRecordExtra || comment > maxRecordComment {
			return invalid("Zip-Verzeichnis mit überlangen Feldern")
		}
		n := name + extra + comment
		if _, err := br.Discard(int(n)); err != nil {
			return invalid("Zip-Verzeichnis beschädigt")
		}
		consumed += zipRecordLen + n
	}
	if consumed != int64(dirSize) {
		return invalid("Zip-Verzeichnis hat mehr oder weniger Einträge als angegeben")
	}
	return nil
}

// findEndRecord sucht das Ende des Zips von hinten wie das Zip-Paket: das
// letzte Vorkommen der Kennung, dessen Kommentarlänge noch in die Datei passt.
func findEndRecord(ra io.ReaderAt, size int64) (int64, []byte, error) {
	if size < zipEndRecordLen {
		return 0, nil, invalid("Wheel ist kein gültiges Zip")
	}
	tail := min(size, zipEndRecordLen+maxEndRecordSearch)
	buf := make([]byte, tail)
	if _, err := ra.ReadAt(buf, size-tail); err != nil && !errors.Is(err, io.EOF) {
		return 0, nil, invalid("Wheel ist kein gültiges Zip")
	}
	for i := len(buf) - zipEndRecordLen; i >= 0; i-- {
		if !bytes.Equal(buf[i:i+4], []byte("PK\x05\x06")) {
			continue
		}
		commentLen := int(binary.LittleEndian.Uint16(buf[i+20 : i+22]))
		if i+zipEndRecordLen+commentLen > len(buf) {
			continue
		}
		return size - tail + int64(i), buf[i : i+zipEndRecordLen], nil
	}
	return 0, nil, invalid("Wheel ist kein gültiges Zip")
}
