package service

import "testing"

func TestAuthoritativeChangeDistinguishesZeroFromMissing(t *testing.T) {
	scores := map[string]float64{"s1": 0, "s2": 2.625}

	if value, ok := authoritativeChange(scores, "s1"); !ok || value != "0" {
		t.Fatalf("real zero score = %q, %v; want 0, true", value, ok)
	}
	if value, ok := authoritativeChange(scores, "s2"); !ok || value != "2.625" {
		t.Fatalf("non-zero score = %q, %v; want 2.625, true", value, ok)
	}
	if value, ok := authoritativeChange(scores, "missing"); ok || value != "" {
		t.Fatalf("missing score = %q, %v; want empty, false", value, ok)
	}
}
