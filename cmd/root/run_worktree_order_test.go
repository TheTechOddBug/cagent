package root

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/docker/docker-agent/pkg/paths"
	"github.com/docker/docker-agent/pkg/runtime"
	"github.com/docker/docker-agent/pkg/session"
	"github.com/docker/docker-agent/pkg/teamloader"
	"github.com/docker/docker-agent/pkg/tui"
)

// workdirRecordingBackend records the working directory the flags carry at the
// moment LoadTeam is called. Toolsets capture that directory when they are
// built during LoadTeam, so this is exactly the value every tool (the shell
// included) would operate in.
type workdirRecordingBackend struct {
	flags          *runExecFlags
	workingDirSeen string
}

func (b *workdirRecordingBackend) LoadTeamRequest() runtime.LoadTeamRequest {
	return runtime.LoadTeamRequest{RunConfig: &b.flags.runConfig}
}

func (b *workdirRecordingBackend) LoadTeam(context.Context, runtime.LoadTeamRequest) (*teamloader.LoadResult, error) {
	b.workingDirSeen = b.flags.runConfig.WorkingDir
	return nil, nil
}

func (b *workdirRecordingBackend) CreateSessionRequest(workingDir string) runtime.CreateSessionRequest {
	return runtime.CreateSessionRequest{WorkingDir: workingDir}
}

func (b *workdirRecordingBackend) CreateSession(context.Context, *teamloader.LoadResult, runtime.CreateSessionRequest) (runtime.Runtime, *session.Session, func(), error) {
	return nil, nil, func() {}, nil
}

func (b *workdirRecordingBackend) Spawner(runtime.Runtime) tui.SessionSpawner { return nil }
func (b *workdirRecordingBackend) Close() error                               { return nil }

// TestLoadTeamInWorktreeSetsWorkingDirBeforeLoad is the regression test for
// the ordering bug: the worktree must be created and installed as the working
// directory BEFORE LoadTeam builds the toolsets, otherwise the shell tool ends
// up rooted in the user's checkout instead of the worktree.
func TestLoadTeamInWorktreeSetsWorkingDirBeforeLoad(t *testing.T) {
	repo := initTestRepo(t)

	paths.SetDataDir(t.TempDir())
	t.Cleanup(func() { paths.SetDataDir("") })

	f := &runExecFlags{worktree: true}
	b := &workdirRecordingBackend{flags: f}

	loadResult, wt, wd, err := f.loadTeamInWorktree(t.Context(), b, repo)
	require.NoError(t, err)
	require.Nil(t, loadResult)
	require.NotNil(t, wt)
	t.Cleanup(func() { _ = wt.Remove(context.WithoutCancel(t.Context())) })

	// The worktree lives under the data dir, not the source repo.
	assert.Equal(t, wt.Dir, wd)
	assert.NotEqual(t, repo, wt.Dir)

	// The crux: LoadTeam saw the worktree directory, so toolsets are rooted
	// there.
	assert.Equal(t, wt.Dir, b.workingDirSeen)
	assert.Equal(t, wt.Dir, f.runConfig.WorkingDir)
}

// initTestRepo creates a throwaway git repository with one commit and returns
// its (symlink-resolved) root.
func initTestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test User"},
		{"config", "commit.gpgsign", "false"},
	} {
		out, err := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("A"), 0o644))
	for _, args := range [][]string{
		{"add", "."},
		{"commit", "-m", "init"},
	} {
		out, err := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	return dir
}
