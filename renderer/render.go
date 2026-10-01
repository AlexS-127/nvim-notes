package main

import (
	"bytes"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// ── [[wiki links]] ───────────────────────────────────────────────

var kindWiki = ast.NewNodeKind("WikiLink")

type wikiNode struct {
	ast.BaseInline
	Target string
	Alias  string
}

func (n *wikiNode) Kind() ast.NodeKind { return kindWiki }
func (n *wikiNode) Dump(src []byte, level int) {
	ast.DumpHelper(n, src, level, map[string]string{"target": n.Target}, nil)
}

type wikiParser struct{}

func (wikiParser) Trigger() []byte { return []byte{'['} }
func (wikiParser) Parse(_ ast.Node, block text.Reader, _ parser.Context) ast.Node {
	line, _ := block.PeekLine()
	if len(line) < 5 || line[1] != '[' {
		return nil
	}
	end := bytes.Index(line[2:], []byte("]]"))
	if end <= 0 {
		return nil
	}
	inner := string(line[2 : 2+end])
	if strings.ContainsAny(inner, "[]\n") {
		return nil
	}
	block.Advance(end + 4)
	n := &wikiNode{Target: inner}
	if i := strings.IndexByte(inner, '|'); i >= 0 {
		n.Target, n.Alias = inner[:i], inner[i+1:]
	}
	n.Target = strings.TrimSpace(n.Target)
	return n
}

// ── Markdown pipeline ────────────────────────────────────────────

type Markdown struct {
	store   *Store
	files   []string
	folders *FolderIndex
	md      goldmark.Markdown
	inline  goldmark.Markdown
	note    string // current note, slash path relative to the notes folder
}

func escPath(p string) string { return (&url.URL{Path: p}).EscapedPath() }

func notesHref(rel string) string { return "#/note/" + escPath(rel) }

func fileHref(rel string) string { return "/files/" + escPath(rel) }

func folderHref(tag string) string { return "#/folder/" + escPath(tag) }

func NewMarkdown(store *Store, note string) *Markdown {
	m := &Markdown{store: store, files: store.Files(), folders: store.Folders(), note: note}
	m.md = goldmark.New(
		goldmark.WithExtensions(
			extension.GFM, // tables, strikethrough, task lists, autolinks
			extension.Footnote,
			highlighting.NewHighlighting(
				highlighting.WithFormatOptions(chromahtml.WithClasses(true), chromahtml.PreventSurroundingPre(true)),
				highlighting.WithWrapperRenderer(codeWrapper),
			),
		),
		goldmark.WithParserOptions(
			parser.WithInlineParsers(util.Prioritized(wikiParser{}, 199)),
			parser.WithASTTransformers(util.Prioritized(m, 100)),
		),
		goldmark.WithRendererOptions(
			renderer.WithNodeRenderers(util.Prioritized(m, 100)),
			html.WithHardWraps(),
		),
	)
	m.inline = newInlineMarkdown(
		goldmark.WithParserOptions(parser.WithASTTransformers(util.Prioritized(m, 100))),
		goldmark.WithRendererOptions(renderer.WithNodeRenderers(util.Prioritized(m, 100))),
	)
	return m
}

// newInlineMarkdown builds a goldmark instance that treats its whole input as
// one paragraph, so a single line such as a task's text keeps its inline
// markup (emphasis, code, links, [[wiki links]]) while block syntax like a
// leading "# " or "> " stays literal text.
func newInlineMarkdown(opts ...goldmark.Option) goldmark.Markdown {
	p := parser.NewParser(
		parser.WithBlockParsers(util.Prioritized(parser.NewParagraphParser(), 1000)),
		parser.WithInlineParsers(parser.DefaultInlineParsers()...),
		parser.WithInlineParsers(util.Prioritized(wikiParser{}, 199)),
	)
	opts = append([]goldmark.Option{
		goldmark.WithParser(p),
		goldmark.WithExtensions(extension.Strikethrough, extension.Linkify),
	}, opts...)
	return goldmark.New(opts...)
}

func codeWrapper(w util.BufWriter, ctx highlighting.CodeBlockContext, entering bool) {
	if !entering {
		_, _ = w.WriteString("</code></pre>\n")
		return
	}
	_, _ = w.WriteString(`<pre class="chroma"`)
	if ctx.Attributes() != nil {
		if v, ok := ctx.Attributes().GetString("data-line"); ok {
			fmt.Fprintf(w, ` data-line="%s"`, v)
		}
	}
	if lang, ok := ctx.Language(); ok && len(lang) > 0 {
		fmt.Fprintf(w, ` data-lang="%s"`, util.EscapeHTML(lang))
	}
	_, _ = w.WriteString("><code>")
}

// RegisterFuncs renders wiki link nodes.
func (m *Markdown) RegisterFuncs(r renderer.NodeRendererFuncRegisterer) {
	r.Register(kindWiki, func(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		wn := n.(*wikiNode)
		label := wn.Alias
		if label == "" {
			label = wn.Target
		}
		if rel, ok := resolveWiki(m.files, wn.Target); ok {
			fmt.Fprintf(w, `<a class="wikilink" href="%s">%s</a>`, notesHref(rel), util.EscapeHTML([]byte(label)))
		} else if f, ok := m.folders.Resolve(wn.Target); ok { // [[act-200]] → the folder's page
			fmt.Fprintf(w, `<a class="wikilink folder" title="Folder %s" href="%s">%s</a>`,
				util.EscapeHTML([]byte(f.Path)), folderHref(f.Tag), util.EscapeHTML([]byte(label)))
		} else {
			t := strings.TrimSuffix(strings.SplitN(wn.Target, "#", 2)[0], ".md")
			fmt.Fprintf(w, `<a class="wikilink missing" title="Note does not exist yet" href="%s">%s</a>`,
				notesHref(t+".md"), util.EscapeHTML([]byte(label)))
		}
		return ast.WalkSkipChildren, nil
	})
}

var schemeRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.\-]*:`)

// Transform adds data-line to block elements and rewrites relative links
// and image sources.
func (m *Markdown) Transform(doc *ast.Document, reader text.Reader, _ parser.Context) {
	src := reader.Source()
	starts := lineStarts(src)
	lineOf := func(off int) int {
		lo, hi := 0, len(starts)-1
		for lo < hi {
			mid := (lo + hi + 1) / 2
			if starts[mid] <= off {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		return lo + 1
	}
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch v := n.(type) {
		case *ast.Heading, *ast.Paragraph, *ast.List, *ast.ListItem, *ast.Blockquote,
			*ast.FencedCodeBlock, *ast.CodeBlock, *east.Table:
			if off, ok := firstOffset(n); ok {
				if f, isFence := n.(*ast.FencedCodeBlock); isFence {
					// point at the opening fence line, not the first content line
					if f.Info != nil {
						off = f.Info.Segment.Start
					} else if l := lineOf(off); l > 1 {
						off = starts[l-2]
					}
				}
				n.SetAttributeString("data-line", []byte(strconv.Itoa(lineOf(off))))
			}
		case *ast.Image:
			if rel, ok := m.relTarget(string(v.Destination)); ok {
				v.Destination = []byte(fileHref(rel))
			}
		case *ast.Link:
			dest := string(v.Destination)
			if rel, ok := m.relTarget(dest); ok {
				if strings.HasSuffix(strings.ToLower(rel), ".md") {
					v.Destination = []byte(notesHref(rel))
				} else {
					v.Destination = []byte(fileHref(rel))
				}
			}
		}
		return ast.WalkContinue, nil
	})
}

func lineStarts(src []byte) []int {
	starts := []int{0}
	for i, b := range src {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

func firstOffset(n ast.Node) (int, bool) {
	if n.Type() == ast.TypeBlock && n.Lines().Len() > 0 {
		return n.Lines().At(0).Start, true
	}
	if t, ok := n.(*ast.Text); ok {
		return t.Segment.Start, true
	}
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if off, ok := firstOffset(c); ok {
			return off, true
		}
	}
	return 0, false
}

// relTarget resolves a relative link destination against the current note,
// returning a path relative to the notes folder.
func (m *Markdown) relTarget(dest string) (string, bool) {
	if dest == "" || strings.HasPrefix(dest, "#") || strings.HasPrefix(dest, "//") || schemeRe.MatchString(dest) {
		return "", false
	}
	if i := strings.IndexAny(dest, "?#"); i >= 0 {
		dest = dest[:i]
	}
	if u, err := url.PathUnescape(dest); err == nil {
		dest = u
	}
	var rel string
	if strings.HasPrefix(dest, "/") {
		rel = path.Clean(dest)[1:]
	} else {
		rel = path.Join(path.Dir(m.note), dest)
	}
	if rel == "" || rel == "." || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return rel, true
}

// movedRe finds list items written as "- [>] …" (tasks carried over to a
// later daily note), with or without a wrapping paragraph.
var movedRe = regexp.MustCompile(`(<li[^>]*?)>(\s*<p[^>]*>)?\[&gt;\][^\S\n]*`)

var paraRe = regexp.MustCompile(`(?s)^\s*<p[^>]*>(.*?)</p>\s*$`)

// RenderInline renders one line of markdown (a task's text) for the note
// rel, without the surrounding paragraph. The whole line is parsed as one
// paragraph, so a leading "#" or ">" stays literal text.
func (m *Markdown) RenderInline(rel, text string) string {
	m.note = rel
	var buf bytes.Buffer
	if err := m.inline.Convert([]byte(strings.TrimSpace(text)), &buf); err != nil {
		return string(util.EscapeHTML([]byte(text)))
	}
	return paraRe.ReplaceAllString(buf.String(), "$1")
}

var calloutRe = regexp.MustCompile(`(<blockquote)([^>]*)>\s*<p([^>]*)>\[!([A-Za-z]+)\][^\S\n]*(?:<br>\s*|\n)?(?:</p>\s*)?`)

// Render converts markdown to HTML and extracts a title.
func (m *Markdown) Render(src []byte) (string, error) {
	var buf bytes.Buffer
	if err := m.md.Convert(src, &buf); err != nil {
		return "", err
	}
	out := calloutRe.ReplaceAllStringFunc(buf.String(), func(s string) string {
		g := calloutRe.FindStringSubmatch(s)
		kind := strings.ToLower(g[4])
		body := ""
		if !strings.HasSuffix(strings.TrimSpace(s), "</p>") {
			body = "<p" + g[3] + ">"
		}
		return fmt.Sprintf(`<blockquote class="callout callout-%s"%s><div class="callout-title">%s</div>%s`,
			kind, g[2], strings.ToUpper(kind[:1])+kind[1:], body)
	})
	out = movedRe.ReplaceAllString(out, `$1 class="task-moved">$2<span class="moved-box" title="Moved to a later note">›</span> `)
	return out, nil
}

// plainMD parses inline markdown for PlainText; it has no renderer hooks.
var plainMD = newInlineMarkdown()

// PlainText strips inline markdown from one line, for places that show text
// rather than HTML (titles, search snippets): "**Big** [[Plan|plan]]" becomes
// "Big plan".
func PlainText(src string) string {
	b := []byte(strings.TrimSpace(src))
	doc := plainMD.Parser().Parse(text.NewReader(b))
	var out strings.Builder
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch v := n.(type) {
		case *wikiNode:
			if v.Alias != "" {
				out.WriteString(v.Alias)
			} else {
				out.WriteString(v.Target)
			}
			return ast.WalkSkipChildren, nil
		case *ast.Text:
			if _, code := v.Parent().(*ast.CodeSpan); code {
				out.Write(v.Segment.Value(b))
			} else {
				out.Write(util.UnescapePunctuations(v.Segment.Value(b)))
			}
			if v.SoftLineBreak() || v.HardLineBreak() {
				out.WriteByte(' ')
			}
		case *ast.String:
			out.Write(v.Value)
		case *ast.AutoLink:
			out.Write(v.Label(b))
		case *ast.RawHTML:
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return strings.TrimSpace(out.String())
}

// ── Syntax highlighting CSS ──────────────────────────────────────

var chromaRuleRe = regexp.MustCompile(`(?m)^(/\*.*?\*/ )?\.chroma`)

// ChromaCSS returns token colours for both themes. Rules are scoped with
// :where() so they weigh no more than a plain ".chroma .k" selector and
// custom.css (loaded last) can override them. Backgrounds come from the
// --code-bg theme variable in app.css, not from the chroma style.
func ChromaCSS() string {
	var out strings.Builder
	for _, t := range []struct{ theme, style string }{{"dark", "github-dark"}, {"light", "github"}} {
		style := styles.Get(t.style)
		if style == nil {
			style = styles.Fallback
		}
		var b bytes.Buffer
		f := chromahtml.New(chromahtml.WithClasses(true))
		_ = f.WriteCSS(&b, style)
		for _, line := range strings.Split(b.String(), "\n") {
			if !chromaRuleRe.MatchString(line) { // e.g. the unscoped ".bg" rule
				continue
			}
			line = chromaRuleRe.ReplaceAllString(line, `${1}:where(:root[data-theme="`+t.theme+`"]) .chroma`)
			if strings.Contains(line, "/* PreWrapper */") {
				line = chromaBgRe.ReplaceAllString(line, "")
			}
			out.WriteString(line + "\n")
		}
	}
	return out.String()
}

var chromaBgRe = regexp.MustCompile(`\s*background-color:[^;}]*;?`)
