package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
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
var scoreDonePts = [4]int{
	3,  // task with no difficulty
	3,  // difficulty 1 (easy)
	6,  // difficulty 2
	10, // difficulty 3 (hardest)
}

const (
	scoreFocusMult  = 2   // done points are multiplied by this for tasks outside the workflow folder
	scoreCreatedPts = 1   // points per task created
	scoreQuizPerMin = 2.0 // points per minute of quiz time
	scoreWordsPer   = 20  // new words per point
	scoreOverduePts = 10  // points lost per overdue task
	scoreXPPer      = 10  // quiz XP per point (a correct answer is 10 XP, up to 40 with a combo)
	scoreLevelPts   = 5   // points per quiz level gained

	scoreRecordEvery = 10 * time.Second // how often the viewer records today's score for the graph
)

// scoreWorkflowCategory is the folder for work on the notes system itself. Tasks done in it
// (or tagged #workflow) earn plain points; every other task, General included, is "focus"
// work and earns scoreFocusMult times as much.
const scoreWorkflowCategory = "workflow"

func isFocusTask(t Task) bool { return t.Category != scoreWorkflowCategory }

// ScoreHints is the text shown when hovering each part of the breakdown.
type ScoreHints struct {
	Done    string `json:"done"`
	Created string `json:"created"`
	Study   string `json:"study"`
	Words   string `json:"words"`
	Overdue string `json:"overdue"`
	XP      string `json:"xp"`
	Level   string `json:"level"`
}

func scoreHints() ScoreHints {
	return ScoreHints{
		Done: fmt.Sprintf("%d each (difficulty 1: %d, 2: %d, 3: %d), x%d outside #%s",
			scoreDonePts[0], scoreDonePts[1], scoreDonePts[2], scoreDonePts[3],
			scoreFocusMult, scoreWorkflowCategory),
		Created: fmt.Sprintf("%d each", scoreCreatedPts),
		Study:   fmt.Sprintf("%g a minute", scoreQuizPerMin),
		Words:   fmt.Sprintf("1 per %d words in tracked folders", scoreWordsPer),
		Overdue: fmt.Sprintf("−%d each", scoreOverduePts),
		XP:      fmt.Sprintf("1 per %d quiz XP (correct answers, combos and recovered weak words earn XP)", scoreXPPer),
		Level:   fmt.Sprintf("%d each time you level up in a quiz", scoreLevelPts),
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
}

// sum is the total of every part, floored at 0.
func (s Score) sum() int {
	return max(0, s.DonePts+s.CreatedPts+s.StudyPts+s.WordsPts+s.OverduePts+s.XPPts+s.LevelPts)
}

// withQuiz adds the day's quiz XP and level-ups to a score and refreshes the total.
func (s Score) withQuiz(xp, levels int) Score {
	s.QuizXP, s.LevelUps = xp, levels
	s.XPPts, s.LevelPts = xp/scoreXPPer, levels*scoreLevelPts
	s.Total = s.sum()
	return s
}

// scoreFor turns one day's counts into a score. doneBy counts the completed tasks by
// difficulty; focus counts the subset of them outside the workflow folder (see isFocusTask).
func scoreFor(doneBy, focus [4]int, created, studySecs, words, overdue int, live bool) Score {
	done, donePts := 0, 0
	for d, n := range doneBy {
		done += n
		donePts += (n-focus[d])*scoreDonePts[d] + focus[d]*scoreDonePts[d]*scoreFocusMult
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
		a.Scores[k] = scoreFor(d.DoneBy, d.DoneFocus, d.Created, d.Study, d.Words, overdueAtEndOf(tasks, k, now, carried, k == a.Today), k == a.Today).withQuiz(d.QuizXP, d.LevelUps)
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
// recorded today (or is the day's first), and returns today's points so far.
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

// FullActivity is everything the Activity view shows, today's score recorded as a side effect.
func (s *Store) FullActivity(now time.Time, nWeeks int) Activity {
	tasks := s.CollectTasks(TaskQuery{All: true, Now: now})
	a := BuildActivity(tasks, now, nWeeks)
	a.AddStudy(s.StudySeconds(), now)
	a.AddQuizGains(s.StudyGains())
	s.RecordWords(now)
	a.AddWords(s.WordsPerDay(), now)
	a.AddScores(tasks, now)
	a.ScoreHints = scoreHints()
	a.ScoreLine = s.RecordScore(now, a.Scores[a.Today].Total)
	return a
}
