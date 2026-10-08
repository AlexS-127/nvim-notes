package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeClaude(t *testing.T, out string) {
	fake := filepath.Join(t.TempDir(), "claude")
	os.WriteFile(fake, []byte("#!/bin/sh\ncat >/dev/null\ncat <<'EOF'\n"+out+"\nEOF\n"), 0o755)
	t.Setenv("NOTESVIEW_CLAUDE", fake)
}

func TestBankBlocks(t *testing.T) {
	bs := bankBlocks("## T\n\nQ: Which is NOT revenue? [gen]\na) a\nb) b\nA: a\n\nQ: Fill in the blank: revenue is ____. [gen]\nA: earned\n\nQ: One? [gen]\nA: true\n\nQ: Two? [gen]\nA: false\n\nQ: Three? [gen]\nA: true\n\nQ: Why accrue? [gen]\nA: matching\nWhy: m\n\nQ: Why accrue? [gen]\nA: again\n")
	want := []string{"negative stem (NOT/EXCEPT)", "fill-in-the-blank copied from the note", "", "", "too many true/false", "", "repeats another question"}
	if len(bs) != len(want) {
		t.Fatalf("%d blocks: %+v", len(bs), bs)
	}
	for i, b := range bs {
		if b.weak != want[i] {
			t.Errorf("block %d %q: weak %q, want %q", i, b.key, b.weak, want[i])
		}
	}
	if !strings.HasSuffix(bs[5].raw, "Why: m") {
		t.Errorf("raw block %q", bs[5].raw)
	}
}

func TestBankHealthAndRepair(t *testing.T) {
	s := revStore(t)
	now := time.Now()
	s.AddTopic("act200/accruals.md", now)
	s.AddTopic("act200/deferrals.md", now)
	bank := "## Accruals\n\nQ: What is an accrual? [gen]\nA: earned before cash\nSrc: act200/accruals.md\n\nQ: Which is NOT an accrual? [gen]\na) x\nb) y\nA: a\n\nQ: When is revenue recognised? [gen]\nA: when earned\n\nQ: Who records accruals? [gen]\nA: the accountant\n"
	os.MkdirAll(filepath.Dir(s.QuestionsPath("act200/accruals.md")), 0o755)
	os.WriteFile(s.QuestionsPath("act200/accruals.md"), []byte(bank), 0o644)
	old := now.Add(-48 * time.Hour)
	os.Chtimes(s.QuestionsPath("act200/accruals.md"), old, old)
	s.setGen("act200/accruals.md", RevisionGen{Status: "ok", At: old.Format(scoreStamp), Words: 3, Count: 3})
	os.MkdirAll(filepath.Join(s.Root, "format"), 0o755)
	os.WriteFile(filepath.Join(s.Root, "format", "revision-questions.md"), []byte("good questions"), 0o644)
	os.WriteFile(filepath.Join(s.Root, "act200", ".quiz_stats.json"), []byte(`{"What is an accrual? [gen]":{"right":4,"wrong":1,"streak":1,"notes":["act200/accruals.md"]},"When is revenue recognised? [gen]":{"right":3,"wrong":1,"streak":3,"notes":["act200/accruals.md"]},"Who records accruals? [gen]":{"right":3,"wrong":0,"streak":3,"notes":["act200/accruals.md"]}}`), 0o644)

	rows := s.BankHealth("act200", now)
	if len(rows) != 2 || rows[0].ID != "act200/accruals.md" {
		t.Fatalf("rows %+v", rows)
	}
	codes := map[string]bool{}
	for _, r := range rows[0].Reasons {
		codes[r.Code] = true
	}
	for _, c := range []string{"guidance", "note", "weak", "worn", "few"} {
		if !codes[c] {
			t.Errorf("missing reason %s: %+v", c, rows[0].Reasons)
		}
	}
	if rows[0].Questions != 3 || rows[0].Known != 2 || len(rows[0].Weak) != 1 || rows[0].Edited {
		t.Errorf("row %+v", rows[0])
	}
	if rows[1].Reasons[0].Code != "none" {
		t.Errorf("deferrals %+v", rows[1])
	}

	// repair: worn: the two known well are replaced; question 1 is kept word for word
	fakeClaude(t, "Here you go\n@@@ act200/accruals.md\nDROP: none\nQ: Select all accruals. [gen]\na) wages owed\nb) prepaid rent\nc) interest earned\nA: a, c\nWhy: w\nSrc: act200/accruals.md\n\nQ: Why are accruals needed? [gen]\nA: matching\nSrc: act200/accruals.md\n\n@@@ act200/deferrals.md\nDROP: none\nQ: What is a deferral? [gen]\nA: cash before revenue\n\nQ: Is unearned revenue a liability? [gen]\nA: true\n\nQ: Name one deferral. [gen]\nA: prepaid rent\n")
	res := s.RebuildBanks("act200", []string{"act200/accruals.md", "act200/deferrals.md"}, "repair", nil)
	for id, err := range res {
		if err != nil {
			t.Fatal(id, err)
		}
	}
	b, _ := os.ReadFile(s.QuestionsPath("act200/accruals.md"))
	got := string(b)
	if !strings.HasPrefix(got, "## Accruals\n\nQ: What is an accrual? [gen]\nA: earned before cash\nSrc: act200/accruals.md\n\nQ: Select all") ||
		strings.Contains(got, "NOT") || strings.Contains(got, "When is revenue") || strings.Contains(got, "Who records") || strings.Contains(got, "@@@") || strings.Contains(got, "DROP") {
		t.Fatalf("repaired bank:\n%s", got)
	}
	for _, tp := range s.Topics() {
		if tp.Gen.Status != "ok" || tp.Gen.Mode != "repair" {
			t.Errorf("gen %+v", tp.Gen)
		}
	}
	// a topic Claude left out keeps its old bank and status
	fakeClaude(t, "@@@ act200/deferrals.md\nQ: a [gen]\nA: b\n")
	res = s.RebuildBanks("act200", []string{"act200/accruals.md"}, "full", nil)
	if res["act200/accruals.md"] == nil {
		t.Fatal("missing section accepted")
	}
	if b2, _ := os.ReadFile(s.QuestionsPath("act200/accruals.md")); string(b2) != got {
		t.Fatal("bank changed after a failed rebuild")
	}
	if s.Topics()[0].Gen.Status != "ok" {
		t.Fatalf("status %+v", s.Topics()[0].Gen)
	}
}

func TestCheckNotes(t *testing.T) {
	s := revStore(t)
	now := time.Now()
	s.RecordWords(now) // baseline the word counts
	fakeClaude(t, `[
 {"kind":"fact","note":"act200/accruals.md","severity":"imprecise","find":"Revenue is recognised when earned.","replace":"Revenue is recognised when earned, not when cash arrives.","why":"w"},
 {"kind":"fact","note":"act200/accruals.md","severity":"wrong","find":"not in the note","replace":"x","why":"w"},
 {"kind":"link","note":"act200/deferrals.md","find":"revenue","target":"act200/accruals.md","heading":"accruals","why":"w"},
 {"kind":"link","note":"act200/deferrals.md","find":"Cash","target":"act200/deferrals.md","why":"self"}
]`)
	n, err := s.CheckNotes("act200", nil, now)
	if err != nil || n != 2 {
		t.Fatal(n, err)
	}
	c, changed := s.CheckView("act200")
	if changed != 0 || len(c.Findings) != 2 {
		t.Fatalf("%d changed, %+v", changed, c.Findings)
	}
	var fact, link NoteFinding
	for _, f := range c.Findings {
		if f.Kind == "fact" {
			fact = f
		} else {
			link = f
		}
	}
	if fact.Line != 3 || link.Line != 3 {
		t.Errorf("lines %d %d", fact.Line, link.Line)
	}
	if link.Replace != "[[accruals#Accruals|revenue]]" {
		t.Errorf("link %q", link.Replace)
	}
	em := s.parseFindings(`[{"kind":"link","note":"a.md","find":"*esse*","target":"b.md"}]`, map[string]string{"a.md": "forms of *esse* here", "b.md": "# B\n"})
	if len(em) != 1 || em[0].Replace != "*[[b|esse]]*" {
		t.Errorf("emphasised link %+v", em)
	}
	if _, err := s.ResolveFindings("act200", []string{fact.ID, link.ID}, true, "", now); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(s.Root, "act200/accruals.md"))
	if !strings.Contains(string(b), "not when cash arrives") {
		t.Fatalf("not applied:\n%s", b)
	}
	if added := s.RecordWords(now); len(added) != 0 {
		t.Fatalf("applied suggestions counted as new words: %v", added)
	}
	if fx := s.factFixes("act200"); len(fx["act200/accruals.md"]) != 1 {
		t.Fatalf("fact fixes %v", fx)
	}
	// a dismissed suggestion is not offered again
	fakeClaude(t, `[{"kind":"fact","note":"act200/deferrals.md","severity":"unclear","find":"Cash first","replace":"Cash comes first","why":"w"}]`)
	s.CheckNotes("act200", nil, now)
	c, changed = s.CheckView("act200")
	var id string
	for _, f := range c.Findings {
		if f.Status == "open" {
			id = f.ID
		}
	}
	if changed != 0 || id == "" {
		t.Fatalf("changed %d, findings %+v", changed, c.Findings)
	}
	s.ResolveFindings("act200", []string{id}, false, "", now)
	s.CheckNotes("act200", nil, now)
	c, _ = s.CheckView("act200")
	for _, f := range c.Findings {
		if f.Status == "open" {
			t.Fatalf("dismissed suggestion came back: %+v", f)
		}
	}
}
