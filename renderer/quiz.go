package main

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

// The quiz, in Go, for the viewer's Quiz tab (quiz_server.go, web/quiz.js). It reads and writes the
// same files as quiz.py, so the terminal quiz and the viewer share history:
//   - <subject>/definitions.md (vocab, `word :: meaning (type)`) and <subject>/questions.md (banks)
//   - .revision/questions/<note> and .revision/questions/_mixed/<subject>.md (revision banks)
//   - <subject>/.quiz_stats.json: per word/question {right, wrong, streak, credit, last, notes, reask}
//     plus the __player__ record (XP, streak days, best combo, badges)
//   - .quiz_log.jsonl: seconds, answered, correct, xp, levels per answer (the score reads it)
//   - .signals/<day>.jsonl: one quiz record per answer (when the quiz sensor is on)
//   - <subject>/mistakes.md: confirmed-wrong answers
// Grading follows quiz.py: vocab and short answers match any listed meaning (case, punctuation and
// macrons ignored), select-all earns partial credit, worked problems are self-graded (y / n /
// partly), and every answer marked wrong can be overridden ("Actually right?"), which for free text
// also saves the answer as an accepted one.

const (
	quizReadCap   = 120 * time.Second // reading a worked solution before grading it
	quizPlayer    = "__player__"
	quizMixTopics = 3 // a blended revision covers up to this many due topics of one folder
	quizMixPer    = 4 // questions drawn from each topic when blending
	quizMixCross  = 3 // cross-topic questions added to a blend
	quizMastered  = 5 // correct answers in a row = mastered
)

// quizAnswerCaps is how long an answer may take before you count as idle (stopped quizzing rather than
// still thinking), per question kind: time beyond it is not quiz time, and the answer's response time
// is not measured (quiz.py ANSWER_CAPS).
var quizAnswerCaps = map[string]time.Duration{"vocab": 10 * time.Second, "tf": 20 * time.Second, "mc": 30 * time.Second,
	"multi": 45 * time.Second, "short": 45 * time.Second, "problem": 600 * time.Second, "recall": 600 * time.Second}

var (
	quizRanks  = []string{"Tiro", "Miles", "Optio", "Centurio", "Tribunus", "Legatus", "Consul", "Imperator"}
	quizBadges = map[string]string{
		"first_blood": "Primus Sanguis (first correct answer)",
		"combo5":      "Ardens (5 in a row)",
		"combo10":     "Fulmen (10 in a row)",
		"combo20":     "Invictus (20 in a row)",
		"recovered":   "Phoenix (turned a weak word around)",
		"perfect":     "Perfectus (10+ answers, no mistakes)",
		"week":        "Constans (7-day streak)",
	}
	quizListMarkRe = regexp.MustCompile(`^\s*(?:[-*+]\s+|\d+\.\s+)?`)
	quizKindRe     = regexp.MustCompile(`\(([^()]*)\)\s*$`)
	quizParenRe    = regexp.MustCompile(`\([^)]*\)`)
	quizPronounRe  = regexp.MustCompile(`(?i)^\s*((?:(?:I|you|he|she|it|we|they)\s*[,/]\s*)+(?:I|you|he|she|it|we|they))\s+(\S.*)$`)
	quizSplitRe    = regexp.MustCompile(`[,/]`)
	quizChoiceRe   = regexp.MustCompile(`^([a-hA-H])[).][ \t]+(.*)$`)
	quizLettersRe  = regexp.MustCompile(`\band\b|[,;&\s]+`)
	quizReferRe    = regexp.MustCompile(`(?i)\babove\b|\b[A-H] and [A-H]\b`)
	quizNegRe      = regexp.MustCompile(`\b(?:NOT|EXCEPT|LEAST)\b`)
	quizNoneOfRe   = regexp.MustCompile(`(?i)\b(?:all|none) of the above\b`)
	quizTrailRe    = regexp.MustCompile(`\s*(\([^()]*\))\s*$`)
)

// ── text matching (quiz.py norm / meanings / check) ──

func quizNorm(s string) string {
	var b strings.Builder
	space := false
	for _, r := range foldText(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_':
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		case unicode.IsSpace(r):
			space = true
		}
	}
	return b.String()
}

// quizMeanings splits "to love, to like; be fond of (x)" into the accepted meanings; "he, she, it
// said" expands to "he said", "she said", "it said".
func quizMeanings(s string) []string {
	s = quizParenRe.ReplaceAllString(s, "")
	var out []string
	for _, part := range strings.Split(s, ";") {
		if m := quizPronounRe.FindStringSubmatch(part); m != nil {
			for _, p := range quizSplitRe.Split(m[1], -1) {
				out = append(out, strings.TrimSpace(p)+" "+strings.TrimSpace(m[2]))
			}
		} else {
			out = append(out, quizSplitRe.Split(part, -1)...)
		}
	}
	var keep []string
	for _, o := range out {
		if quizNorm(o) != "" {
			keep = append(keep, strings.TrimSpace(o))
		}
	}
	return keep
}

func quizCheck(guess, answer string) bool {
	g := quizNorm(guess)
	if g == "" {
		return false
	}
	if g == quizNorm(answer) {
		return true
	}
	for _, m := range quizMeanings(answer) {
		if quizNorm(m) == g {
			return true
		}
	}
	return false
}

// quizOthers is the accepted meanings of answer other than the one guessed.
func quizOthers(guess, answer string) []string {
	g := quizNorm(guess)
	var out []string
	hit := false
	for _, m := range quizMeanings(answer) {
		if quizNorm(m) == g {
			hit = true
		} else {
			out = append(out, m)
		}
	}
	if !hit {
		return nil
	}
	return out
}

// ── banks ──

// QuizQuestion is one block of a question bank (format in notes/format/question-format.md).
type QuizQuestion struct {
	Topic    string   `json:"topic"`
	Q        string   `json:"q"`
	Choices  []string `json:"choices"`
	A        string   `json:"a"`
	Solution string   `json:"solution"`
	Why      string   `json:"why"`
	Src      string   `json:"src"`
	file     string
}

func quizAnswerLetters(a string) []string {
	t := quizLettersRe.ReplaceAllString(strings.ToLower(a), "")
	if t == "" {
		return nil
	}
	var out []string
	seen := map[rune]bool{}
	for _, r := range t {
		if r < 'a' || r > 'h' {
			return nil
		}
		if !seen[r] {
			seen[r] = true
			out = append(out, string(r))
		}
	}
	return out
}

func quizKind(q *QuizQuestion) string {
	switch {
	case q.Solution != "":
		return "problem"
	case len(q.Choices) > 0 && len(quizAnswerLetters(q.A)) > 1:
		return "multi"
	case len(q.Choices) > 0:
		return "mc"
	case quizNorm(q.A) == "true" || quizNorm(q.A) == "false":
		return "tf"
	}
	return "short"
}

func dedent(s string) string {
	lines := strings.Split(s, "\n")
	common := -1
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		n := len(l) - len(strings.TrimLeft(l, " \t"))
		if common < 0 || n < common {
			common = n
		}
	}
	if common > 0 {
		for i, l := range lines {
			if len(l) >= common {
				lines[i] = l[common:]
			} else {
				lines[i] = strings.TrimLeft(l, " \t")
			}
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// loadQuizQuestions parses a bank the way quiz.py's load_questions does (same keys for the stats).
func loadQuizQuestions(path string) []*QuizQuestion {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []*QuizQuestion
	var cur *QuizQuestion
	field, topic := "", ""
	flush := func() {
		if cur == nil {
			return
		}
		cur.Q, cur.A, cur.Solution, cur.Why, cur.Src = dedent(cur.Q), dedent(cur.A), dedent(cur.Solution), dedent(cur.Why), dedent(cur.Src)
		ok := cur.Solution != ""
		if !ok && cur.A != "" {
			ok = len(cur.Choices) == 0
			if !ok {
				ok = true
				ls := quizAnswerLetters(cur.A)
				if len(ls) == 0 {
					ok = false
				}
				for _, l := range ls {
					if int(l[0]-'a') >= len(cur.Choices) {
						ok = false
					}
				}
			}
		}
		if ok {
			out = append(out, cur)
		}
		cur = nil
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimRight(line, " \t\r")
		if strings.HasPrefix(line, "#") {
			flush()
			field = ""
			if strings.HasPrefix(line, "## ") {
				topic = strings.TrimSpace(line[3:])
			}
			continue
		}
		if m := graphFieldRe.FindStringSubmatch(line); m != nil {
			name := strings.ToLower(m[1])
			if name == "q" {
				flush()
				cur = &QuizQuestion{Topic: topic, file: path}
			}
			if cur != nil {
				field = name
				cur.set(field, m[2])
			}
			continue
		}
		if cur == nil || strings.TrimSpace(line) == "" {
			continue
		}
		if c := quizChoiceRe.FindStringSubmatch(strings.TrimSpace(line)); c != nil && (field == "q" || field == "choices") {
			cur.Choices = append(cur.Choices, strings.TrimSpace(c[2]))
			field = "choices"
		} else if field != "" && field != "choices" {
			cur.set(field, cur.get(field)+"\n"+line)
		}
	}
	flush()
	return out
}

func (q *QuizQuestion) get(f string) string {
	switch f {
	case "q":
		return q.Q
	case "a":
		return q.A
	case "solution":
		return q.Solution
	case "why":
		return q.Why
	case "src":
		return q.Src
	}
	return ""
}

func (q *QuizQuestion) set(f, v string) {
	switch f {
	case "q":
		q.Q = v
	case "a":
		q.A = v
	case "solution":
		q.Solution = v
	case "why":
		q.Why = v
	case "src":
		q.Src = v
	}
}

// loadRevisionBank drops generated "which is NOT…" / "all of the above" questions while 3 others remain.
func loadRevisionBank(path string) []*QuizQuestion {
	qs := loadQuizQuestions(path)
	var keep []*QuizQuestion
	for _, q := range qs {
		weak := false
		if len(q.Choices) > 0 && strings.Contains(q.Q+q.Src, "[gen]") {
			weak = quizNegRe.MatchString(q.Q)
			for _, c := range q.Choices {
				weak = weak || quizNoneOfRe.MatchString(c)
			}
		}
		if !weak {
			keep = append(keep, q)
		}
	}
	if len(keep) >= 3 {
		return keep
	}
	return qs
}

type quizPair struct{ Word, Trans string }

func loadVocab(path string) []quizPair {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []quizPair
	for _, l := range strings.Split(string(b), "\n") {
		l = quizListMarkRe.ReplaceAllString(l, "")
		w, t, ok := strings.Cut(l, "::")
		if !ok {
			continue
		}
		w, t = strings.TrimSpace(w), strings.TrimSpace(t)
		if w != "" && t != "" {
			out = append(out, quizPair{w, t})
		}
	}
	return out
}

func vocabType(trans string) string {
	if m := quizKindRe.FindStringSubmatch(trans); m != nil {
		return strings.ToLower(strings.TrimSpace(m[1]))
	}
	return ""
}

// QuizSubject is a folder you can practise: vocab (definitions.md) or a bank (questions.md).
type QuizSubject struct {
	Name   string   `json:"name"`
	Kind   string   `json:"kind"` // vocab or questions
	Count  int      `json:"count"`
	Groups []string `json:"groups"` // word types or bank topics
	Seen   int      `json:"seen"`   // answered at least once
	Weak   int      `json:"weak"`   // struggling
	Known  int      `json:"known"`  // known well (a focused quiz skips them)
	Level  int      `json:"level"`
	Rank   string   `json:"rank"`
	XP     int      `json:"xp"`
}

func (s *Store) QuizSubjects() []QuizSubject {
	ents, _ := os.ReadDir(s.Root)
	var out []QuizSubject
	for _, e := range ents {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		items, kind := s.quizBank(e.Name())
		if len(items) == 0 {
			continue
		}
		st := s.loadQuizStats(e.Name())
		sub := QuizSubject{Name: e.Name(), Kind: kind, Count: len(items)}
		groups := map[string]bool{}
		for _, it := range items {
			groups[it.group] = true
			if st.score(it.Key) >= 0 {
				sub.Seen++
			}
			if st.struggling(it.Key) {
				sub.Weak++
			}
			if st.knownWell(it.Key) {
				sub.Known++
			}
		}
		for g := range groups {
			sub.Groups = append(sub.Groups, g)
		}
		sort.Strings(sub.Groups)
		pl := st.player()
		sub.XP, sub.Level = pl.XP, quizLevel(pl.XP)
		sub.Rank = quizRank(sub.Level)
		out = append(out, sub)
	}
	return out
}

// quizItem is one thing to ask: a vocab pair or a question.
type quizItem struct {
	Key    string
	Pair   *quizPair
	Q      *QuizQuestion
	RevIDs []string // revision topics the answer counts toward
	group  string
}

func (it *quizItem) kind() string {
	if it.Pair != nil {
		return "vocab"
	}
	return quizKind(it.Q)
}

// quizBank is a subject's items: its questions.md if it has one, else definitions.md.
func (s *Store) quizBank(subject string) ([]*quizItem, string) {
	var out []*quizItem
	if qs := loadQuizQuestions(filepath.Join(s.Root, subject, "questions.md")); len(qs) > 0 {
		for _, q := range qs {
			g := q.Topic
			if g == "" {
				g = "untitled"
			}
			out = append(out, &quizItem{Key: q.Q, Q: q, group: g})
		}
		return out, "questions"
	}
	for _, p := range loadVocab(filepath.Join(s.Root, subject, "definitions.md")) {
		p := p
		g := vocabType(p.Trans)
		if g == "" {
			g = "untyped"
		}
		out = append(out, &quizItem{Key: p.Word, Pair: &p, group: g})
	}
	return out, "vocab"
}

// ── stats (.quiz_stats.json, shared with quiz.py) ──

type quizStat struct {
	Right  int      `json:"right"`
	Wrong  int      `json:"wrong"`
	Streak int      `json:"streak"`
	Credit *float64 `json:"credit,omitempty"`
	Last   string   `json:"last,omitempty"`
	Notes  []string `json:"notes,omitempty"`
	Reask  bool     `json:"reask,omitempty"`
	RtMs   *int     `json:"rt_ms,omitempty"` // response time, a running average of answers within the cut-off
}

type quizPlayerRec struct {
	XP        int      `json:"xp"`
	Days      int      `json:"days"`
	BestCombo int      `json:"best_combo"`
	Badges    []string `json:"badges"`
	LastDay   string   `json:"last_day,omitempty"`
}

type quizStats struct {
	raw     map[string]json.RawMessage
	entries map[string]*quizStat
	pl      *quizPlayerRec
}

func (s *Store) quizStatsPath(subject string) string {
	return filepath.Join(s.Root, subject, ".quiz_stats.json")
}

func (s *Store) loadQuizStats(subject string) *quizStats {
	st := &quizStats{raw: map[string]json.RawMessage{}, entries: map[string]*quizStat{}}
	if b, err := os.ReadFile(s.quizStatsPath(subject)); err == nil {
		_ = json.Unmarshal(b, &st.raw)
	}
	return st
}

func (st *quizStats) get(key string) *quizStat {
	if e, ok := st.entries[key]; ok {
		return e
	}
	raw, ok := st.raw[key]
	if !ok || key == quizPlayer {
		return nil
	}
	var e quizStat
	if json.Unmarshal(raw, &e) != nil {
		return nil
	}
	st.entries[key] = &e
	return &e
}

func (st *quizStats) player() *quizPlayerRec {
	if st.pl == nil {
		st.pl = &quizPlayerRec{}
		if raw, ok := st.raw[quizPlayer]; ok {
			_ = json.Unmarshal(raw, st.pl)
		}
		if st.pl.Badges == nil {
			st.pl.Badges = []string{}
		}
	}
	return st.pl
}

func (s *Store) saveQuizStats(subject string, st *quizStats) error {
	for k, e := range st.entries {
		b, _ := json.Marshal(e)
		st.raw[k] = b
	}
	if st.pl != nil {
		b, _ := json.Marshal(st.pl)
		st.raw[quizPlayer] = b
	}
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(st.raw); err != nil {
		return err
	}
	return writeAtomic(s.quizStatsPath(subject), []byte(buf.String()))
}

// score is (right+1)/(answers+2), or -1 when never answered (quiz.py word_score).
func (st *quizStats) score(key string) float64 {
	e := st.get(key)
	if e == nil || e.Right+e.Wrong == 0 {
		return -1
	}
	return float64(e.Right+1) / float64(e.Right+e.Wrong+2)
}

func (st *quizStats) struggling(key string) bool {
	e := st.get(key)
	return e != nil && e.Wrong > 0 && e.Streak < 3
}

func (st *quizStats) weight(key string) float64 {
	e := st.get(key)
	switch {
	case e == nil:
		return 6
	case e.Reask:
		return 8
	case st.struggling(key):
		return 4
	case e.Streak >= quizMastered:
		return 0.15
	}
	return 1
}

// record counts an answer: only full credit is right; credit keeps a running average.
func (st *quizStats) record(key string, credit float64, notes []string, rt *time.Duration, today string) *quizStat {
	e := st.get(key)
	if e == nil {
		e = &quizStat{}
		st.entries[key] = e
	}
	credit = math.Max(0, math.Min(1, credit))
	if credit >= 1 {
		e.Right++
		e.Streak++
		e.Reask = false
	} else {
		e.Wrong++
		e.Streak = 0
	}
	c := credit
	if e.Credit != nil {
		c = (*e.Credit + credit) / 2
	}
	c = math.Round(c*1000) / 1000
	e.Credit = &c
	e.Last = today
	if rt != nil {
		ms := int(rt.Milliseconds())
		if e.RtMs != nil {
			ms = int(math.Round(float64(*e.RtMs+ms) / 2))
		}
		e.RtMs = &ms
	}
	if len(notes) > 0 {
		set := map[string]bool{}
		for _, n := range append(append([]string{}, e.Notes...), notes...) {
			set[n] = true
		}
		e.Notes = e.Notes[:0]
		for n := range set {
			e.Notes = append(e.Notes, n)
		}
		sort.Strings(e.Notes)
	}
	return e
}

func quizLevel(xp int) int    { return 1 + int(math.Sqrt(float64(xp)/50)) }
func quizXPFor(level int) int { return 50 * (level - 1) * (level - 1) }
func quizRank(level int) string {
	return quizRanks[min(level-1, len(quizRanks)-1)]
}

// startDay updates the daily streak (quiz.py start_player); returns a streak message.
func (pl *quizPlayerRec) startDay(now time.Time) string {
	today := now.Format(isoDate)
	if pl.LastDay == today {
		return ""
	}
	if pl.LastDay != "" && addDays(pl.LastDay, 1) == today {
		pl.Days++
	} else {
		pl.Days = 1
	}
	pl.LastDay = today
	if pl.Days > 1 {
		return fmt.Sprintf("%d-day streak", pl.Days)
	}
	return ""
}

func (pl *quizPlayerRec) award(key string) string {
	for _, b := range pl.Badges {
		if b == key {
			return ""
		}
	}
	pl.Badges = append(pl.Badges, key)
	return "Badge unlocked: " + quizBadges[key]
}

// ── writing back: accepted answers, mistakes, logs ──

// addMeaning accepts guess as another meaning of word in definitions.md, before its trailing (type).
func (s *Store) addMeaning(subject, word, guess string) {
	merged := func(t string) string {
		if m := quizTrailRe.FindStringSubmatchIndex(t); m != nil {
			return t[:m[0]] + ", " + guess + " " + t[m[2]:m[3]]
		}
		return t + ", " + guess
	}
	path := filepath.Join(s.Root, subject, "definitions.md")
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(string(b), "\n")
	for i, l := range lines {
		head, tail, ok := strings.Cut(l, "::")
		if ok && strings.TrimSpace(quizListMarkRe.ReplaceAllString(head, "")) == word {
			sp := ""
			if strings.HasPrefix(tail, " ") {
				sp = " "
			}
			lines[i] = head + "::" + sp + merged(strings.TrimSpace(tail))
			_ = writeAtomic(path, []byte(strings.Join(lines, "\n")))
			return
		}
	}
}

// addAnswer appends "; guess" to a short-answer question's first A: line in its bank.
func addAnswer(q *QuizQuestion, guess string) {
	b, err := os.ReadFile(q.file)
	if err != nil {
		return
	}
	lines := strings.Split(string(b), "\n")
	first := strings.TrimSpace(strings.SplitN(q.Q, "\n", 2)[0])
	for i, l := range lines {
		m := graphFieldRe.FindStringSubmatch(strings.TrimRight(l, " \t\r"))
		if m == nil || m[1] != "Q" || strings.TrimSpace(m[2]) != first {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			n := graphFieldRe.FindStringSubmatch(strings.TrimRight(lines[j], " \t\r"))
			if n != nil && n[1] == "Q" {
				return
			}
			if n != nil && n[1] == "A" {
				lines[j] = strings.TrimRight(lines[j], " \t\r") + "; " + guess
				q.A += "; " + guess
				_ = writeAtomic(q.file, []byte(strings.Join(lines, "\n")))
				return
			}
		}
		return
	}
}

// logMistake lists a confirmed-wrong answer in <subject>/mistakes.md (quiz.py log_mistake).
func (s *Store) logMistake(subject, q, guess, answer string, notes []string, now time.Time) {
	one := func(t string, n int) string {
		var parts []string
		for _, x := range strings.Split(t, "\n") {
			if x = strings.TrimSpace(x); x != "" {
				parts = append(parts, x)
			}
		}
		t = strings.Join(parts, " ⏎ ")
		if r := []rune(t); len(r) > n {
			t = string(r[:n-1]) + "…"
		}
		return t
	}
	rel := subject + "/mistakes.md"
	path := filepath.Join(s.Root, rel)
	_, statErr := os.Stat(path)
	if statErr != nil { // what the quiz writes here is not your own writing
		if ex := s.ExcludedWords(); inTracked(rel, s.TrackedWords()) && !inTracked(rel, ex) {
			s.RecordWords(now)
			_ = s.SetExcludedWords(append(ex, rel))
		}
	}
	var lines []string
	if b, err := os.ReadFile(path); err == nil {
		lines = strings.Split(string(b), "\n")
	} else {
		lines = []string{"# Mistakes", "", "Answers the quiz marked wrong and you confirmed wrong, one line per question. Written by the quiz.", ""}
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	today := now.Format(isoDate)
	head := "- **Q:** " + one(q, 400) + " — "
	countRe := regexp.MustCompile(`missed (\d+)× · last [\d-]+`)
	found := false
	for i, l := range lines {
		if strings.HasPrefix(l, head) {
			n := 1
			if m := countRe.FindStringSubmatch(l); m != nil {
				fmt.Sscan(m[1], &n)
			}
			lines[i] = countRe.ReplaceAllString(l, fmt.Sprintf("missed %d× · last %s", n+1, today))
			found = true
			break
		}
	}
	if !found {
		var topics []string
		for _, n := range notes {
			topics = append(topics, strings.TrimSuffix(filepath.Base(n), ".md"))
		}
		g := one(guess, 300)
		if g == "" {
			g = "–"
		}
		line := head + "you: " + g + " — answer: " + one(answer, 300)
		if len(topics) > 0 {
			line += " — " + strings.Join(topics, ", ")
		}
		if len(lines) > 0 && !strings.HasPrefix(lines[len(lines)-1], "- **Q:**") {
			lines = append(lines, "")
		}
		lines = append(lines, line+" · missed 1× · last "+today)
	}
	_ = writeAtomic(path, []byte(strings.Join(lines, "\n")+"\n"))
}

// logQuizTime appends a .quiz_log.jsonl line (quiz.py log_session): what scores quiz time and XP.
func (s *Store) logQuizTime(subject string, secs, answered, correct, xp, levels int, now time.Time) {
	if secs < 1 && answered == 0 && xp == 0 && levels == 0 {
		return
	}
	e := map[string]any{"date": now.Format(isoDate), "at": now.Format(scoreStamp), "subject": subject,
		"seconds": secs, "answered": answered, "correct": correct}
	if xp != 0 || levels != 0 {
		e["xp"], e["levels"] = xp, levels
	}
	b, _ := json.Marshal(e)
	f, err := os.OpenFile(filepath.Join(s.Root, quizLog), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(b, '\n'))
}

// logQuizSignal: rt nil = the answer came after its cut-off, logged as idle without a time.
func (s *Store) logQuizSignal(subject, kind string, rt *time.Duration, credit float64, revision string, now time.Time) {
	if !s.Sensors().Sensors["quiz"] {
		return
	}
	rec := map[string]any{"at": now.Format(scoreStamp), "src": "quiz", "subject": subject, "kind": kind,
		"correct": credit >= 1, "from": "viewer"}
	if rt == nil {
		rec["idle"] = true
	} else {
		rec["latency_ms"] = rt.Milliseconds()
	}
	if credit > 0 && credit < 1 {
		rec["credit"] = math.Round(credit*100) / 100
	}
	if revision != "" {
		rec["revision"] = revision
	}
	appendSignal(s.Root, rec)
}

// ── picking ──

func quizCooldown(age int) float64 { return 0.02 + 0.98*math.Min(1, float64(age)/20) }

// pickQuiz is quiz.py's weighted draw: new and missed items more often, the one just asked almost never.
func pickQuiz(items []*quizItem, st *quizStats, asked map[string]int, n int, last string, rnd *rand.Rand) *quizItem {
	pool := items
	if len(items) > 1 {
		pool = nil
		for _, it := range items {
			if it.Key != last {
				pool = append(pool, it)
			}
		}
	}
	ws := make([]float64, len(pool))
	total := 0.0
	for i, it := range pool {
		w := st.weight(it.Key)
		if a, ok := asked[it.Key]; ok {
			age := n - a
			if e := st.get(it.Key); e != nil && e.Reask {
				age *= 4
			}
			w *= quizCooldown(age)
		}
		ws[i] = w
		total += w
	}
	r := rnd.Float64() * total
	for i, w := range ws {
		if r -= w; r <= 0 {
			return pool[i]
		}
	}
	return pool[len(pool)-1]
}

const quizKnownStreak = 3 // right this many times in a row = known well (a focused quiz skips it)

func (st *quizStats) knownWell(key string) bool {
	e := st.get(key)
	return e != nil && e.Streak >= quizKnownStreak
}

func (st *quizStats) timesAsked(key string) int {
	if e := st.get(key); e != nil {
		return e.Right + e.Wrong
	}
	return 0
}

// pickFocused is a focused quiz's draw: never what you know well; of the rest, one asked the fewest
// times so far (random among ties; not the one just asked unless it is the only one left). Nil when
// everything is known well.
func pickFocused(items []*quizItem, st *quizStats, last string, rnd *rand.Rand) *quizItem {
	var pool, other []*quizItem
	for _, it := range items {
		if st.knownWell(it.Key) {
			continue
		}
		pool = append(pool, it)
		if it.Key != last {
			other = append(other, it)
		}
	}
	if len(other) > 0 {
		pool = other
	}
	if len(pool) == 0 {
		return nil
	}
	least := math.MaxInt
	for _, it := range pool {
		least = min(least, st.timesAsked(it.Key))
	}
	var cands []*quizItem
	for _, it := range pool {
		if st.timesAsked(it.Key) == least {
			cands = append(cands, it)
		}
	}
	return cands[rnd.Intn(len(cands))]
}

// medianRT is the middle response time (ms) of the answered items, for telling slow answers apart.
func medianRT(items []*quizItem, st *quizStats) float64 {
	var ts []int
	for _, it := range items {
		if e := st.get(it.Key); e != nil && e.RtMs != nil && *e.RtMs > 0 {
			ts = append(ts, *e.RtMs)
		}
	}
	if len(ts) == 0 {
		return 0
	}
	sort.Ints(ts)
	return float64(ts[len(ts)/2])
}

// quizNeed is how much practice an item needs (0-1.5) and the main reason (quiz.py need): how much
// credit you got, a confirmed mistake, still shaky, answering slowly (1.5x your usual time or more),
// not seen for longer than its streak holds (1, 2, 4… days). New items 0.6; well-known, fast, recent
// ones next to nothing.
func (st *quizStats) need(key string, medRT float64, today time.Time) (float64, string) {
	e := st.get(key)
	if e == nil || e.Right+e.Wrong == 0 {
		return 0.6, "new"
	}
	acc := float64(e.Right) / float64(e.Right+e.Wrong)
	if e.Credit != nil {
		acc = *e.Credit
	}
	score, reasons := 1-acc, []string{}
	if e.Reask {
		score += 0.4
		reasons = append(reasons, "missed last time")
	} else if e.Wrong > 0 && e.Streak < quizKnownStreak {
		score += 0.2
		reasons = append(reasons, "still shaky")
	}
	if e.RtMs != nil && medRT > 0 && float64(*e.RtMs) >= 1.5*medRT {
		score += 0.3 * math.Min(1, (float64(*e.RtMs)/medRT-1)/2)
		reasons = append(reasons, fmt.Sprintf("slow: %.1fs vs your usual %.1fs", float64(*e.RtMs)/1000, medRT/1000))
	}
	if last, err := time.ParseInLocation(isoDate, e.Last, today.Location()); err == nil {
		y, m, d := today.Date()
		days := int(time.Date(y, m, d, 0, 0, 0, 0, today.Location()).Sub(last).Hours()/24 + 0.5)
		holds := float64(int(1) << min(e.Streak, 6))
		if float64(days) > holds {
			score += math.Min(0.3, 0.1*float64(days)/holds)
			reasons = append(reasons, fmt.Sprintf("not seen in %d days", days))
		}
	}
	if e.Streak >= quizMastered && len(reasons) == 0 {
		score *= 0.3
	}
	if len(reasons) == 0 && acc < 0.7 {
		reasons = append(reasons, fmt.Sprintf("%d%% right so far", int(math.Round(acc*100))))
	}
	why := ""
	if len(reasons) > 0 {
		why = reasons[0]
	}
	return math.Min(score, 1.5), why
}

// pickPersonal is a personalized quiz's draw: weight need² (the neediest come up far more often), cut
// to almost nothing right after an item was asked, as in pickQuiz. Returns the item and its reason.
func pickPersonal(items []*quizItem, st *quizStats, asked map[string]int, n int, last string, now time.Time, rnd *rand.Rand) (*quizItem, string) {
	med := medianRT(items, st)
	pool := items
	if len(items) > 1 {
		pool = nil
		for _, it := range items {
			if it.Key != last {
				pool = append(pool, it)
			}
		}
	}
	ws, whys := make([]float64, len(pool)), make([]string, len(pool))
	total := 0.0
	for i, it := range pool {
		sc, why := st.need(it.Key, med, now)
		w := 0.05 + sc*sc
		if a, ok := asked[it.Key]; ok {
			w *= quizCooldown(n - a)
		}
		ws[i], whys[i] = w, why
		total += w
	}
	r := rnd.Float64() * total
	for i, w := range ws {
		if r -= w; r <= 0 {
			return pool[i], whys[i]
		}
	}
	return pool[len(pool)-1], whys[len(pool)-1]
}

// personalSummary says what a personalized quiz will work on, by main reason.
func personalSummary(items []*quizItem, st *quizStats, now time.Time) string {
	med := medianRT(items, st)
	counts := map[string]int{}
	for _, it := range items {
		sc, why := st.need(it.Key, med, now)
		if sc < 0.3 || why == "" {
			continue
		}
		label := strings.SplitN(strings.SplitN(why, ":", 2)[0], " in ", 2)[0]
		if strings.HasSuffix(label, "% right so far") {
			label = "low accuracy"
		}
		counts[label]++
	}
	type kv struct {
		k string
		n int
	}
	var kvs []kv
	for k, n := range counts {
		kvs = append(kvs, kv{k, n})
	}
	sort.Slice(kvs, func(i, j int) bool {
		if kvs[i].n != kvs[j].n {
			return kvs[i].n > kvs[j].n
		}
		return kvs[i].k < kvs[j].k
	})
	var parts []string
	for _, x := range kvs {
		parts = append(parts, fmt.Sprintf("%d %s", x.n, x.k))
	}
	if len(parts) == 0 {
		return "nothing stands out"
	}
	return strings.Join(parts, ", ")
}

// sampleWeighted draws n questions, missed and unseen ones more likely, no repeats.
func sampleWeighted(qs []*quizItem, st *quizStats, n int, rnd *rand.Rand) []*quizItem {
	type kv struct {
		it *quizItem
		k  float64
	}
	var ks []kv
	for _, it := range qs {
		ks = append(ks, kv{it, math.Pow(rnd.Float64(), 1/st.weight(it.Key))})
	}
	sort.Slice(ks, func(i, j int) bool { return ks[i].k > ks[j].k })
	var out []*quizItem
	for i := 0; i < len(ks) && i < n; i++ {
		out = append(out, ks[i].it)
	}
	return out
}

// interleave shuffles, then swaps so two questions of one topic are not neighbours where possible.
func interleave(qs []*quizItem, rnd *rand.Rand) {
	rnd.Shuffle(len(qs), func(i, j int) { qs[i], qs[j] = qs[j], qs[i] })
	for i := 1; i < len(qs); i++ {
		if qs[i].RevIDs[0] == qs[i-1].RevIDs[0] {
			for j := i + 1; j < len(qs); j++ {
				if qs[j].RevIDs[0] != qs[i-1].RevIDs[0] {
					qs[i], qs[j] = qs[j], qs[i]
					break
				}
			}
		}
	}
}

// QuizItemStat is one word or question in the Quiz tab's stats table.
type QuizItemStat struct {
	Key    string   `json:"key"`
	Answer string   `json:"answer,omitempty"` // vocab: the meaning
	Group  string   `json:"group"`
	Status string   `json:"status"` // new, shaky, learning, known (3 right in a row), mastered (5)
	Score  *float64 `json:"score"`  // (right+1)/(answers+2)
	Right  int      `json:"right"`
	Wrong  int      `json:"wrong"`
	Streak int      `json:"streak"`
	Credit *float64 `json:"credit,omitempty"`
	RtMs   *int     `json:"rt_ms,omitempty"`
	Last   string   `json:"last,omitempty"`
	Need   float64  `json:"need"` // what a personalized quiz goes by
	Why    string   `json:"why,omitempty"`
	Missed bool     `json:"missed,omitempty"` // a confirmed mistake not answered right since
}

// QuizStats is every item of a subject with its record, for the stats table.
func (s *Store) QuizStats(subject string, now time.Time) ([]QuizItemStat, error) {
	items, _ := s.quizBank(subject)
	if len(items) == 0 {
		return nil, fmt.Errorf("%w: nothing to quiz in %q", ErrRevision, subject)
	}
	st := s.loadQuizStats(subject)
	med := medianRT(items, st)
	out := []QuizItemStat{}
	for _, it := range items {
		r := QuizItemStat{Key: it.Key, Group: it.group, Status: "new"}
		if it.Pair != nil {
			r.Answer = it.Pair.Trans
		}
		r.Need, r.Why = st.need(it.Key, med, now)
		if e := st.get(it.Key); e != nil && e.Right+e.Wrong > 0 {
			sc := st.score(it.Key)
			r.Score, r.Right, r.Wrong, r.Streak, r.Credit, r.RtMs, r.Last, r.Missed = &sc, e.Right, e.Wrong, e.Streak, e.Credit, e.RtMs, e.Last, e.Reask
			switch {
			case e.Streak >= quizMastered:
				r.Status = "mastered"
			case e.Streak >= quizKnownStreak:
				r.Status = "known"
			case st.struggling(it.Key):
				r.Status = "shaky"
			default:
				r.Status = "learning"
			}
		}
		out = append(out, r)
	}
	return out, nil
}
