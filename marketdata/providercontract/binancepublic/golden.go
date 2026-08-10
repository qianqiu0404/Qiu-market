package binancepublic

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"time"

	"github.com/the-web3/s78-market-services/marketdata/providercontract"
)

// NewLoopbackGoldenReader is restricted to the full-stack verification
// process. The normal client still owns all operation/path/query validation;
// this seam only replaces the allowlisted origin with an exact loopback HTTP
// fixture and disables proxies and redirects.
func NewLoopbackGoldenReader(origin string, caPEM []byte, clock providercontract.Clock, sink ObservationSink) (*Reader, error) {
	roots := x509.NewCertPool()
	if len(caPEM) == 0 || !roots.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("Binance golden fixture CA is required")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	client, err := newTestClient(origin, transport, clientOptions{Clock: clock, Sink: sink})
	if err != nil {
		return nil, err
	}
	client.httpClient.Timeout = 250 * time.Millisecond
	reader, err := newReaderWithTransport(Config{Enabled: true, Clock: clock, CacheTTL: time.Nanosecond, ObservationSink: sink}, client)
	if err != nil {
		client.httpClient.CloseIdleConnections()
		return nil, err
	}
	if reader == nil {
		return nil, errors.New("nil Binance golden reader")
	}
	return reader, nil
}
