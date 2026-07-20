package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/docker/docker-agent/pkg/config"
	"github.com/docker/docker-agent/pkg/session"
)

// TestCreateSession_WorkingDirValidation covers the path-containment guard
// introduced to address go/path-injection (CodeQL alert #57).
func TestCreateSession_WorkingDirValidation(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	root := t.TempDir()
	rc := &config.RuntimeConfig{Config: config.Config{WorkingDir: root}}

	newSM := func() *SessionManager {
		return NewSessionManager(ctx, config.Sources{}, session.NewInMemorySessionStore(), 0, rc)
	}

	t.Run("empty WorkingDir is accepted without applying a root constraint", func(t *testing.T) {
		t.Parallel()
		sm := newSM()
		created, err := sm.CreateSession(ctx, &session.Session{})
		require.NoError(t, err)
		assert.Empty(t, created.WorkingDir)
	})

	t.Run("WorkingDir equal to root is accepted", func(t *testing.T) {
		t.Parallel()
		sm := newSM()
		created, err := sm.CreateSession(ctx, &session.Session{WorkingDir: root})
		require.NoError(t, err)
		resolvedRoot, err := filepath.EvalSymlinks(root)
		require.NoError(t, err)
		assert.Equal(t, resolvedRoot, created.WorkingDir)
	})

	t.Run("WorkingDir inside root is accepted and symlink-resolved", func(t *testing.T) {
		t.Parallel()
		sub := filepath.Join(root, "sub")
		require.NoError(t, os.Mkdir(sub, 0o755))

		sm := newSM()
		created, err := sm.CreateSession(ctx, &session.Session{WorkingDir: sub})
		require.NoError(t, err)
		resolvedSub, err := filepath.EvalSymlinks(sub)
		require.NoError(t, err)
		assert.Equal(t, resolvedSub, created.WorkingDir)
	})

	t.Run("WorkingDir outside root is rejected", func(t *testing.T) {
		t.Parallel()
		outside := t.TempDir() // different temp dir, not under root
		sm := newSM()
		_, err := sm.CreateSession(ctx, &session.Session{WorkingDir: outside})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "outside the permitted root")
	})

	t.Run("WorkingDir /etc is rejected", func(t *testing.T) {
		t.Parallel()
		sm := newSM()
		_, err := sm.CreateSession(ctx, &session.Session{WorkingDir: "/etc"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "outside the permitted root")
	})

	t.Run("non-existent WorkingDir is rejected", func(t *testing.T) {
		t.Parallel()
		sm := newSM()
		_, err := sm.CreateSession(ctx, &session.Session{WorkingDir: filepath.Join(root, "does-not-exist")})
		require.Error(t, err)
	})

	t.Run("WorkingDir that is a file (not dir) is rejected", func(t *testing.T) {
		t.Parallel()
		f := filepath.Join(root, "afile.txt")
		require.NoError(t, os.WriteFile(f, []byte("x"), 0o644))

		sm := newSM()
		_, err := sm.CreateSession(ctx, &session.Session{WorkingDir: f})
		require.Error(t, err)
		assert.EqualError(t, err, "working directory must be a directory")
	})

	t.Run("symlink inside root pointing outside root is rejected", func(t *testing.T) {
		t.Parallel()
		outside := t.TempDir()
		link := filepath.Join(root, "escape-link")
		require.NoError(t, os.Symlink(outside, link))

		sm := newSM()
		_, err := sm.CreateSession(ctx, &session.Session{WorkingDir: link})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "outside the permitted root")
	})
}

// TestResolveWithinRoot exercises the containment helper directly.
func TestResolveWithinRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	rc := &config.RuntimeConfig{Config: config.Config{WorkingDir: root}}
	sm := NewSessionManager(t.Context(), config.Sources{}, session.NewInMemorySessionStore(), 0, rc)

	t.Run("path equal to root is allowed", func(t *testing.T) {
		t.Parallel()
		resolvedRoot, err := filepath.EvalSymlinks(root)
		require.NoError(t, err)
		got, err := sm.resolveWithinRoot(root)
		require.NoError(t, err)
		assert.Equal(t, resolvedRoot, got)
	})

	t.Run("subpath is allowed", func(t *testing.T) {
		t.Parallel()
		sub := filepath.Join(root, "child")
		require.NoError(t, os.Mkdir(sub, 0o755))
		resolvedSub, err := filepath.EvalSymlinks(sub)
		require.NoError(t, err)
		got, err := sm.resolveWithinRoot(sub)
		require.NoError(t, err)
		assert.Equal(t, resolvedSub, got)
	})

	t.Run("sibling outside root is rejected", func(t *testing.T) {
		t.Parallel()
		sibling := t.TempDir()
		_, err := sm.resolveWithinRoot(sibling)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "outside the permitted root")
	})

	t.Run("non-existent path returns error", func(t *testing.T) {
		t.Parallel()
		_, err := sm.resolveWithinRoot(filepath.Join(root, "ghost"))
		require.Error(t, err)
	})

	t.Run("symlink inside root pointing outside root is rejected", func(t *testing.T) {
		t.Parallel()
		outside := t.TempDir()
		link := filepath.Join(root, "evil-link")
		require.NoError(t, os.Symlink(outside, link))

		_, err := sm.resolveWithinRoot(link)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "outside the permitted root")
	})

	t.Run("dot-dot traversal that resolves outside root is rejected", func(t *testing.T) {
		t.Parallel()
		// Build a sub-dir so that three ".." hops escape root and land at a
		// real ancestor directory (/tmp or similar). filepath.Abs cleans the
		// ".." components; the result is an existing path outside root, so
		// this exercises the containment check itself (not just EvalSymlinks
		// failing on a non-existent path).
		sub := filepath.Join(root, "dotdot-child")
		require.NoError(t, os.Mkdir(sub, 0o755))
		// sub = <tmpdir>/dotdot-child; three '..' → <tmpdir parent> (e.g. /tmp)
		escaped, err := filepath.Abs(filepath.Join(sub, "..", "..", ".."))
		require.NoError(t, err)

		_, err = sm.resolveWithinRoot(escaped)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "outside the permitted root")
	})
}
