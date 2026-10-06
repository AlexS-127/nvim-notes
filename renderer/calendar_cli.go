package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// runCalendarCommand implements `notesview calendar …`; it works on the files directly.
func runCalendarCommand(store *Store, args []string, name string, notClass, asJSON bool, stdout, stderr io.Writer, now time.Time) int {
	fail := func(err error) int {
		fmt.Fprintln(stderr, "notesview:", err)
		return 1
	}
	sub, rest := "list", args
	if len(args) > 0 {
		sub, rest = args[0], args[1:]
	}
	// the flags may also come after the subcommand: `calendar import --name School file.ics`
	var pos []string
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--name", "-name":
			if i+1 < len(rest) {
				name = rest[i+1]
				i++
			}
		case "--not-class", "-not-class":
			notClass = true
		case "--json", "-json":
			asJSON = true
		default:
			pos = append(pos, rest[i])
		}
	}
	rest = pos
	switch sub {
	case "list":
		cals := store.Calendars()
		if asJSON {
			return printJSON(stdout, cals)
		}
		if len(cals) == 0 {
			fmt.Fprintln(stdout, "No calendars. Import one: notesview calendar import FILE.ics")
		}
		for _, m := range cals {
			kind := "calendar"
			if m.Class {
				kind = "classes"
			}
			off := ""
			if !m.Enabled {
				off = " (hidden)"
			}
			fmt.Fprintf(stdout, "%-20s %-10s %4d events  %s%s\n", m.ID, kind, m.Events, m.Name, off)
		}
	case "import":
		if len(rest) == 0 {
			return fail(fmt.Errorf("usage: notesview calendar import [--name NAME] [--not-class] FILE.ics…"))
		}
		if name != "" && len(rest) > 1 {
			return fail(fmt.Errorf("--name works with one file at a time"))
		}
		for _, f := range rest {
			b, err := os.ReadFile(f)
			if err != nil {
				return fail(err)
			}
			n := name
			if n == "" {
				n = strings.TrimSuffix(filepath.Base(f), filepath.Ext(f))
			}
			m, err := store.ImportCalendar(n, b, !notClass, now)
			if err != nil {
				return fail(fmt.Errorf("%s: %w", f, err))
			}
			fmt.Fprintf(stdout, "Imported %s: %d events (%s)\n", m.Name, m.Events, map[bool]string{true: "classes", false: "calendar"}[m.Class])
		}
	case "remove":
		if len(rest) != 1 {
			return fail(fmt.Errorf("usage: notesview calendar remove ID"))
		}
		if err := store.DeleteCalendar(rest[0]); err != nil {
			return fail(err)
		}
		fmt.Fprintln(stdout, "Removed", rest[0])
	case "next":
		classes := store.Upcoming(now, 5)
		if asJSON {
			return printJSON(stdout, map[string]any{"now": now.Format(time.RFC3339), "classes": nonNilEvents(classes), "points": scoreCheckinPts})
		}
		if len(classes) == 0 {
			fmt.Fprintln(stdout, "No upcoming classes.")
		}
		for _, e := range classes {
			fmt.Fprintln(stdout, eventLine(e, now))
		}
	case "today":
		evs := store.Events(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local), time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.Local), now)
		if asJSON {
			return printJSON(stdout, nonNilEvents(evs))
		}
		if len(evs) == 0 {
			fmt.Fprintln(stdout, "Nothing on the calendar today.")
		}
		for _, e := range evs {
			fmt.Fprintln(stdout, eventLine(e, now))
		}
	case "checkin":
		id := ""
		if len(rest) > 0 {
			id = rest[0]
		} else {
			for _, e := range store.Upcoming(now, 5) {
				if e.CanCheck && e.Checked == "" {
					id = e.ID
					break
				}
			}
			if id == "" {
				if up := store.Upcoming(now, 1); len(up) > 0 {
					return fail(fmt.Errorf("nothing to check in to now; next: %s", eventLine(up[0], now)))
				}
				return fail(fmt.Errorf("nothing to check in to: no upcoming classes"))
			}
		}
		ev, err := store.CheckIn(id, now)
		if err != nil {
			return fail(err)
		}
		if asJSON {
			return printJSON(stdout, map[string]any{"event": ev, "points": scoreCheckinPts})
		}
		fmt.Fprintf(stdout, "Checked in to %s (+%d)\n", ev.Title, scoreCheckinPts)
	case "attendance":
		att := store.Attendance(now)
		if asJSON {
			return printJSON(stdout, att)
		}
		for _, a := range att {
			fmt.Fprintf(stdout, "%-30s %d of %d\n", a.Title, a.Attended, a.Held)
		}
	default:
		return fail(fmt.Errorf("usage: notesview calendar [list|import|remove|next|today|checkin|attendance]"))
	}
	return 0
}

// eventLine is one line of text for an event: "10:30-11:45  ACT 200 · Hall 2  (in 25m)".
func eventLine(e CalEvent, now time.Time) string {
	when := "all day"
	if !e.AllDay {
		when = e.start.Format("15:04") + "-" + e.end.Format("15:04")
	}
	day := ""
	if e.Date != now.Format(isoDate) {
		day = e.start.Format("Mon Jan 2") + " "
	}
	where := ""
	if e.Location != "" {
		where = " · " + e.Location
	}
	st := ""
	switch {
	case e.Checked != "":
		st = "  ✓ checked in"
	case e.CanCheck:
		st = "  check-in open"
	case e.start.After(now):
		st = "  (" + untilText(e.start.Sub(now)) + ")"
	}
	return fmt.Sprintf("%s%s  %s%s%s", day, when, e.Title, where, st)
}

func untilText(d time.Duration) string {
	m := int(d.Minutes())
	switch {
	case m < 1:
		return "now"
	case m < 60:
		return fmt.Sprintf("in %dm", m)
	case m < 24*60:
		return fmt.Sprintf("in %dh %02dm", m/60, m%60)
	}
	return fmt.Sprintf("in %dd", m/(24*60))
}
