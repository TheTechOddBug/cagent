// Package cache implements a configurable response cache for agents.
//
// The cache maps a normalized user question to a previously produced agent
// response so that asking the exact same (according to the configured
// normalization rules) question again returns the same answer without
// invoking the model.
//
// Two storage backends are supported:
//
//   - an in-memory map (the default), which keeps entries for the lifetime of
//     the process;
//   - a JSON-file backed store, which persists entries to disk so they
//     survive restarts.
//
// Two normalization options are exposed:
//
//   - case sensitivity: when disabled (the default), questions are
//     compared case-insensitively;
//   - blank trimming: when enabled, leading and trailing whitespace is
//     stripped before comparison.
package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Config describes how a Cache should normalize keys and where it should
// store entries.
type Config struct {
	// Enabled toggles the cache on or off. When false, New returns nil.
	Enabled bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`

	// CaseSensitive controls whether the question matching is
	// case-sensitive. The default (false) means "Hello" and "hello"
	// are treated as the same question.
	CaseSensitive bool `json:"case_sensitive,omitempty" yaml:"case_sensitive,omitempty"`

	// TrimSpaces controls whether leading and trailing whitespace is
	// removed from questions before they are compared. The default
	// (false) preserves whitespace.
	TrimSpaces bool `json:"trim_spaces,omitempty" yaml:"trim_spaces,omitempty"`

	// Path, when non-empty, selects a JSON-file backed cache stored at
	// the given path. When empty, the cache lives only in memory.
	Path string `json:"path,omitempty" yaml:"path,omitempty"`
}

// Cache stores agent responses keyed on the user's question.
//
// Implementations must be safe for concurrent use.
type Cache interface {
	// Lookup returns the stored response for the given question and a
	// boolean indicating whether the question was found.
	Lookup(question string) (string, bool)

	// Store records the response for the given question, replacing any
	// existing entry with the same normalized key.
	Store(question, response string)
}

// New builds a Cache from the given Config. It returns (nil, nil) when
// caching is disabled, allowing callers to short-circuit with a simple
// nil check.
func New(cfg Config) (Cache, error) {
	if !cfg.Enabled {
		return nil, nil //nolint:nilnil // intentional: nil signals caching disabled
	}

	normalize := keyNormalizer(cfg.CaseSensitive, cfg.TrimSpaces)

	if cfg.Path == "" {
		return &memoryCache{
			entries:   make(map[string]string),
			normalize: normalize,
		}, nil
	}

	return newFileCache(cfg.Path, normalize)
}

// keyNormalizer returns a function that applies the configured
// normalization rules to a question before it is used as a cache key.
func keyNormalizer(caseSensitive, trimSpaces bool) func(string) string {
	return func(s string) string {
		if trimSpaces {
			s = strings.TrimSpace(s)
		}
		if !caseSensitive {
			s = strings.ToLower(s)
		}
		return s
	}
}

// memoryCache is the default in-memory cache implementation.
type memoryCache struct {
	mu        sync.RWMutex
	entries   map[string]string
	normalize func(string) string
}

func (c *memoryCache) Lookup(question string) (string, bool) {
	key := c.normalize(question)
	c.mu.RLock()
	defer c.mu.RUnlock()
	resp, ok := c.entries[key]
	return resp, ok
}

func (c *memoryCache) Store(question, response string) {
	key := c.normalize(question)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = response
}

// fileCache wraps memoryCache and persists every Store to a JSON file.
type fileCache struct {
	mu        sync.RWMutex
	path      string
	entries   map[string]string
	normalize func(string) string
}

func newFileCache(path string, normalize func(string) string) (*fileCache, error) {
	c := &fileCache{
		path:      path,
		entries:   make(map[string]string),
		normalize: normalize,
	}

	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if len(data) > 0 {
			if err := json.Unmarshal(data, &c.entries); err != nil {
				return nil, fmt.Errorf("loading cache file %q: %w", path, err)
			}
		}
	case errors.Is(err, os.ErrNotExist):
		// First run: starting with an empty cache is fine.
	default:
		return nil, fmt.Errorf("reading cache file %q: %w", path, err)
	}

	return c, nil
}

func (c *fileCache) Lookup(question string) (string, bool) {
	key := c.normalize(question)
	c.mu.RLock()
	defer c.mu.RUnlock()
	resp, ok := c.entries[key]
	return resp, ok
}

func (c *fileCache) Store(question, response string) {
	key := c.normalize(question)
	c.mu.Lock()
	c.entries[key] = response
	snapshot := make(map[string]string, len(c.entries))
	maps.Copy(snapshot, c.entries)
	path := c.path
	c.mu.Unlock()

	if err := writeJSON(path, snapshot); err != nil {
		// We deliberately swallow the error here: a transient write
		// failure should not break a successful agent turn. The cache
		// will still serve from memory and try to persist again on the
		// next Store.
		_ = err
	}
}

// writeJSON atomically writes the given map to path as pretty-printed JSON.
func writeJSON(path string, entries map[string]string) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating cache directory %q: %w", dir, err)
		}
	}

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling cache: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".cache-*.json")
	if err != nil {
		return fmt.Errorf("creating temp cache file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing temp cache file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp cache file: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("renaming cache file: %w", err)
	}
	return nil
}
