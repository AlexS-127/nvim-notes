package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClassGraph(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("lat/ablative.md", "# Ablative\n\nThe ablative case shows means. See [[first-declension]] for endings.\n")
	write("lat/first-declension.md", "# First declension\n\nNouns like puella; the ablative singular ends in ā. Unlike verbs, they have cases.\n")
	write("lat/verbs.md", "# Verbs\n\nconjugation tense mood voice person number\n")
	write("lat/questions.md", "Q: x\nA: y\n")
	write("lat/.quiz_stats.json", `{"Q1":{"right":3,"wrong":0,"streak":3,"credit":1,"notes":["lat/ablative.md"]},
		"Q2":{"right":0,"wrong":2,"streak":0,"credit":0.2,"notes":["lat/verbs.md"]},"__player__":{"xp":5}}`)
	st := &Store{Root: root}
	g, err := st.ClassGraph("lat", time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) != 3 {
		t.Fatalf("nodes: %+v", g.Nodes)
	}
	var link, mention bool
	for _, e := range g.Edges {
		if e.A == "lat/ablative.md" && e.B == "lat/first-declension.md" && e.Kind == "link" {
			link = true
		}
		if e.Kind == "mention" {
			mention = true
		}
	}
	if !link || !mention {
		t.Errorf("edges: %+v", g.Edges)
	}
	by := map[string]GraphNode{}
	for _, n := range g.Nodes {
		by[n.ID] = n
	}
	if m := by["lat/ablative.md"].Mastery; m == nil || *m < 0.8 {
		t.Errorf("ablative mastery %v", m)
	}
	if m := by["lat/verbs.md"].Mastery; m == nil || *m > 0.2 {
		t.Errorf("verbs mastery %v", m)
	}
	if by["lat/first-declension.md"].Mastery != nil {
		t.Errorf("unasked note should have no mastery")
	}
}
