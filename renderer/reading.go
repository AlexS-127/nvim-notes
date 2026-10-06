package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Reading list and log. Books live in .reading/books.json (to read, reading, read); every
// batch of pages read is one line in the append-only .reading/log.jsonl, which is what
// scores (scoreReadPagesPer in score.go), so removing a book or changing its page count
// never changes past points. Both are hidden files, committed with the notes.

const (
	readDir      = ".reading"
	readBooks    = "books.json"
	readLogFile  = "log.jsonl"
	readToRead   = "to-read"
	readReading  = "reading"
	readFinished = "read"
)

// Book is one entry of the reading list.
type Book struct {
	ID       int    `json:"id"`
	Title    string `json:"title"`
	Author   string `json:"author"`
	Pages    int    `json:"pages,omitempty"` // 0 = unknown, so no percentage
	Page     int    `json:"page"`            // pages read so far
	Status   string `json:"status"`          // to-read, reading, read
	Added    string `json:"added"`           // date
	Started  string `json:"started,omitempty"`
	Finished string `json:"finished,omitempty"`
	Pct      int    `json:"pct"` // -1 without a page count
}

// ReadEntry is one line of the log: pages read (negative for a correction) and where that left the book.
// A baseline entry only moves the bookmark (pages read before the book was logged) and never scores.
type ReadEntry struct {
	At       string `json:"at"` // local time, 2006-01-02T15:04:05
	Date     string `json:"date"`
	Book     int    `json:"book"`
	Title    string `json:"title"`
	Pages    int    `json:"pages"`
	Page     int    `json:"page"`
	Baseline bool   `json:"baseline,omitempty"`
}

var (
	readMu     sync.Mutex
	ErrReading = errors.New("reading")
)

func (s *Store) readPath(name string) string { return filepath.Join(s.Root, readDir, name) }

func (b *Book) fill() {
	b.Pct = -1
	if b.Pages > 0 {
		b.Pct = min(100, b.Page*100/b.Pages)
	}
}

// Books is the reading list in the order added.
func (s *Store) Books() []Book {
	var list []Book
	if b, err := os.ReadFile(s.readPath(readBooks)); err == nil {
		_ = json.Unmarshal(b, &list)
	}
	if list == nil {
		list = []Book{}
	}
	for i := range list {
		list[i].fill()
	}
	return list
}

func (s *Store) saveBooks(list []Book) error {
	if err := os.MkdirAll(s.readPath(""), 0o755); err != nil {
		return err
	}
	for i := range list {
		list[i].Pct = 0 // derived, not stored
	}
	b, _ := json.MarshalIndent(list, "", "  ")
	return writeAtomic(s.readPath(readBooks), append(b, '\n'))
}

// FindBook resolves an id or a case-insensitive title prefix; "" is the book being read that was started last.
func FindBook(list []Book, ref string) (int, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		best := -1
		for i, b := range list {
			if b.Status == readReading && (best < 0 || b.Started >= list[best].Started) {
				best = i
			}
		}
		if best < 0 {
			return -1, fmt.Errorf("%w: not reading anything; name a book", ErrReading)
		}
		return best, nil
	}
	if n, err := strconv.Atoi(ref); err == nil {
		for i, b := range list {
			if b.ID == n {
				return i, nil
			}
		}
		return -1, fmt.Errorf("%w: no book %d", ErrReading, n)
	}
	found := -1
	for i, b := range list {
		if strings.HasPrefix(strings.ToLower(b.Title), strings.ToLower(ref)) {
			if found >= 0 {
				return -1, fmt.Errorf("%w: %q matches more than one book", ErrReading, ref)
			}
			found = i
		}
	}
	if found < 0 {
		return -1, fmt.Errorf("%w: no book %q", ErrReading, ref)
	}
	return found, nil
}

// AddBook puts a book on the to-read list. Title and author are required; pages may be 0.
func (s *Store) AddBook(title, author string, pages int, now time.Time) (Book, error) {
	title, author = strings.TrimSpace(title), strings.TrimSpace(author)
	if title == "" || author == "" {
		return Book{}, fmt.Errorf("%w: a book needs a title and an author", ErrReading)
	}
	if pages < 0 {
		return Book{}, fmt.Errorf("%w: pages must be positive", ErrReading)
	}
	readMu.Lock()
	defer readMu.Unlock()
	list := s.Books()
	id := 1
	for _, b := range list {
		id = max(id, b.ID+1)
	}
	b := Book{ID: id, Title: title, Author: author, Pages: pages, Status: readToRead, Added: now.Format(isoDate)}
	if err := s.saveBooks(append(list, b)); err != nil {
		return Book{}, err
	}
	b.fill()
	return b, nil
}

// LogPages records pages read. With to >= 0 it sets the current page instead (pages is then
// the difference, negative to correct an overcount). The book moves to reading, or to read
// when it reaches its last page.
func (s *Store) LogPages(ref string, pages, to int, now time.Time) (Book, ReadEntry, error) {
	return s.logPages(ref, pages, to, false, now)
}

// SetStartPage moves a book to the page you were already on (read before it was logged here),
// without scoring those pages.
func (s *Store) SetStartPage(ref string, page int, now time.Time) (Book, ReadEntry, error) {
	return s.logPages(ref, 0, max(0, page), true, now)
}

func (s *Store) logPages(ref string, pages, to int, baseline bool, now time.Time) (Book, ReadEntry, error) {
	readMu.Lock()
	defer readMu.Unlock()
	list := s.Books()
	i, err := FindBook(list, ref)
	if err != nil {
		return Book{}, ReadEntry{}, err
	}
	b := &list[i]
	if to >= 0 {
		pages = to - b.Page
	}
	if b.Pages > 0 && b.Page+pages > b.Pages {
		pages = b.Pages - b.Page // can't read past the last page
	}
	if b.Page+pages < 0 {
		pages = -b.Page
	}
	if pages == 0 {
		return Book{}, ReadEntry{}, fmt.Errorf("%w: no pages to log for %s (at page %d)", ErrReading, b.Title, b.Page)
	}
	b.Page += pages
	day := now.Format(isoDate)
	if b.Started == "" {
		b.Started = day
	}
	if b.Pages > 0 && b.Page >= b.Pages {
		b.Status, b.Finished = readFinished, day
	} else {
		b.Status, b.Finished = readReading, ""
	}
	e := ReadEntry{At: now.Format(scoreStamp), Date: day, Book: b.ID, Title: b.Title, Pages: pages, Page: b.Page, Baseline: baseline}
	line, _ := json.Marshal(e)
	if err := os.MkdirAll(s.readPath(""), 0o755); err != nil {
		return Book{}, ReadEntry{}, err
	}
	f, err := os.OpenFile(s.readPath(readLogFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return Book{}, ReadEntry{}, err
	}
	_, err = f.Write(append(line, '\n'))
	f.Close()
	if err != nil {
		return Book{}, ReadEntry{}, err
	}
	out := *b
	if err := s.saveBooks(list); err != nil {
		return Book{}, ReadEntry{}, err
	}
	out.fill()
	return out, e, nil
}

// BookPatch changes a book; nil fields are left alone.
type BookPatch struct {
	ID     int     `json:"id"`
	Title  *string `json:"title"`
	Author *string `json:"author"`
	Pages  *int    `json:"pages"`
	Status *string `json:"status"`
}

// UpdateBook edits a book or moves it between lists. Marking a book read does not log pages
// (log them to score them); moving it back clears its finished date.
func (s *Store) UpdateBook(p BookPatch, now time.Time) (Book, error) {
	readMu.Lock()
	defer readMu.Unlock()
	list := s.Books()
	i, err := FindBook(list, strconv.Itoa(p.ID))
	if err != nil {
		return Book{}, err
	}
	b := &list[i]
	if p.Title != nil && strings.TrimSpace(*p.Title) != "" {
		b.Title = strings.TrimSpace(*p.Title)
	}
	if p.Author != nil && strings.TrimSpace(*p.Author) != "" {
		b.Author = strings.TrimSpace(*p.Author)
	}
	if p.Pages != nil {
		if *p.Pages < 0 {
			return Book{}, fmt.Errorf("%w: pages must be positive", ErrReading)
		}
		b.Pages = *p.Pages
	}
	if p.Status != nil {
		day := now.Format(isoDate)
		switch *p.Status {
		case readToRead:
			b.Status, b.Finished = readToRead, ""
		case readReading:
			b.Status, b.Finished = readReading, ""
			if b.Started == "" {
				b.Started = day
			}
		case readFinished:
			b.Status, b.Finished = readFinished, day
			if b.Started == "" {
				b.Started = day
			}
		default:
			return Book{}, fmt.Errorf("%w: status is to-read, reading or read", ErrReading)
		}
	}
	out := *b
	if err := s.saveBooks(list); err != nil {
		return Book{}, err
	}
	out.fill()
	return out, nil
}

// DeleteBook removes a book from the list. Its logged pages, and their points, stay.
func (s *Store) DeleteBook(id int) error {
	readMu.Lock()
	defer readMu.Unlock()
	list := s.Books()
	i, err := FindBook(list, strconv.Itoa(id))
	if err != nil {
		return err
	}
	return s.saveBooks(append(list[:i], list[i+1:]...))
}

// ReadLog is every logged batch of pages that scores, oldest first (baseline entries left out).
func (s *Store) ReadLog() []ReadEntry {
	var out []ReadEntry
	readTimedLog(s.readPath(readLogFile), func(b []byte) {
		var e ReadEntry
		if json.Unmarshal(b, &e) == nil && e.Date != "" && e.Pages != 0 && !e.Baseline {
			out = append(out, e)
		}
	})
	return out
}

// PagesPerDay sums the pages logged per day (never below zero for a day).
func (s *Store) PagesPerDay() map[string]int {
	out := map[string]int{}
	for _, e := range s.ReadLog() {
		out[e.Date] += e.Pages
	}
	for k, v := range out {
		out[k] = max(0, v)
	}
	return out
}

// ReadingSummary is what the home pages show: the books being read, and pages read.
type ReadingSummary struct {
	Reading    []Book `json:"reading"` // most recently started first
	ToRead     int    `json:"to_read"`
	Read       int    `json:"read"`
	PagesToday int    `json:"pages_today"`
	PagesWeek  int    `json:"pages_week"`
	PagesTotal int    `json:"pages_total"`
	PerPage    int    `json:"pages_per_point"`
}

// AddReading folds pages per day into the Activity and fills its reading summary.
func (a *Activity) AddReading(s *Store, now time.Time) {
	week := weekStart(now)
	r := ReadingSummary{Reading: []Book{}, PerPage: scoreReadPagesPer}
	for k, n := range s.PagesPerDay() {
		d := a.Days[k]
		d.Pages = n
		a.Days[k] = d
		r.PagesTotal += n
		if t, _ := time.ParseInLocation(isoDate, k, now.Location()); !t.Before(week) && !t.After(now) {
			r.PagesWeek += n
		}
	}
	r.PagesToday = a.Days[a.Today].Pages
	for _, b := range s.Books() {
		switch b.Status {
		case readReading:
			r.Reading = append(r.Reading, b)
		case readToRead:
			r.ToRead++
		case readFinished:
			r.Read++
		}
	}
	sort.SliceStable(r.Reading, func(i, j int) bool { return r.Reading[i].Started > r.Reading[j].Started })
	a.Reading = r
}
