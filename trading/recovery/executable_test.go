package recovery

import (
	"strings"
	"testing"
)

func TestBindExecutableSourceDigestFillsAndVerifiesExactBinary(t *testing.T) {
	actual, err := ExecutableSourceDigest()
	if err != nil {
		t.Fatal(err)
	}
	if len(actual) != 64 {
		t.Fatalf("executable digest length = %d", len(actual))
	}
	filled, err := BindExecutableSourceDigest(Provenance{})
	if err != nil || filled.SourceDigest != actual {
		t.Fatalf("filled executable digest = %q err=%v", filled.SourceDigest, err)
	}
	verified, err := BindExecutableSourceDigest(Provenance{SourceDigest: actual})
	if err != nil || verified.SourceDigest != actual {
		t.Fatalf("verified executable digest = %q err=%v", verified.SourceDigest, err)
	}
	if _, err := BindExecutableSourceDigest(Provenance{
		SourceDigest: strings.Repeat("f", 64),
	}); err == nil {
		t.Fatal("mismatched configured executable digest was accepted")
	}
}
