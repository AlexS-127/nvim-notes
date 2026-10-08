package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Check notes (class page): Claude reads every note of a course in one call and suggests (1) fact
// fixes (wrong, imprecise, unclear, or contradicting another of your notes) and (2) links to the
// note or heading that explains an idea a note relies on. Nothing is changed until you apply a
// suggestion; dismissed ones are remembered and not suggested again. An applied change is not your
// own writing, so it never counts as new words (writeNoteNotMine). Applied fact fixes make the
// note's question bank a candidate for Rebuild banks (classbanks.go).
//
// State: .revision/checks/<subject>.json (committed with the notes).

const (
	checkDir      = "checks"
	checkTimeout  = 12 * time.Minute
	checkMaxChars = 200000 // notes per call; a bigger class is checked in several calls
	checkMaxLinks = 3      // link suggestions kept per note
)

// NoteFinding is one suggestion.
type NoteFinding struct {
	ID       string `json:"id"`
	Note     string `json:"note"`
	Kind     string `json:"kind"`               // fact or link
	Severity string `json:"severity,omitempty"` // fact: wrong, imprecise, unclear, contradiction
	Find     string `json:"find"`               // exact text in the note
	Replace  string `json:"replace"`            // what it becomes
	Why      string `json:"why"`
	Other    string `json:"other,omitempty"`   // contradiction: the other note; link: the target note
	Heading  string `json:"heading,omitempty"` // link: the target heading
	Status   string `json:"status"`            // open, applied, dismissed
	At       string `json:"at,omitempty"`      // when applied or dismissed
	Stale    bool   `json:"stale,omitempty"`   // (not stored) the text is no longer in the note once
	Line     int    `json:"line,omitempty"`    // (not stored) the line of the text now, for the viewer to show it
}

// NoteCheck is .revision/checks/<subject>.json.
type NoteCheck struct {
	Subject  string            `json:"subject"`
	Checked  string            `json:"checked,omitempty"`
	Notes    map[string]string `json:"notes,omitempty"` // note → hash of its text when checked
	Findings []NoteFinding     `json:"findings"`
}

func (s *Store) checkPath(subject string) string { return s.revPath(checkDir, subject+".json") }

func (s *Store) LoadCheck(subject string) NoteCheck {
	c := NoteCheck{Subject: subject, Findings: []NoteFinding{}}
	if b, err := os.ReadFile(s.checkPath(subject)); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	if c.Findings == nil {
		c.Findings = []NoteFinding{}
	}
	return c
}

func (s *Store) saveCheck(c NoteCheck) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.checkPath(c.Subject)), 0o755); err != nil {
		return err
	}
	return writeAtomic(s.checkPath(c.Subject), append(b, '\n'))
}

// factFixes is, per note, when fact fixes were applied to it.
func (s *Store) factFixes(subject string) map[string][]string {
	out := map[string][]string{}
	for _, f := range s.LoadCheck(subject).Findings {
		if f.Kind == "fact" && f.Status == "applied" {
			out[f.Note] = append(out[f.Note], f.At)
		}
	}
	return out
}

func textHash(b []byte) string {
	h := sha1.Sum(b)
	return hex.EncodeToString(h[:6])
}

func findingKey(f NoteFinding) string {
	return f.Note + "\x00" + f.Kind + "\x00" + strings.Join(strings.Fields(strings.ToLower(f.Find)), " ")
}

func (s *Store) checkPrompt(subject string, notes []string, texts map[string]string, dismissed []NoteFinding) string {
	var b strings.Builder
	fmt.Fprintf(&b, `You check a student's course notes (course folder %q). The notes are below, each with its id and its headings.

Find two kinds of things and answer with ONE JSON array, nothing else (no code fence, no commentary):

1. FACT problems: a statement that is wrong, imprecise enough to mislead, unclear, or that contradicts another of
   these notes. Be conservative: the notes follow the course's own textbook and lecturer, whose definitions and
   conventions win over other usage; only flag what you are confident about. Ignore style, spelling, formatting
   and missing material.
   {"kind":"fact","note":"<id>","severity":"wrong|imprecise|unclear|contradiction","find":"<exact text>",
    "replace":"<the corrected text>","why":"<one or two sentences>","other":"<id of the contradicting note, contradiction only>"}

2. LINKS: a place where a note relies on an idea another of these notes explains (not the same note). Link the
   first mention only, at most %d per note, only when following the link would really help.
   {"kind":"link","note":"<id>","find":"<exact phrase to turn into the link>","target":"<id of the other note>",
    "heading":"<one of the target's headings, exactly as listed, or empty for the whole note>","why":"<short>"}

"find" must be copied EXACTLY from the note (same characters, macrons, punctuation) and occur only once in it:
a short phrase or one sentence, never across lines, never inside an existing link. "replace" changes as little as
possible and keeps the student's wording and markdown. An empty array is a fine answer.
`, subject, checkMaxLinks)
	if len(dismissed) > 0 {
		b.WriteString("\nThe student already rejected these; do not suggest them again:\n")
		for _, d := range dismissed {
			fmt.Fprintf(&b, "- %s in %s: %q\n", d.Kind, d.Note, d.Find)
		}
	}
	for _, id := range notes {
		_, heads := graphSections(texts[id])
		var hs []string
		for _, h := range heads {
			hs = append(hs, h.title)
		}
		fmt.Fprintf(&b, "\n=== NOTE %s ===\nHeadings: %s\n%s\n", id, strings.Join(hs, " | "), strings.TrimSpace(texts[id]))
	}
	return b.String()
}

var wikiOrMdLink = regexp.MustCompile(`\[\[[^\]]*\]\]|\[[^\]]*\]\([^)]*\)`)

// insideLink reports whether the text at [i, j) overlaps an existing link.
func insideLink(text string, i, j int) bool {
	for _, m := range wikiOrMdLink.FindAllStringIndex(text, -1) {
		if i < m[1] && j > m[0] {
			return true
		}
	}
	return false
}

// linkTarget is how a link to a note is written: its file name, or its path when the name is not
// unique among all notes.
func (s *Store) linkTarget(id string) string {
	stem := strings.TrimSuffix(filepath.Base(id), ".md")
	n := 0
	for _, f := range s.Files() {
		if strings.EqualFold(strings.TrimSuffix(filepath.Base(f), filepath.Ext(f)), stem) {
			n++
		}
	}
	if n > 1 {
		return strings.TrimSuffix(id, ".md")
	}
	return stem
}

// parseFindings reads Claude's JSON and keeps only suggestions that can be applied as given.
func (s *Store) parseFindings(out string, texts map[string]string) []NoteFinding {
	i, j := strings.Index(out, "["), strings.LastIndex(out, "]")
	if i < 0 || j < i {
		return nil
	}
	var raw []struct {
		Kind, Note, Severity, Find, Replace, Why, Other, Target, Heading string
	}
	if json.Unmarshal([]byte(out[i:j+1]), &raw) != nil {
		return nil
	}
	var res []NoteFinding
	links := map[string]int{}
	for _, r := range raw {
		text, ok := texts[r.Note]
		if !ok || r.Find == "" || strings.Contains(r.Find, "\n") || strings.Count(text, r.Find) != 1 {
			continue
		}
		at := strings.Index(text, r.Find)
		f := NoteFinding{Note: r.Note, Kind: r.Kind, Find: r.Find, Why: strings.TrimSpace(r.Why), Status: "open"}
		switch r.Kind {
		case "fact":
			switch r.Severity {
			case "wrong", "imprecise", "unclear", "contradiction":
			default:
				r.Severity = "unclear"
			}
			if r.Replace == "" || r.Replace == r.Find {
				continue
			}
			f.Severity, f.Replace = r.Severity, r.Replace
			if _, ok := texts[r.Other]; ok && r.Other != r.Note {
				f.Other = r.Other
			}
		case "link":
			tt, ok := texts[r.Target]
			if !ok || r.Target == r.Note || insideLink(text, at, at+len(r.Find)) || links[r.Note] >= checkMaxLinks || strings.ContainsAny(r.Find, "[]|") {
				continue
			}
			links[r.Note]++
			f.Other = r.Target
			_, heads := graphSections(tt)
			for _, h := range heads {
				if strings.EqualFold(h.title, strings.TrimSpace(r.Heading)) {
					f.Heading = h.title
				}
			}
			dest := s.linkTarget(r.Target)
			if f.Heading != "" {
				dest += "#" + f.Heading
			}
			// a link's label is shown as plain text: emphasis around the phrase goes outside the link
			inner := strings.TrimLeft(r.Find, "*_")
			lead := r.Find[:len(r.Find)-len(inner)]
			inner = strings.TrimSuffix(inner, lead)
			if len(lead)+len(inner)+len(lead) != len(r.Find) || strings.TrimSpace(inner) == "" {
				lead, inner = "", r.Find
			}
			f.Replace = lead + "[[" + dest + "|" + inner + "]]" + lead
		default:
			continue
		}
		f.ID = textHash([]byte(findingKey(f) + "\x00" + f.Replace))
		res = append(res, f)
	}
	return res
}

// CheckNotes asks Claude about the given notes of a class (all of them when ids is empty) and
// replaces the open suggestions for those notes with the new ones.
func (s *Store) CheckNotes(subject string, ids []string, now time.Time) (int, error) {
	all := s.graphNotes(subject)
	if len(ids) == 0 {
		ids = all
	}
	texts := map[string]string{}
	for _, id := range all { // every note of the class can be linked to or contradict
		if b, err := os.ReadFile(filepath.Join(s.Root, id)); err == nil {
			texts[id] = string(b)
		}
	}
	prev := s.LoadCheck(subject)
	var dismissed []NoteFinding
	gone := map[string]bool{}
	for _, f := range prev.Findings {
		if f.Status == "dismissed" {
			dismissed = append(dismissed, f)
			gone[findingKey(f)] = true
		}
	}
	var found []NoteFinding
	var errs []string
	for len(ids) > 0 {
		n, chars := 0, 0
		for n < len(ids) && (n == 0 || chars+len(texts[ids[n]]) <= checkMaxChars) {
			chars += len(texts[ids[n]])
			n++
		}
		chunk := ids[:n]
		ids = ids[n:]
		out, err := runClaude(s.checkPrompt(subject, chunk, texts, dismissed), checkTimeout)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		sub := map[string]string{}
		for _, id := range chunk {
			sub[id] = texts[id]
		}
		for _, f := range s.parseFindings(out, texts) {
			if _, mine := sub[f.Note]; mine && !gone[findingKey(f)] {
				found = append(found, f)
			}
		}
		for _, id := range chunk {
			if prev.Notes == nil {
				prev.Notes = map[string]string{}
			}
			prev.Notes[id] = textHash([]byte(texts[id]))
		}
		checked := map[string]bool{}
		for _, id := range chunk {
			checked[id] = true
		}
		var keep []NoteFinding
		for _, f := range prev.Findings {
			if f.Status != "open" || !checked[f.Note] {
				keep = append(keep, f)
			}
		}
		prev.Findings = keep
	}
	if len(errs) > 0 && found == nil {
		return 0, fmt.Errorf("%s", errs[0])
	}
	prev.Subject, prev.Checked = subject, now.Format(scoreStamp)
	prev.Findings = append(prev.Findings, found...)
	if prev.Findings == nil {
		prev.Findings = []NoteFinding{}
	}
	return len(found), s.saveCheck(prev)
}

// CheckView is the check for the class page: stale flags, and how many notes changed since.
func (s *Store) CheckView(subject string) (NoteCheck, int) {
	c := s.LoadCheck(subject)
	texts := map[string]string{}
	changed := 0
	for _, id := range s.graphNotes(subject) {
		b, _ := os.ReadFile(filepath.Join(s.Root, id))
		texts[id] = string(b)
		if h, ok := c.Notes[id]; c.Checked != "" && (!ok || h != textHash(b)) {
			changed++
		}
	}
	for i := range c.Findings {
		f := &c.Findings[i]
		f.Stale = f.Status == "open" && strings.Count(texts[f.Note], f.Find) != 1
		if f.Status == "open" && !f.Stale {
			t := texts[f.Note]
			f.Line = 1 + strings.Count(t[:strings.Index(t, f.Find)], "\n")
		}
	}
	sort.SliceStable(c.Findings, func(i, j int) bool { return c.Findings[i].Note < c.Findings[j].Note })
	return c, changed
}

// ResolveFindings applies (replace may override the suggested text, one finding only) or dismisses findings.
func (s *Store) ResolveFindings(subject string, ids []string, apply bool, replace string, now time.Time) (int, error) {
	c := s.LoadCheck(subject)
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	n := 0
	var errs []string
	for i := range c.Findings {
		f := &c.Findings[i]
		if !want[f.ID] || f.Status != "open" {
			continue
		}
		if apply {
			r := f.Replace
			if replace != "" && len(ids) == 1 {
				r = replace
			}
			if err := s.replaceInNote(f.Note, f.Find, r); err != nil {
				errs = append(errs, err.Error())
				continue
			}
			f.Replace = r
			f.Status = "applied"
		} else {
			f.Status = "dismissed"
		}
		f.At = now.Format(scoreStamp)
		n++
	}
	if n > 0 {
		if err := s.saveCheck(c); err != nil {
			return n, err
		}
	}
	if len(errs) > 0 {
		return n, fmt.Errorf("%w: %s", ErrRevision, errs[0])
	}
	return n, nil
}

func (s *Store) replaceInNote(rel, find, repl string) error {
	b, err := os.ReadFile(filepath.Join(s.Root, rel))
	if err != nil {
		return err
	}
	if strings.Count(string(b), find) != 1 {
		return fmt.Errorf("%s changed: the text is no longer there once", rel)
	}
	return s.writeNoteNotMine(rel, b, []byte(strings.Replace(string(b), find, repl, 1)))
}

// writeNoteNotMine writes a change to a note that is not the student's own writing: the new-words
// counter's stored count for it moves by the same amount, so it is never counted as new words (and
// never schedules a revision). Words you typed and the counter has not seen yet still count.
func (s *Store) writeNoteNotMine(rel string, before, after []byte) error {
	wordsMu.Lock()
	defer wordsMu.Unlock()
	if err := writeAtomic(filepath.Join(s.Root, rel), after); err != nil {
		return err
	}
	var st wordsSnapshot
	s.readWordsJSON(wordsState, &st)
	if n, ok := st.Files[rel]; ok {
		st.Files[rel] = n + countWords(string(after)) - countWords(string(before))
		return s.writeWordsJSON(wordsState, st)
	}
	return nil
}
