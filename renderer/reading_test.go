package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestReadingListAndScore(t *testing.T) {
	s := newTestStore(t, map[string]string{"inbox.md": "# Inbox\n"})
	day1 := time.Date(2026, 10, 1, 9, 0, 0, 0, time.Local)
	if _, err := s.AddBook("Dune", "", 0, day1); err == nil {
		t.Fatal("a book without an author was added")
	}
	dune, err := s.AddBook("Dune", "Frank Herbert", 400, day1)
	if err != nil {
		t.Fatal(err)
	}
	med, _ := s.AddBook("Meditations", "Marcus Aurelius", 0, day1)
	if dune.Status != readToRead || dune.Pct != 0 || med.Pct != -1 || med.ID != dune.ID+1 {
		t.Fatalf("added %+v %+v", dune, med)
	}
	if _, _, err := s.LogPages("", 10, -1, day1); err == nil {
		t.Fatal("logged pages with nothing being read and no book named")
	}
	b, _, err := s.LogPages("du", 100, -1, day1.Add(time.Hour)) // 10:00
	if err != nil || b.Status != readReading || b.Pct != 25 || b.Started != "2026-10-01" {
		t.Fatalf("log: %+v %v", b, err)
	}
	if b, _, _ = s.LogPages("", 0, 150, day1.Add(3*time.Hour)); b.Page != 150 { // 12:00, the book being read
		t.Fatalf("--to: %+v", b)
	}
	if b, _, _ = s.LogPages("med", 30, -1, day1.Add(4*time.Hour)); b.Pct != -1 || b.Page != 30 {
		t.Fatalf("no page count: %+v", b)
	}
	// past the last page: capped, and the book is finished
	day2 := day1.AddDate(0, 0, 1)
	b, e, _ := s.LogPages("1", 999, -1, day2)
	if e.Pages != 250 || b.Status != readFinished || b.Finished != "2026-10-02" || b.Pct != 100 {
		t.Fatalf("finish: %+v %+v", b, e)
	}
	// a correction lowers the day's pages but never below zero
	s.LogPages("med", -40, -1, day2)
	if per := s.PagesPerDay(); per["2026-10-01"] != 180 || per["2026-10-02"] != 220 {
		t.Fatalf("pages per day %v", per)
	}

	now := day1.Add(6 * time.Hour)
	a := s.FullActivity(now, 1)
	sc := a.Scores["2026-10-01"]
	if sc.Pages != 180 || sc.PagesPts != 180/scoreReadPagesPer || sc.Total != sc.PagesPts {
		t.Fatalf("score %+v", sc)
	}
	if line := a.ScoreLine; line[len(line)-1].Total != sc.Total {
		t.Fatalf("curve %v should end on %d", line, sc.Total)
	}
	if r := a.Reading; len(r.Reading) != 1 || r.Reading[0].Title != "Meditations" || r.PagesToday != 180 {
		t.Fatalf("summary %+v", r)
	}
	// removing a book keeps its points
	if err := s.DeleteBook(dune.ID); err != nil {
		t.Fatal(err)
	}
	if got := s.FullActivity(now, 1).Scores["2026-10-01"].PagesPts; got != sc.PagesPts {
		t.Fatalf("points after removing the book: %d", got)
	}
}

func TestReadCommand(t *testing.T) {
	s := newTestStore(t, map[string]string{"inbox.md": "# Inbox\n"})
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.Local)
	run := func(args ...string) (string, int) {
		var out, errb bytes.Buffer
		code := runReadCommand(s, args, false, &out, &errb, now)
		return out.String() + errb.String(), code
	}
	if out, code := run("add", "Dune", "Frank Herbert", "400"); code != 0 || !strings.Contains(out, "400 pages") {
		t.Fatalf("add: %q", out)
	}
	if out, code := run("add", "Dune"); code == 0 {
		t.Fatalf("add without author: %q", out)
	}
	if out, _ := run("log", "dune", "40"); !strings.Contains(out, "40/400 · 10%") {
		t.Fatalf("log: %q", out)
	}
	if out, _ := run("log", "--to", "100"); !strings.Contains(out, "100/400 · 25%") {
		t.Fatalf("log --to: %q", out)
	}
	if out, _ := run("done", "1"); !strings.Contains(out, "read 2026-10-01") {
		t.Fatalf("done: %q", out)
	}
}

func TestReadingStartPage(t *testing.T) {
	s := newTestStore(t, map[string]string{"inbox.md": "# Inbox\n"})
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.Local)
	s.AddBook("Moscow", "Amor Towles", 495, now)
	b, e, err := s.SetStartPage("1", 171, now)
	if err != nil || !e.Baseline || b.Page != 171 || b.Status != readReading || b.Pct != 34 {
		t.Fatalf("start page: %+v %+v %v", b, e, err)
	}
	s.LogPages("", 20, -1, now.Add(time.Hour))
	a := s.FullActivity(now.Add(2*time.Hour), 1)
	if sc := a.Scores["2026-10-01"]; sc.Pages != 20 || sc.Total != 20/scoreReadPagesPer {
		t.Fatalf("only the 20 logged pages score: %+v", sc)
	}
	if a.Reading.PagesTotal != 20 || a.Reading.Reading[0].Page != 191 {
		t.Fatalf("summary %+v", a.Reading)
	}
	var out, errb bytes.Buffer
	if runReadCommand(s, []string{"add", "Dune", "Frank Herbert", "400", "--at", "100"}, false, &out, &errb, now) != 0 || s.Books()[1].Page != 100 {
		t.Fatalf("add --at: %q %q %+v", out.String(), errb.String(), s.Books())
	}
	if s.PagesPerDay()["2026-10-01"] != 20 {
		t.Fatalf("add --at scored: %v", s.PagesPerDay())
	}
}
