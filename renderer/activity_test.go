package main

import (
	"math"
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
	if string(b) != "- [ ] two\n\n## Done\n- [x] one ✅ "+time.Now().Format(isoDate)+"\n" {
		t.Fatalf("got %q", b)
	}
	var one Task
	for _, tk := range s.CollectTasks(TaskQuery{All: true}) {
		if tk.Display == "one" {
			one = tk
		}
	}
	if one.DoneDate == "" {
		t.Errorf("stamp should be parsed and hidden: %+v", one)
	}
	s.ToggleCheckbox("a.md", 4)
	b, _ = s.Read("a.md")
	if string(b) != "- [ ] two\n- [ ] one\n\n## Done\n" {
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

func TestAddStudy(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local) // Thursday
	s := newTestStore(t, map[string]string{
		quizLog: `{"date":"2026-10-01","seconds":300}` + "\n" + `not json` + "\n" +
			`{"date":"2026-10-01","seconds":60}` + "\n" + `{"date":"2026-09-20","seconds":120}` + "\n",
	})
	a := BuildActivity(nil, now, 2)
	a.AddStudy(s.StudySeconds(), now)
	if a.StudyToday != 360 || a.StudyWeek != 360 || a.StudyTotal != 480 || a.Days["2026-09-20"].Study != 120 {
		t.Errorf("study: %+v", a)
	}
	if a.Streak != 0 {
		t.Errorf("quiz time alone must not make a task streak: %d", a.Streak)
	}
}

func TestScoreFor(t *testing.T) {
	if s := scoreFor([4]int{}, 0, 0, 0, false); s.Total != 0 {
		t.Errorf("empty day: %+v", s)
	}
	// 3 done (15) + 2 made (4) + 10 min quiz (20) - 2 overdue (20)
	s := scoreFor([4]int{3}, 2, 600, 2, true)
	if s.DonePts != 15 || s.CreatedPts != 4 || s.StudyPts != 20 || s.OverduePts != -20 || s.Total != 19 || !s.Live {
		t.Errorf("score: %+v", s)
	}
	// done points depend on difficulty: 1 plain (5) + 2 easy (6) + 1 medium (6) + 1 hard (10)
	if s := scoreFor([4]int{1, 2, 1, 1}, 0, 0, 0, false); s.Done != 5 || s.DonePts != 27 {
		t.Errorf("difficulty points: %+v", s)
	}
	// each part is capped (100 + 30 + 100), the penalty at -100, and the total never goes below 0
	if s := scoreFor([4]int{20}, 20, 3*3600, 0, false); s.Total != 230 {
		t.Errorf("max: %+v", s)
	}
	if s := scoreFor([4]int{1}, 0, 0, 50, false); s.OverduePts != -100 || s.Total != 0 {
		t.Errorf("penalty cap and floor: %+v", s)
	}
}

func TestAddScores(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local)
	s := newTestStore(t, map[string]string{
		"inbox.md": "- [x] a ✅ 2026-09-30 @2026-09-29\n" + // done a day late: overdue at the end of 9/29 only
			"- [ ] b @2026-09-30\n" + // open since: overdue at the end of 9/30 and 10/1
			"- [x] c ✅ 2026-10-01 @2026-10-05\n" + // not due yet
			"- [ ] d @2026-10-09\n",
		quizLog: `{"date":"2026-10-01","seconds":1200}` + "\n",
	})
	tasks := s.CollectTasks(TaskQuery{All: true, Now: now})
	a := BuildActivity(tasks, now, 2)
	a.AddStudy(s.StudySeconds(), now)
	a.AddScores(tasks, now)
	if got := a.Scores["2026-09-30"]; got.Done != 1 || got.Overdue != 1 || got.Total != 0 { // 5 for a, -10 for b, floored at 0
		t.Errorf("9/30: %+v", got)
	}
	// 5 for c, 40 for quiz, -10 for b
	if got := a.Scores["2026-10-01"]; !got.Live || got.Total != 35 || got.Overdue != 1 {
		t.Errorf("today: %+v", got)
	}
	if _, ok := a.Scores["2026-09-29"]; ok {
		t.Error("a day with no activity should not be scored")
	}
}

func TestRecordScore(t *testing.T) {
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.Local)
	s := newTestStore(t, map[string]string{
		scoreLog: `{"at":"2026-09-30T20:00:00","score":40}` + "\n",
	})
	if p := s.RecordScore(now, 0); len(p) != 1 || p[0].Total != 0 { // yesterday's entry is not today's
		t.Fatalf("first of the day: %+v", p)
	}
	if p := s.RecordScore(now.Add(time.Minute), 0); len(p) != 1 { // unchanged: nothing new
		t.Errorf("unchanged score recorded again: %+v", p)
	}
	p := s.RecordScore(now.Add(time.Hour), 10)
	if len(p) != 2 || p[1].Total != 10 || p[1].At != "2026-10-01T10:00:00" {
		t.Errorf("change: %+v", p)
	}
	if h := scoreHints(); h.Done != "5 each (difficulty 1: 3, 2: 6, 3: 10), up to 100" || h.Study != "2 a minute, up to 100" {
		t.Errorf("hints: %+v", h)
	}
}
