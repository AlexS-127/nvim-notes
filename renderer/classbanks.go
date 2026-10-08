package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Rebuild banks (class page): which revision question banks of a course most need rewriting, and
// rewriting the ones you pick. Need is worked out from what is already on disk (no Claude call):
// written before the current guidance, the note changed since, weak question types, worn out (you
// know every question, so you may be remembering the questions rather than the material), too few
// questions, or a fact in the note corrected by Check notes since. You choose what to rebuild:
//   - repair (default): questions that are not flagged are kept word for word, so their stats
//     (.quiz_stats.json is keyed by question text) and the class graph's colours survive; Claude
//     replaces the flagged ones, may drop kept ones the note no longer supports, and covers what is new.
//   - full: a fresh bank; the old questions' history no longer applies.
// Several topics go to Claude in one call (up to rebuildChunk topics / rebuildChunkChars of notes).

const (
	rebuildChunk      = 8
	rebuildChunkChars = 90000
	rebuildTimeout    = 12 * time.Minute
	bankGoodMin       = 5 // fewer usable questions than this is a reason to rebuild
	bankWornStreak    = 3 // right this many times in a row = known well
)

// BankReason is one reason a bank needs rebuilding; W is how much it counts.
type BankReason struct {
	Code string  `json:"code"`
	Text string  `json:"text"`
	W    float64 `json:"w"`
}

// BankRow is one topic of a class on the Rebuild banks list.
type BankRow struct {
	ID        string       `json:"id"`
	Title     string       `json:"title"`
	Questions int          `json:"questions"` // usable questions in the bank
	Seen      int          `json:"seen"`      // of them answered at least once
	Known     int          `json:"known"`     // of them known well (streak ≥ bankWornStreak)
	Weak      []string     `json:"weak"`      // flagged questions (first line), replaced by a repair
	Gen       RevisionGen  `json:"gen"`
	Edited    bool         `json:"edited"` // the bank was changed by hand after it was written
	Need      float64      `json:"need"`
	Reasons   []BankReason `json:"reasons"`
}

type bankBlock struct {
	key  string // the question text quiz.py keys its stats by
	raw  string // the block as written
	weak string // why it is flagged, or ""
}

var (
	blankRe  = regexp.MustCompile(`(?i)fill in the blank|_{3,}`)
	tfAnswer = regexp.MustCompile(`(?im)^A:\s*(true|false)\s*$`)
)

// bankBlocks splits a bank into its question blocks (heading lines dropped) and flags the weak ones:
// negative stems and all/none of the above (quiz.py skips those), fill-in-the-blank, repeats, and
// true/false beyond the first two.
func bankBlocks(src string) []bankBlock {
	var out []bankBlock
	var cur []string
	flush := func() {
		if len(cur) == 0 {
			return
		}
		raw := strings.TrimSpace(strings.Join(cur, "\n"))
		if qs := graphBank(raw); len(qs) == 1 && countQuestionBlocks(raw) == 1 {
			out = append(out, bankBlock{key: qs[0].key, raw: raw})
		}
		cur = nil
	}
	for _, l := range strings.Split(src, "\n") {
		switch {
		case strings.HasPrefix(l, "#"):
			flush()
		case strings.HasPrefix(l, "Q:"):
			flush()
			cur = []string{l}
		case cur != nil:
			cur = append(cur, l)
		}
	}
	flush()
	seen, tf := map[string]bool{}, 0
	for i := range out {
		b := &out[i]
		stem, choices := b.key, false
		for _, l := range strings.Split(b.raw, "\n") {
			if c := graphChoice.FindStringSubmatch(strings.TrimSpace(l)); c != nil {
				choices = true
				if noneOfRe.MatchString(c[1]) {
					b.weak = "all/none of the above"
				}
			}
		}
		switch {
		case b.weak != "":
		case choices && negativeStemRe.MatchString(stem):
			b.weak = "negative stem (NOT/EXCEPT)"
		case blankRe.MatchString(stem):
			b.weak = "fill-in-the-blank copied from the note"
		case seen[strings.ToLower(stem)]:
			b.weak = "repeats another question"
		case !choices && tfAnswer.MatchString(b.raw):
			if tf++; tf > 2 {
				b.weak = "too many true/false"
			}
		}
		seen[strings.ToLower(stem)] = true
	}
	return out
}

func (s *Store) subjectStats(subject string) map[string]quizStat {
	out := map[string]quizStat{}
	b, err := os.ReadFile(filepath.Join(s.Root, subject, ".quiz_stats.json"))
	if err != nil {
		return out
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(b, &raw) != nil {
		return out
	}
	for k, v := range raw {
		var q quizStat
		if k != "__player__" && json.Unmarshal(v, &q) == nil {
			out[k] = q
		}
	}
	return out
}

// guidanceChanged is when the question guidance this subject's banks are written with last changed.
func (s *Store) guidanceChanged(subject string) time.Time {
	var t time.Time
	for _, n := range []string{"revision-questions.md", "revision-" + subject + ".md", "question-format.md"} {
		if st, err := os.Stat(filepath.Join(s.Root, "format", n)); err == nil && st.ModTime().After(t) {
			t = st.ModTime()
		}
	}
	return t
}

// BankHealth lists a class's scheduled topics, the ones most in need of a rebuild first.
func (s *Store) BankHealth(subject string, now time.Time) []BankRow {
	stats := s.subjectStats(subject)
	guide := s.guidanceChanged(subject)
	fixed := s.factFixes(subject)
	rows := []BankRow{}
	for _, t := range s.Topics() {
		if t.Subject != subject {
			continue
		}
		r := BankRow{ID: t.ID, Title: t.Title, Gen: t.Gen, Weak: []string{}, Reasons: []BankReason{}}
		add := func(code, text string, w float64) {
			r.Reasons = append(r.Reasons, BankReason{code, text, math.Round(w*10) / 10})
			r.Need += w
		}
		gen, _ := time.ParseInLocation(scoreStamp, t.Gen.At, now.Location())
		bst, err := os.Stat(s.QuestionsPath(t.ID))
		if err != nil {
			if t.Gen.Status != "rebuilding" {
				add("none", "no questions yet", 3)
			}
			rows = append(rows, r)
			continue
		}
		r.Edited = t.Gen.At != "" && bankEdited(bst.ModTime(), t.Gen.At)
		src, _ := os.ReadFile(s.QuestionsPath(t.ID))
		blocks := bankBlocks(string(src))
		for _, b := range blocks {
			if b.weak != "" {
				r.Weak = append(r.Weak, firstLine(b.key)+" — "+b.weak)
				continue
			}
			r.Questions++
			if q, ok := stats[b.key]; ok && q.Right+q.Wrong > 0 {
				r.Seen++
				if q.Streak >= bankWornStreak {
					r.Known++
				}
			}
		}
		if t.Gen.At == "" {
			gen = bst.ModTime()
		}
		if guide.After(gen) {
			add("guidance", "written before the current question guidance ("+guide.Format("Jan 2")+")", 1.5)
		}
		if words := s.TopicWords(t.ID); t.Gen.Words > 0 && words != t.Gen.Words {
			ch := float64(words-t.Gen.Words) / float64(t.Gen.Words)
			switch {
			case ch >= 0.1:
				add("note", fmt.Sprintf("note grew %d%% since", int(math.Round(ch*100))), math.Min(2, 0.5+2*ch))
			case ch <= -0.1:
				add("note", fmt.Sprintf("note shrank %d%% since", int(math.Round(-ch*100))), math.Min(2, 0.5-2*ch))
			default:
				add("note", "note edited since", 0.3)
			}
		}
		if n := len(r.Weak); n > 0 {
			add("weak", fmt.Sprintf("%d weak question%s", n, plural(n)), math.Min(3, 0.7*float64(n)))
		}
		if r.Questions >= 3 && 3*r.Known >= 2*r.Questions { // two thirds known well
			add("worn", fmt.Sprintf("worn out: you know %d of %d well, so you may be remembering the questions", r.Known, r.Questions), 1)
		}
		if r.Questions < bankGoodMin {
			add("few", fmt.Sprintf("only %d good question%s", r.Questions, plural(r.Questions)), 1)
		}
		if n := countAfter(fixed[t.ID], gen); n > 0 {
			add("facts", fmt.Sprintf("%d fact%s corrected in the note since (Check notes)", n, plural(n)), 2)
		}
		r.Need = math.Round(r.Need*10) / 10
		rows = append(rows, r)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Need != rows[j].Need {
			return rows[i].Need > rows[j].Need
		}
		return rows[i].Title < rows[j].Title
	})
	return rows
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func countAfter(stamps []string, t time.Time) int {
	n := 0
	for _, a := range stamps {
		if at, err := time.ParseInLocation(scoreStamp, a, t.Location()); err == nil && at.After(t) {
			n++
		}
	}
	return n
}

// runClaude runs `claude -p` with no tools (it sees only the prompt) and returns its output.
func runClaude(prompt string, timeout time.Duration) (string, error) {
	bin := claudeBinary()
	if bin == "" {
		return "", fmt.Errorf("claude not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-p", "--tools", "")
	cmd.Stdin = strings.NewReader(prompt)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("claude: %s", firstLine(msg))
	}
	return out.String(), nil
}

// rebuildPlan is one topic of a rebuild: what is kept, what is replaced, how many new questions.
type rebuildPlan struct {
	t        Topic
	note     string
	keep     []bankBlock
	replace  []bankBlock // flagged: weak, or known well in a worn-out bank
	min, max int
}

func (s *Store) planRebuild(t Topic, mode string, stats map[string]quizStat, row BankRow) rebuildPlan {
	b, _ := os.ReadFile(filepath.Join(s.Root, t.ID))
	p := rebuildPlan{t: t, note: string(b)}
	worn := false
	grew := false
	for _, r := range row.Reasons {
		worn = worn || r.Code == "worn"
		grew = grew || (r.Code == "note" && r.W > 0.3)
	}
	if mode == "repair" {
		src, _ := os.ReadFile(s.QuestionsPath(t.ID))
		for _, bl := range bankBlocks(string(src)) {
			if bl.weak == "" && worn && stats[bl.key].Streak >= bankWornStreak {
				bl.weak = "known well: write a fresh question on the same idea from a different angle or with a new example"
			}
			if bl.weak != "" {
				p.replace = append(p.replace, bl)
			} else {
				p.keep = append(p.keep, bl)
			}
		}
	}
	k := len(p.keep)
	p.min = max(len(p.replace), bankGoodMin-k, 1)
	p.max = max(p.min+1, 8-k)
	if grew {
		p.min, p.max = max(p.min, 2), p.max+2
	}
	return p
}

func (s *Store) rebuildPrompt(subject, mode string, plans []rebuildPlan) string {
	spec, guide := s.genGuidance(subject)
	var b strings.Builder
	fmt.Fprintf(&b, `You maintain the revision question banks of a student's spaced-repetition quiz (course folder %q).

Below are (1) the quiz's question format, (2) the student's guidance on what makes a good question and
(3) several topics, each one of the student's notes`, subject)
	if mode == "repair" {
		b.WriteString(` with its current questions. For each topic:
  - KEPT questions stay unless you drop them. Drop one only if the note no longer supports it, its answer
    key is wrong, or it clearly breaks the guidance. Write a replacement for each one you drop.
  - REPLACED questions are already removed: write new questions that test the same ideas better.
  - Then write new questions so the topic gets the number asked for, covering the note's key ideas that
    no kept question tests yet. Never repeat a kept question.`)
	} else {
		b.WriteString(`. Write a fresh set of questions for each topic, covering the note's key ideas.`)
	}
	b.WriteString(`
Every question must be answerable from its own note. Put "[gen]" at the end of each Q: line, "Src: <topic id>"
on each block and a short Why: on each.

Output, for each topic in order: a line "@@@ <topic id>"`)
	if mode == "repair" {
		b.WriteString(`, then a line "DROP: <numbers of kept questions to drop, comma separated, or none>"`)
	}
	b.WriteString(`, then the new question blocks. Plain text only: no headings, no code fences, no commentary.
Ignore the format file's instructions about writing files, headings, reporting or code blocks.

=== QUESTION FORMAT ===
`)
	b.WriteString(spec + "\n\n=== HOW TO WRITE GOOD QUESTIONS ===\n" + guide + "\n")
	for _, p := range plans {
		fmt.Fprintf(&b, "\n\n=== TOPIC %s (%s) ===\nWrite between %d and %d new questions.\n", p.t.ID, p.t.Title, p.min, p.max)
		if mode == "repair" {
			if len(p.replace) > 0 {
				b.WriteString("\nREPLACED (removed; test these ideas better):\n")
				for _, r := range p.replace {
					fmt.Fprintf(&b, "- [%s]\n%s\n", r.weak, r.raw)
				}
			}
			if len(p.keep) > 0 {
				b.WriteString("\nKEPT:\n")
				for i, k := range p.keep {
					fmt.Fprintf(&b, "%d.\n%s\n", i+1, k.raw)
				}
			}
		}
		fmt.Fprintf(&b, "\nNOTE:\n%s\n", strings.TrimSpace(p.note))
	}
	return b.String()
}

var (
	rebuildSecRe = regexp.MustCompile(`(?m)^@@@[ \t]*(\S+)[ \t]*$`)
	dropRe       = regexp.MustCompile(`(?mi)^DROP:[ \t]*(.*)$`)
	numRe        = regexp.MustCompile(`\d+`)
)

// splitRebuild cuts Claude's answer into topic id → its section.
func splitRebuild(out string) map[string]string {
	res := map[string]string{}
	idx := rebuildSecRe.FindAllStringSubmatchIndex(out, -1)
	for i, m := range idx {
		end := len(out)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		res[out[m[2]:m[3]]] = out[m[1]:end]
	}
	return res
}

// assembleRebuild is the new bank of one topic: kept questions word for word (minus any Claude
// dropped), then the new ones.
func assembleRebuild(p rebuildPlan, section string) (string, int) {
	drop := map[int]bool{}
	head := section
	if i := qLineRe.FindStringIndex(section); i != nil {
		head = section[:i[0]]
	}
	if m := dropRe.FindStringSubmatch(head); m != nil {
		for _, n := range numRe.FindAllString(m[1], -1) {
			v, _ := strconv.Atoi(n)
			drop[v] = true
		}
	}
	var keep []string
	for i, k := range p.keep {
		if !drop[i+1] {
			keep = append(keep, k.raw)
		}
	}
	fresh := ""
	if qLineRe.MatchString(section) {
		fresh = strings.TrimPrefix(cleanGenerated(dropRe.ReplaceAllString(section, ""), p.t.Title), "## "+p.t.Title+"\n")
	}
	text := "## " + p.t.Title + "\n\n" + strings.Join(keep, "\n\n") + "\n\n" + strings.TrimSpace(fresh) + "\n"
	text = dropWeakQuestions(text)
	return text, len(keep)
}

// RebuildBanks rewrites the banks of the given topics of one class (mode repair or full) and
// reports a result per topic (nil = rewritten). progress is told how far it got.
func (s *Store) RebuildBanks(subject string, ids []string, mode string, progress func(done int)) map[string]error {
	now := time.Now()
	res := map[string]error{}
	byID := map[string]Topic{}
	for _, t := range s.Topics() {
		byID[t.ID] = t
	}
	rows := map[string]BankRow{}
	for _, r := range s.BankHealth(subject, now) {
		rows[r.ID] = r
	}
	stats := s.subjectStats(subject)
	var plans []rebuildPlan
	for _, id := range ids {
		t, ok := byID[id]
		if !ok || t.Subject != subject {
			res[id] = fmt.Errorf("%w: %s is not a scheduled topic of %s", ErrRevision, id, subject)
			continue
		}
		plans = append(plans, s.planRebuild(t, mode, stats, rows[id]))
	}
	prev := map[string]RevisionGen{}
	for _, p := range plans {
		prev[p.t.ID] = p.t.Gen
		g := p.t.Gen
		g.Status = "rebuilding"
		_ = s.setGen(p.t.ID, g)
	}
	done := 0
	for len(plans) > 0 {
		n, chars := 0, 0
		for n < len(plans) && n < rebuildChunk && (n == 0 || chars+len(plans[n].note) <= rebuildChunkChars) {
			chars += len(plans[n].note)
			n++
		}
		chunk := plans[:n]
		plans = plans[n:]
		out, err := runClaude(s.rebuildPrompt(subject, mode, chunk), rebuildTimeout)
		secs := splitRebuild(out)
		for _, p := range chunk {
			id := p.t.ID
			sec, ok := secs[id]
			switch {
			case err != nil:
				res[id] = err
			case !ok:
				res[id] = fmt.Errorf("claude wrote nothing for this topic")
			default:
				text, kept := assembleRebuild(p, sec)
				if c := countQuestionBlocks(text); c < revMinQuestions {
					res[id] = fmt.Errorf("only %d usable questions (need %d)", c, revMinQuestions)
				} else if werr := writeAtomic(s.QuestionsPath(id), []byte(text)); werr != nil {
					res[id] = werr
				} else {
					res[id] = s.setGen(id, RevisionGen{Status: "ok", At: time.Now().Format(scoreStamp), Words: s.TopicWords(id), Count: c, Mode: mode, Kept: kept})
				}
			}
			if res[id] != nil {
				g := prev[id]
				if g.Status == "rebuilding" || g.Status == "" {
					g.Status = "ok"
				}
				_ = s.setGen(id, g) // the old bank is untouched
			}
			done++
		}
		if progress != nil {
			progress(done)
		}
	}
	return res
}

// resetRebuilding puts back topics left "rebuilding" by a server that stopped mid-rebuild.
func (s *Store) resetRebuilding() {
	revMu.Lock()
	defer revMu.Unlock()
	t := s.loadTopics()
	changed := false
	for i := range t.Topics {
		if t.Topics[i].Gen.Status == "rebuilding" {
			t.Topics[i].Gen.Status = "none"
			if _, err := os.Stat(s.QuestionsPath(t.Topics[i].ID)); err == nil {
				t.Topics[i].Gen.Status = "ok"
			}
			changed = true
		}
	}
	if changed {
		_ = s.saveTopics(t)
	}
}
