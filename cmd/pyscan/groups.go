package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/carl-egge/ise-predictive-refaas/internal/pyscan"
)

// repoURI pulls meta.json's source repository out of the verbatim block.
// It is not a typed field on domain.FunctionMeta, and adding one just for
// this would push a training-only concern into the domain types.
func repoURI(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var meta struct {
		RepoURI string `json:"repo_uri"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return ""
	}
	return meta.RepoURI
}

// assignGroups runs the leakage audit over the scanned corpus and writes each
// row's group id back onto it.
//
// Returns nil when fewer than two rows scanned successfully, in which case
// grouping is meaningless and every row keeps an empty group id.
func assignGroups(rows []row, threshold float64) *pyscan.Grouping {
	members := make([]pyscan.GroupMember, 0, len(rows))
	for _, r := range rows {
		if r.scan == nil || r.FunctionID == "" {
			continue
		}
		members = append(members, pyscan.GroupMember{
			FunctionID: r.FunctionID,
			RepoURI:    r.repoURI,
			Scan:       r.scan,
		})
	}
	if len(members) < 2 {
		return nil
	}

	grouping := pyscan.GroupFunctions(members, threshold)
	for i := range rows {
		if id, ok := grouping.GroupID[rows[i].FunctionID]; ok {
			rows[i].GroupID = id
		}
	}
	return grouping
}

// reportGrouping prints the audit.
//
// It always prints the headline - how many independent units the corpus
// actually provides - because that number, not the row count, is what a
// grouped cross-validation splits over and what the write-up has to state.
// The per-group detail is printed only under -groups, since a features run
// should not bury its own output.
func reportGrouping(g *pyscan.Grouping, verbose bool) {
	if g == nil {
		return
	}
	groups, functions := g.MultiMemberGroups()
	if groups == 0 {
		fmt.Fprintf(os.Stderr,
			"pyscan: no near-duplicate or same-repo groups found at similarity >= %.2f\n", g.Threshold)
		return
	}

	fmt.Fprintf(os.Stderr,
		"pyscan: LEAKAGE AUDIT - %d functions fall into %d multi-member groups at similarity >= %.2f.\n"+
			"  Effective corpus size is %d independent units, not %d rows. Cross-validation must\n"+
			"  split on group_id (StratifiedGroupKFold), or a near-duplicate pair straddling the\n"+
			"  train/test boundary lets a model score on a function it has already seen.\n",
		functions, groups, g.Threshold, g.EffectiveSize(), len(g.GroupID))

	if !verbose {
		fmt.Fprintln(os.Stderr, "  Re-run with -groups for the group and link detail.")
		return
	}

	fmt.Fprintln(os.Stderr, "\n  Groups:")
	for _, ids := range g.Groups {
		fmt.Fprintf(os.Stderr, "    %v\n", ids)
	}

	fmt.Fprintln(os.Stderr, "\n  Why (most similar first):")
	for _, l := range g.Links {
		switch l.Reason {
		case "same-repo":
			fmt.Fprintf(os.Stderr, "    %-4s %-4s  same repository (structural similarity only %.3f)\n",
				l.A, l.B, l.Similarity)
		default:
			fmt.Fprintf(os.Stderr, "    %-4s %-4s  similarity %.3f\n", l.A, l.B, l.Similarity)
		}
	}
	fmt.Fprintln(os.Stderr,
		"\n  Note: a pair merged on similarity alone comes from *different* repositories -\n"+
			"  copied sample code. Grouping by repo_uri would not have caught it.")
}
