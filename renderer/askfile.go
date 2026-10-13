package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Question inbox: `<subject>/ask.md` is where you drop questions about a class, in any wording. `notesview
// ask <subject>` has Claude answer each one from the class's notes and sources, put the answer in the
// quiz format under a heading that names its topic (and module, when the bank uses them), file it in
// `<subject>/questions.md` and write the answer under the question in ask.md as a `> ` quote. Answered
// questions stay in ask.md (a quote under one marks it answered); questions Claude could not file stay
// unanswered and are tried again next run.

const (
	askFile      = "ask.md"
	askBatch     = 8
	askTimeout   = 5 * time.Minute
	askNoteChars = 40000  // per file of context
	askCtxChars  = 150000 // all context
)

var (
	askMarkRe    = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s+`)
	askCommentRe = regexp.MustCompile(`(?s)<!--.*?-->`)
	askSegRe     = regexp.MustCompile(`^@@\s*(\d+)\s*\|\s*(.+?)\s*$`)
)

const askTemplate = `# Questions

<!-- Write questions about this class here, one per paragraph or list item, in any wording.
Then run ` + "`notesview ask <class>`" + ` (or :Ask in Neovim). Claude answers each from your notes and
sources, files it in questions.md under a topic heading and writes the answer under the question here
(a "> " quote). Answered questions stay; delete the quote to have one answered again. -->
`

// askItem is one question in ask.md and, when it has been answered, the quote lines under it (without
// their `> `).
type askItem struct {
	Q   string
	Ans []string
}

func (it askItem) answered() bool { return len(it.Ans) > 0 }

func askQuoteLine(l string) (string, bool) {
	t := strings.TrimSpace(l)
	if !strings.HasPrefix(t, ">") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(t[1:], " ")), true
}

// askItems splits the questions out of an ask.md: paragraphs separated by blank lines, or one per list
// item. A `> ` quote right under a question is its answer. Headings and comments are not questions.
// preamble is what is kept on rewrite (the title and the instructions comment).
func askItems(text string) (preamble string, items []askItem) {
	var pre []string
	for _, c := range askCommentRe.FindAllString(text, -1) {
		pre = append(pre, c)
	}
	text = askCommentRe.ReplaceAllString(text, "")
	var title string
	var cur []string
	var ans []string
	flush := func() {
		if len(cur) == 0 {
			return
		}
		items = append(items, askItem{strings.TrimSpace(strings.Join(cur, "\n")), ans})
		cur, ans = nil, nil
	}
	var para []string
	endPara := func() {
		if len(para) == 0 {
			return
		}
		if askMarkRe.MatchString(para[0]) {
			for _, l := range para {
				if q, ok := askQuoteLine(l); ok {
					ans = append(ans, q)
				} else if askMarkRe.MatchString(l) {
					flush()
					cur = []string{askMarkRe.ReplaceAllString(l, "")}
				} else if len(cur) > 0 {
					cur = append(cur, strings.TrimSpace(l))
				}
			}
			flush()
		} else {
			for _, l := range para {
				if q, ok := askQuoteLine(l); ok {
					ans = append(ans, q)
				} else {
					cur = append(cur, l)
				}
			}
			flush()
		}
		para = nil
	}
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimRight(l, " \t\r")
		switch {
		case strings.HasPrefix(l, "#"):
			endPara()
			if title == "" {
				title = l
			}
		case strings.TrimSpace(l) == "" || strings.TrimSpace(l) == "---":
			endPara()
		default:
			para = append(para, l)
		}
	}
	endPara()
	if title == "" {
		title = "# Questions"
	}
	preamble = title + "\n"
	if len(pre) > 0 {
		preamble += "\n" + strings.Join(pre, "\n\n") + "\n"
	}
	return preamble, items
}

var askSectionRe = regexp.MustCompile(`^(Q|A|Why|Src|Solution|Rubric):\s*(.*)$`)
var askChoiceRe = regexp.MustCompile(`^\s*([a-z])\)\s*(.*)$`)

// askAnswerLines turns a quiz block into the answer written under the question in ask.md: the correct
// choice or short answer, the solution or rubric, and Why. Src is left out.
func askAnswerLines(block, heading string) []string {
	sec := ""
	body := map[string][]string{}
	choices := map[string]string{}
	for _, l := range strings.Split(block, "\n") {
		if m := askSectionRe.FindStringSubmatch(l); m != nil {
			sec = m[1]
			if m[2] != "" {
				body[sec] = append(body[sec], m[2])
			}
			continue
		}
		if sec == "Q" {
			if m := askChoiceRe.FindStringSubmatch(l); m != nil {
				choices[m[1]] = strings.TrimSpace(m[2])
			}
			continue
		}
		if sec != "" {
			body[sec] = append(body[sec], strings.TrimSpace(l))
		}
	}
	var out []string
	if a := strings.TrimSpace(strings.Join(body["A"], " ")); a != "" {
		if c, ok := choices[strings.ToLower(strings.TrimSuffix(a, ")"))]; ok {
			a = strings.ToLower(strings.TrimSuffix(a, ")")) + ") " + c
		}
		out = append(out, a)
	}
	for _, k := range []string{"Rubric", "Solution"} {
		if ls := body[k]; len(strings.TrimSpace(strings.Join(ls, ""))) > 0 {
			if k == "Rubric" {
				out = append(out, "Points to make:")
			}
			out = append(out, ls...)
		}
	}
	if w := strings.TrimSpace(strings.Join(body["Why"], " ")); w != "" {
		out = append(out, "Why: "+w)
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	if len(out) > 0 {
		out[0] = "**Answer:** " + out[0]
	} else {
		out = []string{"**Answer:** see questions.md"}
	}
	return append(out, "*Filed in questions.md under "+heading+"*")
}

// bankHeadings lists the `## ` headings of a question bank, in order.
func bankHeadings(text string) []string {
	var hs []string
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, "## ") {
			hs = append(hs, strings.TrimSpace(l[3:]))
		}
	}
	return hs
}

// askSegment is one answered question: the number it had in ask.md, the heading to file it under, and
// its question block.
type askSegment struct {
	n       int
	heading string
	block   string
}

// parseAskOutput reads Claude's answer: `@@ <n> | <heading>` then the question block, per question.
// Segments without a usable block, with an unknown number, or repeated are dropped.
func parseAskOutput(out string, count int) []askSegment {
	out = genFenceRe.ReplaceAllString(out, "")
	var segs []askSegment
	var cur *askSegment
	var lines []string
	end := func() {
		if cur == nil {
			return
		}
		cur.block = strings.TrimSpace(strings.Join(lines, "\n"))
		if cur.n >= 1 && cur.n <= count && cur.heading != "" && countQuestionBlocks(cur.block) == 1 {
			segs = append(segs, *cur)
		}
		cur, lines = nil, nil
	}
	seen := map[int]bool{}
	for _, l := range strings.Split(out, "\n") {
		if m := askSegRe.FindStringSubmatch(l); m != nil {
			end()
			n, _ := strconv.Atoi(m[1])
			if seen[n] {
				continue
			}
			seen[n] = true
			cur = &askSegment{n: n, heading: strings.TrimSpace(strings.TrimLeft(m[2], "# "))}
			continue
		}
		if cur != nil && !strings.HasPrefix(l, "#") {
			lines = append(lines, l)
		}
	}
	end()
	return segs
}

// mergeIntoBank adds each block to the end of the section with its heading (matched ignoring case), or
// under a new heading at the end of the file.
func mergeIntoBank(bank string, segs []askSegment) string {
	lines := strings.Split(strings.TrimRight(bank, "\n"), "\n")
	if bank == "" {
		lines = nil
	}
	for _, sg := range segs {
		at, start := -1, -1
		for i, l := range lines {
			if strings.HasPrefix(l, "#") {
				if start >= 0 {
					at = i
					break
				}
				if strings.HasPrefix(l, "## ") && strings.EqualFold(strings.TrimSpace(l[3:]), sg.heading) {
					start = i
				}
			}
		}
		add := append([]string{""}, strings.Split(sg.block, "\n")...)
		switch {
		case start < 0:
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, "## "+sg.heading)
			lines = append(lines, add...)
		default:
			if at < 0 {
				at = len(lines)
			}
			for at > start+1 && strings.TrimSpace(lines[at-1]) == "" {
				at--
			}
			lines = append(lines[:at], append(add, lines[at:]...)...)
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

// askContext gathers the class's notes, then its extracted sources, up to a budget.
func (s *Store) askContext(subject string) string {
	dir := filepath.Join(s.Root, subject)
	var files []string
	for _, pat := range []string{filepath.Join(dir, "*.md"), filepath.Join(dir, ".sources", "*.md")} {
		m, _ := filepath.Glob(pat)
		sort.Strings(m)
		files = append(files, m...)
	}
	var b strings.Builder
	for _, f := range files {
		switch filepath.Base(f) {
		case "questions.md", "mistakes.md", askFile:
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		t := string(data)
		if len(t) > askNoteChars {
			t = t[:askNoteChars] + "\n[…cut…]"
		}
		if b.Len()+len(t) > askCtxChars {
			break
		}
		rel, _ := filepath.Rel(s.Root, f)
		fmt.Fprintf(&b, "=== %s ===\n%s\n\n", rel, t)
	}
	if b.Len() == 0 {
		return "No notes or sources exist for this class yet: answer from well-established university-level knowledge only."
	}
	return b.String()
}

func (s *Store) askPrompt(subject string, items []string, offset int, headings []string, ctx string) string {
	spec, _ := s.genGuidance(subject)
	var qs strings.Builder
	for i, q := range items {
		fmt.Fprintf(&qs, "%d. %s\n", offset+i+1, strings.ReplaceAll(q, "\n", "\n   "))
	}
	hs := "(none yet)"
	if len(headings) > 0 {
		hs = "- " + strings.Join(headings, "\n- ")
	}
	return fmt.Sprintf(`You are answering a student's own questions about their class %q and filing each answer in their quiz bank.

For EACH numbered question below, write one quiz block in the quiz format (given first) that has the student's
question as its Q: (keep their meaning; fix typos; make it self-contained) and your answer:
- a crisp factual question: a short A:, or choices a) to d) with A: <letter>, or A: true / false;
- an explanation, comparison, "why/how" or calculation: Solution: with a full but compact answer, indented
  two spaces (use Rubric: with the points a full answer must make, plus Solution:, for an open-ended
  "argue/evaluate" question).
Add Why: only when it adds something, and "Src: ask" (append the note or source it came from when it did, e.g.
"Src: ask, Session4.md"). Base the answer on the class material below. If you needed knowledge outside it, say so in
the answer ("outside the course material:"). Never invent facts, numbers or quotes: if the material and your
knowledge do not support an answer, leave that question out entirely.

Filing: put each block under a heading that names its topic. Reuse EXACTLY one of the existing headings when the question
fits it; otherwise write a new heading in the same style as the existing ones (for example "Module 3: Learning curves" when
they carry module numbers, or just the topic). Never use a heading that names an exam format (Long case, Medium, Short).

Output format, nothing else (no commentary, no code fences): for each question you answered,
@@ <question number> | <heading>
<the Q:/A:/... block>

=== QUIZ FORMAT ===
%s

=== EXISTING HEADINGS IN questions.md ===
%s

=== QUESTIONS ===
%s
=== CLASS MATERIAL ===
%s`, subject, spec, hs, qs.String(), ctx)
}

// AskResult reports one run of `notesview ask`.
type AskResult struct {
	Subject  string   `json:"subject"`
	Answered int      `json:"answered"`
	Kept     int      `json:"kept"` // still unanswered in ask.md
	Headings []string `json:"headings"`
	Created  bool     `json:"created"` // ask.md did not exist: a template was written
	Error    string   `json:"error,omitempty"`
}

// AnswerAsks answers the questions in <subject>/ask.md and files them in questions.md.
func (s *Store) AnswerAsks(subject string) (*AskResult, error) {
	if subject == "" || strings.ContainsAny(subject, "/\\") || strings.HasPrefix(subject, ".") {
		return nil, fmt.Errorf("no such class %q", subject)
	}
	dir := filepath.Join(s.Root, subject)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("no such class %q", subject)
	}
	res := &AskResult{Subject: subject}
	ap := filepath.Join(dir, askFile)
	raw, err := os.ReadFile(ap)
	if err != nil {
		if err := writeAtomic(ap, []byte(askTemplate)); err != nil {
			return nil, err
		}
		res.Created = true
		return res, nil
	}
	pre, items := askItems(string(raw))
	var pend []string // questions with no answer yet
	var pidx []int    // their place in items
	for i, it := range items {
		if !it.answered() {
			pend = append(pend, it.Q)
			pidx = append(pidx, i)
		}
	}
	res.Kept = len(pend)
	if len(pend) == 0 {
		return res, nil
	}
	qp := filepath.Join(dir, "questions.md")
	rel := subject + "/questions.md" // exclude it from the new-word count before it is written: the server counts within seconds
	if ex := s.ExcludedWords(); !containsStr(ex, rel) {
		if err := s.SetExcludedWords(append(ex, rel)); err != nil {
			return nil, err
		}
	}
	bankBytes, _ := os.ReadFile(qp)
	bank := string(bankBytes)
	ctx := s.askContext(subject)
	done := map[int]bool{}
	var firstErr error
	for off := 0; off < len(pend); off += askBatch {
		batch := pend[off:min(off+askBatch, len(pend))]
		out, err := runClaude(s.askPrompt(subject, batch, off, bankHeadings(bank), ctx), askTimeout)
		if err != nil {
			firstErr = err
			break
		}
		segs := parseAskOutput(out, len(pend))
		var keep []askSegment
		for _, sg := range segs {
			if sg.n <= off || sg.n > off+len(batch) || bankHasQuestion(bank, sg.block) {
				continue
			}
			keep = append(keep, sg)
			done[sg.n-1] = true
			items[pidx[sg.n-1]].Ans = askAnswerLines(sg.block, sg.heading)
			if !containsStr(res.Headings, sg.heading) {
				res.Headings = append(res.Headings, sg.heading)
			}
		}
		bank = mergeIntoBank(bank, keep)
		if err := writeAtomic(qp, []byte(bank)); err != nil {
			return nil, err
		}
	}
	res.Answered = len(done)
	res.Kept = len(pend) - len(done)
	out := pre
	for _, it := range items {
		out += "\n" + it.Q + "\n"
		for _, l := range it.Ans {
			if l == "" {
				out += ">\n"
			} else {
				out += "> " + l + "\n"
			}
		}
	}
	if err := writeAtomic(ap, []byte(out)); err != nil {
		return nil, err
	}
	if firstErr != nil {
		res.Error = firstErr.Error()
	}
	return res, firstErr
}

func containsStr(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// bankHasQuestion reports whether the bank already asks the block's question.
func bankHasQuestion(bank, block string) bool {
	q := func(t string) string {
		for _, l := range strings.Split(t, "\n") {
			if strings.HasPrefix(l, "Q:") {
				return quizNorm(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(l[2:]), "[gen]")))
			}
		}
		return ""
	}
	want := q(block)
	if want == "" {
		return false
	}
	for _, l := range strings.Split(bank, "\n") {
		if strings.HasPrefix(l, "Q:") && q(l) == want {
			return true
		}
	}
	return false
}

// notesview ask [--all] [CLASS…]
func runAskCommand(store *Store, args []string, asJSON bool, stdout, stderr io.Writer) int {
	var subjects []string
	all := false
	for _, a := range args {
		switch a {
		case "--json", "-json":
			asJSON = true
		case "--all":
			all = true
		default:
			subjects = append(subjects, strings.Trim(a, "/"))
		}
	}
	if all {
		ents, _ := os.ReadDir(store.Root)
		for _, e := range ents {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				if _, err := os.Stat(filepath.Join(store.Root, e.Name(), askFile)); err == nil {
					subjects = append(subjects, e.Name())
				}
			}
		}
	}
	if len(subjects) == 0 {
		fmt.Fprintln(stderr, "usage: notesview ask [--all] [--json] CLASS…   (answers <CLASS>/ask.md into <CLASS>/questions.md)")
		return 2
	}
	code := 0
	for _, sub := range subjects {
		r, err := store.AnswerAsks(sub)
		if r == nil {
			fmt.Fprintln(stderr, "notesview:", err)
			code = 1
			continue
		}
		j, _ := json.Marshal(r)
		switch {
		case asJSON:
			fmt.Fprintln(stdout, string(j))
		case r.Created:
			fmt.Fprintf(stdout, "%s: created %s/%s: write questions in it, then run this again\n", sub, sub, askFile)
		case r.Answered == 0 && r.Kept == 0 && err == nil:
			fmt.Fprintf(stdout, "%s: no questions in %s\n", sub, askFile)
		default:
			fmt.Fprintf(stdout, "%s: answered %d, %d unanswered in %s", sub, r.Answered, r.Kept, askFile)
			if len(r.Headings) > 0 {
				fmt.Fprintf(stdout, "; filed under %s", strings.Join(r.Headings, "; "))
			}
			fmt.Fprintln(stdout)
		}
		if err != nil {
			fmt.Fprintln(stderr, "notesview:", err)
			code = 1
		}
	}
	return code
}
