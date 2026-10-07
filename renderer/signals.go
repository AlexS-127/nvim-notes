package main

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Raw data for the future score market's traders (and the Data tab). Every source appends one
// JSON object per line to .signals/YYYY-MM-DD.jsonl (the local day of its "at"), with "src":
//   - sys:   this server's sampler every 15 s (front app, machine idle), see sampleSystem
//   - task:  task captured / ticked / unticked (Store.Capture, ToggleCheckbox, and the Lua toggle)
//   - nvim:  nvim/lua/signals.lua heartbeats (keystroke counts, insert time, backspaces, bursts),
//            buffer switches (hashed), focus, start page
//   - quiz:  quiz.py, one line per answer (latency, correct, kind, confidence)
//   - sense: the notesview-sense helper (macapp/Sense.swift): input counts, apps, window and
//            browser categories, screen class, camera presence/fatigue, sound level, place, media,
//            and {sensor, state} whenever a sensor turns on/off or loses its permission
//   - import: nightly importers (sense_import.go): pmset, Screen Time, message counts, weather, Health
// Only counts, categories, hashes and model outputs are ever written: no text typed, no titles,
// URLs, images or audio. privacy.go audits this. Hidden folder: the viewer and word counter skip it.

const (
	signalsDir    = ".signals"
	sensorsConfig = "config.json" // .signals/config.json: sensor switches, salt, place, health folder
	rulesFile     = "rules.json"  // .signals/rules.json: category rules learnt from your labels
)

var signalMu sync.Mutex

func (s *Store) signalPath(name string) string { return filepath.Join(s.Root, signalsDir, name) }

// appendSignal writes one record (it gets "at" if missing) to the file of its day.
func appendSignal(root string, rec map[string]any) {
	at, _ := rec["at"].(string)
	if at == "" {
		at = time.Now().Format(scoreStamp)
		rec["at"] = at
	}
	day := at
	if len(day) >= 10 {
		day = day[:10]
	}
	signalMu.Lock()
	defer signalMu.Unlock()
	dir := filepath.Join(root, signalsDir)
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, day+".jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(rec)
	f.Write(append(b, '\n'))
}

// ReadSignals is one day's raw records in file order (times are nearly sorted; callers that need
// order sort by "at").
func (s *Store) ReadSignals(day string) []map[string]any {
	var out []map[string]any
	f, err := os.Open(s.signalPath(day + ".jsonl"))
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var r map[string]any
		if json.Unmarshal(sc.Bytes(), &r) == nil {
			if _, ok := r["at"].(string); ok {
				out = append(out, r)
			}
		}
	}
	return out
}

// ── sensor settings ──

// SensorConfig is .signals/config.json, shared with the Swift helper: which sensors run, the
// salt for hashes, and settings of the importers.
type SensorConfig struct {
	Sensors   map[string]bool `json:"sensors"`
	Salt      string          `json:"salt"`
	City      string          `json:"city,omitempty"`
	Lat       float64         `json:"lat,omitempty"`
	Lon       float64         `json:"lon,omitempty"`
	HealthDir string          `json:"health_dir,omitempty"`
	Semester  string          `json:"semester_start,omitempty"` // YYYY-MM-DD, for week of semester
}

// sensorDefaults: what runs unless switched off. The ones that need a permission are on, so the
// permission is asked for once the helper runs; denying it just marks the sensor "denied".
var sensorDefaults = map[string]bool{
	"input": true, "apps": true, "window": true, "browser": true, "screen": true, "camera": true,
	"mic": true, "place": true, "media": true, "bluetooth": false,
	"pmset": true, "screentime": true, "messages": true, "weather": true, "health": true,
	"nvim": true, "quiz": true, "sys": true, "checkins": true,
}

var sensorCfgMu sync.Mutex

// Sensors is the sensor config with defaults filled in; the first call writes it (with a new salt).
func (s *Store) Sensors() SensorConfig {
	sensorCfgMu.Lock()
	defer sensorCfgMu.Unlock()
	var c SensorConfig
	b, err := os.ReadFile(s.signalPath(sensorsConfig))
	if err == nil {
		_ = json.Unmarshal(b, &c)
	}
	changed := err != nil
	if c.Sensors == nil {
		c.Sensors = map[string]bool{}
	}
	for k, v := range sensorDefaults {
		if _, ok := c.Sensors[k]; !ok {
			c.Sensors[k] = v
			changed = true
		}
	}
	if c.Salt == "" {
		r := make([]byte, 16)
		rand.Read(r)
		c.Salt = hex.EncodeToString(r)
		changed = true
	}
	if changed {
		_ = s.saveSensorsLocked(c)
	}
	return c
}

func (s *Store) saveSensorsLocked(c SensorConfig) error {
	if err := os.MkdirAll(s.signalPath(""), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return writeAtomic(s.signalPath(sensorsConfig), append(b, '\n'))
}

// SaveSensors writes the sensor config (the salt is never replaced: old hashes must keep matching).
func (s *Store) SaveSensors(c SensorConfig) error {
	cur := s.Sensors()
	sensorCfgMu.Lock()
	defer sensorCfgMu.Unlock()
	c.Salt = cur.Salt
	if c.Sensors == nil {
		c.Sensors = cur.Sensors
	}
	return s.saveSensorsLocked(c)
}

// SensorOn is whether a sensor is switched on.
func (s *Store) SensorOn(name string) bool { return s.Sensors().Sensors[name] }

// ── hashing and tokens (identical in Sense.swift) ──

// saltedHash is the first 16 hex chars of sha256(salt|lowercased text): stable per person, not
// reversible without the salt and a guess of the text.
func saltedHash(salt, text string) string {
	h := sha256.Sum256([]byte(salt + "|" + strings.ToLower(strings.TrimSpace(text))))
	return hex.EncodeToString(h[:8])
}

// tokens splits text into lowercase words of 3+ letters/digits that are not all digits.
func tokens(text string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len([]rune(w)) < 3 {
			continue
		}
		digits := true
		for _, r := range w {
			if !unicode.IsDigit(r) {
				digits = false
				break
			}
		}
		if !digits {
			out = append(out, w)
		}
	}
	return out
}

// ── task events ──

var (
	sigDueRe     = regexp.MustCompile(`@(\d{4}-\d{2}-\d{2})`)
	sigCreatedRe = regexp.MustCompile(`_\(([A-Z][a-z]{2} \d{2} \d{2}:\d{2})\)_`)
)

// taskSignal records a task event: kind, difficulty, top-level folder, age since capture, due date.
func (s *Store) taskSignal(ev, rel, line string, now time.Time) {
	folder := ""
	if i := strings.Index(rel, "/"); i > 0 {
		folder = rel[:i]
	}
	rec := map[string]any{"src": "task", "ev": ev, "diff": scanDifficulty(line), "folder": folder, "at": now.Format(scoreStamp)}
	if m := sigCreatedRe.FindStringSubmatch(line); m != nil {
		if t, err := time.ParseInLocation("Jan 02 15:04 2006", m[1]+" "+strconv.Itoa(now.Year()), time.Local); err == nil {
			if t.After(now) {
				t = t.AddDate(-1, 0, 0)
			}
			rec["age_min"] = int(now.Sub(t).Minutes())
		}
	}
	if d := sigDueRe.FindStringSubmatch(line); d != nil {
		rec["due"] = d[1]
	}
	appendSignal(s.Root, rec)
}

// ── system sampler (works without the helper) ──

var (
	hidIdleRe = regexp.MustCompile(`"HIDIdleTime"\s*=\s*(\d+)`)
	lsNameRe  = regexp.MustCompile(`"LSDisplayName"\s*=\s*"([^"]*)"`)
)

// sampleSystem reads the machine's input idle time and the frontmost app's name (no permissions).
func sampleSystem() (idleSec float64, app string, ok bool) {
	if runtime.GOOS != "darwin" {
		return 0, "", false
	}
	out, err := exec.Command("ioreg", "-c", "IOHIDSystem", "-d", "4").Output()
	if err != nil {
		return 0, "", false
	}
	if m := hidIdleRe.FindSubmatch(out); m != nil {
		ns, _ := strconv.ParseFloat(string(m[1]), 64)
		idleSec = ns / 1e9
	}
	if asn, err := exec.Command("lsappinfo", "front").Output(); err == nil {
		if info, err := exec.Command("lsappinfo", "info", "-only", "name", strings.TrimSpace(string(asn))).Output(); err == nil {
			if m := lsNameRe.FindSubmatch(info); m != nil {
				app = string(m[1])
			}
		}
	}
	return idleSec, app, true
}

// sysSampler runs in the server: every 15 s, the front app and idle seconds.
func (s *Server) sysSampler() {
	for {
		if s.store.SensorOn("sys") {
			if idle, app, ok := sampleSystem(); ok {
				appendSignal(s.store.Root, map[string]any{"src": "sys", "idle": int(idle), "app": app})
			}
		}
		time.Sleep(15 * time.Second)
	}
}
