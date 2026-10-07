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

func TestScreenPreviews(t *testing.T) {
	s := newTestStore(t, map[string]string{"inbox.md": "# Inbox\n"})
	now := time.Now()
	qf := filepath.Join(t.TempDir(), "queue.json")
	t.Setenv("NOTESVIEW_SENSE_QUEUE", qf)
	a, b, c := "00000000000000aa", "00000000000000bb", "00000000000000cc"
	q := []QueueItem{{Kind: "screen", Hash: a, First: now.Format(scoreStamp), Count: 1},
		{Kind: "screen", Hash: b, First: now.Format(scoreStamp), Count: 1}}
	j, _ := json.Marshal(q)
	os.WriteFile(qf, j, 0o600)
	os.MkdirAll(previewDir(), 0o700)
	pic := func(h string, age time.Duration) string {
		p := filepath.Join(previewDir(), h+".jpg")
		os.WriteFile(p, []byte("jpg"), 0o600)
		os.Chtimes(p, now.Add(-age), now.Add(-age))
		return p
	}
	pa, pb := pic(a, time.Hour), pic(b, 25*time.Hour)                 // b is too old
	pc, pn := pic(c, time.Hour), pic("00000000000000dd", time.Minute) // c: not queued; dd: just written
	got := s.LabelQueue(now)
	if len(got) != 2 || !(got[0].Preview != got[1].Preview) {
		t.Fatalf("queue %+v", got)
	}
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	if !exists(pa) || exists(pb) || exists(pc) || !exists(pn) {
		t.Fatal("prune: keep fresh queued and just-written previews, drop stale and orphaned ones")
	}
	if screenPreview("../queue", now) != "" || screenPreview(b, now) != "" || screenPreview(a, now) != pa {
		t.Fatal("screenPreview")
	}
	if err := s.Label("screen", a, "study", nil, now); err != nil || exists(pa) {
		t.Fatal("labelled screen keeps its preview")
	}
	pb = pic(b, time.Hour)
	s.Skip("screen", b, now)
	if exists(pb) {
		t.Fatal("skipped screen keeps its preview")
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
	if r := OutcomeRating(sc, "2026-10-07", ""); r != 5 {
		t.Fatalf("rating %d", r)
	}
	sc["2026-10-07"] = Score{Total: 5}
	if r := OutcomeRating(sc, "2026-10-07", ""); r != 1 {
		t.Fatalf("low rating %d", r)
	}
	if OutcomeRating(sc, "2026-10-03", "") != 0 {
		t.Fatal("rated with too few days")
	}
	// data start: test days are neither rated nor part of the comparison
	if OutcomeRating(sc, "2026-10-07", "2026-10-03") != 0 || OutcomeRating(sc, "2026-10-02", "2026-10-03") != 0 {
		t.Fatal("rated against or on test days")
	}
}

func TestSleepLog(t *testing.T) {
	s := newTestStore(t, map[string]string{"inbox.md": "# Inbox\n"})
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.Local)
	if p := s.OpenPrompts(now, 0); !p.Sleep {
		t.Fatal("sleep not asked")
	}
	if err := s.LogSleep("23:30", "7:15", "", now); err != nil {
		t.Fatal(err)
	}
	if h, ok := s.SleepWindow("2026-10-07"); !ok || h != 7.8 {
		t.Fatalf("sleep %v %v", h, ok)
	}
	if b, _, _ := s.SleepLog("2026-10-07"); b.Day() != 6 || b.Hour() != 23 {
		t.Fatalf("bed %v", b)
	}
	if p := s.OpenPrompts(now, 0); p.Sleep {
		t.Fatal("sleep asked after logging")
	}
	s.LogSleep("0100", "0830", "", now.Add(time.Hour)) // a correction replaces it; bed after midnight
	if h, _ := s.SleepWindow("2026-10-07"); h != 7.5 {
		t.Fatalf("corrected sleep %v", h)
	}
	for _, bad := range [][2]string{{"23:30", "9:30"}, {"25:00", "7:00"}, {"7:00", "7:20"}} {
		if s.LogSleep(bad[0], bad[1], "", now) == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
	if s.LogSleep("22:00", "6:00", "2026-10-05", now) != nil {
		t.Fatal("backfill for an earlier day")
	}
	s.Skip("sleep", "", now.AddDate(0, 0, 1))
	if p := s.OpenPrompts(now.AddDate(0, 0, 1), 0); p.Sleep {
		t.Fatal("skipped sleep still asked")
	}
	if r := s.loadRules(); r.Skipped[""] {
		t.Fatal("sleep skip written as a rule")
	}
}

func TestSharedNetwork(t *testing.T) {
	s := newTestStore(t, map[string]string{"inbox.md": "# Inbox\n"})
	now := time.Now()
	qf := filepath.Join(t.TempDir(), "queue.json")
	t.Setenv("NOTESVIEW_SENSE_QUEUE", qf)
	net, apLib, apDorm := "00000000000000a1", "00000000000000b1", "00000000000000b2"
	s.updateRules(func(r *Rules) { r.Places[net] = "library" })
	q := []QueueItem{{Kind: "place", Hash: apLib, Text: "campus · access point …aa:01", Net: net, First: now.Format(scoreStamp), Count: 1},
		{Kind: "place", Hash: apDorm, Text: "campus · access point …bb:02", Net: net, First: now.Format(scoreStamp), Count: 1}}
	j, _ := json.Marshal(q)
	os.WriteFile(qf, j, 0o600)
	s.Label("place", apLib, "library", nil, now)
	if s.loadRules().Shared[net] {
		t.Fatal("same place as the network: not shared")
	}
	s.Label("place", apDorm, "home", nil, now)
	if r := s.loadRules(); !r.Shared[net] || r.Places[apDorm] != "home" {
		t.Fatalf("rules %+v", r)
	}
}
