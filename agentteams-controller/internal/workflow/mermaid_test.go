package workflow

import (
	"strings"
	"testing"
)

func TestRenderMermaid(t *testing.T) {
	s := &Snapshot{
		Nodes: []Node{
			{ID: "t1", Name: "Task 1", Status: "completed"},
			{ID: "t2", Name: "Task 2", Status: "delegated"},
		},
		Edges: []Edge{{Source: "t1", Target: "t2"}},
		Next:  []string{"t2"},
	}
	out := RenderMermaid(s)
	if !strings.HasPrefix(out, "flowchart LR") {
		t.Fatalf("expected flowchart header, got %q", out)
	}
	if !strings.Contains(out, `t1["Task 1: completed"]:::completed`) {
		t.Fatalf("expected node t1 with status class, got %q", out)
	}
	if !strings.Contains(out, "t1 --> t2") {
		t.Fatalf("expected edge t1 --> t2, got %q", out)
	}
	// ready node t2 gets the ready class (overrides the status class)
	if !strings.Contains(out, `t2["Task 2: delegated"]:::ready`) {
		t.Fatalf("expected ready class on t2, got %q", out)
	}
	for _, cd := range mermaidClassDefs {
		if !strings.Contains(out, cd) {
			t.Fatalf("expected classDef %q, got %q", cd, out)
		}
	}
}

func TestRenderMermaid_StatusClasses(t *testing.T) {
	s := &Snapshot{
		Nodes: []Node{
			{ID: "a", Name: "A", Status: "pending"},
			{ID: "b", Name: "B", Status: "in-progress"},
			{ID: "c", Name: "C", Status: "revision"},
			{ID: "d", Name: "D", Status: "blocked"},
			{ID: "e", Name: "E"}, // unknown/empty status: no class
		},
	}
	out := RenderMermaid(s)
	for _, want := range []string{
		`a["A: pending"]:::pending`,
		`b["B: in-progress"]:::inProgress`,
		`c["C: revision"]:::revision`,
		`d["D: blocked"]:::blocked`,
		`e["E"]`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in output, got %q", want, out)
		}
	}
}

func TestRenderMermaid_EmptyGraph(t *testing.T) {
	out := RenderMermaid(&Snapshot{Nodes: []Node{}, Edges: []Edge{}, Next: []string{}})
	if !strings.HasPrefix(out, "flowchart LR") {
		t.Fatalf("expected flowchart header, got %q", out)
	}
	if strings.Contains(out, "-->") {
		t.Fatalf("no edges expected, got %q", out)
	}
}

func TestRenderMermaid_NilSnapshot(t *testing.T) {
	// Defensive: a nil snapshot must not panic (renders header only).
	out := RenderMermaid(&Snapshot{})
	if !strings.HasPrefix(out, "flowchart LR") {
		t.Fatalf("expected flowchart header, got %q", out)
	}
}
