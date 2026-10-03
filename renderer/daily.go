package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ── Daily notes and carry-over ───────────────────────────────────

var dailyNameRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})\.md$`)

func dailyRel(date time.Time) string { return "daily/" + date.Format(isoDate) + ".md" }

func dailyTemplate(date time.Time, tasks []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n## Tasks\n\n", date.Format("Monday, January 02 2006"))
	for _, l := range tasks {
		b.WriteString(l + "\n")
	}
	b.WriteString("\n## Notes\n\n")
	return b.String()
}

// previousDaily finds the most recent daily note dated before date.
func (s *Store) previousDaily(date time.Time) (string, bool) {
	entries, err := os.ReadDir(filepath.Join(s.Root, "daily"))
	if err != nil {
		return "", false
	}
	limit, best := date.Format(isoDate), ""
	for _, e := range entries {
		m := dailyNameRe.FindStringSubmatch(e.Name())
		if m == nil || e.IsDir() {
			continue
		}
		if _, err := time.Parse(isoDate, m[1]); err != nil {
			continue
		}
		if m[1] < limit && m[1] > best {
			best = m[1]
		}
	}
	if best == "" {
		return "", false
	}
	return "daily/" + best + ".md", true
}

// DailyResult describes what EnsureDaily did.
type DailyResult struct {
	Path    string `json:"path"`              // slash path relative to the notes folder
	Created bool   `json:"created"`           // the note did not exist before
	From    string `json:"from,omitempty"`    // note tasks were carried over from
	Moved   int    `json:"moved"`             // number of tasks carried over
	Warning string `json:"warning,omitempty"` // carry-over problem (note still created)
}

// EnsureDaily creates the daily note for date if it is missing, using the
// daily template. With carry set it moves open tasks that have no category
// and no due date from the most recent earlier daily note into the new
// note's Tasks section.
func (s *Store) EnsureDaily(date time.Time, carry bool) (DailyResult, error) {
	rel := dailyRel(date)
	res := DailyResult{Path: rel}
	full, err := s.Resolve(rel)
	if err != nil {
		return res, err
	}
	if _, err := os.Stat(full); err == nil {
		return res, nil
	}
	var moved, oldLines []string
	var prevFull string
	if carry {
		if prev, ok := s.previousDaily(date); ok {
			if prevFull, err = s.Resolve(prev); err == nil {
				if data, rerr := os.ReadFile(prevFull); rerr == nil {
					var n int
					oldLines, moved, n = carryOver(strings.Split(string(data), "\n"), s.Folders(), prev, date.Format(isoDate))
					if n > 0 {
						res.From, res.Moved = prev, n
					}
				}
			}
		}
	}
	if err := createExclusive(full, []byte(dailyTemplate(date, moved))); err != nil {
		if errors.Is(err, os.ErrExist) { // someone else just made it
			return DailyResult{Path: rel}, nil
		}
		return res, err
	}
	res.Created = true
	if res.Moved > 0 {
		if err := writeAtomic(prevFull, []byte(strings.Join(oldLines, "\n"))); err != nil {
			res.Warning = fmt.Sprintf("tasks were copied but %s could not be updated: %v", res.From, err)
		}
	}
	return res, nil
}

var openBoxRe = regexp.MustCompile(`^(\s*[-*+] )\[ \]`)

// carryOver splits a daily note into what stays and the task lines that
// move to the note named target (YYYY-MM-DD). An open task moves, with its
// nested lines, only if it has no category (folder tag) and no due date;
// the others stay put and show up in the Tasks view by date. Nested tasks
// that have a category or due date stay behind too. Each moved task is
// left behind as "- [>] text → [[target]]".
func carryOver(lines []string, idx *FolderIndex, rel, target string) (keep, moved []string, n int) {
	fence := make([]bool, len(lines)) // line is a fence marker or inside a fenced block
	in := false
	for i, l := range lines {
		if fenceRe.MatchString(l) {
			fence[i] = true
			in = !in
			continue
		}
		fence[i] = in
	}
	isTask := func(i int) (indent int, state, text string, ok bool) {
		if fence[i] {
			return 0, "", "", false
		}
		return parseTaskLine(lines[i])
	}
	// stays: the task has a due date or belongs to a folder
	stays := func(text string) bool {
		due, tags := scanTask(text)
		if due != "" {
			return true
		}
		if _, ok := idx.ForPath(rel); ok {
			return true
		}
		for _, tag := range tags {
			if _, ok := idx.ForTag(tag); ok {
				return true
			}
		}
		return false
	}
	// blockEnd returns the index after the lines nested under line i.
	blockEnd := func(i, indent int) int {
		end := i + 1
		for j := i + 1; j < len(lines); j++ {
			l := strings.TrimRight(lines[j], "\r")
			if strings.TrimSpace(l) == "" {
				continue
			}
			if indentWidth(l) <= indent {
				break
			}
			end = j + 1
		}
		return end
	}

	for i := 0; i < len(lines); {
		indent, state, text, ok := isTask(i)
		if !ok || state != StateOpen {
			keep = append(keep, lines[i])
			i++
			continue
		}
		end := blockEnd(i, indent)
		if stays(text) {
			keep = append(keep, lines[i:end]...)
			i = end
			continue
		}
		prefix := lines[i][:len(lines[i])-len(strings.TrimLeft(lines[i], " \t"))]
		line, cr := strings.TrimSuffix(lines[i], "\r"), ""
		if len(line) < len(lines[i]) {
			cr = "\r"
		}
		moved = append(moved, strings.TrimPrefix(line, prefix))
		keep = append(keep, openBoxRe.ReplaceAllString(line, "${1}[>]")+" → [["+target+"]]"+cr)
		n++
		// nested lines move too, except subtasks that stay and their children
		for j := i + 1; j < end; {
			if cind, _, ctext, cok := isTask(j); cok && stays(ctext) {
				cend := blockEnd(j, cind)
				keep = append(keep, lines[j:cend]...)
				j = cend
				continue
			}
			moved = append(moved, strings.TrimSuffix(strings.TrimPrefix(lines[j], prefix), "\r"))
			j++
		}
		i = end
	}
	return keep, moved, n
}

func createExclusive(full string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// ── Capture ──────────────────────────────────────────────────────

// DueLabel formats a date for confirmation prompts: "Fri Oct 2".
func DueLabel(d time.Time) string { return d.Format("Mon Jan 2") }

// ParseDue reads a due date typed at a prompt: anything `notesview date`
// understands, with or without the @, or an ISO date.
func ParseDue(in string, now time.Time) (time.Time, bool) {
	in = strings.TrimPrefix(strings.TrimSpace(in), "@")
	if d, err := time.ParseInLocation(isoDate, in, now.Location()); err == nil {
		return d, true
	}
	return ResolveNaturalDate(strings.ReplaceAll(in, " ", ""), now)
}

// CaptureOpts are the answers to the capture steps.
type CaptureOpts struct {
	Text   string // the task (natural @dates in it are converted)
	Folder string // folder tag or path; "" or "none" for no folder
	Due    string // natural or ISO due date; "" for none
	Diff   int    // difficulty 1-3; 0 for none (or whatever the text already says)
}

// BuildCapture turns the answers into the task line (without the "- [ ] "
// prefix and timestamp): text, then #folder-tag, then @due.
//
// A folder that does not exist yet is not an error: BuildCapture returns the
// tag for it and Capture creates the directory.
func BuildCapture(o CaptureOpts, idx *FolderIndex, now time.Time) (string, error) {
	text, _, err := buildCapture(o, idx, now)
	return text, err
}

// buildCapture also returns the folder to create, if the answer named one
// that does not exist.
func buildCapture(o CaptureOpts, idx *FolderIndex, now time.Time) (text string, create *Folder, err error) {
	text = ConvertNaturalDates(strings.Join(strings.Fields(o.Text), " "), now)
	if text == "" {
		return "", nil, errors.New("nothing to capture")
	}
	if f := strings.TrimSpace(o.Folder); f != "" && !strings.EqualFold(f, "none") {
		f = strings.TrimPrefix(f, "#")
		folder, ok := idx.Resolve(f)
		if !ok {
			if folder, ok = newFolder(f); !ok {
				return "", nil, fmt.Errorf("can't make a folder called %q", f)
			}
			create = &folder
		}
		if _, tags := scanTask(text); !containsFold(tags, folder.Tag) {
			text += " #" + folder.Tag
		}
	}
	if strings.TrimSpace(o.Due) != "" {
		d, ok := ParseDue(o.Due, now)
		if !ok {
			return "", nil, fmt.Errorf("can't read due date %q (try fri, tomorrow, oct6, 10/6, +3d)", o.Due)
		}
		iso := d.Format(isoDate)
		if old, _ := scanTask(text); old != "" {
			text = strings.Replace(text, "@"+old, "@"+iso, 1)
		} else {
			text += " @" + iso
		}
	}
	if o.Diff != 0 {
		if o.Diff < 1 || o.Diff > 3 {
			return "", nil, fmt.Errorf("difficulty must be 1, 2 or 3 (got %d)", o.Diff)
		}
		if scanDifficulty(text) == 0 {
			text += fmt.Sprintf(" !%d", o.Diff)
		}
	}
	return text, create, nil
}

// newFolder describes a category or category/topic folder that does not
// exist yet, from a typed path like "ACT 200/Chapter 6". Deeper paths and
// names that slugify to nothing (or are reserved) are refused.
func newFolder(p string) (Folder, bool) {
	var parts []string
	for _, seg := range strings.Split(strings.ReplaceAll(p, "\\", "/"), "/") {
		if seg = strings.TrimSpace(seg); seg != "" {
			parts = append(parts, seg)
		}
	}
	if len(parts) == 0 || len(parts) > 2 || !categoryDir(parts[0]) || slugify(parts[0]) == "" {
		return Folder{}, false
	}
	cat := Folder{Path: parts[0], Tag: slugify(parts[0]), Name: parts[0], Kind: "category"}
	cat.Category, cat.CategoryName = cat.Tag, cat.Name
	if len(parts) == 1 {
		return cat, true
	}
	if !topicDir(parts[1]) || slugify(parts[1]) == "" {
		return Folder{}, false
	}
	return Folder{
		Path: parts[0] + "/" + parts[1], Tag: cat.Tag + "/" + slugify(parts[1]), Name: parts[1], Kind: "topic",
		Category: cat.Tag, CategoryName: cat.Name, Topic: slugify(parts[1]), TopicName: parts[1],
	}, true
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// Capture adds "- [ ] text #tag @due _(timestamp)_" to inbox.md (above ## Done
// when there is one, else at the end) and returns the line it wrote.
func (s *Store) Capture(o CaptureOpts, now time.Time) (string, error) {
	text, create, err := buildCapture(o, s.Folders(), now)
	if err != nil {
		return "", err
	}
	if create != nil {
		dir, err := s.Resolve(create.Path)
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
	}
	line := fmt.Sprintf("- [ ] %s _(%s)_", text, now.Format("Jan 02 15:04"))
	full, err := s.Resolve("inbox.md")
	if err != nil {
		return "", err
	}
	_ = createExclusive(full, []byte("# Inbox\n\n"))
	data, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	lines, lf := splitLines(string(data))
	if out, ok := insertOpen(lines, []string{line}); ok {
		return line, writeAtomic(full, []byte(joinLines(out, lf)))
	}
	prefix := ""
	if len(data) > 0 && data[len(data)-1] != '\n' {
		prefix = "\n"
	}
	f, err := os.OpenFile(full, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(prefix + line + "\n"); err != nil {
		f.Close()
		return "", err
	}
	return line, f.Close()
}
