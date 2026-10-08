package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// The productivity score is worked out from the same data as the rest of the Activity
// view and recomputed on every request, which makes today's score live: it moves as tasks
// are ticked, written or quizzed. Edit the numbers in the block below to retune it; the
// breakdown's hover text on the Activity page is generated from them.
//
// A task is overdue at the end of a day when its due date is that day or earlier and it
// was still open then. Today, while the day is still running, only tasks due before today
// count, so adding or still working on a task due today never costs points; it counts
// against today once the day is over. The total never goes below zero.
//
// Done points depend on difficulty and on whether the task is workflow work (see isFocusTask).
var scoreDonePts = [4]int{ // tasks outside the workflow folder
	3,  // task with no difficulty
	3,  // difficulty 1 (easy)
	5,  // difficulty 2
	10, // difficulty 3 (hardest)
}

var scoreWorkflowPts = [4]int{ // tasks in the workflow folder
	1, // task with no difficulty
	1, // difficulty 1 (easy)
	2, // difficulty 2
	5, // difficulty 3 (hardest)
}

const (
	scoreCreatedPts   = 1   // points per task created
	scoreQuizPerMin   = 1.0 // points per minute of quiz time
	scoreWordsPer     = 20  // new words per point
	scoreOverduePts   = 10  // points lost per overdue task
	scoreXPPer        = 30  // quiz XP per point (a correct answer is 10 XP, up to 40 with a combo)
	scoreLevelPts     = 5   // points per quiz level gained
	scoreCheckinPts   = 5   // points per class checked in to (calendar.go)
	scoreReadPagesPer = 1   // pages read per point (reading.go)
	scoreRoutinePts   = 1   // points per morning routine item done (routine.go)
	scoreRoutineBonus = 5   // extra points for doing the whole routine
	scoreRevisionPts  = 5   // points per topic revised (revision.go), once per topic per day

	scoreRecordEvery = 10 * time.Second // how often the viewer records today's score for the graph
)

// scoreWorkflowCategory is the folder for work on the notes system itself. Tasks done in it
// (or tagged #workflow) earn scoreWorkflowPts; every other task, General included, is "focus"
// work and earns scoreDonePts.
const scoreWorkflowCategory = "workflow"

func isFocusTask(t Task) bool { return t.Category != scoreWorkflowCategory }

// ScoreHints is the text shown when hovering each part of the breakdown.
type ScoreHints struct {
	Done     string `json:"done"`
	Created  string `json:"created"`
	Study    string `json:"study"`
	Words    string `json:"words"`
	Overdue  string `json:"overdue"`
	XP       string `json:"xp"`
	Level    string `json:"level"`
	Checkin  string `json:"checkin"`
	Read     string `json:"read"`
	Routine  string `json:"routine"`
	Revision string `json:"revision"`
}

func scoreHints() ScoreHints {
	return ScoreHints{
		Done: fmt.Sprintf("%d each (difficulty 1: %d, 2: %d, 3: %d); #%s tasks %d (1: %d, 2: %d, 3: %d)",
			scoreDonePts[0], scoreDonePts[1], scoreDonePts[2], scoreDonePts[3], scoreWorkflowCategory,
			scoreWorkflowPts[0], scoreWorkflowPts[1], scoreWorkflowPts[2], scoreWorkflowPts[3]),
		Created:  fmt.Sprintf("%d each", scoreCreatedPts),
		Study:    fmt.Sprintf("%g a minute", scoreQuizPerMin),
		Words:    fmt.Sprintf("1 per %d words in tracked folders", scoreWordsPer),
		Overdue:  fmt.Sprintf("−%d each", scoreOverduePts),
		XP:       fmt.Sprintf("1 per %d quiz XP (correct answers, combos and recovered weak words earn XP)", scoreXPPer),
		Level:    fmt.Sprintf("%d each time you level up in a quiz", scoreLevelPts),
		Checkin:  fmt.Sprintf("%d per class checked in to", scoreCheckinPts),
		Revision: fmt.Sprintf("%d per topic revised (spaced repetition; once per topic a day)", scoreRevisionPts),
		Routine:  fmt.Sprintf("%d per morning routine item, +%d for the whole routine", scoreRoutinePts, scoreRoutineBonus),
		Read:     fmt.Sprintf("1 per %d page(s) logged on the Reading page or start page", scoreReadPagesPer),
	}
}

// Score is one day's score and the calculation behind it.
type Score struct {
	Total   int  `json:"total"`
	Live    bool `json:"live"` // today: still changing, tasks due today not overdue yet
	Done    int  `json:"done"`
	Created int  `json:"created"`
	Study   int  `json:"study"` // seconds of quiz time
	Words   int  `json:"words"` // new words in tracked folders
	Overdue int  `json:"overdue"`
	// points each part contributed (Overdue is zero or negative)
	DonePts    int `json:"done_pts"`
	CreatedPts int `json:"created_pts"`
	StudyPts   int `json:"study_pts"`
	WordsPts   int `json:"words_pts"`
	OverduePts int `json:"overdue_pts"`
	// quiz progress (quiz.py's XP and levels), see withQuiz
	QuizXP   int `json:"quiz_xp"`
	LevelUps int `json:"level_ups"`
	XPPts    int `json:"xp_pts"`
	LevelPts int `json:"level_pts"`
	// classes checked in to, see withCheckins
	Checkins   int `json:"checkins"`
	CheckinPts int `json:"checkin_pts"`
	// pages read, see withReading
	Pages    int `json:"pages"`
	PagesPts int `json:"pages_pts"`
	// morning routine, see withRoutine
	Routine         int  `json:"routine"`
	RoutineComplete bool `json:"routine_complete"`
	RoutinePts      int  `json:"routine_pts"`
	// topics revised, see withRevision
	Revisions   int `json:"revisions"`
	RevisionPts int `json:"revision_pts"`
}

// sum is the total of every part, floored at 0.
func (s Score) sum() int {
	return max(0, s.DonePts+s.CreatedPts+s.StudyPts+s.WordsPts+s.OverduePts+s.XPPts+s.LevelPts+s.CheckinPts+s.PagesPts+s.RoutinePts+s.RevisionPts)
}

// withQuiz adds the day's quiz XP and level-ups to a score and refreshes the total.
func (s Score) withQuiz(xp, levels int) Score {
	s.QuizXP, s.LevelUps = xp, levels
	s.XPPts, s.LevelPts = xp/scoreXPPer, levels*scoreLevelPts
	s.Total = s.sum()
	return s
}

// withCheckins adds the day's class check-ins to a score and refreshes the total.
func (s Score) withCheckins(n int) Score {
	s.Checkins, s.CheckinPts = n, n*scoreCheckinPts
	s.Total = s.sum()
	return s
}

// withReading adds the day's pages read to a score and refreshes the total.
func (s Score) withReading(pages int) Score {
	s.Pages, s.PagesPts = pages, max(0, pages)/scoreReadPagesPer
	s.Total = s.sum()
	return s
}

// withRoutine adds the day's morning routine (items done, completed or not) and refreshes the total.
func (s Score) withRoutine(items int, complete bool) Score {
	s.Routine, s.RoutineComplete = items, complete
	s.RoutinePts = items * scoreRoutinePts
	if complete {
		s.RoutinePts += scoreRoutineBonus
	}
	s.Total = s.sum()
	return s
}

// withRevision adds the day's revisions (topics revised) and refreshes the total.
func (s Score) withRevision(n int) Score {
	s.Revisions, s.RevisionPts = n, n*scoreRevisionPts
	s.Total = s.sum()
	return s
}

// scoreFor turns one day's counts into a score. doneBy counts the completed tasks by
// difficulty; focus counts the subset of them outside the workflow folder (see isFocusTask).
func scoreFor(doneBy, focus [4]int, created, studySecs, words, overdue int, live bool) Score {
	done, donePts := 0, 0
	for d, n := range doneBy {
		done += n
		donePts += (n-focus[d])*scoreWorkflowPts[d] + focus[d]*scoreDonePts[d]
	}
	s := Score{Live: live, Done: done, Created: created, Study: studySecs, Words: words, Overdue: overdue}
	s.DonePts = donePts
	s.CreatedPts = created * scoreCreatedPts
	s.StudyPts = int(math.Round(float64(studySecs) / 60 * scoreQuizPerMin))
	s.WordsPts = words / scoreWordsPer
	s.OverduePts = -overdue * scoreOverduePts
	s.Total = s.sum()
	return s
}

// overdueAtEndOf counts the tasks that were still open and past due when the given day ended.
// For a live day (today) tasks due that day are not overdue yet.
func overdueAtEndOf(tasks []Task, day string, now time.Time, carried map[string]bool, live bool) int {
	n := 0
	for _, t := range tasks {
		if t.Due == "" || t.Due > day || (live && t.Due == day) || t.State == StateMoved {
			continue
		}
		if t.State == StateDone && (t.DoneDate == "" || t.DoneDate <= day) {
			continue
		}
		if c := createdDate(t, now, carried); c != "" && c > day {
			continue // didn't exist yet
		}
		n++
	}
	return n
}

// carriedTasks finds the tasks that were carried over to a later daily note.
func carriedTasks(tasks []Task) map[string]bool {
	carried := map[string]bool{}
	for _, t := range tasks {
		if m := movedToRe.FindStringSubmatch(t.Text); m != nil && t.State == StateMoved {
			carried["daily/"+m[1]+".md\x00"+strings.TrimSpace(movedTailRe.ReplaceAllString(t.Text, ""))] = true
		}
	}
	return carried
}

// AddScores scores every day that has activity, plus today. Call it after AddStudy.
func (a *Activity) AddScores(tasks []Task, now time.Time) {
	carried := carriedTasks(tasks)
	a.Scores = map[string]Score{}
	days := map[string]bool{a.Today: true}
	for k := range a.Days {
		days[k] = true
	}
	for k := range days {
		if k > a.Today {
			continue // a task stamped in the future
		}
		d := a.Days[k]
		a.Scores[k] = scoreFor(d.DoneBy, d.DoneFocus, d.Created, d.Study, d.Words, overdueAtEndOf(tasks, k, now, carried, k == a.Today), k == a.Today).withQuiz(d.QuizXP, d.LevelUps).withCheckins(d.Checkins).withReading(d.Pages).withRoutine(d.Routine, d.RoutineComplete).withRevision(d.Revisions)
	}
}

// ScorePoint is today's score at one moment, for the line graph.
type ScorePoint struct {
	At    string `json:"at"` // local time, 2006-01-02T15:04:05
	Total int    `json:"total"`
}

const (
	scoreLog   = ".score_log.jsonl"
	scoreStamp = "2006-01-02T15:04:05"
)

var scoreLogMu sync.Mutex

// RecordScore appends today's score to the log when it differs from the last one
// recorded today (or is the day's first), and returns today's points so far. The log is
// what was shown at the time, under the rules of the time; it is never rewritten. Graphs
// use scoreCurve instead.
func (s *Store) RecordScore(now time.Time, total int) []ScorePoint {
	scoreLogMu.Lock()
	defer scoreLogMu.Unlock()
	path := filepath.Join(s.Root, scoreLog)
	day := now.Format(isoDate)
	points := []ScorePoint{}
	if f, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			var p struct {
				At    string `json:"at"`
				Score int    `json:"score"`
			}
			if json.Unmarshal(sc.Bytes(), &p) == nil && strings.HasPrefix(p.At, day) {
				points = append(points, ScorePoint{p.At, p.Score})
			}
		}
		f.Close()
	}
	if n := len(points); n == 0 || points[n-1].Total != total {
		at := now.Format(scoreStamp)
		line, _ := json.Marshal(map[string]any{"at": at, "score": total})
		if f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			f.Write(append(line, '\n'))
			f.Close()
			points = append(points, ScorePoint{at, total})
		}
	}
	return points
}

// ScoreEvent is one thing that moved a day's score, at the moment it happened. The curve is
// rebuilt from these under the current rules, so retuning the score redraws past days too.
// A source without a time (date-only ✅ stamps, tasks with no capture stamp) counts from 00:00.
type ScoreEvent struct {
	At       string `json:"at"`                   // local time, 2006-01-02T15:04:05
	Kind     string `json:"kind"`                 // done, made, quiz, words, checkin, read, routine, routine_done, revision
	Text     string `json:"text,omitempty"`       // done/made: task text
	Diff     int    `json:"difficulty,omitempty"` // done: 0 (none) to 3
	Workflow bool   `json:"workflow,omitempty"`   // done: task in the workflow folder (see isFocusTask)
	Seconds  int    `json:"seconds,omitempty"`    // quiz
	XP       int    `json:"xp,omitempty"`         // quiz
	Levels   int    `json:"levels,omitempty"`     // quiz
	Words    int    `json:"words,omitempty"`      // words
	Pages    int    `json:"pages,omitempty"`      // read (negative: a correction)
	Items    int    `json:"items,omitempty"`      // routine: +1 item done, -1 unticked
	Pts      int    `json:"pts"`                  // change in the total it caused (set by scoreCurve)
}

// dayClock places a timestamp on a day: at itself when it falls on that day, else at the
// day's start (no time, or logged before the day) or its last second (logged after it).
func dayClock(day, at string) string {
	switch {
	case len(at) >= 10 && at[:10] == day && len(at) >= 16:
		if len(at) == 16 {
			return at + ":00"
		}
		return at[:19]
	case len(at) >= 10 && at[:10] > day:
		return day + "T23:59:59"
	}
	return day + "T00:00:00"
}

// taskTimes are when a task was made and finished as stamps ("" = unknown / not done).
func taskTimes(t Task, now time.Time, carried map[string]bool) (made, done string) {
	if d := createdDate(t, now, carried); d != "" {
		made = d + "T00:00:00"
		if m := createdRe.FindStringSubmatch(t.Text); m != nil {
			made = d + "T" + m[2] + ":00"
		}
	}
	if t.State == StateDone && t.DoneDate != "" {
		done = dayClock(t.DoneDate, t.DoneDate+"T"+t.DoneTime)
	}
	return made, done
}

// readTimedLog reads a JSONL log into its raw lines; a missing or malformed log is just empty.
func readTimedLog(path string, each func([]byte)) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		each(sc.Bytes())
	}
}

// ScoreEvents collects every timed score event, by the day it scores on, sorted by time.
func (s *Store) ScoreEvents(tasks []Task, now time.Time) map[string][]ScoreEvent {
	out := map[string][]ScoreEvent{}
	add := func(day string, e ScoreEvent) {
		if _, err := time.Parse(isoDate, day); err == nil {
			out[day] = append(out[day], e)
		}
	}
	carried := carriedTasks(tasks)
	for _, t := range tasks {
		made, done := taskTimes(t, now, carried)
		_, _, text := splitDoneStamp(t.Display)
		if made != "" {
			add(made[:10], ScoreEvent{At: made, Kind: "made", Text: text})
		}
		if done != "" {
			add(done[:10], ScoreEvent{At: done, Kind: "done", Text: text, Diff: t.Difficulty, Workflow: !isFocusTask(t)})
		}
	}
	readTimedLog(filepath.Join(s.Root, quizLog), func(b []byte) {
		var e struct {
			Date, At            string
			Seconds, XP, Levels int
		}
		if json.Unmarshal(b, &e) == nil && (e.Seconds > 0 || e.XP > 0 || e.Levels > 0) {
			add(e.Date, ScoreEvent{At: dayClock(e.Date, e.At), Kind: "quiz", Seconds: max(e.Seconds, 0), XP: max(e.XP, 0), Levels: max(e.Levels, 0)})
		}
	})
	readTimedLog(filepath.Join(s.Root, wordsLog), func(b []byte) {
		var e struct {
			Date, At string
			Words    int
		}
		if json.Unmarshal(b, &e) == nil && e.Words > 0 {
			add(e.Date, ScoreEvent{At: dayClock(e.Date, e.At), Kind: "words", Words: e.Words})
		}
	})
	seen := map[string]bool{}
	for _, c := range s.readCheckins() {
		if k := checkinKey(c.Cal, c.UID, c.Start); !seen[k] {
			seen[k] = true
			add(c.Date, ScoreEvent{At: dayClock(c.Date, c.At), Kind: "checkin", Text: c.Title})
		}
	}
	for _, e := range s.ReadLog() {
		add(e.Date, ScoreEvent{At: dayClock(e.Date, e.At), Kind: "read", Text: e.Title, Pages: e.Pages})
	}
	days := map[string]*routineDay{}
	for _, e := range s.RoutineLog() {
		d := days[e.Date]
		if d == nil {
			d = &routineDay{done: map[string]bool{}}
			days[e.Date] = d
		}
		n, complete := len(d.done), d.complete
		d.apply(e)
		if dn := len(d.done) - n; dn != 0 {
			add(e.Date, ScoreEvent{At: dayClock(e.Date, e.At), Kind: "routine", Text: e.Item, Items: dn})
		}
		if d.complete && !complete {
			add(e.Date, ScoreEvent{At: dayClock(e.Date, e.At), Kind: "routine_done"})
		}
	}
	for _, e := range s.RevisionLog() {
		if e.Kind == "done" && e.Points {
			add(e.Date, ScoreEvent{At: dayClock(e.Date, e.At), Kind: "revision", Text: e.Title})
		}
	}
	for _, evs := range out {
		sort.SliceStable(evs, func(i, j int) bool { return evs[i].At < evs[j].At })
	}
	return out
}

// scoreCurve replays one day's events under the current rules: the total after each moment
// something changed (events, and overdue tasks being made or finished), from 00:00 to now
// for today. A finished day ends at 23:59:59 on its final score, where tasks due that day
// that are still open count as overdue. evs gets each event's Pts filled in.
func scoreCurve(tasks []Task, evs []ScoreEvent, day string, now time.Time) []ScorePoint {
	live := day == now.Format(isoDate)
	limit := day + "T23:59:59"
	if live {
		limit = now.Format(scoreStamp)
	}
	carried := carriedTasks(tasks)
	// tasks due before the day: overdue from when they exist until they are finished
	type span struct{ from, to string }
	var due []span
	times := map[string]bool{day + "T00:00:00": true}
	for _, t := range tasks {
		if t.Due == "" || t.Due >= day || t.State == StateMoved {
			continue
		}
		made, done := taskTimes(t, now, carried)
		if t.State == StateDone && done == "" {
			continue // ticked without a stamp: done long ago
		}
		if made != "" && made[:10] > day || done != "" && done[:10] < day {
			continue
		}
		sp := span{from: day + "T00:00:00", to: "~"}
		if made != "" && made[:10] == day {
			sp.from = made
		}
		if done != "" && done[:10] == day {
			sp.to = done
		}
		due = append(due, sp)
		times[sp.from], times[sp.to] = true, true
	}
	for _, e := range evs {
		times[e.At] = true
	}
	at := make([]string, 0, len(times))
	for k := range times {
		if k <= limit {
			at = append(at, k)
		}
	}
	sort.Strings(at)

	var d DayStat
	var xp, levels, checkins, pages, routine int
	var routineDone bool
	var revisions int
	total := func(t string, endOfDay bool) int {
		over := 0
		for _, sp := range due {
			if sp.from <= t && t < sp.to {
				over++
			}
		}
		if endOfDay {
			over = overdueAtEndOf(tasks, day, now, carried, false)
		}
		return scoreFor(d.DoneBy, d.DoneFocus, d.Created, d.Study, d.Words, over, live).withQuiz(xp, levels).withCheckins(checkins).withReading(max(0, pages)).withRoutine(routine, routineDone).withRevision(revisions).Total
	}
	var pts []ScorePoint
	next, before := 0, total(at[0], false)
	for _, t := range at {
		// an event's Pts includes overdue tasks it ends (from the previous moment's total)
		for ; next < len(evs) && evs[next].At <= t; next++ {
			e := &evs[next]
			switch e.Kind {
			case "done":
				d.DoneBy[e.Diff]++
				if !e.Workflow {
					d.DoneFocus[e.Diff]++
				}
			case "made":
				d.Created++
			case "quiz":
				d.Study += e.Seconds
				xp += e.XP
				levels += e.Levels
			case "words":
				d.Words += e.Words
			case "checkin":
				checkins++
			case "read":
				pages += e.Pages
			case "routine":
				routine += e.Items
			case "routine_done":
				routineDone = true
			case "revision":
				revisions++
			}
			after := total(t, false)
			e.Pts, before = after-before, after
		}
		v := total(t, false)
		if len(pts) == 0 || pts[len(pts)-1].Total != v {
			pts = append(pts, ScorePoint{t, v})
		}
		before = v
	}
	if !live {
		if v := total(limit, true); pts[len(pts)-1].Total != v {
			pts = append(pts, ScorePoint{limit, v})
		}
	}
	return pts
}

// ScoreDay is one day's score curve and the events behind it, under the current rules.
type ScoreDay struct {
	Day    string       `json:"day"`
	Points []ScorePoint `json:"points"`
	Events []ScoreEvent `json:"events"`
}

// ScoreDay rebuilds one day's curve (GET /api/score/day, and the market's training data).
func (s *Store) ScoreDay(day string, now time.Time) ScoreDay {
	tasks := s.CollectTasks(TaskQuery{All: true, Now: now})
	evs := s.ScoreEvents(tasks, now)[day]
	return ScoreDay{Day: day, Points: scoreCurve(tasks, evs, day, now), Events: append([]ScoreEvent{}, evs...)}
}

// FullActivity is everything the Activity view shows, today's score recorded as a side effect.
func (s *Store) FullActivity(now time.Time, nWeeks int) Activity {
	tasks := s.CollectTasks(TaskQuery{All: true, Now: now})
	a := BuildActivity(tasks, now, nWeeks)
	a.AddStudy(s.StudySeconds(), now)
	a.AddQuizGains(s.StudyGains())
	s.RecordWords(now)
	a.AddWords(s.WordsPerDay(), now)
	a.AddCheckins(s.CheckinsPerDay())
	a.AddReading(s, now)
	a.AddRoutine(s, now)
	s.RecordRevision(now)
	a.AddRevision(s, now)
	s.RecordAttendance(now)
	a.Attendance = s.AttendanceSummary(now)
	a.AddScores(tasks, now)
	a.ScoreHints = scoreHints()
	// the log is a frozen record of what was shown; the graph is rebuilt from events
	s.RecordScore(now, a.Scores[a.Today].Total)
	a.ScoreLine = scoreCurve(tasks, s.ScoreEvents(tasks, now)[a.Today], a.Today, now)
	return a
}
