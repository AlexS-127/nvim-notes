package main

import (
	"strings"
	"testing"
)

func TestDropWeakQuestions(t *testing.T) {
	in := "## T\n\nQ: Which is NOT a mood? [gen]\na) A\nb) B\nA: b\n\nQ: Select all moods. [gen]\na) A\nb) B\nc) C\nA: a, c\n\nQ: Pick one\na) x\nb) all of the above\nA: b\n\nQ: Select all moods. [gen]\nA: x\n\nQ: Why?\nA: because\n"
	got := dropWeakQuestions(in)
	if strings.Contains(got, "NOT") || strings.Contains(got, "all of the above") {
		t.Errorf("weak questions kept:\n%s", got)
	}
	if strings.Count(got, "Select all moods") != 1 || !strings.Contains(got, "Q: Why?") || !strings.HasPrefix(got, "## T") {
		t.Errorf("good questions lost or repeated:\n%s", got)
	}
}
