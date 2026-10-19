// Package store keeps a small local history of deploy runs (JSONL, append-only).
package store

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Run is one recorded pipeline execution.
type Run struct {
	Time     time.Time `json:"time"`
	App      string    `json:"app"`
	Env      string    `json:"env"`
	Host     string    `json:"host"`
	Pipeline string    `json:"pipeline"`
	OK       bool      `json:"ok"`
	Duration string    `json:"duration"`
	Output   string    `json:"output,omitempty"`
}

// Runs is a JSONL-backed history. Appends are serialised; reads tolerate a
// missing file and malformed lines.
type Runs struct {
	path string
	mu   sync.Mutex
}

// DefaultPath is $XDG_STATE_HOME/wmdep/runs.jsonl, else ~/.local/state/wmdep.
func DefaultPath() string {
	if v := os.Getenv("XDG_STATE_HOME"); v != "" {
		return filepath.Join(v, "wmdep", "runs.jsonl")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "wmdep", "runs.jsonl")
}

// Open returns a history handle. A write failure is reported on Append.
func Open(path string) *Runs { return &Runs{path: path} }

// Append writes one record.
func (r *Runs) Append(run Run) error {
	if r == nil || r.path == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(run)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

// Recent returns the last n runs, newest last.
func (r *Runs) Recent(n int) []Run {
	if r == nil || r.path == "" || n <= 0 {
		return nil
	}
	f, err := os.Open(r.path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var runs []Run
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var run Run
		if err := json.Unmarshal(sc.Bytes(), &run); err != nil {
			continue
		}
		runs = append(runs, run)
	}
	if len(runs) > n {
		runs = runs[len(runs)-n:]
	}
	return runs
}
