package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestQuizMatching(t *testing.T) {
	cases := []struct {
		guess, answer string
		ok            bool
	}{
		{"girl", "girl, maiden (noun)", true},
		{"Maiden!", "girl, maiden (noun)", true},
		{"boy", "girl, maiden (noun)", false},
		{"she said", "he, she, it said (verb)", true},
		{"puella", "puellā", true}, // macrons ignored
		{"", "x", false},
		{"liabilities", "liabilities; debts", true},
	}
	for _, c := range cases {
		if got := quizCheck(c.guess, c.answer); got != c.ok {
			t.Errorf("check(%q, %q) = %v", c.guess, c.answer, got)
		}
	}
	if o := quizOthers("girl", "girl, maiden (noun)"); len(o) != 1 || o[0] != "maiden" {
		t.Errorf("others: %v", o)
	}
	if got := mergedMeaning("girl, maiden (noun)", "lass"); got != "girl, maiden, lass (noun)" {
		t.Errorf("merged: %q", got)
	}
}

func TestQuizBankParse(t *testing.T) {
	s := newTestStore(t, map[string]string{"t/questions.md": "## Moods\nQ: Select all moods.\na) Imperative\nb) Indicative\nc) Passive\nd) Subjunctive\nA: a, b, d\nWhy: Passive is a voice.\n\nQ: Work it\nSolution:\n  42\n\nQ: Is it true?\nA: true\n\nQ: bad\na) x\nA: z\n"})
	items, kind := s.quizBank("t")
	if kind != "questions" || len(items) != 3 {
		t.Fatalf("items %d kind %s", len(items), kind)
	}
	want := []string{"multi", "problem", "tf"}
	for i, it := range items {
		if it.kind() != want[i] || it.group != "Moods" {
			t.Errorf("item %d: %s %s", i, it.kind(), it.group)
		}
	}
}

func quizStatsOf(t *testing.T, s *Store, sub string) map[string]map[string]any {
	b, err := os.ReadFile(filepath.Join(s.Root, sub, ".quiz_stats.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestQuizPracticeVocab(t *testing.T) {
	s := newTestStore(t, map[string]string{"lat/definitions.md": "- puella, puellae, f. :: girl, maiden (noun)\n"})
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.Local)
	q, err := s.StartQuiz(QuizStart{Mode: "practice", Subject: "lat", Dir: 0}, now)
	if err != nil || q.cur == nil || q.cur.card.Prompt != "puella, puellae, f." {
		t.Fatalf("start: %v %+v", err, q)
	}
	// right answer: XP, stats, time log
	res, err := s.AnswerQuiz(q, QuizAnswer{Response: "Maiden"}, now.Add(5*time.Second))
	if err != nil || res.Credit != 1 || res.XP != 10 || res.Pending {
		t.Fatalf("right: %v %+v", err, res)
	}
	s.NextQuiz(q, now.Add(6*time.Second))
	// wrong, then "Actually right?": saved as a meaning, counted right
	res, _ = s.AnswerQuiz(q, QuizAnswer{Response: "lass"}, now.Add(10*time.Second))
	if !res.Pending || !res.Override {
		t.Fatalf("wrong should wait for override: %+v", res)
	}
	res, err = s.OverrideQuiz(q, now.Add(11*time.Second))
	if err != nil || res.Credit != 1 || res.Saved != "lass" {
		t.Fatalf("override: %v %+v", err, res)
	}
	b, _ := os.ReadFile(filepath.Join(s.Root, "lat/definitions.md"))
	if !strings.Contains(string(b), "girl, maiden, lass (noun)") || !strings.HasPrefix(string(b), "- puella") {
		t.Errorf("meaning not saved: %q", b)
	}
	s.NextQuiz(q, now.Add(12*time.Second))
	// wrong and confirmed: mistakes.md, reask
	s.AnswerQuiz(q, QuizAnswer{Response: "boy"}, now.Add(15*time.Second))
	s.NextQuiz(q, now.Add(16*time.Second))
	sig, _ := os.ReadFile(filepath.Join(s.Root, ".signals", "2026-10-07.jsonl"))
	if !strings.Contains(string(sig), `"latency_ms":5000`) || !strings.Contains(string(sig), `"latency_ms":3000`) || strings.Contains(string(sig), `"latency_ms":0`) {
		t.Errorf("latencies (a wrong answer must keep its own): %s", sig)
	}
	m, _ := os.ReadFile(filepath.Join(s.Root, "lat/mistakes.md"))
	if !strings.Contains(string(m), "you: boy") {
		t.Errorf("mistake not listed: %s", m)
	}
	st := quizStatsOf(t, s, "lat")
	e := st["puella, puellae, f."]
	if e["right"].(float64) != 2 || e["wrong"].(float64) != 1 || e["reask"] != true {
		t.Errorf("stats: %v", e)
	}
	if st[quizPlayer]["xp"].(float64) != 20 {
		t.Errorf("player: %v", st[quizPlayer])
	}
	log, _ := os.ReadFile(filepath.Join(s.Root, quizLog))
	if strings.Count(string(log), `"subject":"lat"`) != 3 || !strings.Contains(string(log), `"xp":10`) {
		t.Errorf("quiz log: %s", log)
	}
	s.EndQuiz(q, now.Add(20*time.Second))
	if q.summary == nil || q.summary.Answered != 3 {
		t.Errorf("summary: %+v", q.summary)
	}
}

func TestQuizSelectAllPartial(t *testing.T) {
	s := newTestStore(t, map[string]string{"t/questions.md": "Q: Select all moods.\na) Imperative\nb) Indicative\nc) Passive\nd) Subjunctive\nA: a, b, d\n"})
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.Local)
	q, err := s.StartQuiz(QuizStart{Mode: "practice", Subject: "t"}, now)
	if err != nil {
		t.Fatal(err)
	}
	pos := map[string]int{}
	for i, c := range q.cur.card.Choices {
		pos[c] = i
	}
	res, _ := s.AnswerQuiz(q, QuizAnswer{Choices: []int{pos["Imperative"], pos["Indicative"], pos["Passive"]}}, now)
	if res.Credit < 0.33 || res.Credit > 0.34 || res.Partial != "2 of 3 right, 1 wrong pick" {
		t.Errorf("partial: %+v", res)
	}
}

func TestQuizRevision(t *testing.T) {
	s := newTestStore(t, map[string]string{
		"a/one.md": "# One\n\nbody\n", "a/two.md": "# Two\n\nbody\n",
		".revision/questions/a/one.md": "## One\nQ: one-1?\nA: x\n\nQ: one-2?\nA: y\n\nQ: one-3?\nA: z\n",
		".revision/questions/a/two.md": "## Two\nQ: two-1?\nA: x\n\nQ: two-2?\nA: y\n\nQ: two-3?\nA: z\n",
	})
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.Local)
	for _, id := range []string{"a/one.md", "a/two.md"} {
		if _, err := s.AddTopic(id, now.AddDate(0, 0, -2)); err != nil {
			t.Fatal(err)
		}
	}
	q, err := s.StartQuiz(QuizStart{Mode: "revise", ID: "a/one.md"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(q.torder) != 2 || len(q.queue) != 6 || !strings.HasPrefix(q.Title, "Revision (mixed)") {
		t.Fatalf("blend: %v %d %s", q.torder, len(q.queue), q.Title)
	}
	answers := map[string]string{"one-1?": "x", "one-2?": "y", "one-3?": "z", "two-1?": "x", "two-2?": "wrong", "two-3?": "z"}
	for i := 0; i < 20 && !q.done; i++ {
		if q.cur == nil {
			s.NextQuiz(q, now)
			continue
		}
		s.AnswerQuiz(q, QuizAnswer{Response: answers[q.cur.card.Prompt]}, now)
		s.NextQuiz(q, now)
	}
	if !q.done || q.summary == nil || len(q.summary.Revisions) != 2 {
		t.Fatalf("not finished: %+v", q.summary)
	}
	if len(q.retry) != 1 {
		t.Errorf("retry round: %d", len(q.retry))
	}
	for _, tp := range s.Topics() {
		if tp.Count != 1 {
			t.Errorf("%s not recorded: %+v", tp.ID, tp)
		}
	}
}

func TestQuizRecall(t *testing.T) {
	s := newTestStore(t, map[string]string{"a/one.md": "# One\n\n## Part\n\nbody\n"})
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.Local)
	s.AddTopic("a/one.md", now.AddDate(0, 0, -2))
	q, err := s.StartQuiz(QuizStart{Mode: "revise", ID: "a/one.md"}, now)
	if err != nil || q.cur == nil || q.cur.card.Kind != "recall" || len(q.cur.card.Headings) != 2 {
		t.Fatalf("recall: %v %+v", err, q.cur)
	}
	if _, err := s.AnswerQuiz(q, QuizAnswer{Grade: "3"}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if !q.done || s.Topics()[0].Count != 1 {
		t.Errorf("recall not recorded: %+v", s.Topics()[0])
	}
}
