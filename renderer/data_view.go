package main

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The Data tab (web/data.js, #/data): everything the data layer collects, per sensor and per day,
// plus switches, settings, labels, the feature store, importance and the privacy audit.

// sensorInfo describes each sensor for the Data tab.
var sensorInfo = map[string][3]string{ // name → {title, what is stored, permission}
	"sys":        {"Server sampler", "front app name, idle seconds every 15 s", ""},
	"nvim":       {"Neovim", "keystroke counts, backspaces, insert time, typing bursts, note folder, buffer switches (hashed)", ""},
	"quiz":       {"Quiz answers", "per answer: latency, right/wrong, kind", ""},
	"input":      {"Keyboard & mouse", "system-wide key / click / scroll counts and idle", ""},
	"apps":       {"Apps & power", "front app, app switches, lock, display sleep, displays, power, battery", ""},
	"window":     {"Window titles", "category of the front window's title + a hash", "Accessibility"},
	"browser":    {"Browser", "category of the front tab's site + a hash", "Automation"},
	"screen":     {"Screen activity", "on-screen activity class from text recognition; image and text dropped", "Screen Recording"},
	"camera":     {"Camera", "at the desk, facing the screen, eye closure (PERCLOS), yawns; no frames", "Camera"},
	"mic":        {"Sound", "sound level and speech probability; no audio", "Microphone"},
	"place":      {"Place", "Wi-Fi network hash and your label for it", "Location"},
	"media":      {"Music", "playing or not", "Automation"},
	"pmset":      {"Sleep & wake", "lid, sleep, wake, display on/off (pmset)", ""},
	"screentime": {"Screen Time", "app usage minutes on the Mac and (shared) iPhone", "Full Disk Access"},
	"messages":   {"Message counts", "incoming Messages and Mail per hour; no content or senders", "Full Disk Access"},
	"weather":    {"Weather", "daily temperature, rain, daylight for your city", "network"},
	"health":     {"Health (Apple Watch)", "heart rate, HRV, sleep, resting HR, steps, workouts", "Health Auto Export folder"},
	"checkins":   {"Focus check-ins", "random 1-5 prompts while you're active", ""},
	"confidence": {"Quiz confidence", "ask how sure you were after each answer", ""},
	"previews":   {"Screen previews", "a picture of each unsure screen while it waits for your label (outside the notes, deleted on label/skip or after 24 h)", "Screen Recording"},
}

// permissionPanes open the right System Settings page.
var permissionPanes = map[string]string{
	"Accessibility":    "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility",
	"Automation":       "x-apple.systempreferences:com.apple.preference.security?Privacy_Automation",
	"Screen Recording": "x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture",
	"Camera":           "x-apple.systempreferences:com.apple.preference.security?Privacy_Camera",
	"Microphone":       "x-apple.systempreferences:com.apple.preference.security?Privacy_Microphone",
	"Location":         "x-apple.systempreferences:com.apple.preference.security?Privacy_LocationServices",
	"Full Disk Access": "x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles",
}

// coverage is minutes each sensor reported per day, from the raw records (a minute counts once).
func (s *Store) coverage(day string) (map[string]int, map[string]string) {
	mins := map[string]map[string]bool{}
	state := map[string]string{}
	for _, r := range s.ReadSignals(day) {
		sn := recSensor(r)
		if sn == "" {
			continue
		}
		if st := str(r, "state"); st != "" {
			state[sn] = st
			continue
		}
		if mins[sn] == nil {
			mins[sn] = map[string]bool{}
		}
		if at := str(r, "at"); len(at) >= 16 {
			mins[sn][at[:16]] = true
		}
	}
	out := map[string]int{}
	for k, v := range mins {
		out[k] = len(v)
	}
	return out, state
}

// SensorRow is one sensor on the Data tab.
type SensorRow struct {
	Name, Title, Stores, Permission, State string
	On                                     bool
	Days                                   []int // minutes per day, 14 days, oldest first
	Pane                                   string
}

// DataOverview is the Data tab's first screen.
func (s *Store) DataOverview(now time.Time) map[string]any {
	today := now.Format(isoDate)
	cfg := s.Sensors()
	imp := s.loadImportState()
	cov := map[string][]int{}
	lastState := map[string]string{}
	for d := 13; d >= 0; d-- {
		c, st := s.coverage(addDays(today, -d))
		for k, v := range st {
			lastState[k] = v
		}
		for _, n := range append(append([]string{}, sensorNames...), "checkins") {
			cov[n] = append(cov[n], c[n])
		}
	}
	for k, v := range imp.States {
		lastState[k] = v
	}
	var rows []SensorRow
	for _, n := range append(append([]string{}, sensorNames...), "checkins", "confidence", "previews") {
		info := sensorInfo[n]
		st := lastState[n]
		days := cov[n]
		if days == nil {
			days = make([]int, 14)
		}
		if st == "" {
			if len(days) > 0 && days[len(days)-1] > 0 {
				st = "on"
			} else if cfg.Sensors[n] {
				st = "waiting"
			} else {
				st = "off"
			}
		}
		if !cfg.Sensors[n] {
			st = "off"
		}
		rows = append(rows, SensorRow{Name: n, Title: info[0], Stores: info[1], Permission: info[2], State: st, On: cfg.Sensors[n], Days: days, Pane: permissionPanes[info[2]]})
	}
	h := HelperStatus()
	return map[string]any{"today": today, "sensors": rows, "config": map[string]any{"city": cfg.City, "lat": cfg.Lat, "lon": cfg.Lon, "health_dir": cfg.HealthDir, "semester_start": cfg.Semester, "data_start": cfg.DataStart},
		"helper": h, "dirs": map[string]string{"signals": s.signalPath(""), "features": filepath.Join(s.Root, featuresDir), "labels": s.labelPath("")}}
}

// DataToday is today's live timeline: per-minute strips the Data tab draws.
func (s *Store) DataToday(now time.Time, day string) (map[string]any, error) {
	start, n, err := dayBounds(day)
	if err != nil {
		return nil, err
	}
	until := start.Add(time.Duration(n)*time.Minute - time.Second)
	if until.After(now) {
		until = now
	}
	f, err := s.BuildFeatures(day, now, until)
	if err != nil {
		return nil, err
	}
	strip := func(key string, cat bool) []any {
		out := make([]any, len(f.Rows))
		for i, r := range f.Rows {
			if cat {
				if v := r.C[key]; v != "" {
					out[i] = v
				}
			} else if v, ok := r.V[key]; ok {
				out[i] = math.Round(v*100) / 100
			}
		}
		return out
	}
	active := make([]any, len(f.Rows))
	for i, r := range f.Rows {
		if minuteActive(r) {
			active[i] = 1
		} else if _, ok := r.V["idle"]; ok {
			active[i] = 0
		}
	}
	return map[string]any{"day": day, "minutes": n, "now": int(now.Sub(start).Minutes()),
		"strips": map[string]any{"score": strip("score", false), "focus": strip("focus", false), "active": active, "cat": strip("cat", true),
			"screen": strip("screen_class", true), "present": strip("present", false), "db": strip("db", false), "place": strip("place", true),
			"keys": strip("keys", false), "nvim_keys": strip("nvim_keys", false), "phone": strip("phone_use_min", false), "hr": strip("hr", false)},
		"cover": f.Cover, "inputs": f.Inputs, "labels": f.Labels}, nil
}

// DataDays is the day table and the forecast calibration, last 60 days.
func (s *Store) DataDays(now time.Time) map[string]any {
	acts := s.FullActivityCached(now)
	forecasts := map[string]int{}
	for _, e := range s.RoutineLog() {
		if e.Kind == "forecast" && e.Rating > 0 {
			forecasts[e.Date] = e.Rating
		}
	}
	checkouts := map[string]LabelEntry{}
	checkins := map[string][]int{}
	for _, e := range s.Labels() {
		switch e.Kind {
		case "checkout":
			checkouts[e.Date] = e
		case "checkin":
			checkins[e.Date] = append(checkins[e.Date], e.Focus)
		}
	}
	var grid [5][5]int // forecast × outcome
	var days []map[string]any
	today := now.Format(isoDate)
	start := s.DataStart()
	for d := 0; d < 60; d++ {
		day := addDays(today, -d)
		row := map[string]any{"day": day}
		if testDay(day, start) {
			row["test"] = true
		}
		has := false
		if sc, ok := acts.Scores[day]; ok {
			row["score"] = sc.Total
			has = true
		}
		if fc, ok := forecasts[day]; ok {
			row["forecast"] = fc
			has = true
			if o := OutcomeRating(acts.Scores, day, start); o > 0 && day != today {
				row["outcome"] = o
				grid[fc-1][o-1]++
			}
		}
		if co, ok := checkouts[day]; ok {
			row["checkout"] = map[string]int{"productivity": co.Productivity, "energy": co.Energy, "mood": co.Mood, "sleep": co.Sleep}
			has = true
		}
		if cs := checkins[day]; len(cs) > 0 {
			t := 0
			for _, v := range cs {
				t += v
			}
			row["checkin_mean"] = math.Round(float64(t)/float64(len(cs))*10) / 10
		}
		if h, src, ok := s.sleepSource(day); ok {
			row["sleep_h"], row["sleep_src"] = h, src
			if b, w, ok := s.SleepLog(day); ok && src == "log" {
				row["sleep_times"] = b.Format("15:04") + "–" + w.Format("15:04")
			}
			has = true
		}
		if f, err := s.LoadFeatures(day); err == nil {
			row["focus_min"] = f.Labels["focus_min"]
			var phone, mac float64
			for _, r := range f.Rows {
				phone += r.V["phone_use_min"]
				mac += r.V["mac_use_min"]
			}
			if phone > 0 {
				row["phone_min"] = int(phone)
			}
			if mac > 0 {
				row["mac_min"] = int(mac)
			}
			if w, ok := f.Inputs["weather"].(map[string]any); ok {
				row["temp"] = w["temperature_2m_max"]
			}
			has = true
		}
		if has {
			days = append(days, row)
		}
	}
	hit, total := 0, 0
	for i := 0; i < 5; i++ {
		for j := 0; j < 5; j++ {
			total += grid[i][j]
			if i == j {
				hit += grid[i][j]
			}
		}
	}
	return map[string]any{"days": days, "grid": grid, "hits": hit, "rated": total}
}

// DataFeatures describes the feature store.
func (s *Store) DataFeatures() map[string]any {
	files, _ := filepath.Glob(filepath.Join(s.Root, featuresDir, "*.jsonl.gz"))
	sort.Strings(files)
	var size int64
	for _, f := range files {
		if st, err := os.Stat(f); err == nil {
			size += st.Size()
		}
	}
	last := ""
	if len(files) > 0 {
		if f, err := s.LoadFeatures(strings.TrimSuffix(filepath.Base(files[len(files)-1]), ".jsonl.gz")); err == nil {
			last = f.Built
		}
	}
	model, _ := s.loadFocusModel()
	var imp map[string]any
	if b, err := os.ReadFile(filepath.Join(s.Root, featuresDir, "importance.json")); err == nil {
		_ = json.Unmarshal(b, &imp)
	}
	return map[string]any{"schema": featureSchema, "files": len(files), "bytes": size, "last_built": last, "channels": channelDocs,
		"z": zChannels, "focus_model": model, "importance": imp}
}

// channelDocs explains the minute channels (Data tab → Features).
var channelDocs = [][2]string{
	{"keys / clicks / scroll", "system-wide input counts per minute (helper)"}, {"idle / sys_idle", "seconds since the last input"},
	{"switches", "app switches per minute"}, {"locked / display_sleep / displays / on_ac / battery", "machine state"},
	{"cat", "activity category: browser site > window title > screen class > app"}, {"window_cat / browser_cat / screen_class / app_cat", "each source's own category"},
	{"present / facing / perclos / yawn", "camera: at the desk, facing the screen, eye-closure fraction, yawn"}, {"db / speech", "sound level (dBFS) and speech probability"},
	{"place", "your label for the Wi-Fi network"}, {"music", "music playing"},
	{"nvim_keys / nvim_bs / nvim_ins / nvim_bursts / nvim_max_burst / nvim_pause / nvim_buf_switches", "Neovim typing detail"},
	{"quiz_answers / quiz_correct / quiz_latency / quiz_conf_sum", "quiz answers in the minute"},
	{"task_captured / task_done / task_done_diff / task_undone", "task events"},
	{"score / pts_<source> / ev_<source>", "the score curve, points per source, event flags"},
	{"class_in_0/30/60/120 / min_to_class", "known future: classes now and ahead"}, {"due_today_open / due_today_diff", "known future: work due today still open"},
	{"phone_use_min / mac_use_min / messages_hour", "Screen Time and message counts"}, {"hr / hrv / rhr / steps / …", "Apple Watch (Health export)"},
	{"power_sleep / power_wake / power_display_on / power_display_off", "pmset events"}, {"focus", "inferred focus 0-1 (focus.go)"},
	{"mask.<sensor>", "on / off / denied / absent: never read missing as zero"}, {"z.<channel>", "person-relative z-score: (value − your 28-day median) / IQR"},
}

// ── importance: which sensor groups help predict the day ──

// importanceGroups are day-level summaries per sensor group (from the minute rows).
var importanceGroups = map[string][]string{
	"input": {"keys", "clicks", "idle"}, "apps": {"switches"}, "category": {"cat_work"}, "screen": {"screen_work"},
	"camera": {"present", "perclos"}, "mic": {"db", "speech"}, "nvim": {"nvim_keys", "nvim_bs"}, "quiz": {"quiz_answers", "quiz_latency"},
	"phone": {"phone_use_min"}, "health": {"hr", "hrv"}, "focus": {"focus"},
}

// Importance fits a ridge regression of each finished day's final score on day summaries
// (leave-one-out), then measures how much worse it gets when one group's columns are shuffled
// (permutation importance). Needs 30 days with features. Saved to .features/importance.json.
func (s *Store) Importance(now time.Time) (map[string]any, error) {
	files, _ := filepath.Glob(filepath.Join(s.Root, featuresDir, "*.jsonl.gz"))
	var groups []string
	for g := range importanceGroups {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	var X [][]float64
	var Y []float64
	for _, p := range files {
		f, err := s.LoadFeatures(strings.TrimSuffix(filepath.Base(p), ".jsonl.gz"))
		if err != nil {
			continue
		}
		fs, ok := f.Labels["final_score"].(float64)
		if !ok {
			continue
		}
		var x []float64
		for _, g := range groups {
			for _, ch := range importanceGroups[g] {
				var t, n float64
				for _, r := range f.Rows {
					v, has := r.V[ch]
					switch ch {
					case "cat_work":
						w, ok := categoryWork[r.C["cat"]]
						v, has = w, ok
					case "screen_work":
						w, ok := categoryWork[r.C["screen_class"]]
						v, has = w, ok
					}
					if has {
						t += v
						n++
					}
				}
				if n > 0 {
					x = append(x, t/n)
				} else {
					x = append(x, 0)
				}
			}
		}
		X, Y = append(X, x), append(Y, fs)
	}
	if len(Y) < 30 {
		return nil, fmt.Errorf("importance needs 30 days of features, there are %d", len(Y))
	}
	// standardise columns
	k := len(X[0])
	for j := 0; j < k; j++ {
		var m, sd float64
		for i := range X {
			m += X[i][j]
		}
		m /= float64(len(X))
		for i := range X {
			sd += (X[i][j] - m) * (X[i][j] - m)
		}
		sd = math.Sqrt(sd/float64(len(X))) + 1e-9
		for i := range X {
			X[i][j] = (X[i][j] - m) / sd
		}
	}
	looErr := func(X [][]float64) float64 {
		var sse float64
		for hold := range X {
			w := ridge(X, Y, hold, 1.0)
			p := w[0]
			for j := range X[hold] {
				p += w[j+1] * X[hold][j]
			}
			sse += (p - Y[hold]) * (p - Y[hold])
		}
		return sse / float64(len(X))
	}
	base := looErr(X)
	var ymean, vary float64
	for _, y := range Y {
		ymean += y
	}
	ymean /= float64(len(Y))
	for _, y := range Y {
		vary += (y - ymean) * (y - ymean)
	}
	vary /= float64(len(Y))
	rng := rand.New(rand.NewSource(1))
	out := map[string]any{"days": len(Y), "r2": 1 - base/vary, "built": now.Format(scoreStamp)}
	imp := map[string]float64{}
	col := 0
	for _, g := range groups {
		cols := len(importanceGroups[g])
		cp := make([][]float64, len(X))
		for i := range X {
			cp[i] = append([]float64{}, X[i]...)
		}
		perm := rng.Perm(len(X))
		for i := range cp {
			for c := col; c < col+cols; c++ {
				cp[i][c] = X[perm[i]][c]
			}
		}
		imp[g] = math.Round((looErr(cp)-base)/vary*1000) / 1000 // drop in R² when shuffled
		col += cols
	}
	out["groups"] = imp
	b, _ := json.MarshalIndent(out, "", "  ")
	_ = os.MkdirAll(filepath.Join(s.Root, featuresDir), 0o755)
	return out, writeAtomic(filepath.Join(s.Root, featuresDir, "importance.json"), append(b, '\n'))
}

// ridge solves (XᵀX + λI)w = Xᵀy with an intercept, leaving out row `skip`.
func ridge(X [][]float64, Y []float64, skip int, lambda float64) []float64 {
	k := len(X[0]) + 1
	A := make([][]float64, k)
	b := make([]float64, k)
	for i := range A {
		A[i] = make([]float64, k)
		if i > 0 {
			A[i][i] = lambda
		}
	}
	for r := range X {
		if r == skip {
			continue
		}
		x := append([]float64{1}, X[r]...)
		for i := 0; i < k; i++ {
			b[i] += x[i] * Y[r]
			for j := 0; j < k; j++ {
				A[i][j] += x[i] * x[j]
			}
		}
	}
	// Gaussian elimination
	for c := 0; c < k; c++ {
		p := c
		for r := c + 1; r < k; r++ {
			if math.Abs(A[r][c]) > math.Abs(A[p][c]) {
				p = r
			}
		}
		A[c], A[p] = A[p], A[c]
		b[c], b[p] = b[p], b[c]
		if math.Abs(A[c][c]) < 1e-12 {
			continue
		}
		for r := 0; r < k; r++ {
			if r == c {
				continue
			}
			f := A[r][c] / A[c][c]
			for j := c; j < k; j++ {
				A[r][j] -= f * A[c][j]
			}
			b[r] -= f * b[c]
		}
	}
	w := make([]float64, k)
	for i := range w {
		if math.Abs(A[i][i]) > 1e-12 {
			w[i] = b[i] / A[i][i]
		}
	}
	return w
}

// ForgetDay deletes a day's raw records and features (labels stay); ForgetSensor drops one
// sensor's records from every day (and rebuilds features).
func (s *Store) ForgetDay(day string) error {
	if _, err := time.Parse(isoDate, day); err != nil {
		return fmt.Errorf("%w: day is YYYY-MM-DD", ErrLabel)
	}
	os.Remove(s.signalPath(day + ".jsonl"))
	os.Remove(s.featurePath(day))
	return nil
}

func (s *Store) ForgetSensor(sensor string, now time.Time) error {
	files, _ := filepath.Glob(s.signalPath("*.jsonl"))
	signalMu.Lock()
	for _, p := range files {
		day := strings.TrimSuffix(filepath.Base(p), ".jsonl")
		var keep []string
		for _, r := range s.ReadSignals(day) {
			if recSensor(r) == sensor {
				continue
			}
			b, _ := json.Marshal(r)
			keep = append(keep, string(b))
		}
		data := ""
		if len(keep) > 0 {
			data = strings.Join(keep, "\n") + "\n"
		}
		_ = writeAtomic(p, []byte(data))
	}
	signalMu.Unlock()
	feats, _ := filepath.Glob(filepath.Join(s.Root, featuresDir, "*.jsonl.gz"))
	var days []string
	for _, p := range feats {
		days = append(days, strings.TrimSuffix(filepath.Base(p), ".jsonl.gz"))
	}
	sort.Strings(days)
	s.RebuildFeatures(now, days...)
	return nil
}

// geocode finds a city's coordinates (Open-Meteo geocoding, no key), rounded to city level.
func geocode(city string) (float64, float64, string, error) {
	c := http.Client{Timeout: 10 * time.Second}
	resp, err := c.Get("https://geocoding-api.open-meteo.com/v1/search?count=1&name=" + url.QueryEscape(city))
	if err != nil {
		return 0, 0, "", err
	}
	defer resp.Body.Close()
	var d struct {
		Results []struct {
			Name      string  `json:"name"`
			Country   string  `json:"country"`
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil || len(d.Results) == 0 {
		return 0, 0, "", fmt.Errorf("%w: city not found", ErrLabel)
	}
	r := d.Results[0]
	return math.Round(r.Latitude*100) / 100, math.Round(r.Longitude*100) / 100, r.Name + ", " + r.Country, nil
}

// dataViewRoutes adds the Data tab's endpoints.
func (s *Server) dataViewRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/data/overview", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.store.DataOverview(time.Now())) })
	mux.HandleFunc("/api/data/today", func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		day := r.URL.Query().Get("day")
		if _, err := time.Parse(isoDate, day); err != nil {
			day = now.Format(isoDate)
		}
		d, err := s.store.DataToday(now, day)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		writeJSON(w, d)
	})
	mux.HandleFunc("/api/data/days", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.store.DataDays(time.Now())) })
	mux.HandleFunc("/api/data/features", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.store.DataFeatures()) })
	mux.HandleFunc("/api/data/audit", func(w http.ResponseWriter, r *http.Request) {
		iss, n := s.store.Audit()
		if iss == nil {
			iss = []AuditIssue{}
		}
		writeJSON(w, map[string]any{"issues": iss, "lines": n, "summary": auditSummary(iss, n), "backup": backupInfo()})
	})
	mux.HandleFunc("/api/data/sensor", s.post(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name string `json:"name"`
			On   bool   `json:"on"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil || sensorInfo[req.Name][0] == "" {
			http.Error(w, "bad request", 400)
			return
		}
		c := s.store.Sensors()
		c.Sensors[req.Name] = req.On
		s.dataReply(w, s.store.SaveSensors(c))
	}))
	mux.HandleFunc("/api/data/settings", s.post(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			City      *string `json:"city"`
			HealthDir *string `json:"health_dir"`
			Semester  *string `json:"semester_start"`
			DataStart *string `json:"data_start"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			http.Error(w, "bad request", 400)
			return
		}
		c := s.store.Sensors()
		if req.City != nil {
			if strings.TrimSpace(*req.City) == "" {
				c.City, c.Lat, c.Lon = "", 0, 0
			} else {
				lat, lon, name, err := geocode(*req.City)
				if err != nil {
					s.dataReply(w, err)
					return
				}
				c.City, c.Lat, c.Lon = name, lat, lon
			}
		}
		if req.HealthDir != nil {
			c.HealthDir = strings.TrimSpace(*req.HealthDir)
		}
		if req.Semester != nil {
			if *req.Semester != "" {
				if _, err := time.Parse(isoDate, *req.Semester); err != nil {
					s.dataReply(w, fmt.Errorf("%w: semester start is YYYY-MM-DD", ErrLabel))
					return
				}
			}
			c.Semester = *req.Semester
		}
		if req.DataStart != nil {
			if *req.DataStart != "" {
				if _, err := time.Parse(isoDate, *req.DataStart); err != nil {
					s.dataReply(w, fmt.Errorf("%w: data start is YYYY-MM-DD", ErrLabel))
					return
				}
			}
			c.DataStart = *req.DataStart
		}
		s.dataReply(w, s.store.SaveSensors(c))
	}))
	mux.HandleFunc("/api/data/permission", s.post(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Permission string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		pane, ok := permissionPanes[req.Permission]
		if !ok {
			http.Error(w, "unknown permission", 400)
			return
		}
		err := exec.Command("open", pane).Run()
		if req.Permission == "Full Disk Access" {
			// the importers run in this server, not the helper: show its binary to drag into the list
			if exe, e := os.Executable(); e == nil {
				_ = exec.Command("open", "-R", exe).Run()
			}
		}
		s.dataReply(w, err)
	}))
	mux.HandleFunc("/api/data/forget", s.post(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Day, Sensor string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Sensor != "" {
			s.dataReply(w, s.store.ForgetSensor(req.Sensor, time.Now()))
			return
		}
		s.dataReply(w, s.store.ForgetDay(req.Day))
	}))
	mux.HandleFunc("/api/data/helper", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeJSON(w, HelperStatus())
			return
		}
		s.post(func(w http.ResponseWriter, r *http.Request) {
			var req struct{ Action string }
			_ = json.NewDecoder(r.Body).Decode(&req)
			st, err := HelperControl(req.Action)
			if err != nil {
				s.dataReply(w, err)
				return
			}
			writeJSON(w, st)
		})(w, r)
	})
	mux.HandleFunc("/api/data/quality", func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		day := r.URL.Query().Get("day")
		if _, err := time.Parse(isoDate, day); err != nil {
			day = now.Format(isoDate)
		}
		q := s.store.DataQuality(day, now)
		if q == nil {
			q = []QualityCheck{}
		}
		writeJSON(w, map[string]any{"day": day, "checks": q})
	})
	mux.HandleFunc("/api/data/now", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.store.DataNow(time.Now(), r.URL.Query().Get("debug") == "1"))
	})
	mux.HandleFunc("/api/data/live", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"now": time.Now().Format(scoreStamp), "readings": s.store.LiveReadings(time.Now())})
	})
	mux.HandleFunc("/api/data/rebuild", s.post(func(w http.ResponseWriter, r *http.Request) {
		days := s.store.RebuildFeatures(time.Now())
		writeJSON(w, map[string]any{"rebuilt": days})
	}))
}

// backupInfo is the private data repo's last commit, if it exists.
func backupInfo() map[string]any {
	dir := filepath.Join(os.Getenv("HOME"), "notes-data")
	out, err := exec.Command("git", "-C", dir, "log", "-1", "--format=%cI").Output()
	if err != nil {
		return map[string]any{"repo": dir, "exists": false}
	}
	return map[string]any{"repo": dir, "exists": true, "last": strings.TrimSpace(string(out))}
}

// DataNow is the current minute as the feature store sees it, for the overlay: the activity
// category, inferred focus, open tasks due today and minutes to the next class. With debug it
// also has the whole minute row (numbers, categories, masks, z-scores), each sensor's newest raw
// reading with its age and expected interval, the helper, the labelling queue and failing
// quality checks, so you can check that the sensors see what is really happening.
func (s *Store) DataNow(now time.Time, debug bool) map[string]any {
	day := now.Format(isoDate)
	out := map[string]any{"now": now.Format(scoreStamp)}
	start, n, err := dayBounds(day)
	if err != nil {
		return out
	}
	f, err := s.BuildFeatures(day, now, now)
	if err != nil {
		return out
	}
	// the last finished minute (the current one is still filling)
	m := int(now.Sub(start).Minutes()) - 1
	if m < 0 || m >= n || m >= len(f.Rows) {
		return out
	}
	r := f.Rows[m]
	out["minute"] = start.Add(time.Duration(m) * time.Minute).Format("15:04")
	out["cat"] = r.C["cat"]
	out["active"] = minuteActive(r)
	for k, name := range map[string]string{"focus": "focus", "due_today_open": "due_today", "min_to_class": "min_to_class"} {
		if v, ok := r.V[k]; ok {
			out[name] = math.Round(v*100) / 100
		}
	}
	if !debug {
		return out
	}
	out["row"] = r
	live := s.LiveReadings(now)
	ages := map[string]int{}
	for sn, rec := range live {
		if at, err := time.ParseInLocation(scoreStamp, str(rec, "at"), time.Local); err == nil {
			ages[sn] = int(now.Sub(at).Seconds())
		}
	}
	out["live"], out["ages"], out["intervals"] = live, ages, sensorInterval
	out["helper"] = HelperStatus()
	out["queue"] = len(s.LabelQueue(now))
	var fails []QualityCheck
	for _, c := range s.DataQuality(day, now) {
		if c.Status == "fail" || c.Status == "warn" {
			fails = append(fails, c)
		}
	}
	out["quality"] = fails
	return out
}
