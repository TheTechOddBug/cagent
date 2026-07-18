// Package gitroot resolves a directory to the root of the git repository
// containing it by reading .git metadata directly, without shelling out to
// the git binary.
package gitroot

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

// maxPointerFileSize bounds reads of .git pointer and commondir files. Valid
// ones hold a single path, so anything larger is treated as malformed rather
// than read into memory.
const maxPointerFileSize = 4096

// Root returns the top-level directory of the repository containing dir,
// walking up parent directories until a .git entry is found. Linked worktrees
// resolve to the main repository's root, so every worktree of one repository
// shares the same root. It returns "" when dir is empty or not inside a
// repository.
func Root(dir string) string {
	if dir == "" {
		return ""
	}
	d := filepath.Clean(dir)
	for {
		gitPath := filepath.Join(d, ".git")
		info, err := os.Stat(gitPath)
		switch {
		case err == nil && info.IsDir():
			return d
		case err == nil:
			// A .git file points at the real git dir (worktree or submodule).
			if root := mainRepoRoot(gitPath, d); root != "" {
				return root
			}
			// A submodule (no commondir) is its own repository.
			return d
		}

		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}

// mainRepoRoot resolves the main repository root of a linked worktree whose
// .git file lives at gitFile. It returns "" when the gitdir cannot be parsed
// or has no commondir file (i.e. it is not a linked worktree).
func mainRepoRoot(gitFile, base string) string {
	gd := parseGitdir(gitFile, base)
	if gd == "" {
		return ""
	}
	// Linked worktrees carry a commondir file pointing at the main .git dir.
	data := readPointerFile(filepath.Join(gd, "commondir"))
	if data == "" {
		return ""
	}
	common := data
	if !filepath.IsAbs(common) {
		common = filepath.Join(gd, common)
	}
	common = filepath.Clean(common)
	// The main repository root is the parent of its .git dir. A bare
	// repository is its own common dir (no worktree to strip).
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common)
	}
	return common
}

func parseGitdir(gitFile, base string) string {
	gd, ok := strings.CutPrefix(readPointerFile(gitFile), "gitdir: ")
	if !ok {
		return ""
	}
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(base, gd)
	}
	return gd
}

// readPointerFile reads a small metadata file and returns its trimmed
// content, or "" when the file is missing or suspiciously large.
func readPointerFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxPointerFileSize+1))
	if err != nil || len(data) > maxPointerFileSize {
		return ""
	}
	return strings.TrimSpace(string(data))
}
