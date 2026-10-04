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
// was still open then. For today that is a projection: the tasks that would count against
// you if the day ended now. The total never goes below zero.
var scoreDonePts = [4]int{
	3,  // task with no difficulty
	3,  // difficulty 1 (easy)
	6,  // difficulty 2
	10, // difficulty 3 (hardest)
}

const (
	scoreDoneCap    = 100 // most points tasks completed can give
	scoreCreatedPts = 1   // points per task created
	scoreCreatedCap = 60  // most points tasks created can give
	scoreQuizPerMin = 2.0 // points per minute of quiz time
	scoreQuizCap    = 100 // most points quiz time can give
	scoreWordsPer   = 15  // new words per point
	scoreWordsCap   = 100 // most points new words can give
	scoreOverduePts = 10  // points lost per overdue task
	scoreOverdueCap = 100 // most points overdue tasks can cost

	scoreRecordEvery = 10 * time.Second // how often the viewer records today's score for the graph
)

// ScoreHints is the text shown when hovering each part of the breakdown.
type ScoreHints struct {
	Done    string `json:"done"`
	Created string `json:"created"`
	Study   string `json:"study"`
	Words   string `json:"words"`
	Overdue string `json:"overdue"`
}

func scoreHints() ScoreHints {
	return ScoreHints{
		Done: fmt.Sprintf("%d each (difficulty 1: %d, 2: %d, 3: %d), up to %d",
			scoreDonePts[0], scoreDonePts[1], scoreDonePts[2], scoreDonePts[3], scoreDoneCap),
		Created: fmt.Sprintf("%d each, up to %d", scoreCreatedPts, scoreCreatedCap),
		Study:   fmt.Sprintf("%g a minute, up to %d", scoreQuizPerMin, scoreQuizCap),
		Words:   fmt.Sprintf("1 per %d words in tracked folders, up to %d", scoreWordsPer, scoreWordsCap),
		Overdue: fmt.Sprintf("−%d each, up to −%d", scoreOverduePts, scoreOverdueCap),
	}
}

// Score is one day's score and the calculation behind it.
type Score struct {
	Total   int  `json:"total"`
	Live    bool `json:"live"` // today: still changing, overdue is a projection
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
}

func capInt(v, limit int) int {
	if v > limit {
		return limit
	}
	return v
}

// scoreFor turns one day's counts into a score.
func scoreFor(doneBy [4]int, created, studySecs, words, overdue int, live bool) Score {
	done, donePts := 0, 0
	for d, n := range doneBy {
		done += n
		donePts += n * scoreDonePts[d]
	}
	s := Score{Live: live, Done: done, Created: created, Study: studySecs, Words: words, Overdue: overdue}
	s.DonePts = capInt(donePts, scoreDoneCap)
	s.CreatedPts = capInt(created*scoreCreatedPts, scoreCreatedCap)
	s.StudyPts = capInt(int(math.Round(float64(studySecs)/60*scoreQuizPerMin)), scoreQuizCap)
	s.WordsPts = capInt(words/scoreWordsPer, scoreWordsCap)
	s.OverduePts = -capInt(overdue*scoreOverduePts, scoreOverdueCap)
	total := s.DonePts + s.CreatedPts + s.StudyPts + s.WordsPts + s.OverduePts
	s.Total = max(0, total)
	return s
}

// overdueAtEndOf counts the tasks that were still open and past due when the given day ended.
func overdueAtEndOf(tasks []Task, day string, now time.Time, carried map[string]bool) int {
	n := 0
	for _, t := range tasks {
		if t.Due == "" || t.Due > day || t.State == StateMoved {
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
		a.Scores[k] = scoreFor(d.DoneBy, d.Created, d.Study, d.Words, overdueAtEndOf(tasks, k, now, carried), k == a.Today)
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
	s.RecordWords(now)
	a.AddWords(s.WordsPerDay(), now)
	a.AddScores(tasks, now)
	a.ScoreHints = scoreHints()
	a.ScoreLine = s.RecordScore(now, a.Scores[a.Today].Total)
	return a
}
