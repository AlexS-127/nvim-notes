package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNextStep(t *testing.T) {
	iv := defaultIntervals
	cases := []struct {
		step  int
		score float64
		want  int
	}{{0, 0.9, 1}, {2, 0.8, 3}, {2, 0.79, 2}, {2, 0.5, 2}, {3, 0.49, 0}, {6, 1, 6}}
	for _, c := range cases {
		if got := NextStep(c.step, c.score, iv); got != c.want {
			t.Errorf("NextStep(%d, %v) = %d, want %d", c.step, c.score, got, c.want)
		}
	}
}

func revStore(t *testing.T) *Store {
	return newTestStore(t, map[string]string{
		"act200/accruals.md":  "# Accruals\n\nRevenue is recognised when earned.\n",
		"act200/deferrals.md": "# Deferrals\n\nCash first, revenue later.\n",
		"misc/random.md":      "# Random\n",
		"act200/questions.md": "## x\n",
		".words.json":         `{"tracked":["act200","misc","daily"]}`,
	})
}

func TestRevisionSchedule(t *testing.T) {
	s := revStore(t)
	day1 := time.Date(2026, 10, 7, 10, 0, 0, 0, time.Local)
	cfg := s.RevisionSettings(day1)
	if len(cfg.Folders) != 1 || cfg.Folders[0] != "act200" || cfg.MinWords != 50 || cfg.Since != "2026-09-30" {
		t.Fatalf("default config %+v", cfg)
	}
	// words log: 60 words in accruals on day 1, 20 in deferrals (too few), 80 in misc (not watched),
	// 100 in accruals long before `since`
	log := `{"at":"2026-10-07T09:00:00","date":"2026-10-07","file":"act200/accruals.md","words":40}
{"at":"2026-10-07T09:30:00","date":"2026-10-07","file":"act200/accruals.md","words":20}
{"at":"2026-10-07T09:30:00","date":"2026-10-07","file":"act200/deferrals.md","words":20}
{"at":"2026-10-07T09:30:00","date":"2026-10-07","file":"misc/random.md","words":80}
{"at":"2026-01-01T09:30:00","date":"2026-01-01","file":"act200/deferrals.md","words":100}
`
	os.WriteFile(filepath.Join(s.Root, wordsLog), []byte(log), 0o644)
	s.RecordRevision(day1)
	ts := s.Topics()
	if len(ts) != 1 || ts[0].ID != "act200/accruals.md" || ts[0].Title != "Accruals" || ts[0].Due != "2026-10-08" || ts[0].Gen.Status != "none" {
		t.Fatalf("topics %+v", ts)
	}
	if len(s.RevisionDue(day1)) != 0 {
		t.Fatal("due on the day it was learned")
	}
	if _, err := s.AddTopic("act200/deferrals.md", day1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTopic("act200/questions.md", day1); err == nil {
		t.Fatal("a question bank was scheduled")
	}
	day2 := day1.AddDate(0, 0, 1)
	due := s.RevisionDue(day2)
	if len(due) != 2 || due[0].ID != "act200/accruals.md" {
		t.Fatalf("due %+v", due)
	}
	// revise the first: 0.9 → step 1 (3 days), +5; a second revision the same day scores nothing
	tp, e, err := s.RevisionDone("act200/accruals.md", 0.9, 9, 10, day2)
	if err != nil || tp.Step != 1 || tp.Due != "2026-10-11" || !e.Points {
		t.Fatalf("done %+v %+v %v", tp, e, err)
	}
	if _, e, _ = s.RevisionDone("act200/accruals.md", 1, 5, 5, day2); e.Points {
		t.Fatal("second revision of a topic the same day scored")
	}
	if _, e, _ = s.RevisionDone("act200/deferrals.md", 0.3, 1, 2, day2); e.Points {
		t.Fatal("a revision with 2 answers scored")
	}
	if next := s.RevisionDue(day2); len(next) != 1 || next[0].ID != "act200/deferrals.md" || next[0].Due != "2026-10-08" {
		// deferrals' poor (uncounted) revision still reset it: due tomorrow
		if len(next) != 0 {
			t.Fatalf("next %+v", next)
		}
	}
	a := s.FullActivity(day2.Add(time.Hour), 1)
	sc := a.Scores["2026-10-08"]
	if sc.Revisions != 1 || sc.RevisionPts != scoreRevisionPts {
		t.Fatalf("score %+v", sc)
	}
	if line := a.ScoreLine; line[len(line)-1].Total != sc.Total {
		t.Fatalf("curve %v ends off %d", line, sc.Total)
	}
	// removing keeps it off until new writing after the removal
	s.RemoveTopic("act200/accruals.md", day2)
	s.RecordRevision(day2)
	for _, x := range s.Topics() {
		if x.ID == "act200/accruals.md" {
			t.Fatal("removed topic came back without new writing")
		}
	}
}

func TestGenerateQuestions(t *testing.T) {
	s := revStore(t)
	now := time.Date(2026, 10, 7, 10, 0, 0, 0, time.Local)
	s.AddTopic("act200/accruals.md", now)
	dir := t.TempDir()
	fake := filepath.Join(dir, "claude")
	good := "Sure! Here they are:\n```\n## Wrong heading\n\nQ: Revenue is recognised when cash arrives. [gen]\nA: false\nSrc: act200/accruals.md\n\nQ: What is an accrual? [gen]\nA: revenue earned before cash\n\nQ: Pick one [gen]\na) x\nb) y\nA: a\n\nQ: no answer\n```\n"
	os.WriteFile(fake, []byte("#!/bin/sh\ncat >/dev/null\ncat <<'EOF'\n"+good+"EOF\n"), 0o755)
	t.Setenv("NOTESVIEW_CLAUDE", fake)
	if s.needsQuestions(now) != "act200/accruals.md" {
		t.Fatal("new topic should need questions")
	}
	n, err := s.GenerateQuestions("act200/accruals.md", now)
	if err != nil || n != 3 {
		t.Fatal(n, err)
	}
	b, _ := os.ReadFile(s.QuestionsPath("act200/accruals.md"))
	if !strings.HasPrefix(string(b), "## Accruals\n\nQ: Revenue") || strings.Contains(string(b), "Wrong heading") || strings.Contains(string(b), "```") {
		t.Fatalf("questions file:\n%s", b)
	}
	if s.Topics()[0].Gen.Status != "ok" || s.needsQuestions(now) != "" {
		t.Fatalf("gen %+v", s.Topics()[0].Gen)
	}
	// a bank that exists before any generation (written by hand) is kept
	s.AddTopic("act200/deferrals.md", now)
	os.MkdirAll(filepath.Dir(s.QuestionsPath("act200/deferrals.md")), 0o755)
	os.WriteFile(s.QuestionsPath("act200/deferrals.md"), []byte("## D\n\nQ: x\nA: y\n"), 0o644)
	if id := s.needsQuestions(now); id != "" {
		t.Fatalf("hand-written bank would be replaced: %s", id)
	}
	// too few questions: failed, retried tomorrow, not today
	os.WriteFile(fake, []byte("#!/bin/sh\ncat >/dev/null\necho 'Q: one [gen]'\necho 'A: x'\n"), 0o755)
	s.RequestQuestions("act200/accruals.md")
	if _, err := s.GenerateQuestions("act200/accruals.md", now); err == nil {
		t.Fatal("one question accepted")
	}
	if s.needsQuestions(now) != "" || s.needsQuestions(now.AddDate(0, 0, 1)) != "act200/accruals.md" {
		t.Fatal("failed generation retry rule")
	}
}

func TestReviseCommand(t *testing.T) {
	s := revStore(t)
	now := time.Date(2026, 10, 7, 10, 0, 0, 0, time.Local)
	run := func(args ...string) string {
		var out, errb bytes.Buffer
		runReviseCommand(s, args, false, &out, &errb, now)
		return out.String() + errb.String()
	}
	if out := run("add", "act200/accruals.md"); !strings.Contains(out, "first revision 2026-10-08") {
		t.Fatal(out)
	}
	if out := run("due"); !strings.Contains(out, "Nothing to revise") {
		t.Fatal(out)
	}
	if out := run("done", "act200/accruals.md", "--score", "0.85", "--correct", "6", "--total", "7"); !strings.Contains(out, "(+5)") {
		t.Fatal(out)
	}
	if out := run("done", "act200/accruals.md"); !strings.Contains(out, "usage") {
		t.Fatal(out)
	}
}
