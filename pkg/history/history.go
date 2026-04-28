package history

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type History struct {
	Messages []string `json:"messages"`

	path    string
	current int
}

type options struct {
	homeDir string
}

type Opt func(*options)

func WithBaseDir(dir string) Opt {
	return func(o *options) {
		o.homeDir = dir
	}
}

func New(opts ...Opt) (*History, error) {
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}

	homeDir := o.homeDir
	if homeDir == "" {
		var err error
		if homeDir, err = os.UserHomeDir(); err != nil {
			return nil, err
		}
	}

	h := &History{
		path:    filepath.Join(homeDir, ".cagent", "history"),
		current: -1,
	}

	if err := h.migrateOldHistory(homeDir); err != nil {
		return nil, err
	}

	if err := h.load(); err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	return h, nil
}

func (h *History) migrateOldHistory(homeDir string) error {
	oldPath := filepath.Join(homeDir, ".cagent", "history.json")

	data, err := os.ReadFile(oldPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	var old struct {
		Messages []string `json:"messages"`
	}
	if err := json.Unmarshal(data, &old); err != nil {
		return err
	}

	for _, msg := range old.Messages {
		if err := h.append(msg); err != nil {
			return err
		}
	}

	return os.Remove(oldPath)
}

func (h *History) Add(message string) error {
	h.addInMemory(message)
	h.current = len(h.Messages)
	return h.append(message)
}

func (h *History) Previous() string {
	if len(h.Messages) == 0 {
		return ""
	}
	switch {
	case h.current == -1:
		h.current = len(h.Messages) - 1
	case h.current > 0:
		h.current--
	}
	return h.Messages[h.current]
}

func (h *History) Next() string {
	if len(h.Messages) == 0 {
		return ""
	}
	if h.current >= len(h.Messages)-1 {
		h.current = len(h.Messages)
		return ""
	}
	h.current++
	return h.Messages[h.current]
}

// LatestMatch returns the most recent history entry that extends the provided
// prefix, or an empty string when none does.
func (h *History) LatestMatch(prefix string) string {
	for _, msg := range slices.Backward(h.Messages) {
		if strings.HasPrefix(msg, prefix) && len(msg) > len(prefix) {
			return msg
		}
	}
	return ""
}

// FindPrevContains searches backward through history for a message containing query.
// from is an exclusive upper bound index. Pass len(Messages) to start from the most recent.
// Returns the matched message, its index, and whether a match was found.
// An empty query matches any entry.
func (h *History) FindPrevContains(query string, from int) (msg string, idx int, ok bool) {
	query = strings.ToLower(query)
	for i := min(from-1, len(h.Messages)-1); i >= 0; i-- {
		if query == "" || strings.Contains(strings.ToLower(h.Messages[i]), query) {
			return h.Messages[i], i, true
		}
	}
	return "", -1, false
}

// FindNextContains searches forward through history for a message containing query.
// from is an exclusive lower bound index. Pass -1 to start from the oldest.
// Returns the matched message, its index, and whether a match was found.
// An empty query matches any entry.
func (h *History) FindNextContains(query string, from int) (msg string, idx int, ok bool) {
	query = strings.ToLower(query)
	for i := max(from+1, 0); i < len(h.Messages); i++ {
		if query == "" || strings.Contains(strings.ToLower(h.Messages[i]), query) {
			return h.Messages[i], i, true
		}
	}
	return "", -1, false
}

func (h *History) SetCurrent(i int) {
	h.current = i
}

// addInMemory removes any prior occurrence of message and appends it as the
// most recent entry.
func (h *History) addInMemory(message string) {
	h.Messages = slices.DeleteFunc(h.Messages, func(m string) bool {
		return m == message
	})
	h.Messages = append(h.Messages, message)
}

func (h *History) append(message string) error {
	if err := os.MkdirAll(filepath.Dir(h.path), 0o755); err != nil {
		return err
	}

	f, err := os.OpenFile(h.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	encoded, err := json.Marshal(message)
	if err != nil {
		return err
	}

	_, err = f.Write(append(encoded, '\n'))
	return err
}

func (h *History) load() error {
	data, err := os.ReadFile(h.path)
	if err != nil {
		return err
	}

	// The file is append-only with one JSON-encoded string per line.
	// Replaying each entry through addInMemory naturally deduplicates,
	// keeping the latest occurrence of each message.
	for line := range bytes.Lines(data) {
		line = bytes.TrimRight(line, "\n")
		if len(line) == 0 {
			continue
		}
		var msg string
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		h.addInMemory(msg)
	}
	return nil
}
