package database

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAppliedProductionMigration0026IsImmutable(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "migrations", "2026082400026.sql"))
	require.NoError(t, err)
	digest := sha256.Sum256(content)
	require.Equal(t,
		"c095dc512ec90d144ff2c0282271efc2a1c3773dd9e34c5f3edc2f64968e7d15",
		fmt.Sprintf("%x", digest),
	)
}

func TestCollectSQLMigrationsIsSortedAndIgnoresOtherFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "002.sql"), []byte("SELECT 2;"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "001.sql"), []byte("SELECT 1;"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("ignore"), 0o600))

	files, err := collectSQLMigrations(dir)

	require.NoError(t, err)
	require.Len(t, files, 2)
	require.Equal(t, "001.sql", files[0].filename)
	require.Equal(t, "002.sql", files[1].filename)
	require.Len(t, files[0].checksum, 64)
	require.NotEqual(t, files[0].checksum, files[1].checksum)
}
