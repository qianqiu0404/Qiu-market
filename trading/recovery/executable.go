package recovery

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

// ExecutableSourceDigest streams the current executable through SHA-256. The
// managed release path is immutable; this digest is the backend artifact
// identity recorded in every recovery epoch.
func ExecutableSourceDigest() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve current executable: %w", err)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open current executable: %w", err)
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", fmt.Errorf("hash current executable: %w", err)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// BindExecutableSourceDigest fills an omitted digest from the running binary,
// or rejects a configured value that does not describe that exact binary.
func BindExecutableSourceDigest(value Provenance) (Provenance, error) {
	actual, err := ExecutableSourceDigest()
	if err != nil {
		return Provenance{}, err
	}
	configured := strings.ToLower(strings.TrimSpace(value.SourceDigest))
	if configured != "" && configured != actual {
		return Provenance{}, fmt.Errorf("configured recovery source digest does not match current executable")
	}
	value.SourceDigest = actual
	return value, nil
}
