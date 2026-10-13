package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func topicStore(t *testing.T) *Store {
	return newTestStore(t, map[string]string{
		"lat101/subjunctive.md":    "# The subjunctive mood\n\n## Uses\n\nPurpose clauses take the subjunctive.\n",
		"lat101/nouns.md":          "# First declension nouns\n\nPuella, puellae.\n",
		"act200/accruals.md":       "# Accruals\n\nRevenue is recognised when earned. A subjunctive aside.\n",
		"act200/questions.md":      "## x\nQ: subjunctive?\nA: y\n",
		"daily/2026-10-07.md":      "# subjunctive diary\n",
		".revision/questions/x.md": "subjunctive\n",
	})
}

func TestTopicNotes(t *testing.T) {
	s := topicStore(t)
	got := s.topicNotes("the subjunctive", "", "")
	if len(got) == 0 || got[0] != "lat101/subjunctive.md" {
		t.Fatalf("best match first: %v", got)
	}
	for _, id := range got {
		if strings.HasPrefix(id, "daily/") || strings.HasSuffix(id, "questions.md") || strings.HasPrefix(id, ".") {
			t.Errorf("searched %s", id)
		}
	}
	if got := s.topicNotes("subjunctive", "", "act200"); len(got) != 0 {
		t.Errorf("a single passing mention is not a match: %v", got)
	}
	if got := s.topicNotes("subjunctive", "", "lat101"); len(got) != 1 || got[0] != "lat101/subjunctive.md" {
		t.Errorf("limited to a class: %v", got)
	}
	if got := s.topicNotes("zzzz", "", ""); len(got) != 0 {
		t.Errorf("no match: %v", got)
	}
	if got := s.topicNotes("", "lat101/nouns.md", ""); len(got) != 1 {
		t.Errorf("given note: %v", got)
	}
	if got := s.topicNotes("", "../etc/passwd", ""); len(got) != 0 {
		t.Errorf("escape: %v", got)
	}
}

func TestTopicQuiz(t *testing.T) {
	s := topicStore(t)
	fakeClaude(t, "```\nQ: Purpose clauses take which mood? [gen]\nA: subjunctive\n\nQ: Pick [gen]\na) x\nb) y\nA: a\n\nQ: Why? [gen]\nA: because\n```\n")
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.Local)
	q, err := s.StartQuiz(QuizStart{Mode: "topic", Topic: "subjunctive mood", Count: 5}, now)
	if err != nil {
		t.Fatal(err)
	}
	if q.Subject != "lat101" || len(q.queue) != 3 || q.cur == nil || q.cur.card.Total != 3 || !strings.HasPrefix(q.Title, "Topic: ") {
		t.Fatalf("session: %s %d %+v %s", q.Subject, len(q.queue), q.cur, q.Title)
	}
	if _, err := os.Stat(filepath.Join(s.Root, ".topics", "questions", "subjunctive-mood.md")); err != nil {
		t.Error("bank not saved:", err)
	}
	for i := 0; i < 20 && !q.done; i++ {
		if q.cur == nil {
			s.NextQuiz(q, now)
			continue
		}
		s.AnswerQuiz(q, QuizAnswer{Response: "wrong"}, now)
		s.NextQuiz(q, now)
	}
	if !q.done || q.summary == nil || len(q.summary.Revisions) != 0 || len(s.Topics()) != 0 {
		t.Fatalf("a topic quiz must not touch revision: %+v", q.summary)
	}
	if len(q.retry) == 0 {
		t.Error("missed ones are asked again")
	}
	// no note matches: general knowledge, stats under .topics
	q, err = s.StartQuiz(QuizStart{Mode: "topic", Topic: "plate tectonics"}, now)
	if err != nil || q.Subject != topicSubject || !strings.Contains(q.intro, "general knowledge") {
		t.Fatalf("no notes: %v %s %q", err, q.Subject, q.intro)
	}
	if _, err := s.StartQuiz(QuizStart{Mode: "topic", Topic: "x", Subject: "nope"}, now); err == nil {
		t.Error("unknown class accepted")
	}
	if _, err := s.StartQuiz(QuizStart{Mode: "topic"}, now); err == nil {
		t.Error("empty topic accepted")
	}
	fakeClaude(t, "I cannot.\n")
	if _, err := s.StartQuiz(QuizStart{Mode: "topic", Topic: "subjunctive"}, now); err == nil || !strings.Contains(err.Error(), "usable") {
		t.Errorf("too few questions: %v", err)
	}
}
