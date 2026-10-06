package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestRoutine(t *testing.T) {
	s := newTestStore(t, map[string]string{"inbox.md": "# Inbox\n"})
	now := time.Date(2026, 10, 7, 7, 0, 0, 0, time.Local)
	st := s.Routine(now)
	if len(st.Items) != len(defaultRoutine) || !st.Show || st.Done != 0 {
		t.Fatalf("fresh routine %+v", st)
	}
	if len(s.RoutineItems()) != len(defaultRoutine) { // defaults written out, read back
		t.Fatal("config not written")
	}
	if _, err := s.TickRoutine("forecast", true, now); err == nil {
		t.Fatal("the forecast item was ticked without a score")
	}
	if _, err := s.TickRoutine("nope", true, now); err == nil {
		t.Fatal("unknown item ticked")
	}
	s.TickRoutine("shower", true, now.Add(time.Minute))
	s.TickRoutine("brush", true, now.Add(2*time.Minute)) // label prefix
	s.TickRoutine("2", false, now.Add(3*time.Minute))    // number: untick teeth
	st, _ = s.TickRoutine("shower", true, now.Add(4*time.Minute))
	if st.Done != 1 {
		t.Fatalf("done %d", st.Done)
	}
	for _, id := range []string{"teeth", "breakfast", "water", "bed"} {
		s.TickRoutine(id, true, now.Add(5*time.Minute))
	}
	if st = s.Routine(now); st.Complete || !st.Show {
		t.Fatal("complete before the forecast")
	}
	st, err := s.RoutineForecast(40, 5, now.Add(10*time.Minute))
	if err != nil || !st.Complete || st.Show || st.Forecast.Strike != 40 || st.Forecast.Conf != routineConf || st.Forecast.Total != 5 {
		t.Fatalf("forecast %+v %v", st, err)
	}
	s.RoutineForecast(45, 9, now.Add(11*time.Minute)) // the last one counts, no second tick
	a := s.FullActivity(now.Add(time.Hour), 1)
	sc := a.Scores["2026-10-07"]
	want := len(defaultRoutine)*scoreRoutinePts + scoreRoutineBonus
	if sc.RoutinePts != want || sc.Total != want || a.Routine.Forecast.Strike != 45 {
		t.Fatalf("score %+v", sc)
	}
	if line := a.ScoreLine; line[len(line)-1].Total != want {
		t.Fatalf("curve %v", line)
	}
	// next day: ended early, items done score, no bonus
	day2 := now.AddDate(0, 0, 1)
	s.TickRoutine("shower", true, day2)
	if st, _ = s.EndRoutine(day2); !st.Ended || st.Show || st.Complete {
		t.Fatalf("end %+v", st)
	}
	if sc := s.FullActivity(day2.Add(time.Hour), 1).Scores["2026-10-08"]; sc.RoutinePts != scoreRoutinePts {
		t.Fatalf("ended day %+v", sc)
	}
}

func TestRoutineCommand(t *testing.T) {
	s := newTestStore(t, map[string]string{"inbox.md": "# Inbox\n"})
	now := time.Date(2026, 10, 7, 7, 0, 0, 0, time.Local)
	var out, errb bytes.Buffer
	if runRoutineCommand(s, []string{"tick", "shower"}, false, &out, &errb, now) != 0 || !strings.Contains(out.String(), "1 [x] Shower") {
		t.Fatalf("tick: %q %q", out.String(), errb.String())
	}
	out.Reset()
	if runRoutineCommand(s, []string{"forecast", "30"}, false, &out, &errb, now) != 0 || !strings.Contains(out.String(), "80% sure: 30") {
		t.Fatalf("forecast: %q %q", out.String(), errb.String())
	}
	out.Reset()
	if runRoutineCommand(s, []string{"end"}, false, &out, &errb, now) != 0 || !strings.Contains(out.String(), "Routine ended: 2 of 6") {
		t.Fatalf("end: %q", out.String())
	}
}
