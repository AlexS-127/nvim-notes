package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Topic quizzes: "quiz me on X", whenever you want. Unlike a revision nothing is scheduled and
// nothing is recorded in the revision schedule; XP, quiz time and the per-question stats count as
// in any quiz. You give a topic (free text), optionally one note or a class folder. The notes that
// match are found without Claude (topicNotes), Claude writes the questions from them (or, when no
// note matches, from well-established general knowledge), and the bank is saved to
// .topics/questions/<slug>.md in the same format as every other bank. The quiz then asks each
// question once (the terminal's `quiz.py --topic`, the viewer's mode "topic").

const (
	topicMaxNotes   = 4
	topicNoteChars  = 14000
	topicDefaultN   = 10
	topicMinN       = 3
	topicMaxN       = 20
	topicGenTimeout = 4 * time.Minute
	topicSubject    = ".topics" // stats of a topic no note matched live in <notes>/.topics/.quiz_stats.json
)

// TopicBank is a written bank and where it came from.
type TopicBank struct {
	Topic   string   `json:"topic"`
	Title   string   `json:"title"`
	Path    string   `json:"path"`    // absolute
	Subject string   `json:"subject"` // folder whose stats and XP the quiz uses
	Notes   []string `json:"notes"`   // the notes the questions come from (none: general knowledge)
	Count   int      `json:"count"`
}

var topicStop = map[string]bool{"the": true, "and": true, "for": true, "with": true, "what": true, "about": true, "quiz": true,
	"notes": true, "from": true, "that": true, "this": true, "how": true, "are": true, "does": true, "why": true, "can": true}

func topicTokens(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len(w) >= 3 && !topicStop[w] && !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}

// topicNotes finds the notes a topic is about: the given note, else the best matches by title,
// headings and body (inside one folder when subject is given). Quiz banks, the mistakes list,
// daily notes and hidden or format folders are not searched.
func (s *Store) topicNotes(topic, id, subject string) []string {
	if id != "" {
		if s.revisable(id) && !strings.Contains(id, "..") {
			return []string{id}
		}
		return nil
	}
	toks := topicTokens(topic)
	if len(toks) == 0 {
		return nil
	}
	phrase := strings.ToLower(strings.TrimSpace(topic))
	type hit struct {
		id    string
		score int
	}
	var hits []hit
	root := s.Root
	if subject != "" {
		root = filepath.Join(s.Root, subject)
	}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if p != s.Root && (strings.HasPrefix(name, ".") || name == "format" || name == "daily" || name == "assets") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(s.Root, p)
		if err != nil || !s.revisable(filepath.ToSlash(rel)) {
			return nil
		}
		rel = filepath.ToSlash(rel)
		src, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		title := strings.ToLower(Title(rel, src))
		score := 0
		if strings.Contains(title, phrase) {
			score += 10
		}
		low := strings.ToLower(string(src))
		for _, t := range toks {
			if strings.Contains(title, t) {
				score += 6
			}
			n := 0
			for _, l := range strings.Split(low, "\n") {
				if strings.HasPrefix(l, "#") && strings.Contains(l, t) {
					n++
				}
			}
			score += min(n, 3) * 3
			score += min(strings.Count(low, t), 5)
		}
		if score >= 3 {
			hits = append(hits, hit{rel, score})
		}
		return nil
	})
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].id < hits[j].id
	})
	var out []string
	for _, h := range hits {
		if len(out) < topicMaxNotes {
			out = append(out, h.id)
		}
	}
	return out
}

func (s *Store) topicBankPath(topic string) string {
	slug := slugify(topic)
	if len(slug) > 60 {
		slug = slug[:60]
	}
	if slug == "" {
		slug = "topic"
	}
	return filepath.Join(s.Root, ".topics", "questions", slug+".md")
}

func (s *Store) topicPrompt(topic string, notes []string, n int, subject string) string {
	spec, guide := s.genGuidance(strings.TrimPrefix(subject, "."))
	var material strings.Builder
	var src string
	if len(notes) == 0 {
		src = "general knowledge"
		material.WriteString("No note of the student's matches this topic. Write from well-established, university-level knowledge only, and only facts you are certain of; if the topic is too vague or obscure to write good questions, write fewer.\n")
	} else {
		src = strings.Join(notes, ", ")
		for _, id := range notes {
			b, _ := os.ReadFile(filepath.Join(s.Root, id))
			text := string(b)
			if len(text) > topicNoteChars {
				text = text[:topicNoteChars] + "\n[…note cut…]"
			}
			fmt.Fprintf(&material, "=== NOTE: %s (%s) ===\n%s\n\n", s.noteTitle(id), id, text)
		}
	}
	return fmt.Sprintf(`You write questions for a student's on-demand quiz on ONE TOPIC: %q.

Below are (1) the quiz's question format, (2) the student's guidance on what makes a good question and
(3) the student's own notes that match the topic (if any). Write %d questions about the topic,
following the guidance, answerable from the notes below (or, when no notes are given, from the general
knowledge described). Cover the topic's key ideas, not everything in the notes: skip what is only about
other topics. Put "[gen]" at the end of each Q: line and "Src: %s" on each block. Add a short Why: to each.

Ignore the format file's instructions about writing files, headings, reporting or code blocks: output
ONLY the question blocks as plain text, no heading, no code fence, no commentary.

=== QUESTION FORMAT ===
%s

=== HOW TO WRITE GOOD QUESTIONS ===
%s

%s`, topic, n, src, spec, guide, material.String())
}

// GenerateTopicBank finds the notes and has Claude write n questions about the topic. subject, when
// given, limits the search to that class folder; id names one note.
func (s *Store) GenerateTopicBank(topic, id, subject string, n int) (*TopicBank, error) {
	topic = strings.TrimSpace(topic)
	if topic == "" && id != "" {
		topic = s.noteTitle(id)
	}
	if topic == "" {
		return nil, fmt.Errorf("%w: say what to be quizzed on", ErrRevision)
	}
	if n <= 0 {
		n = topicDefaultN
	}
	n = min(max(n, topicMinN), topicMaxN)
	if subject != "" {
		if strings.ContainsAny(subject, "/\\") || strings.HasPrefix(subject, ".") {
			return nil, fmt.Errorf("%w: no such class %q", ErrRevision, subject)
		}
		if st, err := os.Stat(filepath.Join(s.Root, subject)); err != nil || !st.IsDir() {
			return nil, fmt.Errorf("%w: no such class %q", ErrRevision, subject)
		}
	}
	if id != "" && len(s.topicNotes(topic, id, "")) == 0 {
		return nil, fmt.Errorf("%w: no such note %q", ErrRevision, id)
	}
	notes := s.topicNotes(topic, id, subject)
	subj := subject
	switch {
	case len(notes) > 0:
		subj = strings.SplitN(notes[0], "/", 2)[0]
	case subj == "":
		subj = topicSubject
	}
	prompt := s.topicPrompt(topic, notes, n, subj)
	out, err := runClaude(prompt, topicGenTimeout)
	if err != nil {
		return nil, err
	}
	title := topic
	if id != "" && topic == s.noteTitle(id) {
		title = s.noteTitle(id)
	}
	text := dropWeakQuestions(cleanGenerated(out, title))
	got := countQuestionBlocks(text)
	if got < topicMinN {
		return nil, fmt.Errorf("claude wrote %d usable questions (need %d): try a more specific topic", got, topicMinN)
	}
	p := s.topicBankPath(topic)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	if err := writeAtomic(p, []byte(text)); err != nil {
		return nil, err
	}
	if subj == topicSubject {
		_ = os.MkdirAll(filepath.Join(s.Root, subj), 0o755)
	}
	return &TopicBank{Topic: topic, Title: title, Path: p, Subject: subj, Notes: notes, Count: got}, nil
}

// TopicNote is a note offered in the topic box.
type TopicNote struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// TopicChoices lists the class folders and notes a topic quiz can be limited to (same notes as
// topicNotes searches), for the Quiz tab's boxes.
func (s *Store) TopicChoices() (classes []string, notes []TopicNote) {
	seen := map[string]bool{}
	_ = filepath.WalkDir(s.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != s.Root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "format" || d.Name() == "daily" || d.Name() == "assets") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(s.Root, p)
		if err != nil || !s.revisable(filepath.ToSlash(rel)) {
			return nil
		}
		rel = filepath.ToSlash(rel)
		notes = append(notes, TopicNote{rel, s.noteTitle(rel)})
		if c := strings.SplitN(rel, "/", 2)[0]; !seen[c] {
			seen[c] = true
			classes = append(classes, c)
		}
		return nil
	})
	sort.Strings(classes)
	sort.SliceStable(notes, func(i, j int) bool { return notes[i].ID < notes[j].ID })
	return classes, notes
}

// runTopicCommand implements `notesview topic …`.
//
//	topic gen [--note NOTE] [--class FOLDER] [--n N] TOPIC…   write the questions, print the bank (--json)
//	topic find [--class FOLDER] TOPIC…                        which notes would be used
func runTopicCommand(store *Store, args []string, asJSON bool, stdout, stderr io.Writer, now time.Time) int {
	fail := func(err error) int {
		fmt.Fprintln(stderr, "notesview:", strings.TrimPrefix(err.Error(), "revision: "))
		return 1
	}
	if len(args) == 0 || (args[0] != "gen" && args[0] != "find") {
		fmt.Fprintln(stderr, "usage: notesview topic gen|find [--note NOTE] [--class FOLDER] [--n N] [--json] TOPIC…")
		return 2
	}
	verb, n, note, class := args[0], 0, "", ""
	var words []string
	for i := 1; i < len(args); i++ {
		switch a := args[i]; a {
		case "--json", "-json":
			asJSON = true
		case "--note", "--class", "--n":
			if i+1 >= len(args) {
				return fail(fmt.Errorf("%s needs a value", a))
			}
			v := args[i+1]
			i++
			switch a {
			case "--note":
				note = v
			case "--class":
				class = v
			default:
				var err error
				if n, err = strconv.Atoi(v); err != nil {
					return fail(fmt.Errorf("--n needs a number"))
				}
			}
		default:
			words = append(words, a)
		}
	}
	topic := strings.Join(words, " ")
	if verb == "find" {
		notes := store.topicNotes(topic, note, class)
		if asJSON {
			b, _ := json.Marshal(notes)
			fmt.Fprintln(stdout, string(b))
			return 0
		}
		for _, id := range notes {
			fmt.Fprintln(stdout, id)
		}
		return 0
	}
	b, err := store.GenerateTopicBank(topic, note, class, n)
	if err != nil {
		return fail(err)
	}
	if asJSON {
		j, _ := json.Marshal(b)
		fmt.Fprintln(stdout, string(j))
		return 0
	}
	src := "general knowledge"
	if len(b.Notes) > 0 {
		src = strings.Join(b.Notes, ", ")
	}
	fmt.Fprintf(stdout, "%d questions on %q from %s\n%s\n", b.Count, b.Title, src, b.Path)
	return 0
}
