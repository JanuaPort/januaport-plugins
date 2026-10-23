// Kopierte Kollisionsprobe aus contract/contract_test.go; TestCopiedCollisionProbe prueft identische Zeilen.
package client

import (
	"encoding/base64"
	"testing"
)

func TestSHA1Collision(t *testing.T) {
	// SHA-mbles, 640-Byte-Kollisionsprobe aus sha1cd v0.7.0:
	// https://github.com/pjbgf/sha1cd/blob/v0.7.0/test/testdata/files/sha-mbles-1.bin
	const collision = "mQQNBH/oF4ABIAD/S2V5IGlzIHBhcnQgb2YgYSBjb2xsaXNpb24hIEl0J3MgYSB0cmFwIXnGGvCvzAVFFdknTnMHYksdx/sjmIu43otXXbp7nqsxwWdLbZdDeKgncy/1hRx2ouYHcrWkfOHqxAu5k8EtjHDiSk+NX83twbMsnPGeMa8kKXWdQuTf2zFxn1h2I+5VKTm23NxFn8pTVTtw+H7eMKJH6jr2x1mi8gsyDXYNtk/0eQhP08yzzdSDYtlqnEMGF8r/bDbGN+U/3ihBf2Jv7FTteUOkbl9XMPK7OPsd9uAJABDQDiSteL+SZBmTYI6NFYp4nzTEb+HmAn81pMv7gnB2xQ7KDot8ymm7LCt5Aln5v5Vw3Y1EN6MRX6/3w8rAmtJSZgVcJxBHVReOrv+CWiyqKs+13mTOdkHcWaVBqfycdWdW4uI9xxPIwkyXkKprDjin9V8URSocooUN3ZVi/ZoYrUJJaqlwCPdGcvaO9GHriLCZM9YmtPkYdJzAJ/3dbEJfxCFoNdATTRUoW6sst4Sk98u0+1FNS/D2I3zwCp6fEyuaBm5v0X9sQph0eFhv9lGvlnR/tCa5hyuaiOQGP1m7M0zABlD4OoDEJ1G3GXTTAPwoGaLo8eMsG1HLGOa/xNubrvZ11Kr1sVdKBH+PbdLsFTqTQSKTl02Sj4jO2TY8/vl84udCvzTJa47zh1Z2/qXMqOX33qC6skE9TeAO5x7gHxYr220er9kl5q66rmo1TvF88gWkBPvbEvxFTUH92VzyRZZkoq0DLR2mCnMmQHXX8eDWwUA656DYYd8/5XBxiN1eB9FYm5+LZjBVP4/DUrPgwn2oC926TGQCDQ=="
	b, err := base64.StdEncoding.DecodeString(collision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := digest("sha1", b); err == nil {
		t.Fatal("SHA-1-Kollision akzeptiert")
	}
	if _, err := digest("sha256", []byte("harmlos")); err != nil {
		t.Fatal(err)
	}
}
