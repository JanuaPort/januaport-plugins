// Spike JanuaPort/januaport#1044 P0 Teil 2: winziges, unsigniertes Go-Programm als Stellvertreter fuer die Laeufer-Exe.
// Es haengt eine Zeile an C:\spike\out\hello-ran.txt an und beendet sich. Kein Produktcode.
package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	f, err := os.OpenFile(`C:\spike\out\hello-ran.txt`, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		os.Exit(2)
	}
	defer f.Close()
	fmt.Fprintf(f, "%s hello gelaufen pid=%d args=%s\n", time.Now().Format("15:04:05"), os.Getpid(), strings.Join(os.Args[1:], " "))
}
