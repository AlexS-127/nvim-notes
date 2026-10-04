package main

import (
	"bufio"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Completed tasks carry a "✅ YYYY-MM-DD" stamp, added when the box is ticked
// (in the viewer or in Neovim) and removed when it is unticked. The activity
// view is built from those stamps plus each task's creation date.
var (
	doneStampRe = regexp.MustCompile(`\s*✅ (\d{4}-\d{2}-\d{2})`)
	createdRe   = regexp.MustCompile(`_\(([A-Z][a-z]{2} \d{2}) \d{2}:\d{2}\)_`)
	movedTailRe = regexp.MustCompile(`\s*→ \[\[[^\]]*\]\]\s*$`)
	movedToRe   = regexp.MustCompile(`→ \[\[([^\]]*)\]\]\s*$`)
	dailyRelRe  = regexp.MustCompile(`^daily/(\d{4}-\d{2}-\d{2})\.md$`)
)

// splitDoneStamp separates the completion stamp from a task's text.
func splitDoneStamp(text string) (date, rest string) {
	m := doneStampRe.FindStringSubmatch(text)
	if m == nil {
		return "", text
	}
	if _, err := time.Parse(isoDate, m[1]); err != nil {
		return "", text
	}
	return m[1], strings.TrimSpace(doneStampRe.ReplaceAllString(text, ""))
}

// stampDone appends the completion stamp to a task line, keeping its line ending.
func stampDone(line string, now time.Time) string {
	body := strings.TrimRight(line, "\r\n")
	end := line[len(body):]
	if doneStampRe.MatchString(body) {
		return line
	}
	return strings.TrimRight(body, " \t") + " ✅ " + now.Format(isoDate) + end
}

func unstampDone(line string) string {
	body := strings.TrimRight(line, "\r\n")
	return doneStampRe.ReplaceAllString(body, "") + line[len(body):]
}

// createdDate is when a task was made: its capture timestamp, else the date of
// the daily note it was written in. Tasks carried over from an earlier daily
// note are not new, and neither are moved ones.
func createdDate(t Task, now time.Time, carried map[string]bool) string {
	if t.State == StateMoved {
		return ""
	}
	if m := createdRe.FindStringSubmatch(t.Text); m != nil {
		d, err := time.ParseInLocation("Jan 02 2006", m[1]+" "+now.Format("2006"), now.Location())
		if err != nil {
			return ""
		}
		if d.After(now) {
			d = d.AddDate(-1, 0, 0)
		}
		return d.Format(isoDate)
	}
	if m := dailyRelRe.FindStringSubmatch(t.File); m != nil && !carried[t.File+"\x00"+strings.TrimSpace(t.Text)] {
		return m[1]
	}
	return ""
}

type DayStat struct {
	Done    int `json:"done"`
	Created int `json:"created"`
	Study   int `json:"study"` // seconds of quiz time
	Words   int `json:"words"` // new words written in tracked folders, see words.go
	// DoneBy counts completed tasks by difficulty: [0] has none set, [1]-[3] are !1-!3.
	DoneBy [4]int `json:"-"`
	// DoneFocus is the part of DoneBy outside the workflow folder, scored double (score.go).
	DoneFocus [4]int `json:"-"`
}

type WeekStat struct {
	Start      string  `json:"start"` // Monday
	Done       int     `json:"done"`
	Created    int     `json:"created"`
	Days       int     `json:"days"` // days counted: 7, or so far for the current week
	AvgDone    float64 `json:"avg_done"`
	AvgCreated float64 `json:"avg_created"`
	Total      int     `json:"total"` // running total of completed tasks at the end of the week
}

// WeekdayStat is the average for one day of the week (Monday first) over the weeks shown.
type WeekdayStat struct {
	Name       string  `json:"name"`
	Done       int     `json:"done"`
	Created    int     `json:"created"`
	Count      int     `json:"count"` // how many of this weekday fall in the range
	AvgDone    float64 `json:"avg_done"`
	AvgCreated float64 `json:"avg_created"`
}

// Distribution is the histogram of tasks done per day, one entry for every
// finished day since the first completion (zero days included). Today is left
// out until it is over, so the thresholds don't move while you work.
type Distribution struct {
	Counts   []int   `json:"counts"` // Counts[n] = number of days with n tasks done
	Days     int     `json:"days"`
	Median   float64 `json:"median"`
	Mean     float64 `json:"mean"`
	Sigma    float64 `json:"sigma"`
	Today    int     `json:"today"`
	ToMedian int     `json:"to_median"` // tasks still needed today to beat the median (0 = already did)
	ToSigma  int     `json:"to_sigma"`  // tasks still needed today to reach mean + 1 sigma
}

// RecentTask is a task completed today, for the Activity view's list.
type RecentTask struct {
	Text string `json:"text"`
	File string `json:"file"`
}

type Activity struct {
	Today        string             `json:"today"`
	Days         map[string]DayStat `json:"days"`
	TotalDone    int                `json:"total_done"`
	Earlier      int                `json:"earlier"` // done before stamps existed (no date)
	TotalCreated int                `json:"total_created"`
	Open         int                `json:"open"`
	Streak       int                `json:"streak"`
	BestStreak   int                `json:"best_streak"`
	Weeks        []WeekStat         `json:"weeks"`
	Weekdays     []WeekdayStat      `json:"weekdays"`
	Distribution Distribution       `json:"distribution"`
	Recent       []RecentTask       `json:"recent"`
	BestWeekday  int                `json:"best_weekday"` // index into Weekdays, -1 if nothing done yet
	StudyToday   int                `json:"study_today"`  // seconds of quiz time today
	StudyWeek    int                `json:"study_week"`   // seconds this week (Monday on)
	StudyTotal   int                `json:"study_total"`
	WordsToday   int                `json:"words_today"` // new words in tracked folders today
	WordsWeek    int                `json:"words_week"`
	WordsTotal   int                `json:"words_total"`
	Scores       map[string]Score   `json:"scores"`      // productivity score per day, see score.go
	ScoreLine    []ScorePoint       `json:"score_line"`  // today's score as it changed
	ScoreHints   ScoreHints         `json:"score_hints"` // hover text for the breakdown
}

// quizLog is where quiz.py appends one JSON line per session: {"date","seconds",...}.
const quizLog = ".quiz_log.jsonl"

// StudySeconds sums the quiz log by day. A missing or malformed log is just empty.
func (s *Store) StudySeconds() map[string]int {
	out := map[string]int{}
	f, err := os.Open(filepath.Join(s.Root, quizLog))
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e struct {
			Date    string `json:"date"`
			Seconds int    `json:"seconds"`
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Seconds <= 0 {
			continue
		}
		if _, err := time.Parse(isoDate, e.Date); err == nil {
			out[e.Date] += e.Seconds
		}
	}
	return out
}

// AddStudy folds per-day quiz seconds into an Activity built from tasks.
func (a *Activity) AddStudy(secs map[string]int, now time.Time) {
	week := weekStart(now)
	for k, n := range secs {
		d := a.Days[k]
		d.Study = n
		a.Days[k] = d
		a.StudyTotal += n
		if t, _ := time.ParseInLocation(isoDate, k, now.Location()); !t.Before(week) && !t.After(now) {
			a.StudyWeek += n
		}
	}
	a.StudyToday = secs[a.Today]
}

func weekStart(d time.Time) time.Time {
	d = midnight(d)
	return d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7))
}

// BuildActivity turns every task in the notes into the numbers the Activity view shows.
func BuildActivity(tasks []Task, now time.Time, nWeeks int) Activity {
	a := Activity{Today: now.Format(isoDate), Days: map[string]DayStat{}}
	// a task carried over to a later daily note is not new there: key = note + text
	carriedTo := carriedTasks(tasks)
	for _, t := range tasks {
		if t.State == StateOpen {
			a.Open++
		}
		if t.State == StateDone {
			if t.DoneDate == "" {
				a.Earlier++
			} else {
				d := a.Days[t.DoneDate]
				d.Done++
				d.DoneBy[t.Difficulty]++
				if isFocusTask(t) {
					d.DoneFocus[t.Difficulty]++
				}
				a.Days[t.DoneDate] = d
			}
		}
		if c := createdDate(t, now, carriedTo); c != "" {
			d := a.Days[c]
			d.Created++
			a.Days[c] = d
			a.TotalCreated++
		}
	}
	for _, t := range tasks {
		if t.State == StateDone && t.DoneDate == a.Today {
			_, text := splitDoneStamp(t.Display)
			text = strings.TrimSpace(createdRe.ReplaceAllString(text, ""))
			a.Recent = append(a.Recent, RecentTask{Text: text, File: t.File})
		}
	}
	for _, d := range a.Days {
		a.TotalDone += d.Done
	}
	a.TotalDone += a.Earlier

	// streaks: consecutive days with a completion; today may still be empty
	today := midnight(now)
	for d, run := today, 0; ; d = d.AddDate(0, 0, -1) {
		if a.Days[d.Format(isoDate)].Done > 0 {
			run++
			continue
		}
		if d.Equal(today) {
			continue
		}
		a.Streak = run
		break
	}
	run, prev := 0, time.Time{}
	for _, k := range sortedKeys(a.Days) {
		d, _ := time.ParseInLocation(isoDate, k, now.Location())
		if a.Days[k].Done == 0 {
			continue
		}
		if !prev.IsZero() && daysBetween(prev, d) == 1 {
			run++
		} else {
			run = 1
		}
		prev = d
		if run > a.BestStreak {
			a.BestStreak = run
		}
	}

	// weeks, oldest first, Monday to Sunday
	thisWeek := weekStart(now)
	running := a.Earlier
	first := thisWeek.AddDate(0, 0, -7*(nWeeks-1))
	for k, d := range a.Days { // everything before the first shown week
		if t, _ := time.ParseInLocation(isoDate, k, now.Location()); t.Before(first) {
			running += d.Done
		}
	}
	for i := 0; i < nWeeks; i++ {
		start := first.AddDate(0, 0, 7*i)
		w := WeekStat{Start: start.Format(isoDate), Days: 7}
		if start.Equal(thisWeek) {
			w.Days = daysBetween(start, today) + 1
		}
		for j := 0; j < w.Days; j++ {
			d := a.Days[start.AddDate(0, 0, j).Format(isoDate)]
			w.Done += d.Done
			w.Created += d.Created
		}
		w.AvgDone = float64(w.Done) / float64(w.Days)
		w.AvgCreated = float64(w.Created) / float64(w.Days)
		running += w.Done
		w.Total = running
		a.Weeks = append(a.Weeks, w)
	}

	// weekday profile over the same weeks, up to today
	names := []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}
	a.Weekdays = make([]WeekdayStat, 7)
	for i := range a.Weekdays {
		a.Weekdays[i].Name = names[i]
	}
	for d := first; !d.After(today); d = d.AddDate(0, 0, 1) {
		w := &a.Weekdays[(int(d.Weekday())+6)%7]
		st := a.Days[d.Format(isoDate)]
		w.Count++
		w.Done += st.Done
		w.Created += st.Created
	}
	a.BestWeekday = -1
	best := 0.0
	for i := range a.Weekdays {
		w := &a.Weekdays[i]
		if w.Count > 0 {
			w.AvgDone = float64(w.Done) / float64(w.Count)
			w.AvgCreated = float64(w.Created) / float64(w.Count)
		}
		if w.AvgDone > best {
			best, a.BestWeekday = w.AvgDone, i
		}
	}
	a.Distribution = buildDistribution(a.Days, now)
	return a
}

func buildDistribution(days map[string]DayStat, now time.Time) Distribution {
	today := midnight(now)
	dist := Distribution{Counts: []int{}, Today: days[today.Format(isoDate)].Done}
	var first time.Time
	for k, d := range days {
		if t, err := time.ParseInLocation(isoDate, k, now.Location()); err == nil && d.Done > 0 && (first.IsZero() || t.Before(first)) {
			first = t
		}
	}
	var vals []int
	if !first.IsZero() {
		for d := first; d.Before(today); d = d.AddDate(0, 0, 1) {
			vals = append(vals, days[d.Format(isoDate)].Done)
		}
	}
	dist.Days = len(vals)
	if len(vals) == 0 {
		return dist
	}
	sorted := append([]int(nil), vals...)
	sort.Ints(sorted)
	if n := len(sorted); n%2 == 1 {
		dist.Median = float64(sorted[n/2])
	} else {
		dist.Median = float64(sorted[n/2-1]+sorted[n/2]) / 2
	}
	sum := 0
	for _, v := range vals {
		sum += v
	}
	dist.Mean = float64(sum) / float64(len(vals))
	if len(vals) > 1 {
		ss := 0.0
		for _, v := range vals {
			ss += (float64(v) - dist.Mean) * (float64(v) - dist.Mean)
		}
		dist.Sigma = math.Sqrt(ss / float64(len(vals)-1))
	}
	dist.Counts = make([]int, sorted[len(sorted)-1]+1)
	for _, v := range vals {
		dist.Counts[v]++
	}
	if need := int(math.Floor(dist.Median)) + 1 - dist.Today; need > 0 {
		dist.ToMedian = need
	}
	if need := int(math.Ceil(dist.Mean+dist.Sigma-1e-9)) - dist.Today; need > 0 {
		dist.ToSigma = need
	}
	return dist
}

func sortedKeys(m map[string]DayStat) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ { // dates sort as strings; few enough that insertion sort is fine
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
