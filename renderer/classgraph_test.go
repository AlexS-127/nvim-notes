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
	write("lat/ablative.md", "# Ablative\n\nThe ablative case shows means. See [[first-declension#Endings]] for forms.\n\n## Means\n\nWith an instrument, no preposition.\n\n## Prepositions\n\nin, cum, de, ab\n")
	write("lat/first-declension.md", "# First declension\n\nNouns like puella. Unlike verbs, they have cases.\n\n## Endings\n\nThe ablative singular ends in ā.\n\n```\n# not a heading\n```\n")
	write("lat/verbs.md", "# Verbs\n\nconjugation tense mood voice person number\n")
	write("lat/questions.md", "Q: x\nA: y\n")
	write("lat/mistakes.md", "# Mistakes\n- a\n")
	write(".revision/questions/lat/ablative.md", "## s\nQ: What does the ablative of means need?\nA: no preposition\n\nQ: Which prepositions take the ablative?\na) in, cum\nb) ad\nA: a\n")
	write("lat/.quiz_stats.json", `{"What does the ablative of means need?":{"right":3,"wrong":0,"streak":3,"credit":1,"notes":["lat/ablative.md"]},
		"Q2":{"right":0,"wrong":2,"streak":0,"credit":0.2,"notes":["lat/verbs.md"]},"__player__":{"xp":5}}`)
	st := &Store{Root: root}
	g, err := st.ClassGraph("lat", time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]GraphNode{}
	for _, n := range g.Nodes {
		by[n.ID] = n
	}
	for _, id := range []string{"lat/ablative.md", "lat/ablative.md#means", "lat/ablative.md#prepositions", "lat/first-declension.md", "lat/first-declension.md#endings", "lat/verbs.md"} {
		if _, ok := by[id]; !ok {
			t.Errorf("missing node %s; have %v", id, len(by))
		}
	}
	if len(g.Nodes) != 6 || g.Notes != 3 {
		t.Errorf("nodes %d notes %d", len(g.Nodes), g.Notes)
	}
	if by["lat/ablative.md#means"].Parent != "lat/ablative.md" || by["lat/ablative.md#means"].Kind != "heading" {
		t.Errorf("means: %+v", by["lat/ablative.md#means"])
	}
	kinds := map[string]bool{}
	var linked bool
	for _, e := range g.Edges {
		kinds[e.Kind] = true
		if e.Kind == "link" && e.A == "lat/ablative.md" && e.B == "lat/first-declension.md#endings" {
			linked = true
		}
	}
	if !kinds["part"] || !linked {
		t.Errorf("edges: %+v", g.Edges)
	}
	// the question about means lands on the Means section; the note's own node counts it too
	if n := by["lat/ablative.md#means"]; n.Questions != 1 || n.Seen != 1 || n.Mastery == nil || *n.Mastery < 0.6 {
		t.Errorf("means section: %+v", n)
	}
	if n := by["lat/ablative.md"]; n.Questions != 2 || n.Seen != 1 {
		t.Errorf("ablative root: %+v", n)
	}
	if m := by["lat/verbs.md"].Mastery; m == nil || *m > 0.2 {
		t.Errorf("verbs mastery %v", m)
	}
	if _, ok := by["lat/mistakes.md"]; ok {
		t.Errorf("mistakes.md should not be a node")
	}
}
