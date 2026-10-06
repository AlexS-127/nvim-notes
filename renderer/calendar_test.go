package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func init() {
	if l, err := time.LoadLocation("America/New_York"); err == nil {
		time.Local = l
	}
}

func ics(events ...string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\n" + strings.Join(events, "") + "END:VCALENDAR\r\n"
}

func vevent(lines ...string) string {
	return "BEGIN:VEVENT\r\n" + strings.Join(lines, "\r\n") + "\r\nEND:VEVENT\r\n"
}

func at(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, time.Local)
}

func titles(evs []CalEvent) []string {
	var out []string
	for _, e := range evs {
		out = append(out, e.Date+" "+e.Title)
	}
	return out
}

func TestParseICSBasics(t *testing.T) {
	evs, err := ParseICS(ics(vevent(
		"UID:a1", "SUMMARY:Intro to\\, well\\nlines", "DTSTART;TZID=America/Chicago:20261012T093000", "DTEND;TZID=America/Chicago:20261012T104500",
		"LOCATION:Hall 2", "DESCRIPTION:folded", " continuation",
		"BEGIN:VALARM", "TRIGGER:-PT10M", "END:VALARM",
	)))
	if err != nil || len(evs) != 1 {
		t.Fatalf("got %v, %v", evs, err)
	}
	e := evs[0]
	if e.Summary != "Intro to, well\nlines" || e.Location != "Hall 2" || e.Desc != "foldedcontinuation" {
		t.Errorf("text fields: %+v", e)
	}
	// 09:30 in Chicago is 10:30 in New York
	if got := e.Start.In(time.Local).Format("15:04"); got != "10:30" {
		t.Errorf("start in local time = %s, want 10:30", got)
	}
	if e.End.Sub(e.Start) != 75*time.Minute {
		t.Errorf("duration = %v", e.End.Sub(e.Start))
	}
	if _, err := ParseICS("hello"); err == nil {
		t.Error("a non-calendar should be rejected")
	}
}

func TestParseDurationAndAllDay(t *testing.T) {
	evs, _ := ParseICS(ics(
		vevent("UID:d", "SUMMARY:Dur", "DTSTART:20261012T140000Z", "DURATION:PT1H30M"),
		vevent("UID:e", "SUMMARY:Holiday", "DTSTART;VALUE=DATE:20261013", "DTEND;VALUE=DATE:20261015"),
	))
	if len(evs) != 2 || evs[0].End.Sub(evs[0].Start) != 90*time.Minute || !evs[1].AllDay {
		t.Fatalf("%+v", evs)
	}
}

func TestWeeklyRecurrenceKeepsLocalTimeAcrossDST(t *testing.T) {
	// Mon/Wed 10:30 from Oct 26, 2026; US clocks go back on Nov 1
	evs, _ := ParseICS(ics(vevent("UID:w", "SUMMARY:Class", "DTSTART;TZID=America/New_York:20261026T103000", "DTEND;TZID=America/New_York:20261026T114500",
		"RRULE:FREQ=WEEKLY;BYDAY=MO,WE;COUNT=5", "EXDATE;TZID=America/New_York:20261028T103000")))
	got := evs[0].expand(at(2027, 1, 1, 0, 0))
	want := []string{"2026-10-26", "2026-11-02", "2026-11-04", "2026-11-09"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i, g := range got {
		if g.Format(isoDate) != want[i] || g.Format("15:04") != "10:30" {
			t.Errorf("occurrence %d = %v, want %s 10:30", i, g, want[i])
		}
	}
}

func TestMonthlyAndUntil(t *testing.T) {
	evs, _ := ParseICS(ics(
		vevent("UID:m", "SUMMARY:Second Tue", "DTSTART;TZID=America/New_York:20261013T180000", "DTEND;TZID=America/New_York:20261013T190000", "RRULE:FREQ=MONTHLY;BYDAY=2TU;UNTIL=20261231T000000Z"),
		vevent("UID:l", "SUMMARY:Last Fri", "DTSTART;TZID=America/New_York:20261030T180000", "DTEND;TZID=America/New_York:20261030T190000", "RRULE:FREQ=MONTHLY;BYDAY=-1FR;COUNT=2"),
	))
	var second, last []string
	for _, s := range evs[0].expand(at(2028, 1, 1, 0, 0)) {
		second = append(second, s.Format(isoDate))
	}
	for _, s := range evs[1].expand(at(2028, 1, 1, 0, 0)) {
		last = append(last, s.Format(isoDate))
	}
	if strings.Join(second, ",") != "2026-10-13,2026-11-10,2026-12-08" {
		t.Errorf("second Tuesdays: %v", second)
	}
	if strings.Join(last, ",") != "2026-10-30,2026-11-27" {
		t.Errorf("last Fridays: %v", last)
	}
}

func calStore(t *testing.T, class bool, events ...string) *Store {
	t.Helper()
	s := newTestStore(t, nil)
	if _, err := s.ImportCalendar("School", []byte(ics(events...)), class, at(2026, 10, 1, 8, 0)); err != nil {
		t.Fatal(err)
	}
	return s
}

const classEvent = "UID:act200"

func weeklyClass() string {
	return vevent(classEvent, "SUMMARY:ACT 200", "LOCATION:Hall 2", "DTSTART;TZID=America/New_York:20261005T103000", "DTEND;TZID=America/New_York:20261005T114500", "RRULE:FREQ=WEEKLY;BYDAY=MO,WE")
}

func TestEventsRangeOverridesAndCancel(t *testing.T) {
	s := calStore(t, true, weeklyClass(),
		vevent(classEvent, "SUMMARY:ACT 200 (moved)", "RECURRENCE-ID;TZID=America/New_York:20261012T103000", "DTSTART;TZID=America/New_York:20261012T130000", "DTEND;TZID=America/New_York:20261012T141500"),
		vevent("UID:x", "SUMMARY:Cancelled", "STATUS:CANCELLED", "DTSTART;TZID=America/New_York:20261012T080000", "DTEND;TZID=America/New_York:20261012T090000"),
	)
	got := titles(s.Events(at(2026, 10, 12, 0, 0), at(2026, 10, 15, 0, 0), at(2026, 10, 1, 0, 0)))
	want := "2026-10-12 ACT 200 (moved)|2026-10-14 ACT 200"
	if strings.Join(got, "|") != want {
		t.Errorf("got %v, want %s", got, want)
	}
}

func TestMultiDayAndAllDayDates(t *testing.T) {
	s := calStore(t, true, vevent("UID:h", "SUMMARY:Break", "DTSTART;VALUE=DATE:20261013", "DTEND;VALUE=DATE:20261015"))
	evs := s.Events(at(2026, 10, 1, 0, 0), at(2026, 11, 1, 0, 0), at(2026, 10, 1, 0, 0))
	if len(evs) != 1 || evs[0].Date != "2026-10-13" || evs[0].EndDate != "2026-10-14" || !evs[0].AllDay || evs[0].Class {
		t.Fatalf("%+v", evs)
	}
}

func TestCheckInWindowAndScore(t *testing.T) {
	s := calStore(t, true, weeklyClass())
	cls := func(now time.Time) CalEvent {
		up := s.Upcoming(now, 1)
		if len(up) != 1 {
			t.Fatalf("no upcoming class at %v", now)
		}
		return up[0]
	}
	// Mon Oct 5 10:30 class: window opens 10:15
	early := at(2026, 10, 5, 10, 0)
	ev := cls(early)
	if ev.Date != "2026-10-05" || ev.CanCheck {
		t.Fatalf("early: %+v", ev)
	}
	if _, err := s.CheckIn(ev.ID, early); err == nil || !strings.Contains(err.Error(), "opens at 10:15") {
		t.Errorf("early check-in: %v", err)
	}
	open := at(2026, 10, 5, 10, 20)
	if ev = cls(open); !ev.CanCheck {
		t.Fatalf("window should be open: %+v", ev)
	}
	if _, err := s.CheckIn(ev.ID, open); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CheckIn(ev.ID, open.Add(time.Minute)); err == nil {
		t.Error("a second check-in should be refused")
	}
	if ev = cls(open.Add(time.Minute)); ev.Checked == "" || ev.CanCheck {
		t.Errorf("after check-in: %+v", ev)
	}
	// the next class is Wednesday, and a finished one cannot be checked in to
	if next := cls(at(2026, 10, 5, 12, 0)); next.Date != "2026-10-07" {
		t.Errorf("next class: %+v", next)
	}
	wed := s.Events(at(2026, 10, 7, 0, 0), at(2026, 10, 8, 0, 0), at(2026, 10, 7, 12, 0))
	if _, err := s.CheckIn(wed[0].ID, at(2026, 10, 7, 12, 0)); err == nil || !strings.Contains(err.Error(), "ended") {
		t.Errorf("late check-in: %v", err)
	}
	if !wed[0].Missed {
		t.Error("an unattended finished class is Missed")
	}

	// score: 5 points on the day of the class
	a := s.FullActivity(at(2026, 10, 6, 9, 0), 1)
	sc := a.Scores["2026-10-05"]
	if sc.Checkins != 1 || sc.CheckinPts != scoreCheckinPts || sc.Total < scoreCheckinPts {
		t.Errorf("score %+v", sc)
	}
	// deleting the calendar keeps the points
	if err := s.DeleteCalendar("school"); err != nil {
		t.Fatal(err)
	}
	if a = s.FullActivity(at(2026, 10, 6, 9, 0), 1); a.Scores["2026-10-05"].Checkins != 1 {
		t.Error("check-ins must survive deleting the calendar")
	}
}

func TestNotAClassCalendarHasNoCheckin(t *testing.T) {
	s := calStore(t, false, weeklyClass())
	if up := s.Upcoming(at(2026, 10, 5, 9, 0), 3); len(up) != 0 {
		t.Errorf("a non-class calendar has no upcoming classes: %+v", up)
	}
	evs := s.Events(at(2026, 10, 5, 0, 0), at(2026, 10, 6, 0, 0), at(2026, 10, 5, 10, 20))
	if len(evs) != 1 || evs[0].Class || evs[0].CanCheck {
		t.Fatalf("%+v", evs)
	}
	if _, err := s.CheckIn(evs[0].ID, at(2026, 10, 5, 10, 20)); err == nil {
		t.Error("check-in to a non-class event must fail")
	}
}

func TestImportReplacesSameNameAndKeepsSettings(t *testing.T) {
	s := calStore(t, true, weeklyClass())
	off := false
	if _, err := s.UpdateCalendar(CalendarPatch{ID: "school", Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	m, err := s.ImportCalendar("school", []byte(ics(vevent("UID:z", "SUMMARY:One", "DTSTART:20261020T100000Z"))), true, at(2026, 11, 1, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	cals := s.Calendars()
	if len(cals) != 1 || m.Enabled || m.Events != 1 || cals[0].Added != "2026-10-01" {
		t.Errorf("%+v", cals)
	}
	if _, err := s.ImportCalendar("Other", []byte(ics(vevent("UID:z", "SUMMARY:One", "DTSTART:20261020T100000Z"))), false, at(2026, 11, 1, 0, 0)); err != nil || len(s.Calendars()) != 2 {
		t.Errorf("a second calendar: %v", s.Calendars())
	}
}

func TestAttendanceCountsSinceImport(t *testing.T) {
	s := calStore(t, true, weeklyClass()) // imported 2026-10-01
	ev := s.Events(at(2026, 10, 5, 0, 0), at(2026, 10, 6, 0, 0), at(2026, 10, 5, 10, 20))[0]
	if _, err := s.CheckIn(ev.ID, at(2026, 10, 5, 10, 20)); err != nil {
		t.Fatal(err)
	}
	att := s.Attendance(at(2026, 10, 8, 0, 0)) // Mon and Wed have happened
	if len(att) != 1 || att[0].Held != 2 || att[0].Attended != 1 {
		t.Errorf("%+v", att)
	}
}

func TestAttendanceSummaryAndOutcomeLog(t *testing.T) {
	s := calStore(t, true, weeklyClass()) // Mon/Wed 10:30-11:45, imported 2026-10-01
	ev := s.Events(at(2026, 10, 5, 0, 0), at(2026, 10, 6, 0, 0), at(2026, 10, 5, 10, 20))[0]
	if _, err := s.CheckIn(ev.ID, at(2026, 10, 5, 10, 20)); err != nil { // 10 min early
		t.Fatal(err)
	}
	// Wed 12:00: Monday attended, Wednesday's class just ended unattended
	now := at(2026, 10, 7, 12, 0)
	sum := s.AttendanceSummary(now)
	if sum.Total != (AttendanceCount{2, 1}) || sum.Today != (AttendanceCount{1, 0}) || sum.TotalPct != 50 || sum.TodayPct != 0 || sum.Classes != 1 {
		t.Errorf("%+v", sum)
	}
	if got := s.AttendanceSummary(at(2026, 10, 7, 9, 0)); got.TodayPct != -1 {
		t.Errorf("nothing held yet today should be -1: %+v", got)
	}
	s.RecordAttendance(now)
	s.RecordAttendance(now.Add(time.Hour)) // idempotent
	b, _ := os.ReadFile(s.calPath(calAttendance))
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"status":"attended"`) || !strings.Contains(lines[0], `"lead":10`) || !strings.Contains(lines[1], `"status":"missed"`) {
		t.Errorf("outcome log: %v", lines)
	}
}

func TestCheckedInClassCountsBeforeItEnds(t *testing.T) {
	s := calStore(t, true, weeklyClass())
	now := at(2026, 10, 5, 10, 45) // Monday's class (10:30-11:45) is running
	ev := s.Upcoming(now, 1)[0]
	if _, err := s.CheckIn(ev.ID, now); err != nil {
		t.Fatal(err)
	}
	if sum := s.AttendanceSummary(now); sum.Total != (AttendanceCount{1, 1}) || sum.Today != (AttendanceCount{1, 1}) || sum.TotalPct != 100 {
		t.Errorf("%+v", sum)
	}
}
