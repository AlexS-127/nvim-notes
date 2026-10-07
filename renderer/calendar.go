package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Calendar: several .ics files, kept as raw copies in <notes>/.calendar/ (hidden, so the note
// scanner ignores it, but committed with the notes). A calendar marked as a class schedule has
// events you can check in to, from checkinEarly before the start until the end, for
// scoreCheckinPts each (score.go). Check-ins are an append-only log, so the points of a past day
// never change when a calendar is edited, re-imported or deleted. A calendar marked as your exam
// calendar (Settings → Calendars, Use: Exams) has exams: no check-in, shown with the upcoming
// classes, and the only calendar source of days_to_exam in the feature store.

const (
	calDir        = ".calendar"
	calMetaFile   = "calendars.json"
	calCheckins   = "checkins.jsonl"
	checkinEarly  = 15 * time.Minute // the check-in window opens this long before a class starts
	calMaxEvents  = 5000             // occurrences expanded per calendar per request
	calMaxImport  = 10 << 20
	calStampLocal = "2006-01-02T15:04:05"
)

// CalMeta is what is remembered about one imported calendar besides its events.
type CalMeta struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Color   string `json:"color"`   // swatch name, see SWATCHES in app.js
	Class   bool   `json:"class"`   // events are classes you can check in to
	Exam    bool   `json:"exam"`    // events are exams (never also Class)
	Enabled bool   `json:"enabled"` // shown in the viewer and counted for upcoming classes
	Added   string `json:"added"`   // date first imported: attendance is counted from here
	Events  int    `json:"events"`  // VEVENTs in the file (not occurrences)
}

var calColors = []string{"blue", "green", "orange", "violet", "teal", "rose", "amber", "indigo", "mint", "pink"}

// ── .ics parsing ─────────────────────────────────────────────────

type icsEvent struct {
	UID, Summary, Location, Desc string
	Start, End                   time.Time // in loc (all-day: local midnight, End exclusive)
	AllDay                       bool
	RRule                        string
	Ex                           []time.Time
	RDates                       []time.Time
	RecID                        time.Time // set on an override of one occurrence of a recurring event
	Cancelled                    bool
	loc                          *time.Location
}

type icsLine struct {
	name   string
	params map[string]string
	value  string
}

// unfoldICS joins folded lines (continuations start with a space or tab) and splits them.
func unfoldICS(data string) []string {
	data = strings.ReplaceAll(strings.ReplaceAll(data, "\r\n", "\n"), "\r", "\n")
	var out []string
	for _, l := range strings.Split(data, "\n") {
		if (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")) && len(out) > 0 {
			out[len(out)-1] += l[1:]
			continue
		}
		out = append(out, l)
	}
	return out
}

func parseICSLine(l string) (icsLine, bool) {
	// the name/params end at the first colon outside double quotes
	inQ, at := false, -1
	for i := 0; i < len(l); i++ {
		if l[i] == '"' {
			inQ = !inQ
		} else if l[i] == ':' && !inQ {
			at = i
			break
		}
	}
	if at < 0 {
		return icsLine{}, false
	}
	head := splitOutsideQuotes(l[:at], ';')
	p := map[string]string{}
	for _, kv := range head[1:] {
		if k, v, ok := strings.Cut(kv, "="); ok {
			p[strings.ToUpper(k)] = strings.Trim(v, `"`)
		}
	}
	return icsLine{name: strings.ToUpper(head[0]), params: p, value: l[at+1:]}, true
}

func splitOutsideQuotes(s string, sep byte) []string {
	var out []string
	inQ, from := false, 0
	for i := 0; i < len(s); i++ {
		if s[i] == '"' {
			inQ = !inQ
		} else if s[i] == sep && !inQ {
			out = append(out, s[from:i])
			from = i + 1
		}
	}
	return append(out, s[from:])
}

func icsText(s string) string {
	r := strings.NewReplacer(`\n`, "\n", `\N`, "\n", `\,`, ",", `\;`, ";", `\\`, `\`)
	return r.Replace(s)
}

// A few Windows zone names (Outlook exports) and their IANA equivalents; unknown names fall
// back to the local zone.
var winZones = map[string]string{
	"Eastern Standard Time": "America/New_York", "Central Standard Time": "America/Chicago",
	"Mountain Standard Time": "America/Denver", "Pacific Standard Time": "America/Los_Angeles",
	"GMT Standard Time": "Europe/London", "W. Europe Standard Time": "Europe/Berlin",
	"Romance Standard Time": "Europe/Paris", "UTC": "UTC",
}

func zoneFor(name string) *time.Location {
	if name == "" {
		return time.Local
	}
	if l, err := time.LoadLocation(name); err == nil {
		return l
	}
	if iana, ok := winZones[name]; ok {
		if l, err := time.LoadLocation(iana); err == nil {
			return l
		}
	}
	return time.Local
}

// parseICSTime reads a DATE or DATE-TIME value. Floating times are local.
func parseICSTime(l icsLine) (t time.Time, allDay bool, ok bool) {
	v := strings.TrimSpace(l.value)
	if l.params["VALUE"] == "DATE" || len(v) == 8 {
		d, err := time.ParseInLocation("20060102", v, time.Local)
		return d, true, err == nil
	}
	if strings.HasSuffix(v, "Z") {
		d, err := time.Parse("20060102T150405Z", v)
		return d, false, err == nil
	}
	d, err := time.ParseInLocation("20060102T150405", v, zoneFor(l.params["TZID"]))
	return d, false, err == nil
}

var durRe = regexp.MustCompile(`^([+-])?P(?:(\d+)W)?(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?$`)

func parseICSDuration(s string) (time.Duration, bool) {
	m := durRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, false
	}
	n := func(i int) time.Duration { v, _ := strconv.Atoi(m[i]); return time.Duration(v) }
	d := n(2)*7*24*time.Hour + n(3)*24*time.Hour + n(4)*time.Hour + n(5)*time.Minute + n(6)*time.Second
	if m[1] == "-" {
		d = -d
	}
	return d, true
}

// ParseICS reads every VEVENT of an .ics file. Events it cannot make sense of are skipped.
func ParseICS(data string) ([]icsEvent, error) {
	lines := unfoldICS(data)
	if !strings.Contains(strings.ToUpper(data), "BEGIN:VCALENDAR") {
		return nil, errors.New("not an .ics calendar (no BEGIN:VCALENDAR)")
	}
	var evs []icsEvent
	var cur *icsEvent
	var dur time.Duration
	var hasEnd bool
	depth := 0 // nested components inside a VEVENT (VALARM)
	for _, raw := range lines {
		l, ok := parseICSLine(raw)
		if !ok {
			continue
		}
		if l.name == "BEGIN" {
			if strings.EqualFold(l.value, "VEVENT") && cur == nil {
				cur, dur, hasEnd, depth = &icsEvent{loc: time.Local}, 0, false, 0
			} else if cur != nil {
				depth++
			}
			continue
		}
		if l.name == "END" {
			if cur == nil {
				continue
			}
			if depth > 0 {
				depth--
				continue
			}
			if !cur.Start.IsZero() {
				if !hasEnd {
					if dur > 0 {
						cur.End = cur.Start.Add(dur)
					} else if cur.AllDay {
						cur.End = cur.Start.AddDate(0, 0, 1)
					} else {
						cur.End = cur.Start
					}
				}
				if cur.End.Before(cur.Start) {
					cur.End = cur.Start
				}
				if cur.UID == "" {
					cur.UID = fmt.Sprintf("%s-%d", cur.Summary, cur.Start.Unix())
				}
				evs = append(evs, *cur)
			}
			cur = nil
			continue
		}
		if cur == nil || depth > 0 {
			continue
		}
		switch l.name {
		case "UID":
			cur.UID = strings.TrimSpace(l.value)
		case "SUMMARY":
			cur.Summary = strings.TrimSpace(icsText(l.value))
		case "LOCATION":
			cur.Location = strings.TrimSpace(icsText(l.value))
		case "DESCRIPTION":
			cur.Desc = strings.TrimSpace(icsText(l.value))
		case "STATUS":
			cur.Cancelled = strings.EqualFold(strings.TrimSpace(l.value), "CANCELLED")
		case "DTSTART":
			if t, all, ok := parseICSTime(l); ok {
				cur.Start, cur.AllDay, cur.loc = t, all, t.Location()
			}
		case "DTEND":
			if t, _, ok := parseICSTime(l); ok {
				cur.End, hasEnd = t, true
			}
		case "DURATION":
			if d, ok := parseICSDuration(l.value); ok {
				dur = d
			}
		case "RRULE":
			cur.RRule = strings.TrimSpace(l.value)
		case "EXDATE", "RDATE":
			for _, v := range strings.Split(l.value, ",") {
				if t, _, ok := parseICSTime(icsLine{params: l.params, value: v}); ok {
					if l.name == "EXDATE" {
						cur.Ex = append(cur.Ex, t)
					} else {
						cur.RDates = append(cur.RDates, t)
					}
				}
			}
		case "RECURRENCE-ID":
			if t, _, ok := parseICSTime(l); ok {
				cur.RecID = t
			}
		}
	}
	return evs, nil
}

// ── recurrence ───────────────────────────────────────────────────

type byDay struct {
	n   int // 0 = every, 1 = first, -1 = last, ...
	day time.Weekday
}

type rrule struct {
	freq       string
	interval   int
	count      int
	until      time.Time
	byDay      []byDay
	byMonthDay []int
	byMonth    []int
}

var weekdayCodes = map[string]time.Weekday{"SU": time.Sunday, "MO": time.Monday, "TU": time.Tuesday, "WE": time.Wednesday, "TH": time.Thursday, "FR": time.Friday, "SA": time.Saturday}

func parseRRule(s string, loc *time.Location) (rrule, bool) {
	r := rrule{interval: 1}
	for _, part := range strings.Split(s, ";") {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		switch strings.ToUpper(k) {
		case "FREQ":
			r.freq = strings.ToUpper(v)
		case "INTERVAL":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				r.interval = n
			}
		case "COUNT":
			r.count, _ = strconv.Atoi(v)
		case "UNTIL":
			if t, _, ok := parseICSTime(icsLine{params: map[string]string{}, value: v}); ok {
				if len(v) == 8 { // a date: through the end of that day
					t = t.AddDate(0, 0, 1)
				}
				r.until = t
			}
		case "BYDAY":
			for _, d := range strings.Split(v, ",") {
				d = strings.ToUpper(strings.TrimSpace(d))
				if len(d) < 2 {
					continue
				}
				wd, ok := weekdayCodes[d[len(d)-2:]]
				if !ok {
					continue
				}
				n, _ := strconv.Atoi(d[:len(d)-2])
				r.byDay = append(r.byDay, byDay{n, wd})
			}
		case "BYMONTHDAY":
			for _, d := range strings.Split(v, ",") {
				if n, err := strconv.Atoi(d); err == nil {
					r.byMonthDay = append(r.byMonthDay, n)
				}
			}
		case "BYMONTH":
			for _, d := range strings.Split(v, ",") {
				if n, err := strconv.Atoi(d); err == nil {
					r.byMonth = append(r.byMonth, n)
				}
			}
		}
	}
	switch r.freq {
	case "DAILY", "WEEKLY", "MONTHLY", "YEARLY":
		return r, true
	}
	return r, false
}

func daysIn(y int, m time.Month) int { return time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day() }

// monthDays lists the days of one month an MONTHLY/YEARLY rule selects.
func (r rrule) monthDays(y int, m time.Month, def int) []int {
	n := daysIn(y, m)
	set := map[int]bool{}
	switch {
	case len(r.byMonthDay) > 0:
		for _, d := range r.byMonthDay {
			if d < 0 {
				d = n + d + 1
			}
			if d >= 1 && d <= n {
				set[d] = true
			}
		}
	case len(r.byDay) > 0:
		for _, b := range r.byDay {
			var days []int
			for d := 1; d <= n; d++ {
				if time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Weekday() == b.day {
					days = append(days, d)
				}
			}
			switch {
			case b.n == 0:
				for _, d := range days {
					set[d] = true
				}
			case b.n > 0 && b.n <= len(days):
				set[days[b.n-1]] = true
			case b.n < 0 && -b.n <= len(days):
				set[days[len(days)+b.n]] = true
			}
		}
	default:
		if def <= n {
			set[def] = true
		}
	}
	out := make([]int, 0, len(set))
	for d := range set {
		out = append(out, d)
	}
	sort.Ints(out)
	return out
}

// expand returns the start of every occurrence that starts before `to`, in order, skipping
// excluded dates. It always counts from the first occurrence, so COUNT is right.
func (e *icsEvent) expand(to time.Time) []time.Time {
	s := e.Start
	if e.AllDay {
		s = time.Date(s.Year(), s.Month(), s.Day(), 0, 0, 0, 0, time.Local)
	}
	loc := s.Location()
	at := func(y int, m time.Month, d int) time.Time {
		return time.Date(y, m, d, s.Hour(), s.Minute(), s.Second(), 0, loc)
	}
	r, ok := parseRRule(e.RRule, loc)
	var out []time.Time
	if !ok {
		out = append(out, s)
	} else {
		n := 0
		inMonths := func(m time.Month) bool {
			if len(r.byMonth) == 0 {
				return true
			}
			for _, x := range r.byMonth {
				if int(m) == x {
					return true
				}
			}
			return false
		}
	periods:
		for k := 0; k < 200000 && (len(out) < calMaxEvents*4); k++ {
			var cands []time.Time
			var periodStart time.Time
			switch r.freq {
			case "DAILY":
				d := s.AddDate(0, 0, k*r.interval)
				periodStart = d
				cands = []time.Time{at(d.Year(), d.Month(), d.Day())}
				if len(r.byDay) > 0 {
					keep := false
					for _, b := range r.byDay {
						keep = keep || b.day == cands[0].Weekday()
					}
					if !keep {
						cands = nil
					}
				}
			case "WEEKLY":
				back := (int(s.Weekday()) + 6) % 7 // weeks start on Monday
				ws := s.AddDate(0, 0, -back+k*r.interval*7)
				periodStart = ws
				days := r.byDay
				if len(days) == 0 {
					days = []byDay{{0, s.Weekday()}}
				}
				for _, b := range days {
					off := (int(b.day) + 6) % 7
					d := ws.AddDate(0, 0, off)
					cands = append(cands, at(d.Year(), d.Month(), d.Day()))
				}
				sort.Slice(cands, func(i, j int) bool { return cands[i].Before(cands[j]) })
			case "MONTHLY":
				first := time.Date(s.Year(), s.Month()+time.Month(k*r.interval), 1, 0, 0, 0, 0, loc)
				periodStart = first
				if inMonths(first.Month()) {
					for _, d := range r.monthDays(first.Year(), first.Month(), s.Day()) {
						cands = append(cands, at(first.Year(), first.Month(), d))
					}
				}
			case "YEARLY":
				y := s.Year() + k*r.interval
				periodStart = time.Date(y, 1, 1, 0, 0, 0, 0, loc)
				months := r.byMonth
				if len(months) == 0 {
					months = []int{int(s.Month())}
				}
				sort.Ints(months)
				for _, mo := range months {
					for _, d := range r.monthDays(y, time.Month(mo), s.Day()) {
						cands = append(cands, at(y, time.Month(mo), d))
					}
				}
			}
			if periodStart.After(to) {
				break
			}
			for _, c := range cands {
				if c.Before(s) {
					continue
				}
				if !r.until.IsZero() && c.After(r.until) {
					break periods
				}
				out = append(out, c)
				n++
				if r.count > 0 && n >= r.count {
					break periods
				}
			}
		}
	}
	out = append(out, e.RDates...)
	keep := out[:0]
	for _, t := range out {
		if t.After(to) || e.excluded(t) {
			continue
		}
		keep = append(keep, t)
	}
	sort.Slice(keep, func(i, j int) bool { return keep[i].Before(keep[j]) })
	return keep
}

func (e *icsEvent) excluded(t time.Time) bool {
	for _, x := range e.Ex {
		if x.Equal(t) || (e.AllDay && x.Format(isoDate) == t.Format(isoDate)) {
			return true
		}
	}
	return false
}

// ── events for the viewer ────────────────────────────────────────

// CalEvent is one occurrence of an event, in local time.
type CalEvent struct {
	ID        string `json:"id"` // calendar|uid|start (unix seconds): what check-in takes
	Cal       string `json:"cal"`
	UID       string `json:"uid"`
	Title     string `json:"title"`
	Location  string `json:"location,omitempty"`
	Desc      string `json:"desc,omitempty"`
	Start     string `json:"start"`
	End       string `json:"end"`
	Date      string `json:"date"`     // local day it starts
	EndDate   string `json:"end_date"` // local day it covers last (same as Date for most)
	AllDay    bool   `json:"all_day"`
	Class     bool   `json:"class"`
	Exam      bool   `json:"exam,omitempty"`
	Checked   string `json:"checked,omitempty"` // when you checked in
	CanCheck  bool   `json:"can_check"`         // the check-in window is open now
	Missed    bool   `json:"missed,omitempty"`  // a class that ended without a check-in
	Color     string `json:"color"`
	CalName   string `json:"cal_name"`
	start     time.Time
	end       time.Time
	startUnix int64
}

func eventID(cal, uid string, start time.Time) string {
	return cal + "|" + uid + "|" + strconv.FormatInt(start.Unix(), 10)
}

type calFile struct {
	meta CalMeta
	evs  []icsEvent
}

var calMu sync.Mutex

func (s *Store) calPath(name string) string { return filepath.Join(s.Root, calDir, name) }

// Calendars lists the imported calendars.
func (s *Store) Calendars() []CalMeta {
	var list []CalMeta
	if b, err := os.ReadFile(s.calPath(calMetaFile)); err == nil {
		_ = json.Unmarshal(b, &list)
	}
	if list == nil {
		list = []CalMeta{}
	}
	return list
}

func (s *Store) saveCalendars(list []CalMeta) error {
	if err := os.MkdirAll(s.calPath(""), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(list, "", "  ")
	return writeAtomic(s.calPath(calMetaFile), append(b, '\n'))
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func calSlug(name string) string {
	s := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if s == "" {
		s = "calendar"
	}
	return s
}

// ImportCalendar stores an .ics file. A calendar with the same name is replaced (re-export of
// a schedule), keeping its settings and check-ins.
func (s *Store) ImportCalendar(name string, data []byte, class bool, now time.Time) (CalMeta, error) {
	if len(data) > calMaxImport {
		return CalMeta{}, errors.New("calendar file is too large (limit 10 MB)")
	}
	evs, err := ParseICS(string(data))
	if err != nil {
		return CalMeta{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Calendar"
	}
	calMu.Lock()
	defer calMu.Unlock()
	list := s.Calendars()
	idx := -1
	for i, m := range list {
		if strings.EqualFold(m.Name, name) {
			idx = i
		}
	}
	var meta CalMeta
	if idx >= 0 {
		meta = list[idx]
	} else {
		id, taken := calSlug(name), map[string]bool{}
		for _, m := range list {
			taken[m.ID] = true
		}
		base := id
		for i := 2; taken[id]; i++ {
			id = fmt.Sprintf("%s-%d", base, i)
		}
		meta = CalMeta{ID: id, Name: name, Color: calColors[len(list)%len(calColors)], Class: class, Enabled: true, Added: now.Format(isoDate)}
	}
	meta.Events = len(evs)
	if err := os.MkdirAll(s.calPath(""), 0o755); err != nil {
		return meta, err
	}
	if err := writeAtomic(s.calPath(meta.ID+".ics"), data); err != nil {
		return meta, err
	}
	if idx >= 0 {
		list[idx] = meta
	} else {
		list = append(list, meta)
	}
	return meta, s.saveCalendars(list)
}

// CalendarPatch changes a calendar's settings; nil fields stay as they are.
type CalendarPatch struct {
	ID      string  `json:"id"`
	Name    *string `json:"name"`
	Color   *string `json:"color"`
	Class   *bool   `json:"class"`
	Exam    *bool   `json:"exam"`
	Enabled *bool   `json:"enabled"`
}

func (s *Store) UpdateCalendar(p CalendarPatch) (CalMeta, error) {
	calMu.Lock()
	defer calMu.Unlock()
	list := s.Calendars()
	for i := range list {
		if list[i].ID != p.ID {
			continue
		}
		if p.Name != nil && strings.TrimSpace(*p.Name) != "" {
			list[i].Name = strings.TrimSpace(*p.Name)
		}
		if p.Color != nil {
			list[i].Color = *p.Color
		}
		if p.Class != nil {
			list[i].Class = *p.Class
		}
		if p.Exam != nil {
			list[i].Exam = *p.Exam
		}
		if list[i].Class && list[i].Exam { // one use per calendar: the newest choice wins
			if p.Exam != nil && *p.Exam {
				list[i].Class = false
			} else {
				list[i].Exam = false
			}
		}
		if p.Enabled != nil {
			list[i].Enabled = *p.Enabled
		}
		return list[i], s.saveCalendars(list)
	}
	return CalMeta{}, errors.New("no such calendar")
}

// DeleteCalendar removes a calendar and its file. Its check-ins stay in the log.
func (s *Store) DeleteCalendar(id string) error {
	calMu.Lock()
	defer calMu.Unlock()
	list := s.Calendars()
	for i := range list {
		if list[i].ID == id {
			_ = os.Remove(s.calPath(id + ".ics"))
			return s.saveCalendars(append(list[:i], list[i+1:]...))
		}
	}
	return errors.New("no such calendar")
}

func (s *Store) loadCalendar(m CalMeta) (calFile, bool) {
	b, err := os.ReadFile(s.calPath(m.ID + ".ics"))
	if err != nil {
		return calFile{}, false
	}
	evs, err := ParseICS(string(b))
	if err != nil {
		return calFile{}, false
	}
	return calFile{m, evs}, true
}

// Checkin is one line of the check-in log.
type Checkin struct {
	At    string `json:"at"`    // when you checked in (local time)
	Cal   string `json:"cal"`   // calendar id
	UID   string `json:"uid"`   // event uid
	Start int64  `json:"start"` // unix start of the occurrence
	Date  string `json:"date"`  // local day of the class, the day it scores on
	Title string `json:"title"`
	Lead  int    `json:"lead"` // minutes before the start you checked in (negative: after it began)
}

func checkinKey(cal, uid string, start int64) string {
	return cal + "|" + uid + "|" + strconv.FormatInt(start, 10)
}

func (s *Store) readCheckins() []Checkin {
	f, err := os.Open(s.calPath(calCheckins))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Checkin
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var c Checkin
		if json.Unmarshal(sc.Bytes(), &c) == nil && c.Cal != "" {
			out = append(out, c)
		}
	}
	return out
}

// CheckinsPerDay counts check-ins by the day of the class (they score on that day).
func (s *Store) CheckinsPerDay() map[string]int {
	out := map[string]int{}
	seen := map[string]bool{}
	for _, c := range s.readCheckins() {
		k := checkinKey(c.Cal, c.UID, c.Start)
		if !seen[k] {
			seen[k] = true
			out[c.Date]++
		}
	}
	return out
}

// Events returns the occurrences that overlap [from, to) from every enabled calendar, with
// check-in state worked out at `now`, ordered by start. Cancelled events are left out.
func (s *Store) Events(from, to, now time.Time) []CalEvent {
	checked := map[string]string{}
	for _, c := range s.readCheckins() {
		checked[checkinKey(c.Cal, c.UID, c.Start)] = c.At
	}
	var out []CalEvent
	for _, m := range s.Calendars() {
		if !m.Enabled {
			continue
		}
		cf, ok := s.loadCalendar(m)
		if !ok {
			continue
		}
		out = append(out, cf.occurrences(from, to, now, checked)...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].AllDay != out[j].AllDay {
			return out[i].AllDay // all-day first within a day
		}
		return out[i].start.Before(out[j].start)
	})
	sort.SliceStable(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

func (cf calFile) occurrences(from, to, now time.Time, checked map[string]string) []CalEvent {
	type ov struct {
		uid string
		at  int64
	}
	overridden := map[ov]bool{}
	for _, e := range cf.evs {
		if !e.RecID.IsZero() {
			overridden[ov{e.UID, e.RecID.Unix()}] = true
		}
	}
	var out []CalEvent
	add := func(e icsEvent, st time.Time) {
		if e.Cancelled {
			return
		}
		en := st.Add(e.End.Sub(e.Start))
		if e.AllDay { // DST-proof: whole days
			days := int(e.End.Sub(e.Start).Hours()/24 + .5)
			en = st.AddDate(0, 0, max(days, 1))
		}
		// overlaps [from, to): starts before `to` and ends after `from` (an instant event: starts at or after it)
		if !st.Before(to) || (en.After(st) && !en.After(from)) || (!en.After(st) && st.Before(from)) {
			return
		}
		ls, le := st.In(time.Local), en.In(time.Local)
		last := le
		if le.After(ls) {
			last = le.Add(-time.Second) // an event ending at midnight doesn't cover the next day
		}
		ev := CalEvent{
			ID: eventID(cf.meta.ID, e.UID, st), Cal: cf.meta.ID, UID: e.UID, Title: e.Summary, Location: e.Location, Desc: e.Desc,
			Start: ls.Format(time.RFC3339), End: le.Format(time.RFC3339),
			Date: ls.Format(isoDate), EndDate: last.Format(isoDate), AllDay: e.AllDay,
			Class: cf.meta.Class && !e.AllDay, Exam: cf.meta.Exam, Color: cf.meta.Color, CalName: cf.meta.Name,
			start: ls, end: le, startUnix: st.Unix(),
		}
		if ev.Title == "" {
			ev.Title = "(no title)"
		}
		if ev.Class {
			if at, ok := checked[checkinKey(cf.meta.ID, e.UID, st.Unix())]; ok {
				ev.Checked = at
			} else if !now.Before(ls.Add(-checkinEarly)) && now.Before(le) {
				ev.CanCheck = true
			} else if !now.Before(le) {
				ev.Missed = true
			}
		}
		out = append(out, ev)
	}
	for _, e := range cf.evs {
		if !e.RecID.IsZero() {
			add(e, e.Start)
			continue
		}
		if e.RRule == "" && len(e.RDates) == 0 {
			add(e, e.Start)
			continue
		}
		for _, st := range e.expand(to) {
			if overridden[ov{e.UID, st.Unix()}] {
				continue
			}
			add(e, st)
		}
	}
	return out
}

// Upcoming returns the classes and exams that have not ended yet (the running one first), at most
// n of them within the next two weeks.
func (s *Store) Upcoming(now time.Time, n int) []CalEvent {
	var out []CalEvent
	for _, e := range s.Events(now.AddDate(0, 0, -1), now.AddDate(0, 0, 14), now) {
		if (e.Class || e.Exam) && e.end.After(now) {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].start.Before(out[j].start) })
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

var ErrCheckin = errors.New("check-in")

// CheckIn confirms attendance at a class (id from CalEvent.ID). It must be a class, you must
// not have checked in already, and the window (checkinEarly before the start until the end)
// must be open.
func (s *Store) CheckIn(id string, now time.Time) (CalEvent, error) {
	parts := strings.Split(id, "|")
	if len(parts) < 3 {
		return CalEvent{}, fmt.Errorf("%w: bad event id", ErrCheckin)
	}
	unix, err := strconv.ParseInt(parts[len(parts)-1], 10, 64)
	if err != nil {
		return CalEvent{}, fmt.Errorf("%w: bad event id", ErrCheckin)
	}
	cal, uid := parts[0], strings.Join(parts[1:len(parts)-1], "|")
	st := time.Unix(unix, 0)
	var ev *CalEvent
	for _, e := range s.Events(st.Add(-time.Hour), st.Add(time.Hour), now) {
		if e.Cal == cal && e.UID == uid && e.startUnix == unix {
			e := e
			ev = &e
		}
	}
	if ev == nil {
		return CalEvent{}, fmt.Errorf("%w: no such class (is its calendar enabled?)", ErrCheckin)
	}
	if !ev.Class {
		return *ev, fmt.Errorf("%w: %s is not in a class calendar", ErrCheckin, ev.Title)
	}
	calMu.Lock()
	defer calMu.Unlock()
	if at := s.checkedAt(cal, uid, unix); at != "" {
		return *ev, fmt.Errorf("%w: already checked in to %s at %s", ErrCheckin, ev.Title, at)
	}
	if now.Before(ev.start.Add(-checkinEarly)) {
		return *ev, fmt.Errorf("%w: check-in for %s opens at %s", ErrCheckin, ev.Title, ev.start.Add(-checkinEarly).Format("15:04"))
	}
	if !now.Before(ev.end) {
		return *ev, fmt.Errorf("%w: %s has ended", ErrCheckin, ev.Title)
	}
	c := Checkin{At: now.Format(calStampLocal), Cal: cal, UID: uid, Start: unix, Date: ev.Date, Title: ev.Title, Lead: int(ev.start.Sub(now).Minutes())}
	line, _ := json.Marshal(c)
	if err := os.MkdirAll(s.calPath(""), 0o755); err != nil {
		return *ev, err
	}
	f, err := os.OpenFile(s.calPath(calCheckins), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return *ev, err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return *ev, err
	}
	ev.Checked, ev.CanCheck = c.At, false
	return *ev, nil
}

func (s *Store) checkedAt(cal, uid string, unix int64) string {
	for _, c := range s.readCheckins() {
		if c.Cal == cal && c.UID == uid && c.Start == unix {
			return c.At
		}
	}
	return ""
}

// ClassAttendance is how often you checked in to one class (by title) since its calendar was imported.
type ClassAttendance struct {
	Title    string `json:"title"`
	Cal      string `json:"cal"`
	CalName  string `json:"cal_name"`
	Color    string `json:"color"`
	Held     int    `json:"held"`
	Attended int    `json:"attended"`
}

// Attendance counts, per class title, the sessions that have ended since each class calendar
// was imported and how many of them you checked in to.
func (s *Store) Attendance(now time.Time) []ClassAttendance {
	checked := map[string]string{}
	for _, c := range s.readCheckins() {
		checked[checkinKey(c.Cal, c.UID, c.Start)] = c.At
	}
	var out []ClassAttendance
	for _, m := range s.Calendars() {
		if !m.Class || !m.Enabled {
			continue
		}
		cf, ok := s.loadCalendar(m)
		if !ok {
			continue
		}
		since, err := time.ParseInLocation(isoDate, m.Added, time.Local)
		if err != nil {
			since = now.AddDate(0, -1, 0)
		}
		by := map[string]*ClassAttendance{}
		var order []string
		for _, e := range cf.occurrences(since, now, now, checked) {
			if !e.Class || (e.end.After(now) && e.Checked == "") {
				continue
			}
			a := by[e.Title]
			if a == nil {
				a = &ClassAttendance{Title: e.Title, Cal: m.ID, CalName: m.Name, Color: m.Color}
				by[e.Title] = a
				order = append(order, e.Title)
			}
			a.Held++
			if e.Checked != "" {
				a.Attended++
			}
		}
		sort.Strings(order)
		for _, t := range order {
			out = append(out, *by[t])
		}
	}
	if out == nil {
		out = []ClassAttendance{}
	}
	return out
}

// AddCheckins folds per-day class check-ins into an Activity.
func (a *Activity) AddCheckins(n map[string]int) {
	for k, c := range n {
		d := a.Days[k]
		d.Checkins = c
		a.Days[k] = d
	}
}

// AttendanceCount is classes held against classes checked in to.
type AttendanceCount struct {
	Held     int `json:"held"`
	Attended int `json:"attended"`
}

// Pct is the attendance as a whole percentage, -1 when no class has been held yet.
func (c AttendanceCount) Pct() int {
	if c.Held == 0 {
		return -1
	}
	return int(float64(c.Attended)/float64(c.Held)*100 + .5)
}

// AttendanceSummary is what the Activity tile shows: today's classes that have ended, and all
// of them since each class calendar was imported.
type AttendanceSummary struct {
	Today    AttendanceCount `json:"today"`
	Total    AttendanceCount `json:"total"`
	TodayPct int             `json:"today_pct"` // -1: none held yet today
	TotalPct int             `json:"total_pct"`
	Classes  int             `json:"classes"` // class calendars; 0 hides the tile
}

func (s *Store) AttendanceSummary(now time.Time) AttendanceSummary {
	var a AttendanceSummary
	for _, m := range s.Calendars() {
		if m.Class && m.Enabled {
			a.Classes++
		}
	}
	today := now.Format(isoDate)
	for _, c := range s.Attendance(now) {
		a.Total.Held += c.Held
		a.Total.Attended += c.Attended
	}
	for _, m := range s.Calendars() {
		if !m.Class || !m.Enabled {
			continue
		}
		cf, ok := s.loadCalendar(m)
		if !ok {
			continue
		}
		checked := s.checkedSet()
		for _, e := range cf.occurrences(now.AddDate(0, 0, -1), now.AddDate(0, 0, 1), now, checked) {
			if e.Class && e.Date == today && (!e.end.After(now) || e.Checked != "") {
				a.Today.Held++
				if e.Checked != "" {
					a.Today.Attended++
				}
			}
		}
	}
	a.TodayPct, a.TotalPct = a.Today.Pct(), a.Total.Pct()
	return a
}

func (s *Store) checkedSet() map[string]string {
	checked := map[string]string{}
	for _, c := range s.readCheckins() {
		checked[checkinKey(c.Cal, c.UID, c.Start)] = c.At
	}
	return checked
}

// ClassOutcome is one line of .calendar/attendance.jsonl, the log of how every class went. It is
// written once a class has ended (so it holds the misses that checkins.jsonl does not) and is
// meant as a signal for later features (revision after class, the market).
type ClassOutcome struct {
	Date      string `json:"date"`
	Cal       string `json:"cal"`
	UID       string `json:"uid"`
	Start     int64  `json:"start"`
	End       string `json:"end"` // local time the class ended
	Title     string `json:"title"`
	Status    string `json:"status"` // "attended" or "missed"
	CheckinAt string `json:"checkin_at,omitempty"`
	Lead      int    `json:"lead"` // minutes before the start of the check-in (attended only)
}

const calAttendance = "attendance.jsonl"

// RecordAttendance appends an outcome for every class that has ended since its calendar was
// imported (at most the last 60 days) and is not logged yet. Safe to call often.
func (s *Store) RecordAttendance(now time.Time) {
	cals := s.Calendars()
	any := false
	for _, m := range cals {
		any = any || (m.Class && m.Enabled)
	}
	if !any {
		return
	}
	calMu.Lock()
	defer calMu.Unlock()
	done := map[string]bool{}
	if f, err := os.Open(s.calPath(calAttendance)); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			var o ClassOutcome
			if json.Unmarshal(sc.Bytes(), &o) == nil {
				done[checkinKey(o.Cal, o.UID, o.Start)] = true
			}
		}
		f.Close()
	}
	checkins := map[string]Checkin{}
	for _, c := range s.readCheckins() {
		checkins[checkinKey(c.Cal, c.UID, c.Start)] = c
	}
	checked := s.checkedSet()
	var lines []byte
	for _, m := range cals {
		if !m.Class || !m.Enabled {
			continue
		}
		cf, ok := s.loadCalendar(m)
		if !ok {
			continue
		}
		since, err := time.ParseInLocation(isoDate, m.Added, time.Local)
		if err != nil || since.Before(now.AddDate(0, 0, -60)) {
			since = now.AddDate(0, 0, -60)
		}
		for _, e := range cf.occurrences(since, now, now, checked) {
			k := checkinKey(m.ID, e.UID, e.startUnix)
			if !e.Class || e.end.After(now) || done[k] {
				continue
			}
			done[k] = true
			o := ClassOutcome{Date: e.Date, Cal: m.ID, UID: e.UID, Start: e.startUnix, End: e.end.Format(calStampLocal), Title: e.Title, Status: "missed"}
			if c, ok := checkins[k]; ok {
				o.Status, o.CheckinAt, o.Lead = "attended", c.At, c.Lead
			}
			b, _ := json.Marshal(o)
			lines = append(lines, append(b, '\n')...)
		}
	}
	if len(lines) == 0 {
		return
	}
	if err := os.MkdirAll(s.calPath(""), 0o755); err != nil {
		return
	}
	if f, err := os.OpenFile(s.calPath(calAttendance), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		f.Write(lines)
		f.Close()
	}
}
