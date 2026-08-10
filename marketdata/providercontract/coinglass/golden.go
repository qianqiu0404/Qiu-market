package coinglass

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"time"

	"github.com/the-web3/s78-market-services/marketdata/providercontract"
)

type goldenSecretProvider struct{}

func (goldenSecretProvider) APIKey(context.Context) ([]byte, error) {
	return []byte("full-stack-fixture-ephemeral"), nil
}

// NewLoopbackGoldenReader exercises the credential-header and official schema
// path against an isolated loopback fixture. The built-in ephemeral value is
// never returned, logged or accepted from caller configuration.
func NewLoopbackGoldenReader(origin string, caPEM []byte, clock providercontract.Clock, sink ObservationSink) (*Reader, error) {
	roots := x509.NewCertPool()
	if len(caPEM) == 0 || !roots.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("CoinGlass golden fixture CA is required")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	secret := goldenSecretProvider{}
	client, err := newTestClient(origin, transport, clientOptions{Clock: clock, Sink: sink, SecretProvider: secret})
	if err != nil {
		return nil, err
	}
	client.httpClient.Timeout = 250 * time.Millisecond
	reader, err := newReaderWithTransport(Config{Enabled: true, Clock: clock, CacheTTL: time.Nanosecond, ObservationSink: sink, SecretProvider: secret}, client)
	if err != nil {
		client.httpClient.CloseIdleConnections()
		return nil, err
	}
	if reader == nil {
		return nil, errors.New("nil CoinGlass golden reader")
	}
	return reader, nil
}
