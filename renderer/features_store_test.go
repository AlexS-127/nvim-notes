package main

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// writeSignals puts raw records into a day's file.
func writeSignals(t *testing.T, s *Store, day string, recs ...map[string]any) {
	t.Helper()
	os.MkdirAll(s.signalPath(""), 0o755)
	var b strings.Builder
	for _, r := range recs {
		j, _ := json.Marshal(r)
		b.Write(append(j, '\n'))
	}
	os.WriteFile(s.signalPath(day+".jsonl"), []byte(b.String()), 0o644)
}

func featureStore(t *testing.T) (*Store, string) {
	s := newTestStore(t, map[string]string{"inbox.md": "# Inbox\n- [x] task !2 ✅ 2026-10-07 10:05\n"})
	day := "2026-10-07"
	s.Sensors()
	writeSignals(t, s, day,
		map[string]any{"at": day + "T10:00:10", "src": "sense", "sensor": "camera", "state": "denied"},
		map[string]any{"at": day + "T10:00:15", "src": "sense", "sensor": "input", "keys": 120, "clicks": 3, "scroll": 0, "idle": 1},
		map[string]any{"at": day + "T10:00:15", "src": "sense", "sensor": "apps", "bundle": "com.mitchellh.ghostty", "switches": 0, "power": "ac", "displays": 1},
		map[string]any{"at": day + "T10:00:20", "src": "sense", "sensor": "window", "cat": "study", "title_hash": "x"},
		map[string]any{"at": day + "T10:00:30", "src": "sense", "sensor": "input", "keys": 80, "clicks": 0, "scroll": 0, "idle": 2},
		map[string]any{"at": day + "T10:00:30", "src": "sense", "sensor": "input", "keys": 80, "clicks": 0, "scroll": 0, "idle": 2}, // duplicate line
		map[string]any{"at": day + "T10:01:05", "src": "sense", "sensor": "mic", "db": -42.5, "speech": 0.1},
		map[string]any{"at": day + "T10:02:00", "src": "nvim", "ev": "hb", "keys": 50, "bs": 4, "ins": 5, "bursts": 2, "max_burst": 30, "pause": 3, "folder": "act200"},
		map[string]any{"at": day + "T10:03:00", "src": "quiz", "subject": "act200", "kind": "mc", "latency_ms": 4200, "correct": true},
		map[string]any{"at": day + "T10:30:00", "src": "sense", "sensor": "input", "keys": 0, "clicks": 0, "scroll": 0, "idle": 900},
		map[string]any{"at": day + "T23:30:00", "src": "sense", "sensor": "input", "keys": 5, "clicks": 0, "scroll": 0, "idle": 0},
	)
	return s, day
}

func TestBuildFeatures(t *testing.T) {
	s, day := featureStore(t)
	now := time.Date(2026, 10, 8, 4, 0, 0, 0, time.Local)
	start, _, _ := dayBounds(day)
	f, err := s.BuildFeatures(day, now, start.Add(24*time.Hour-time.Second))
	if err != nil || f.Minutes != 1440 || len(f.Rows) != 1440 || f.Schema != featureSchema {
		t.Fatalf("%v minutes=%d rows=%d", err, f.Minutes, len(f.Rows))
	}
	r := f.Rows[600] // 10:00
	if r.V["keys"] != 200 || r.V["clicks"] != 3 || r.C["cat"] != "study" || r.Mask["input"] != "on" || r.Mask["camera"] != "denied" || r.Mask["screen"] != "absent" {
		t.Fatalf("10:00 row %+v", r)
	}
	if f.Rows[601].V["db"] != -42.5 || f.Rows[602].V["db"] != -42.5 { // mic carried one minute
		t.Fatalf("mic carry %+v %+v", f.Rows[601].V, f.Rows[602].V)
	}
	if f.Rows[602].V["nvim_keys"] != 50 || f.Rows[602].C["nvim_folder"] != "act200" || f.Rows[603].V["quiz_latency"] != 4.2 {
		t.Fatalf("nvim/quiz %+v %+v", f.Rows[602], f.Rows[603].V)
	}
	if f.Rows[605].V["pts_done"] != float64(scoreDonePts[2]) || f.Rows[700].V["score"] < float64(scoreDonePts[2]) {
		t.Fatalf("score %+v %+v", f.Rows[605].V, f.Rows[700].V)
	}
	if f.Rows[600].V["focus"] <= 0 || focusV1(f.Rows, 600) != 1 || f.Rows[630].V["focus"] != 0 {
		t.Fatalf("focus %v %v", f.Rows[600].V["focus"], f.Rows[630].V["focus"])
	}
	if f.Cover["input"] == 0 || f.Inputs["dow"] != 2 {
		t.Fatalf("cover %v inputs %v", f.Cover, f.Inputs)
	}
	// round trip
	if err := s.SaveFeatures(f); err != nil {
		t.Fatal(err)
	}
	g, err := s.LoadFeatures(day)
	if err != nil || len(g.Rows) != 1440 || g.Rows[600].V["keys"] != 200 || g.Rows[600].Mask["camera"] != "denied" {
		t.Fatalf("load %v %d", err, len(g.Rows))
	}
	if roll := Rolling(g.Rows, "keys", 15, "sum"); roll[610] != 200 || roll[620] != 0 {
		t.Fatalf("rolling %v %v", roll[610], roll[620])
	}
}

// causality: the rows of a day built up to 10:15 equal the full day's rows up to 10:15
func TestTerminalFolderCategory(t *testing.T) {
	s := newTestStore(t, map[string]string{"inbox.md": "# Inbox\n", "workflow/a.md": "x", "lat101/b.md": "y"})
	day := "2026-10-07"
	writeSignals(t, s, day,
		map[string]any{"at": day + "T10:00:05", "src": "sense", "sensor": "apps", "bundle": "com.mitchellh.ghostty"},
		map[string]any{"at": day + "T10:00:06", "src": "sense", "sensor": "window", "cat": "other", "title_hash": "x"},
		map[string]any{"at": day + "T10:00:10", "src": "nvim", "ev": "hb", "keys": 30, "folder": "lat101"},
		map[string]any{"at": day + "T10:03:05", "src": "sense", "sensor": "apps", "bundle": "com.mitchellh.ghostty"},
		map[string]any{"at": day + "T10:03:06", "src": "sense", "sensor": "window", "cat": "other", "title_hash": "x"},
		map[string]any{"at": day + "T10:20:05", "src": "sense", "sensor": "apps", "bundle": "com.mitchellh.ghostty"},
		map[string]any{"at": day + "T10:20:10", "src": "nvim", "ev": "hb", "keys": 30, "folder": "workflow"},
		map[string]any{"at": day + "T10:40:05", "src": "sense", "sensor": "apps", "bundle": "com.google.Chrome"},
		map[string]any{"at": day + "T10:40:06", "src": "sense", "sensor": "browser", "cat": "entertainment", "domain_hash": "y"},
	)
	now := time.Date(2026, 10, 8, 4, 0, 0, 0, time.Local)
	start, _, _ := dayBounds(day)
	f, _ := s.BuildFeatures(day, now, start.Add(24*time.Hour-time.Second))
	for m, want := range map[int]string{600: "study", 603: "study", 620: "admin", 640: "entertainment"} {
		if got := f.Rows[m].C["cat"]; got != want {
			t.Errorf("minute %d: %q, want %q (%v)", m, got, want, f.Rows[m].C)
		}
	}
}

func TestFeaturesCausal(t *testing.T) {
	s, day := featureStore(t)
	now := time.Date(2026, 10, 8, 4, 0, 0, 0, time.Local)
	start, _, _ := dayBounds(day)
	full, _ := s.BuildFeatures(day, now, start.Add(24*time.Hour-time.Second))
	cut := start.Add(10*time.Hour + 15*time.Minute)
	part, _ := s.BuildFeatures(day, now, cut.Add(-time.Second))
	for m := 0; m < 615; m++ {
		a, b := full.Rows[m], part.Rows[m]
		delete(a.V, "focus") // smoothing looks back only, but compare the rest strictly
		delete(b.V, "focus")
		if !reflect.DeepEqual(a.V, b.V) || !reflect.DeepEqual(a.Mask, b.Mask) {
			t.Fatalf("minute %d differs:\nfull %+v\npart %+v", m, a, b)
		}
	}
	if _, ok := part.Rows[1410].V["keys"]; ok {
		t.Fatal("a truncated day has data from after the cutoff")
	}
}

func TestDSTDayLength(t *testing.T) {
	_, n, _ := dayBounds("2026-11-01") // US fall back
	_, m, _ := dayBounds("2026-03-08") // spring forward
	if n != 1500 || m != 1380 {
		t.Fatalf("DST lengths %d %d", n, m)
	}
}

func TestZScores(t *testing.T) {
	s, day := featureStore(t)
	now := time.Date(2026, 10, 8, 4, 0, 0, 0, time.Local)
	// a baseline day: keys 10 per minute all day
	prev := addDays(day, -1)
	f := DayFeatures{Type: "day", Schema: featureSchema, Day: prev, Minutes: 1440, Inputs: map[string]any{}, Labels: map[string]any{}}
	for m := 0; m < 1440; m++ {
		f.Rows = append(f.Rows, Row{M: m, V: map[string]float64{"keys": float64(10 + m%5)}, Mask: map[string]string{}})
	}
	if err := s.SaveFeatures(f); err != nil {
		t.Fatal(err)
	}
	start, _, _ := dayBounds(day)
	g, _ := s.BuildFeatures(day, now, start.Add(24*time.Hour-time.Second))
	if z := g.Rows[600].Z["keys"]; z < 50 { // 200 keys against a median of 12, IQR 2
		t.Fatalf("z keys %v", z)
	}
}

func TestSleepWindow(t *testing.T) {
	s := newTestStore(t, map[string]string{"inbox.md": "# Inbox\n"})
	writeSignals(t, s, "2026-10-06", map[string]any{"at": "2026-10-06T23:40:00", "src": "import", "kind": "power", "ev": "sleep"})
	writeSignals(t, s, "2026-10-07", map[string]any{"at": "2026-10-07T07:10:00", "src": "import", "kind": "power", "ev": "wake"})
	if _, ok := s.SleepWindow("2026-10-07"); ok {
		t.Fatal("sleep guessed from the Mac's power log")
	}
	writeSignals(t, s, "2026-10-08", map[string]any{"at": "2026-10-08T00:00:00", "src": "import", "kind": "health", "metric": "sleep", "totalsleep": 6.83})
	if h, ok := s.SleepWindow("2026-10-08"); !ok || h != 6.8 {
		t.Fatalf("watch sleep %v %v", h, ok)
	}
}
