package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Revision questions are written by Claude: `claude -p --tools ""` (no tools, so it only sees
// what it is given) gets the quiz format spec and one note, and writes 5-8 questions in that
// format. The output is checked (at least revMinQuestions valid blocks) before it is saved to
// .revision/questions/<note path>, where you can edit it. One note at a time, in the server's
// background worker; a failure is retried the next day. Banks are never rewritten on their own
// after that: the class page's Rebuild banks (classbanks.go) does it when you choose.

const (
	revMinQuestions = 3
	revGenTimeout   = 4 * time.Minute
	revGenEvery     = time.Minute
)

// claudeBinary finds the claude CLI: NOTESVIEW_CLAUDE, ~/.local/bin, Homebrew, PATH (the
// launch agent that runs the server has no PATH of its own).
func claudeBinary() string {
	if p := os.Getenv("NOTESVIEW_CLAUDE"); p != "" {
		if p == "off" {
			return ""
		}
		return p
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{filepath.Join(home, ".local", "bin", "claude"), "/opt/homebrew/bin/claude", "/usr/local/bin/claude"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("claude"); err == nil {
		return p
	}
	return ""
}

// QuestionsPath is where a topic's questions live.
func (s *Store) QuestionsPath(id string) string {
	return s.revPath(revQuestions, filepath.FromSlash(id))
}

var (
	genFenceRe = regexp.MustCompile("(?m)^```[a-z]*\\s*$")
	qLineRe = regexp.MustCompile(`(?m)^Q:\s*\S`)
)

// countQuestionBlocks counts blocks that quiz.py can ask: `Q:` with an `A:` or `Solution:` before
// the next `Q:` or heading.
func countQuestionBlocks(text string) int {
	n, open, answered := 0, false, false
	end := func() {
		if open && answered {
			n++
		}
		open, answered = false, false
	}
	for _, l := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(l, "Q:"):
			end()
			open = strings.TrimSpace(l[2:]) != ""
		case strings.HasPrefix(l, "#"):
			end()
		case strings.HasPrefix(l, "A:") && strings.TrimSpace(l[2:]) != "", strings.HasPrefix(l, "Solution:"):
			answered = open
		}
	}
	end()
	return n
}

// cleanGenerated keeps the question blocks: no code fences or chatter before the first heading
// or question, and exactly one `## <title>` heading on top.
func cleanGenerated(out, title string) string {
	out = genFenceRe.ReplaceAllString(out, "")
	if i := qLineRe.FindStringIndex(out); i != nil {
		out = out[i[0]:]
	}
	var keep []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "## ") || strings.HasPrefix(l, "# ") {
			continue // one heading, ours
		}
		keep = append(keep, l)
	}
	return "## " + title + "\n\n" + strings.TrimSpace(strings.Join(keep, "\n")) + "\n"
}

// genGuidance reads the quiz format and the student's guidance on good questions: how to write
// them is format/revision-questions.md, then format/revision-<subject>.md (both optional, edited by
// the student); a short built-in version when neither exists.
var (
	negativeStemRe = regexp.MustCompile(`\b(NOT|EXCEPT|LEAST)\b`)
	noneOfRe       = regexp.MustCompile(`(?i)\b(all|none) of the above\b`)
)

// dropWeakQuestions removes generated questions that test memorising the options instead of the
// idea: multiple choice with a negative stem ("which is NOT…", "EXCEPT") or "all/none of the above"
// (the guidance asks for select-all questions instead), and repeats of an earlier question. The
// heading and the order of the rest are kept.
func dropWeakQuestions(text string) string {
	var head []string
	var blocks [][]string
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, "Q:") {
			blocks = append(blocks, nil)
		}
		if len(blocks) == 0 {
			head = append(head, l)
		} else {
			blocks[len(blocks)-1] = append(blocks[len(blocks)-1], l)
		}
	}
	seen := map[string]bool{}
	out := strings.TrimSpace(strings.Join(head, "\n")) + "\n\n"
	for _, b := range blocks {
		stem, choices := strings.TrimSpace(strings.TrimPrefix(b[0], "Q:")), false
		weak := false
		for _, l := range b[1:] {
			t := strings.TrimSpace(l)
			if len(t) > 2 && t[1] == ')' && t[0] >= 'a' && t[0] <= 'h' {
				choices = true
				weak = weak || noneOfRe.MatchString(t)
			}
		}
		key := strings.ToLower(stem)
		if (choices && (weak || negativeStemRe.MatchString(stem))) || seen[key] {
			continue
		}
		seen[key] = true
		out += strings.TrimRight(strings.Join(b, "\n"), "\n") + "\n\n"
	}
	return strings.TrimSpace(out) + "\n"
}

func (s *Store) genGuidance(subject string) (spec, guide string) {
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(s.Root, "format", name))
		return strings.TrimSpace(string(b))
	}
	spec = read("question-format.md")
	guide = read("revision-questions.md")
	if guide == "" {
		guide = "Write 5 to 8 questions that check the key ideas of this note only, answerable from it: definitions, how and why, worked examples or calculations if the note has them. Mix the kinds (multiple choice, true/false, short answer, a worked problem with Solution: only when the note supports one)."
	}
	if extra := read("revision-" + subject + ".md"); extra != "" {
		guide += "\n\n" + extra
	}
	return spec, guide
}

func (s *Store) genPrompt(t Topic, note string) string {
	spec, guide := s.genGuidance(t.Subject)
	return fmt.Sprintf(`You write revision questions for a student's spaced-repetition quiz.

Below are (1) the quiz's question format, (2) the student's guidance on what makes a good question and
(3) one of the student's own notes. Write the questions for THIS NOTE ONLY, following the guidance.
Put "[gen]" at the end of each Q: line and "Src: %s" on each block. Add a short Why: to each.

Ignore the format file's instructions about writing files, headings, reporting or code blocks: output
ONLY the question blocks as plain text, no heading, no code fence, no commentary.

=== QUESTION FORMAT ===
%s

=== HOW TO WRITE GOOD QUESTIONS ===
%s

=== NOTE: %s (%s) ===
%s
`, t.ID, spec, guide, t.Title, t.ID, note)
}

// GenerateQuestions writes a topic's questions with Claude, records the outcome in topics.json,
// and returns how many questions were written.
func (s *Store) GenerateQuestions(id string, now time.Time) (int, error) {
	var tp *Topic
	for _, t := range s.Topics() {
		if t.ID == id {
			t := t
			tp = &t
		}
	}
	if tp == nil {
		return 0, fmt.Errorf("%w: %s is not scheduled", ErrRevision, id)
	}
	note, err := os.ReadFile(filepath.Join(s.Root, id))
	if err != nil {
		return 0, s.setGen(id, RevisionGen{Status: "failed", At: now.Format(scoreStamp), Error: err.Error()})
	}
	bin := claudeBinary()
	if bin == "" {
		err := fmt.Errorf("claude not found")
		_ = s.setGen(id, RevisionGen{Status: "failed", At: now.Format(scoreStamp), Error: err.Error()})
		return 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), revGenTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-p", "--tools", "")
	cmd.Stdin = strings.NewReader(s.genPrompt(*tp, string(note)))
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		_ = s.setGen(id, RevisionGen{Status: "failed", At: now.Format(scoreStamp), Error: firstLine(msg)})
		return 0, fmt.Errorf("claude: %s", firstLine(msg))
	}
	text := dropWeakQuestions(cleanGenerated(out.String(), tp.Title))
	n := countQuestionBlocks(text)
	if n < revMinQuestions {
		err := fmt.Errorf("claude wrote %d usable questions (need %d)", n, revMinQuestions)
		_ = s.setGen(id, RevisionGen{Status: "failed", At: now.Format(scoreStamp), Error: err.Error()})
		return 0, err
	}
	p := s.QuestionsPath(id)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return 0, err
	}
	if err := writeAtomic(p, []byte(text)); err != nil {
		return 0, err
	}
	// stamped after the write: the bank's file time then marks a later change as an edit by hand
	return n, s.setGen(id, RevisionGen{Status: "ok", At: time.Now().Format(scoreStamp), Words: countWords(string(note)), Count: n})
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

func (s *Store) setGen(id string, g RevisionGen) error {
	revMu.Lock()
	defer revMu.Unlock()
	t := s.loadTopics()
	for i := range t.Topics {
		if t.Topics[i].ID == id {
			t.Topics[i].Gen = g
			return s.saveTopics(t)
		}
	}
	return nil
}

// RequestQuestions marks a topic for (re)generation by the worker.
func (s *Store) RequestQuestions(id string) error {
	revMu.Lock()
	defer revMu.Unlock()
	t := s.loadTopics()
	for i := range t.Topics {
		if t.Topics[i].ID == id {
			t.Topics[i].Gen.Status = "pending"
			return s.saveTopics(t)
		}
	}
	return fmt.Errorf("%w: %s is not scheduled", ErrRevision, id)
}

// needsQuestions is the next topic the worker should write questions for, or "".
func (s *Store) needsQuestions(now time.Time) string {
	today := now.Format(isoDate)
	due := s.RevisionDue(now)
	order := append(due, s.Topics()...) // due topics first
	for _, t := range order {
		_, statErr := os.Stat(s.QuestionsPath(t.ID))
		switch t.Gen.Status {
		case "", "none":
			if statErr != nil { // a bank written by hand is kept
				return t.ID
			}
		case "pending":
			return t.ID
		case "failed":
			if len(t.Gen.At) < 10 || t.Gen.At[:10] < today {
				return t.ID
			}
		case "ok":
			if statErr != nil {
				return t.ID // file deleted
			}
			// a note that changed is not rewritten here: it is a reason on the class page's
			// Rebuild banks list (classbanks.go), and you decide when to rebuild
		}
	}
	return ""
}

// bankEdited reports whether a bank file was changed after Claude wrote it (by hand, or by the quiz
// adding an accepted answer). Stamps written before 2026-10-08 are the time generation started, so
// the file is allowed up to the generation timeout after the stamp.
func bankEdited(mod time.Time, at string) bool {
	gen, err := time.ParseInLocation(scoreStamp, at, mod.Location())
	if err != nil {
		return false
	}
	return mod.After(gen.Add(revGenTimeout + 5*time.Second))
}

// revisionWorker writes questions in the background, one topic at a time; kick wakes it early.
func (s *Server) revisionWorker(kick <-chan struct{}) {
	if os.Getenv("NOTESVIEW_NO_REVISION_GEN") != "" {
		return
	}
	s.store.resetRebuilding()    // a rebuild cut short by a restart (classbanks.go)
	time.Sleep(10 * time.Second) // let the score loop schedule new topics first (RecordRevision)
	t := time.NewTicker(revGenEvery)
	defer t.Stop()
	for {
		for {
			id := s.store.needsQuestions(time.Now())
			if claudeBinary() == "" {
				break
			}
			if id == "" { // every topic has its own questions: now the folders' cross-topic ones
				sub, members := s.store.needsMixed(time.Now())
				if sub == "" {
					break
				}
				if _, err := s.store.GenerateMixed(sub, members, time.Now()); err != nil {
					fmt.Fprintln(os.Stderr, "revision mixed questions:", sub+":", err)
				}
				continue
			}
			if _, err := s.store.GenerateQuestions(id, time.Now()); err != nil {
				fmt.Fprintln(os.Stderr, "revision questions:", id+":", err)
			}
		}
		select {
		case <-t.C:
		case <-kick:
		}
	}
}
