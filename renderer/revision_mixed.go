package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Cross-topic questions: one bank per folder, .revision/questions/_mixed/<subject>.md, written by
// Claude from up to revMixedTopics of the folder's notes at once. Every question needs ideas from at
// least two of them and lists the notes it needs in `Src:`. quiz.py adds them to a blended revision
// when two or more of the listed notes are in the session. Rewritten when a member's questions are
// newer than the bank or the bank is a week old (new mix, new questions).

const (
	revMixedTopics = 4
	revMixedMaxAge = 7 * 24 * time.Hour
	revMixedRetry  = 6 * time.Hour
	revMixedNote   = 8000 // characters of each note given to Claude
	revMixedDir    = "_mixed"
)

var (
	mixedMu    sync.Mutex
	mixedTried = map[string]time.Time{} // subject -> last attempt, so a failure is not retried at once
)

// MixedPath is where a folder's cross-topic questions live.
func (s *Store) MixedPath(subject string) string {
	return s.revPath(revQuestions, revMixedDir, subject+".md")
}

// mixedMembers picks the topics a folder's cross-topic bank is written from: those with questions,
// due soonest first, at most revMixedTopics.
func (s *Store) mixedMembers(subject string, now time.Time) []Topic {
	due := s.RevisionDue(now)
	rest := s.Topics()
	sort.SliceStable(rest, func(i, j int) bool { return rest[i].Due < rest[j].Due })
	seen := map[string]bool{}
	var out []Topic
	for _, t := range append(due, rest...) {
		if t.Subject != subject || seen[t.ID] || t.Gen.Status != "ok" {
			continue
		}
		seen[t.ID] = true
		if _, err := os.Stat(s.QuestionsPath(t.ID)); err == nil {
			out = append(out, t)
		}
		if len(out) == revMixedTopics {
			break
		}
	}
	return out
}

// needsMixed is a folder whose cross-topic bank should be (re)written, with its members, or "".
func (s *Store) needsMixed(now time.Time) (string, []Topic) {
	subjects := map[string]bool{}
	for _, t := range s.Topics() {
		subjects[t.Subject] = true
	}
	var names []string
	for n := range subjects {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, sub := range names {
		mixedMu.Lock()
		tried := mixedTried[sub]
		mixedMu.Unlock()
		if now.Sub(tried) < revMixedRetry {
			continue
		}
		members := s.mixedMembers(sub, now)
		if len(members) < 2 {
			continue
		}
		st, err := os.Stat(s.MixedPath(sub))
		stale := err != nil || now.Sub(st.ModTime()) > revMixedMaxAge
		if !stale {
			for _, m := range members {
				if ms, e := os.Stat(s.QuestionsPath(m.ID)); e == nil && ms.ModTime().After(st.ModTime()) {
					stale = true
				}
			}
		}
		if stale {
			return sub, members
		}
	}
	return "", nil
}

func (s *Store) mixedPrompt(subject string, members []Topic) string {
	spec, guide := s.genGuidance(subject)
	var notes strings.Builder
	var ids []string
	for _, m := range members {
		b, _ := os.ReadFile(filepath.Join(s.Root, m.ID))
		text := string(b)
		if r := []rune(text); len(r) > revMixedNote {
			text = string(r[:revMixedNote])
		}
		fmt.Fprintf(&notes, "\n=== NOTE: %s (%s) ===\n%s\n", m.Title, m.ID, text)
		ids = append(ids, m.ID)
	}
	return fmt.Sprintf(`You write revision questions for a student's spaced-repetition quiz.

Below are (1) the quiz's question format, (2) the student's guidance on what makes a good question and
(3) several of the student's own notes from one course. Write 4 to 6 CROSS-TOPIC questions: each must
REQUIRE ideas from at least two of the notes (combine a rule from one with a case from another, tell
apart things the notes treat separately, or use both in one worked example). A question answerable from
a single note does not belong. Follow the guidance for kinds, distractors and quality.

Put "[gen]" at the end of each Q: line. Each block's "Src:" lists the ids of the notes it needs, comma
separated, using exactly these ids: %s (for example "Src: %s, %s [gen]"). Add a short Why: to each.

Ignore the format file's instructions about writing files, headings, reporting or code blocks: output
ONLY the question blocks as plain text, no heading, no code fence, no commentary.

=== QUESTION FORMAT ===
%s

=== HOW TO WRITE GOOD QUESTIONS ===
%s
%s`, strings.Join(ids, ", "), ids[0], ids[1], spec, guide, notes.String())
}

// GenerateMixed writes a folder's cross-topic bank and returns how many questions it holds.
func (s *Store) GenerateMixed(subject string, members []Topic, now time.Time) (int, error) {
	mixedMu.Lock()
	mixedTried[subject] = now
	mixedMu.Unlock()
	if len(members) < 2 {
		return 0, fmt.Errorf("%w: %s needs at least two topics with questions", ErrRevision, subject)
	}
	bin := claudeBinary()
	if bin == "" {
		return 0, fmt.Errorf("claude not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), revGenTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-p", "--tools", "")
	cmd.Stdin = strings.NewReader(s.mixedPrompt(subject, members))
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return 0, fmt.Errorf("claude: %s", firstLine(msg))
	}
	text := cleanGenerated(out.String(), "Mixed · "+subject)
	n := countQuestionBlocks(text)
	if n < revMinQuestions {
		return 0, fmt.Errorf("claude wrote %d usable questions (need %d)", n, revMinQuestions)
	}
	p := s.MixedPath(subject)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return 0, err
	}
	return n, writeAtomic(p, []byte(text))
}
