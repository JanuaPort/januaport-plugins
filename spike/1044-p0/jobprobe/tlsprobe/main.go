// Spike JanuaPort/januaport#1044 P0, Sonde go-tls-box-ca: Test-CA, HTTPS-Server
// und Go-Clients mit/ohne zusaetzliche CA. Kein Produktcode, nur Standardbibliothek.
//
//	tlsprobe gen   <dir>                 CA + Server-Zertifikat nach <dir> schreiben
//	tlsprobe serve <dir> <addr>          HTTPS-Server (blockiert)
//	tlsprobe client <url> <modus> [ca]   modus: system | system+ca | neuerpool+ca
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "FEHLER:", err)
		os.Exit(1)
	}
}

func writePEM(path, typ string, der []byte, mode os.FileMode) {
	must(os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), mode))
}

func gen(dir string) {
	must(os.MkdirAll(dir, 0o700))
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(err)
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Spike Box CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	must(err)
	caCert, err := x509.ParseCertificate(caDER)
	must(err)
	writePEM(filepath.Join(dir, "ca.pem"), "CERTIFICATE", caDER, 0o644)

	srvKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(err)
	srvTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost", "box.test"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	srvDER, err := x509.CreateCertificate(rand.Reader, srvTmpl, caCert, &srvKey.PublicKey, caKey)
	must(err)
	keyDER, err := x509.MarshalECPrivateKey(srvKey)
	must(err)
	writePEM(filepath.Join(dir, "server.pem"), "CERTIFICATE", srvDER, 0o644)
	writePEM(filepath.Join(dir, "server.key"), "EC PRIVATE KEY", keyDER, 0o600)
	fmt.Println("gen ok")
}

func serve(dir, addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	must(srv.ListenAndServeTLS(filepath.Join(dir, "server.pem"), filepath.Join(dir, "server.key")))
}

func client(url, mode, caPath string) {
	var pool *x509.CertPool
	switch mode {
	case "system", "system+ca":
		var err error
		pool, err = x509.SystemCertPool()
		must(err)
	case "neuerpool+ca":
		pool = x509.NewCertPool()
	default:
		must(fmt.Errorf("unbekannter Modus %q", mode))
	}
	if mode != "system" {
		pemBytes, err := os.ReadFile(caPath)
		must(err)
		if !pool.AppendCertsFromPEM(pemBytes) {
			must(fmt.Errorf("CA-PEM nicht lesbar"))
		}
	}
	c := &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}},
	}
	resp, err := c.Get(url)
	if err != nil {
		fmt.Println("TLSFEHLER:", err)
		os.Exit(3)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	fmt.Printf("OK status=%d body=%s\n", resp.StatusCode, b)
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: tlsprobe gen|serve|client ...")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "gen":
		gen(os.Args[2])
	case "serve":
		serve(os.Args[2], os.Args[3])
	case "client":
		ca := ""
		if len(os.Args) > 4 {
			ca = os.Args[4]
		}
		client(os.Args[2], os.Args[3], ca)
	default:
		os.Exit(2)
	}
}
