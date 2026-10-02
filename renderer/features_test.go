package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Thursday 1 October 2026, noon.
var testNow = time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local)

func readFile(t *testing.T, s *Store, rel string) string {
	t.Helper()
	b, err := s.Read(rel)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ── natural dates ──

func TestNaturalDates(t *testing.T) {
	cases := map[string]string{
		"@today":                              "@2026-10-01",
		"@tomorrow":                           "@2026-10-02",
		"@Tomorrow":                           "@2026-10-02",
		"@fri":                                "@2026-10-02", // next occurrence
		"@thu":                                "@2026-10-08", // today is Thursday: the next one
		"@wed":                                "@2026-10-07",
		"@mon":                                "@2026-10-05",
		"@sun":                                "@2026-10-04",
		"@oct6":                               "@2026-10-06",
		"@sep6":                               "@2027-09-06", // already passed this year
		"@10/6":                               "@2026-10-06",
		"@1/15":                               "@2027-01-15",
		"@+3d":                                "@2026-10-04",
		"@+2w":                                "@2026-10-15",
		"@2026-10-09":                         "@2026-10-09", // already ISO
		"@someday":                            "@someday",    // unknown
		"@feb30":                              "@feb30",      // no such day
		"@13/1":                               "@13/1",
		"@fri-ish":                            "@fri-ish",
		"me@tomorrow.com":                     "me@tomorrow.com", // not after whitespace
		"- [ ] read ch. 4 @fri #act200":       "- [ ] read ch. 4 @2026-10-02 #act200",
		"  - [ ] a @today, b @+1d. (c @oct6)": "  - [ ] a @2026-10-01, b @2026-10-02. (c @2026-10-06)",
		"no dates here":                       "no dates here",
	}
	for in, want := range cases {
		if got := ConvertNaturalDates(in, testNow); got != want {
			t.Errorf("ConvertNaturalDates(%q) = %q, want %q", in, got, want)
		}
	}
	// Feb 29 skips to the next leap year
	if d, ok := ResolveNaturalDate("feb29", testNow); !ok || d.Format(isoDate) != "2028-02-29" {
		t.Errorf("feb29 -> %v %v", d, ok)
	}
}

// ── task parsing ──

func TestDateGroups(t *testing.T) {
	cases := map[string]string{
		"2026-09-30": GroupOverdue,
		"2025-01-01": GroupOverdue,
		"2026-10-01": GroupToday,
		"2026-10-02": GroupTomorrow,
		"2026-10-03": GroupWeek,
		"2026-10-08": GroupWeek,
		"2026-10-09": GroupWeek2,
		"2026-10-15": GroupWeek2,
		"2026-10-16": GroupWeek3,
		"2026-10-22": GroupWeek3,
		"2026-10-23": GroupLater,
		"":           GroupNone,
		"garbage":    GroupNone,
	}
	for due, want := range cases {
		if got := DateGroup(due, testNow); got != want {
			t.Errorf("DateGroup(%q) = %s, want %s", due, got, want)
		}
	}
}

// ── daily notes ──

func TestCarryOverKeepsCRLF(t *testing.T) {
	keep, moved, n := carryOver(strings.Split("- [ ] a\r\n  - [ ] b\r\n", "\n"), NewFolderIndex(nil), "daily/x.md", "2026-10-01")
	if n != 1 || strings.Join(keep, "\n") != "- [>] a → [[2026-10-01]]\r\n" || strings.Join(moved, "|") != "- [ ] a|  - [ ] b" {
		t.Errorf("keep=%q moved=%q n=%d", keep, moved, n)
	}
}

func TestVersionCompare(t *testing.T) {
	cases := []struct {
		a, b string
		less bool
	}{
		{"0.1.0", "0.2.0", true}, {"v0.2.0", "0.2.0", false}, {"0.10.0", "0.9.9", false},
		{"0.2", "0.2.1", true}, {"1.0.0", "0.9.0", false}, {"dev", "0.2.0", false},
	}
	for _, c := range cases {
		if got := versionLess(c.a, c.b); got != c.less {
			t.Errorf("versionLess(%s,%s) = %v", c.a, c.b, got)
		}
	}
}

// ── viewer ──

func TestRenderMovedAndInline(t *testing.T) {
	s := newTestStore(t, map[string]string{"Other.md": "# O"})
	out, err := NewMarkdown(s, "daily/x.md").Render([]byte("- [>] moved → [[Other]]\n- [ ] open\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `class="task-moved"`) || !strings.Contains(out, `moved-box`) || strings.Contains(out, "[&gt;]") {
		t.Errorf("moved item not rendered:\n%s", out)
	}
	md := NewMarkdown(s, "")
	if got := md.RenderInline("inbox.md", "read **ch 4** and [[Other]]"); got != `read <strong>ch 4</strong> and <a class="wikilink" href="#/note/Other.md">Other</a>` {
		t.Errorf("inline: %s", got)
	}
	if got := md.RenderInline("inbox.md", "1. <b>"); strings.Contains(got, "<ol") || strings.Contains(got, "<b>") {
		t.Errorf("non-paragraph should be escaped text: %s", got)
	}
}

func TestWatcherReportsTreeChanges(t *testing.T) {
	s := newTestStore(t, map[string]string{"a.md": "x"})
	srv := NewServer(s, 0)
	srv.css = filepath.Join(t.TempDir(), "custom.css")
	stop, err := srv.Watch()
	if err != nil {
		t.Skip("no file watching here:", err)
	}
	defer stop()
	ch := make(chan event, 16)
	srv.mu.Lock()
	srv.clients[ch] = true
	srv.mu.Unlock()
	wait := func(what string) event {
		t.Helper()
		select {
		case e := <-ch:
			return e
		case <-time.After(3 * time.Second):
			t.Fatalf("no change event after %s", what)
		}
		return event{}
	}
	os.MkdirAll(filepath.Join(s.Root, "new", "deeper"), 0o755)
	if e := wait("mkdir"); !strings.Contains(e.Data, `"tree":true`) {
		t.Errorf("mkdir: %+v", e)
	}
	time.Sleep(150 * time.Millisecond)
	os.WriteFile(filepath.Join(s.Root, "new", "deeper", "b.md"), []byte("x"), 0o644)
	if e := wait("file in new folder"); !strings.Contains(e.Data, `"tree":true`) {
		t.Errorf("new file: %+v", e)
	}
	os.Rename(filepath.Join(s.Root, "new"), filepath.Join(s.Root, "renamed"))
	wait("rename")
	time.Sleep(150 * time.Millisecond)
	os.WriteFile(filepath.Join(s.Root, "renamed", "deeper", "c.md"), []byte("x"), 0o644)
	if e := wait("file in renamed folder"); !strings.Contains(e.Data, `"tree":true`) {
		t.Errorf("file in renamed folder: %+v", e)
	}
}
