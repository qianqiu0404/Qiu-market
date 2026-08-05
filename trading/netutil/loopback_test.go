package netutil

import "testing"

func TestIsIPLoopbackAddress(t *testing.T) {
	t.Parallel()
	for _, address := range []string{"127.0.0.1:9094", "[::1]:9094"} {
		if !IsIPLoopbackAddress(address) {
			t.Fatalf("loopback address rejected: %s", address)
		}
	}
	for _, address := range []string{
		"localhost:9094",
		"0.0.0.0:9094",
		":9094",
		"192.0.2.1:9094",
	} {
		if IsIPLoopbackAddress(address) {
			t.Fatalf("non-explicit-loopback address accepted: %s", address)
		}
	}
}
