package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// runGradeCommand implements `notesview grade COURSE ITEM SCORE[/MAX]`.
func runGradeCommand(store *Store, args []string, stdout, stderr io.Writer, now time.Time) int {
	if len(args) < 3 {
		fmt.Fprintln(stderr, "notesview: usage: notesview grade COURSE ITEM SCORE[/MAX]   e.g. grade act200 \"Midterm 1\" 87/100")
		return 1
	}
	course, item, sc := args[0], strings.Join(args[1:len(args)-1], " "), args[len(args)-1]
	num, den, _ := strings.Cut(sc, "/")
	score, err := strconv.ParseFloat(strings.TrimSuffix(num, "%"), 64)
	max := 0.0
	if den != "" {
		max, err = strconv.ParseFloat(den, 64)
	} else if strings.HasSuffix(num, "%") {
		max = 100
	}
	if err != nil {
		fmt.Fprintln(stderr, "notesview: score must be a number, like 87 or 87/100")
		return 1
	}
	if err := store.Grade(course, item, score, max, now); err != nil {
		fmt.Fprintln(stderr, "notesview:", strings.TrimPrefix(err.Error(), "label: "))
		return 1
	}
	fmt.Fprintf(stdout, "Recorded %s %s: %s\n", course, item, sc)
	return 0
}

// runLabelCommand implements `notesview label …` for the start page: prompts, checkin, checkout, skip.
func runLabelCommand(store *Store, args []string, asJSON bool, stdout, stderr io.Writer, now time.Time) int {
	fail := func(err error) int {
		fmt.Fprintln(stderr, "notesview:", strings.TrimPrefix(err.Error(), "label: "))
		return 1
	}
	sub := "prompts"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	atoi := func(i int) int {
		if i < len(args) {
			n, _ := strconv.Atoi(args[i])
			return n
		}
		return 0
	}
	switch sub {
	case "prompts":
		p := store.OpenPrompts(now, store.CurrentIdle(now))
		if asJSON {
			return printJSON(stdout, map[string]any{"prompts": p, "queue": store.LabelQueue(now), "categories": labelCategories, "places": placeLabels})
		}
		fmt.Fprintf(stdout, "check-in open: %v  check-out: %v  to label: %d\n", p.Checkin != "", p.Checkout, p.Queue)
	case "checkin": // checkin FOCUS [PROMPT_TIME]
		prompted := ""
		if len(args) > 1 {
			prompted = args[1]
		}
		if err := store.Checkin(prompted, atoi(0), now); err != nil {
			return fail(err)
		}
		fmt.Fprintln(stdout, "Thanks: focus", atoi(0))
	case "checkout": // checkout PROD ENERGY MOOD SLEEP [NOTE…]
		note := ""
		if len(args) > 4 {
			note = strings.Join(args[4:], " ")
		}
		if err := store.Checkout(atoi(0), atoi(1), atoi(2), atoi(3), note, now); err != nil {
			return fail(err)
		}
		fmt.Fprintln(stdout, "Checked out for today")
	case "sleep": // sleep BED WAKE [WAKE_DAY]
		if len(args) < 2 {
			return fail(fmt.Errorf("usage: notesview label sleep BED WAKE [YYYY-MM-DD], e.g. 23:30 7:15"))
		}
		day := ""
		if len(args) > 2 {
			day = args[2]
		}
		if err := store.LogSleep(args[0], args[1], day, now); err != nil {
			return fail(err)
		}
		if day == "" {
			day = now.Format(isoDate)
		}
		h, _ := store.SleepWindow(day)
		fmt.Fprintf(stdout, "Slept %s → %s: %.1f h in bed\n", args[0], args[1], h)
	case "skip": // skip TARGET [KEY]
		if len(args) == 0 {
			return fail(fmt.Errorf("usage: notesview label skip checkin|checkout|HASH-KIND [KEY]"))
		}
		key := ""
		if len(args) > 1 {
			key = args[1]
		}
		if err := store.Skip(args[0], key, now); err != nil {
			return fail(err)
		}
	case "set": // set KIND HASH CATEGORY [TOKEN…]
		if len(args) < 3 {
			return fail(fmt.Errorf("usage: notesview label set KIND HASH CATEGORY"))
		}
		if err := store.Label(args[0], args[1], args[2], args[3:], now); err != nil {
			return fail(err)
		}
	default:
		return fail(fmt.Errorf("usage: notesview label [prompts|checkin N|checkout P E M S [NOTE]|skip T [KEY]|set KIND HASH CAT]"))
	}
	return 0
}

// runDataCommand implements `notesview data …` (works on the files; no server needed).
func runDataCommand(store *Store, args []string, asJSON bool, stdout, stderr io.Writer, now time.Time) int {
	fail := func(err error) int {
		fmt.Fprintln(stderr, "notesview:", strings.TrimPrefix(err.Error(), "label: "))
		return 1
	}
	sub := "status"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	arg := func() string {
		if len(args) > 0 {
			return args[0]
		}
		return ""
	}
	switch sub {
	case "status":
		o := store.DataOverview(now)
		if asJSON {
			return printJSON(stdout, o)
		}
		h := o["helper"].(HelperState)
		fmt.Fprintf(stdout, "helper (NotesViewSense): agent installed %v, loaded %v, running %v\n", h.Installed, h.Loaded, h.Running)
		fmt.Fprintf(stdout, "%-12s %-8s %-18s %s\n", "sensor", "state", "permission", "minutes/day, last 14 days (today last)")
		for _, r := range o["sensors"].([]SensorRow) {
			var days []string
			for _, d := range r.Days {
				days = append(days, strconv.Itoa(d))
			}
			fmt.Fprintf(stdout, "%-12s %-8s %-18s %s\n", r.Name, r.State, r.Permission, strings.Join(days, " "))
		}
	case "features": // features [DAY]
		day := arg()
		var done []string
		if day == "" {
			done = store.RebuildFeatures(now)
		} else {
			done = store.RebuildFeatures(now, day)
		}
		fmt.Fprintf(stdout, "rebuilt %d day(s): %s\n", len(done), strings.Join(done, " "))
	case "export": // export DAY → JSON lines on stdout
		f, err := store.LoadFeatures(arg())
		if err != nil {
			return fail(err)
		}
		printJSON(stdout, f)
		for _, r := range f.Rows {
			printJSON(stdout, r)
		}
	case "forget":
		if err := store.ForgetDay(arg()); err != nil {
			return fail(err)
		}
		fmt.Fprintln(stdout, "forgot", arg())
	case "forget-sensor":
		if sensorInfo[arg()][0] == "" {
			return fail(fmt.Errorf("unknown sensor %q", arg()))
		}
		store.ForgetSensor(arg(), now)
		fmt.Fprintln(stdout, "forgot every", arg(), "record")
	case "helper": // helper [status|start|stop|restart]
		act := arg()
		var st HelperState
		var err error
		if act == "" || act == "status" {
			st = HelperStatus()
		} else if st, err = HelperControl(act); err != nil {
			return fail(err)
		}
		if asJSON {
			return printJSON(stdout, st)
		}
		state := "stopped"
		if st.Running {
			state = fmt.Sprintf("running (pid %d)", st.PID)
		}
		fmt.Fprintln(stdout, "NotesViewSense:", state)
	case "quality": // quality [DAY]
		day := arg()
		if day == "" {
			day = now.Format(isoDate)
		}
		q := store.DataQuality(day, now)
		if asJSON {
			return printJSON(stdout, q)
		}
		for _, c := range q {
			fmt.Fprintf(stdout, "%-5s %-8s %-24s %s\n", c.Status, c.Sensor, c.Check, c.Detail)
		}
	case "imports":
		store.RunImports(now)
		fmt.Fprintln(stdout, "imports run:", store.loadImportState().States)
	case "importance":
		out, err := store.Importance(now)
		if err != nil {
			return fail(err)
		}
		return printJSON(stdout, out)
	case "fit-focus":
		m, err := store.FitFocusModel(now)
		if err != nil {
			return fail(err)
		}
		if m.N < 50 {
			fmt.Fprintf(stdout, "%d check-ins so far; version 2 needs 50\n", m.N)
			return 0
		}
		fmt.Fprintf(stdout, "check-ins %d: version 1 %.0f%%, version 2 %.0f%% → using version %d\n", m.N, 100*m.AccV1, 100*m.AccV2, map[bool]int{true: 2, false: 1}[m.Use])
	case "audit":
		iss, n := store.Audit()
		if asJSON {
			return printJSON(stdout, map[string]any{"issues": iss, "lines": n})
		}
		fmt.Fprintln(stdout, auditSummary(iss, n))
		for _, i := range iss {
			fmt.Fprintf(stdout, "  %s:%d %s = %q\n", i.File, i.Line, i.Key, i.Value)
		}
		if len(iss) > 0 {
			return 1
		}
	default:
		return fail(fmt.Errorf("usage: notesview data [status|quality [DAY]|helper [start|stop|restart]|features [DAY]|export DAY|forget DAY|forget-sensor NAME|imports|importance|fit-focus|audit]"))
	}
	return 0
}
