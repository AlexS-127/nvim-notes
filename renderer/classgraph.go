package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Class graph (Classes page → a class): every heading of every note of a course folder is a node
// (a note's own node is its root), coloured by how well you know it; edges show how the topics are
// connected. Edge kinds:
//   - part:    a heading inside its parent heading / note
//   - link:    a section links to another note ([[wiki]] or [text](file.md))
//   - mention: a section uses another note's title (or a distinctive heading) as a phrase
//   - similar: sections of different notes share vocabulary (TF-IDF cosine), the strongest few
// Knowledge ("mastery", 0-1, or none when nothing was ever asked) combines the answers you gave to
// revision questions (credit, streak, how many you have seen; quiz.py keeps that in
// <folder>/.quiz_stats.json) with your last revision score of the note, which fades the longer the
// note is overdue. Each question belongs to the section whose text it resembles most; a note's own
// node counts all its questions, a section without questions of its own takes the note's revision
// score (`inferred`).

type GraphNode struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"` // note (root) or heading
	Note      string   `json:"note"` // the note's path
	Parent    string   `json:"parent,omitempty"`
	Level     int      `json:"level"`
	Line      int      `json:"line"` // 1-based line of the heading (0 for a note's root)
	Title     string   `json:"title"`
	Words     int      `json:"words"`
	Mastery   *float64 `json:"mastery"`
	Inferred  bool     `json:"inferred,omitempty"` // mastery comes from the note's revision, not this section's questions
	Scheduled bool     `json:"scheduled"`
	Due       string   `json:"due,omitempty"`
	Overdue   int      `json:"overdue,omitempty"` // days past due
	Step      int      `json:"step,omitempty"`
	Revisions int      `json:"revisions,omitempty"`
	Last      string   `json:"last,omitempty"`
	LastScore *float64 `json:"last_score,omitempty"`
	Questions int      `json:"questions"` // in the bank, for this node
	Seen      int      `json:"seen"`      // of them answered at least once
	QMastery  *float64 `json:"q_mastery,omitempty"`
	RMastery  *float64 `json:"r_mastery,omitempty"`
	Degree    int      `json:"degree"`
}

type GraphEdge struct {
	A    string  `json:"a"`
	B    string  `json:"b"`
	W    float64 `json:"w"`
	Kind string  `json:"kind"` // part, link, mention, similar
}

type ClassGraph struct {
	Subject string      `json:"subject"`
	Notes   int         `json:"notes"`
	Nodes   []GraphNode `json:"nodes"`
	Edges   []GraphEdge `json:"edges"`
	Average *float64    `json:"average"` // of the notes that have a mastery
}

const (
	graphSimilarMin     = 0.25 // cosine below this is not an edge
	graphSimilarTop     = 1    // similar edges kept per note
	graphMentionTop     = 2    // mention edges kept per note
	graphLinkW          = 1.0
	graphMentionMinUses = 2 // a section must use a title this often to count as mentioning it
	graphMentionMin     = 4 // shortest title (letters) that counts as a mention
)

var (
	graphWikiRe  = regexp.MustCompile(`\[\[([^\]|#]+)(?:[|#][^\]]*)?\]\]`)
	graphMdRe    = regexp.MustCompile(`\]\(([^)#\s]+\.md)(?:#[^)]*)?\)`)
	graphWordRe  = regexp.MustCompile(`[\p{L}\p{N}]{3,}`)
	graphFoldMap = strings.NewReplacer(
		"ā", "a", "ē", "e", "ī", "i", "ō", "o", "ū", "u", "ȳ", "y", "Ā", "a", "Ē", "e", "Ī", "i", "Ō", "o", "Ū", "u",
		"á", "a", "à", "a", "â", "a", "ä", "a", "é", "e", "è", "e", "ê", "e", "ë", "e", "í", "i", "ì", "i", "î", "i",
		"ï", "i", "ó", "o", "ò", "o", "ô", "o", "ö", "o", "ú", "u", "ù", "u", "û", "u", "ü", "u", "ç", "c", "ñ", "n")
	graphStop = map[string]bool{}
)

func init() {
	for _, w := range strings.Fields(`the and for are but not you all can had her was one our out has his how its may new now old see two way who
		boy did let put say she too use that with have this from they will would there their what about which when your said each
		than them then into more some such only other also any these those been were being does done very just like over under`) {
		graphStop[w] = true
	}
}

// foldText lowercases and drops macrons and accents, so Latin forms match their plain spelling.
func foldText(s string) string { return graphFoldMap.Replace(strings.ToLower(s)) }

// GraphSubjects lists the folders that have at least one note a graph could show.
func (s *Store) GraphSubjects() []string {
	ents, _ := os.ReadDir(s.Root)
	var out []string
	for _, e := range ents {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && len(s.graphNotes(e.Name())) > 0 {
			out = append(out, e.Name())
		}
	}
	return out
}

// graphNotes is the topic-able notes of a folder (relative paths, sorted).
func (s *Store) graphNotes(subject string) []string {
	var out []string
	root := filepath.Join(s.Root, subject)
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && p != root && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		if rel, e := filepath.Rel(s.Root, p); e == nil && s.revisable(filepath.ToSlash(rel)) {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(out)
	return out
}

type graphStat struct {
	Right  int      `json:"right"`
	Wrong  int      `json:"wrong"`
	Streak int      `json:"streak"`
	Credit *float64 `json:"credit"`
	Notes  []string `json:"notes"`
}

// questionKnowledge is how well one question is known: the running average of the credit you got,
// less while the streak of full answers is short (3 in a row counts as settled).
func questionKnowledge(g graphStat) float64 {
	k := 0.0
	if g.Credit != nil {
		k = *g.Credit
	} else if g.Right+g.Wrong > 0 {
		k = float64(g.Right) / float64(g.Right+g.Wrong)
	}
	return k * (0.7 + 0.1*math.Min(float64(g.Streak), 3))
}

func ptr(f float64) *float64 { return &f }

var (
	graphHeadRe  = regexp.MustCompile(`^(#{1,4})[ \t]+(.+?)[ \t#]*$`)
	graphWiki2Re = regexp.MustCompile(`\[\[([^\]|#]+)(?:#([^\]|]*))?(?:\|[^\]]*)?\]\]`)
	graphMd2Re   = regexp.MustCompile(`\]\(([^)#\s]+\.md)(?:#([^)]*))?\)`)
	graphFieldRe = regexp.MustCompile(`^(Q|A|Why|Src|Solution):[ \t]?(.*)$`)
	graphChoice  = regexp.MustCompile(`^[a-hA-H][).][ \t]+(.*)$`)
	graphSlugRe  = regexp.MustCompile(`[^a-z0-9]+`)
)

func graphSlug(s string) string {
	return strings.Trim(graphSlugRe.ReplaceAllString(foldText(s), "-"), "-")
}

type gHead struct {
	level int
	title string
	line  int
	body  []string
}

// graphSections splits a note into the text before its first heading and its headings (levels 1-4,
// outside code fences) with the lines under each up to the next heading.
func graphSections(src string) (intro []string, heads []gHead) {
	fence := false
	for n, l := range strings.Split(src, "\n") {
		l = strings.TrimRight(l, "\r")
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			fence = !fence
		}
		if m := graphHeadRe.FindStringSubmatch(l); m != nil && !fence {
			if t := PlainText(m[2]); t != "" {
				heads = append(heads, gHead{level: len(m[1]), title: t, line: n + 1})
				continue
			}
		}
		if len(heads) == 0 {
			intro = append(intro, l)
		} else {
			heads[len(heads)-1].body = append(heads[len(heads)-1].body, l)
		}
	}
	return
}

type graphQ struct{ key, text string }

// graphBank reads a question bank the way quiz.py's load_questions does: the key is the question
// text quiz.py files its stats under, the text adds the choices, answer and explanation.
func graphBank(src string) []graphQ {
	var out []graphQ
	var q, rest struct{ s []string }
	field, open := "", false
	flush := func() {
		if open {
			out = append(out, graphQ{strings.TrimSpace(strings.Join(q.s, "\n")), strings.Join(q.s, "\n") + "\n" + strings.Join(rest.s, "\n")})
		}
		q.s, rest.s, field, open = nil, nil, "", false
	}
	for _, l := range strings.Split(src, "\n") {
		l = strings.TrimRight(l, " \t\r")
		if strings.HasPrefix(l, "#") {
			flush()
			continue
		}
		if m := graphFieldRe.FindStringSubmatch(l); m != nil {
			if m[1] == "Q" {
				flush()
				open = true
			}
			field = strings.ToLower(m[1])
			if open {
				if field == "q" {
					q.s = append(q.s, m[2])
				} else {
					rest.s = append(rest.s, m[2])
				}
			}
			continue
		}
		if !open || strings.TrimSpace(l) == "" {
			continue
		}
		if c := graphChoice.FindStringSubmatch(strings.TrimSpace(l)); c != nil && (field == "q" || field == "choices") {
			rest.s = append(rest.s, c[1])
			field = "choices"
		} else if field == "q" {
			q.s = append(q.s, l)
		} else if field != "choices" {
			rest.s = append(rest.s, l)
		}
	}
	flush()
	return out
}

func blendMastery(q, r *float64) *float64 {
	switch {
	case q != nil && r != nil:
		return ptr((*q + *r) / 2)
	case q != nil:
		return q
	}
	return r
}

// ClassGraph builds the graph of one folder.
func (s *Store) ClassGraph(subject string, now time.Time) (ClassGraph, error) {
	ids := s.graphNotes(subject)
	if len(ids) == 0 {
		return ClassGraph{}, fmt.Errorf("%w: no notes in %q", ErrRevision, subject)
	}
	today := now.Format(isoDate)
	cfg := s.RevisionSettings(now)
	topics := map[string]Topic{}
	for _, t := range s.Topics() {
		topics[t.ID] = t
	}
	stats := map[string]graphStat{}
	if b, err := os.ReadFile(filepath.Join(s.Root, subject, ".quiz_stats.json")); err == nil {
		var raw map[string]json.RawMessage
		if json.Unmarshal(b, &raw) == nil {
			for k, v := range raw {
				var g graphStat
				if json.Unmarshal(v, &g) == nil && len(g.Notes) > 0 {
					stats[k] = g
				}
			}
		}
	}

	g := ClassGraph{Subject: subject, Notes: len(ids), Edges: []GraphEdge{}}
	var texts, folded []string // per node: own raw text, folded text
	var noteOf []int           // per node: index into ids
	root := make([]int, len(ids))
	byID := map[string]int{}
	slugs := map[string]int{} // note + "#" + slug -> node
	addNode := func(n GraphNode, text string, note int) int {
		i := len(g.Nodes)
		g.Nodes = append(g.Nodes, n)
		texts = append(texts, text)
		folded = append(folded, foldText(n.Title+"\n"+text))
		noteOf = append(noteOf, note)
		byID[n.ID] = i
		return i
	}
	rev := make([]*float64, len(ids)) // per note: faded last revision score
	for ni, id := range ids {
		b, _ := os.ReadFile(filepath.Join(s.Root, id))
		title := Title(id, b)
		intro, heads := graphSections(string(b))
		rootText := strings.Join(intro, "\n")
		if len(heads) > 0 && heads[0].level == 1 && heads[0].title == title { // the note's own title heading is its root
			rootText += "\n" + strings.Join(heads[0].body, "\n")
			heads = heads[1:]
		}
		rn := GraphNode{ID: id, Kind: "note", Note: id, Level: 0, Title: title, Words: countWords(rootText)}
		if t, ok := topics[id]; ok {
			rn.Scheduled, rn.Due, rn.Step, rn.Revisions, rn.Last = true, t.Due, t.Step, t.Count, t.Last
			if t.Count > 0 {
				rn.LastScore = ptr(t.Score)
				if t.Due < today {
					a, _ := time.Parse(isoDate, t.Due)
					rn.Overdue = int(now.Sub(a).Hours() / 24)
				}
				// forgetting: a revision score fades about 8% per overdue day (and slower on later steps)
				rev[ni] = ptr(t.Score / (1 + 0.08*float64(rn.Overdue)/(1+0.15*float64(min(t.Step, len(cfg.Intervals))))))
				rn.RMastery = rev[ni]
			}
		}
		root[ni] = addNode(rn, rootText, ni)
		slugs[id+"#"] = root[ni]
		type frame struct {
			level int
			id    string
		}
		stack := []frame{{0, id}}
		for _, h := range heads {
			for len(stack) > 1 && stack[len(stack)-1].level >= h.level {
				stack = stack[:len(stack)-1]
			}
			slug, nid := graphSlug(h.title), ""
			for k := 1; ; k++ {
				nid = id + "#" + slug
				if k > 1 {
					nid += "-" + fmt.Sprint(k)
				}
				if _, dup := byID[nid]; !dup {
					break
				}
			}
			n := GraphNode{ID: nid, Kind: "heading", Note: id, Parent: stack[len(stack)-1].id, Level: h.level, Line: h.line, Title: h.title, Words: countWords(strings.Join(h.body, "\n"))}
			n.Scheduled, n.Due = rn.Scheduled, rn.Due
			i := addNode(n, strings.Join(h.body, "\n"), ni)
			if _, ok := slugs[id+"#"+slug]; !ok {
				slugs[id+"#"+slug] = i
			}
			stack = append(stack, frame{h.level, nid})
		}
	}
	N := len(g.Nodes)

	// term vectors: TF-IDF over all sections
	df := map[string]int{}
	tfs := make([]map[string]float64, N)
	toks := make([]int, N)
	for i := range tfs {
		tf := map[string]float64{}
		for _, w := range graphWordRe.FindAllString(folded[i], -1) {
			if !graphStop[w] {
				tf[w]++
				toks[i]++
			}
		}
		for w := range tf {
			df[w]++
		}
		tfs[i] = tf
	}
	vectorize := func(tf map[string]float64) (map[string]float64, float64) {
		v, norm := map[string]float64{}, 0.0
		for w, c := range tf {
			d := df[w]
			if d == 0 {
				d = 1
			}
			x := (1 + math.Log(c)) * math.Log(1+float64(N)/float64(d))
			v[w] = x
			norm += x * x
		}
		return v, math.Sqrt(norm)
	}
	vecs := make([]map[string]float64, N)
	norms := make([]float64, N)
	for i := range tfs {
		vecs[i], norms[i] = vectorize(tfs[i])
	}
	cosine := func(a map[string]float64, na float64, b map[string]float64, nb float64) float64 {
		if na == 0 || nb == 0 {
			return 0
		}
		if len(b) < len(a) {
			a, b = b, a
		}
		var dot float64
		for w, v := range a {
			dot += v * b[w]
		}
		return dot / (na * nb)
	}

	// questions → the section each resembles most; knowledge per node
	type acc struct {
		assigned, seen int
		sum            float64
	}
	accs := make([]acc, N)
	for ni, id := range ids {
		matched := map[string]bool{}
		if bank, err := os.ReadFile(s.QuestionsPath(id)); err == nil {
			var members []int
			for i := range g.Nodes {
				if noteOf[i] == ni {
					members = append(members, i)
				}
			}
			for _, q := range graphBank(string(bank)) {
				tf := map[string]float64{}
				for _, w := range graphWordRe.FindAllString(foldText(q.text), -1) {
					if !graphStop[w] {
						tf[w]++
					}
				}
				qv, qn := vectorize(tf)
				best, bestC := root[ni], 0.04
				for _, i := range members {
					if c := cosine(qv, qn, vecs[i], norms[i]); c > bestC {
						best, bestC = i, c
					}
				}
				matched[q.key] = true
				st, seen := stats[q.key]
				seen = seen && contains(st.Notes, id)
				owners := []int{best}
				if best != root[ni] {
					owners = append(owners, root[ni]) // the note's own node counts every question
				}
				for _, i := range owners {
					accs[i].assigned++
					if seen {
						accs[i].seen++
						accs[i].sum += questionKnowledge(st)
					}
				}
			}
		}
		for k, st := range stats { // answered questions not in this note's own bank (cross-topic ones): the note as a whole
			if !matched[k] && contains(st.Notes, id) {
				accs[root[ni]].seen++
				accs[root[ni]].sum += questionKnowledge(st)
			}
		}
	}
	for i := range g.Nodes {
		n, a := &g.Nodes[i], accs[i]
		n.Questions, n.Seen = a.assigned, a.seen
		if a.seen > 0 {
			cov := 1.0
			if a.assigned > 0 {
				cov = math.Min(1, float64(a.seen)/float64(a.assigned))
			}
			n.QMastery = ptr(a.sum / float64(a.seen) * (0.6 + 0.4*cov))
		}
		r := rev[noteOf[i]]
		if n.Kind == "heading" {
			n.RMastery = r
			n.Inferred = n.QMastery == nil && r != nil
		}
		n.Mastery = blendMastery(n.QMastery, r)
	}

	// edges
	edge := map[[2]int]GraphEdge{}
	rank := map[string]int{"similar": 0, "mention": 1, "link": 2, "part": 3}
	add := func(i, j int, w float64, kind string) {
		if i == j {
			return
		}
		if i > j {
			i, j = j, i
		}
		old, ok := edge[[2]int{i, j}]
		if ok && (rank[old.Kind] > rank[kind] || (rank[old.Kind] == rank[kind] && old.W >= w)) {
			return
		}
		edge[[2]int{i, j}] = GraphEdge{A: g.Nodes[i].ID, B: g.Nodes[j].ID, W: w, Kind: kind}
	}
	for i, n := range g.Nodes {
		if n.Parent != "" {
			add(i, byID[n.Parent], 1, "part")
		}
	}
	// links
	noteIdx := map[string]int{} // folded stem / title -> note
	for ni, id := range ids {
		stem := strings.TrimSuffix(filepath.Base(id), ".md")
		noteIdx[foldText(stem)] = ni
		noteIdx[foldText(strings.TrimSuffix(id, ".md"))] = ni // [[act200/revenue]]: the path form Check notes uses for a name that is not unique
		noteIdx[foldText(strings.ReplaceAll(stem, "-", " "))] = ni
		noteIdx[foldText(g.Nodes[root[ni]].Title)] = ni
	}
	target := func(note, frag string) (int, bool) {
		ni, ok := noteIdx[foldText(strings.TrimSpace(strings.TrimSuffix(note, ".md")))]
		if !ok {
			return 0, false
		}
		if frag != "" {
			if j, ok := slugs[ids[ni]+"#"+graphSlug(frag)]; ok {
				return j, true
			}
		}
		return root[ni], true
	}
	for i := range g.Nodes {
		for _, m := range graphWiki2Re.FindAllStringSubmatch(texts[i], -1) {
			if j, ok := target(m[1], m[2]); ok && noteOf[j] != noteOf[i] {
				add(i, j, graphLinkW, "link")
			}
		}
		for _, m := range graphMd2Re.FindAllStringSubmatch(texts[i], -1) {
			if j, ok := target(strings.TrimSuffix(filepath.Base(m[1]), ".md"), m[2]); ok && noteOf[j] != noteOf[i] {
				add(i, j, graphLinkW, "link")
			}
		}
	}
	// mentions: another note's title, or a heading title that is distinctive (5+ letters, used once)
	titleCount := map[string]int{}
	for _, n := range g.Nodes {
		titleCount[foldText(n.Title)]++
	}
	type mtarget struct {
		j  int
		re []*regexp.Regexp
	}
	var targets []mtarget
	for j, n := range g.Nodes {
		names := []string{n.Title}
		if n.Kind == "note" {
			names = append(names, strings.ReplaceAll(strings.TrimSuffix(filepath.Base(n.ID), ".md"), "-", " "))
		} else if _, isNote := noteIdx[foldText(n.Title)]; isNote || titleCount[foldText(n.Title)] != 1 || len([]rune(n.Title)) < 5 {
			continue
		}
		var res []*regexp.Regexp
		for _, name := range names {
			name = foldText(strings.TrimSpace(name))
			if len([]rune(name)) < graphMentionMin {
				continue
			}
			if re, err := regexp.Compile(`(?:^|[^\p{L}\p{N}])` + regexp.QuoteMeta(name) + `s?(?:[^\p{L}\p{N}]|$)`); err == nil {
				res = append(res, re)
			}
		}
		if len(res) > 0 {
			targets = append(targets, mtarget{j, res})
		}
	}
	type mention struct{ j, n int }
	for i := range g.Nodes {
		var ms []mention
		for _, t := range targets {
			if noteOf[t.j] == noteOf[i] {
				continue
			}
			n := 0
			for _, re := range t.re {
				if c := len(re.FindAllStringIndex(foldText(texts[i]), -1)); c > n {
					n = c
				}
			}
			if n >= graphMentionMinUses {
				ms = append(ms, mention{t.j, n})
			}
		}
		sort.SliceStable(ms, func(a, b int) bool { return ms[a].n > ms[b].n })
		for k, m := range ms {
			if k >= graphMentionTop {
				break
			}
			add(i, m.j, 0.3+0.1*float64(min(m.n, 5)), "mention")
		}
	}
	// similarity between sections of different notes
	type cand struct {
		j int
		c float64
	}
	for i := range g.Nodes {
		if toks[i] < 6 {
			continue
		}
		var cs []cand
		for j := range g.Nodes {
			if noteOf[j] == noteOf[i] || toks[j] < 6 {
				continue
			}
			if c := cosine(vecs[i], norms[i], vecs[j], norms[j]); c >= graphSimilarMin {
				cs = append(cs, cand{j, c})
			}
		}
		sort.Slice(cs, func(x, y int) bool { return cs[x].c > cs[y].c })
		for k := 0; k < len(cs) && k < graphSimilarTop; k++ {
			add(i, cs[k].j, cs[k].c, "similar")
		}
	}

	for _, e := range edge {
		g.Edges = append(g.Edges, e)
		g.Nodes[byID[e.A]].Degree++
		g.Nodes[byID[e.B]].Degree++
	}
	sort.Slice(g.Edges, func(i, j int) bool {
		if g.Edges[i].A != g.Edges[j].A {
			return g.Edges[i].A < g.Edges[j].A
		}
		return g.Edges[i].B < g.Edges[j].B
	})
	var sum float64
	var n int
	for _, nd := range g.Nodes {
		if nd.Kind == "note" && nd.Mastery != nil {
			sum += *nd.Mastery
			n++
		}
	}
	if n > 0 {
		g.Average = ptr(sum / float64(n))
	}
	return g, nil
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// GET /api/classes/graph?subject=NAME: the graph of one folder; without a subject, the folders that have notes.
func (s *Server) classGraphRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/classes/graph", func(w http.ResponseWriter, r *http.Request) {
		sub := r.URL.Query().Get("subject")
		if sub == "" {
			writeJSON(w, map[string]any{"subjects": s.store.GraphSubjects()})
			return
		}
		if strings.ContainsAny(sub, "/\\") || strings.HasPrefix(sub, ".") {
			http.Error(w, "bad folder", 400)
			return
		}
		now := time.Now()
		s.store.RecordRevision(now)
		g, err := s.store.ClassGraph(sub, now)
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		writeJSON(w, g)
	})
}
