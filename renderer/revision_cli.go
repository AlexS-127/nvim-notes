package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// runReviseCommand implements `notesview revise …`; it works on the files directly.
func runReviseCommand(store *Store, args []string, asJSON bool, stdout, stderr io.Writer, now time.Time) int {
	fail := func(err error) int {
		fmt.Fprintln(stderr, "notesview:", strings.TrimPrefix(err.Error(), "revision: "))
		return 1
	}
	score, correct, total := -1.0, 0, 0
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--json", "-json":
			asJSON = true
		case "--score", "--correct", "--total":
			if i+1 >= len(args) {
				return fail(fmt.Errorf("%s needs a number", a))
			}
			v := args[i+1]
			i++
			var err error
			switch a {
			case "--score":
				score, err = strconv.ParseFloat(v, 64)
			case "--correct":
				correct, err = strconv.Atoi(v)
			default:
				total, err = strconv.Atoi(v)
			}
			if err != nil {
				return fail(fmt.Errorf("%s needs a number", a))
			}
		default:
			pos = append(pos, a)
		}
	}
	sub := "due"
	if len(pos) > 0 {
		sub, pos = pos[0], pos[1:]
	}
	one := func() (string, bool) {
		if len(pos) != 1 {
			return "", false
		}
		return pos[0], true
	}
	switch sub {
	case "due", "next":
		store.RecordRevision(now)
		due := store.RevisionDue(now)
		if asJSON {
			r := RevisionSummary{Due: len(due), DoneToday: store.RevisionsPerDay()[now.Format(isoDate)], Topics: len(store.Topics()), Points: scoreRevisionPts}
			if len(due) > 0 {
				r.Next = &due[0]
			}
			return printJSON(stdout, r)
		}
		if len(due) == 0 {
			fmt.Fprintln(stdout, "Nothing to revise today.")
		}
		for i, t := range due {
			fmt.Fprintf(stdout, "%d  %s · %s  (due %s, step %d, questions %s)\n", i+1, t.Title, t.Subject, t.Due, t.Step, t.Gen.Status)
		}
	case "list":
		store.RecordRevision(now)
		ts := store.Topics()
		if asJSON {
			return printJSON(stdout, ts)
		}
		if len(ts) == 0 {
			fmt.Fprintln(stdout, "No topics yet. Write in a course note, or: notesview revise add NOTE")
		}
		for _, t := range ts {
			fmt.Fprintf(stdout, "%-40s due %s  step %d  revised %d×  questions %s\n", t.ID, t.Due, t.Step, t.Count, t.Gen.Status)
		}
	case "add":
		id, ok := one()
		if !ok {
			return fail(fmt.Errorf("usage: notesview revise add NOTE"))
		}
		t, err := store.AddTopic(id, now)
		if err != nil {
			return fail(err)
		}
		if asJSON {
			return printJSON(stdout, t)
		}
		fmt.Fprintf(stdout, "Scheduled %s: first revision %s\n", t.Title, t.Due)
	case "remove", "rm":
		id, ok := one()
		if !ok {
			return fail(fmt.Errorf("usage: notesview revise remove NOTE"))
		}
		if err := store.RemoveTopic(id, now); err != nil {
			return fail(err)
		}
		fmt.Fprintln(stdout, "Removed", id, "from revision")
	case "done":
		id, ok := one()
		if !ok || score < 0 {
			return fail(fmt.Errorf("usage: notesview revise done ID --score 0-1 [--correct C --total T]"))
		}
		t, e, err := store.RevisionDone(id, score, correct, total, now)
		if err != nil {
			return fail(err)
		}
		if asJSON {
			return printJSON(stdout, map[string]any{"topic": t, "entry": e, "points": map[bool]int{true: scoreRevisionPts}[e.Points]})
		}
		pts := ""
		if e.Points {
			pts = fmt.Sprintf(" (+%d)", scoreRevisionPts)
		}
		fmt.Fprintf(stdout, "Revised %s%s. Next revision %s.\n", t.Title, pts, t.Due)
	case "gen":
		id, ok := one()
		if !ok {
			return fail(fmt.Errorf("usage: notesview revise gen NOTE"))
		}
		n, err := store.GenerateQuestions(id, now)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "Wrote %d questions to %s\n", n, store.QuestionsPath(id))
	case "gen-mixed":
		sub, ok := one()
		if !ok {
			return fail(fmt.Errorf("usage: notesview revise gen-mixed SUBJECT"))
		}
		n, err := store.GenerateMixed(sub, store.mixedMembers(sub, now), now)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "Wrote %d cross-topic questions to %s\n", n, store.MixedPath(sub))
	case "path":
		id, ok := one()
		if !ok {
			return fail(fmt.Errorf("usage: notesview revise path ID"))
		}
		fmt.Fprintln(stdout, store.QuestionsPath(id))
	default:
		return fail(fmt.Errorf("usage: notesview revise [due|list|add NOTE|remove NOTE|done ID --score S|gen NOTE|gen-mixed SUBJECT|path ID]"))
	}
	return 0
}
