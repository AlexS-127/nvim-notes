package main

import (
	"bufio"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sampleNotes is a notes folder with two categories and a few topics.
func sampleNotes(t *testing.T, extra map[string]string) *Store {
	t.Helper()
	files := map[string]string{
		"ACT 200/syllabus.md":        "# Syllabus\n- [ ] buy textbook\n",
		"ACT 200/Chapter 5/costs.md": "# Costs\n- [ ] problem set 5 @2026-10-02\n",
		"ACT 200/Chapter 6/.keep.md": "",
		"personal/garden.md":         "# Garden\n",
		"personal/assets/x.md":       "- [ ] in assets\n",
		"assets/img.md":              "# not a category\n",
		"daily/2026-09-30.md":        "# Day\n",
		".hidden/secret.md":          "# hidden\n",
		"inbox.md":                   "# Inbox\n",
	}
	for k, v := range extra {
		files[k] = v
	}
	s := newTestStore(t, files)
	os.MkdirAll(filepath.Join(s.Root, "personal", "Empty Topic"), 0o755)
	return s
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"ACT 200": "act-200", "act-200": "act-200", "Chapter 5: Costs": "chapter-5-costs",
		"  Personal  ": "personal", "Café Notes": "café-notes", "a__b--c": "a-b-c", "!!!": "",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
	if got := slugPath("ACT 200/Chapter 5/"); got != "act-200/chapter-5" {
		t.Errorf("slugPath: %q", got)
	}
}

func TestFolderTags(t *testing.T) {
	s := sampleNotes(t, nil)
	idx := s.Folders()
	var got []string
	for _, f := range idx.List {
		got = append(got, f.Kind+":"+f.Tag+"="+f.Path)
	}
	want := []string{
		"category:act-200=ACT 200",
		"topic:act-200/chapter-5=ACT 200/Chapter 5",
		"topic:act-200/chapter-6=ACT 200/Chapter 6",
		"category:personal=personal",
		"topic:personal/empty-topic=personal/Empty Topic",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("folders:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// a note's folder comes from its path
	pathCases := map[string]string{
		"ACT 200/syllabus.md":         "act-200",
		"ACT 200/Chapter 5/costs.md":  "act-200/chapter-5",
		"ACT 200/Chapter 5/deep/x.md": "act-200/chapter-5",
		"personal/assets/x.md":        "personal", // assets is not a topic
		"daily/2026-09-30.md":         "",
		"assets/img.md":               "",
		".hidden/secret.md":           "",
		"inbox.md":                    "",
	}
	for rel, want := range pathCases {
		f, ok := idx.ForPath(rel)
		if (want == "") == ok || f.Tag != want {
			t.Errorf("ForPath(%q) = %q,%v; want %q", rel, f.Tag, ok, want)
		}
	}
	// tags name folders; an unknown topic falls back to its category
	tagCases := map[string]string{
		"act-200": "act-200", "ACT-200/Chapter-5": "act-200/chapter-5",
		"act-200/chapter-99": "act-200", "personal/empty-topic": "personal/empty-topic",
		"urgent": "", "daily": "", "assets": "",
	}
	for tag, want := range tagCases {
		f, ok := idx.ForTag(tag)
		if (want == "") == ok || f.Tag != want {
			t.Errorf("ForTag(%q) = %q,%v; want %q", tag, f.Tag, ok, want)
		}
	}
}

func TestParseTasksFolders(t *testing.T) {
	s := sampleNotes(t, map[string]string{
		"inbox.md": "# Inbox\n" +
			"- [ ] read ch 5 #act-200/chapter-5 @2026-10-02\n" + // 2
			"- [ ] email prof #ACT-200, today\n" + // 3
			"  - [ ] draft it\n" + // 4: inherits
			"- [ ] water plants #personal #urgent\n" + // 5
			"- [ ] groceries #urgent\n" + // 6
			"- [ ] `#act-200` in code\n" + // 7
			"- [x] done #personal\n" + // 8
			"- [>] moved → [[2026-10-01]]\n", // 9
	})
	idx := s.Folders()
	src, _ := s.Read("inbox.md")
	tasks := ParseTasks("inbox.md", src, idx)
	type want struct {
		line                   int
		state, tag, label, due string
		display                string
	}
	wants := []want{
		{2, StateOpen, "act-200/chapter-5", "ACT 200 · Chapter 5", "2026-10-02", "read ch 5"},
		{3, StateOpen, "act-200", "ACT 200", "", "email prof, today"},
		{4, StateOpen, "act-200", "ACT 200", "", "draft it"},
		{5, StateOpen, "personal", "personal", "", "water plants #urgent"},
		{6, StateOpen, "", "General", "", "groceries #urgent"},
		{7, StateOpen, "", "General", "", "`#act-200` in code"},
		{8, StateDone, "personal", "personal", "", "done"},
		{9, StateMoved, "", "General", "", "moved → [[2026-10-01]]"},
	}
	if len(tasks) != len(wants) {
		t.Fatalf("got %d tasks: %+v", len(tasks), tasks)
	}
	for i, w := range wants {
		g := tasks[i]
		if g.Line != w.line || g.State != w.state || g.Tag != w.tag || g.Label() != w.label || g.Due != w.due || g.Display != w.display {
			t.Errorf("task %d: got line=%d state=%s tag=%q label=%q due=%q display=%q\nwant %+v",
				i, g.Line, g.State, g.Tag, g.Label(), g.Due, g.Display, w)
		}
	}
	// inside a folder, the path decides (a tag for another folder does not move it)
	src2 := []byte("- [ ] ex 3 #personal\n- [ ] ex 4\n")
	for _, tk := range ParseTasks("ACT 200/Chapter 5/costs.md", src2, idx) {
		if tk.Tag != "act-200/chapter-5" || tk.CategoryName != "ACT 200" || tk.TopicName != "Chapter 5" {
			t.Errorf("folder task: %+v", tk)
		}
	}
}

func TestCollectTasksByDate(t *testing.T) {
	s := sampleNotes(t, map[string]string{
		"inbox.md": "- [ ] later @2026-12-01\n- [ ] overdue #personal @2026-09-28\n- [ ] undated\n",
	})
	var order []string
	for _, x := range s.CollectTasks(TaskQuery{Now: testNow}) {
		order = append(order, x.Group+"/"+x.Label()+"/"+x.Display)
	}
	want := []string{
		"overdue/personal/overdue",
		"tomorrow/ACT 200 · Chapter 5/problem set 5",
		"later/General/later",
		"none/ACT 200/buy textbook",
		"none/General/undated",
		"none/personal/in assets",
	}
	if strings.Join(order, "\n") != strings.Join(want, "\n") {
		t.Errorf("order:\n%s\nwant:\n%s", strings.Join(order, "\n"), strings.Join(want, "\n"))
	}
}

func TestFolderLinks(t *testing.T) {
	s := sampleNotes(t, map[string]string{
		"personal.md":  "# A note named like a folder\n", // note beats folder
		"links.md":     "[[act-200]] [[ACT 200/Chapter 5|ch5]] [[act-200/chapter-5]] [[personal]] [[nowhere]]\n",
		"elsewhere.md": "- [ ] tagged #act-200/chapter-5 @2026-10-05\n",
	})
	cases := map[string]string{
		"act-200": "folder:ACT 200", "ACT 200": "folder:ACT 200", "act-200/chapter-5": "folder:ACT 200/Chapter 5",
		"Act-200/Chapter-5/": "folder:ACT 200/Chapter 5", "personal": "note:personal.md",
		"personal/empty-topic": "folder:personal/Empty Topic", "costs": "note:ACT 200/Chapter 5/costs.md",
		"daily": "missing:daily", "nowhere": "missing:nowhere",
	}
	for target, want := range cases {
		r := s.ResolveLink(target)
		if got := r.Kind + ":" + r.Path; got != want {
			t.Errorf("ResolveLink(%q) = %s, want %s", target, got, want)
		}
	}
	if r := s.ResolveLink("act-200"); len(r.Notes) != 2 || r.Tag != "act-200" {
		t.Errorf("folder notes: %+v", r.Notes)
	}

	out, _ := NewMarkdown(s, "links.md").Render(readBytes(t, s, "links.md"))
	for _, want := range []string{
		`href="#/folder/act-200">act-200</a>`, `href="#/folder/act-200/chapter-5">ch5</a>`,
		`href="#/note/personal.md">personal</a>`, `wikilink missing`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %s:\n%s", want, out)
		}
	}

	f, _ := s.Folders().Resolve("act-200")
	page := s.FolderPage(f, testNow)
	var notes, topics, tasks []string
	for _, n := range page.Notes {
		notes = append(notes, n.Title)
	}
	for _, tp := range page.Topics {
		topics = append(topics, tp.Name)
	}
	for _, tk := range page.Tasks {
		tasks = append(tasks, tk.Group+":"+tk.Display)
	}
	if strings.Join(notes, ",") != "Syllabus" || strings.Join(topics, ",") != "Chapter 5,Chapter 6" ||
		strings.Join(tasks, ",") != "tomorrow:problem set 5,week:tagged,none:buy textbook" {
		t.Errorf("category page: notes=%v topics=%v tasks=%v", notes, topics, tasks)
	}
	f, _ = s.Folders().Resolve("act-200/chapter-5")
	page = s.FolderPage(f, testNow)
	if len(page.Notes) != 1 || len(page.Topics) != 0 || len(page.Tasks) != 2 {
		t.Errorf("topic page: %+v", page)
	}
}

func readBytes(t *testing.T, s *Store, rel string) []byte {
	t.Helper()
	b, err := s.Read(rel)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// ── daily carry-over ──

func TestDailyCarryOver(t *testing.T) {
	old := "# Tuesday\n\n## Tasks\n\n" +
		"- [ ] call bank\n" +
		"  - [ ] find account number\n" +
		"  - [x] look up hours\n" +
		"\n" +
		"    some note under it\n" +
		"  - [ ] quiz prep #act-200\n" + // has a category: stays
		"  - [ ] pay by @2026-10-03\n" + // has a date: stays
		"- [x] finished\n" +
		"- [ ] read chapter #act-200/chapter-5\n" + // stays with its subtree
		"  - [ ] stays with its parent\n" +
		"* [ ] buy milk @2026-10-01\n" + // stays: due date
		"- [ ] tidy desk #urgent\n" + // unknown tag: moves
		"- [>] already moved → [[2026-09-29]]\n" +
		"- not a task\n" +
		"  - [ ] nested under a plain bullet\n" +
		"```\n- [ ] in code\n```\n" +
		"\n## Notes\n\nstuff\n"
	s := sampleNotes(t, map[string]string{
		"daily/2026-09-28.md": "- [ ] older note task\n",
		"daily/2026-09-29.md": old, // two days back: the most recent earlier note
		"daily/2026-10-05.md": "- [ ] future note\n",
		"daily/notes.md":      "- [ ] not a daily note\n",
	})
	os.Remove(filepath.Join(s.Root, "daily", "2026-09-30.md"))
	res, err := s.EnsureDaily(testNow, true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.From != "daily/2026-09-29.md" || res.Moved != 3 || res.Path != "daily/2026-10-01.md" {
		t.Errorf("result: %+v", res)
	}
	wantNew := "# Thursday, October 01 2026\n\n## Tasks\n\n" +
		"- [ ] call bank\n" +
		"  - [ ] find account number\n" +
		"  - [x] look up hours\n" +
		"\n" +
		"    some note under it\n" +
		"- [ ] tidy desk #urgent\n" +
		"- [ ] nested under a plain bullet\n" +
		"\n## Notes\n\n"
	if got := readFile(t, s, "daily/2026-10-01.md"); got != wantNew {
		t.Errorf("new note:\n%s\nwant:\n%s", got, wantNew)
	}
	wantOld := "# Tuesday\n\n## Tasks\n\n" +
		"- [>] call bank → [[2026-10-01]]\n" +
		"  - [ ] quiz prep #act-200\n" +
		"  - [ ] pay by @2026-10-03\n" +
		"- [x] finished\n" +
		"- [ ] read chapter #act-200/chapter-5\n" +
		"  - [ ] stays with its parent\n" +
		"* [ ] buy milk @2026-10-01\n" +
		"- [>] tidy desk #urgent → [[2026-10-01]]\n" +
		"- [>] already moved → [[2026-09-29]]\n" +
		"- not a task\n" +
		"  - [>] nested under a plain bullet → [[2026-10-01]]\n" +
		"```\n- [ ] in code\n```\n" +
		"\n## Notes\n\nstuff\n"
	if got := readFile(t, s, "daily/2026-09-29.md"); got != wantOld {
		t.Errorf("old note:\n%s\nwant:\n%s", got, wantOld)
	}
	if got := readFile(t, s, "daily/2026-09-28.md"); got != "- [ ] older note task\n" {
		t.Errorf("older note touched: %q", got)
	}
	// once the note exists nothing more happens
	res, err = s.EnsureDaily(testNow, true)
	if err != nil || res.Created || res.Moved != 0 {
		t.Errorf("second run: %+v %v", res, err)
	}
}

func TestDailyWithoutPreviousNote(t *testing.T) {
	s := newTestStore(t, nil)
	res, err := s.EnsureDaily(testNow, true)
	if err != nil || !res.Created || res.Moved != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if got := readFile(t, s, res.Path); got != "# Thursday, October 01 2026\n\n## Tasks\n\n\n## Notes\n\n" {
		t.Errorf("template: %q", got)
	}
	s2 := newTestStore(t, map[string]string{"daily/2026-09-20.md": "- [ ] x\n"})
	res, _ = s2.EnsureDaily(testNow.AddDate(0, 0, -1), false)
	if res.Moved != 0 || readFile(t, s2, "daily/2026-09-20.md") != "- [ ] x\n" {
		t.Errorf("carry-over without carry flag: %+v", res)
	}
}

// ── capture ──

func TestParseDue(t *testing.T) {
	cases := map[string]string{
		"fri": "2026-10-02 Fri Oct 2", "@fri": "2026-10-02 Fri Oct 2", "Tomorrow": "2026-10-02 Fri Oct 2",
		"oct 6": "2026-10-06 Tue Oct 6", "10/6": "2026-10-06 Tue Oct 6", "+3d": "2026-10-04 Sun Oct 4",
		"2026-11-30": "2026-11-30 Mon Nov 30", "someday": "", "": "", "2026-13-01": "",
	}
	for in, want := range cases {
		d, ok := ParseDue(in, testNow)
		got := ""
		if ok {
			got = d.Format(isoDate) + " " + DueLabel(d)
		}
		if got != want {
			t.Errorf("ParseDue(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCaptureOptions(t *testing.T) {
	s := sampleNotes(t, map[string]string{"inbox.md": "# Inbox\n\nno newline at end"})
	cases := []struct {
		opts CaptureOpts
		want string
	}{
		{CaptureOpts{Text: "read ch 5", Folder: "act-200/chapter-5", Due: "fri"}, "- [ ] read ch 5 #act-200/chapter-5 @2026-10-02 _(Oct 01 12:00)_"},
		{CaptureOpts{Text: "email prof", Folder: "ACT 200", Due: ""}, "- [ ] email prof #act-200 _(Oct 01 12:00)_"},
		{CaptureOpts{Text: "  buy\nmilk  ", Folder: "none", Due: ""}, "- [ ] buy milk _(Oct 01 12:00)_"},
		{CaptureOpts{Text: "pay rent @tomorrow #personal", Folder: "personal"}, "- [ ] pay rent @2026-10-02 #personal _(Oct 01 12:00)_"},
		{CaptureOpts{Text: "move date @today", Due: "+3d"}, "- [ ] move date @2026-10-04 _(Oct 01 12:00)_"},
	}
	for _, c := range cases {
		got, err := s.Capture(c.opts, testNow)
		if err != nil || got != c.want {
			t.Errorf("Capture(%+v) = %q, %v; want %q", c.opts, got, err, c.want)
		}
	}
	if !strings.HasPrefix(readFile(t, s, "inbox.md"), "# Inbox\n\nno newline at end\n- [ ] read ch 5") {
		t.Errorf("inbox:\n%s", readFile(t, s, "inbox.md"))
	}
	for _, bad := range []CaptureOpts{{Text: "  "}, {Text: "x", Folder: "nope"}, {Text: "x", Due: "someday"}} {
		if _, err := s.Capture(bad, testNow); err == nil {
			t.Errorf("Capture(%+v) should fail", bad)
		}
	}
}

func TestInteractiveCapture(t *testing.T) {
	run := func(s *Store, prefill, input string, pick string) (string, string, error) {
		var out strings.Builder
		var offered []string
		ui := &captureUI{in: bufio.NewReader(strings.NewReader(input)), out: &out,
			pick: func(c []string) (string, bool) { offered = c; return pick, pick != "" }}
		line, err := runCaptureInteractive(s, prefill, ui, testNow)
		if offered != nil && offered[0] != "none" {
			t.Errorf("picker must offer none first: %v", offered)
		}
		return line, out.String(), err
	}
	s := sampleNotes(t, nil)

	// a topic and a natural date, with a typo first
	line, out, err := run(s, "", "read ch 6\nsomeday\nfri\n\n", "act-200/chapter-6")
	if err != nil || line != "- [ ] read ch 6 #act-200/chapter-6 @2026-10-02 _(Oct 01 12:00)_" {
		t.Errorf("topic capture: %q %v\n%s", line, err, out)
	}
	if !strings.Contains(out, `Can't read "someday"`) || !strings.Contains(out, "fri → Fri Oct 2") {
		t.Errorf("prompts:\n%s", out)
	}
	// a category, changing the date at the confirmation
	line, out, _ = run(s, "", "email prof\ntomorrow\noct6\n\n", "act-200")
	if line != "- [ ] email prof #act-200 @2026-10-06 _(Oct 01 12:00)_" {
		t.Errorf("category capture: %q\n%s", line, out)
	}
	// skip both (Esc in the picker, empty date); prefilled text
	line, out, _ = run(s, "call mom", "\n", "")
	if line != "- [ ] call mom _(Oct 01 12:00)_" || !strings.Contains(out, "Task: call mom") || !strings.Contains(out, "Folder: none") {
		t.Errorf("skip both: %q\n%s", line, out)
	}
	// text that already has a tag and date skips those steps
	line, out, _ = run(s, "pay rent #personal @mon", "", "SHOULD NOT BE ASKED")
	if line != "- [ ] pay rent #personal @2026-10-05 _(Oct 01 12:00)_" || strings.Contains(out, "Due (") {
		t.Errorf("pre-answered: %q\n%s", line, out)
	}
	// Ctrl+D at the first prompt saves nothing
	if _, _, err := run(s, "", "", ""); err != errCancelled {
		t.Errorf("EOF should cancel, got %v", err)
	}

	// each capture lands in the Tasks view with the right label and group
	var got []string
	for _, tk := range s.CollectTasks(TaskQuery{Now: testNow}) {
		if tk.File == "inbox.md" {
			got = append(got, tk.Group+"|"+tk.Label()+"|"+tk.Display)
		}
	}
	want := []string{
		"tomorrow|ACT 200 · Chapter 6|read ch 6 _(Oct 01 12:00)_",
		"week|personal|pay rent _(Oct 01 12:00)_",
		"week|ACT 200|email prof _(Oct 01 12:00)_",
		"none|General|call mom _(Oct 01 12:00)_",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("tasks view:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// ── CLI and API ──

func TestCLICommands(t *testing.T) {
	s := sampleNotes(t, nil)
	dir := s.Root
	run := func(stdin string, args ...string) (string, int) {
		var out, errb strings.Builder
		code := runCommandIO(args[0], args[1:], strings.NewReader(stdin), &out, &errb)
		return out.String() + errb.String(), code
	}
	today := time.Now()
	if out, code := run("", "date", "--", "- [ ] x @today"); code != 0 || out != "- [ ] x @"+today.Format(isoDate)+"\n" {
		t.Errorf("date: %q %d", out, code)
	}
	if out, code := run("", "due", "--json", "today"); code != 0 || !strings.Contains(out, `"date":"`+today.Format(isoDate)+`"`) {
		t.Errorf("due: %q", out)
	}
	if _, code := run("", "due", "someday"); code == 0 {
		t.Error("unparseable due should fail")
	}
	out, code := run("", "capture", "--dir", dir, "--folder", "act-200/chapter-5", "--due", "tomorrow", "--", "hw 3")
	if code != 0 || !strings.HasPrefix(out, "Added to inbox: - [ ] hw 3 #act-200/chapter-5 @") {
		t.Errorf("capture: %q", out)
	}
	t.Setenv("PATH", "") // no fzf: the numbered list reads the folder from stdin
	if out, code := run("water plants\n\n\n", "capture", "-i", "--dir", dir); code != 0 || !strings.Contains(out, "Added to inbox: - [ ] water plants _(") {
		t.Errorf("capture -i: %q", out)
	}
	if out, _ := run("", "capture", "--parse", "--dir", dir, "--", "x @fri #ACT-200 #urgent"); !strings.Contains(out, `"folder":"act-200"`) || !strings.Contains(out, `"due":"`+today.AddDate(0, 0, 0).Format("2006")) {
		t.Errorf("capture --parse: %q", out)
	}
	out, _ = run("", "tasks", "--json", "--dir", dir)
	var tasks []Task
	if err := json.Unmarshal([]byte(out), &tasks); err != nil || len(tasks) != 5 {
		t.Fatalf("tasks --json: %q %v", out, err)
	}
	out, _ = run("", "folders", "--json", "--dir", dir)
	if !strings.Contains(out, `"tag":"act-200/chapter-5"`) || !strings.Contains(out, `"path":"ACT 200/Chapter 5"`) {
		t.Errorf("folders: %q", out)
	}
	out, _ = run("", "resolve", "--json", "--dir", dir, "act-200/chapter-5")
	if !strings.Contains(out, `"kind":"folder"`) || !strings.Contains(out, `"title":"Costs"`) {
		t.Errorf("resolve: %q", out)
	}
	if out, code := run("", "daily", "--dir", dir); code != 0 || !strings.HasSuffix(strings.TrimSpace(out), "daily/"+today.Format(isoDate)+".md") {
		t.Errorf("daily: %q", out)
	}
}

func TestTasksFolderAndDailyAPI(t *testing.T) {
	s := sampleNotes(t, map[string]string{"inbox.md": "- [ ] hw **bold** #act-200\n- [>] gone → [[x]]\n"})
	h := NewServer(s, 0).Handler()
	get := func(u string) string {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", u, nil)
		req.Host = "127.0.0.1:7777"
		h.ServeHTTP(rec, req)
		return rec.Body.String()
	}
	var body struct {
		Tasks []Task `json:"tasks"`
	}
	json.Unmarshal([]byte(get("/api/tasks")), &body)
	if len(body.Tasks) != 4 || body.Tasks[2].Display != "hw **bold**" || body.Tasks[2].HTML != "hw <strong>bold</strong>" {
		t.Errorf("tasks: %+v", body.Tasks)
	}
	if out := get("/api/folder?path=act-200"); !strings.Contains(out, `"name":"Chapter 5"`) || !strings.Contains(out, `"title":"Syllabus"`) {
		t.Errorf("folder: %s", out)
	}
	if out := get("/api/folder?path=nope"); !strings.Contains(out, "no such folder") {
		t.Errorf("missing folder: %s", out)
	}
	if out := get("/api/folders"); !strings.Contains(out, `"tag":"personal/empty-topic"`) {
		t.Errorf("folders: %s", out)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/daily", strings.NewReader("{}"))
	req.Host = "127.0.0.1:7777"
	req.Header.Set("X-Notesview", "1")
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"created":true`) {
		t.Errorf("daily: %d %s", rec.Code, rec.Body.String())
	}
}
