package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"time"
)

// Inferred focus, 0-1 per minute, from the feature rows (no timer, nothing to start or stop).
//
// Version 1 is transparent: active (input in the last minute, not idle, or the camera sees you
// reading the screen; screen awake) × how much
// the minute's activity category counts as work (categoryWork) × a penalty for rapid app
// switching × a penalty when the camera sees nobody or someone looking away, smoothed over 5
// minutes. Focus minutes are minutes ≥ 0.6 inside runs of 10+ minutes.
//
// Version 2 is fitted to your focus check-ins (labels.go): logistic regression on a few
// 15-minute summaries. FitFocusModel fits it with 5-fold cross-validation and switches to it only
// when it predicts your answers better than version 1 (.features/focus_model.json says which).

const focusModelFile = "focus_model.json"

// FocusModel is the fitted version 2 and how both versions did on your check-ins.
type FocusModel struct {
	Weights []float64 `json:"weights"`
	N       int       `json:"n"`      // check-ins used
	AccV1   float64   `json:"acc_v1"` // cross-validated accuracy predicting "focus ≥ 4"
	AccV2   float64   `json:"acc_v2"`
	Use     bool      `json:"use"` // version 2 is better: use it
	Fitted  string    `json:"fitted"`
}

func (s *Store) loadFocusModel() (FocusModel, bool) {
	var m FocusModel
	b, err := os.ReadFile(filepath.Join(s.Root, featuresDir, focusModelFile))
	if err != nil || json.Unmarshal(b, &m) != nil {
		return m, false
	}
	return m, true
}

// focusInputs are version 2's inputs at minute i (15-minute summaries, past only).
func focusInputs(rows []Row, i int, v1 float64) []float64 {
	from := max(0, i-14)
	var keys, sw, work, pres, active, n, presN float64
	for j := from; j <= i; j++ {
		r := rows[j]
		keys += r.V["keys"] + r.V["nvim_keys"]
		sw += r.V["switches"]
		w, ok := categoryWork[r.C["cat"]]
		if !ok {
			w = 0.5
		}
		work += w
		if r.Mask["camera"] == "on" {
			pres += r.V["present"]
			presN++
		}
		if minuteActive(r) {
			active++
		}
		n++
	}
	p := 0.5
	if presN > 0 {
		p = pres / presN
	}
	return []float64{1, math.Log1p(keys / n), sw / n, work / n, p, active / n, v1}
}

func minuteActive(r Row) bool {
	if r.V["locked"] > 0 || r.V["display_sleep"] > 0 {
		return false
	}
	if r.V["keys"]+r.V["clicks"]+r.V["scroll"]+r.V["nvim_keys"]+r.V["quiz_answers"] > 0 {
		return true
	}
	// no input but the camera sees you at the screen, facing it, eyes open: reading (a long answer, a
	// PDF, a lecture slide), not away
	if r.Mask["camera"] == "on" && r.V["present"] >= 0.5 && r.V["facing"] >= 0.5 && r.V["perclos"] < 0.5 {
		return true
	}
	if v, ok := r.V["idle"]; ok {
		return v < 60
	}
	if v, ok := r.V["sys_idle"]; ok {
		return v < 60
	}
	return false
}

// focusV1 is version 1's raw (unsmoothed) value for minute i.
func focusV1(rows []Row, i int) float64 {
	r := rows[i]
	if !minuteActive(r) {
		return 0
	}
	w, ok := categoryWork[r.C["cat"]]
	if !ok {
		w = 0.5
	}
	sw := 0.0
	for j := max(0, i-4); j <= i; j++ {
		sw += rows[j].V["switches"]
	}
	switch {
	case sw > 8:
		w *= 0.6
	case sw > 4:
		w *= 0.8
	}
	if r.Mask["camera"] == "on" {
		if r.V["present"] < 0.5 && r.V["keys"]+r.V["nvim_keys"] == 0 {
			w *= 0.3 // away from the desk and not typing
		} else if r.V["facing"] < 0.4 {
			w *= 0.7
		}
	}
	return w
}

func sigmoid(x float64) float64 { return 1 / (1 + math.Exp(-x)) }

func dot(a, b []float64) float64 {
	t := 0.0
	for i := range a {
		t += a[i] * b[i]
	}
	return t
}

// addFocus fills "focus" for each minute and the day's focus totals (labels).
func (s *Store) addFocus(f *DayFeatures) {
	rows := f.Rows
	raw := make([]float64, len(rows))
	for i := range rows {
		raw[i] = focusV1(rows, i)
	}
	model, ok := s.loadFocusModel()
	for i := range rows {
		from := max(0, i-4)
		v := mean(raw[from : i+1])
		if ok && model.Use && len(model.Weights) == 7 {
			v = sigmoid(dot(model.Weights, focusInputs(rows, i, v)))
		}
		if minuteActive(rows[i]) || v > 0 {
			rows[i].V["focus"] = math.Round(v*100) / 100
		}
	}
	// focus minutes: ≥ 0.6 in runs of 10+
	total, longest, run := 0, 0, 0
	for i := 0; i <= len(rows); i++ {
		if i < len(rows) && rows[i].V["focus"] >= 0.6 {
			run++
			continue
		}
		if run >= 10 {
			total += run
		}
		longest = max(longest, run)
		run = 0
	}
	f.Labels["focus_min"], f.Labels["focus_longest"] = total, longest
	f.Labels["focus_version"] = map[bool]int{true: 2, false: 1}[ok && model.Use]
}

// FitFocusModel fits version 2 to every check-in that has feature rows, and saves it with the
// cross-validated accuracy of both versions. It needs 50 check-ins.
func (s *Store) FitFocusModel(now time.Time) (FocusModel, error) {
	var X [][]float64
	var Y []float64
	var V1 []float64
	byDay := map[string][]LabelEntry{}
	start := s.DataStart()
	for _, e := range s.Labels() {
		if e.Kind == "checkin" && e.Prompted != "" && !testDay(e.Date, start) {
			byDay[e.Date] = append(byDay[e.Date], e)
		}
	}
	for day, es := range byDay {
		f, err := s.LoadFeatures(day)
		if err != nil {
			continue
		}
		start, n, _ := dayBounds(day)
		for _, e := range es {
			m := minuteOf(start, n, e.Prompted)
			if m < 0 || m >= len(f.Rows) {
				continue
			}
			v1 := f.Rows[m].V["focus"]
			X = append(X, focusInputs(f.Rows, m, v1))
			V1 = append(V1, v1)
			Y = append(Y, map[bool]float64{true: 1, false: 0}[e.Focus >= 4])
		}
	}
	model := FocusModel{N: len(Y), Fitted: now.Format(scoreStamp)}
	if len(Y) < 50 {
		return model, nil
	}
	fit := func(idx []int) []float64 {
		w := make([]float64, 7)
		for epoch := 0; epoch < 400; epoch++ {
			g := make([]float64, 7)
			for _, i := range idx {
				d := sigmoid(dot(w, X[i])) - Y[i]
				for k := range w {
					g[k] += d * X[i][k]
				}
			}
			for k := range w {
				w[k] -= 0.5 * (g[k]/float64(len(idx)) + 0.01*w[k])
			}
		}
		return w
	}
	var right1, right2 float64
	for fold := 0; fold < 5; fold++ {
		var train, test []int
		for i := range Y {
			if i%5 == fold {
				test = append(test, i)
			} else {
				train = append(train, i)
			}
		}
		w := fit(train)
		for _, i := range test {
			if (V1[i] >= 0.6) == (Y[i] == 1) {
				right1++
			}
			if (sigmoid(dot(w, X[i])) >= 0.5) == (Y[i] == 1) {
				right2++
			}
		}
	}
	all := make([]int, len(Y))
	for i := range all {
		all[i] = i
	}
	model.Weights = fit(all)
	model.AccV1, model.AccV2 = right1/float64(len(Y)), right2/float64(len(Y))
	model.Use = model.AccV2 > model.AccV1+0.02
	b, _ := json.MarshalIndent(model, "", "  ")
	_ = os.MkdirAll(filepath.Join(s.Root, featuresDir), 0o755)
	return model, writeAtomic(filepath.Join(s.Root, featuresDir, focusModelFile), append(b, '\n'))
}
