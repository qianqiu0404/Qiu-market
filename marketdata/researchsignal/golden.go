package researchsignal

import (
	"errors"
	"net/http"
	"time"
)

const goldenFixtureOrigin = "http://127.0.0.1:19095"

// NewGoldenFixtureReader is intentionally non-configurable. It exists only so
// cmd/research-golden can exercise the real adapter against the repository's
// fixed loopback fixture. Arbitrary callers cannot inject an origin.
func NewGoldenFixtureReader() (Reader, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	client := &http.Client{
		Transport: transport,
		Timeout:   requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("redirects are forbidden")
		},
	}
	result, err := newTestClient(goldenFixtureOrigin, client, nil)
	if err != nil {
		return nil, err
	}
	result.cache = newResponseCache(cacheEntries, 100*time.Millisecond)
	return result, nil
}
