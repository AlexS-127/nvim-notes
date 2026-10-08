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

// Class graph (Classes page → a class): every note of a course folder is a node, coloured by how
// well you know it; edges show how connected the topics are. Edges come from three sources:
//   - link:    one note links to another ([[wiki]] or [text](file.md))
//   - mention: one note's text uses another note's title as a phrase
//   - similar: the notes share vocabulary (TF-IDF cosine), the strongest few per note
// Knowledge ("mastery", 0-1, or none when nothing was ever asked) combines the answers you gave to the
// note's revision questions (credit, streak, how many of its questions you have seen, quiz.py keeps
// that in <folder>/.quiz_stats.json) with your last revision score, which fades the longer the topic
// is overdue.

type GraphNode struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Words     int      `json:"words"`
	Mastery   *float64 `json:"mastery"`
	Scheduled bool     `json:"scheduled"`
	Due       string   `json:"due,omitempty"`
	Overdue   int      `json:"overdue,omitempty"` // days past due
	Step      int      `json:"step,omitempty"`
	Revisions int      `json:"revisions,omitempty"`
	Last      string   `json:"last,omitempty"`
	LastScore *float64 `json:"last_score,omitempty"`
	Questions int      `json:"questions"` // in the note's bank
	Seen      int      `json:"seen"`      // of them answered at least once
	QMastery  *float64 `json:"q_mastery,omitempty"`
	RMastery  *float64 `json:"r_mastery,omitempty"`
	Degree    int      `json:"degree"`
}

type GraphEdge struct {
	A    string  `json:"a"`
	B    string  `json:"b"`
	W    float64 `json:"w"`
	Kind string  `json:"kind"` // link, mention, similar
}

type ClassGraph struct {
	Subject string      `json:"subject"`
	Nodes   []GraphNode `json:"nodes"`
	Edges   []GraphEdge `json:"edges"`
	Average *float64    `json:"average"` // of the nodes that have a mastery
}

const (
	graphSimilarMin = 0.14 // cosine below this is not an edge
	graphSimilarTop = 3    // similar edges kept per note
	graphMentionTop = 5    // mention edges kept per note
	graphLinkW      = 1.0
	graphMentionMin = 4 // shortest title (letters) that counts as a mention
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

	g := ClassGraph{Subject: subject, Edges: []GraphEdge{}}
	texts := make([]string, len(ids))
	folded := make([]string, len(ids))
	for i, id := range ids {
		b, _ := os.ReadFile(filepath.Join(s.Root, id))
		texts[i] = string(b)
		folded[i] = foldText(texts[i])
		n := GraphNode{ID: id, Title: Title(id, b), Words: countWords(texts[i])}
		if bank, err := os.ReadFile(s.QuestionsPath(id)); err == nil {
			n.Questions = countQuestionBlocks(string(bank))
		}
		var sum float64
		for _, st := range stats {
			for _, nid := range st.Notes {
				if nid == id {
					n.Seen++
					sum += questionKnowledge(st)
				}
			}
		}
		if n.Seen > 0 {
			cov := 1.0
			if n.Questions > 0 {
				cov = math.Min(1, float64(n.Seen)/float64(n.Questions))
			}
			n.QMastery = ptr(sum / float64(n.Seen) * (0.6 + 0.4*cov))
		}
		if t, ok := topics[id]; ok {
			n.Scheduled, n.Due, n.Step, n.Revisions, n.Last = true, t.Due, t.Step, t.Count, t.Last
			if t.Count > 0 {
				n.LastScore = ptr(t.Score)
				over := 0
				if t.Due < today {
					a, _ := time.Parse(isoDate, t.Due)
					over = int(now.Sub(a).Hours() / 24)
				}
				n.Overdue = over
				// forgetting: a revision score fades about 8% per overdue day (and slower on later steps)
				n.RMastery = ptr(t.Score / (1 + 0.08*float64(over)/(1+0.15*float64(min(t.Step, len(cfg.Intervals))))))
			}
		}
		switch {
		case n.QMastery != nil && n.RMastery != nil:
			n.Mastery = ptr((*n.QMastery + *n.RMastery) / 2)
		case n.QMastery != nil:
			n.Mastery = n.QMastery
		case n.RMastery != nil:
			n.Mastery = n.RMastery
		}
		g.Nodes = append(g.Nodes, n)
	}

	edge := map[[2]int]GraphEdge{}
	add := func(i, j int, w float64, kind string) {
		if i == j {
			return
		}
		if i > j {
			i, j = j, i
		}
		rank := map[string]int{"similar": 0, "mention": 1, "link": 2}
		old, ok := edge[[2]int{i, j}]
		if ok && rank[old.Kind] > rank[kind] {
			return
		}
		if ok && rank[old.Kind] == rank[kind] && old.W >= w {
			return
		}
		edge[[2]int{i, j}] = GraphEdge{A: ids[i], B: ids[j], W: w, Kind: kind}
	}

	// links
	index := map[string]int{} // folded stem / title -> note
	for i, id := range ids {
		stem := strings.TrimSuffix(filepath.Base(id), ".md")
		index[foldText(stem)] = i
		index[foldText(strings.ReplaceAll(stem, "-", " "))] = i
		index[foldText(g.Nodes[i].Title)] = i
	}
	for i := range ids {
		for _, m := range graphWikiRe.FindAllStringSubmatch(texts[i], -1) {
			if j, ok := index[foldText(strings.TrimSpace(strings.TrimSuffix(m[1], ".md")))]; ok {
				add(i, j, graphLinkW, "link")
			}
		}
		for _, m := range graphMdRe.FindAllStringSubmatch(texts[i], -1) {
			stem := strings.TrimSuffix(filepath.Base(m[1]), ".md")
			if j, ok := index[foldText(stem)]; ok {
				add(i, j, graphLinkW, "link")
			}
		}
	}
	// mentions: another note's title used as a phrase; each note keeps its graphMentionTop most
	// repeated ones, and the more often a title is used the stronger the edge
	type mention struct{ j, n int }
	mentions := make([][]mention, len(ids))
	for j := range ids {
		var res []*regexp.Regexp
		for _, name := range []string{g.Nodes[j].Title, strings.ReplaceAll(strings.TrimSuffix(filepath.Base(ids[j]), ".md"), "-", " ")} {
			name = foldText(strings.TrimSpace(name))
			if len([]rune(name)) < graphMentionMin {
				continue
			}
			if re, err := regexp.Compile(`(?:^|[^\p{L}\p{N}])` + regexp.QuoteMeta(name) + `s?(?:[^\p{L}\p{N}]|$)`); err == nil {
				res = append(res, re)
			}
		}
		for i := range ids {
			if i == j {
				continue
			}
			n := 0
			for _, re := range res {
				if c := len(re.FindAllStringIndex(folded[i], -1)); c > n {
					n = c
				}
			}
			if n > 0 {
				mentions[i] = append(mentions[i], mention{j, n})
			}
		}
	}
	for i, ms := range mentions {
		sort.SliceStable(ms, func(a, b int) bool { return ms[a].n > ms[b].n })
		for k, m := range ms {
			if k >= graphMentionTop {
				break
			}
			add(i, m.j, 0.3+0.1*float64(min(m.n, 5)), "mention")
		}
	}
	// similarity: TF-IDF cosine
	docs := make([]map[string]float64, len(ids))
	df := map[string]int{}
	for i := range ids {
		tf := map[string]float64{}
		for _, w := range graphWordRe.FindAllString(folded[i], -1) {
			if !graphStop[w] {
				tf[w]++
			}
		}
		for w := range tf {
			df[w]++
		}
		docs[i] = tf
	}
	norms := make([]float64, len(ids))
	for i, tf := range docs {
		for w, c := range tf {
			v := (1 + math.Log(c)) * math.Log(1+float64(len(ids))/float64(df[w]))
			tf[w] = v
			norms[i] += v * v
		}
		norms[i] = math.Sqrt(norms[i])
	}
	type cand struct {
		j int
		c float64
	}
	for i := range ids {
		var cs []cand
		for j := range ids {
			if i == j || norms[i] == 0 || norms[j] == 0 {
				continue
			}
			var dot float64
			a, b := docs[i], docs[j]
			if len(b) < len(a) {
				a, b = b, a
			}
			for w, v := range a {
				dot += v * b[w]
			}
			if c := dot / (norms[i] * norms[j]); c >= graphSimilarMin {
				cs = append(cs, cand{j, c})
			}
		}
		sort.Slice(cs, func(x, y int) bool { return cs[x].c > cs[y].c })
		for k := 0; k < len(cs) && k < graphSimilarTop; k++ {
			add(i, cs[k].j, cs[k].c, "similar")
		}
	}

	pos := map[string]int{}
	for i, id := range ids {
		pos[id] = i
	}
	for _, e := range edge {
		g.Edges = append(g.Edges, e)
		g.Nodes[pos[e.A]].Degree++
		g.Nodes[pos[e.B]].Degree++
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
		if nd.Mastery != nil {
			sum += *nd.Mastery
			n++
		}
	}
	if n > 0 {
		g.Average = ptr(sum / float64(n))
	}
	return g, nil
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
