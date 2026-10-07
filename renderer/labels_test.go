package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckinTimesAndPrompts(t *testing.T) {
	s := newTestStore(t, map[string]string{"inbox.md": "# Inbox\n"})
	day := "2026-10-07"
	ts := s.CheckinTimes(day)
	if len(ts) != checkinsADay {
		t.Fatalf("times %v", ts)
	}
	for i, x := range ts {
		if x.Hour() < checkinFrom || x.Hour() >= checkinTo || (i > 0 && x.Sub(ts[i-1]) < time.Hour) {
			t.Fatalf("time %d out of window or order: %v", i, ts)
		}
	}
	if again := s.CheckinTimes(day); !again[0].Equal(ts[0]) {
		t.Fatal("times change within a day")
	}
	at := ts[1].Add(5 * time.Minute)
	p := s.OpenPrompts(at, 10)
	if p.Checkin != ts[1].Format(scoreStamp) {
		t.Fatalf("open check-in %+v", p)
	}
	if s.OpenPrompts(at, 600).Checkin != "" {
		t.Fatal("asked while away")
	}
	if s.OpenPrompts(ts[1].Add(checkinWindow+time.Minute), 10).Checkin == ts[1].Format(scoreStamp) {
		t.Fatal("prompt still open after its window")
	}
	if err := s.Checkin(p.Checkin, 4, at); err != nil {
		t.Fatal(err)
	}
	if s.OpenPrompts(at, 10).Checkin != "" {
		t.Fatal("answered prompt still open")
	}
	if err := s.Checkin("", 7, at); err == nil {
		t.Fatal("focus 7 accepted")
	}
	evening := time.Date(2026, 10, 7, 19, 0, 0, 0, time.Local)
	if !s.OpenPrompts(evening, 10).Checkout || s.OpenPrompts(evening.Add(-3*time.Hour), 10).Checkout {
		t.Fatal("check-out window")
	}
	if err := s.Checkout(0, 0, 0, 0, "", evening); err == nil {
		t.Fatal("empty check-out accepted")
	}
	s.Checkout(4, 3, 0, 5, "good day", evening)
	if s.OpenPrompts(evening, 10).Checkout {
		t.Fatal("check-out asked twice")
	}
	if g := s.Grade("act200", "Midterm 1", 87, 100, evening); g != nil {
		t.Fatal(g)
	}
	if s.Grade("act200", "x", 120, 100, evening) == nil {
		t.Fatal("score above max accepted")
	}
}

func TestLabelQueueAndRules(t *testing.T) {
	s := newTestStore(t, map[string]string{"inbox.md": "# Inbox\n"})
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.Local)
	qf := filepath.Join(t.TempDir(), "queue.json")
	t.Setenv("NOTESVIEW_SENSE_QUEUE", qf)
	salt := s.Sensors().Salt
	h1, h2 := saltedHash(salt, "youtube.com"), saltedHash(salt, "ACT 200 – Chapter 4.pdf")
	q := []QueueItem{{Kind: "domain", Hash: h1, Text: "youtube.com", First: now.Format(scoreStamp), Count: 3},
		{Kind: "title", Hash: h2, Text: "ACT 200 – Chapter 4.pdf", First: now.Format(scoreStamp), Count: 9},
		{Kind: "title", Hash: "old", Text: "x", First: now.AddDate(0, 0, -9).Format(scoreStamp), Count: 1}}
	b, _ := json.Marshal(q)
	os.WriteFile(qf, b, 0o600)
	got := s.LabelQueue(now)
	if len(got) != 2 || got[0].Hash != h2 {
		t.Fatalf("queue %+v", got)
	}
	var th []string
	for _, w := range tokens("ACT 200 – Chapter 4.pdf") {
		th = append(th, saltedHash(salt, w))
	}
	if err := s.Label("title", h2, "study", th, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Label("domain", h1, "nonsense", nil, now); err == nil {
		t.Fatal("unknown category accepted")
	}
	s.Skip("domain", h1, now)
	if len(s.LabelQueue(now)) != 0 {
		t.Fatal("labelled/skipped items still queued")
	}
	r := s.loadRules()
	if r.Titles[h2] != "study" || r.Tokens["study"][saltedHash(salt, "chapter")] != 1 || !r.Skipped[h1] {
		t.Fatalf("rules %+v", r)
	}
}

func TestTokensAndHash(t *testing.T) {
	if got := tokens("ACT 200 – Chapter 4: Accruals.pdf"); len(got) != 4 || got[0] != "act" || got[2] != "accruals" {
		t.Fatalf("tokens %v", got)
	}
	if saltedHash("s", "Hello ") != saltedHash("s", "hello") || saltedHash("s", "a") == saltedHash("t", "a") {
		t.Fatal("hash")
	}
}

func TestOutcomeRating(t *testing.T) {
	sc := map[string]Score{}
	for i, v := range []int{10, 20, 30, 40, 50, 60} {
		sc[addDays("2026-10-01", i)] = Score{Total: v}
	}
	sc["2026-10-07"] = Score{Total: 55}
	if r := OutcomeRating(sc, "2026-10-07"); r != 5 {
		t.Fatalf("rating %d", r)
	}
	sc["2026-10-07"] = Score{Total: 5}
	if r := OutcomeRating(sc, "2026-10-07"); r != 1 {
		t.Fatalf("low rating %d", r)
	}
	if OutcomeRating(sc, "2026-10-03") != 0 {
		t.Fatal("rated with too few days")
	}
}
