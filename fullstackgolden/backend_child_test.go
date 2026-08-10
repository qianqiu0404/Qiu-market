package fullstackgolden

import "testing"

func TestBackendChildConfigRequiresPrivatePostgresAndLoopbackGRPC(t *testing.T) {
	t.Parallel()
	valid := BackendChildConfig{
		PostgresURL: "postgresql://127.0.0.1:5432/qiu_fullstack?sslmode=disable",
		GRPCAddress: "127.0.0.1:18081",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	for name, config := range map[string]BackendChildConfig{
		"missing postgres": {GRPCAddress: valid.GRPCAddress},
		"hostname":         {PostgresURL: valid.PostgresURL, GRPCAddress: "localhost:18081"},
		"wildcard":         {PostgresURL: valid.PostgresURL, GRPCAddress: "0.0.0.0:18081"},
		"missing port":     {PostgresURL: valid.PostgresURL, GRPCAddress: "127.0.0.1"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := config.Validate(); err == nil {
				t.Fatal("accepted unsafe child config")
			}
		})
	}
}
