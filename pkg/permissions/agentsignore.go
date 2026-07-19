package permissions

import (
	"path/filepath"
	"strings"

	"github.com/docker/docker-agent/pkg/fsx"
)

var pathToolArgs = []struct {
	tool string
	arg  string
}{
	{"read_file", "path"},
	{"read_multiple_files", "paths"},
	{"write_file", "path"},
	{"edit_file", "path"},
	{"create_directory", "path"},
	{"remove_directory", "path"},
	{"list_directory", "path"},
	{"directory_tree", "path"},
	{"search_files_content", "path"},
}

func FromAgentsIgnore(startDir string) *Checker {
	path := fsx.FindAgentsIgnore(startDir)
	if path == "" {
		return nil
	}
	patterns, err := fsx.ReadAgentsIgnoreGlobs(path)
	if err != nil || len(patterns) == 0 {
		return nil
	}

	var deny []string
	for _, p := range patterns {
		for _, glob := range permissionGlobsFor(p) {
			for _, t := range pathToolArgs {
				deny = append(deny, t.tool+":"+t.arg+"="+glob)
			}
		}
	}
	if len(deny) == 0 {
		return nil
	}
	return NewCheckerFromRules(nil, nil, deny)
}

func permissionGlobsFor(pattern string) []string {
	p := strings.TrimSpace(pattern)
	if p == "" || strings.HasPrefix(p, "#") || strings.HasPrefix(p, "!") {
		return nil
	}
	p = strings.TrimSuffix(p, "/")
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return nil
	}
	base := filepath.ToSlash(p)
	globs := []string{base, base + "/*"}
	if !strings.Contains(base, "/") {
		globs = append(globs, "*/"+base, "*/"+base+"/*")
	}
	return globs
}
