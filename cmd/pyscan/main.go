// Command pyscan runs the deterministic Python analysis over one or more
// function artifacts and prints the result as CSV or JSON.
//
// It never runs during a conversion - it is the offline half of internal/
// pyscan, the same separation cmd/energy keeps for the energy model. Its
// output is the feature side of [I4]'s dataset table: one row per function,
// which the training pipeline joins to the labels from [I1]'s run log.
//
//	go run ./cmd/pyscan evaluation/evaluation_set/*.zip > features.csv
//	go run ./cmd/pyscan -json examples/paper/f1.zip
//	go run ./cmd/pyscan -hints examples/paper/f1.zip
//
// The column order comes from internal/pyscan.FeatureNames(), so the table
// and the model that consumes it cannot disagree about which value is which.
package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/carl-egge/ise-predictive-refaas/internal/fixture"
	"github.com/carl-egge/ise-predictive-refaas/internal/inputhandler"
	"github.com/carl-egge/ise-predictive-refaas/internal/pyscan"
	"github.com/google/uuid"
)

type row struct {
	FunctionID string `json:"function_id"`
	Artifact   string `json:"artifact"`
	Bucket     string `json:"bucket,omitempty"`
	AWS        bool   `json:"aws"`
	// GroupID is the leakage-audit unit ([I11]): functions that are
	// structural near-duplicates or share a source repository get one id, and
	// a grouped cross-validation must split on this rather than on
	// function_id.
	GroupID  string             `json:"group_id,omitempty"`
	Features map[string]float64 `json:"features"`
	Hints    *hints             `json:"hints,omitempty"`
	Error    string             `json:"error,omitempty"`

	// Carried for the audit, not serialised: the structural fingerprint and
	// the source repository this row is grouped on.
	scan    *pyscan.Result
	repoURI string
}

type hints struct {
	LibHints    string `json:"lib_hints,omitempty"`
	AWSHints    string `json:"aws_hints,omitempty"`
	PyFeatures  string `json:"py_features,omitempty"`
	Feasibility string `json:"feasibility_warning,omitempty"`
}

func main() {
	asJSON := flag.Bool("json", false, "emit JSON instead of CSV")
	withHints := flag.Bool("hints", false, "include the prompt-facing hint text (implies -json)")
	showGroups := flag.Bool("groups", false, "print the near-duplicate leakage audit in full ([I11])")
	similarity := flag.Float64("similarity", pyscan.DefaultSimilarityThreshold,
		"Jaccard threshold at or above which two functions are treated as one unit")
	flag.Parse()

	paths := flag.Args()
	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "usage: pyscan [-json] [-hints] [-groups] <artifact.zip>...")
		os.Exit(2)
	}
	if !pyscan.Available() {
		fmt.Fprintln(os.Stderr, "pyscan: no python3 interpreter on PATH (set PYSCAN_PYTHON to override)")
		os.Exit(1)
	}

	rows := make([]row, 0, len(paths))
	failures := 0
	for _, path := range paths {
		r := scanArtifact(path, *withHints)
		if r.Error != "" {
			failures++
			fmt.Fprintf(os.Stderr, "pyscan: %s: %s\n", path, r.Error)
		}
		rows = append(rows, r)
	}

	// Grouping happens before output so group_id ships as a column of the
	// same table: [I4]'s dataset builder must not have to join it in from a
	// second file, and [I7]'s splitter must not have to be trusted to
	// remember.
	grouping := assignGroups(rows, *similarity)

	var err error
	if *asJSON || *withHints {
		err = writeJSON(rows)
	} else {
		err = writeCSV(rows)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "pyscan: %v\n", err)
		os.Exit(1)
	}

	reportConstantColumns(rows)
	reportGrouping(grouping, *showGroups)

	// A partial table is worse than a loud failure: a function silently
	// missing from the features file becomes a function silently missing
	// from the training set.
	if failures > 0 {
		fmt.Fprintf(os.Stderr, "pyscan: %d of %d artifacts failed\n", failures, len(paths))
		os.Exit(1)
	}
}

// reportConstantColumns names the features that take a single value across
// the whole scanned corpus.
//
// They carry no information for a model trained on it, and at N=95 every
// such column is pure width - the dimension that hurts most at this sample
// size. They are deliberately *not* dropped from the vector, which is a
// fixed contract and must stay general enough for an arbitrary upload; the
// training pipeline drops zero-variance columns instead, and must do so
// inside each cross-validation fold rather than over the whole table, or the
// choice of which columns to drop leaks the test fold ([I7]).
//
// Worth reading closely rather than skimming: if has_infeasible_lib is
// constant zero, then baseline B4's blocklist ([I5]) cannot skip a single
// function on this corpus and is identical to always-translate.
func reportConstantColumns(rows []row) {
	scanned := make([]row, 0, len(rows))
	for _, r := range rows {
		if r.Features != nil {
			scanned = append(scanned, r)
		}
	}
	if len(scanned) < 2 {
		return
	}

	var constant []string
	for _, name := range pyscan.FeatureNames() {
		first := scanned[0].Features[name]
		same := true
		for _, r := range scanned[1:] {
			if r.Features[name] != first {
				same = false
				break
			}
		}
		if same {
			constant = append(constant, fmt.Sprintf("%s=%s", name, formatValue(first)))
		}
	}
	if len(constant) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "pyscan: %d of %d feature columns are constant across these %d functions "+
		"(no information for training; drop zero-variance columns inside the CV fold, not over the whole table):\n  %s\n",
		len(constant), len(pyscan.FeatureNames()), len(scanned), strings.Join(constant, ", "))
}

func scanArtifact(path string, wantHints bool) row {
	r := row{Artifact: filepath.Base(path)}

	pkg, err := inputhandler.ReadFromFile(path)
	if err != nil {
		r.Error = fmt.Sprintf("unreadable artifact: %v", err)
		return r
	}
	r.FunctionID = domain.ResolveFunctionID(pkg.Meta, filepath.Base(path), uuid.Nil)
	if pkg.Meta != nil {
		r.Bucket = pkg.Meta.Bucket
		r.AWS = pkg.Meta.AWS
		r.repoURI = repoURI(pkg.Meta.Raw)
	}

	result, err := pyscan.Scan(context.Background(), pkg.RootFile)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	r.scan = result

	// Fixtures are part of the feature vector ([I3] family 4). An artifact
	// whose fixtures do not parse still yields source features, but the row
	// is marked so the dataset builder can exclude it rather than train on a
	// silently zeroed fixture family.
	cases, ferr := fixture.FromPackage(pkg)
	if ferr != nil {
		r.Error = fmt.Sprintf("fixtures unreadable (source features only): %v", ferr)
		cases = nil
	}

	features, err := pyscan.BuildFeatures(result, cases)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	r.Features = features.Map()

	if wantHints {
		r.Hints = &hints{
			LibHints:    result.LibHints(),
			AWSHints:    result.AWSHints(),
			PyFeatures:  result.PyFeatures(),
			Feasibility: result.FeasibilityWarning(),
		}
	}
	return r
}

func writeCSV(rows []row) error {
	names := pyscan.FeatureNames()
	w := csv.NewWriter(os.Stdout)
	defer w.Flush()

	header := append([]string{"function_id", "artifact", "bucket", "aws", "group_id"}, names...)
	if err := w.Write(header); err != nil {
		return err
	}
	for _, r := range rows {
		if r.Features == nil {
			continue
		}
		rec := []string{r.FunctionID, r.Artifact, r.Bucket, strconv.FormatBool(r.AWS), r.GroupID}
		for _, n := range names {
			rec = append(rec, formatValue(r.Features[n]))
		}
		if err := w.Write(rec); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// formatValue keeps integers integral in the CSV so the table reads cleanly
// and a downstream parser is not forced to treat every count as a float.
//
// Non-integers use 'g' with precision -1: the shortest decimal that round-trips
// back to the same float64. This used to be six fixed decimals, which read more
// tidily and was wrong in a way that only shows up at deployment. A model is
// trained on the values in this file but scored, in the service, against the
// values internal/pyscan produces directly — so any rounding here is a
// train/serve skew. Measured on the evaluation_set model it moved probabilities
// by ~1e-7 (via cc_per_lloc and halstead_difficulty, whose sixth decimal is
// significant), which is far too small to notice and quite large enough to flip
// a candidate sitting on the threshold. Exactness costs a few characters per
// row.
func formatValue(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

func writeJSON(rows []row) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{
		"feature_schema_version": pyscan.FeatureSchemaVersion,
		"feature_names":          pyscan.FeatureNames(),
		"functions":              rows,
	})
}
