package main

import (
	"strings"
	"testing"
	"time"
)

func TestCaptureGoesAboveDone(t *testing.T) {
	s := newTestStore(t, map[string]string{"inbox.md": "# Inbox\n- [ ] a\n  - [ ] sub\n- [ ] b\n\n## Done\n- [x] old\n"})
	if _, err := s.Capture(CaptureOpts{Text: "new"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	b, _ := s.Read("inbox.md")
	lines := strings.Split(string(b), "\n")
	if !strings.HasPrefix(lines[4], "- [ ] new") || lines[6] != "## Done" || lines[7] != "- [x] old" {
		t.Errorf("got %q", b)
	}
}

func TestCaptureWithoutDoneAppends(t *testing.T) {
	s := newTestStore(t, map[string]string{"inbox.md": "# Inbox\n- [ ] a"})
	s.Capture(CaptureOpts{Text: "new"}, time.Now())
	b, _ := s.Read("inbox.md")
	if !strings.HasPrefix(string(b), "# Inbox\n- [ ] a\n- [ ] new") {
		t.Errorf("got %q", b)
	}
}

func TestTickMovesSubtasksAndKeepsDoneOrder(t *testing.T) {
	s := newTestStore(t, map[string]string{"n.md": "- [ ] a\n  - [ ] sub\n- [ ] b\n\n## Done\n- [x] old\n\n## Notes\ntext\n"})
	if err := s.ToggleCheckbox("n.md", 1); err != nil {
		t.Fatal(err)
	}
	b, _ := s.Read("n.md")
	st := " ✅ " + time.Now().Format(isoDate)
	want := "- [ ] b\n\n## Done\n- [x] old\n- [x] a" + st + "\n  - [ ] sub\n\n## Notes\ntext\n"
	if string(b) != want {
		t.Errorf("got %q\nwant %q", b, want)
	}
}
