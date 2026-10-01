package main

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ErrOutside is returned when a requested path escapes the notes folder.
var ErrOutside = errors.New("path is outside the notes folder")

// Store gives safe access to a folder of markdown notes.
type Store struct {
	Root string // absolute, symlinks resolved
}

func NewStore(dir string) (*Store, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	return &Store{Root: real}, nil
}

// Resolve maps a slash-separated path relative to the notes folder to an
// absolute filesystem path, refusing anything that would land outside it
// (via "..", absolute-looking input, or symlinks).
func (s *Store) Resolve(rel string) (string, error) {
	if rel == "" || strings.ContainsRune(rel, 0) {
		return "", ErrOutside
	}
	rel = strings.ReplaceAll(rel, "\\", "/")
	for _, part := range strings.Split(rel, "/") {
		if part == ".." {
			return "", ErrOutside
		}
	}
	clean := path.Clean("/" + rel)[1:]
	if clean == "" {
		return "", ErrOutside
	}
	full := filepath.Join(s.Root, filepath.FromSlash(clean))
	// Resolve symlinks on the deepest existing ancestor and re-check.
	probe := full
	for {
		if real, err := filepath.EvalSymlinks(probe); err == nil {
			if !within(s.Root, real) {
				return "", ErrOutside
			}
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		probe = parent
	}
	if !within(s.Root, full) {
		return "", ErrOutside
	}
	return full, nil
}

func within(root, p string) bool {
	r, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}

// Rel converts a user-supplied path (absolute or relative) to a path
// relative to the notes folder.
func (s *Store) Rel(p string) (string, error) {
	if filepath.IsAbs(p) {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			p = real
		}
		r, err := filepath.Rel(s.Root, p)
		if err != nil || !within(s.Root, p) {
			return "", ErrOutside
		}
		return filepath.ToSlash(r), nil
	}
	if _, err := s.Resolve(p); err != nil {
		return "", err
	}
	return path.Clean(filepath.ToSlash(p)), nil
}

// Files lists every markdown note (slash paths relative to Root), skipping
// hidden directories.
func (s *Store) Files() []string {
	var out []string
	filepath.WalkDir(s.Root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if p != s.Root && strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(strings.ToLower(name), ".md") && !strings.HasPrefix(name, ".") {
			r, _ := filepath.Rel(s.Root, p)
			out = append(out, filepath.ToSlash(r))
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func (s *Store) Read(rel string) ([]byte, error) {
	full, err := s.Resolve(rel)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(full)
}

// ResolveWiki finds the note a [[target]] refers to. Matching is
// case-insensitive, first by full path (without .md), then by file name,
// preferring the shallowest path.
func (s *Store) ResolveWiki(target string) (string, bool) {
	return resolveWiki(s.Files(), target)
}

func resolveWiki(files []string, target string) (string, bool) {
	if i := strings.IndexByte(target, '#'); i >= 0 {
		target = target[:i]
	}
	target = strings.TrimSpace(strings.ReplaceAll(target, "\\", "/"))
	target = strings.TrimSuffix(strings.TrimPrefix(target, "/"), ".md")
	if target == "" {
		return "", false
	}
	want := strings.ToLower(target)
	best := ""
	for _, f := range files {
		key := strings.ToLower(strings.TrimSuffix(f, path.Ext(f)))
		if key == want {
			return f, true
		}
		if !strings.Contains(want, "/") && path.Base(key) == want {
			if best == "" || strings.Count(f, "/") < strings.Count(best, "/") {
				best = f
			}
		}
	}
	return best, best != ""
}

var (
	wikiRe  = regexp.MustCompile(`\[\[([^\]\|\n]+?)(?:\|[^\]\n]*)?\]\]`)
	taskRe  = regexp.MustCompile(`^\s*[-*+] \[ \] ?(.*)$`)
	fenceRe = regexp.MustCompile("^\\s*(```|~~~)")
	h1Re    = regexp.MustCompile(`^#\s+(.+?)\s*#*\s*$`)
	// blockRe matches the block markers at the start of a line (heading,
	// quote, list item, task box) that a search snippet leaves out.
	blockRe = regexp.MustCompile(`^(?:#{1,6}\s+|>\s?|[-*+]\s+(?:\[[ xX]\]\s+)?|\d+[.)]\s+(?:\[[ xX]\]\s+)?)+`)
)

// Title is the first level-1 heading as plain text, or the file name.
func Title(rel string, src []byte) string {
	for _, l := range strings.Split(string(src), "\n") {
		if m := h1Re.FindStringSubmatch(strings.TrimRight(l, "\r")); m != nil {
			if t := PlainText(m[1]); t != "" {
				return t
			}
		}
	}
	return strings.TrimSuffix(path.Base(rel), path.Ext(rel))
}

type Task struct {
	Line int    `json:"line"`
	Text string `json:"text"`
	HTML string `json:"html,omitempty"` // Text rendered as inline markdown
}

type NoteTasks struct {
	Path  string `json:"path"`
	Title string `json:"title"`
	Tasks []Task `json:"tasks"`
}

// Tasks gathers every open "- [ ]" item across all notes (ignoring fenced
// code), grouped by note.
func (s *Store) Tasks() []NoteTasks {
	out := []NoteTasks{}
	for _, f := range s.Files() {
		src, err := s.Read(f)
		if err != nil {
			continue
		}
		var tasks []Task
		inFence := false
		for i, l := range strings.Split(string(src), "\n") {
			l = strings.TrimRight(l, "\r")
			if fenceRe.MatchString(l) {
				inFence = !inFence
				continue
			}
			if inFence {
				continue
			}
			if m := taskRe.FindStringSubmatch(l); m != nil {
				tasks = append(tasks, Task{Line: i + 1, Text: m[1]})
			}
		}
		if len(tasks) > 0 {
			out = append(out, NoteTasks{Path: f, Title: Title(f, src), Tasks: tasks})
		}
	}
	return out
}

// ToggleCheckbox flips the checkbox on one 1-based line, rewriting the file
// atomically and leaving every other byte untouched.
func (s *Store) ToggleCheckbox(rel string, line int) error {
	full, err := s.Resolve(rel)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return err
	}
	lines := strings.SplitAfter(string(data), "\n")
	if line < 1 || line > len(lines) {
		return fmt.Errorf("line %d out of range", line)
	}
	l := lines[line-1]
	switch {
	case taskOpenRe.MatchString(l):
		l = taskOpenRe.ReplaceAllString(l, "${1}[x]")
	case taskDoneRe.MatchString(l):
		l = taskDoneRe.ReplaceAllString(l, "${1}[ ]")
	default:
		return fmt.Errorf("line %d is not a task", line)
	}
	lines[line-1] = l
	return writeAtomic(full, []byte(strings.Join(lines, "")))
}

var (
	taskOpenRe = regexp.MustCompile(`^(\s*[-*+] )\[ \]`)
	taskDoneRe = regexp.MustCompile(`^(\s*[-*+] )\[[xX]\]`)
)

func writeAtomic(full string, data []byte) error {
	mode := os.FileMode(0o644)
	if st, err := os.Stat(full); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(full), ".notesview-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, full)
}

// Backlinks lists notes that contain a [[wiki link]] resolving to rel.
func (s *Store) Backlinks(rel string) []map[string]string {
	files := s.Files()
	out := []map[string]string{}
	for _, f := range files {
		if f == rel {
			continue
		}
		src, err := s.Read(f)
		if err != nil {
			continue
		}
		for _, m := range wikiRe.FindAllStringSubmatch(string(src), -1) {
			if r, ok := resolveWiki(files, m[1]); ok && r == rel {
				out = append(out, map[string]string{"path": f, "title": Title(f, src)})
				break
			}
		}
	}
	return out
}

type SearchHit struct {
	Path    string `json:"path"`
	Title   string `json:"title"`
	Snippet string `json:"snippet,omitempty"`
}

// Search filters notes by title and full text (case-insensitive).
func (s *Store) Search(q string) []SearchHit {
	q = strings.ToLower(strings.TrimSpace(q))
	out := []SearchHit{}
	if q == "" {
		return out
	}
	for _, f := range s.Files() {
		src, err := s.Read(f)
		if err != nil {
			continue
		}
		title := Title(f, src)
		if strings.Contains(strings.ToLower(title), q) || strings.Contains(strings.ToLower(f), q) {
			out = append(out, SearchHit{Path: f, Title: title})
			continue
		}
		for _, l := range strings.Split(string(src), "\n") {
			if i := strings.Index(strings.ToLower(l), q); i >= 0 {
				l = PlainText(blockRe.ReplaceAllString(strings.TrimSpace(l), ""))
				if r := []rune(l); len(r) > 120 {
					l = string(r[:120]) + "…"
				}
				out = append(out, SearchHit{Path: f, Title: title, Snippet: l})
				break
			}
		}
	}
	return out
}

type TreeNode struct {
	Name     string      `json:"name"`
	Path     string      `json:"path"`
	Dir      bool        `json:"dir,omitempty"`
	Children []*TreeNode `json:"children,omitempty"`
}

// Tree builds the sidebar tree. inbox.md is pinned first, folders come
// before files, and daily/ is sorted newest first.
func (s *Store) Tree() []*TreeNode {
	root := &TreeNode{Dir: true}
	dirs := map[string]*TreeNode{"": root}
	for _, f := range s.Files() {
		parts := strings.Split(f, "/")
		cur := root
		for i, part := range parts {
			p := strings.Join(parts[:i+1], "/")
			if i == len(parts)-1 {
				cur.Children = append(cur.Children, &TreeNode{Name: strings.TrimSuffix(part, path.Ext(part)), Path: f})
				break
			}
			d, ok := dirs[p]
			if !ok {
				d = &TreeNode{Name: part, Path: p, Dir: true}
				dirs[p] = d
				cur.Children = append(cur.Children, d)
			}
			cur = d
		}
	}
	var sortNode func(n *TreeNode)
	sortNode = func(n *TreeNode) {
		daily := n.Path == "daily"
		sort.SliceStable(n.Children, func(i, j int) bool {
			a, b := n.Children[i], n.Children[j]
			if n.Path == "" {
				if (a.Path == "inbox.md") != (b.Path == "inbox.md") {
					return a.Path == "inbox.md"
				}
			}
			if a.Dir != b.Dir {
				return a.Dir
			}
			if daily {
				return a.Name > b.Name
			}
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		})
		for _, c := range n.Children {
			if c.Dir {
				sortNode(c)
			}
		}
	}
	sortNode(root)
	return root.Children
}
