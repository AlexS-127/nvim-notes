package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Scheduled revision (spaced repetition). A topic is one note in a course folder. It is
// scheduled when you write at least min_words of your own words in it on a day (from the
// new-words log), or by hand, and then quizzed at growing intervals (Leitner steps): a good
// score moves it a step up, a middling one repeats the step, a poor one starts again. The
// questions for each topic are written by Claude (revision_gen.go) into
// .revision/questions/<note>.md; quiz.py --revise ID asks them and reports back with
// `notesview revise done`. Each revision scores scoreRevisionPts (once per topic per day).
//
// Files in .revision/ (committed with the notes): config.json (watched folders, min_words,
// intervals), topics.json (the schedule), log.jsonl (append-only: add, remove, done).

const (
	revDir       = ".revision"
	revConfig    = "config.json"
	revTopics    = "topics.json"
	revLogFile   = "log.jsonl"
	revQuestions = "questions"
	revMinTotal  = 3 // questions answered for a revision to count
)

var defaultIntervals = []int{1, 3, 7, 14, 30, 60, 120}

// RevisionConfig is .revision/config.json.
type RevisionConfig struct {
	Folders   []string `json:"folders"`
	MinWords  int      `json:"min_words"`
	Intervals []int    `json:"intervals"`
	Since     string   `json:"since"` // writing before this day never schedules anything (set on first use)
}

// RevisionGen is the state of a topic's Claude-written questions.
type RevisionGen struct {
	Status string `json:"status"`          // none, pending, ok, failed, rebuilding (classbanks.go)
	At     string `json:"at,omitempty"`    // when it last ran
	Words  int    `json:"words,omitempty"` // the note's word count then
	Count  int    `json:"count,omitempty"` // questions written
	Error  string `json:"error,omitempty"`
	Mode   string `json:"mode,omitempty"` // set by Rebuild banks: repair or full
	Kept   int    `json:"kept,omitempty"` // repair: questions kept word for word
}

// Topic is one note on the revision schedule.
type Topic struct {
	ID      string      `json:"id"` // note path relative to the notes folder
	Title   string      `json:"title"`
	Subject string      `json:"subject"` // its top-level folder
	Learned string      `json:"learned"` // day it was scheduled from
	Step    int         `json:"step"`
	Due     string      `json:"due"`
	Last    string      `json:"last,omitempty"`  // day of the last revision
	Score   float64     `json:"score,omitempty"` // last revision's score (0-1)
	Count   int         `json:"count,omitempty"` // revisions done
	Gen     RevisionGen `json:"gen"`
}

// RevisionEntry is one line of the log.
type RevisionEntry struct {
	At      string  `json:"at"`
	Date    string  `json:"date"`
	ID      string  `json:"id"`
	Title   string  `json:"title,omitempty"`
	Kind    string  `json:"kind"` // add, remove, done
	Score   float64 `json:"score,omitempty"`
	Correct int     `json:"correct,omitempty"`
	Total   int     `json:"total,omitempty"`
	Step    int     `json:"step,omitempty"` // step after
	NextDue string  `json:"next_due,omitempty"`
	Points  bool    `json:"points,omitempty"` // this done scored (counted, first for the topic that day)
	Manual  bool    `json:"manual,omitempty"` // add: by hand
}

type revisionTopics struct {
	Topics  []Topic           `json:"topics"`
	Removed map[string]string `json:"removed,omitempty"` // id → day removed (writing after it schedules again)
}

var (
	revMu       sync.Mutex
	ErrRevision = errors.New("revision")
)

func (s *Store) revPath(name ...string) string {
	return filepath.Join(append([]string{s.Root, revDir}, name...)...)
}

// RevisionSettings is the config, with defaults for anything missing; the first call writes it.
func (s *Store) RevisionSettings(now time.Time) RevisionConfig {
	var c RevisionConfig
	b, err := os.ReadFile(s.revPath(revConfig))
	missing := os.IsNotExist(err)
	if err == nil {
		_ = json.Unmarshal(b, &c)
	}
	if c.Folders == nil {
		for _, t := range s.TrackedWords() {
			switch t {
			case "daily", "inbox", "inbox.md", "workflow", "misc", "reviews", "format":
				continue
			}
			if st, err := os.Stat(filepath.Join(s.Root, t)); err == nil && st.IsDir() {
				c.Folders = append(c.Folders, t)
			}
		}
		if c.Folders == nil {
			c.Folders = []string{}
		}
	}
	if c.MinWords <= 0 {
		c.MinWords = 50
	}
	if len(c.Intervals) == 0 {
		c.Intervals = defaultIntervals
	}
	if c.Since == "" {
		c.Since = now.AddDate(0, 0, -7).Format(isoDate) // the last week's notes count, not all history
	}
	if missing {
		_ = s.SaveRevisionSettings(c)
	}
	return c
}

// SaveRevisionSettings writes config.json (intervals must be positive, folders clean).
func (s *Store) SaveRevisionSettings(c RevisionConfig) error {
	var iv []int
	for _, d := range c.Intervals {
		if d > 0 {
			iv = append(iv, d)
		}
	}
	if len(iv) == 0 {
		iv = defaultIntervals
	}
	c.Intervals = iv
	c.Folders = nonNilStrings(cleanWordsPaths(c.Folders))
	if c.MinWords <= 0 {
		c.MinWords = 50
	}
	if err := os.MkdirAll(s.revPath(), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return writeAtomic(s.revPath(revConfig), append(b, '\n'))
}

func (s *Store) loadTopics() revisionTopics {
	var t revisionTopics
	if b, err := os.ReadFile(s.revPath(revTopics)); err == nil {
		_ = json.Unmarshal(b, &t)
	}
	if t.Topics == nil {
		t.Topics = []Topic{}
	}
	if t.Removed == nil {
		t.Removed = map[string]string{}
	}
	return t
}

func (s *Store) saveTopics(t revisionTopics) error {
	if err := os.MkdirAll(s.revPath(), 0o755); err != nil {
		return err
	}
	sort.SliceStable(t.Topics, func(i, j int) bool { return t.Topics[i].ID < t.Topics[j].ID })
	b, _ := json.MarshalIndent(t, "", "  ")
	return writeAtomic(s.revPath(revTopics), append(b, '\n'))
}

// Topics is the revision schedule.
func (s *Store) Topics() []Topic { return s.loadTopics().Topics }

func (s *Store) appendRevision(e RevisionEntry) error {
	if err := os.MkdirAll(s.revPath(), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(s.revPath(revLogFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	line, _ := json.Marshal(e)
	_, err = f.Write(append(line, '\n'))
	return err
}

// RevisionLog is every logged revision event, oldest first.
func (s *Store) RevisionLog() []RevisionEntry {
	var out []RevisionEntry
	readTimedLog(s.revPath(revLogFile), func(b []byte) {
		var e RevisionEntry
		if json.Unmarshal(b, &e) == nil && e.Date != "" && e.ID != "" {
			out = append(out, e)
		}
	})
	return out
}

func addDays(day string, n int) string {
	t, err := time.Parse(isoDate, day)
	if err != nil {
		return day
	}
	return t.AddDate(0, 0, n).Format(isoDate)
}

// NextStep is the Leitner step after a revision with this score (0-1): 0.8 or more moves up,
// 0.5 to 0.8 stays, under 0.5 starts again. The last step repeats.
func NextStep(step int, score float64, intervals []int) int {
	switch {
	case score >= 0.8:
		step++
	case score < 0.5:
		step = 0
	}
	return min(max(step, 0), len(intervals)-1)
}

// noteTitle is a note's title (first heading, else its file name).
func (s *Store) noteTitle(rel string) string {
	src, _ := os.ReadFile(filepath.Join(s.Root, rel))
	return Title(rel, src)
}

// revisable is whether a note can be a topic: a markdown note that exists, in a folder, not a quiz bank
// or the quiz's mistakes list.
func (s *Store) revisable(rel string) bool {
	base := filepath.Base(rel)
	if !strings.HasSuffix(rel, ".md") || !strings.Contains(rel, "/") || strings.HasPrefix(rel, ".") ||
		base == "questions.md" || base == "definitions.md" || base == "mistakes.md" {
		return false
	}
	st, err := os.Stat(filepath.Join(s.Root, rel))
	return err == nil && !st.IsDir()
}

func (s *Store) newTopic(rel, learned string) Topic {
	return Topic{ID: rel, Title: s.noteTitle(rel), Subject: strings.SplitN(rel, "/", 2)[0], Learned: learned,
		Due: addDays(learned, 1), Gen: RevisionGen{Status: "none"}}
}

// AddTopic schedules a note by hand, learned today (first revision tomorrow). Re-adding a
// scheduled note is a no-op.
func (s *Store) AddTopic(rel string, now time.Time) (Topic, error) {
	rel = filepath.ToSlash(filepath.Clean(strings.TrimPrefix(strings.TrimSpace(rel), "./")))
	if r, err := s.Rel(filepath.Join(s.Root, rel)); err == nil {
		rel = r
	}
	if !s.revisable(rel) {
		return Topic{}, fmt.Errorf("%w: %s is not a note in a folder", ErrRevision, rel)
	}
	revMu.Lock()
	defer revMu.Unlock()
	t := s.loadTopics()
	for _, x := range t.Topics {
		if x.ID == rel {
			return x, nil
		}
	}
	day := now.Format(isoDate)
	tp := s.newTopic(rel, day)
	t.Topics = append(t.Topics, tp)
	delete(t.Removed, rel)
	if err := s.saveTopics(t); err != nil {
		return Topic{}, err
	}
	_ = s.appendRevision(RevisionEntry{At: now.Format(scoreStamp), Date: day, ID: rel, Title: tp.Title, Kind: "add", Manual: true})
	return tp, nil
}

// RemoveTopic takes a note off the schedule; only writing after today schedules it again.
func (s *Store) RemoveTopic(rel string, now time.Time) error {
	revMu.Lock()
	defer revMu.Unlock()
	t := s.loadTopics()
	for i, x := range t.Topics {
		if x.ID == rel {
			t.Topics = append(t.Topics[:i], t.Topics[i+1:]...)
			t.Removed[rel] = now.Format(isoDate)
			if err := s.saveTopics(t); err != nil {
				return err
			}
			return s.appendRevision(RevisionEntry{At: now.Format(scoreStamp), Date: now.Format(isoDate), ID: rel, Title: x.Title, Kind: "remove"})
		}
	}
	return fmt.Errorf("%w: %s is not scheduled", ErrRevision, rel)
}

// RecordRevision schedules notes in watched folders that gained min_words of your words on a
// day (since config.since). Run by the server's score loop; cheap when nothing is new.
func (s *Store) RecordRevision(now time.Time) {
	cfg := s.RevisionSettings(now)
	if len(cfg.Folders) == 0 {
		return
	}
	type key struct{ day, file string }
	per := map[key]int{}
	readTimedLog(filepath.Join(s.Root, wordsLog), func(b []byte) {
		var e struct {
			Date, File string
			Words      int
		}
		if json.Unmarshal(b, &e) == nil && e.Words > 0 && e.Date >= cfg.Since {
			per[key{e.Date, filepath.ToSlash(e.File)}] += e.Words
		}
	})
	watched := func(rel string) bool {
		for _, f := range cfg.Folders {
			if strings.HasPrefix(rel, f+"/") {
				return true
			}
		}
		return false
	}
	learned := map[string]string{} // first qualifying day per note
	for k, n := range per {
		if n >= cfg.MinWords && watched(k.file) {
			if d, ok := learned[k.file]; !ok || k.day < d {
				learned[k.file] = k.day
			}
		}
	}
	if len(learned) == 0 {
		return
	}
	revMu.Lock()
	defer revMu.Unlock()
	t := s.loadTopics()
	have := map[string]bool{}
	for _, x := range t.Topics {
		have[x.ID] = true
	}
	var added []Topic
	for rel, day := range learned {
		if have[rel] || !s.revisable(rel) {
			continue
		}
		if r, ok := t.Removed[rel]; ok {
			// removed: only new writing after the removal counts
			later := ""
			for k, n := range per {
				if k.file == rel && n >= cfg.MinWords && k.day > r && (later == "" || k.day < later) {
					later = k.day
				}
			}
			if later == "" {
				continue
			}
			day = later
			delete(t.Removed, rel)
		}
		tp := s.newTopic(rel, day)
		t.Topics = append(t.Topics, tp)
		added = append(added, tp)
	}
	if len(added) == 0 {
		return
	}
	if s.saveTopics(t) == nil {
		for _, tp := range added {
			_ = s.appendRevision(RevisionEntry{At: now.Format(scoreStamp), Date: tp.Learned, ID: tp.ID, Title: tp.Title, Kind: "add"})
		}
	}
}

// RevisionDone records a finished revision: the next step and due date, and whether it scores
// (at least revMinTotal answered, and the topic's first scoring revision today).
func (s *Store) RevisionDone(id string, score float64, correct, total int, now time.Time) (Topic, RevisionEntry, error) {
	revMu.Lock()
	defer revMu.Unlock()
	cfg := s.RevisionSettings(now)
	t := s.loadTopics()
	day := now.Format(isoDate)
	for i := range t.Topics {
		tp := &t.Topics[i]
		if tp.ID != id {
			continue
		}
		score = min(1, max(0, score))
		tp.Step = NextStep(tp.Step, score, cfg.Intervals)
		tp.Due = addDays(day, cfg.Intervals[tp.Step])
		tp.Last, tp.Score = day, score
		tp.Count++
		points := total >= revMinTotal
		if points {
			for _, e := range s.RevisionLog() {
				if e.Kind == "done" && e.ID == id && e.Date == day && e.Points {
					points = false
				}
			}
		}
		e := RevisionEntry{At: now.Format(scoreStamp), Date: day, ID: id, Title: tp.Title, Kind: "done", Score: score,
			Correct: correct, Total: total, Step: tp.Step, NextDue: tp.Due, Points: points}
		if err := s.saveTopics(t); err != nil {
			return Topic{}, e, err
		}
		return *tp, e, s.appendRevision(e)
	}
	return Topic{}, RevisionEntry{}, fmt.Errorf("%w: %s is not scheduled", ErrRevision, id)
}

// RevisionDue is the topics due today or earlier: most overdue first, then learned first.
func (s *Store) RevisionDue(now time.Time) []Topic {
	day := now.Format(isoDate)
	var out []Topic
	for _, t := range s.Topics() {
		if t.Due <= day {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Due != out[j].Due {
			return out[i].Due < out[j].Due
		}
		if out[i].Learned != out[j].Learned {
			return out[i].Learned < out[j].Learned
		}
		return out[i].ID < out[j].ID
	})
	if out == nil {
		out = []Topic{}
	}
	return out
}

// RevisionsPerDay counts the scoring revisions per day.
func (s *Store) RevisionsPerDay() map[string]int {
	out := map[string]int{}
	for _, e := range s.RevisionLog() {
		if e.Kind == "done" && e.Points {
			out[e.Date]++
		}
	}
	return out
}

// RevisionSummary is what the home pages show: the next topic, how many are due, done today.
type RevisionSummary struct {
	Next      *Topic `json:"next"`
	Due       int    `json:"due"`
	DoneToday int    `json:"done_today"`
	Topics    int    `json:"topics"`
	Points    int    `json:"points"`
}

// AddRevision folds revisions into the Activity: per-day counts for the score and the summary.
func (a *Activity) AddRevision(s *Store, now time.Time) {
	for k, n := range s.RevisionsPerDay() {
		d := a.Days[k]
		d.Revisions = n
		a.Days[k] = d
	}
	due := s.RevisionDue(now)
	r := RevisionSummary{Due: len(due), DoneToday: a.Days[a.Today].Revisions, Topics: len(s.Topics()), Points: scoreRevisionPts}
	if len(due) > 0 {
		r.Next = &due[0]
	}
	a.Revision = r
}

// TopicWords is a note's current word count (Rebuild banks compares it with the count its questions were written from).
func (s *Store) TopicWords(rel string) int {
	b, err := os.ReadFile(filepath.Join(s.Root, rel))
	if err != nil {
		return 0
	}
	return countWords(string(b))
}
