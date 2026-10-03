package main

import (
	"math"
	"strings"
	"time"
)

// The productivity score is out of 100 and is worked out from the same data as the
// rest of the Activity view, so nothing extra is stored. It is recomputed on every
// request, which makes today's score live: it moves as tasks are ticked, written
// or quizzed.
//
//	completed  10 points per task done that day        (at most 50)
//	created     2 points per task made that day        (at most 10)
//	quiz        1 point per minute of quiz time        (at most 40)
//	overdue    -5 points per task overdue at day's end (at most -30)
//
// A task is overdue at the end of a day when its due date is that day or earlier and
// it was still open then. For today that is a projection: the tasks that would count
// against you if the day ended now.
const (
	scoreDonePts    = 10
	scoreDoneCap    = 50
	scoreCreatedPts = 2
	scoreCreatedCap = 10
	scoreQuizPerMin = 1.0
	scoreQuizCap    = 40
	scoreOverduePts = 5
	scoreOverdueCap = 30
	scoreMax        = 100
)

// Score is one day's score and the calculation behind it.
type Score struct {
	Total   int  `json:"total"` // 0-100
	Live    bool `json:"live"`  // today: still changing, overdue is a projection
	Done    int  `json:"done"`
	Created int  `json:"created"`
	Study   int  `json:"study"` // seconds of quiz time
	Overdue int  `json:"overdue"`
	// points each part contributed (Overdue is zero or negative)
	DonePts    int `json:"done_pts"`
	CreatedPts int `json:"created_pts"`
	StudyPts   int `json:"study_pts"`
	OverduePts int `json:"overdue_pts"`
}

func capInt(v, limit int) int {
	if v > limit {
		return limit
	}
	return v
}

// scoreFor turns one day's counts into a score.
func scoreFor(done, created, studySecs, overdue int, live bool) Score {
	s := Score{Live: live, Done: done, Created: created, Study: studySecs, Overdue: overdue}
	s.DonePts = capInt(done*scoreDonePts, scoreDoneCap)
	s.CreatedPts = capInt(created*scoreCreatedPts, scoreCreatedCap)
	s.StudyPts = capInt(int(math.Round(float64(studySecs)/60*scoreQuizPerMin)), scoreQuizCap)
	s.OverduePts = -capInt(overdue*scoreOverduePts, scoreOverdueCap)
	total := s.DonePts + s.CreatedPts + s.StudyPts + s.OverduePts
	s.Total = max(0, min(scoreMax, total))
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
		a.Scores[k] = scoreFor(d.Done, d.Created, d.Study, overdueAtEndOf(tasks, k, now, carried), k == a.Today)
	}
}
