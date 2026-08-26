package pyscan

import (
	"context"
	"math"
	"testing"
)

func fp(hashes ...string) *Result { return &Result{CodeLineHashes: hashes} }

func TestJaccardMath(t *testing.T) {
	cases := []struct {
		name string
		a, b []string
		want float64
	}{
		{"identical", []string{"a", "b", "c"}, []string{"a", "b", "c"}, 1},
		{"disjoint", []string{"a", "b"}, []string{"c", "d"}, 0},
		{"half", []string{"a", "b"}, []string{"b", "c"}, 1.0 / 3.0},
		{"subset", []string{"a", "b", "c", "d"}, []string{"a", "b"}, 0.5},
		{"empty left", nil, []string{"a"}, 0},
		{"empty right", []string{"a"}, nil, 0},
		{"duplicates ignored", []string{"a", "a", "b"}, []string{"a", "b"}, 1},
	}
	for _, tc := range cases {
		if got := jaccard(tc.a, tc.b); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("%s: jaccard = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A missing fingerprint must read as "not duplicates", so the audit reports
// fewer groups rather than silently merging unrelated functions.
func TestSimilarityFailsTowardNotDuplicate(t *testing.T) {
	if got := Similarity(nil, fp("a")); got != 0 {
		t.Errorf("nil scan: %v, want 0", got)
	}
	if got := Similarity(fp(), fp("a")); got != 0 {
		t.Errorf("empty fingerprint: %v, want 0", got)
	}
}

func TestGroupFunctionsMergesNearDuplicates(t *testing.T) {
	members := []GroupMember{
		{FunctionID: "f1", Scan: fp("a", "b", "c", "d")},
		{FunctionID: "f2", Scan: fp("a", "b", "c", "e")}, // 3/5 = 0.6
		{FunctionID: "f3", Scan: fp("x", "y", "z")},
	}
	g := GroupFunctions(members, 0.5)

	if g.GroupID["f1"] != g.GroupID["f2"] {
		t.Error("f1 and f2 are above threshold and must share a group")
	}
	if g.GroupID["f3"] == g.GroupID["f1"] {
		t.Error("f3 is unrelated and must stay in its own group")
	}
	if got := g.EffectiveSize(); got != 2 {
		t.Errorf("effective size = %d, want 2", got)
	}
}

func TestGroupFunctionsRespectsThreshold(t *testing.T) {
	members := []GroupMember{
		{FunctionID: "f1", Scan: fp("a", "b", "c", "d")},
		{FunctionID: "f2", Scan: fp("a", "b", "c", "e")}, // 0.6
	}
	if g := GroupFunctions(members, 0.7); g.GroupID["f1"] == g.GroupID["f2"] {
		t.Error("0.6 similarity must not merge at a 0.7 threshold")
	}
	if g := GroupFunctions(members, 0.5); g.GroupID["f1"] != g.GroupID["f2"] {
		t.Error("0.6 similarity must merge at a 0.5 threshold")
	}
}

// Sample code copied into three repos is one unit, not three - and not two
// plus one, which is what a non-transitive pairwise rule would give.
func TestGroupFunctionsIsTransitive(t *testing.T) {
	members := []GroupMember{
		{FunctionID: "a", Scan: fp("1", "2", "3", "4")},
		{FunctionID: "b", Scan: fp("1", "2", "3", "5")},
		{FunctionID: "c", Scan: fp("1", "2", "3", "6")},
	}
	g := GroupFunctions(members, 0.5)
	if g.GroupID["a"] != g.GroupID["c"] {
		t.Error("a~b and b~c must put a and c in one group")
	}
	if got := g.EffectiveSize(); got != 1 {
		t.Errorf("effective size = %d, want 1", got)
	}
}

// Same-repo functions share an author and a house style even when the code
// differs, so they are not independent observations.
func TestGroupFunctionsMergesSameRepositoryDespiteLowSimilarity(t *testing.T) {
	members := []GroupMember{
		{FunctionID: "f1", RepoURI: "https://github.com/x/y", Scan: fp("a", "b")},
		{FunctionID: "f2", RepoURI: "https://github.com/x/y", Scan: fp("c", "d")},
		{FunctionID: "f3", RepoURI: "https://github.com/other/z", Scan: fp("e", "f")},
	}
	g := GroupFunctions(members, 0.7)
	if g.GroupID["f1"] != g.GroupID["f2"] {
		t.Error("same repo must merge regardless of similarity")
	}
	if g.GroupID["f3"] == g.GroupID["f1"] {
		t.Error("a different repo must not merge")
	}
}

// An unknown repository must not merge everything that also lacks one.
func TestGroupFunctionsIgnoresEmptyRepoURI(t *testing.T) {
	members := []GroupMember{
		{FunctionID: "f1", RepoURI: "", Scan: fp("a", "b")},
		{FunctionID: "f2", RepoURI: "", Scan: fp("c", "d")},
	}
	if g := GroupFunctions(members, 0.7); g.GroupID["f1"] == g.GroupID["f2"] {
		t.Error("two unknown repositories are not the same repository")
	}
}

// Group ids end up in a committed table, so they must not depend on input
// order or on map iteration.
func TestGroupIDsAreDeterministic(t *testing.T) {
	forward := []GroupMember{
		{FunctionID: "f1", Scan: fp("a", "b", "c")},
		{FunctionID: "f2", Scan: fp("a", "b", "c")},
		{FunctionID: "f3", Scan: fp("z")},
	}
	reversed := []GroupMember{forward[2], forward[1], forward[0]}

	a := GroupFunctions(forward, 0.7)
	b := GroupFunctions(reversed, 0.7)
	for _, id := range []string{"f1", "f2", "f3"} {
		if a.GroupID[id] != b.GroupID[id] {
			t.Errorf("%s: group %q vs %q - assignment depends on input order", id, a.GroupID[id], b.GroupID[id])
		}
	}
	if a.GroupID["f1"] != "f1" {
		t.Errorf("group id should be the lexically smallest member, got %q", a.GroupID["f1"])
	}
}

func TestGroupFunctionsReportsWhyItMerged(t *testing.T) {
	members := []GroupMember{
		{FunctionID: "f1", RepoURI: "r", Scan: fp("a", "b")},
		{FunctionID: "f2", RepoURI: "r", Scan: fp("c", "d")},
		{FunctionID: "f3", Scan: fp("a", "b")},
	}
	g := GroupFunctions(members, 0.7)

	reasons := map[string]string{}
	for _, l := range g.Links {
		reasons[l.A+"/"+l.B] = l.Reason
	}
	if reasons["f1/f3"] != "similarity" {
		t.Errorf("identical fingerprints should merge on similarity, got %q", reasons["f1/f3"])
	}
	if reasons["f1/f2"] != "same-repo" {
		t.Errorf("dissimilar same-repo pair should merge on same-repo, got %q", reasons["f1/f2"])
	}
}

func TestMultiMemberGroupsCountsOnlyRealGroups(t *testing.T) {
	members := []GroupMember{
		{FunctionID: "a", Scan: fp("1")},
		{FunctionID: "b", Scan: fp("1")},
		{FunctionID: "c", Scan: fp("2")},
	}
	g := GroupFunctions(members, 0.7)
	groups, functions := g.MultiMemberGroups()
	if groups != 1 || functions != 2 {
		t.Errorf("got %d groups covering %d functions, want 1 and 2", groups, functions)
	}
}

// -- fingerprint properties (need the scanner) -----------------------------

// The fingerprint must see through commentary. Two copies of one sample that
// differ only in comments and docstrings are the same function for grouping
// purposes, and a text-based comparison would score them merely "similar".
func TestFingerprintIgnoresCommentsAndDocstrings(t *testing.T) {
	requireScanner(t)
	plain := `
def lambda_handler(event, context):
    total = 0
    for item in event["items"]:
        total += item["price"]
    return {"total": total}
`
	commented := `
"""Module docstring that says nothing useful."""
# a leading comment

def lambda_handler(event, context):
    """Adds up the prices.

    Multi-line docstring.
    """
    # running total
    total = 0
    for item in event["items"]:   # inline comment
        total += item["price"]
    return {"total": total}
`
	a, err := Scan(context.Background(), plain)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Scan(context.Background(), commented)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.CodeLineHashes) == 0 {
		t.Fatal("no fingerprint produced")
	}
	if got := Similarity(a, b); got != 1 {
		t.Errorf("similarity = %.3f, want 1.0 - comments and docstrings must not affect the fingerprint", got)
	}
}

// Formatting differences must not either, or reformatted copies escape the audit.
func TestFingerprintIgnoresFormatting(t *testing.T) {
	requireScanner(t)
	a, err := Scan(context.Background(), "def handler(e,c):\n    return {'a':1,'b':2}\n")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Scan(context.Background(), "def handler( e , c ):\n    return {\n        'a': 1,\n        'b': 2,\n    }\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := Similarity(a, b); got != 1 {
		t.Errorf("similarity = %.3f, want 1.0 - formatting must not affect the fingerprint", got)
	}
}

// It must still distinguish genuinely different functions, or everything
// collapses into one group.
func TestFingerprintDistinguishesDifferentFunctions(t *testing.T) {
	requireScanner(t)
	a, err := Scan(context.Background(), "import json\n\ndef handler(e, c):\n    return {'sum': e['a'] + e['b']}\n")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Scan(context.Background(), "import boto3\n\ndef handler(e, c):\n    s3 = boto3.client('s3')\n    return s3.list_buckets()\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := Similarity(a, b); got >= DefaultSimilarityThreshold {
		t.Errorf("similarity = %.3f, want below the %.2f threshold", got, DefaultSimilarityThreshold)
	}
}
