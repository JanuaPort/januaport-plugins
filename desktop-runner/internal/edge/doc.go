//go:build windows

// Package edge ist eine festgenagelte Kopie von github.com/wailsapp/go-webview2 v1.0.19 (pkg/edge und
// internal/w32, MIT, siehe LICENSE und NOTICE). Die Kopie ist die Grundlage; Abweichungen vom Original sind
// ausschließlich im Commit "Kopie angepasst" und in der Modul-CLAUDE.md beschrieben: kein os.Exit, keine
// unbegrenzte Nachrichtenschleife, keine Echo- und Fatal-Handler.
package edge
