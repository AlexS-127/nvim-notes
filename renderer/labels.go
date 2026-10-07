package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Labels: ground truth that you give, optionally and without points, so that inferred measures
// (focus, the score forecast) can be checked and learnt. All in .labels/log.jsonl (append-only):
//   checkout  evening: productivity, energy, mood, last night's sleep quality (1-5), a note
//   checkin   focus right now (1-5), answering a prompt at a random time (experience sampling)
//   label     a category for an unknown window title, site, place or screen minute (also kept
//             as a rule in .signals/rules.json so the helper uses it from then on)
//   skip      a prompt or queue item you dismissed
//   grade     an exam or assignment result
// The morning forecast (1-5) lives in the routine log (routine.go). For training, checkout and
// grade are labels of their own day and inputs only for later days.

const (
	labelsDir     = ".labels"
	labelsLog     = "log.jsonl"
	checkinsADay  = 4
	checkinWindow = 45 * time.Minute // a prompt stays open this long after its time
	checkinFrom   = 9                // prompts fall between 09:00
	checkinTo     = 22               // and 22:00
	checkoutFrom  = 18               // the evening check-out is offered from 18:00
)

// Categories a window title, site or screen minute can be labelled with.
var labelCategories = []string{"study", "reading", "problem", "writing", "comms", "entertainment", "social", "news", "shopping", "admin", "other"}

// Place labels.
var placeLabels = []string{"home", "library", "class", "cafe", "other"}

// LabelEntry is one line of .labels/log.jsonl.
type LabelEntry struct {
	At   string `json:"at"`
	Date string `json:"date"`
	Kind string `json:"kind"`
	// checkout
	Productivity int    `json:"productivity,omitempty"`
	Energy       int    `json:"energy,omitempty"`
	Mood         int    `json:"mood,omitempty"`
	Sleep        int    `json:"sleep,omitempty"`
	Note         string `json:"note,omitempty"` // your own words: stays in .labels, never in features
	// checkin
	Focus    int    `json:"focus,omitempty"`
	Prompted string `json:"prompted,omitempty"` // the prompt time it answers
	// label / skip
	Target   string `json:"target,omitempty"` // title, domain, place, screen, checkin, checkout
	Hash     string `json:"hash,omitempty"`
	Category string `json:"category,omitempty"`
	// grade
	Course string  `json:"course,omitempty"`
	Item   string  `json:"item,omitempty"`
	Score  float64 `json:"score,omitempty"`
	Max    float64 `json:"max,omitempty"`
}

var (
	labelMu  sync.Mutex
	ErrLabel = errors.New("label")
)

func (s *Store) labelPath(name string) string { return filepath.Join(s.Root, labelsDir, name) }

func (s *Store) appendLabel(e LabelEntry) error {
	labelMu.Lock()
	defer labelMu.Unlock()
	if err := os.MkdirAll(s.labelPath(""), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(s.labelPath(labelsLog), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, _ := json.Marshal(e)
	_, err = f.Write(append(b, '\n'))
	return err
}

// Labels is every label, oldest first.
func (s *Store) Labels() []LabelEntry {
	var out []LabelEntry
	readTimedLog(s.labelPath(labelsLog), func(b []byte) {
		var e LabelEntry
		if json.Unmarshal(b, &e) == nil && e.Kind != "" {
			out = append(out, e)
		}
	})
	return out
}

func in1to5(v int) bool { return v >= 1 && v <= 5 }

// Checkout records the evening check-out (each rating 1-5; 0 = left out, but at least one).
func (s *Store) Checkout(prod, energy, mood, sleep int, note string, now time.Time) error {
	any := false
	for _, v := range []int{prod, energy, mood, sleep} {
		if v != 0 && !in1to5(v) {
			return fmt.Errorf("%w: ratings are 1-5", ErrLabel)
		}
		any = any || v != 0
	}
	if !any {
		return fmt.Errorf("%w: rate at least one thing", ErrLabel)
	}
	return s.appendLabel(LabelEntry{At: now.Format(scoreStamp), Date: now.Format(isoDate), Kind: "checkout",
		Productivity: prod, Energy: energy, Mood: mood, Sleep: sleep, Note: strings.TrimSpace(note)})
}

// CheckinTimes are today's random prompt times (stable for the day: seeded by the salt and date).
func (s *Store) CheckinTimes(day string) []time.Time {
	d, err := time.ParseInLocation(isoDate, day, time.Local)
	if err != nil {
		return nil
	}
	h := sha256.Sum256([]byte(s.Sensors().Salt + "|checkin|" + day))
	rng := rand.New(rand.NewSource(int64(binary.LittleEndian.Uint64(h[:8]))))
	// one random minute in each of checkinsADay equal slices of the window, never in a slice's last
	// hour, so prompts are at least an hour apart (one is closed before the next opens)
	span := (checkinTo - checkinFrom) * 60 / checkinsADay
	var out []time.Time
	for i := 0; i < checkinsADay; i++ {
		m := checkinFrom*60 + i*span + rng.Intn(span-60)
		out = append(out, d.Add(time.Duration(m)*time.Minute))
	}
	return out
}

// Prompts is what home may ask you now: an open focus check-in, the evening check-out, and how
// many labelling items wait. Nothing is asked while you are away (idle) or the switch is off.
type Prompts struct {
	Checkin  string `json:"checkin,omitempty"` // prompt time of the open check-in
	Checkout bool   `json:"checkout"`
	Queue    int    `json:"queue"`
	Forecast bool   `json:"forecast"` // the morning forecast still open (routine)
}

// OpenPrompts works out the prompts for now. idle is the machine's idle seconds (-1 unknown).
func (s *Store) OpenPrompts(now time.Time, idle float64) Prompts {
	day := now.Format(isoDate)
	var p Prompts
	answered := map[string]bool{}
	checkedOut := false
	for _, e := range s.Labels() {
		if e.Date != day {
			continue
		}
		switch {
		case e.Kind == "checkin" || (e.Kind == "skip" && e.Target == "checkin"):
			answered[e.Prompted] = true
		case e.Kind == "checkout" || (e.Kind == "skip" && e.Target == "checkout"):
			checkedOut = true
		}
	}
	if s.SensorOn("checkins") && (idle < 0 || idle < 120) {
		for _, t := range s.CheckinTimes(day) {
			k := t.Format(scoreStamp)
			if !now.Before(t) && now.Before(t.Add(checkinWindow)) && !answered[k] {
				p.Checkin = k
			}
		}
	}
	p.Checkout = !checkedOut && now.Hour() >= checkoutFrom
	p.Queue = len(s.LabelQueue(now))
	st := s.Routine(now)
	if st.Show {
		for _, it := range st.Items {
			if it.Kind == routineForecast && !it.Done {
				p.Forecast = true
			}
		}
	}
	return p
}

// Checkin records a focus check-in (1-5) for a prompt time.
func (s *Store) Checkin(prompted string, focus int, now time.Time) error {
	if !in1to5(focus) {
		return fmt.Errorf("%w: focus is 1-5", ErrLabel)
	}
	if prompted == "" {
		prompted = now.Format(scoreStamp) // a check-in you gave without a prompt
	}
	return s.appendLabel(LabelEntry{At: now.Format(scoreStamp), Date: now.Format(isoDate), Kind: "checkin", Focus: focus, Prompted: prompted})
}

// Skip dismisses a prompt (checkin with its time, checkout) or a queue item (by hash).
func (s *Store) Skip(target, key string, now time.Time) error {
	e := LabelEntry{At: now.Format(scoreStamp), Date: now.Format(isoDate), Kind: "skip", Target: target}
	if target == "checkin" {
		e.Prompted = key
	} else {
		e.Hash = key
	}
	if target != "checkin" && target != "checkout" {
		s.updateRules(func(r *Rules) { r.Skipped[key] = true })
	}
	return s.appendLabel(e)
}

// Grade records an exam or assignment result.
func (s *Store) Grade(course, item string, score, max float64, now time.Time) error {
	course, item = strings.TrimSpace(course), strings.TrimSpace(item)
	if course == "" || item == "" || score < 0 || (max > 0 && score > max) {
		return fmt.Errorf("%w: grade needs a course, an item and a score (≤ max)", ErrLabel)
	}
	return s.appendLabel(LabelEntry{At: now.Format(scoreStamp), Date: now.Format(isoDate), Kind: "grade", Course: course, Item: item, Score: score, Max: max})
}

// ── rules (shared with the helper) and the labelling queue ──

// Rules is .signals/rules.json: what you have labelled, by salted hash, plus token counts per
// category (also hashed) that the helper's naive Bayes classifier uses for unseen titles and
// screens. No readable text.
type Rules struct {
	Titles  map[string]string         `json:"titles"`  // title hash → category
	Domains map[string]string         `json:"domains"` // domain hash → category
	Places  map[string]string         `json:"places"`  // network hash → place
	Tokens  map[string]map[string]int `json:"tokens"`  // category → token hash → count
	Skipped map[string]bool           `json:"skipped"` // hashes you chose not to label
}

var rulesMu sync.Mutex

func (s *Store) loadRules() Rules {
	var r Rules
	if b, err := os.ReadFile(s.signalPath(rulesFile)); err == nil {
		_ = json.Unmarshal(b, &r)
	}
	if r.Titles == nil {
		r.Titles = map[string]string{}
	}
	if r.Domains == nil {
		r.Domains = map[string]string{}
	}
	if r.Places == nil {
		r.Places = map[string]string{}
	}
	if r.Tokens == nil {
		r.Tokens = map[string]map[string]int{}
	}
	if r.Skipped == nil {
		r.Skipped = map[string]bool{}
	}
	return r
}

func (s *Store) updateRules(f func(*Rules)) error {
	rulesMu.Lock()
	defer rulesMu.Unlock()
	r := s.loadRules()
	f(&r)
	if err := os.MkdirAll(s.signalPath(""), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(r, "", " ")
	return writeAtomic(s.signalPath(rulesFile), append(b, '\n'))
}

// QueueItem is something the helper could not classify, waiting for you to label it. The text
// (title or domain) lives only in the helper's local queue file outside the notes folder
// (~/Library/Application Support/notesview-sense/queue.json, mode 0600), never in .signals.
type QueueItem struct {
	Kind   string   `json:"kind"` // title, domain, place, screen
	Hash   string   `json:"hash"`
	Text   string   `json:"text,omitempty"` // title or domain; "" for screens
	App    string   `json:"app,omitempty"`
	Guess  string   `json:"guess,omitempty"` // the helper's best guess
	Tokens []string `json:"tokens,omitempty"`
	First  string   `json:"first"`
	Count  int      `json:"count"`
}

// queuePath is the helper's queue (NOTESVIEW_SENSE_QUEUE overrides, for tests).
func queuePath() string {
	if p := os.Getenv("NOTESVIEW_SENSE_QUEUE"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support", "notesview-sense", "queue.json")
}

// LabelQueue is the helper's queue minus what is labelled or skipped, most seen first, last 7 days.
func (s *Store) LabelQueue(now time.Time) []QueueItem {
	var q []QueueItem
	if b, err := os.ReadFile(queuePath()); err == nil {
		_ = json.Unmarshal(b, &q)
	}
	r := s.loadRules()
	cutoff := now.AddDate(0, 0, -7).Format(scoreStamp)
	out := []QueueItem{}
	for _, it := range q {
		known := r.Skipped[it.Hash] || r.Titles[it.Hash] != "" || r.Domains[it.Hash] != "" || r.Places[it.Hash] != ""
		// a title that is only the app's name (a blank window title) can't be labelled meaningfully
		bare := it.Kind == "title" && (strings.TrimSpace(it.Text) == "" || strings.EqualFold(strings.Trim(strings.TrimSpace(it.Text), "()"), it.App))
		if !known && !bare && it.First >= cutoff {
			out = append(out, it)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out
}

// Label gives a queue item (or any hash) a category: kept as a rule, and its tokens are counted
// for the classifier.
func (s *Store) Label(kind, hash, category string, tokenHashes []string, now time.Time) error {
	ok := false
	for _, c := range append(append([]string{}, labelCategories...), placeLabels...) {
		ok = ok || c == category
	}
	if !ok || hash == "" {
		return fmt.Errorf("%w: unknown category %q", ErrLabel, category)
	}
	err := s.updateRules(func(r *Rules) {
		switch kind {
		case "title":
			r.Titles[hash] = category
		case "domain":
			r.Domains[hash] = category
		case "place":
			r.Places[hash] = category
		}
		if kind == "title" || kind == "screen" {
			if r.Tokens[category] == nil {
				r.Tokens[category] = map[string]int{}
			}
			for _, t := range tokenHashes {
				r.Tokens[category][t]++
			}
		}
		if kind == "screen" {
			r.Skipped[hash] = true // answered: out of the queue
		}
	})
	if err != nil {
		return err
	}
	return s.appendLabel(LabelEntry{At: now.Format(scoreStamp), Date: now.Format(isoDate), Kind: "label", Target: kind, Hash: hash, Category: category})
}

// ── forecast calibration ──

// OutcomeRating is a finished day's score as 1-5: its quintile among the 30 finished days before
// it (needs 5), or 0 when there are too few.
func OutcomeRating(scores map[string]Score, day string) int {
	var prev []int
	for k, sc := range scores {
		if k < day && k >= addDays(day, -30) {
			prev = append(prev, sc.Total)
		}
	}
	sc, ok := scores[day]
	if !ok || len(prev) < 5 {
		return 0
	}
	below := 0
	for _, v := range prev {
		if v < sc.Total {
			below++
		}
	}
	return min(5, 1+below*5/len(prev))
}
