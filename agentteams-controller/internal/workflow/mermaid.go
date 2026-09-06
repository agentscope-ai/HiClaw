// Package workflow holds workflow-snapshot presentation helpers shared by
// the controller API and the agt CLI.
package workflow

import (
	"fmt"
	"strings"
)

// Snapshot is the minimal workflow shape needed for mermaid rendering. It
// mirrors the nodes/edges/next fields of the controller's workflow response
// (LangGraph StateSnapshot-aligned).
type Snapshot struct {
	Nodes []Node   `json:"nodes"`
	Edges []Edge   `json:"edges"`
	Next  []string `json:"next"`
}

// Node is a single workflow graph node. Status is the normalized frontend
// enum (pending | delegated | in-progress | completed | revision | blocked).
type Node struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Status   string `json:"status,omitempty"`
	Assignee string `json:"assignee,omitempty"`
}

// Edge is a dependency edge (source must complete before target).
type Edge struct {
	Source      string `json:"source"`
	Target      string `json:"target"`
	Conditional bool   `json:"conditional,omitempty"`
}

// mermaidStatusClass maps the normalized node status to a mermaid classDef
// name.
var mermaidStatusClass = map[string]string{
	"pending":     "pending",
	"delegated":   "delegated",
	"in-progress": "inProgress",
	"completed":   "completed",
	"revision":    "revision",
	"blocked":     "blocked",
}

// mermaidClassDefs lists every classDef the renderer emits, in stable order.
var mermaidClassDefs = []string{
	"classDef ready fill:#d4edda,stroke:#28a745;",
	"classDef pending fill:#e9ecef,stroke:#6c757d;",
	"classDef delegated fill:#cfe2ff,stroke:#0d6efd;",
	"classDef inProgress fill:#fff3cd,stroke:#ffc107;",
	"classDef completed fill:#d4edda,stroke:#198754;",
	"classDef revision fill:#ffe5d0,stroke:#fd7e14;",
	"classDef blocked fill:#f8d7da,stroke:#dc3545;",
}

// RenderMermaid renders a workflow snapshot as a mermaid flowchart
// (flowchart LR), mirroring LangGraph's draw_mermaid helper. Each node label
// is "name: status"; next/ready nodes are highlighted with the `ready` class,
// all other nodes get a status-specific class.
func RenderMermaid(s *Snapshot) string {
	var b strings.Builder
	b.WriteString("flowchart LR\n")
	nextSet := map[string]bool{}
	for _, id := range s.Next {
		nextSet[id] = true
	}
	for _, n := range s.Nodes {
		label := n.Name
		if n.Status != "" {
			label += ": " + n.Status
		}
		style := ""
		if nextSet[n.ID] {
			style = ":::ready"
		} else if c, ok := mermaidStatusClass[n.Status]; ok {
			style = ":::" + c
		}
		fmt.Fprintf(&b, "    %s[%q]%s\n", n.ID, label, style)
	}
	for _, e := range s.Edges {
		fmt.Fprintf(&b, "    %s --> %s\n", e.Source, e.Target)
	}
	for _, cd := range mermaidClassDefs {
		b.WriteString("    " + cd + "\n")
	}
	return b.String()
}
