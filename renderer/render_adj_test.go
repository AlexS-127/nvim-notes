package main

import (
	"strings"
	"testing"
)

func TestListRightAfterText(t *testing.T) {
	s := newTestStore(t, nil)
	cases := []struct {
		src  string
		want bool
	}{
		{"text\n- a\n- b\n", true},
		{"text\n\n- a\n- b\n", false},
		{"# Head\n- a\n- b\n", false},
		{"two\nlines\n1. a\n", true},
	}
	for _, c := range cases {
		out, err := NewMarkdown(s, "x.md").Render([]byte(c.src))
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(out, "after-text"); got != c.want {
			t.Errorf("%q: after-text=%v want %v\n%s", c.src, got, c.want, out)
		}
	}
}
