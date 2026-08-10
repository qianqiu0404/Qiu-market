package researchsignal

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

const goldenFixtureOrigin = "http://127.0.0.1:19095"

// NewGoldenFixtureReader is intentionally non-configurable. It exists only so
// cmd/research-golden can exercise the real adapter against the repository's
// fixed loopback fixture. Arbitrary callers cannot inject an origin.
func NewGoldenFixtureReader() (Reader, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return newGoldenReader(goldenFixtureOrigin, transport)
}

// NewLoopbackGoldenFixtureReader is the production-inaccessible full-stack
// verification seam. It accepts only an exact credential-free HTTP origin on
// an IP loopback address; paths, queries, redirects and proxies remain owned
// and revalidated by the adapter.
func NewLoopbackGoldenFixtureReader(origin string, caPEM []byte, now func() time.Time) (Reader, error) {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("research golden origin must be an exact loopback HTTP origin")
	}
	host, port, splitErr := net.SplitHostPort(parsed.Host)
	ip := net.ParseIP(host)
	if splitErr != nil || port == "" || ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("research golden origin must use an explicit IP loopback address")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	roots := x509.NewCertPool()
	if len(caPEM) == 0 || !roots.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("research golden fixture CA is required")
	}
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	return newGoldenReaderWithClock(origin, transport, now)
}

func newGoldenReader(origin string, transport *http.Transport) (Reader, error) {
	return newGoldenReaderWithClock(origin, transport, nil)
}

func newGoldenReaderWithClock(origin string, transport *http.Transport, now func() time.Time) (Reader, error) {
	client := &http.Client{
		Transport: transport,
		Timeout:   requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("redirects are forbidden")
		},
	}
	result, err := newTestClient(origin, client, now)
	if err != nil {
		return nil, err
	}
	result.cache = newResponseCache(cacheEntries, 100*time.Millisecond)
	return result, nil
}
