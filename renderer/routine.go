package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Morning routine. The items are in .routine/routine.json (edit it to add, remove or reorder
// them; written with the defaults the first time it is read). What you do each day is the
// append-only .routine/log.jsonl: ticks, unticks, the day's forecast ("call option": the score
// you are 80% sure to reach today), "complete" when every item is done, and "end" when you stop
// the routine early. The routine shows on the home pages until it is complete or ended. Each item
// done scores scoreRoutinePts, a complete routine scoreRoutineBonus (score.go). The forecasts are
// kept for the score market (branch market) as training data and a live input.

const (
	routineDir      = ".routine"
	routineConfig   = "routine.json"
	routineLogFile  = "log.jsonl"
	routineForecast = "forecast" // item kind: asks for the forecast instead of a tick
	routineConf     = 0.8        // the forecast is the score you are this sure to reach
)

// RoutineItem is one step of the routine.
type RoutineItem struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Kind  string `json:"kind,omitempty"` // "" (tick) or "forecast"
}

var defaultRoutine = []RoutineItem{
	{ID: "shower", Label: "Shower"},
	{ID: "teeth", Label: "Brush teeth"},
	{ID: "breakfast", Label: "Breakfast"},
	{ID: "water", Label: "Drink water"},
	{ID: "bed", Label: "Make bed"},
	{ID: "forecast", Label: "Buy a call option: today's score, 80% sure", Kind: routineForecast},
}

// RoutineEntry is one line of the log.
type RoutineEntry struct {
	At     string  `json:"at"` // local time, 2006-01-02T15:04:05
	Date   string  `json:"date"`
	Kind   string  `json:"kind"` // tick, untick, forecast, complete, end
	Item   string  `json:"item,omitempty"`
	Strike int     `json:"strike,omitempty"` // forecast: the score
	Conf   float64 `json:"conf,omitempty"`   // forecast: how sure (0.8)
	Total  int     `json:"total,omitempty"`  // forecast: the score when it was made
}

var (
	routineMu  sync.Mutex
	ErrRoutine = errors.New("routine")
)

func (s *Store) routinePath(name string) string { return filepath.Join(s.Root, routineDir, name) }

// RoutineItems is the routine as configured; the defaults are written out the first time.
func (s *Store) RoutineItems() []RoutineItem {
	var cfg struct {
		Items []RoutineItem `json:"items"`
	}
	b, err := os.ReadFile(s.routinePath(routineConfig))
	if os.IsNotExist(err) {
		cfg.Items = defaultRoutine
		if os.MkdirAll(s.routinePath(""), 0o755) == nil {
			out, _ := json.MarshalIndent(cfg, "", "  ")
			_ = writeAtomic(s.routinePath(routineConfig), append(out, '\n'))
		}
		return append([]RoutineItem{}, defaultRoutine...)
	}
	if json.Unmarshal(b, &cfg) != nil {
		return append([]RoutineItem{}, defaultRoutine...) // a broken file shouldn't hide the routine
	}
	var items []RoutineItem
	for _, it := range cfg.Items {
		if it.ID = strings.TrimSpace(it.ID); it.ID != "" {
			if it.Label == "" {
				it.Label = it.ID
			}
			items = append(items, it)
		}
	}
	return items
}

// RoutineLog is every logged routine event, oldest first.
func (s *Store) RoutineLog() []RoutineEntry {
	var out []RoutineEntry
	readTimedLog(s.routinePath(routineLogFile), func(b []byte) {
		var e RoutineEntry
		if json.Unmarshal(b, &e) == nil && e.Date != "" && e.Kind != "" {
			out = append(out, e)
		}
	})
	return out
}

func (s *Store) appendRoutine(es ...RoutineEntry) error {
	if err := os.MkdirAll(s.routinePath(""), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(s.routinePath(routineLogFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, e := range es {
		line, _ := json.Marshal(e)
		if _, err := f.Write(append(line, '\n')); err != nil {
			return err
		}
	}
	return nil
}

// routineDay is what the log says about one day.
type routineDay struct {
	done     map[string]bool
	order    []string // items done, first tick first
	forecast *RoutineEntry
	complete bool
	ended    bool
}

func (d *routineDay) apply(e RoutineEntry) {
	switch e.Kind {
	case "tick", "forecast":
		if !d.done[e.Item] {
			d.done[e.Item] = true
			d.order = append(d.order, e.Item)
		}
		if e.Kind == "forecast" {
			f := e
			d.forecast = &f
		}
	case "untick":
		delete(d.done, e.Item)
	case "complete":
		d.complete = true
	case "end":
		d.ended = true
	}
}

func (s *Store) routineDays() map[string]*routineDay {
	out := map[string]*routineDay{}
	for _, e := range s.RoutineLog() {
		d := out[e.Date]
		if d == nil {
			d = &routineDay{done: map[string]bool{}}
			out[e.Date] = d
		}
		d.apply(e)
	}
	return out
}

// RoutinePerDay is, per day, how many items were done and whether the routine was completed.
func (s *Store) RoutinePerDay() map[string]RoutineCount {
	out := map[string]RoutineCount{}
	for k, d := range s.routineDays() {
		out[k] = RoutineCount{Items: len(d.done), Complete: d.complete}
	}
	return out
}

// RoutineCount is one day's routine for the score.
type RoutineCount struct {
	Items    int
	Complete bool
}

// RoutineState is today's routine as the home pages show it.
type RoutineState struct {
	Date     string             `json:"date"`
	Items    []RoutineItemState `json:"items"`
	Done     int                `json:"done"`
	Complete bool               `json:"complete"`
	Ended    bool               `json:"ended"`
	Show     bool               `json:"show"` // neither complete nor ended
	Forecast *RoutineEntry      `json:"forecast"`
	Conf     float64            `json:"conf"`
	ItemPts  int                `json:"item_pts"`
	Bonus    int                `json:"bonus"`
}

type RoutineItemState struct {
	RoutineItem
	Done bool `json:"done"`
}

// Routine is today's routine.
func (s *Store) Routine(now time.Time) RoutineState {
	day := now.Format(isoDate)
	d := s.routineDays()[day]
	if d == nil {
		d = &routineDay{done: map[string]bool{}}
	}
	st := RoutineState{Date: day, Items: []RoutineItemState{}, Complete: d.complete, Ended: d.ended, Forecast: d.forecast,
		Conf: routineConf, ItemPts: scoreRoutinePts, Bonus: scoreRoutineBonus}
	for _, it := range s.RoutineItems() {
		st.Items = append(st.Items, RoutineItemState{it, d.done[it.ID]})
		if d.done[it.ID] {
			st.Done++
		}
	}
	st.Show = !st.Complete && !st.Ended
	return st
}

// findRoutineItem resolves an item by id, a label prefix or its number (1-based).
func findRoutineItem(items []RoutineItem, ref string) (RoutineItem, error) {
	ref = strings.ToLower(strings.TrimSpace(ref))
	if n, err := strconv.Atoi(ref); err == nil && n >= 1 && n <= len(items) {
		return items[n-1], nil
	}
	for _, it := range items {
		if strings.ToLower(it.ID) == ref {
			return it, nil
		}
	}
	var found []RoutineItem
	for _, it := range items {
		if ref != "" && strings.HasPrefix(strings.ToLower(it.Label), ref) {
			found = append(found, it)
		}
	}
	if len(found) == 1 {
		return found[0], nil
	}
	return RoutineItem{}, fmt.Errorf("%w: no routine item %q", ErrRoutine, ref)
}

// completeIfDone appends "complete" when this tick finished every item (once a day).
func (s *Store) completeIfDone(now time.Time) {
	st := s.Routine(now)
	if !st.Complete && len(st.Items) > 0 && st.Done == len(st.Items) {
		_ = s.appendRoutine(RoutineEntry{At: now.Format(scoreStamp), Date: st.Date, Kind: "complete"})
	}
}

// TickRoutine ticks (or unticks) an item. A forecast item needs RoutineForecast instead.
func (s *Store) TickRoutine(ref string, done bool, now time.Time) (RoutineState, error) {
	routineMu.Lock()
	defer routineMu.Unlock()
	it, err := findRoutineItem(s.RoutineItems(), ref)
	if err != nil {
		return RoutineState{}, err
	}
	st := s.Routine(now)
	was := false
	for _, x := range st.Items {
		if x.ID == it.ID {
			was = x.Done
		}
	}
	if done && it.Kind == routineForecast && !was {
		return RoutineState{}, fmt.Errorf("%w: %s needs a score: notesview routine forecast N", ErrRoutine, it.Label)
	}
	if was == done {
		return st, nil
	}
	kind := map[bool]string{true: "tick", false: "untick"}[done]
	if err := s.appendRoutine(RoutineEntry{At: now.Format(scoreStamp), Date: st.Date, Kind: kind, Item: it.ID}); err != nil {
		return RoutineState{}, err
	}
	if done {
		s.completeIfDone(now)
	}
	return s.Routine(now), nil
}

// RoutineForecast records today's call option: the score you are 80% sure to reach. Making it
// again replaces it (the last one counts); total is today's score at that moment.
func (s *Store) RoutineForecast(strike, total int, now time.Time) (RoutineState, error) {
	if strike < 0 {
		return RoutineState{}, fmt.Errorf("%w: the forecast is a score, 0 or more", ErrRoutine)
	}
	routineMu.Lock()
	defer routineMu.Unlock()
	item := routineForecast
	for _, it := range s.RoutineItems() {
		if it.Kind == routineForecast {
			item = it.ID
			break
		}
	}
	day := now.Format(isoDate)
	if err := s.appendRoutine(RoutineEntry{At: now.Format(scoreStamp), Date: day, Kind: "forecast", Item: item, Strike: strike, Conf: routineConf, Total: total}); err != nil {
		return RoutineState{}, err
	}
	s.completeIfDone(now)
	return s.Routine(now), nil
}

// EndRoutine stops today's routine early: it stops showing; items done still score, no bonus.
func (s *Store) EndRoutine(now time.Time) (RoutineState, error) {
	routineMu.Lock()
	defer routineMu.Unlock()
	st := s.Routine(now)
	if st.Ended || st.Complete {
		return st, nil
	}
	if err := s.appendRoutine(RoutineEntry{At: now.Format(scoreStamp), Date: st.Date, Kind: "end"}); err != nil {
		return RoutineState{}, err
	}
	return s.Routine(now), nil
}

// AddRoutine folds the routine into the Activity: per-day counts for the score, and today's state.
func (a *Activity) AddRoutine(s *Store, now time.Time) {
	for k, c := range s.RoutinePerDay() {
		d := a.Days[k]
		d.Routine, d.RoutineComplete = c.Items, c.Complete
		a.Days[k] = d
	}
	a.Routine = s.Routine(now)
}
