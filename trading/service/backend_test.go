package service

import "testing"

func TestValidateConfigRequiresLoopback(t *testing.T) {
	t.Parallel()
	valid := Config{
		PostgresURL: "postgres://example.invalid/s78",
		GRPCAddress: "127.0.0.1:9094",
	}
	if err := validateConfig(valid); err != nil {
		t.Fatal(err)
	}
	valid.GRPCAddress = "0.0.0.0:9094"
	if err := validateConfig(valid); err == nil {
		t.Fatal("accepted non-loopback trading listener")
	}
}

func TestRandomRequestPrefixIsUnique(t *testing.T) {
	t.Parallel()
	first, err := randomRequestPrefix()
	if err != nil {
		t.Fatal(err)
	}
	second, err := randomRequestPrefix()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("demo-maker request prefix repeated")
	}
}
