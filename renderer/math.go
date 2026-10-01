package main

import (
	"bytes"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// ── $…$, $$…$$, \(…\), \[…\] math ───────────────────────────────────────────────────
//
// The TeX is passed through untouched as escaped text in a .math element;
// the page typesets it with the bundled KaTeX (see app.js).

var (
	kindMathBlock  = ast.NewNodeKind("MathBlock")
	kindMathInline = ast.NewNodeKind("MathInline")
)

type mathBlock struct {
	ast.BaseBlock
	closer string // "$$" or `\]`
}

func (n *mathBlock) Kind() ast.NodeKind { return kindMathBlock }
func (n *mathBlock) Dump(src []byte, level int) {
	ast.DumpHelper(n, src, level, nil, nil)
}
func (n *mathBlock) IsRaw() bool { return true }

type mathInline struct {
	ast.BaseInline
	TeX     []byte
	Display bool // \[…\] and $$…$$ typeset in display style only when set by \[
}

func (n *mathInline) Kind() ast.NodeKind { return kindMathInline }
func (n *mathInline) Dump(src []byte, level int) {
	ast.DumpHelper(n, src, level, map[string]string{"tex": string(n.TeX)}, nil)
}

// Block form: a line starting with $$ (or \[) opens a block that runs to the
// next line containing the matching $$ (or \]), which may be the same line.
type mathBlockParser struct{}

func (mathBlockParser) Trigger() []byte { return []byte{'$', '\\'} }
func (mathBlockParser) Open(_ ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, seg := reader.PeekLine()
	pos := pc.BlockOffset()
	if pos < 0 || len(line) < pos+2 {
		return nil, parser.NoChildren
	}
	var closer string
	switch string(line[pos : pos+2]) {
	case "$$":
		closer = "$$"
	case `\[`:
		closer = `\]`
	default:
		return nil, parser.NoChildren
	}
	rest := line[pos+2:]
	n := &mathBlock{closer: closer}
	if i := bytes.Index(rest, []byte(closer)); i >= 0 {
		if len(bytes.TrimSpace(rest[i+2:])) > 0 {
			return nil, parser.NoChildren // "$$a$$ and more": inline math in a paragraph
		}
		n.Lines().Append(text.NewSegment(seg.Start+pos+2, seg.Start+pos+2+i))
		reader.AdvanceToEOL()
		return n, parser.NoChildren
	}
	if s := seg.Start + pos + 2; s < seg.Stop && len(bytes.TrimSpace(rest)) > 0 {
		n.Lines().Append(text.NewSegment(s, seg.Stop))
	}
	reader.AdvanceToEOL()
	pc.Set(mathOpen, n)
	return n, parser.NoChildren
}

var mathOpen = parser.NewContextKey()

func (mathBlockParser) Continue(node ast.Node, reader text.Reader, pc parser.Context) parser.State {
	if pc.Get(mathOpen) != node {
		return parser.Close
	}
	line, seg := reader.PeekLine()
	if i := bytes.Index(line, []byte(node.(*mathBlock).closer)); i >= 0 {
		if i > 0 {
			node.Lines().Append(text.NewSegment(seg.Start, seg.Start+i))
		}
		reader.AdvanceToEOL()
		pc.Set(mathOpen, nil)
		return parser.Close
	}
	node.Lines().Append(seg)
	reader.AdvanceToEOL()
	return parser.Continue | parser.NoChildren
}
func (mathBlockParser) Close(ast.Node, text.Reader, parser.Context) {}
func (mathBlockParser) CanInterruptParagraph() bool                 { return true }
func (mathBlockParser) CanAcceptIndentedLine() bool                 { return false }

// Inline forms: $$…$$, \(…\) and \[…\] (which may wrap across lines of a
// paragraph), and $…$ (one line, Pandoc's rules so "$5 and $10" stays text).
type mathInlineParser struct{}

func (mathInlineParser) Trigger() []byte { return []byte{'$', '\\'} }
func (mathInlineParser) Parse(_ ast.Node, block text.Reader, _ parser.Context) ast.Node {
	line, _ := block.PeekLine()
	switch {
	case bytes.HasPrefix(line, []byte("$$")):
		return scanMath(block, 2, "$$", false, false)
	case bytes.HasPrefix(line, []byte(`\(`)):
		return scanMath(block, 2, `\)`, false, false)
	case bytes.HasPrefix(line, []byte(`\[`)):
		return scanMath(block, 2, `\]`, true, false)
	case line[0] == '$':
		// opener must be followed by non-space
		if len(line) < 2 || isSpace(line[1]) {
			return nil
		}
		return scanMath(block, 1, "$", false, true)
	}
	return nil
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// scanMath reads from the current delimiter (skip bytes long) to closer,
// advancing the reader past it. It restores the reader and returns nil when
// there is no valid closer, so the text falls through as ordinary markdown.
func scanMath(block text.Reader, skip int, closer string, display, pandoc bool) ast.Node {
	startLine, startSeg := block.Position()
	var tex []byte
	line, _ := block.PeekLine()
	from := skip
	for line != nil {
		end := -1
		for i := from; i < len(line); i++ {
			if bytes.HasPrefix(line[i:], []byte(closer)) {
				if pandoc {
					after := i + 1
					if (i > 0 && isSpace(line[i-1])) || i == from || (after < len(line) && line[after] >= '0' && line[after] <= '9') {
						continue
					}
				}
				end = i
				break
			}
			if line[i] == '\\' { // \$, \), \\ … never close (unless it is the closer itself)
				i++
				continue
			}
		}
		if end >= 0 {
			tex = append(tex, line[from:end]...)
			block.Advance(end + len(closer))
			if t := bytes.TrimSpace(tex); len(t) > 0 {
				return &mathInline{TeX: append([]byte(nil), t...), Display: display}
			}
			break
		}
		if pandoc {
			break // single-$ math never spans lines
		}
		tex = append(tex, line[from:]...)
		block.AdvanceLine()
		line, _ = block.PeekLine()
		from = 0
	}
	block.SetPosition(startLine, startSeg)
	return nil
}

type mathRenderer struct{}

func (mathRenderer) RegisterFuncs(r renderer.NodeRendererFuncRegisterer) {
	r.Register(kindMathBlock, func(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		_, _ = w.WriteString(`<div class="math math-display"`)
		if v, ok := n.AttributeString("data-line"); ok {
			_, _ = w.WriteString(` data-line="`)
			_, _ = w.Write(v.([]byte))
			_, _ = w.WriteString(`"`)
		}
		_, _ = w.WriteString(`>`)
		lines := n.Lines()
		for i := 0; i < lines.Len(); i++ {
			s := lines.At(i)
			_, _ = w.Write(util.EscapeHTML(s.Value(src)))
		}
		_, _ = w.WriteString("</div>\n")
		return ast.WalkSkipChildren, nil
	})
	r.Register(kindMathInline, func(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			m := n.(*mathInline)
			if m.Display {
				_, _ = w.WriteString(`<span class="math math-display">`)
			} else {
				_, _ = w.WriteString(`<span class="math math-inline">`)
			}
			_, _ = w.Write(util.EscapeHTML(m.TeX))
			_, _ = w.WriteString("</span>")
		}
		return ast.WalkSkipChildren, nil
	})
}

type mathExt struct{}

func (mathExt) Extend(md goldmark.Markdown) {
	md.Parser().AddOptions(
		parser.WithBlockParsers(util.Prioritized(mathBlockParser{}, 850)),
		parser.WithInlineParsers(util.Prioritized(mathInlineParser{}, 150)),
	)
	md.Renderer().AddOptions(renderer.WithNodeRenderers(util.Prioritized(mathRenderer{}, 500)))
}
