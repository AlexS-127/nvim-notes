package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// New words are the words you add to notes in the folders you chose to track, so
// writing you did yourself stays apart from text pasted or generated elsewhere.
// Every score recording pass compares each tracked note's word count with the count
// stored last time and logs any increase against today. Deleting words lowers the
// stored count without taking anything off, so rewriting a passage counts again but
// reverting does not. Task lines (separately scored), fenced code and anything that is
// not a letter or number do not count. A note that appears in a tracked folder (new
// file, rename) counts in full; notes already there when a folder starts being tracked
// are only baselined.
const (
	wordsConfig = ".words.json"       // {"tracked": ["daily", "inbox.md", ...], "excluded": ["lat101/definitions.md", ...]}
	wordsState  = ".words_state.json" // last seen count per tracked note
	wordsLog    = ".words_log.jsonl"  // {at, date, file, words} per increase
)

var wordsMu sync.Mutex

// wordsTracked are the notes-relative folders (or single notes) whose writing counts.
// Excluded are notes or folders inside a tracked entry whose writing does not count.
type wordsTracked struct {
	Tracked  []string `json:"tracked"`
	Excluded []string `json:"excluded,omitempty"`
}

type wordsSnapshot struct {
	Dirs  []string       `json:"dirs"`               // what was tracked when the counts were taken
	Excl  []string       `json:"excluded,omitempty"` // what was excluded when the counts were taken
	Files map[string]int `json:"files"`              // note path -> words
}

// countWords counts the words in a note's prose.
func countWords(text string) int {
	n, fenced := 0, false
	for _, l := range strings.Split(text, "\n") {
		if fenceRe.MatchString(l) {
			fenced = !fenced
			continue
		}
		if fenced || checkboxRe.MatchString(strings.TrimRight(l, "\r")) {
			continue
		}
		for _, w := range strings.Fields(l) {
			if strings.IndexFunc(w, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }) >= 0 {
				n++
			}
		}
	}
	return n
}

func (s *Store) readWordsJSON(name string, v any) {
	if b, err := os.ReadFile(filepath.Join(s.Root, name)); err == nil {
		json.Unmarshal(b, v)
	}
}

func (s *Store) writeWordsJSON(name string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.Root, name+".tmp")
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(s.Root, name))
}

// cleanWordsPaths trims, de-duplicates and sorts notes-relative paths.
func cleanWordsPaths(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, t := range in {
		t = strings.Trim(filepath.ToSlash(filepath.Clean(strings.TrimSpace(t))), "/")
		if t != "" && t != "." && !strings.HasPrefix(t, "..") && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

// TrackedWords lists the tracked folders and notes, cleaned and sorted.
func (s *Store) TrackedWords() []string {
	var c wordsTracked
	s.readWordsJSON(wordsConfig, &c)
	return cleanWordsPaths(c.Tracked)
}

// ExcludedWords lists the notes and folders inside tracked entries that do not count.
func (s *Store) ExcludedWords() []string {
	var c wordsTracked
	s.readWordsJSON(wordsConfig, &c)
	return cleanWordsPaths(c.Excluded)
}

// SetTrackedWords saves the tracked list, keeping the exclusions.
func (s *Store) SetTrackedWords(dirs []string) error {
	wordsMu.Lock()
	defer wordsMu.Unlock()
	var c wordsTracked
	s.readWordsJSON(wordsConfig, &c)
	c.Tracked = dirs
	return s.writeWordsJSON(wordsConfig, c)
}

// SetExcludedWords saves the exclusion list, keeping the tracked list.
func (s *Store) SetExcludedWords(paths []string) error {
	wordsMu.Lock()
	defer wordsMu.Unlock()
	var c wordsTracked
	s.readWordsJSON(wordsConfig, &c)
	c.Excluded = paths
	return s.writeWordsJSON(wordsConfig, c)
}

func inTracked(rel string, tracked []string) bool {
	for _, t := range tracked {
		if rel == t || rel == t+".md" || strings.HasPrefix(rel, t+"/") {
			return true
		}
	}
	return false
}

// wordsCounted reports whether rel is in a tracked entry and not excluded.
func wordsCounted(rel string, tracked, excluded []string) bool {
	return inTracked(rel, tracked) && !inTracked(rel, excluded)
}

// RecordWords logs the words added to tracked notes since the last call and returns
// them per file.
func (s *Store) RecordWords(now time.Time) map[string]int {
	wordsMu.Lock()
	defer wordsMu.Unlock()
	tracked, excluded := s.TrackedWords(), s.ExcludedWords()
	var prev wordsSnapshot
	s.readWordsJSON(wordsState, &prev)
	known := map[string]bool{}
	for _, d := range prev.Dirs {
		known[d] = true
	}
	next := wordsSnapshot{Dirs: tracked, Excl: excluded, Files: map[string]int{}}
	added := map[string]int{}
	for _, rel := range s.Files() {
		if !wordsCounted(rel, tracked, excluded) {
			continue
		}
		b, err := s.Read(rel)
		if err != nil {
			if old, ok := prev.Files[rel]; ok {
				next.Files[rel] = old // unreadable for now: keep the old count
			}
			continue
		}
		n := countWords(string(b))
		next.Files[rel] = n
		old, seen := prev.Files[rel]
		if !seen && (!s.wordsEntryKnown(rel, tracked, known) || inTracked(rel, prev.Excl)) {
			continue // just tracked or just un-excluded: baseline, don't credit
		}
		if n > old {
			added[rel] = n - old
		}
	}
	if len(added) > 0 {
		if f, err := os.OpenFile(filepath.Join(s.Root, wordsLog), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			for rel, n := range added {
				line, _ := json.Marshal(map[string]any{"at": now.Format(scoreStamp), "date": now.Format(isoDate), "file": rel, "words": n})
				f.Write(append(line, '\n'))
			}
			f.Close()
		}
	}
	if !sameWordsSnapshot(prev, next) {
		s.writeWordsJSON(wordsState, next)
	}
	return added
}

// wordsEntryKnown reports whether the tracked entry containing rel was already tracked
// when the stored counts were taken.
func (s *Store) wordsEntryKnown(rel string, tracked []string, known map[string]bool) bool {
	for _, t := range tracked {
		if inTracked(rel, []string{t}) {
			return known[t]
		}
	}
	return false
}

func sameWordsSnapshot(a, b wordsSnapshot) bool {
	if len(a.Dirs) != len(b.Dirs) || len(a.Excl) != len(b.Excl) || len(a.Files) != len(b.Files) {
		return false
	}
	for i := range a.Dirs {
		if a.Dirs[i] != b.Dirs[i] {
			return false
		}
	}
	for i := range a.Excl {
		if a.Excl[i] != b.Excl[i] {
			return false
		}
	}
	for k, v := range b.Files {
		if o, ok := a.Files[k]; !ok || o != v {
			return false
		}
	}
	return true
}

// WordsPerDay sums the words log by day. A missing or malformed log is just empty.
func (s *Store) WordsPerDay() map[string]int {
	out := map[string]int{}
	f, err := os.Open(filepath.Join(s.Root, wordsLog))
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e struct {
			Date  string `json:"date"`
			Words int    `json:"words"`
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Words <= 0 {
			continue
		}
		if _, err := time.Parse(isoDate, e.Date); err == nil {
			out[e.Date] += e.Words
		}
	}
	return out
}

// AddWords folds per-day new word counts into an Activity.
func (a *Activity) AddWords(words map[string]int, now time.Time) {
	week := weekStart(now)
	for k, n := range words {
		d := a.Days[k]
		d.Words = n
		a.Days[k] = d
		a.WordsTotal += n
		if t, _ := time.ParseInLocation(isoDate, k, now.Location()); !t.Before(week) && !t.After(now) {
			a.WordsWeek += n
		}
	}
	a.WordsToday = words[a.Today]
}
