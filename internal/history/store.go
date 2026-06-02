package history

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Entry struct {
	Timestamp     time.Time `json:"timestamp"`
	ContainerID   string    `json:"containerId"`
	ContainerName string    `json:"containerName"`
	Reason        string    `json:"reason"`
	Action        string    `json:"action"`
	Attempt       int       `json:"attempt"`
	Status        string    `json:"status"`
	Error         string    `json:"error,omitempty"`
	Output        string    `json:"output,omitempty"`
	DurationMs    int64     `json:"durationMs"`
}

type Store struct {
	path string
	mu   sync.Mutex
}

func NewStore(configDir string) (*Store, error) {
	dataDir := filepath.Join(configDir, "data")
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	return &Store{path: filepath.Join(dataDir, "action-history.jsonl")}, nil
}

func (s *Store) Append(entry Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal history entry: %w", err)
	}

	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open history file: %w", err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("append history file: %w", err)
	}
	return nil
}

type ListOptions struct {
	Limit         int
	Offset        int
	Service       string   // filter by containerName or containerId (empty = all)
	ExcludeStatus string   // exclude entries with this status (e.g. "healthy")
	OnlyStatuses  []string // if non-empty, only include entries with one of these statuses
}

func (s *Store) List(limit int) ([]Entry, error) {
	entries, _, err := s.ListPage(ListOptions{Limit: limit})
	return entries, err
}

// ListPage returns a page of entries newest-first plus the total matching count.
func (s *Store) ListPage(opts ListOptions) ([]Entry, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if opts.Limit <= 0 {
		opts.Limit = 50
	}

	f, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return []Entry{}, 0, nil
		}
		return nil, 0, fmt.Errorf("open history file: %w", err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 4*1024*1024)

	// Read all matching entries into a slice (newest-last from file).
	var all []Entry
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		if opts.Service != "" && e.ContainerName != opts.Service && e.ContainerID != opts.Service {
			continue
		}
		if opts.ExcludeStatus != "" && e.Status == opts.ExcludeStatus {
			continue
		}
		if len(opts.OnlyStatuses) > 0 {
			match := false
			for _, s := range opts.OnlyStatuses {
				if e.Status == s {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}
		all = append(all, e)
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, fmt.Errorf("scan history file: %w", err)
	}

	total := len(all)

	// Reverse to newest-first.
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}

	// Apply offset + limit.
	if opts.Offset >= len(all) {
		return []Entry{}, total, nil
	}
	all = all[opts.Offset:]
	if opts.Limit < len(all) {
		all = all[:opts.Limit]
	}

	return all, total, nil
}
