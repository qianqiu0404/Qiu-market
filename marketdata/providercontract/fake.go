package providercontract

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ManualClock is deterministic and safe for concurrent router/cache tests.
type ManualClock struct {
	mu  sync.Mutex
	now time.Time
}

func NewManualClock(now time.Time) *ManualClock {
	return &ManualClock{now: now.UTC()}
}

func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *ManualClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}

func (c *ManualClock) Set(value time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = value.UTC()
}

type FakeStep struct {
	Response Response
	Err      error
}

// FakeProvider consumes request-specific scripts in insertion order. It never
// reads the environment or performs network I/O.
type FakeProvider struct {
	mu       sync.Mutex
	identity ProviderIdentity
	scripts  map[string][]FakeStep
	calls    []Request
}

func NewFakeProvider(identity ProviderIdentity) *FakeProvider {
	identity.Capabilities = append([]Capability(nil), identity.Capabilities...)
	return &FakeProvider{identity: identity, scripts: make(map[string][]FakeStep)}
}

func (p *FakeProvider) Identity() ProviderIdentity {
	p.mu.Lock()
	defer p.mu.Unlock()
	identity := p.identity
	identity.Capabilities = append([]Capability(nil), identity.Capabilities...)
	return identity
}

func (p *FakeProvider) Capabilities() []Capability {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Capability(nil), p.identity.Capabilities...)
}

func (p *FakeProvider) Script(request Request, steps ...FakeStep) error {
	normalized, err := NormalizeRequest(request)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	cloned := make([]FakeStep, len(steps))
	for index, step := range steps {
		step.Response = cloneResponse(step.Response)
		cloned[index] = step
	}
	p.scripts[requestCacheKey(normalized)] = cloned
	return nil
}

func (p *FakeProvider) Fetch(ctx context.Context, request Request) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, request)
	key := requestCacheKey(request)
	steps := p.scripts[key]
	if len(steps) == 0 {
		return Response{}, NewError(ErrorUnsupported, p.identity.ID, "fake_fetch", errors.New("no scripted response"))
	}
	step := steps[0]
	p.scripts[key] = steps[1:]
	return cloneResponse(step.Response), step.Err
}

func (p *FakeProvider) Calls() []Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make([]Request, len(p.calls))
	for index, request := range p.calls {
		request.Parameters = append([]Parameter(nil), request.Parameters...)
		result[index] = request
	}
	return result
}

func (p *FakeProvider) Remaining(request Request) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.scripts[requestCacheKey(request)])
}
