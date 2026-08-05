package database

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

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
