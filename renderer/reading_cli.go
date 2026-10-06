package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// runReadCommand implements `notesview read …`; it works on the files directly.
func runReadCommand(store *Store, args []string, asJSON bool, stdout, stderr io.Writer, now time.Time) int {
	fail := func(err error) int {
		fmt.Fprintln(stderr, "notesview:", strings.TrimPrefix(err.Error(), "reading: "))
		return 1
	}
	sub, rest := "list", args
	if len(args) > 0 {
		sub, rest = args[0], args[1:]
	}
	// flags may come after the subcommand: `read add Dune "Frank Herbert" --pages 412`
	pages, to, at := 0, -1, 0
	var pos []string
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--pages", "-pages", "--to", "-to", "--at", "-at":
			if i+1 >= len(rest) {
				return fail(fmt.Errorf("%s needs a number", rest[i]))
			}
			n, err := strconv.Atoi(rest[i+1])
			if err != nil || n < 0 {
				return fail(fmt.Errorf("%s needs a number", rest[i]))
			}
			if strings.HasSuffix(rest[i], "to") {
				to = n
			} else if strings.HasSuffix(rest[i], "at") {
				at = n
			} else {
				pages = n
			}
			i++
		case "--json", "-json":
			asJSON = true
		default:
			pos = append(pos, rest[i])
		}
	}
	show := func(b Book) int {
		if asJSON {
			return printJSON(stdout, b)
		}
		fmt.Fprintln(stdout, bookLine(b))
		return 0
	}
	find := func(ref string) (Book, error) {
		list := store.Books()
		i, err := FindBook(list, ref)
		if err != nil {
			return Book{}, err
		}
		return list[i], nil
	}
	setStatus := func(status string) int {
		if len(pos) != 1 {
			return fail(fmt.Errorf("usage: notesview read %s BOOK", sub))
		}
		b, err := find(pos[0])
		if err != nil {
			return fail(err)
		}
		if b, err = store.UpdateBook(BookPatch{ID: b.ID, Status: &status}, now); err != nil {
			return fail(err)
		}
		return show(b)
	}
	switch sub {
	case "list", "ls":
		books := store.Books()
		if asJSON {
			return printJSON(stdout, books)
		}
		if len(books) == 0 {
			fmt.Fprintln(stdout, `No books. Add one: notesview read add "Title" "Author" [PAGES]`)
		}
		for _, status := range []string{readReading, readToRead, readFinished} {
			first := true
			for _, b := range books {
				if b.Status != status {
					continue
				}
				if first {
					fmt.Fprintf(stdout, "%s:\n", map[string]string{readReading: "Reading", readToRead: "To read", readFinished: "Read"}[status])
					first = false
				}
				fmt.Fprintln(stdout, "  "+bookLine(b))
			}
		}
	case "add":
		if len(pos) == 3 && pages == 0 {
			n, err := strconv.Atoi(pos[2])
			if err != nil || n < 0 {
				return fail(fmt.Errorf("pages must be a number: %q", pos[2]))
			}
			pages, pos = n, pos[:2]
		}
		if len(pos) != 2 {
			return fail(fmt.Errorf(`usage: notesview read add "TITLE" "AUTHOR" [PAGES] [--at PAGE]`))
		}
		b, err := store.AddBook(pos[0], pos[1], pages, now)
		if err != nil {
			return fail(err)
		}
		if at > 0 {
			if b, _, err = store.SetStartPage(strconv.Itoa(b.ID), at, now); err != nil {
				return fail(err)
			}
		}
		if asJSON {
			return printJSON(stdout, b)
		}
		fmt.Fprintln(stdout, "Added to read:", bookLine(b))
	case "log":
		ref, n := "", 0
		switch {
		case to >= 0 && len(pos) <= 1:
			if len(pos) == 1 {
				ref = pos[0]
			}
		case to < 0 && (len(pos) == 1 || len(pos) == 2):
			var err error
			if n, err = strconv.Atoi(pos[len(pos)-1]); err != nil || n == 0 {
				return fail(fmt.Errorf("pages must be a number: %q", pos[len(pos)-1]))
			}
			if len(pos) == 2 {
				ref = pos[0]
			}
		default:
			return fail(fmt.Errorf("usage: notesview read log [BOOK] PAGES  |  notesview read log [BOOK] --to PAGE"))
		}
		b, e, err := store.LogPages(ref, n, to, now)
		if err != nil {
			return fail(err)
		}
		pts := max(0, e.Pages) / scoreReadPagesPer
		if asJSON {
			return printJSON(stdout, map[string]any{"book": b, "entry": e, "points": pts})
		}
		done := ""
		if b.Status == readFinished {
			done = "  finished!"
		}
		fmt.Fprintf(stdout, "%+d pages of %s (+%d)  %s%s\n", e.Pages, b.Title, pts, progressText(b), done)
	case "at":
		if len(pos) < 1 || len(pos) > 2 {
			return fail(fmt.Errorf("usage: notesview read at [BOOK] PAGE  (the page you were already on; scores nothing)"))
		}
		n, err := strconv.Atoi(pos[len(pos)-1])
		if err != nil || n < 0 {
			return fail(fmt.Errorf("page must be a number: %q", pos[len(pos)-1]))
		}
		ref := ""
		if len(pos) == 2 {
			ref = pos[0]
		}
		b, _, err := store.SetStartPage(ref, n, now)
		if err != nil {
			return fail(err)
		}
		if asJSON {
			return printJSON(stdout, b)
		}
		fmt.Fprintf(stdout, "%s is at page %d (no points)  %s\n", b.Title, b.Page, progressText(b))
	case "start":
		if len(pos) == 1 {
			return setStatus(readReading)
		}
		return fail(fmt.Errorf("usage: notesview read start BOOK"))
	case "done", "finish":
		return setStatus(readFinished)
	case "later", "unstart":
		return setStatus(readToRead)
	case "pages":
		if len(pos) != 2 {
			return fail(fmt.Errorf("usage: notesview read pages BOOK N (0 = unknown)"))
		}
		b, err := find(pos[0])
		if err != nil {
			return fail(err)
		}
		n, err := strconv.Atoi(pos[1])
		if err != nil {
			return fail(fmt.Errorf("pages must be a number: %q", pos[1]))
		}
		if b, err = store.UpdateBook(BookPatch{ID: b.ID, Pages: &n}, now); err != nil {
			return fail(err)
		}
		return show(b)
	case "remove", "rm":
		if len(pos) != 1 {
			return fail(fmt.Errorf("usage: notesview read remove BOOK"))
		}
		b, err := find(pos[0])
		if err != nil {
			return fail(err)
		}
		if err := store.DeleteBook(b.ID); err != nil {
			return fail(err)
		}
		fmt.Fprintln(stdout, "Removed", b.Title, "(its logged pages keep their points)")
	default:
		return fail(fmt.Errorf("usage: notesview read [list|add|log|at|start|done|later|pages|remove]"))
	}
	return 0
}

// progressText is "135/300 · 45%", or "135 pages" without a page count.
func progressText(b Book) string {
	if b.Pages > 0 {
		return fmt.Sprintf("%d/%d · %d%%", b.Page, b.Pages, b.Pct)
	}
	return fmt.Sprintf("%d pages", b.Page)
}

// bookLine is one line for a book: "3  Dune — Frank Herbert  135/412 · 32%".
func bookLine(b Book) string {
	tail := ""
	switch {
	case b.Status == readFinished:
		tail = "  read " + b.Finished
	case b.Page > 0:
		tail = "  " + progressText(b)
	case b.Pages > 0:
		tail = fmt.Sprintf("  %d pages", b.Pages)
	}
	return fmt.Sprintf("%-3d %s — %s%s", b.ID, b.Title, b.Author, tail)
}
