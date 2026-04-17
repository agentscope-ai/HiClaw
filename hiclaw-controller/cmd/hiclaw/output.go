package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

// printTable renders rows as an aligned text table (similar to kubectl get).
func printTable(headers []string, rows [][]string) {
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, strings.Join(headers, "\t"))
	for _, row := range rows {
		fmt.Fprintln(w, strings.Join(row, "\t"))
	}
	w.Flush()
}

// KeyValue is a label-value pair for detail output.
type KeyValue struct {
	Key   string
	Value string
}

// printDetail renders a single resource in "Key: Value" format.
func printDetail(fields []KeyValue) {
	maxKey := 0
	for _, f := range fields {
		if len(f.Key) > maxKey {
			maxKey = len(f.Key)
		}
	}
	for _, f := range fields {
		if f.Value != "" {
			fmt.Printf("%-*s  %s\n", maxKey+1, f.Key+":", f.Value)
		}
	}
}

// printJSON outputs v as indented JSON.
func printJSON(v interface{}) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: marshal JSON: %v\n", err)
		return
	}
	fmt.Println(string(data))
}

// or returns fallback if s is empty.
func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// boolDisplay returns trueStr when b is true, falseStr otherwise. Used for
// table columns and detail rows where a bool should render as a short
// human-readable string rather than "true"/"false".
func boolDisplay(b bool, trueStr, falseStr string) string {
	if b {
		return trueStr
	}
	return falseStr
}

// joinTeamAccess renders Human teamAccess entries as "team:role" joined by
// ", " for compact detail-line output.
func joinTeamAccess(entries []teamAccessEntry) string {
	if len(entries) == 0 {
		return ""
	}
	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		parts = append(parts, fmt.Sprintf("%s:%s", e.Team, e.Role))
	}
	return strings.Join(parts, ", ")
}

// printBundleResponse prints one line per bundle item and returns true if
// any item represents a hard failure (status "error" or "invalid"). Items
// with status "not_found" that carry Warning=true are shown but do not
// count toward fatal.
func printBundleResponse(resp *bundleResponseWire) bool {
	if resp == nil || len(resp.Items) == 0 {
		return false
	}
	fatal := false
	for _, it := range resp.Items {
		label := fmt.Sprintf("%s/%s", it.Kind, it.Name)
		if it.Name == "" {
			label = it.Kind
		}
		warnTag := ""
		if it.Warning {
			warnTag = " (warning)"
		}
		msgTag := ""
		if it.Message != "" {
			msgTag = " — " + it.Message
		}
		fmt.Printf("%s: %s%s%s\n", label, it.Status, warnTag, msgTag)
		if it.Status == "error" || it.Status == "invalid" {
			fatal = true
		}
	}
	return fatal
}
