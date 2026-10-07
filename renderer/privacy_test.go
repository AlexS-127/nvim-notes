package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAudit(t *testing.T) {
	s := newTestStore(t, map[string]string{"inbox.md": "# Inbox\n"})
	t.Setenv("NOTESVIEW_SENSE_QUEUE", filepath.Join(t.TempDir(), "queue.json"))
	writeSignals(t, s, "2026-10-07",
		map[string]any{"at": "2026-10-07T10:00:00", "src": "sense", "sensor": "window", "cat": "study", "title_hash": "0123456789abcdef", "app": "Google Chrome"},
		map[string]any{"at": "2026-10-07T10:00:15", "src": "sense", "sensor": "apps", "bundle": "com.google.Chrome", "app": "Google Chrome"},
		map[string]any{"at": "2026-10-07T10:00:20", "src": "quiz", "subject": "act200", "revision": "act200/accruals.md", "kind": "mc"},
	)
	if iss, n := s.Audit(); len(iss) != 0 || n != 3 {
		t.Fatalf("clean data flagged: %+v (%d lines)", iss, n)
	}
	writeSignals(t, s, "2026-10-08",
		map[string]any{"at": "2026-10-08T10:00:00", "src": "sense", "sensor": "window", "cat": "study", "title": "My private essay about something personal and long"},
		map[string]any{"at": "2026-10-08T10:00:01", "src": "sense", "sensor": "browser", "cat": "https://example.com/secret"},
		map[string]any{"at": "2026-10-08T10:00:02", "src": "nvim", "ev": "hb", "folder": "/Users/alex/notes/act200"},
	)
	iss, _ := s.Audit()
	if len(iss) != 3 {
		t.Fatalf("planted values: %+v", iss)
	}
	// images: a screenshot in .signals, and a screen preview kept too long
	os.WriteFile(s.signalPath("shot.png"), []byte("png"), 0o644)
	os.MkdirAll(previewDir(), 0o700)
	old := filepath.Join(previewDir(), "00000000000000aa.jpg")
	os.WriteFile(old, []byte("jpg"), 0o600)
	os.WriteFile(filepath.Join(previewDir(), "00000000000000bb.jpg"), []byte("jpg"), 0o600)
	os.Chtimes(old, time.Now().Add(-30*time.Hour), time.Now().Add(-30*time.Hour))
	if iss, _ := s.Audit(); len(iss) != 5 {
		t.Fatalf("planted images: %+v", iss)
	}
}
