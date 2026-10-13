package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAskItems(t *testing.T) {
	pre, items := askItems("# Questions\n\n<!-- how to -->\n\nWhy is bottling less profitable\nthan concentrate?\n\n- What is MES?\n- Is a2 milk\n  a judo play?\n\n1. Learning rate?\n")
	want := []string{"Why is bottling less profitable\nthan concentrate?", "What is MES?", "Is a2 milk\na judo play?", "Learning rate?"}
	if len(items) != len(want) {
		t.Fatalf("%d items: %q", len(items), items)
	}
	for i := range want {
		if items[i].Q != want[i] || items[i].answered() {
			t.Errorf("item %d = %+v, want %q unanswered", i, items[i], want[i])
		}
	}
	_, items = askItems("# Q\n\nWhy x?\n> **Answer:** because\n>\n> more\n\n- A?\n  > **Answer:** b\n- C?\n")
	if len(items) != 3 || items[0].Q != "Why x?" || len(items[0].Ans) != 3 || items[0].Ans[1] != "" || !items[1].answered() || items[1].Q != "A?" || items[2].answered() {
		t.Errorf("answers not read: %+v", items)
	}
	if !strings.Contains(pre, "# Questions") || !strings.Contains(pre, "<!-- how to -->") {
		t.Errorf("preamble lost: %q", pre)
	}
}

func TestMergeIntoBank(t *testing.T) {
	bank := "## Long case\n\nQ: x\nA: y\n\n## Module 2: Forces\n\nQ: a\nA: b\n\n## Module 3: Cost\n\nQ: c\nA: d\n"
	out := mergeIntoBank(bank, []askSegment{
		{1, "module 2: forces", "Q: new\nA: one"},
		{2, "Module 4: Judo", "Q: j\nA: k"},
	})
	i2, in, i3, i4 := strings.Index(out, "Q: a"), strings.Index(out, "Q: new"), strings.Index(out, "## Module 3"), strings.Index(out, "## Module 4: Judo")
	if !(i2 < in && in < i3 && i3 < i4) {
		t.Errorf("wrong placement:\n%s", out)
	}
	if strings.Count(out, "## Module 2") != 1 {
		t.Errorf("existing heading duplicated:\n%s", out)
	}
	if !strings.HasSuffix(out, "## Module 4: Judo\n\nQ: j\nA: k\n") {
		t.Errorf("new heading at the end:\n%s", out)
	}
}

func TestAnswerAsks(t *testing.T) {
	s := newTestStore(t, map[string]string{
		"oam331/notes.md":     "# Notes\nMES is the smallest volume at the minimum average cost.",
		"oam331/questions.md": "## Module 3: Cost\n\nQ: old\nA: o\n",
		"oam331/ask.md":       "# Questions\n\n<!-- hi -->\n\nWhat is MES?\n\nWhat is the airspeed of a swallow?\n\nWhy does scale matter?\n",
	})
	fakeClaude(t, "chatter\n@@ 1 | Module 3: Cost\nQ: What is minimum efficient scale?\nA: the smallest volume at the minimum average cost\nSrc: ask, notes.md\n\n@@ 3 | Module 3: Scale\nQ: Why does scale matter?\nSolution:\n  Fixed cost per unit falls.\nSrc: ask\n")
	r, err := s.AnswerAsks("oam331")
	if err != nil {
		t.Fatal(err)
	}
	if r.Answered != 2 || r.Kept != 1 {
		t.Fatalf("result %+v", r)
	}
	bank, _ := os.ReadFile(filepath.Join(s.Root, "oam331", "questions.md"))
	b := string(bank)
	if !strings.Contains(b, "Q: What is minimum efficient scale?") || !strings.Contains(b, "## Module 3: Scale") || !strings.Contains(b, "Q: old") {
		t.Errorf("bank:\n%s", b)
	}
	ask, _ := os.ReadFile(filepath.Join(s.Root, "oam331", "ask.md"))
	a := string(ask)
	if !strings.Contains(a, "swallow") || !strings.Contains(a, "<!-- hi -->") ||
		!strings.Contains(a, "What is MES?\n> **Answer:** the smallest volume at the minimum average cost\n> *Filed in questions.md under Module 3: Cost*") ||
		!strings.Contains(a, "Why does scale matter?\n> **Answer:** Fixed cost per unit falls.") {
		t.Errorf("ask.md:\n%s", a)
	}
	// a second run leaves the answered ones alone and only retries the swallow
	fakeClaude(t, "nothing\n")
	r, err = s.AnswerAsks("oam331")
	if err != nil || r.Answered != 0 || r.Kept != 1 {
		t.Errorf("second run: %+v %v", r, err)
	}
	if a2, _ := os.ReadFile(filepath.Join(s.Root, "oam331", "ask.md")); string(a2) != a {
		t.Errorf("rerun changed ask.md:\n%s", a2)
	}
	if !containsStr(s.ExcludedWords(), "oam331/questions.md") {
		t.Errorf("questions.md must be excluded from new words: %v", s.ExcludedWords())
	}
}
