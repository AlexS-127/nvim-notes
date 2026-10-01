package main

import (
	"regexp"
	"sort"
	"strings"
	"time"
)

// Task states, from the character inside the checkbox.
const (
	StateOpen  = "open"  // - [ ]
	StateDone  = "done"  // - [x]
	StateMoved = "moved" // - [>]  carried over to a later daily note
)

// Kinds: tasks that belong to a class are homework, everything else is other.
const (
	KindHomework = "homework"
	KindOther    = "other"
)

// Date groups, in display order.
const (
	GroupOverdue  = "overdue"
	GroupToday    = "today"
	GroupTomorrow = "tomorrow"
	GroupWeek     = "week" // 2–7 days from today
	GroupLater    = "later"
	GroupNone     = "none" // no due date
)

var GroupOrder = []string{GroupOverdue, GroupToday, GroupTomorrow, GroupWeek, GroupLater, GroupNone}

type Task struct {
	File      string   `json:"file"`  // slash path relative to the notes folder
	Title     string   `json:"title"` // title of the note
	Line      int      `json:"line"`  // 1-based
	Indent    int      `json:"indent"`
	State     string   `json:"state"`
	Text      string   `json:"text"`    // everything after the checkbox
	Display   string   `json:"display"` // text without the due date and class tag
	Due       string   `json:"due,omitempty"`
	Class     string   `json:"class,omitempty"`
	ClassName string   `json:"class_name,omitempty"`
	Tags      []string `json:"tags"`
	Kind      string   `json:"kind"`
	Group     string   `json:"group"`
	HTML      string   `json:"html,omitempty"` // Display rendered as inline markdown (viewer only)
}

var (
	// checkboxRe matches a list item with a checkbox: indent, state char, text.
	checkboxRe = regexp.MustCompile(`^(\s*)[-*+] \[([ xX>])\](?:[ \t]+(.*?))?\s*$`)
	dueRe      = regexp.MustCompile(`(^|[\s(\[])@(\d{4}-\d{2}-\d{2})\b`)
	tagRe      = regexp.MustCompile(`(^|[\s(\[])#([A-Za-z][\w/-]*)`)
	inlineCode = regexp.MustCompile("`[^`]*`")
	spacesRe   = regexp.MustCompile(`[ \t]{2,}`)
)

// indentWidth counts leading whitespace, a tab as four columns.
func indentWidth(s string) int {
	n := 0
	for _, r := range s {
		switch r {
		case ' ':
			n++
		case '\t':
			n += 4
		default:
			return n
		}
	}
	return n
}

// parseTaskLine parses one line. It reports false for non-task lines.
func parseTaskLine(line string) (indent int, state, text string, ok bool) {
	m := checkboxRe.FindStringSubmatch(strings.TrimRight(line, "\r"))
	if m == nil {
		return 0, "", "", false
	}
	switch m[2] {
	case " ":
		state = StateOpen
	case ">":
		state = StateMoved
	default:
		state = StateDone
	}
	return indentWidth(m[1]), state, m[3], true
}

// ParseTasks extracts every checkbox item from one note, skipping fenced
// code. Each task gets its due date, tags, class and kind.
func ParseTasks(rel string, src []byte, cfg *Config) []Task {
	title := Title(rel, src)
	var out []Task
	// parents holds the enclosing task items, so subtasks inherit a class
	type parent struct {
		indent int
		class  Class
	}
	var parents []parent
	inFence := false
	for i, l := range strings.Split(string(src), "\n") {
		if fenceRe.MatchString(l) {
			inFence = !inFence
			continue
		}
		if inFence || strings.TrimSpace(l) == "" {
			continue
		}
		w := indentWidth(l)
		for len(parents) > 0 && parents[len(parents)-1].indent >= w {
			parents = parents[:len(parents)-1]
		}
		indent, state, text, ok := parseTaskLine(l)
		if !ok {
			continue
		}
		t := Task{File: rel, Title: title, Line: i + 1, Indent: indent, State: state, Text: text, Tags: []string{}}
		// tags and dates inside `code` don't count
		scan := inlineCode.ReplaceAllStringFunc(text, func(s string) string { return strings.Repeat(" ", len(s)) })
		if m := dueRe.FindStringSubmatch(scan); m != nil {
			if _, err := time.Parse(isoDate, m[2]); err == nil {
				t.Due = m[2]
			}
		}
		t.Tags = tagsOf(scan)
		c, ok := classOf(cfg, rel, t.Tags)
		if !ok && len(parents) > 0 {
			c = parents[len(parents)-1].class
		}
		t.Class, t.ClassName = c.ID, c.Name
		parents = append(parents, parent{indent, c})
		t.Kind = KindOther
		if t.Class != "" {
			t.Kind = KindHomework
		}
		t.Display = displayText(text, t.Due, t.Class)
		out = append(out, t)
	}
	return out
}

func tagsOf(text string) []string {
	tags := []string{}
	for _, m := range tagRe.FindAllStringSubmatch(text, -1) {
		tags = append(tags, strings.ToLower(m[2]))
	}
	return tags
}

// classOf decides a task's class: the class whose folder holds the note, or
// else the first #tag naming a class. (ParseTasks also lets a subtask
// inherit its parent task's class.)
func classOf(cfg *Config, rel string, tags []string) (Class, bool) {
	if c, ok := cfg.ClassForPath(rel); ok {
		return c, true
	}
	for _, tag := range tags {
		if c, ok := cfg.ClassByID(tag); ok {
			return c, true
		}
	}
	return Class{}, false
}

// displayText drops the due date and class tag, which the views show as
// labels of their own.
func displayText(text, due, class string) string {
	d := text
	if due != "" {
		d = regexp.MustCompile(`(^|[\s(\[])@`+regexp.QuoteMeta(due)+`\b`).ReplaceAllString(d, "$1")
	}
	if class != "" {
		d = regexp.MustCompile(`(?i)(^|[\s(\[])#`+regexp.QuoteMeta(class)+`\b`).ReplaceAllString(d, "$1")
	}
	d = strings.TrimSpace(spacesRe.ReplaceAllString(d, " "))
	if d == "" {
		return text
	}
	return d
}

// DateGroup places a due date relative to today.
func DateGroup(due string, today time.Time) string {
	if due == "" {
		return GroupNone
	}
	d, err := time.ParseInLocation(isoDate, due, today.Location())
	if err != nil {
		return GroupNone
	}
	days := daysBetween(today, d)
	switch {
	case days < 0:
		return GroupOverdue
	case days == 0:
		return GroupToday
	case days == 1:
		return GroupTomorrow
	case days <= 7:
		return GroupWeek
	default:
		return GroupLater
	}
}

// daysBetween counts calendar days from a to b (DST-safe).
func daysBetween(a, b time.Time) int {
	ua := time.Date(a.Year(), a.Month(), a.Day(), 0, 0, 0, 0, time.UTC)
	ub := time.Date(b.Year(), b.Month(), b.Day(), 0, 0, 0, 0, time.UTC)
	return int(ub.Sub(ua).Hours() / 24)
}

// TaskQuery selects tasks for `notesview tasks` and the Tasks view.
type TaskQuery struct {
	All bool      // include done and moved tasks, not only open ones
	Now time.Time // reference time for date groups
}

// CollectTasks gathers tasks from every note, assigns date groups and sorts
// them: homework first, then by group, due date, file and line.
func (s *Store) CollectTasks(cfg *Config, q TaskQuery) []Task {
	if q.Now.IsZero() {
		q.Now = time.Now()
	}
	out := []Task{}
	for _, f := range s.Files() {
		src, err := s.Read(f)
		if err != nil {
			continue
		}
		for _, t := range ParseTasks(f, src, cfg) {
			if !q.All && t.State != StateOpen {
				continue
			}
			t.Group = DateGroup(t.Due, q.Now)
			out = append(out, t)
		}
	}
	rank := map[string]int{}
	for i, g := range GroupOrder {
		rank[g] = i
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Kind != b.Kind {
			return a.Kind == KindHomework
		}
		if a.Group != b.Group {
			return rank[a.Group] < rank[b.Group]
		}
		if a.Due != b.Due {
			return a.Due < b.Due
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
	return out
}
