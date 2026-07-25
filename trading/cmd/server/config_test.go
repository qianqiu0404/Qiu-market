package main

import "testing"

func TestLoopbackAddressValidation(t *testing.T) {
	t.Parallel()
	if !isIPLoopbackAddress("127.0.0.1:9094") ||
		!isIPLoopbackAddress("[::1]:9094") {
		t.Fatal("IP loopback address was rejected")
	}
	for _, address := range []string{
		"localhost:9094",
		"0.0.0.0:9094",
		":9094",
		"192.0.2.1:9094",
	} {
		if isIPLoopbackAddress(address) {
			t.Fatalf("non-explicit-loopback address accepted: %s", address)
		}
	}
}
