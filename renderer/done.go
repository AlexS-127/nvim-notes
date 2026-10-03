package main

import (
	"regexp"
	"strings"
)

// Ticking a task moves it (and its nested lines) to the end of the note's
// "## Done" section, creating the section if needed. Unticking one that sits in
// Done moves it back up with the other open tasks, and new captures land there
// too, so the not-done pile stays above Done.
var (
	doneHeadRe = regexp.MustCompile(`^##\s+Done\s*$`)
	anyHeadRe  = regexp.MustCompile(`^#{1,2}\s`)
	taskLineRe = regexp.MustCompile(`^\s*[-*+] \[[ xX>]\]`)
)

func indentOf(l string) int {
	return len(l) - len(strings.TrimLeft(l, " \t"))
}

// taskEnd is the end (exclusive) of the task at lines[i] with its nested lines.
func taskEnd(lines []string, i int) int {
	end, ind := i+1, indentOf(lines[i])
	for end < len(lines) && strings.TrimSpace(lines[end]) != "" && indentOf(lines[end]) > ind {
		end++
	}
	return end
}

func doneHeading(lines []string) int {
	for i, l := range lines {
		if doneHeadRe.MatchString(l) {
			return i
		}
	}
	return -1
}

// doneSectionEnd is where the Done section stops: the next heading or the end.
func doneSectionEnd(lines []string, head int) int {
	for i := head + 1; i < len(lines); i++ {
		if anyHeadRe.MatchString(lines[i]) {
			return i
		}
	}
	return len(lines)
}

func splice(lines []string, at int, block []string) []string {
	out := make([]string, 0, len(lines)+len(block))
	out = append(out, lines[:at]...)
	out = append(out, block...)
	return append(out, lines[at:]...)
}

// insertOpen puts block with the open tasks: after the last task above Done, else
// just above the Done heading. ok is false when there is no Done section.
func insertOpen(lines, block []string) ([]string, bool) {
	head := doneHeading(lines)
	if head < 0 {
		return lines, false
	}
	at := -1
	for i := 0; i < head; i++ {
		if taskLineRe.MatchString(lines[i]) {
			at = taskEnd(lines, i)
			if at > head {
				at = head
			}
			i = at - 1
		}
	}
	if at < 0 {
		at = head
		for at > 0 && strings.TrimSpace(lines[at-1]) == "" {
			at--
		}
	}
	return splice(lines, at, block), true
}

// moveToDone moves the ticked task at lines[i] to the end of the Done section.
func moveToDone(lines []string, i int) []string {
	head := doneHeading(lines)
	if head >= 0 && i > head && i < doneSectionEnd(lines, head) {
		return lines // already in Done
	}
	end := taskEnd(lines, i)
	block := append([]string(nil), lines[i:end]...)
	rest := append(append([]string(nil), lines[:i]...), lines[end:]...)
	if head = doneHeading(rest); head < 0 {
		for len(rest) > 0 && strings.TrimSpace(rest[len(rest)-1]) == "" {
			rest = rest[:len(rest)-1]
		}
		return append(append(rest, "", "## Done"), block...)
	}
	at := doneSectionEnd(rest, head)
	for at > head+1 && strings.TrimSpace(rest[at-1]) == "" {
		at--
	}
	return splice(rest, at, block)
}

// moveFromDone moves the unticked task at lines[i] out of Done, back with the open tasks.
func moveFromDone(lines []string, i int) []string {
	head := doneHeading(lines)
	if head < 0 || i < head || i >= doneSectionEnd(lines, head) {
		return lines
	}
	end := taskEnd(lines, i)
	block := append([]string(nil), lines[i:end]...)
	rest := append(append([]string(nil), lines[:i]...), lines[end:]...)
	out, _ := insertOpen(rest, block)
	return out
}

// lineFile remembers a file's line ending and trailing newline across a
// split/join, so edits to the lines don't change the rest of the file.
type lineFile struct {
	eol   string
	final bool
}

func splitLines(s string) ([]string, lineFile) {
	f := lineFile{eol: "\n", final: strings.HasSuffix(s, "\n")}
	if strings.Contains(s, "\r\n") {
		f.eol = "\r\n"
	}
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	if f.eol == "\r\n" {
		for i, l := range lines {
			lines[i] = strings.TrimSuffix(l, "\r")
		}
	}
	return lines, f
}

func joinLines(lines []string, f lineFile) string {
	s := strings.Join(lines, f.eol)
	if f.final {
		s += f.eol
	}
	return s
}
