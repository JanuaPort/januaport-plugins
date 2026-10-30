package client

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

type LogCode uint8

const (
	LogStarted LogCode = iota + 1
	LogSyncFailed
	LogRefused
	LogCompleted
	LogResultDropped // n = bisher verworfene Ergebnisse (Verdrängung und abgelehnte)
	LogTempLeft      // Rest eines Laufverzeichnisses blieb liegen, der Besen räumt beim Start
)

type Logger struct {
	Root string
	mu   sync.Mutex
}

func (l *Logger) Write(code LogCode, n uint64) error {
	if code < LogStarted || code > LogTempLeft {
		return ErrProtocol
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	line := []byte(fmt.Sprintf("%d %d\n", code, n))
	p := filepath.Join(l.Root, "runner.log")
	if info, e := os.Stat(p); e == nil && info.Size()+int64(len(line)) > 1<<20 {
		for i := 4; i >= 1; i-- {
			to := p + "." + strconv.Itoa(i)
			from := p
			if i > 1 {
				from = p + "." + strconv.Itoa(i-1)
			}
			if i == 4 {
				if e = os.Remove(to); e != nil && !os.IsNotExist(e) {
					return e
				}
			}
			if e = os.Rename(from, to); e != nil && !os.IsNotExist(e) {
				return e
			}
		}
	}
	f, e := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(line)
	ce := f.Close()
	if e != nil {
		return e
	}
	return ce
}
