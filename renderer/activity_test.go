package main

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

func TestDoneStamp(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local)
	got := stampDone("- [x] read ch 5 #act-200\r\n", now)
	if got != "- [x] read ch 5 #act-200 ✅ 2026-10-01 12:00\r\n" {
		t.Errorf("stamp: %q", got)
	}
	if stampDone(got, now) != got {
		t.Error("stamping twice should be a no-op")
	}
	if un := unstampDone(got); un != "- [x] read ch 5 #act-200\r\n" {
		t.Errorf("unstamp: %q", un)
	}
	// older date-only stamps still parse and unstamp
	if un := unstampDone("- [x] a ✅ 2026-10-01\n"); un != "- [x] a\n" {
		t.Errorf("unstamp date-only: %q", un)
	}
	for in, want := range map[string][3]string{
		"a ✅ 2026-10-01 09:05":     {"2026-10-01", "09:05", "a"},
		"a ✅ 2026-10-01":           {"2026-10-01", "", "a"},
		"a ✅ 2026-10-01 99:99":     {"2026-10-01", "", "a"},
		"a ✅ 2026-10-01 12:00 xyz": {"2026-10-01", "12:00", "a xyz"},
	} {
		if d, c, r := splitDoneStamp(in); [3]string{d, c, r} != want {
			t.Errorf("split %q: %q %q %q", in, d, c, r)
		}
	}
}

func TestToggleStamps(t *testing.T) {
	s := newTestStore(t, map[string]string{"a.md": "- [ ] one\n- [ ] two\n"})
	if err := s.ToggleCheckbox("a.md", 1); err != nil {
		t.Fatal(err)
	}
	b, _ := s.Read("a.md")
	if string(b) != "- [ ] two\n\n## Done\n- [x] one ✅ "+time.Now().Format(doneStampFmt)+"\n" {
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
	if len(a.Recent) != 1 || a.Recent[0].Text != "b" {
		t.Errorf("recent: %+v", a.Recent)
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

func TestQuizGains(t *testing.T) {
	s := newTestStore(t, map[string]string{
		quizLog: `{"date":"2026-10-01","seconds":60,"xp":35,"levels":1}` + "\n" + `{"date":"2026-10-01","seconds":60}` + "\n" +
			`{"date":"2026-10-01","xp":20}` + "\n" + `{"date":"2026-09-30","xp":9}` + "\n",
	})
	g := s.StudyGains()
	if g["2026-10-01"] != (QuizGain{55, 1}) || g["2026-09-30"] != (QuizGain{9, 0}) {
		t.Errorf("gains: %+v", g)
	}
	// 55 XP is 5 points, one level-up is scoreLevelPts; both add to the total
	sc := scoreFor([4]int{}, [4]int{}, 0, 0, 0, 0, false).withQuiz(55, 1)
	if sc.XPPts != 55/scoreXPPer || sc.LevelPts != scoreLevelPts || sc.Total != 55/scoreXPPer+scoreLevelPts {
		t.Errorf("quiz score: %+v", sc)
	}
	// through the whole pipeline
	a := BuildActivity(nil, time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local), 2)
	a.AddQuizGains(g)
	a.AddScores(nil, time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local))
	if a.Scores["2026-10-01"].Total != sc.Total {
		t.Errorf("activity score: %+v", a.Scores["2026-10-01"])
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

// Expected values are derived from the tunable constants in score.go, so retuning
// the score does not break these tests; they check the rules, not the numbers.
func clampInt(v, lo, hi int) int { return max(lo, min(hi, v)) }

func quizPts(seconds int) int {
	return int(float64(seconds) / 60 * scoreQuizPerMin)
}

func TestScoreFor(t *testing.T) {
	if s := scoreFor([4]int{}, [4]int{}, 0, 0, 0, 0, false); s.Total != 0 {
		t.Errorf("empty day: %+v", s)
	}
	// 3 done + 2 made + 10 min quiz - 2 overdue
	wantDone, wantMade, wantQuiz, wantOver := 3*scoreWorkflowPts[0], 2*scoreCreatedPts, quizPts(600), -2*scoreOverduePts
	s := scoreFor([4]int{3}, [4]int{}, 2, 600, 0, 2, true)
	if s.DonePts != wantDone || s.CreatedPts != wantMade || s.StudyPts != wantQuiz || s.OverduePts != wantOver ||
		s.Total != max(0, wantDone+wantMade+wantQuiz+wantOver) || !s.Live {
		t.Errorf("score: %+v", s)
	}
	// done points depend on difficulty: 1 plain + 2 easy + 1 medium + 1 hard (all workflow)
	wantDone = scoreWorkflowPts[0] + 2*scoreWorkflowPts[1] + scoreWorkflowPts[2] + scoreWorkflowPts[3]
	if s := scoreFor([4]int{1, 2, 1, 1}, [4]int{}, 0, 0, 0, 0, false); s.Done != 5 || s.DonePts != wantDone {
		t.Errorf("difficulty points: %+v", s)
	}
	// nothing is capped (a big day keeps scoring), but the total never goes below 0
	s = scoreFor([4]int{1000}, [4]int{}, 1000, 1000*3600, 1000*scoreWordsPer*1000, 0, false)
	if s.DonePts != 1000*scoreWorkflowPts[0] || s.CreatedPts != 1000*scoreCreatedPts || s.StudyPts != quizPts(1000*3600) || s.WordsPts != 1000*1000 {
		t.Errorf("uncapped: %+v", s)
	}
	if s := scoreFor([4]int{1}, [4]int{}, 0, 0, 0, 1000, false); s.OverduePts != -1000*scoreOverduePts || s.Total != 0 {
		t.Errorf("penalty and floor: %+v", s)
	}
	// tasks outside the workflow folder earn scoreDonePts, workflow ones scoreWorkflowPts: 2 plain done, 1 of them focus
	wantDone = scoreWorkflowPts[0] + scoreDonePts[0]
	if s := scoreFor([4]int{2}, [4]int{1}, 0, 0, 0, 0, false); s.Done != 2 || s.DonePts != wantDone {
		t.Errorf("focus points: %+v", s)
	}
}

func TestAddScores(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local)
	s := newTestStore(t, map[string]string{
		"inbox.md": "- [x] a ✅ 2026-09-30 @2026-09-29\n" + // done a day late: overdue at the end of 9/29 only
			"- [ ] b @2026-09-30\n" + // open since: overdue at the end of 9/30 and 10/1
			"- [x] c ✅ 2026-10-01 @2026-10-05\n" + // not due yet
			"- [ ] d @2026-10-09\n" +
			"- [ ] e @2026-10-01\n", // due today: not overdue while the day runs, overdue once it ends
		quizLog: `{"date":"2026-10-01","seconds":1200}` + "\n",
	})
	tasks := s.CollectTasks(TaskQuery{All: true, Now: now})
	a := BuildActivity(tasks, now, 2)
	a.AddStudy(s.StudySeconds(), now)
	a.AddScores(tasks, now)
	if got := a.Scores["2026-09-30"]; got.Done != 1 || got.Overdue != 1 || got.Total != clampInt(scoreDonePts[0]-scoreOverduePts, 0, 1<<30) { // a done (General, so focus), b overdue, floored at 0
		t.Errorf("9/30: %+v", got)
	}
	// c done, 20 min quiz, b overdue
	want := max(0, scoreDonePts[0]+quizPts(1200)-scoreOverduePts)
	if got := a.Scores["2026-10-01"]; !got.Live || got.Total != want || got.Overdue != 1 {
		t.Errorf("today: %+v", got)
	}
	// tomorrow, today's e counts against 10/1
	later := now.AddDate(0, 0, 1)
	tasks = s.CollectTasks(TaskQuery{All: true, Now: later})
	b := BuildActivity(tasks, later, 2)
	b.AddScores(tasks, later)
	if got := b.Scores["2026-10-01"]; got.Live || got.Overdue != 2 {
		t.Errorf("10/1 after it ended: %+v", got)
	}
	if _, ok := a.Scores["2026-09-29"]; ok {
		t.Error("a day with no activity should not be scored")
	}
}

func TestScoreFocusVsWorkflow(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local)
	s := newTestStore(t, map[string]string{
		"workflow/ideas.md": "- [x] tweak the viewer !2 ✅ 2026-10-01\n",
		"act200/hw.md":      "- [x] homework !2 ✅ 2026-10-01\n",
		"inbox.md":          "- [x] tagged into workflow #workflow ✅ 2026-10-01\n- [x] tagged into a course #act200 ✅ 2026-10-01\n",
	})
	tasks := s.CollectTasks(TaskQuery{All: true, Now: now})
	a := BuildActivity(tasks, now, 2)
	a.AddScores(tasks, now)
	// workflow: !2 + plain at scoreWorkflowPts; act200: !2 + plain at scoreDonePts
	want := scoreWorkflowPts[2] + scoreWorkflowPts[0] + scoreDonePts[2] + scoreDonePts[0]
	if got := a.Scores["2026-10-01"]; got.Done != 4 || got.DonePts != want {
		t.Errorf("got %+v, want done_pts %d", got, want)
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
	h := scoreHints()
	if strings.Contains(h.Done, "up to") || !strings.Contains(h.Overdue, fmt.Sprint(scoreOverduePts)) {
		t.Errorf("hints: %+v", h)
	}
}

func TestScoreCurve(t *testing.T) {
	now := time.Date(2026, 10, 1, 15, 0, 0, 0, time.Local)
	s := newTestStore(t, map[string]string{
		"act200/hw.md":      "- [x] hw !3 ✅ 2026-10-01 12:30\n- [x] old stamp !3 ✅ 2026-10-01\n- [ ] later !1 ✅ 2026-10-01 18:00\n",
		"workflow/ideas.md": "- [x] tweak !2 ✅ 2026-10-01 12:45 _(Oct 01 09:15)_\n",
		"inbox.md":          "- [x] late @2026-09-30 ✅ 2026-10-01 12:00\n- [ ] due today @2026-10-01\n- [x] yesterday ✅ 2026-09-30 20:00 @2026-09-30\n",
		quizLog:             `{"date":"2026-10-01","at":"2026-10-01T13:00:00","seconds":600,"xp":40,"levels":1}` + "\n",
	})
	tasks := s.CollectTasks(TaskQuery{All: true, Now: now})
	a := s.FullActivity(now, 2)
	line := a.ScoreLine
	if line[0].At != "2026-10-01T00:00:00" || line[len(line)-1].Total != a.Scores["2026-10-01"].Total {
		t.Fatalf("curve %v should end on today's total %d", line, a.Scores["2026-10-01"].Total)
	}
	at := func(clock string) int { // curve value at a time
		v := 0
		for _, p := range line {
			if p.At <= "2026-10-01T"+clock {
				v = p.Total
			}
		}
		return v
	}
	// 00:00: old stamp done (untimed), "late" overdue
	if got, want := at("00:00:00"), max(0, scoreDonePts[3]-scoreOverduePts); got != want {
		t.Errorf("00:00 = %d, want %d", got, want)
	}
	// 12:30 hw !3 outside workflow, 12:45 tweak !2 in workflow (made 09:15)
	if got := at("12:30:00") - at("12:29:59"); got != scoreDonePts[3] {
		t.Errorf("hw jump %d", got)
	}
	if got := at("12:45:00") - at("12:44:59"); got != scoreWorkflowPts[2] {
		t.Errorf("tweak jump %d", got)
	}
	// 12:00 late finished: done points plus the overdue penalty lifted, in one event
	evs := s.ScoreDay("2026-10-01", now).Events
	for _, e := range evs {
		if e.Text == "late" && e.Pts != scoreDonePts[0]+scoreOverduePts {
			t.Errorf("late event pts %d", e.Pts)
		}
		if e.Kind == "done" && e.Text == "hw" && (e.Diff != 3 || e.Workflow || e.At != "2026-10-01T12:30:00") {
			t.Errorf("hw event %+v", e)
		}
		if e.Kind == "made" && e.Text == "tweak" && e.At != "2026-10-01T09:15:00" {
			t.Errorf("made event %+v", e)
		}
	}
	if got := at("13:00:00") - at("12:59:59"); got != quizPts(600)+40/scoreXPPer+scoreLevelPts {
		t.Errorf("quiz jump %d", got)
	}
	// a finished day ends on its final, with tasks due that day counted as overdue at 23:59:59
	later := now.AddDate(0, 0, 1)
	tasks = s.CollectTasks(TaskQuery{All: true, Now: later})
	b := BuildActivity(tasks, later, 2)
	b.AddStudy(s.StudySeconds(), later)
	b.AddQuizGains(s.StudyGains())
	b.AddScores(tasks, later)
	past := s.ScoreDay("2026-10-01", later).Points
	if last := past[len(past)-1]; last.At != "2026-10-01T23:59:59" || last.Total != b.Scores["2026-10-01"].Total {
		t.Errorf("past day ends %+v, want %d", last, b.Scores["2026-10-01"].Total)
	}
	y := s.ScoreDay("2026-09-30", later).Points
	if last := y[len(y)-1]; last.Total != b.Scores["2026-09-30"].Total {
		t.Errorf("9/30 ends %+v, want %d", last, b.Scores["2026-09-30"].Total)
	}
}
