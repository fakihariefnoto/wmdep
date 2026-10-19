package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestAppendAndRecent(t *testing.T) {
	r := Open(filepath.Join(t.TempDir(), "runs.jsonl"))
	for i := 0; i < 3; i++ {
		if err := r.Append(Run{Time: time.Unix(int64(i), 0), App: "api", OK: i != 1}); err != nil {
			t.Fatal(err)
		}
	}
	got := r.Recent(2)
	if len(got) != 2 || got[1].App != "api" || got[0].OK {
		t.Fatalf("recent = %+v", got)
	}
}

func TestRecentMissingFileIsEmpty(t *testing.T) {
	if got := Open(filepath.Join(t.TempDir(), "none.jsonl")).Recent(5); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}
