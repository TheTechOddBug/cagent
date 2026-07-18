package gitroot

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newRepo creates a fake main repository at dir with a .git directory.
func newRepo(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
}

// newWorktree links a fake worktree at dir to the main repository at mainDir,
// mirroring the on-disk layout produced by `git worktree add`.
func newWorktree(t *testing.T, dir, mainDir string) {
	t.Helper()
	gitdir := filepath.Join(mainDir, ".git", "worktrees", filepath.Base(dir))
	require.NoError(t, os.MkdirAll(gitdir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(gitdir, "commondir"), []byte("../..\n"), 0o644))
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644))
}

func TestRoot_MainRepository(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	newRepo(t, repo)

	assert.Equal(t, repo, Root(repo))
}

func TestRoot_Subdirectory(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	newRepo(t, repo)
	sub := filepath.Join(repo, "a", "b")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	assert.Equal(t, repo, Root(sub))
}

func TestRoot_LinkedWorktree(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	newRepo(t, repo)
	wt := filepath.Join(base, "wt")
	newWorktree(t, wt, repo)

	assert.Equal(t, repo, Root(wt))
}

func TestRoot_LinkedWorktreeSubdirectory(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	newRepo(t, repo)
	wt := filepath.Join(base, "wt")
	newWorktree(t, wt, repo)
	sub := filepath.Join(wt, "pkg")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	assert.Equal(t, repo, Root(sub))
}

func TestRoot_LinkedWorktreeAbsoluteCommondir(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	newRepo(t, repo)
	gitdir := filepath.Join(repo, ".git", "worktrees", "wt")
	require.NoError(t, os.MkdirAll(gitdir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(gitdir, "commondir"), []byte(filepath.Join(repo, ".git")+"\n"), 0o644))
	wt := filepath.Join(base, "wt")
	require.NoError(t, os.MkdirAll(wt, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644))

	assert.Equal(t, repo, Root(wt))
}

func TestRoot_LinkedWorktreeOfBareRepository(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	// Bare repositories are their own git dir; commondir points straight at them.
	bare := filepath.Join(base, "project.git")
	gitdir := filepath.Join(bare, "worktrees", "wt")
	require.NoError(t, os.MkdirAll(gitdir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(gitdir, "commondir"), []byte("../..\n"), 0o644))
	wt := filepath.Join(base, "wt")
	require.NoError(t, os.MkdirAll(wt, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644))

	assert.Equal(t, bare, Root(wt), "bare-repo worktrees resolve to the bare repo, not its parent")
}

func TestRoot_SubmoduleIsItsOwnRoot(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	newRepo(t, repo)
	// Submodule git dirs live under .git/modules and have no commondir file.
	gitdir := filepath.Join(repo, ".git", "modules", "sub")
	require.NoError(t, os.MkdirAll(gitdir, 0o755))
	sub := filepath.Join(repo, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644))

	assert.Equal(t, sub, Root(sub))
}

func TestRoot_DanglingGitdirPointer(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git"),
		[]byte("gitdir: "+filepath.Join(dir, "missing")+"\n"), 0o644))

	assert.Equal(t, dir, Root(dir), "a dangling gitdir pointer falls back to the dir itself")
}

func TestRoot_OversizedGitFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	huge := append([]byte("gitdir: "), make([]byte, maxPointerFileSize)...)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git"), huge, 0o644))

	assert.Equal(t, dir, Root(dir), "oversized .git files are treated as malformed")
}

func TestRoot_UncleanInput(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	newRepo(t, repo)

	assert.Equal(t, repo, Root(repo+string(filepath.Separator)))
	assert.Equal(t, repo, Root(filepath.Join(repo, "a", "..")))
}

func TestRoot_NotARepository(t *testing.T) {
	t.Parallel()
	assert.Empty(t, Root(t.TempDir()))
}

func TestRoot_EmptyDir(t *testing.T) {
	t.Parallel()
	assert.Empty(t, Root(""))
}

func TestRoot_UnreadableGitFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// A .git file with unexpected content: treated as the repo root anyway,
	// matching git's behavior of stopping the upward walk at any .git entry.
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git"), []byte("garbage"), 0o644))

	assert.Equal(t, dir, Root(dir))
}
