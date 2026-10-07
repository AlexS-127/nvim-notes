package main

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The feature store: one versioned file per day, .features/YYYY-MM-DD.jsonl.gz, rebuilt from the
// raw .signals records, the importers, the labels and the score events. It is what the score
// market's traders will train and run on (one code path for both), and what the Data tab shows.
//
//   line 1   {"type":"day", ...}    day-level context (inputs) and labels (targets)
//   lines 2… {"type":"min","m":i,…} one row per minute of the local day (1380/1440/1500 on DST days)
//
// Minute rows hold raw values, a mask per sensor ("on", "off", "denied", "absent": never read a
// missing sensor as zero), person-relative z-scores of the main numeric channels (median/IQR of
// the previous 28 days, past only), points per score source, known-future inputs (classes ahead,
// work due today) and the inferred focus (focus.go). A row uses only records stamped before the
// end of its minute (causality, tested); day-level context uses only earlier days, except the
// morning forecast, which carries the time it was made ("forecast_at"). Multi-scale views
// (5/15/60-minute) are computed on load with Rolling, so they are not stored three times.

const (
	featuresDir   = ".features"
	featureSchema = 1
)

// sensorNames are the raw sources that get a mask, in display order.
var sensorNames = []string{"sys", "nvim", "quiz", "input", "apps", "window", "browser", "screen", "camera", "mic", "place", "media", "pmset", "screentime", "messages", "weather", "health"}

// zChannels get a person-relative z-score next to the raw value.
var zChannels = []string{"keys", "clicks", "scroll", "idle", "switches", "nvim_keys", "nvim_bs", "db", "perclos", "hr", "hrv", "quiz_latency", "score", "focus"}

// categoryWork is how much a category counts as focused work (focus.go and the Data tab).
var categoryWork = map[string]float64{"study": 1, "code": 1, "problem": 1, "writing": 1, "reading": 0.9, "admin": 0.5, "comms": 0.3, "other": 0.4, "unknown": 0.5,
	"news": 0.1, "shopping": 0, "social": 0, "entertainment": 0, "none": 0.4, "browser": 0.5}

// Row is one minute: numbers in V, categories in C, sensor masks in Mask, z-scores in Z.
type Row struct {
	M    int                `json:"m"`
	V    map[string]float64 `json:"v"`
	C    map[string]string  `json:"c,omitempty"`
	Mask map[string]string  `json:"mask"`
	Z    map[string]float64 `json:"z,omitempty"`
}

// DayFeatures is a day's file.
type DayFeatures struct {
	Type    string         `json:"type"` // "day"
	Schema  int            `json:"schema"`
	Day     string         `json:"day"`
	Built   string         `json:"built"`
	Minutes int            `json:"minutes"`
	Inputs  map[string]any `json:"inputs"` // day-level context known at (or before) the start of the day
	Labels  map[string]any `json:"labels"` // targets: never an input for this day
	Cover   map[string]int `json:"coverage"` // minutes each sensor was on
	Rows    []Row          `json:"-"`
}

func (s *Store) featurePath(day string) string {
	return filepath.Join(s.Root, featuresDir, day+".jsonl.gz")
}

// num and str read record fields.
func num(r map[string]any, k string) (float64, bool) {
	switch v := r[k].(type) {
	case float64:
		return v, true
	case bool:
		if v {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

func str(r map[string]any, k string) string { v, _ := r[k].(string); return v }

// recSensor is the sensor a record belongs to ("" for none).
func recSensor(r map[string]any) string {
	switch str(r, "src") {
	case "sys", "nvim", "quiz":
		return str(r, "src")
	case "task":
		return ""
	case "sense":
		return str(r, "sensor")
	case "import":
		switch str(r, "kind") {
		case "power":
			return "pmset"
		case "state":
			return str(r, "importer")
		default:
			return str(r, "kind")
		}
	}
	return ""
}

// dayBounds is the local day's start and its length in minutes (DST days are 23 or 25 hours).
func dayBounds(day string) (time.Time, int, error) {
	start, err := time.ParseInLocation(isoDate, day, time.Local)
	if err != nil {
		return time.Time{}, 0, err
	}
	end := time.Date(start.Year(), start.Month(), start.Day()+1, 0, 0, 0, 0, time.Local)
	return start, int(end.Sub(start).Minutes()), nil
}

// minuteOf is a stamp's minute index within the day, -1 when outside.
func minuteOf(start time.Time, n int, at string) int {
	t, err := time.ParseInLocation(scoreStamp, at, time.Local)
	if err != nil {
		return -1
	}
	m := int(t.Sub(start).Minutes())
	if m < 0 || m >= n {
		return -1
	}
	return m
}

var examRe = regexp.MustCompile(`(?i)\b(midterm|exam|final|test)\b`)

var courseFolderRe = regexp.MustCompile(`^[a-z]{2,5}\d{3}$`)

// folderCategory is the activity category of editing a note in a folder: course folders (a code
// like act200, or one revision watches) are study, workflow is admin, daily/inbox/reviews other.
func folderCategory(folder string, study map[string]bool) string {
	switch folder {
	case "workflow":
		return "admin"
	case "daily", "inbox", "inbox.md", "reviews", "format":
		return "other"
	}
	if courseFolderRe.MatchString(folder) || study[folder] {
		return "study"
	}
	return ""
}

func isTerminal(app string) bool {
	a := strings.ToLower(app)
	return strings.Contains(a, "ghostty") || a == "terminal" || strings.Contains(a, "com.apple.terminal") || strings.Contains(a, "iterm")
}

// BuildFeatures builds one day's features. Records stamped after `until` are ignored (a live,
// partial day; and the causality test).
func (s *Store) BuildFeatures(day string, now, until time.Time) (DayFeatures, error) {
	start, n, err := dayBounds(day)
	if err != nil {
		return DayFeatures{}, err
	}
	f := DayFeatures{Type: "day", Schema: featureSchema, Day: day, Built: now.Format(scoreStamp), Minutes: n,
		Inputs: map[string]any{}, Labels: map[string]any{}, Cover: map[string]int{}}
	rows := make([]Row, n)
	for i := range rows {
		rows[i] = Row{M: i, V: map[string]float64{}, C: map[string]string{}, Mask: map[string]string{}}
	}
	limit := until.Format(scoreStamp)

	// raw records of the day (and the previous day's last states), deduped
	recs := s.ReadSignals(day)
	rules := s.loadRules()
	seen := map[string]bool{}
	var clean []map[string]any
	for _, r := range recs {
		b, _ := json.Marshal(r)
		if seen[string(b)] || str(r, "at") > limit {
			continue
		}
		seen[string(b)] = true
		clean = append(clean, r)
	}
	sort.SliceStable(clean, func(i, j int) bool { return str(clean[i], "at") < str(clean[j], "at") })

	// sensor state: last explicit state (from earlier days too) and last reading minute
	state := map[string]string{}
	for d := 1; d <= 3; d++ { // carry states from up to 3 days back
		for _, r := range s.ReadSignals(addDays(day, -d)) {
			if sn, st := recSensor(r), str(r, "state"); sn != "" && st != "" {
				if _, ok := state[sn]; !ok {
					state[sn] = st
				}
			}
		}
	}
	lastSeen := map[string]int{}
	// Neovim's note folder decides the category of terminal minutes (see folderCategory)
	studyFolders := map[string]bool{}
	for _, fo := range s.RevisionSettings(now).Folders {
		studyFolders[fo] = true
	}
	lastFolder, lastFolderM := "", -1000
	add := func(m int, k string, v float64) { rows[m].V[k] += v }
	set := func(m int, k string, v float64) { rows[m].V[k] = v }
	ri := 0
	for m := 0; m < n; m++ {
		mEnd := start.Add(time.Duration(m+1) * time.Minute).Format(scoreStamp)
		for ; ri < len(clean) && str(clean[ri], "at") < mEnd; ri++ {
			r := clean[ri]
			if minuteOf(start, n, str(r, "at")) < 0 {
				continue
			}
			sn := recSensor(r)
			if st := str(r, "state"); st != "" && sn != "" {
				state[sn] = st
				continue
			}
			if sn != "" {
				lastSeen[sn] = m
			}
			switch str(r, "src") {
			case "sys":
				if v, ok := num(r, "idle"); ok {
					set(m, "sys_idle", v)
				}
				if a := str(r, "app"); a != "" {
					rows[m].C["sys_app_cat"] = bundleCategory(a)
					if isTerminal(a) {
						rows[m].V["terminal_front"] = 1
					}
				}
			case "nvim":
				switch str(r, "ev") {
				case "hb":
					for src, dst := range map[string]string{"keys": "nvim_keys", "bs": "nvim_bs", "ins": "nvim_ins", "bursts": "nvim_bursts", "pause": "nvim_pause"} {
						if v, ok := num(r, src); ok {
							add(m, dst, v)
						}
					}
					if v, ok := num(r, "max_burst"); ok {
						rows[m].V["nvim_max_burst"] = math.Max(rows[m].V["nvim_max_burst"], v)
					}
					if fo := str(r, "folder"); fo != "" {
						rows[m].C["nvim_folder"] = fo
					}
				case "buf":
					add(m, "nvim_buf_switches", 1)
				case "start_enter":
					add(m, "start_page", 1)
				case "claude":
					add(m, "claude", 1)
				}
			case "quiz":
				add(m, "quiz_answers", 1)
				if c, _ := num(r, "correct"); c > 0 {
					add(m, "quiz_correct", 1)
				}
				if v, ok := num(r, "latency_ms"); ok {
					add(m, "quiz_latency_sum", v/1000)
				}
				if v, ok := num(r, "conf"); ok {
					add(m, "quiz_conf_sum", v)
				}
			case "task":
				switch str(r, "ev") {
				case "capture":
					add(m, "task_captured", 1)
				case "done":
					d, _ := num(r, "diff")
					add(m, "task_done", 1)
					add(m, "task_done_diff", d)
				case "undone":
					add(m, "task_undone", 1)
				}
			case "sense":
				switch sn {
				case "input":
					for _, k := range []string{"keys", "clicks", "scroll"} {
						if v, ok := num(r, k); ok {
							add(m, k, v)
						}
					}
					if v, ok := num(r, "idle"); ok {
						set(m, "idle", v)
					}
				case "apps":
					for _, k := range []string{"switches"} {
						if v, ok := num(r, k); ok {
							add(m, k, v)
						}
					}
					for _, k := range []string{"locked", "display_sleep", "displays", "battery"} {
						if v, ok := num(r, k); ok {
							set(m, k, v)
						}
					}
					set(m, "on_ac", map[bool]float64{true: 1, false: 0}[str(r, "power") == "ac"])
					if a := str(r, "bundle"); a != "" {
						rows[m].C["app_cat"] = bundleCategory(a)
						if isTerminal(a) {
							rows[m].V["terminal_front"] = 1
						}
					}
				case "window":
					rows[m].C["window_cat"] = labelled(rules.Titles, str(r, "title_hash"), str(r, "cat"))
				case "browser":
					rows[m].C["browser_cat"] = labelled(rules.Domains, str(r, "domain_hash"), str(r, "cat"))
				case "screen":
					rows[m].C["screen_class"] = str(r, "class")
					if v, ok := num(r, "conf"); ok {
						set(m, "screen_conf", v)
					}
				case "camera":
					for _, k := range []string{"present", "facing", "perclos", "yawn"} {
						if v, ok := num(r, k); ok {
							set(m, k, v)
						}
					}
				case "mic":
					for _, k := range []string{"db", "speech"} {
						if v, ok := num(r, k); ok {
							set(m, k, v)
						}
					}
				case "place":
					rows[m].C["place"] = rules.placeOf(r)
				case "media":
					if v, ok := num(r, "playing"); ok {
						set(m, "music", v)
					}
				}
			case "import":
				switch str(r, "kind") {
				case "power":
					add(m, "power_"+str(r, "ev"), 1)
				case "screentime":
					if str(r, "stream") == "app/usage" {
						sec, _ := num(r, "sec")
						key := "mac_use_min"
						if str(r, "device") != "mac" {
							key = "phone_use_min"
						}
						// spread the interval over the minutes it covers
						for k := 0; k <= int(sec)/60 && m+k < n; k++ {
							rows[m+k].V[key] = math.Min(1, rows[m+k].V[key]+math.Min(1, sec/60-float64(k)))
						}
					}
				case "messages":
					v, _ := num(r, "n")
					for k := 0; k < 60 && m+k < n; k++ {
						rows[m+k].V["messages_hour"] += v
					}
				case "health":
					met := str(r, "metric")
					for _, k := range []string{"avg", "qty"} {
						if v, ok := num(r, k); ok && met != "sleep" && met != "workout" {
							set(m, met, v)
							break
						}
					}
				}
			}
		}
		// masks: on when the sensor reported in the last 2 minutes, else its last state
		for _, sn := range sensorNames {
			mk := state[sn]
			if lm, ok := lastSeen[sn]; ok && m-lm <= 2 {
				mk = "on"
			} else if mk == "" || mk == "on" {
				if sn == "weather" || sn == "health" || sn == "screentime" || sn == "messages" || sn == "pmset" {
					if mk == "" {
						mk = "absent"
					}
				} else {
					mk = map[bool]string{true: "absent", false: "off"}[mk == ""]
				}
			}
			rows[m].Mask[sn] = mk
			if mk == "on" {
				f.Cover[sn]++
			}
		}
		// one activity category for the minute: in a terminal, the folder of the note Neovim is
		// editing (last 5 minutes); otherwise the most specific source wins
		if fo := rows[m].C["nvim_folder"]; fo != "" {
			lastFolder, lastFolderM = fo, m
		}
		if rows[m].V["terminal_front"] > 0 && lastFolder != "" && m-lastFolderM <= 5 {
			if c := folderCategory(lastFolder, studyFolders); c != "" {
				rows[m].C["nvim_cat"], rows[m].C["cat"] = c, c
			}
		}
		for _, k := range []string{"browser_cat", "window_cat", "screen_class", "app_cat", "sys_app_cat"} {
			if rows[m].C["cat"] != "" {
				break
			}
			if c := rows[m].C[k]; c != "" && c != "unknown" && c != "none" {
				if k == "browser_cat" && rows[m].C["app_cat"] != "browser" && rows[m].C["sys_app_cat"] != "browser" {
					continue // the browser sensor only speaks for minutes the browser was in front
				}
				rows[m].C["cat"] = c
				break
			}
		}
		if q := rows[m].V["quiz_answers"]; q > 0 {
			rows[m].V["quiz_latency"] = rows[m].V["quiz_latency_sum"] / q
		}
		delete(rows[m].V, "quiz_latency_sum")
	}

	// carry slow sensors forward: a reading holds until the next one (camera 15 s, screen 60 s...)
	carry := map[string]int{"present": 1, "facing": 1, "perclos": 1, "db": 1, "speech": 1, "screen_conf": 1, "music": 1, "battery": 15, "displays": 15, "on_ac": 15, "hr": 10, "hrv": 120}
	for k, hold := range carry {
		last, lastM := 0.0, -1000
		for m := 0; m < n; m++ {
			if v, ok := rows[m].V[k]; ok {
				last, lastM = v, m
			} else if m-lastM <= hold {
				rows[m].V[k] = last
			}
		}
	}
	for _, k := range []string{"screen_class", "place", "window_cat"} {
		last, lastM := "", -1000
		for m := 0; m < n; m++ {
			if v := rows[m].C[k]; v != "" {
				last, lastM = v, m
			} else if last != "" && m-lastM <= 2 {
				rows[m].C[k] = last
			}
		}
	}

	// score curve and points per source (score.go)
	tasks := s.CollectTasks(TaskQuery{All: true, Now: now})
	var evCopy []ScoreEvent
	for _, e := range s.ScoreEvents(tasks, now)[day] {
		if e.At <= limit { // causality: nothing after the cutoff
			evCopy = append(evCopy, e)
		}
	}
	curve := scoreCurve(tasks, evCopy, day, until)
	ci := 0
	cur := 0.0
	for m := 0; m < n; m++ {
		mEnd := start.Add(time.Duration(m+1) * time.Minute).Format(scoreStamp)
		for ; ci < len(curve) && curve[ci].At < mEnd; ci++ {
			cur = float64(curve[ci].Total)
		}
		if start.Add(time.Duration(m) * time.Minute).After(until) {
			break // a live day: no score for minutes that haven't happened
		}
		rows[m].V["score"] = cur
	}
	for _, e := range evCopy {
		if m := minuteOf(start, n, e.At); m >= 0 {
			rows[m].V["pts_"+e.Kind] += float64(e.Pts)
			rows[m].V["ev_"+e.Kind] = 1
		}
	}

	// known future inputs: classes ahead, open work due today, exams
	evts := s.Events(start.Add(-time.Hour), start.Add(time.Duration(n+180)*time.Minute), now)
	for m := 0; m < n; m++ {
		t := start.Add(time.Duration(m) * time.Minute)
		next := 600.0
		for _, e := range evts {
			if e.Exam && !e.AllDay && !t.Before(e.start) && t.Before(e.end) {
				rows[m].V["exam_now"] = 1
			}
			if !e.Class || e.AllDay {
				continue
			}
			for _, ahead := range []int{0, 30, 60, 120} {
				at := t.Add(time.Duration(ahead) * time.Minute)
				if !at.Before(e.start) && at.Before(e.end) {
					rows[m].V[fmt.Sprintf("class_in_%d", ahead)] = 1
				}
			}
			if d := e.start.Sub(t).Minutes(); d >= 0 && d < next {
				next = d
			}
		}
		rows[m].V["min_to_class"] = next
	}
	for _, t := range tasks {
		if t.Due != day || t.State == StateMoved {
			continue
		}
		doneAt := n
		if t.State == StateDone && t.DoneDate == day {
			if m := minuteOf(start, n, dayClock(day, t.DoneDate+"T"+t.DoneTime)); m >= 0 {
				doneAt = m
			}
		} else if t.State == StateDone {
			continue
		}
		for m := 0; m < doneAt; m++ {
			rows[m].V["due_today_open"]++
			rows[m].V["due_today_diff"] += float64(t.Difficulty)
		}
	}

	f.Rows = rows
	s.addFocus(&f) // focus.go
	s.addZScores(&f)
	s.dayContext(&f, tasks, evts, start, now)
	return f, nil
}

// dayContext fills the day-level inputs and labels.
func (s *Store) dayContext(f *DayFeatures, tasks []Task, evts []CalEvent, start, now time.Time) {
	day := f.Day
	in, lb := f.Inputs, f.Labels
	in["dow"] = int(start.Weekday()+6) % 7 // Monday = 0
	cfg := s.Sensors()
	if sem, err := time.ParseInLocation(isoDate, cfg.Semester, time.Local); err == nil && !start.Before(sem) {
		in["week_of_semester"] = int(start.Sub(sem).Hours()/24/7) + 1
	}
	// exams: calendar events or tasks naming an exam, the nearest one ahead per course
	exams := map[string]int{}
	note := func(course string, d int) {
		if v, ok := exams[course]; !ok || d < v {
			exams[course] = d
		}
	}
	// with an exam calendar, its events are the exams; without one, events whose title names one
	hasExamCal := false
	for _, c := range s.Calendars() {
		hasExamCal = hasExamCal || (c.Exam && c.Enabled)
	}
	for _, e := range s.Events(start, start.AddDate(0, 0, 60), now) {
		if (hasExamCal && e.Exam) || (!hasExamCal && examRe.MatchString(e.Title)) {
			note(strings.ToLower(strings.Fields(e.Title)[0]+courseDigits(e.Title)), int(e.start.Sub(start).Hours()/24))
		}
	}
	for _, t := range tasks {
		if t.State == StateOpen && t.Due >= day && examRe.MatchString(t.Display) {
			if d, err := time.ParseInLocation(isoDate, t.Due, time.Local); err == nil {
				c := t.Category
				if c == "" {
					c = "general"
				}
				note(c, int(d.Sub(start).Hours()/24))
			}
		}
	}
	if len(exams) > 0 {
		in["days_to_exam"] = exams
		minD := 999
		for _, d := range exams {
			minD = min(minD, d)
		}
		in["days_to_next_exam"] = minD
	}
	// the morning forecast (made during the day: forecast_at tells the market from when it is known)
	for _, e := range s.RoutineLog() {
		if e.Date == day && e.Kind == "forecast" && e.Rating > 0 {
			in["forecast"], in["forecast_at"] = e.Rating, e.At
		}
	}
	// earlier days: final scores, check-out, sleep, trends
	acts := s.FullActivityCached(now)
	finals := []float64{}
	for d := 1; d <= 7; d++ {
		if sc, ok := acts.Scores[addDays(day, -d)]; ok {
			finals = append(finals, float64(sc.Total))
		}
	}
	if len(finals) > 0 {
		in["score_7d_mean"] = mean(finals)
		if sc, ok := acts.Scores[addDays(day, -1)]; ok {
			in["yesterday_score"] = sc.Total
		}
	}
	for _, e := range s.Labels() {
		switch {
		case e.Kind == "checkout" && e.Date == addDays(day, -1):
			in["yesterday_checkout"] = map[string]int{"productivity": e.Productivity, "energy": e.Energy, "mood": e.Mood, "sleep": e.Sleep}
		case e.Kind == "checkout" && e.Date == day:
			lb["checkout"] = map[string]int{"productivity": e.Productivity, "energy": e.Energy, "mood": e.Mood, "sleep": e.Sleep}
		case e.Kind == "checkin" && e.Date == day:
			cs, _ := lb["checkins"].([]map[string]any)
			lb["checkins"] = append(cs, map[string]any{"at": e.Prompted, "focus": e.Focus})
		case e.Kind == "grade" && e.Date == day:
			gs, _ := lb["grades"].([]map[string]any)
			lb["grades"] = append(gs, map[string]any{"course": e.Course, "item": e.Item, "score": e.Score, "max": e.Max})
		}
	}
	if sw, src, ok := s.sleepSource(day); ok {
		in["sleep_h"], in["sleep_src"] = sw, src
	}
	if b, w, ok := s.SleepLog(day); ok { // clock hours of the wake day: 23:30 the evening before = -0.5
		mid := time.Date(w.Year(), w.Month(), w.Day(), 0, 0, 0, 0, time.Local)
		in["bed_hour"] = math.Round(b.Sub(mid).Hours()*100) / 100
		in["wake_hour"] = math.Round(w.Sub(mid).Hours()*100) / 100
	}
	var debt float64
	nights := 0
	for d := 0; d < 14; d++ {
		if h, ok := s.SleepWindow(addDays(day, -d)); ok {
			debt += math.Max(0, 8-h)
			nights++
		}
	}
	if nights > 0 {
		in["sleep_debt_14d"] = debt
	}
	// backlog: open tasks weighted by difficulty and how soon they are due
	var backlog float64
	for _, t := range tasks {
		if t.State != StateOpen {
			continue
		}
		w := 1.0
		if t.Due != "" {
			if d, err := time.ParseInLocation(isoDate, t.Due, time.Local); err == nil {
				days := d.Sub(start).Hours() / 24
				w = 1 + 2/math.Max(1, days+1)
			}
		}
		backlog += w * float64(max(1, t.Difficulty))
	}
	in["backlog"] = math.Round(backlog*10) / 10
	in["revision_due"] = len(s.RevisionDue(start.Add(12 * time.Hour)))
	// weather and health of the day (last record wins)
	for _, r := range s.ReadSignals(day) {
		if str(r, "src") != "import" {
			continue
		}
		switch str(r, "kind") {
		case "weather":
			w := map[string]any{}
			for k, v := range r {
				if k != "src" && k != "kind" && k != "at" && k != "day" {
					w[k] = v
				}
			}
			in["weather"] = w
		case "health":
			if str(r, "metric") == "rhr" {
				in["resting_hr"], _ = num(r, "qty")
			}
		}
	}
	// labels: the day's outcome
	if sc, ok := acts.Scores[day]; ok {
		lb["final_score"] = sc.Total
		lb["outcome_rating"] = OutcomeRating(acts.Scores, day, cfg.DataStart)
	}
	if testDay(day, cfg.DataStart) {
		lb["test_day"] = true // before the data start: don't train on it
	}
}

func courseDigits(s string) string {
	m := regexp.MustCompile(`\d{3}`).FindString(s)
	return m
}

func mean(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	t := 0.0
	for _, x := range v {
		t += x
	}
	return t / float64(len(v))
}

// ── your labels applied to past records ──
// The raw records keep what the helper decided at the time; the features use your labels as they
// are now, so labelling a window title, site, network or access point fills every earlier minute
// it was seen (all days, on the next rebuild; today's live views at once).

// labelled is your label for a hash, else the recorded category.
func labelled(rules map[string]string, hash, recorded string) string {
	if c := rules[hash]; c != "" && hash != "" && hash != "none" {
		return c
	}
	return recorded
}

// placeOf is a place record's place: your label for its access point, else for its network (not a
// shared one), else what was recorded.
func (r Rules) placeOf(rec map[string]any) string {
	if p := r.Places[str(rec, "ap_hash")]; p != "" {
		return p
	}
	net, recorded := str(rec, "net_hash"), str(rec, "place")
	if p := r.Places[net]; p != "" && !r.Shared[net] && (recorded == "unknown" || recorded == "") {
		return p
	}
	return recorded
}

// SleepWindow is the night before `day`, in hours: Apple Watch sleep (Health export: total sleep)
// when it exists, else the time in bed you logged (`notesview label sleep`, home, start page u).
// The Mac's power log never counts: it only says when the laptop was idle.
func (s *Store) SleepWindow(day string) (float64, bool) {
	h, _, ok := s.sleepSource(day)
	return h, ok
}

// sleepSource is SleepWindow plus where it came from: "watch" or "log".
func (s *Store) sleepSource(day string) (float64, string, bool) {
	for _, r := range s.ReadSignals(day) {
		if str(r, "kind") == "health" && str(r, "metric") == "sleep" {
			if v, ok := num(r, "totalsleep"); ok && v > 0 {
				return math.Round(v*10) / 10, "watch", true
			}
		}
	}
	if b, w, ok := s.SleepLog(day); ok {
		return math.Round(w.Sub(b).Hours()*10) / 10, "log", true
	}
	return 0, "", false
}

// FullActivityCached is the Activity data (scores per day), cached for a minute (building many days).
var actCache struct {
	at time.Time
	a  Activity
}

func (s *Store) FullActivityCached(now time.Time) Activity {
	if time.Since(actCache.at) < time.Minute && actCache.a.Scores != nil {
		return actCache.a
	}
	tasks := s.CollectTasks(TaskQuery{All: true, Now: now})
	a := BuildActivity(tasks, now, 1)
	a.AddStudy(s.StudySeconds(), now)
	a.AddQuizGains(s.StudyGains())
	a.AddWords(s.WordsPerDay(), now)
	a.AddCheckins(s.CheckinsPerDay())
	a.AddReading(s, now)
	a.AddRoutine(s, now)
	a.AddRevision(s, now)
	a.AddScores(tasks, now)
	actCache.at, actCache.a = time.Now(), a
	return a
}

// ── person-relative scaling ──

// baseline is the median and IQR of each z-channel over the previous 28 days' feature files
// (minutes where the value exists), so a value reads as "for you, today".
func (s *Store) baseline(day string) map[string][2]float64 {
	vals := map[string][]float64{}
	start := s.DataStart()
	for d := 1; d <= 28; d++ {
		if testDay(addDays(day, -d), start) {
			break // test days don't set what is normal for you
		}
		prev, err := s.LoadFeatures(addDays(day, -d))
		if err != nil {
			continue
		}
		for i, r := range prev.Rows {
			if i%3 != 0 { // every third minute is plenty for a median
				continue
			}
			for _, k := range zChannels {
				if v, ok := r.V[k]; ok {
					vals[k] = append(vals[k], v)
				}
			}
		}
	}
	out := map[string][2]float64{}
	for k, v := range vals {
		if len(v) < 60 {
			continue
		}
		sort.Float64s(v)
		q := func(p float64) float64 { return v[int(p*float64(len(v)-1))] }
		iqr := q(0.75) - q(0.25)
		if iqr <= 0 {
			iqr = math.Max(1e-6, math.Abs(q(0.5))*0.1+1e-3)
		}
		out[k] = [2]float64{q(0.5), iqr}
	}
	return out
}

func (s *Store) addZScores(f *DayFeatures) {
	b := s.baseline(f.Day)
	for i := range f.Rows {
		for _, k := range zChannels {
			bk, ok := b[k]
			v, has := f.Rows[i].V[k]
			if !ok || !has {
				continue
			}
			if f.Rows[i].Z == nil {
				f.Rows[i].Z = map[string]float64{}
			}
			f.Rows[i].Z[k] = math.Round((v-bk[0])/bk[1]*100) / 100
		}
	}
}

// Rolling is a channel aggregated over the previous `window` minutes (inclusive), "sum" or "mean"
// of the minutes where it exists: the 5/15/60-minute views the market computes on load.
func Rolling(rows []Row, channel string, window int, how string) []float64 {
	out := make([]float64, len(rows))
	sum, cnt := 0.0, 0
	for i := range rows {
		if v, ok := rows[i].V[channel]; ok {
			sum += v
			cnt++
		}
		if j := i - window; j >= 0 {
			if v, ok := rows[j].V[channel]; ok {
				sum -= v
				cnt--
			}
		}
		if how == "mean" && cnt > 0 {
			out[i] = sum / float64(cnt)
		} else if how == "sum" {
			out[i] = sum
		}
	}
	return out
}

// ── files ──

// SaveFeatures writes the day's file (gzip JSON lines).
func (s *Store) SaveFeatures(f DayFeatures) error {
	if err := os.MkdirAll(filepath.Join(s.Root, featuresDir), 0o755); err != nil {
		return err
	}
	tmp := s.featurePath(f.Day) + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(out)
	enc := json.NewEncoder(zw)
	if err := enc.Encode(f); err != nil {
		out.Close()
		return err
	}
	for _, r := range f.Rows {
		line := map[string]any{"type": "min", "m": r.M, "v": r.V, "mask": r.Mask}
		if len(r.C) > 0 {
			line["c"] = r.C
		}
		if len(r.Z) > 0 {
			line["z"] = r.Z
		}
		if err := enc.Encode(line); err != nil {
			out.Close()
			return err
		}
	}
	if err := zw.Close(); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, s.featurePath(f.Day))
}

// LoadFeatures reads a day's file.
func (s *Store) LoadFeatures(day string) (DayFeatures, error) {
	var f DayFeatures
	in, err := os.Open(s.featurePath(day))
	if err != nil {
		return f, err
	}
	defer in.Close()
	zr, err := gzip.NewReader(in)
	if err != nil {
		return f, err
	}
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 64*1024), 8<<20)
	first := true
	for sc.Scan() {
		if first {
			if err := json.Unmarshal(sc.Bytes(), &f); err != nil {
				return f, err
			}
			first = false
			continue
		}
		var r Row
		if json.Unmarshal(sc.Bytes(), &r) == nil {
			f.Rows = append(f.Rows, r)
		}
	}
	return f, sc.Err()
}

// RebuildFeatures builds and saves days: yesterday and any day with signals but no (or an older
// schema's) file, up to 60 days back. Returns the days written.
func (s *Store) RebuildFeatures(now time.Time, force ...string) []string {
	var days []string
	if len(force) > 0 {
		days = force
	} else {
		today := now.Format(isoDate)
		// labels given since a day was built change its categories and places (labelled, placeOf)
		labelsAt := ""
		if fi, err := os.Stat(s.signalPath(rulesFile)); err == nil {
			labelsAt = fi.ModTime().Format(scoreStamp)
		}
		for d := 1; d <= 60; d++ {
			day := addDays(today, -d)
			if _, err := os.Stat(s.signalPath(day + ".jsonl")); err != nil && d > 1 {
				continue
			}
			if f, err := s.LoadFeatures(day); err == nil && f.Schema == featureSchema && f.Built > day+"T23:59:59" && f.Built >= labelsAt {
				continue
			}
			days = append(days, day)
		}
		sort.Strings(days) // oldest first: each day's baseline uses the days before it
	}
	var done []string
	for _, day := range days {
		_, n, err := dayBounds(day)
		if err != nil {
			continue
		}
		start, _ := time.ParseInLocation(isoDate, day, time.Local)
		until := start.Add(time.Duration(n) * time.Minute).Add(-time.Second)
		if until.After(now) {
			until = now
		}
		f, err := s.BuildFeatures(day, now, until)
		if err == nil && s.SaveFeatures(f) == nil {
			done = append(done, day)
		}
	}
	return done
}

// featureLoop rebuilds missing or stale days (built before the day ended, or before your latest
// labels) at start and every night at 03:30.
func (s *Server) featureLoop() {
	time.Sleep(time.Minute)
	for {
		s.store.RebuildFeatures(time.Now())
		now := time.Now()
		next := time.Date(now.Year(), now.Month(), now.Day(), 3, 30, 0, 0, time.Local)
		if !next.After(now) {
			next = next.AddDate(0, 0, 1)
		}
		time.Sleep(time.Until(next))
	}
}
