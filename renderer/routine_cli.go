package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// scoreNow is today's score so far (stored with a forecast), from the rebuilt curve.
func (s *Store) scoreNow(now time.Time) int {
	if pts := s.ScoreDay(now.Format(isoDate), now).Points; len(pts) > 0 {
		return pts[len(pts)-1].Total
	}
	return 0
}

// runRoutineCommand implements `notesview routine …`; it works on the files directly.
func runRoutineCommand(store *Store, args []string, asJSON bool, stdout, stderr io.Writer, now time.Time) int {
	fail := func(err error) int {
		fmt.Fprintln(stderr, "notesview:", strings.TrimPrefix(err.Error(), "routine: "))
		return 1
	}
	var pos []string
	for _, a := range args {
		if a == "--json" || a == "-json" {
			asJSON = true
		} else {
			pos = append(pos, a)
		}
	}
	sub := "show"
	if len(pos) > 0 {
		sub, pos = pos[0], pos[1:]
	}
	var st RoutineState
	var err error
	switch sub {
	case "show", "status":
		st = store.Routine(now)
	case "tick", "done", "untick", "undo":
		if len(pos) != 1 {
			return fail(fmt.Errorf("usage: notesview routine %s ITEM (id, number or start of the label)", sub))
		}
		st, err = store.TickRoutine(pos[0], sub == "tick" || sub == "done", now)
	case "forecast", "call":
		n := -1
		if len(pos) == 1 {
			n, err = strconv.Atoi(pos[0])
		}
		if len(pos) != 1 || err != nil || !in1to5(n) {
			return fail(fmt.Errorf("usage: notesview routine forecast 1-5  (how productive you expect today to be)"))
		}
		st, err = store.RoutineForecast(n, store.scoreNow(now), now)
	case "end":
		st, err = store.EndRoutine(now)
	default:
		return fail(fmt.Errorf("usage: notesview routine [show|tick ITEM|untick ITEM|forecast SCORE|end]"))
	}
	if err != nil {
		return fail(err)
	}
	if asJSON {
		return printJSON(stdout, st)
	}
	for i, it := range st.Items {
		box := "[ ]"
		if it.Done {
			box = "[x]"
		}
		label := it.Label
		if it.Kind == routineForecast && st.Forecast != nil {
			label += fmt.Sprintf(": %d", max(st.Forecast.Rating, st.Forecast.Strike))
		}
		fmt.Fprintf(stdout, "%d %s %s\n", i+1, box, label)
	}
	switch {
	case st.Complete:
		fmt.Fprintf(stdout, "Routine done (+%d)\n", st.Done*scoreRoutinePts+scoreRoutineBonus)
	case st.Ended:
		fmt.Fprintf(stdout, "Routine ended: %d of %d (+%d)\n", st.Done, len(st.Items), st.Done*scoreRoutinePts)
	}
	return 0
}
