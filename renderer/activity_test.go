package main

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestDoneStamp(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local)
	got := stampDone("- [x] read ch 5 #act-200\r\n", now)
	if got != "- [x] read ch 5 #act-200 ✅ 2026-10-01\r\n" {
		t.Errorf("stamp: %q", got)
	}
	if stampDone(got, now) != got {
		t.Error("stamping twice should be a no-op")
	}
	if un := unstampDone(got); un != "- [x] read ch 5 #act-200\r\n" {
		t.Errorf("unstamp: %q", un)
	}
}

func TestToggleStamps(t *testing.T) {
	s := newTestStore(t, map[string]string{"a.md": "- [ ] one\n- [ ] two\n"})
	if err := s.ToggleCheckbox("a.md", 1); err != nil {
		t.Fatal(err)
	}
	b, _ := s.Read("a.md")
	if !strings.HasPrefix(string(b), "- [x] one ✅ "+time.Now().Format(isoDate)+"\n- [ ] two") {
		t.Fatalf("got %q", b)
	}
	tasks := s.CollectTasks(TaskQuery{All: true})
	if tasks[0].DoneDate == "" || tasks[0].Display != "one" {
		t.Errorf("stamp should be parsed and hidden: %+v", tasks[0])
	}
	s.ToggleCheckbox("a.md", 1)
	b, _ = s.Read("a.md")
	if string(b) != "- [ ] one\n- [ ] two\n" {
		t.Errorf("unticking should remove the stamp: %q", b)
	}
}

func TestBuildActivity(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local) // Thursday
	s := newTestStore(t, map[string]string{
		"inbox.md":            "- [x] a ✅ 2026-09-30 _(Sep 29 09:00)_\n- [x] b ✅ 2026-10-01 _(Oct 01 10:00)_\n- [x] old\n- [ ] c _(Oct 01 11:00)_\n",
		"daily/2026-09-30.md": "- [>] carry → [[2026-10-01]]\n- [x] d ✅ 2026-09-30\n",
		"daily/2026-10-01.md": "- [ ] carry\n- [ ] fresh\n",
	})
	a := BuildActivity(s.CollectTasks(TaskQuery{All: true, Now: now}), now, 2)
	if a.TotalDone != 4 || a.Earlier != 1 || a.Open != 3 {
		t.Errorf("totals: %+v", a)
	}
	if a.Days["2026-09-30"].Done != 2 || a.Days["2026-10-01"].Done != 1 {
		t.Errorf("days: %+v", a.Days)
	}
	// created: a (9/29), b, c (10/1), fresh (10/1); the carried copy and moved line don't count; d has no date in a daily... it does: 9/30
	if a.TotalCreated != 5 || a.Days["2026-10-01"].Created != 3 {
		t.Errorf("created: %d %+v", a.TotalCreated, a.Days)
	}
	if a.Streak != 2 || a.BestStreak != 2 {
		t.Errorf("streak %d best %d", a.Streak, a.BestStreak)
	}
	w := a.Weeks[1]
	if w.Start != "2026-09-28" || w.Days != 4 || w.Done != 3 || w.Total != 4 {
		t.Errorf("week: %+v", w)
	}
	// Thursday 10/1 and Wednesday 9/30 are in range; 3 done on Wed (2) and Thu (1)
	if a.BestWeekday != 2 || a.Weekdays[2].Done != 2 || a.Weekdays[3].Done != 1 || a.Weekdays[2].Count != 2 {
		t.Errorf("weekdays: best %d %+v", a.BestWeekday, a.Weekdays)
	}
	// finished days 9/29..9/30 -> 0 done and 2 done; today (1) is excluded
	d := a.Distribution
	if d.Days != 1 || d.Today != 1 { // first completion was 9/30
		t.Errorf("distribution: %+v", d)
	}
}

func TestDistributionThresholds(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.Local)
	days := map[string]DayStat{}
	// 10/1..10/9: 0,1,1,2,2,2,3,3,5 done; today 1
	for i, n := range []int{0, 1, 1, 2, 2, 2, 3, 3, 5} {
		days[time.Date(2026, 10, 1+i, 0, 0, 0, 0, time.Local).Format(isoDate)] = DayStat{Done: n}
	}
	days["2026-10-01"] = DayStat{Created: 1} // first done day is 10/2, so the 0 on 10/1 is not counted
	days["2026-10-10"] = DayStat{Done: 1}
	d := buildDistribution(days, now)
	if d.Days != 8 || d.Median != 2 || d.Counts[2] != 3 || d.Counts[5] != 1 || d.Today != 1 {
		t.Fatalf("%+v", d)
	}
	if d.ToMedian != 2 { // needs 3 to beat a median of 2
		t.Errorf("to median %d", d.ToMedian)
	}
	if want := int(math.Ceil(d.Mean+d.Sigma)) - 1; d.ToSigma != want {
		t.Errorf("to sigma %d want %d (mean %.2f sigma %.2f)", d.ToSigma, want, d.Mean, d.Sigma)
	}
}
