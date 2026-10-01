package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// errCancelled ends an interactive capture without saving.
var errCancelled = errors.New("cancelled")

// noneChoice is the first entry of the folder picker.
const noneChoice = "none"

// captureUI is what the interactive capture talks to: a line reader for the
// prompts, an output, and a folder picker (fzf in a terminal).
type captureUI struct {
	in   *bufio.Reader
	out  io.Writer
	pick func(choices []string) (string, bool) // false: skipped (Esc)
}

func (ui *captureUI) ask(prompt string) (string, error) {
	fmt.Fprint(ui.out, prompt)
	line, err := ui.in.ReadString('\n')
	if err != nil && (line == "" || err != io.EOF) {
		fmt.Fprintln(ui.out)
		return "", errCancelled
	}
	return strings.TrimSpace(line), nil
}

// folderChoices is the picker list: "none", then every category and topic tag.
func folderChoices(idx *FolderIndex) []string {
	out := []string{noneChoice}
	for _, f := range idx.List {
		out = append(out, f.Tag)
	}
	return out
}

// askDue loops until the answer is empty (no date) or a date the user
// confirmed. It returns the ISO date or "".
func askDue(ui *captureUI, now time.Time) (string, error) {
	answer, err := ui.ask("Due (fri, tomorrow, oct6, 10/6, +3d — empty to skip): ")
	for {
		if err != nil {
			return "", err
		}
		if answer == "" {
			return "", nil
		}
		d, ok := ParseDue(answer, now)
		if !ok {
			answer, err = ui.ask(fmt.Sprintf("  Can't read %q. Due (empty to skip): ", answer))
			continue
		}
		next, err2 := ui.ask(fmt.Sprintf("  %s → %s  (Enter to confirm, or type another date): ",
			strings.TrimPrefix(answer, "@"), DueLabel(d)))
		if err2 != nil {
			return "", err2
		}
		if next == "" {
			return d.Format(isoDate), nil
		}
		answer = next
	}
}

// CaptureParse says which capture steps the task text already answers:
// a #tag naming a folder, an @date (natural dates are converted first).
type CaptureParse struct {
	Text   string `json:"text"`
	Folder string `json:"folder"` // folder tag, or ""
	Due    string `json:"due"`    // YYYY-MM-DD, or ""
}

func ParseCaptureText(text string, idx *FolderIndex, now time.Time) CaptureParse {
	p := CaptureParse{Text: ConvertNaturalDates(strings.TrimSpace(text), now)}
	var tags []string
	p.Due, tags = scanTask(p.Text)
	for _, tag := range tags {
		if f, ok := idx.ForTag(tag); ok {
			p.Folder = f.Tag
			break
		}
	}
	return p
}

// runCaptureInteractive asks for the task text (unless prefilled), a folder
// and a due date, then appends the task to inbox.md. Steps the text already
// answers (a folder #tag, an @date) are skipped.
func runCaptureInteractive(store *Store, prefill string, ui *captureUI, now time.Time) (string, error) {
	idx := store.Folders()
	text := strings.TrimSpace(prefill)
	var err error
	if text != "" {
		fmt.Fprintf(ui.out, "Task: %s\n", text)
	}
	for text == "" {
		if text, err = ui.ask("Task: "); err != nil {
			return "", err
		}
	}
	parsed := ParseCaptureText(text, idx, now)
	text, folder, due := parsed.Text, parsed.Folder, parsed.Due
	if folder != "" {
		fmt.Fprintf(ui.out, "Folder: #%s (from the text)\n", folder)
	} else if len(idx.List) > 0 {
		if choice, ok := ui.pick(folderChoices(idx)); ok && choice != noneChoice {
			folder = choice
		}
		if folder != "" {
			fmt.Fprintf(ui.out, "Folder: #%s\n", folder)
		} else {
			fmt.Fprintln(ui.out, "Folder: none")
		}
	}

	if due == "" {
		if due, err = askDue(ui, now); err != nil {
			return "", err
		}
	}
	return store.Capture(CaptureOpts{Text: text, Folder: folder, Due: due}, now)
}

// terminalPicker picks a folder with fzf, or from a numbered list when fzf
// is not installed.
func terminalPicker(in *bufio.Reader, out io.Writer) func([]string) (string, bool) {
	return func(choices []string) (string, bool) {
		if fzf, err := exec.LookPath("fzf"); err == nil {
			cmd := exec.Command(fzf, "--prompt", "Folder> ", "--height", "40%", "--reverse",
				"--header", "Enter: choose · Esc: no folder", "--no-multi")
			cmd.Stdin = strings.NewReader(strings.Join(choices, "\n") + "\n")
			cmd.Stderr = os.Stderr // fzf draws on the terminal itself
			b, err := cmd.Output()
			if err != nil { // Esc (130) or no match (1): skip
				return "", false
			}
			return strings.TrimSpace(string(b)), true
		}
		for i, c := range choices {
			fmt.Fprintf(out, "  %2d) %s\n", i, c)
		}
		fmt.Fprint(out, "Folder (number or name, empty for none): ")
		line, _ := in.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			return "", false
		}
		if n, err := strconv.Atoi(line); err == nil && n >= 0 && n < len(choices) {
			return choices[n], true
		}
		for _, c := range choices { // first one containing what was typed
			if strings.Contains(c, strings.ToLower(line)) {
				return c, true
			}
		}
		return "", false
	}
}
