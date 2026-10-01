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
// daily template. With carry set it moves open non-class tasks from the
// most recent earlier daily note into the new note's Tasks section.
func (s *Store) EnsureDaily(cfg *Config, date time.Time, carry bool) (DailyResult, error) {
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
					oldLines, moved, n = carryOver(strings.Split(string(data), "\n"), cfg, prev, date.Format(isoDate))
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
// move to the note named target (YYYY-MM-DD). Open tasks that are not
// homework move with their nested lines; homework (and anything nested
// under open homework) never moves. Each moved task is left behind as
// "- [>] text → [[target]]".
func carryOver(lines []string, cfg *Config, rel, target string) (keep, moved []string, n int) {
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
	isHomework := func(text string) bool {
		_, ok := classOf(cfg, rel, tagsOf(inlineCode.ReplaceAllString(text, "")))
		return ok
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
		if isHomework(text) {
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
		// nested lines move too, except homework subtasks and their children
		for j := i + 1; j < end; {
			if cind, _, ctext, cok := isTask(j); cok && isHomework(ctext) {
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

// ── Classes and lecture notes ────────────────────────────────────

func classIndexTemplate(c Class) string {
	return fmt.Sprintf("# %s\n\nLecture notes for %s, one per day (`<Space>nc` in Neovim).\n\n"+
		"Homework: any task (`- [ ]`) in this folder, or tagged `#%s` anywhere else, "+
		"shows up under **Homework** in the Tasks view (`<Space>nt`).\n", c.Name, c.Name, c.ID)
}

func lectureTemplate(c Class, date time.Time) string {
	return fmt.Sprintf("# %s — %s\n\n## Topics\n\n## Notes\n\n## Key terms\n\n## Questions\n\n## Homework\n\n",
		c.Name, date.Format("Monday, January 2 2006"))
}

// EnsureClassIndex creates classes/<folder>/index.md if it is missing.
func (s *Store) EnsureClassIndex(c Class) (string, error) {
	rel := c.Dir() + "/index.md"
	full, err := s.Resolve(rel)
	if err != nil {
		return "", err
	}
	if err := createExclusive(full, []byte(classIndexTemplate(c))); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	return rel, nil
}

// EnsureLecture creates the class index and the lecture note for date
// (classes/<folder>/YYYY-MM-DD.md) when missing, and returns its path.
func (s *Store) EnsureLecture(c Class, date time.Time) (string, error) {
	if _, err := s.EnsureClassIndex(c); err != nil {
		return "", err
	}
	rel := c.Dir() + "/" + date.Format(isoDate) + ".md"
	full, err := s.Resolve(rel)
	if err != nil {
		return "", err
	}
	if err := createExclusive(full, []byte(lectureTemplate(c, date))); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	return rel, nil
}

// ── Capture ──────────────────────────────────────────────────────

// Capture appends "- [ ] text _(timestamp)_" to inbox.md, converting natural
// due dates first. It returns the line it wrote.
func (s *Store) Capture(text string, now time.Time) (string, error) {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return "", errors.New("nothing to capture")
	}
	line := fmt.Sprintf("- [ ] %s _(%s)_", ConvertNaturalDates(text, now), now.Format("Jan 02 15:04"))
	full, err := s.Resolve("inbox.md")
	if err != nil {
		return "", err
	}
	_ = createExclusive(full, []byte("# Inbox\n\n"))
	data, err := os.ReadFile(full)
	if err != nil {
		return "", err
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
