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

// ── $$ math $$ ───────────────────────────────────────────────────
//
// The TeX is passed through untouched as escaped text in a .math element;
// the page typesets it with the bundled KaTeX (see app.js).

var (
	kindMathBlock  = ast.NewNodeKind("MathBlock")
	kindMathInline = ast.NewNodeKind("MathInline")
)

type mathBlock struct{ ast.BaseBlock }

func (n *mathBlock) Kind() ast.NodeKind { return kindMathBlock }
func (n *mathBlock) Dump(src []byte, level int) {
	ast.DumpHelper(n, src, level, nil, nil)
}
func (n *mathBlock) IsRaw() bool { return true }

type mathInline struct {
	ast.BaseInline
	TeX []byte
}

func (n *mathInline) Kind() ast.NodeKind { return kindMathInline }
func (n *mathInline) Dump(src []byte, level int) {
	ast.DumpHelper(n, src, level, map[string]string{"tex": string(n.TeX)}, nil)
}

// Block form: a line starting with $$ opens a block that runs to the next
// line containing $$ (which may be the same line).
type mathBlockParser struct{}

func (mathBlockParser) Trigger() []byte { return []byte{'$'} }
func (mathBlockParser) Open(_ ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, seg := reader.PeekLine()
	if pos := pc.BlockOffset(); pos < 0 || len(line) < pos+2 || line[pos] != '$' || line[pos+1] != '$' {
		return nil, parser.NoChildren
	}
	rest := line[pc.BlockOffset()+2:]
	n := &mathBlock{}
	if i := bytes.Index(rest, []byte("$$")); i >= 0 {
		if len(bytes.TrimSpace(rest[i+2:])) > 0 {
			return nil, parser.NoChildren // "$$a$$ and more": inline math in a paragraph
		}
		n.Lines().Append(text.NewSegment(seg.Start+pc.BlockOffset()+2, seg.Start+pc.BlockOffset()+2+i))
		reader.AdvanceToEOL()
		return n, parser.NoChildren
	}
	if s := seg.Start + pc.BlockOffset() + 2; s < seg.Stop && len(bytes.TrimSpace(rest)) > 0 {
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
	if i := bytes.Index(line, []byte("$$")); i >= 0 {
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
func (mathBlockParser) CanInterruptParagraph() bool                  { return true }
func (mathBlockParser) CanAcceptIndentedLine() bool                  { return false }

// Inline form: $$…$$ within a line of text.
type mathInlineParser struct{}

func (mathInlineParser) Trigger() []byte { return []byte{'$'} }
func (mathInlineParser) Parse(_ ast.Node, block text.Reader, _ parser.Context) ast.Node {
	line, _ := block.PeekLine()
	if len(line) < 5 || line[1] != '$' {
		return nil
	}
	end := bytes.Index(line[2:], []byte("$$"))
	if end <= 0 {
		return nil
	}
	tex := bytes.TrimSpace(line[2 : 2+end])
	if len(tex) == 0 {
		return nil
	}
	block.Advance(end + 4)
	return &mathInline{TeX: append([]byte(nil), tex...)}
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
			_, _ = w.WriteString(`<span class="math math-inline">`)
			_, _ = w.Write(util.EscapeHTML(n.(*mathInline).TeX))
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
