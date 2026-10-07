package main

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// Data quality: automatic checks that each sensor's data is plausible, alive and consistent
// with the others, so a broken sensor (a microphone recording digital silence, a camera that
// can't find a face) shows up the same day. Shown on the Data tab (Quality) and summarised on
// the home card; also `notesview data quality`.
//
// Kinds of check: coverage (readings while you were active, against the sensor's interval),
// liveness (last reading not stale while active), variation (not stuck on one value), range
// (values physically possible), and agreement between independent sensors (the camera should
// see you while you type; the screen class should match the site/window category; Neovim's
// keystrokes can't exceed the system's).

// QualityCheck is one check's result.
type QualityCheck struct {
	Sensor string  `json:"sensor"`
	Check  string  `json:"check"`
	Status string  `json:"status"` // ok, warn, fail, info
	Value  float64 `json:"value"`
	Detail string  `json:"detail"`
}

// sensorInterval is the expected seconds between readings while active.
var sensorInterval = map[string]float64{"input": 15, "apps": 15, "window": 15, "browser": 15, "screen": 60, "camera": 15, "mic": 20, "place": 60, "media": 60, "sys": 15}

func stdev(v []float64) float64 {
	if len(v) < 2 {
		return 0
	}
	m := mean(v)
	t := 0.0
	for _, x := range v {
		t += (x - m) * (x - m)
	}
	return math.Sqrt(t / float64(len(v)-1))
}

// DataQuality runs the checks over one day's raw records (today: up to now).
func (s *Store) DataQuality(day string, now time.Time) []QualityCheck {
	recs := s.ReadSignals(day)
	var out []QualityCheck
	add := func(sensor, check, status string, v float64, detail string, args ...any) {
		out = append(out, QualityCheck{sensor, check, status, math.Round(v*100) / 100, fmt.Sprintf(detail, args...)})
	}
	vals := map[string][]float64{}      // sensor.field → values
	last := map[string]time.Time{}      // sensor → last reading
	first := map[string]time.Time{}     // sensor → first reading or "on" state (coverage counts from there)
	count := map[string]int{}           // sensor → readings
	state := map[string]string{}        // sensor → last state
	activeMin := map[string]bool{}      // minutes with input
	typingAt := []time.Time{}           // input samples with typing
	nvimKeys, sysKeys := 0.0, 0.0
	type catAt struct {
		t   time.Time
		cat string
	}
	var screens, contexts []catAt
	var cams []map[string]any
	for _, r := range recs {
		sn := recSensor(r)
		if sn == "" {
			continue
		}
		t, err := time.ParseInLocation(scoreStamp, str(r, "at"), time.Local)
		if err != nil {
			continue
		}
		if st := str(r, "state"); st != "" {
			state[sn] = st
			if st == "on" && first[sn].IsZero() {
				first[sn] = t
			}
			continue
		}
		if first[sn].IsZero() {
			first[sn] = t
		}
		count[sn]++
		if t.After(last[sn]) {
			last[sn] = t
		}
		for _, k := range []string{"db", "speech", "present", "facing", "perclos", "keys", "idle", "conf", "words"} {
			if v, ok := num(r, k); ok {
				vals[sn+"."+k] = append(vals[sn+"."+k], v)
			}
		}
		switch sn {
		case "input":
			k, _ := num(r, "keys")
			c, _ := num(r, "clicks")
			sysKeys += k
			if k+c > 0 {
				activeMin[t.Format("15:04")] = true
			}
			if k > 5 {
				typingAt = append(typingAt, t)
			}
		case "sys":
			if v, ok := num(r, "idle"); ok && v < 60 {
				activeMin[t.Format("15:04")] = true
			}
		case "nvim":
			if v, ok := num(r, "keys"); ok {
				nvimKeys += v
			}
		case "screen":
			screens = append(screens, catAt{t, str(r, "class")})
		case "window", "browser":
			if c := str(r, "cat"); c != "" && c != "other" && c != "unknown" && c != "none" {
				contexts = append(contexts, catAt{t, c})
			}
		case "camera":
			cams = append(cams, r)
		}
	}
	active := float64(len(activeMin))

	// permission / signal states
	for _, sn := range []string{"window", "browser", "screen", "camera", "mic", "place", "media"} {
		switch state[sn] {
		case "denied":
			add(sn, "permission", "fail", 0, "permission not granted: no data")
		case "no-signal":
			add(sn, "signal", "fail", 0, "input is digital silence (a virtual or muted device)")
		case "absent":
			add(sn, "device", "warn", 0, "no device found")
		}
	}
	if !s.SensorOn("input") && !s.SensorOn("apps") {
		return out
	}

	// coverage and liveness of the helper's sensors, against the time you were active
	if active < 10 {
		add("all", "activity", "info", active, "only %d active minutes so far: checks need more data", int(active))
	}
	for _, sn := range []string{"input", "apps", "window", "screen", "camera", "mic", "place"} {
		if !s.SensorOn(sn) || state[sn] == "denied" || state[sn] == "off" {
			continue
		}
		iv := sensorInterval[sn]
		since := 0.0 // active minutes since the sensor started today
		for m := range activeMin {
			if mt, err := time.ParseInLocation("2006-01-02 15:04", day+" "+m, time.Local); err == nil && !mt.Before(first[sn].Truncate(time.Minute)) {
				since++
			}
		}
		expect := since * 60 / iv
		if sn == "window" || sn == "screen" || sn == "camera" { // these skip minutes with the display asleep etc.
			expect *= 0.6
		}
		if expect >= 5 {
			cov := float64(count[sn]) / expect
			st := "ok"
			if cov < 0.3 {
				st = "fail"
			} else if cov < 0.6 {
				st = "warn"
			}
			add(sn, "coverage", st, math.Min(cov, 1), "%d readings for %d active minutes since it started (%.0f%% of expected)", count[sn], int(since), 100*math.Min(cov, 1))
		}
		if day == now.Format(isoDate) && !last[sn].IsZero() && activeMin[now.Add(-time.Minute).Format("15:04")] {
			if age := now.Sub(last[sn]).Seconds(); age > 4*iv+30 {
				add(sn, "liveness", "warn", age, "no reading for %.0f s while you're active", age)
			}
		}
	}

	// variation and range
	type rng struct {
		key       string
		lo, hi    float64
		minStdev  float64
		stuckWhat string
	}
	for _, c := range []rng{
		{"mic.db", -100, 0, 0.5, "sound level never changes"},
		{"camera.present", 0, 1, 0, ""},
		{"camera.perclos", 0, 1, 0, ""},
		{"camera.facing", 0, 1, 0, ""},
		{"input.idle", 0, 1e7, 0.5, "idle time never changes"},
	} {
		v := vals[c.key]
		if len(v) < 10 {
			continue
		}
		bad := 0
		for _, x := range v {
			if x < c.lo || x > c.hi {
				bad++
			}
		}
		sensor := c.key[:indexDot(c.key)]
		if bad > 0 {
			add(sensor, "range", "fail", float64(bad), "%d of %d %s values outside %g…%g", bad, len(v), c.key, c.lo, c.hi)
		}
		if c.minStdev > 0 {
			if sd := stdev(v); sd < c.minStdev {
				add(sensor, "variation", "fail", sd, "%s (spread %.2f over %d readings)", c.stuckWhat, sd, len(v))
			} else {
				add(sensor, "variation", "ok", sd, "%s varies (spread %.1f)", c.key, sd)
			}
		}
	}
	if v := vals["mic.db"]; len(v) >= 10 {
		m := mean(v)
		st := "ok"
		if m < -85 {
			st = "warn"
		}
		add("mic", "level", st, m, "mean %.0f dBFS (a quiet room is about −60 to −45; under −85 suggests a wrong input)", m)
	}

	// agreement: the camera should see a face while you type
	if len(typingAt) >= 5 && len(cams) >= 5 {
		var seen []float64
		for _, c := range cams {
			ct, _ := time.ParseInLocation(scoreStamp, str(c, "at"), time.Local)
			for _, t := range typingAt {
				if math.Abs(ct.Sub(t).Seconds()) <= 20 {
					p, _ := num(c, "present")
					seen = append(seen, p)
					break
				}
			}
		}
		if len(seen) >= 5 {
			m := mean(seen)
			st := "ok"
			if m < 0.6 {
				st = "fail"
			} else if m < 0.8 {
				st = "warn"
			}
			add("camera", "sees you while typing", st, m, "face found %.0f%% of the time while you type (%d bursts; aim ≥ 80%%)", 100*m, len(seen))
		}
	}
	// agreement: screen class vs the site/window category at the same time
	if len(screens) >= 5 && len(contexts) >= 5 {
		agree, n := 0, 0
		for _, sc := range screens {
			best := ""
			bestD := 31.0
			for _, c := range contexts {
				if d := math.Abs(sc.t.Sub(c.t).Seconds()); d < bestD {
					best, bestD = c.cat, d
				}
			}
			if best == "" || sc.cat == "other" {
				continue
			}
			n++
			if sc.cat == best || (categoryWork[sc.cat] >= 0.9) == (categoryWork[best] >= 0.9) {
				agree++
			}
		}
		if n >= 5 {
			r := float64(agree) / float64(n)
			st := "ok"
			if r < 0.5 {
				st = "warn"
			}
			add("screen", "agrees with window/site", st, r, "%d of %d screen readings match the window or site category (work vs not)", agree, n)
		}
	}
	if v := vals["screen.conf"]; len(v) >= 5 {
		low := 0
		for _, x := range v {
			if x < 0.3 {
				low++
			}
		}
		r := float64(low) / float64(len(v))
		st := "ok"
		if r > 0.5 {
			st = "warn"
		}
		add("screen", "confidence", st, 1-r, "%d of %d readings unsure (label a few in the queue to teach it)", low, len(v))
	}
	// agreement: Neovim can't type more than the whole system
	if sysKeys > 50 && nvimKeys > 0 {
		st := "ok"
		if nvimKeys > sysKeys*1.2 {
			st = "warn"
		}
		add("input", "agrees with Neovim", st, nvimKeys/sysKeys, "Neovim %.0f keys, system %.0f (Neovim must be ≤ system)", nvimKeys, sysKeys)
	}
	sort.SliceStable(out, func(i, j int) bool {
		rank := map[string]int{"fail": 0, "warn": 1, "info": 2, "ok": 3}
		return rank[out[i].Status] < rank[out[j].Status]
	})
	return out
}

func indexDot(s string) int {
	for i, c := range s {
		if c == '.' {
			return i
		}
	}
	return len(s)
}

// LiveReadings is the newest reading of each sensor today (what the sensors are seeing now).
func (s *Store) LiveReadings(now time.Time) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, r := range s.ReadSignals(now.Format(isoDate)) {
		sn := recSensor(r)
		if sn == "" || str(r, "state") != "" || (str(r, "src") == "import" && str(r, "kind") == "state") {
			continue
		}
		if sn == "nvim" && str(r, "ev") != "hb" {
			continue
		}
		cp := map[string]any{}
		for k, v := range r {
			if k != "src" && k != "sensor" {
				cp[k] = v
			}
		}
		out[sn] = cp
	}
	return out
}
