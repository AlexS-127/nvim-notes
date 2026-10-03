package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCountWords(t *testing.T) {
	text := "# Title here\n- [ ] a task with words\n```\ncode block words\n```\nplain **bold** words — – ---\n"
	if got := countWords(text); got != 5 { // Title here + plain bold words (heading # and dashes are not words); task and code skipped
		t.Errorf("countWords = %d", got)
	}
}

func TestRecordWords(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.Local)
	s := newTestStore(t, map[string]string{
		"mine/a.md":  "one two three\n",
		"other/b.md": "ai written text here\n",
	})
	write := func(rel, text string) {
		if err := os.WriteFile(filepath.Join(s.Root, filepath.FromSlash(rel)), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetTrackedWords([]string{"mine"}); err != nil {
		t.Fatal(err)
	}
	if got := s.RecordWords(now); len(got) != 0 { // first pass only baselines
		t.Fatalf("baseline credited words: %v", got)
	}
	write("mine/a.md", "one two three four five\n")
	write("other/b.md", "lots and lots of untracked words here\n")
	if got := s.RecordWords(now); got["mine/a.md"] != 2 || len(got) != 1 {
		t.Fatalf("added words: %v", got)
	}
	write("mine/a.md", "one\n") // deleting never subtracts, and lowers the baseline
	if got := s.RecordWords(now); len(got) != 0 {
		t.Fatalf("deletion: %v", got)
	}
	write("mine/a.md", "one two three\n") // retyping counts again
	write("mine/new.md", "fresh note of four\n")
	if got := s.RecordWords(now); got["mine/a.md"] != 2 || got["mine/new.md"] != 4 {
		t.Fatalf("retype and new note: %v", got)
	}
	if d := s.WordsPerDay(); d["2026-10-03"] != 8 {
		t.Errorf("per day: %v", d)
	}
	// a folder tracked later is baselined, not credited
	if err := s.SetTrackedWords([]string{"mine", "other"}); err != nil {
		t.Fatal(err)
	}
	if got := s.RecordWords(now); len(got) != 0 {
		t.Errorf("newly tracked folder credited: %v", got)
	}
}

func TestWordsScore(t *testing.T) {
	if s := scoreFor([4]int{}, 0, 0, 5*scoreWordsPer, 0, false); s.WordsPts != 5 || s.Total != 5 {
		t.Errorf("words points: %+v", s)
	}
	a := Activity{Today: "2026-10-03", Days: map[string]DayStat{}}
	a.AddWords(map[string]int{"2026-10-03": 120}, time.Date(2026, 10, 3, 9, 0, 0, 0, time.Local))
	if a.WordsToday != 120 || a.WordsWeek != 120 || a.WordsTotal != 120 || a.Days["2026-10-03"].Words != 120 {
		t.Errorf("AddWords: %+v", a)
	}
}
