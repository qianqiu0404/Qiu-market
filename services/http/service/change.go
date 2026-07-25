package service

import (
	"strconv"
	"strings"
)

// authoritativeChange keeps a real zero distinct from a missing Redis member.
// PostgreSQL radio is intentionally not consulted here because the legacy
// worker can overwrite it with a different daily-change definition.
func authoritativeChange(scores map[string]float64, symbolGuid string) (string, bool) {
	score, ok := scores[symbolGuid]
	if !ok {
		return "", false
	}
	return strconv.FormatFloat(score, 'f', -1, 64), true
}

// canonicalChange uses Redis when present and the canonical nullable
// PostgreSQL column as the fallback. The legacy radio field is never read.
func canonicalChange(scores map[string]float64, symbolGuid string, fallback *string) (string, bool, string) {
	if value, ok := authoritativeChange(scores, symbolGuid); ok {
		return value, true, "redis"
	}
	if fallback == nil || strings.TrimSpace(*fallback) == "" {
		return "", false, "unavailable"
	}
	return strings.TrimSpace(*fallback), true, "postgres"
}
