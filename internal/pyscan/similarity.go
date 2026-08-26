package pyscan

import "sort"

// Near-duplicate detection for the leakage audit ([I11]).
//
// Why this exists at all: `evaluation_set` is scraped from public GitHub
// repositories, and public repositories are full of *copies* - AWS sample
// handlers, Alexa skill templates, bootcamp assignments - vendored into
// different projects by different authors. Two such functions are not
// independent observations, so putting one in a training fold and the other
// in the test fold lets a model score on a function it has effectively
// already seen. At N=95 with ~19 functions per test fold, one leaked pair is
// ~5% of that fold, and because it inflates every method equally it does not
// show up as anomalous - it just makes the whole comparison look better than
// it is.
//
// Grouping by repository is the obvious defence and it is *not sufficient*:
// the most similar pair in this corpus comes from two different repositories
// (a fork), and the AWS-sample clusters come from repositories with nothing
// else in common. Structural similarity is what catches those.

// DefaultSimilarityThreshold is the Jaccard score at or above which two
// functions are treated as the same unit.
//
// Chosen from the corpus rather than by taste: over evaluation_set's 4,465
// pairs the scores fall away sharply after the genuine duplicates, with a gap
// between 0.75 and 0.68 and nothing but shared Lambda boilerplate below it
// (every function imports boto3, defines lambda_handler and returns a
// statusCode, which alone buys ~0.4-0.6 between unrelated functions). Any
// threshold in 0.65-0.75 yields the same connected components on this corpus,
// so the choice is not delicately balanced.
const DefaultSimilarityThreshold = 0.70

// Similarity is the Jaccard index of two functions' canonical code-line
// fingerprints: |A ∩ B| / |A ∪ B|, in [0,1].
//
// The fingerprints come from extract.py's AST canonicalisation, so comments,
// docstrings and formatting are already gone and two copies of the same
// sample that differ only in commentary score 1.0 rather than merely high.
// Returns 0 when either side has no fingerprint (an interpreter too old to
// unparse), which fails toward "not duplicates" - the audit then reports
// fewer groups rather than silently merging unrelated functions.
func Similarity(a, b *Result) float64 {
	if a == nil || b == nil {
		return 0
	}
	return jaccard(a.CodeLineHashes, b.CodeLineHashes)
}

func jaccard(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	set := make(map[string]bool, len(a))
	for _, h := range a {
		set[h] = true
	}
	intersection := 0
	seen := make(map[string]bool, len(b))
	for _, h := range b {
		if seen[h] {
			continue
		}
		seen[h] = true
		if set[h] {
			intersection++
		}
	}
	union := len(set) + len(seen) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// -- grouping ---------------------------------------------------------------

// GroupMember is one function considered for grouping.
type GroupMember struct {
	// FunctionID is the dataset identity and the group's unit of reporting.
	FunctionID string
	// RepoURI is meta.json's source repository, "" when unknown. Two
	// functions from one repository share an author and a house style even
	// when their code differs, so they are merged regardless of similarity.
	RepoURI string
	// Scan supplies the structural fingerprint; may be nil.
	Scan *Result
}

// GroupLink records why two functions ended up in the same group, so the
// audit can be read and argued with rather than taken on trust.
type GroupLink struct {
	A, B       string
	Similarity float64
	Reason     string // "similarity" or "same-repo"
}

// Grouping is the result of the leakage audit.
type Grouping struct {
	// GroupID maps each function id to its group's id (the lexically
	// smallest member, so the assignment is stable across runs).
	GroupID map[string]string
	// Links are the merges that were made, most similar first.
	Links []GroupLink
	// Groups holds the multi-member groups, each sorted, ordered by first
	// member. Singletons are omitted.
	Groups [][]string
	// Threshold is the similarity actually used.
	Threshold float64
}

// GroupFunctions partitions the corpus into units that must not be split
// across a train/test boundary.
//
// Two functions are merged when they are structurally near-identical, or when
// they come from the same repository. Merging is transitive (union-find): if A
// duplicates B and B duplicates C, all three are one unit, which is the
// correct treatment for the sample-code clusters this corpus contains.
func GroupFunctions(members []GroupMember, threshold float64) *Grouping {
	if threshold <= 0 {
		threshold = DefaultSimilarityThreshold
	}
	g := &Grouping{GroupID: make(map[string]string, len(members)), Threshold: threshold}

	parent := make(map[string]string, len(members))
	for _, m := range members {
		parent[m.FunctionID] = m.FunctionID
	}
	var find func(string) string
	find = func(x string) string {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	union := func(a, b string) {
		ra, rb := find(a), find(b)
		if ra == rb {
			return
		}
		// Keep the lexically smaller root so group ids are deterministic.
		if rb < ra {
			ra, rb = rb, ra
		}
		parent[rb] = ra
	}

	for i := 0; i < len(members); i++ {
		for j := i + 1; j < len(members); j++ {
			a, b := members[i], members[j]
			if sim := Similarity(a.Scan, b.Scan); sim >= threshold {
				g.Links = append(g.Links, GroupLink{A: a.FunctionID, B: b.FunctionID, Similarity: sim, Reason: "similarity"})
				union(a.FunctionID, b.FunctionID)
				continue
			}
			if a.RepoURI != "" && a.RepoURI == b.RepoURI {
				g.Links = append(g.Links, GroupLink{
					A: a.FunctionID, B: b.FunctionID,
					Similarity: Similarity(a.Scan, b.Scan), Reason: "same-repo",
				})
				union(a.FunctionID, b.FunctionID)
			}
		}
	}

	byRoot := map[string][]string{}
	for _, m := range members {
		root := find(m.FunctionID)
		g.GroupID[m.FunctionID] = root
		byRoot[root] = append(byRoot[root], m.FunctionID)
	}
	for root, ids := range byRoot {
		if len(ids) < 2 {
			continue
		}
		sortStrings(ids)
		_ = root
		g.Groups = append(g.Groups, ids)
	}
	sortGroups(g.Groups)
	sortLinks(g.Links)
	return g
}

// MultiMemberGroups reports how many groups hold more than one function, and
// how many functions those groups cover - the two numbers the write-up needs
// to state the corpus's effective size.
func (g *Grouping) MultiMemberGroups() (groups, functions int) {
	for _, ids := range g.Groups {
		groups++
		functions += len(ids)
	}
	return groups, functions
}

// EffectiveSize is the number of independent units the corpus actually
// provides, which is what a grouped cross-validation splits over - not the 95
// rows the table appears to have.
func (g *Grouping) EffectiveSize() int {
	roots := map[string]bool{}
	for _, root := range g.GroupID {
		roots[root] = true
	}
	return len(roots)
}

func sortStrings(s []string) { sort.Strings(s) }

func sortGroups(groups [][]string) {
	sort.Slice(groups, func(i, j int) bool { return groups[i][0] < groups[j][0] })
}

// sortLinks orders merges by descending similarity, so the audit report leads
// with the most blatant duplicates.
func sortLinks(links []GroupLink) {
	sort.Slice(links, func(i, j int) bool {
		if links[i].Similarity != links[j].Similarity {
			return links[i].Similarity > links[j].Similarity
		}
		if links[i].A != links[j].A {
			return links[i].A < links[j].A
		}
		return links[i].B < links[j].B
	})
}
