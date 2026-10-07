package main

import "testing"

func TestAudit(t *testing.T) {
	s := newTestStore(t, map[string]string{"inbox.md": "# Inbox\n"})
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
}
