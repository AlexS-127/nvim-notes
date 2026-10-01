package main

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Folders give notes their structure: a top-level folder is a category
// (act-200, personal) and a folder inside it is a topic (act-200/chapter-5).
// Their slugified paths are tags: #act-200, #act-200/chapter-5.

// skipCategory lists top-level folders that are not categories.
var skipCategory = map[string]bool{"assets": true, "daily": true}

// slugify lowercases s and joins runs of letters and digits with "-":
// "ACT 200" → "act-200", "Chapter 5: Costs" → "chapter-5-costs".
func slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
		} else {
			dash = true
		}
	}
	return b.String()
}

// slugPath slugifies each segment of a slash path and drops empty ones.
func slugPath(p string) string {
	var parts []string
	for _, seg := range strings.Split(strings.ReplaceAll(p, "\\", "/"), "/") {
		if s := slugify(strings.TrimSuffix(seg, ".md")); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "/")
}

// Folder is a category or topic folder.
type Folder struct {
	Path         string `json:"path"` // folder path relative to the notes folder
	Tag          string `json:"tag"`  // act-200 or act-200/chapter-5
	Name         string `json:"name"` // the folder's own name
	Kind         string `json:"kind"` // "category" or "topic"
	Category     string `json:"category"`
	CategoryName string `json:"category_name"`
	Topic        string `json:"topic,omitempty"`
	TopicName    string `json:"topic_name,omitempty"`
}

func categoryDir(name string) bool {
	return name != "" && !strings.HasPrefix(name, ".") && !skipCategory[strings.ToLower(name)]
}

func topicDir(name string) bool {
	return name != "" && !strings.HasPrefix(name, ".") && strings.ToLower(name) != "assets"
}

// FolderIndex looks folders up by tag and path.
type FolderIndex struct {
	List   []Folder
	byTag  map[string]Folder
	byPath map[string]Folder
}

func NewFolderIndex(folders []Folder) *FolderIndex {
	idx := &FolderIndex{List: folders, byTag: map[string]Folder{}, byPath: map[string]Folder{}}
	for _, f := range folders {
		if _, dup := idx.byTag[f.Tag]; !dup { // two folders with the same slug: first wins
			idx.byTag[f.Tag] = f
		}
		idx.byPath[f.Path] = f
	}
	return idx
}

// Folders lists every category and topic folder, sorted by tag.
func (s *Store) Folders() *FolderIndex {
	var out []Folder
	cats, _ := os.ReadDir(s.Root)
	for _, c := range cats {
		if !c.IsDir() || !categoryDir(c.Name()) || slugify(c.Name()) == "" {
			continue
		}
		cat := Folder{Path: c.Name(), Tag: slugify(c.Name()), Name: c.Name(), Kind: "category"}
		cat.Category, cat.CategoryName = cat.Tag, cat.Name
		out = append(out, cat)
		topics, _ := os.ReadDir(filepath.Join(s.Root, c.Name()))
		for _, t := range topics {
			if !t.IsDir() || !topicDir(t.Name()) || slugify(t.Name()) == "" {
				continue
			}
			out = append(out, Folder{
				Path: c.Name() + "/" + t.Name(), Tag: cat.Tag + "/" + slugify(t.Name()), Name: t.Name(), Kind: "topic",
				Category: cat.Tag, CategoryName: cat.Name, Topic: slugify(t.Name()), TopicName: t.Name(),
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Tag < out[j].Tag })
	return NewFolderIndex(out)
}

// ForPath returns the category or topic folder a note lives in, judged by
// its path alone.
func (idx *FolderIndex) ForPath(rel string) (Folder, bool) {
	parts := strings.Split(rel, "/")
	if len(parts) < 2 || !categoryDir(parts[0]) || slugify(parts[0]) == "" {
		return Folder{}, false
	}
	cat := Folder{Path: parts[0], Tag: slugify(parts[0]), Name: parts[0], Kind: "category"}
	cat.Category, cat.CategoryName = cat.Tag, cat.Name
	if len(parts) < 3 || !topicDir(parts[1]) || slugify(parts[1]) == "" {
		return cat, true
	}
	return Folder{
		Path: parts[0] + "/" + parts[1], Tag: cat.Tag + "/" + slugify(parts[1]), Name: parts[1], Kind: "topic",
		Category: cat.Tag, CategoryName: cat.Name, Topic: slugify(parts[1]), TopicName: parts[1],
	}, true
}

// ForTag finds the folder a tag names. A tag for an unknown topic of a
// known category falls back to the category.
func (idx *FolderIndex) ForTag(tag string) (Folder, bool) {
	tag = slugPath(tag)
	if f, ok := idx.byTag[tag]; ok {
		return f, true
	}
	if i := strings.IndexByte(tag, '/'); i > 0 {
		if f, ok := idx.byTag[tag[:i]]; ok {
			return f, true
		}
	}
	return Folder{}, false
}

// Resolve finds the folder a [[link]] or picker choice names, by tag or by
// path ("act-200", "ACT 200/Chapter 5", "act-200/chapter-5/").
func (idx *FolderIndex) Resolve(target string) (Folder, bool) {
	target = strings.Trim(strings.TrimSpace(strings.ReplaceAll(target, "\\", "/")), "/")
	if i := strings.IndexByte(target, '#'); i >= 0 {
		target = target[:i]
	}
	if target == "" {
		return Folder{}, false
	}
	if f, ok := idx.byPath[path.Clean(target)]; ok {
		return f, true
	}
	f, ok := idx.byTag[slugPath(target)]
	return f, ok
}

// NoteRef names a note.
type NoteRef struct {
	Path  string `json:"path"`
	Title string `json:"title"`
}

func (s *Store) noteRef(rel string) NoteRef {
	src, _ := s.Read(rel)
	return NoteRef{Path: rel, Title: Title(rel, src)}
}

// LinkTarget is what a [[link]] points to.
type LinkTarget struct {
	Kind  string    `json:"kind"` // "note", "folder" or "missing"
	Path  string    `json:"path"` // note path or folder path
	Tag   string    `json:"tag,omitempty"`
	Name  string    `json:"name,omitempty"`
	Notes []NoteRef `json:"notes,omitempty"` // folder: every note inside it
}

// ResolveLink resolves a [[link]] target. A note with that name wins over
// a folder.
func (s *Store) ResolveLink(target string) LinkTarget {
	files := s.Files()
	if rel, ok := resolveWiki(files, target); ok {
		return LinkTarget{Kind: "note", Path: rel}
	}
	f, ok := s.Folders().Resolve(target)
	if !ok {
		return LinkTarget{Kind: "missing", Path: strings.TrimSpace(target)}
	}
	out := LinkTarget{Kind: "folder", Path: f.Path, Tag: f.Tag, Name: f.Name, Notes: []NoteRef{}}
	for _, rel := range files {
		if strings.HasPrefix(rel, f.Path+"/") {
			out.Notes = append(out.Notes, s.noteRef(rel))
		}
	}
	return out
}

// FolderPage is the generated page for a category or topic folder.
type FolderPage struct {
	Folder
	Notes  []NoteRef    `json:"notes"`  // notes directly in the folder
	Topics []TopicCount `json:"topics"` // a category's topic folders
	Tasks  []Task       `json:"tasks"`  // open tasks in this category / topic
}

type TopicCount struct {
	Folder
	Count int `json:"count"` // notes inside
}

// FolderPage lists a folder's notes, topics and open tasks (sorted by due
// date). Nothing is written to disk.
func (s *Store) FolderPage(f Folder, now time.Time) FolderPage {
	page := FolderPage{Folder: f, Notes: []NoteRef{}, Topics: []TopicCount{}, Tasks: []Task{}}
	files := s.Files()
	for _, rel := range files {
		if path.Dir(rel) == f.Path {
			page.Notes = append(page.Notes, s.noteRef(rel))
		}
	}
	if f.Kind == "category" {
		for _, t := range s.Folders().List {
			if t.Kind != "topic" || t.Category != f.Category || !strings.HasPrefix(t.Path, f.Path+"/") {
				continue
			}
			tc := TopicCount{Folder: t}
			for _, rel := range files {
				if strings.HasPrefix(rel, t.Path+"/") {
					tc.Count++
				}
			}
			page.Topics = append(page.Topics, tc)
		}
	}
	for _, t := range s.CollectTasks(TaskQuery{Now: now}) {
		if t.Category == f.Category && (f.Kind == "category" || t.Topic == f.Topic) {
			page.Tasks = append(page.Tasks, t)
		}
	}
	return page
}
