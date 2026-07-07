package root

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/docker/docker-agent/pkg/paths"
)

// Regression test: the --session-db default used to be computed at flag
// registration time as ~/.cagent/session.db, before the root
// PersistentPreRunE applied --data-dir. Sessions were then always read from
// the home dir, making past sessions stored under a custom data dir
// invisible. The default must resolve lazily against the effective data dir.
func TestSessionDBPath_FollowsDataDir(t *testing.T) {
	// Not parallel: mutates the process-wide data-dir override.
	dataDir := t.TempDir()
	paths.SetDataDir(dataDir)
	t.Cleanup(func() { paths.SetDataDir("") })

	assert.Equal(t, filepath.Join(dataDir, "session.db"), sessionDBPath(""))
}

func TestSessionDBPath_ExplicitFlagWins(t *testing.T) {
	// Not parallel: mutates the process-wide data-dir override.
	paths.SetDataDir(t.TempDir())
	t.Cleanup(func() { paths.SetDataDir("") })

	assert.Equal(t, "/explicit/db.sqlite", sessionDBPath("/explicit/db.sqlite"))
}
