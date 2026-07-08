package sqliteutil

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsTransientError(t *testing.T) {
	t.Parallel()

	assert.False(t, IsTransientError(nil))
	assert.False(t, IsTransientError(errors.New("boom")))
	assert.True(t, IsTransientError(context.Canceled))
	assert.True(t, IsTransientError(context.DeadlineExceeded))
	assert.True(t, IsTransientError(fmt.Errorf("wrapped: %w", context.Canceled)))
}

func TestIsTransientError_SQLiteBusy(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "busy.db")
	dsn := path + "?_pragma=busy_timeout(0)&_pragma=journal_mode(WAL)"

	writer, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	defer writer.Close()

	tx, err := writer.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer tx.Rollback() //nolint:errcheck // test cleanup, error irrelevant
	_, err = tx.ExecContext(t.Context(), "CREATE TABLE t (id INTEGER)")
	require.NoError(t, err)

	// A second connection hits the write lock and gets SQLITE_BUSY.
	blocked, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	defer blocked.Close()

	_, err = blocked.ExecContext(t.Context(), "CREATE TABLE u (id INTEGER)")
	require.Error(t, err)
	assert.True(t, IsTransientError(err), "SQLITE_BUSY should be transient: %v", err)
	assert.False(t, IsCantOpenError(err))
}
