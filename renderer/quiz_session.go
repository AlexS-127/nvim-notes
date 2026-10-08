package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	mrand "math/rand"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Quiz sessions for the viewer (quiz.go has the shared pieces). A session is either practice (one
// subject, endless weighted draws, optionally one word type / topic or your weakest N) or a revision
// (one scheduled note, blended with other due notes of its folder, as quiz.py --revise). Sessions live
// in memory on the server; the stats, logs and revision schedule they write are the same files the
// terminal quiz writes, after every answer.
//
// An answer graded below full credit stays pending until you move on: "Actually right?" (override)
// turns it into full credit, Next confirms it (a confirmed mistake: listed in mistakes.md and asked
// again sooner). Skips and self-graded problems are committed at once.

type QuizCard struct {
	N        int      `json:"n"`
	Total    int      `json:"total"` // 0 = practice, open-ended
	Kind     string   `json:"kind"`  // vocab, mc, multi, tf, short, problem, recall
	Prompt   string   `json:"prompt"`
	Choices  []string `json:"choices,omitempty"`
	Hint     string   `json:"hint,omitempty"`
	Retry    bool     `json:"retry,omitempty"`
	Headings []string `json:"headings,omitempty"` // recall: what the note covers
	Note     string   `json:"note,omitempty"`     // recall: the note's path
	Known    *float64 `json:"known,omitempty"`    // the item's score before answering
}

type QuizResult struct {
	Credit     float64  `json:"credit"`
	Skipped    bool     `json:"skipped,omitempty"`
	Answer     string   `json:"answer"`
	Guess      string   `json:"guess,omitempty"` // what you answered
	Why        string   `json:"why,omitempty"`
	Src        string   `json:"src,omitempty"`
	Also       []string `json:"also,omitempty"`
	Partial    string   `json:"partial,omitempty"`
	Override   bool     `json:"override,omitempty"`   // "Actually right?" is on offer
	Overridden bool     `json:"overridden,omitempty"` // it was used
	Saved      string   `json:"saved,omitempty"`      // what the override added as an accepted answer
	XP         int      `json:"xp"`
	Combo      int      `json:"combo"`
	Notes      []string `json:"notes,omitempty"` // badges, level-ups, recovered words
	Known      *float64 `json:"known,omitempty"` // the item's score after
	Right      int      `json:"right"`
	Wrong      int      `json:"wrong"`
	Solution   string   `json:"solution,omitempty"` // a problem's worked solution (reveal)
	Pending    bool     `json:"pending,omitempty"`  // waiting for override or Next
}

type QuizSummary struct {
	Answered  int      `json:"answered"`
	Right     float64  `json:"right"`
	XP        int      `json:"xp"`
	BestCombo int      `json:"best_combo"`
	Seconds   int      `json:"seconds"`
	Missed    []string `json:"missed"`
	Revisions []string `json:"revisions,omitempty"` // what each revised note recorded
	Level     int      `json:"level"`
	Rank      string   `json:"rank"`
	PlayerXP  int      `json:"player_xp"`
	LevelLo   int      `json:"level_lo"`
	LevelHi   int      `json:"level_hi"`
	Notes     []string `json:"notes,omitempty"`
}

type QuizState struct {
	ID       string       `json:"id"`
	Mode     string       `json:"mode"`
	Title    string       `json:"title"`
	Subject  string       `json:"subject"`
	Latin    bool         `json:"latin"` // the ; macron shortcut applies
	Answered int          `json:"answered"`
	Right    float64      `json:"right"`
	XP       int          `json:"xp"`
	Combo    int          `json:"combo"`
	Streak   string       `json:"streak,omitempty"`
	Card     *QuizCard    `json:"card,omitempty"`
	Last     *QuizCard    `json:"last,omitempty"` // the card a pending result belongs to
	Result   *QuizResult  `json:"result,omitempty"`
	Summary  *QuizSummary `json:"summary,omitempty"`
	Done     bool         `json:"done"`
}

type quizAsk struct {
	item     *quizItem
	card     QuizCard
	forward  bool
	order    []int
	answer   string
	shown    time.Time
	cap      time.Duration
	revealed bool
}

type quizPending struct {
	took         time.Duration // from the card appearing to the answer (logged as the answer's latency)
	item         *quizItem
	ask          *quizAsk
	credit       float64
	guess, shown string // what you answered, and as the mistakes list shows it
	result       *QuizResult
}

type QuizSession struct {
	mu      sync.Mutex
	ID      string
	Mode    string // practice, revise
	Subject string
	Title   string

	items   []*quizItem // practice pool
	queue   []*quizItem // revision, in order
	retry   []*quizItem // revision: missed, asked once more at the end
	pos     int
	inRetry bool
	n       int
	asked   map[string]int
	last    string
	dir     int // vocab: 0 forward, 1 backward, 2 mixed

	recall  string            // revision with no bank: the note asked by recall
	topics  map[string]string // revision: id → title
	torder  []string
	planned map[string]int
	got     map[string]*[2]float64 // credit, answered

	cur     *quizAsk
	pending *quizPending
	last1   *QuizResult // the last result (for a reload)

	answered        int
	right           float64
	xp, combo, best int
	levels          int
	active, logged  time.Duration
	missed          []string
	revLines        []string
	notes           []string
	streak          string
	done            bool
	summary         *QuizSummary
	touched         time.Time
	rnd             *mrand.Rand
}

var quizSessions = struct {
	sync.Mutex
	m map[string]*QuizSession
}{m: map[string]*QuizSession{}}

func newQuizID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func getQuizSession(id string) *QuizSession {
	quizSessions.Lock()
	defer quizSessions.Unlock()
	for k, q := range quizSessions.m { // forget sessions untouched for 12 hours
		if time.Since(q.touched) > 12*time.Hour {
			delete(quizSessions.m, k)
		}
	}
	return quizSessions.m[id]
}

// QuizStart describes a session to start.
type QuizStart struct {
	Mode    string `json:"mode"`    // practice or revise
	Subject string `json:"subject"` // practice
	Group   string `json:"group"`   // practice: one word type or topic ("" = all)
	Dir     int    `json:"dir"`     // vocab: 0 forward, 1 backward, 2 mixed
	Weakest int    `json:"weakest"` // practice: only the N weakest answered items
	ID      string `json:"id"`      // revise: the note
	Solo    bool   `json:"solo"`    // revise: no blending
}

func (s *Store) StartQuiz(req QuizStart, now time.Time) (*QuizSession, error) {
	q := &QuizSession{ID: newQuizID(), Mode: req.Mode, asked: map[string]int{}, dir: req.Dir, touched: now,
		rnd: mrand.New(mrand.NewSource(now.UnixNano()))}
	switch req.Mode {
	case "practice":
		items, kind := s.quizBank(req.Subject)
		if len(items) == 0 || strings.ContainsAny(req.Subject, "/\\") {
			return nil, fmt.Errorf("%w: nothing to quiz in %q", ErrRevision, req.Subject)
		}
		if req.Group != "" {
			var keep []*quizItem
			for _, it := range items {
				if it.group == req.Group {
					keep = append(keep, it)
				}
			}
			items = keep
		}
		st := s.loadQuizStats(req.Subject)
		if req.Weakest > 0 {
			items = weakestQuiz(items, st, req.Weakest)
			if len(items) == 0 {
				return nil, fmt.Errorf("%w: nothing has been answered yet, so nothing is the weakest", ErrRevision)
			}
		}
		if len(items) == 0 {
			return nil, fmt.Errorf("%w: nothing in that group", ErrRevision)
		}
		q.Subject, q.items = req.Subject, items
		q.Title = req.Subject
		if req.Weakest > 0 {
			q.Title += fmt.Sprintf(" · weakest %d", len(items))
		} else if req.Group != "" {
			q.Title += " · " + req.Group
		}
		if kind == "vocab" {
			q.Title += " · " + []string{"Latin → English", "English → Latin", "mixed"}[min(max(req.Dir, 0), 2)]
		}
	case "revise":
		if err := s.planRevision(q, req.ID, req.Solo, now); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("%w: unknown mode %q", ErrRevision, req.Mode)
	}
	st := s.loadQuizStats(q.Subject)
	q.streak = st.player().startDay(now)
	_ = s.saveQuizStats(q.Subject, st)
	q.advance(s, now)
	quizSessions.Lock()
	quizSessions.m[q.ID] = q
	quizSessions.Unlock()
	return q, nil
}

var quizHeadLineRe = regexp.MustCompile(`^#{1,4} `)

// planRevision is quiz.py revise(): the note's bank, or recall when it has none; blended with up to
// two other due notes of the folder (quizMixPer questions each, missed and unseen likelier) and up to
// quizMixCross cross-topic questions that name two of them.
func (s *Store) planRevision(q *QuizSession, id string, solo bool, now time.Time) error {
	if _, err := os.Stat(filepath.Join(s.Root, id)); err != nil || !s.revisable(id) {
		return fmt.Errorf("%w: no such note %q", ErrRevision, id)
	}
	q.Subject = strings.SplitN(id, "/", 2)[0]
	q.topics, q.planned, q.got = map[string]string{}, map[string]int{}, map[string]*[2]float64{}
	title := s.noteTitle(id)
	q.topics[id], q.torder = title, []string{id}
	bank := func(nid string) []*quizItem {
		var out []*quizItem
		for _, qq := range loadRevisionBank(s.QuestionsPath(nid)) {
			out = append(out, &quizItem{Key: qq.Q, Q: qq, RevIDs: []string{nid}})
		}
		return out
	}
	own := bank(id)
	if len(own) == 0 {
		q.recall, q.Title = id, "Revision: "+title+" (recall)"
		q.planned[id], q.got[id] = 1, &[2]float64{}
		return nil
	}
	st := s.loadQuizStats(q.Subject)
	banks := map[string][]*quizItem{id: own}
	if !solo {
		today := now.Format(isoDate)
		var due []Topic
		for _, t := range s.Topics() {
			if t.ID != id && t.Subject == q.Subject && t.Due <= today {
				if _, err := os.Stat(s.QuestionsPath(t.ID)); err == nil {
					due = append(due, t)
				}
			}
		}
		sort.SliceStable(due, func(i, j int) bool {
			if due[i].Due != due[j].Due {
				return due[i].Due < due[j].Due
			}
			if due[i].Learned != due[j].Learned {
				return due[i].Learned < due[j].Learned
			}
			return due[i].ID < due[j].ID
		})
		for _, t := range due {
			if len(q.torder) >= quizMixTopics {
				break
			}
			if b := bank(t.ID); len(b) > 0 {
				banks[t.ID] = b
				q.topics[t.ID] = t.Title
				q.torder = append(q.torder, t.ID)
			}
		}
	}
	var qs []*quizItem
	if len(q.torder) > 1 {
		for _, nid := range q.torder {
			qs = append(qs, sampleWeighted(banks[nid], st, quizMixPer, q.rnd)...)
		}
		var cross []*quizItem
		for _, qq := range loadQuizQuestions(s.MixedPath(q.Subject)) {
			var here []string
			for _, x := range strings.Split(strings.ReplaceAll(qq.Src, "[gen]", ""), ",") {
				if _, ok := q.topics[strings.TrimSpace(x)]; ok {
					here = append(here, strings.TrimSpace(x))
				}
			}
			if len(here) >= 2 {
				cross = append(cross, &quizItem{Key: qq.Q, Q: qq, RevIDs: here})
			}
		}
		q.rnd.Shuffle(len(cross), func(i, j int) { cross[i], cross[j] = cross[j], cross[i] })
		qs = append(qs, cross[:min(quizMixCross, len(cross))]...)
		var names []string
		for _, nid := range q.torder {
			names = append(names, q.topics[nid])
		}
		q.Title = "Revision (mixed): " + strings.Join(names, " · ")
	} else {
		qs = own
		q.Title = "Revision: " + title
	}
	interleave(qs, q.rnd)
	for _, it := range qs {
		for _, nid := range it.RevIDs {
			q.planned[nid]++
		}
	}
	for _, nid := range q.torder {
		q.got[nid] = &[2]float64{}
	}
	q.queue = qs
	return nil
}

// advance shows the next card, or finishes the session.
func (q *QuizSession) advance(s *Store, now time.Time) {
	q.cur = nil
	if q.done {
		return
	}
	if q.recall != "" {
		if q.answered > 0 {
			q.finish(s, now)
			return
		}
		b, _ := os.ReadFile(filepath.Join(s.Root, q.recall))
		var heads []string
		for _, l := range strings.Split(string(b), "\n") {
			if quizHeadLineRe.MatchString(l) && len(heads) < 12 {
				heads = append(heads, strings.TrimSpace(strings.TrimLeft(l, "#")))
			}
		}
		q.n = 1
		q.cur = &quizAsk{card: QuizCard{N: 1, Total: 1, Kind: "recall", Prompt: q.topics[q.recall], Headings: heads, Note: q.recall}, shown: now, cap: quizProblemCap}
		return
	}
	var it *quizItem
	switch {
	case q.Mode == "practice":
		st := s.loadQuizStats(q.Subject)
		it = pickQuiz(q.items, st, q.asked, q.n+1, q.last, q.rnd)
	case q.pos < len(q.queue):
		it = q.queue[q.pos]
		q.pos++
	case !q.inRetry && len(q.retry) > 0:
		q.inRetry, q.queue, q.pos = true, q.retry, 0
		it = q.queue[q.pos]
		q.pos++
	default:
		q.finish(s, now)
		return
	}
	q.n++
	q.asked[it.Key], q.last = q.n, it.Key
	q.cur = q.makeAsk(s, it, now)
}

func (q *QuizSession) makeAsk(s *Store, it *quizItem, now time.Time) *quizAsk {
	a := &quizAsk{item: it, shown: now, cap: quizIdleCap}
	c := QuizCard{N: q.n, Kind: it.kind(), Retry: q.inRetry}
	if q.Mode == "revise" {
		c.Total = len(q.queue)
		if q.inRetry {
			c.N = q.pos
		}
	}
	if sc := s.loadQuizStats(q.Subject).score(it.Key); sc >= 0 {
		c.Known = &sc
	}
	if it.Pair != nil {
		a.forward = q.dir == 0 || (q.dir == 2 && q.rnd.Float64() < 0.5)
		if a.forward {
			c.Prompt, a.answer, c.Hint = it.Pair.Word, it.Pair.Trans, "Latin → English"
		} else {
			c.Prompt, a.answer, c.Hint = it.Pair.Trans, it.Pair.Word, "English → Latin"
		}
		if !strings.HasPrefix(q.Subject, "lat") {
			c.Hint = map[bool]string{true: "word → meaning", false: "meaning → word"}[a.forward]
		}
	} else {
		qq := it.Q
		c.Prompt = qq.Q
		if q.Mode == "practice" && qq.Topic != "" {
			c.Hint = qq.Topic
		}
		if c.Kind == "problem" {
			a.cap = quizProblemCap
		}
		if c.Kind == "mc" || c.Kind == "multi" {
			a.order = make([]int, len(qq.Choices))
			for i := range a.order {
				a.order[i] = i
			}
			refers := false
			for _, ch := range qq.Choices {
				refers = refers || quizReferRe.MatchString(ch)
			}
			if !refers {
				q.rnd.Shuffle(len(a.order), func(i, j int) { a.order[i], a.order[j] = a.order[j], a.order[i] })
			}
			for _, o := range a.order {
				c.Choices = append(c.Choices, qq.Choices[o])
			}
		}
	}
	a.card = c
	return a
}

// QuizAnswer is what the page sends for the open card.
type QuizAnswer struct {
	Response string `json:"response"` // typed answer, or true / false
	Choices  []int  `json:"choices"`  // picked choice positions, as shown
	Reveal   bool   `json:"reveal"`   // a problem: show the solution
	Grade    string `json:"grade"`    // a problem: y, n or p; recall: 1-4
	Percent  int    `json:"percent"`  // a problem graded p
	Skip     bool   `json:"skip"`
}

func (s *Store) AnswerQuiz(q *QuizSession, a QuizAnswer, now time.Time) (*QuizResult, error) {
	q.touched = now
	ask := q.cur
	if ask == nil || q.pending != nil {
		return nil, fmt.Errorf("%w: no open question", ErrRevision)
	}
	kind := ask.card.Kind
	if kind == "problem" && a.Reveal && !ask.revealed {
		ask.revealed = true
		return &QuizResult{Solution: ask.item.Q.Solution, Why: ask.item.Q.Why, Src: ask.item.Q.Src, Pending: true}, nil
	}
	took := now.Sub(ask.shown)
	q.active += min(took, ask.cap)
	res := &QuizResult{}
	credit, overridable, guess := 0.0, false, ""
	skip := a.Skip || ((kind == "vocab" || kind == "short") && (strings.TrimSpace(a.Response) == "" || strings.TrimSpace(a.Response) == "?"))
	switch kind {
	case "recall":
		grades := map[string]float64{"1": 0.3, "2": 0.6, "3": 0.85, "4": 1}
		sc, ok := grades[a.Grade]
		if !ok {
			return nil, fmt.Errorf("%w: grade 1-4", ErrRevision)
		}
		q.answered, q.right = 1, sc
		q.got[q.recall] = &[2]float64{sc, 1}
		s.logQuizTime(q.Subject, int(math.Round((q.active - q.logged).Seconds())), 1, b2i(sc >= 1), 0, 0, now)
		q.logged = q.active
		res.Credit, res.Answer = sc, ""
		q.last1 = res
		q.cur = nil
		q.finish(s, now)
		return res, nil
	case "vocab":
		guess = strings.TrimSpace(a.Response)
		res.Answer = ask.answer
		if !skip && quizCheck(guess, ask.answer) {
			credit = 1
			res.Also = quizOthers(guess, ask.answer)
		}
		overridable = !skip
	case "short":
		guess = strings.TrimSpace(a.Response)
		res.Answer = ask.item.Q.A
		if !skip && quizCheck(guess, ask.item.Q.A) {
			credit = 1
		}
		overridable = !skip
	case "tf":
		r := strings.ToLower(strings.TrimSpace(a.Response))
		skip = skip || r == ""
		guess = r
		res.Answer = strings.ToLower(quizNorm(ask.item.Q.A))
		if !skip && (strings.HasPrefix(r, "t") == (res.Answer == "true")) {
			credit = 1
		}
		overridable = !skip
	case "mc", "multi":
		rights := []int{}
		for _, l := range quizAnswerLetters(ask.item.Q.A) {
			for pos, o := range ask.order {
				if o == int(l[0]-'a') {
					rights = append(rights, pos)
				}
			}
		}
		sort.Ints(rights)
		var lines, picked []string
		for _, r := range rights {
			lines = append(lines, fmt.Sprintf("%c) %s", 'a'+r, ask.card.Choices[r]))
		}
		res.Answer = strings.Join(lines, "\n")
		chosen := map[int]bool{}
		for _, c := range a.Choices {
			if c >= 0 && c < len(ask.order) {
				chosen[c] = true
			}
		}
		for c := range ask.card.Choices {
			if chosen[c] {
				picked = append(picked, ask.card.Choices[c])
			}
		}
		guess = strings.Join(picked, ", ")
		skip = skip || len(chosen) == 0
		if !skip {
			hits, extra := 0, 0
			for c := range chosen {
				found := false
				for _, r := range rights {
					found = found || r == c
				}
				if found {
					hits++
				} else {
					extra++
				}
			}
			if kind == "mc" {
				credit = float64(b2i(hits == 1 && extra == 0))
			} else {
				credit = math.Max(0, float64(hits-extra)/float64(len(rights)))
				if credit > 0 && credit < 1 {
					res.Partial = fmt.Sprintf("%d of %d right", hits, len(rights))
					if extra > 0 {
						res.Partial += fmt.Sprintf(", %d wrong pick%s", extra, map[bool]string{true: "", false: "s"}[extra == 1])
					}
				}
			}
		}
		overridable = !skip
	case "problem":
		res.Answer, res.Solution = ask.item.Q.Solution, ask.item.Q.Solution
		switch a.Grade {
		case "y":
			credit = 1
		case "n":
		case "p":
			if a.Percent <= 0 || a.Percent >= 100 {
				return nil, fmt.Errorf("%w: partly needs 1-99 %%", ErrRevision)
			}
			credit, guess = float64(a.Percent)/100, fmt.Sprintf("%d%% of it", a.Percent)
		default:
			if !skip {
				return nil, fmt.Errorf("%w: grade y, n or p", ErrRevision)
			}
		}
	}
	if ask.item.Q != nil {
		res.Why, res.Src = ask.item.Q.Why, ask.item.Q.Src
	}
	res.Skipped = skip
	if skip {
		credit = 0
	} else {
		res.Guess = guess
	}
	res.Credit = credit
	if credit < 1 && overridable {
		res.Override, res.Pending = true, true
		q.pending = &quizPending{took: took, item: ask.item, ask: ask, credit: credit, guess: guess, shown: guess, result: res}
		q.last1 = res
		q.cur = nil
		return res, nil
	}
	mistake := kind == "problem" && !skip && credit < 1
	s.commitQuiz(q, ask, credit, mistake, guess, res, took, now)
	q.last1 = res
	q.cur = nil
	return res, nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// OverrideQuiz: the pending answer was right after all; free text is saved as an accepted answer.
func (s *Store) OverrideQuiz(q *QuizSession, now time.Time) (*QuizResult, error) {
	q.touched = now
	p := q.pending
	if p == nil {
		return nil, fmt.Errorf("%w: nothing to override", ErrRevision)
	}
	q.pending = nil
	res := p.result
	res.Override, res.Pending, res.Overridden, res.Credit, res.Partial = false, false, true, 1, ""
	if g := strings.TrimSpace(p.guess); g != "" {
		switch {
		case p.item.Pair != nil && p.ask.forward:
			s.addMeaning(q.Subject, p.item.Pair.Word, g)
			p.item.Pair.Trans = mergedMeaning(p.item.Pair.Trans, g)
			res.Saved = g
		case p.item.Q != nil && quizKind(p.item.Q) == "short":
			addAnswer(p.item.Q, g)
			res.Saved = g
		}
	}
	s.commitQuiz(q, p.ask, 1, false, p.guess, res, p.took, now)
	return res, nil
}

func mergedMeaning(t, guess string) string {
	if m := quizTrailRe.FindStringSubmatchIndex(t); m != nil {
		return t[:m[0]] + ", " + guess + " " + t[m[2]:m[3]]
	}
	return t + ", " + guess
}

// NextQuiz confirms a pending answer (a confirmed mistake) and shows the next card.
func (s *Store) NextQuiz(q *QuizSession, now time.Time) {
	q.touched = now
	if p := q.pending; p != nil {
		q.pending = nil
		s.commitQuiz(q, p.ask, p.credit, true, p.shown, p.result, p.took, now)
	}
	if q.cur == nil {
		q.advance(s, now)
	}
}

// EndQuiz confirms what is pending and finishes (a revision records what was answered enough).
func (s *Store) EndQuiz(q *QuizSession, now time.Time) {
	q.touched = now
	if p := q.pending; p != nil {
		q.pending = nil
		s.commitQuiz(q, p.ask, p.credit, true, p.shown, p.result, p.took, now)
	}
	q.cur = nil
	q.finish(s, now)
}

// commitQuiz writes one answer: stats, XP, the time and XP log, the signal, the mistakes list.
func (s *Store) commitQuiz(q *QuizSession, ask *quizAsk, credit float64, mistake bool, guess string, res *QuizResult, took time.Duration, now time.Time) {
	it := ask.item
	st := s.loadQuizStats(q.Subject)
	pl := st.player()
	wasHard := st.struggling(it.Key)
	e := st.record(it.Key, credit, it.RevIDs, now.Format(isoDate))
	if mistake {
		e.Reask = true
		qtext := ask.card.Prompt
		s.logMistake(q.Subject, qtext, guess, ask.answerText(), it.RevIDs, now)
	}
	gain, oldLevel := 0, quizLevel(pl.XP)
	if !q.inRetry {
		switch {
		case credit >= 1:
			q.combo++
			gain = 10 * (1 + min(q.combo-1, 9)/3)
			if wasHard && !st.struggling(it.Key) {
				gain += 25
				res.Notes = append(res.Notes, "Weak one recovered! +25 XP")
				if m := pl.award("recovered"); m != "" {
					res.Notes = append(res.Notes, m)
				}
			}
			if m := pl.award("first_blood"); m != "" {
				res.Notes = append(res.Notes, m)
			}
			for _, n := range []int{5, 10, 20} {
				if q.combo == n {
					if m := pl.award(fmt.Sprintf("combo%d", n)); m != "" {
						res.Notes = append(res.Notes, m)
					}
				}
			}
			if pl.Days >= 7 {
				if m := pl.award("week"); m != "" {
					res.Notes = append(res.Notes, m)
				}
			}
		case credit > 0: // partial credit: XP in proportion, the combo neither grows nor breaks
			gain = max(1, int(math.Round(10*credit)))
		default:
			q.combo = 0
		}
		pl.XP += gain
		q.best = max(q.best, q.combo)
		pl.BestCombo = max(pl.BestCombo, q.combo)
	}
	lv := quizLevel(pl.XP) - oldLevel
	if lv > 0 {
		res.Notes = append(res.Notes, fmt.Sprintf("Level up! You are now a %s (level %d)", quizRank(quizLevel(pl.XP)), quizLevel(pl.XP)))
	}
	_ = s.saveQuizStats(q.Subject, st)
	res.XP, res.Combo, res.Right, res.Wrong = gain, q.combo, e.Right, e.Wrong
	if sc := st.score(it.Key); sc >= 0 {
		res.Known = &sc
	}
	if !q.inRetry {
		q.answered++
		q.right += credit
		q.xp += gain
		q.levels += lv
		for _, nid := range it.RevIDs {
			if g := q.got[nid]; g != nil {
				g[0] += credit
				g[1]++
			}
		}
		if credit < 1 {
			lab := ask.card.Prompt
			if it.Pair != nil {
				lab = it.Pair.Word + " :: " + it.Pair.Trans
			}
			q.missed = append(q.missed, firstLine(lab))
			if q.Mode == "revise" {
				q.retry = append(q.retry, it)
			}
		}
	}
	secs := int(math.Round((q.active - q.logged).Seconds()))
	q.logged += time.Duration(secs) * time.Second
	s.logQuizTime(q.Subject, secs, b2i(!q.inRetry), b2i(credit >= 1 && !q.inRetry), gain, lv, now)
	rev := ""
	if len(it.RevIDs) > 0 {
		rev = it.RevIDs[0]
	}
	s.logQuizSignal(q.Subject, ask.card.Kind, took, credit, rev, now)
}

func (a *quizAsk) answerText() string {
	if a.item.Pair != nil {
		return a.answer
	}
	q := a.item.Q
	switch a.card.Kind {
	case "problem":
		return q.Solution
	case "mc", "multi":
		var out []string
		for _, l := range quizAnswerLetters(q.A) {
			if i := int(l[0] - 'a'); i < len(q.Choices) {
				out = append(out, q.Choices[i])
			}
		}
		return strings.Join(out, "; ")
	}
	return q.A
}

// finish records revisions and builds the summary.
func (q *QuizSession) finish(s *Store, now time.Time) {
	if q.done {
		return
	}
	q.done = true
	st := s.loadQuizStats(q.Subject)
	pl := st.player()
	sum := &QuizSummary{Answered: q.answered, Right: math.Round(q.right*10) / 10, XP: q.xp, BestCombo: q.best,
		Seconds: int(q.active.Seconds()), Missed: q.missed}
	if q.answered >= 10 && q.right >= float64(q.answered) {
		if m := pl.award("perfect"); m != "" {
			sum.Notes = append(sum.Notes, m)
		}
		_ = s.saveQuizStats(q.Subject, st)
	}
	if q.Mode == "revise" {
		for _, nid := range q.torder {
			g := q.got[nid]
			if g == nil || g[1] == 0 || g[1]*2 < float64(q.planned[nid]) {
				sum.Revisions = append(sum.Revisions, fmt.Sprintf("%s: stopped early, not recorded (still due)", q.topics[nid]))
				continue
			}
			score, total := g[0]/g[1], int(g[1])
			correct := int(math.Round(g[0]))
			if q.recall != "" {
				correct, total = int(math.Round(score*3)), 3
			}
			t, e, err := s.RevisionDone(nid, score, correct, total, now)
			if err != nil {
				sum.Revisions = append(sum.Revisions, fmt.Sprintf("%s: %v", q.topics[nid], err))
				continue
			}
			line := fmt.Sprintf("%s: %d%%, next revision %s", t.Title, int(math.Round(score*100)), t.Due)
			if e.Points {
				line += fmt.Sprintf(" (+%d)", scoreRevisionPts)
			}
			sum.Revisions = append(sum.Revisions, line)
		}
	}
	sum.PlayerXP, sum.Level = pl.XP, quizLevel(pl.XP)
	sum.Rank, sum.LevelLo, sum.LevelHi = quizRank(sum.Level), quizXPFor(sum.Level), quizXPFor(sum.Level+1)
	q.summary = sum
}

// State is what the page shows.
func (q *QuizSession) State() QuizState {
	st := QuizState{ID: q.ID, Mode: q.Mode, Title: q.Title, Subject: q.Subject, Latin: strings.HasPrefix(q.Subject, "lat"),
		Answered: q.answered, Right: math.Round(q.right*10) / 10, XP: q.xp, Combo: q.combo, Streak: q.streak, Done: q.done, Summary: q.summary}
	if q.cur != nil {
		c := q.cur.card
		st.Card = &c
	}
	if q.pending != nil {
		st.Result = q.pending.result
		c := q.pending.ask.card
		st.Last = &c
	}
	return st
}
