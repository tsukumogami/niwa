//go:build darwin && niwa_functional

package infisical

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"os"
)

// This file is built only into the functional suite's test binary (`make
// build-test` sets the niwa_functional tag), never into a release build.
//
// The suite's Infisical doubles serve HTTPS with a self-signed certificate and
// hand the niwa subprocess that certificate through SSL_CERT_FILE. Go's
// crypto/x509 reads SSL_CERT_FILE as the root pool on Linux, but on macOS it
// verifies through the platform trust store and ignores the variable, so every
// call to a double failed with "certificate signed by unknown authority". This
// gives this package's client, on macOS, the same reading of SSL_CERT_FILE that
// Linux gives the whole process.
func init() {
	f := os.Getenv("SSL_CERT_FILE")
	if f == "" {
		return
	}
	pem, err := os.ReadFile(f)
	if err != nil {
		return
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return
	}
	// A clone of the default transport, so only the trust pool differs from
	// what a release build uses.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: pool}
	HTTPClient = &http.Client{Transport: transport}
}
