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
	File         string   `json:"file"`  // slash path relative to the notes folder
	Title        string   `json:"title"` // title of the note
	Line         int      `json:"line"`  // 1-based
	Indent       int      `json:"indent"`
	State        string   `json:"state"`
	Text         string   `json:"text"`    // everything after the checkbox
	Display      string   `json:"display"` // text without the due date and folder tag
	Due          string   `json:"due,omitempty"`
	Tag          string   `json:"tag,omitempty"`      // folder tag: act-200 or act-200/chapter-5
	Category     string   `json:"category,omitempty"` // act-200
	CategoryName string   `json:"category_name,omitempty"`
	Topic        string   `json:"topic,omitempty"` // chapter-5
	TopicName    string   `json:"topic_name,omitempty"`
	Tags         []string `json:"tags"`
	Group        string   `json:"group"`
	HTML         string   `json:"html,omitempty"` // Display rendered as inline markdown (viewer only)
}

// Label is "Category · Topic", or "General" for tasks outside any folder.
func (t Task) Label() string {
	switch {
	case t.Category == "":
		return "General"
	case t.Topic == "":
		return t.CategoryName
	default:
		return t.CategoryName + " · " + t.TopicName
	}
}

func (t *Task) setFolder(f Folder) {
	t.Tag, t.Category, t.CategoryName, t.Topic, t.TopicName = f.Tag, f.Category, f.CategoryName, f.Topic, f.TopicName
}

var (
	// checkboxRe matches a list item with a checkbox: indent, state char, text.
	checkboxRe = regexp.MustCompile(`^(\s*)[-*+] \[([ xX>])\](?:[ \t]+(.*?))?\s*$`)
	dueRe      = regexp.MustCompile(`(^|[\s(\[])@(\d{4}-\d{2}-\d{2})\b`)
	tagRe      = regexp.MustCompile(`(^|[\s(\[])#([\p{L}\p{N}][\p{L}\p{N}_/-]*)`)
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
// code. Each task gets its due date, tags and folder: the category/topic
// folder the note is in, else the first #tag naming a folder, else its
// parent task's folder.
func ParseTasks(rel string, src []byte, idx *FolderIndex) []Task {
	if idx == nil {
		idx = NewFolderIndex(nil)
	}
	title := Title(rel, src)
	var out []Task
	// parents holds the enclosing task items, so subtasks inherit a folder
	type parent struct {
		indent int
		folder Folder
		ok     bool
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
		t := Task{File: rel, Title: title, Line: i + 1, Indent: indent, State: state, Text: text}
		var shown string // the tag that placed it, hidden from Display
		t.Due, t.Tags = scanTask(text)
		f, ok := idx.ForPath(rel)
		if !ok {
			for _, tag := range t.Tags {
				if f, ok = idx.ForTag(tag); ok {
					shown = tag
					break
				}
			}
		}
		if !ok && len(parents) > 0 {
			f, ok = parents[len(parents)-1].folder, parents[len(parents)-1].ok
		}
		if ok {
			t.setFolder(f)
		}
		parents = append(parents, parent{indent, f, ok})
		t.Display = displayText(text, t.Due, shown)
		out = append(out, t)
	}
	return out
}

// scanTask finds the due date and tags in a task's text, ignoring `code`.
func scanTask(text string) (due string, tags []string) {
	scan := inlineCode.ReplaceAllStringFunc(text, func(s string) string { return strings.Repeat(" ", len(s)) })
	if m := dueRe.FindStringSubmatch(scan); m != nil {
		if _, err := time.Parse(isoDate, m[2]); err == nil {
			due = m[2]
		}
	}
	tags = []string{}
	for _, m := range tagRe.FindAllStringSubmatch(scan, -1) {
		if tag := strings.Trim(strings.ToLower(m[2]), "/-"); tag != "" {
			tags = append(tags, tag)
		}
	}
	return due, tags
}

// displayText drops the due date and folder tag, which the views show as
// labels of their own.
func displayText(text, due, tag string) string {
	d := text
	if due != "" {
		d = regexp.MustCompile(`(^|[\s(\[])@`+regexp.QuoteMeta(due)+`\b`).ReplaceAllString(d, "$1")
	}
	if tag != "" {
		d = regexp.MustCompile(`(?i)(^|[\s(\[])#`+regexp.QuoteMeta(tag)+`/?([\s,.;:!?)\]]|$)`).ReplaceAllString(d, "$1$2")
		d = strings.NewReplacer(" ,", ",", " ;", ";", " :", ":", " .", ".").Replace(d)
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
// them by group (undated tasks form the last group), due date soonest first,
// folder, file and line.
func (s *Store) CollectTasks(q TaskQuery) []Task {
	if q.Now.IsZero() {
		q.Now = time.Now()
	}
	idx := s.Folders()
	out := []Task{}
	for _, f := range s.Files() {
		src, err := s.Read(f)
		if err != nil {
			continue
		}
		for _, t := range ParseTasks(f, src, idx) {
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
		if a.Group != b.Group {
			return rank[a.Group] < rank[b.Group]
		}
		if a.Due != b.Due {
			return a.Due < b.Due
		}
		if a.Tag != b.Tag {
			// tasks outside any folder (empty tag) come after the foldered ones
			if a.Tag == "" || b.Tag == "" {
				return a.Tag != ""
			}
			return a.Tag < b.Tag
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
	return out
}
