package pipeline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The repository ships several ConverterOptions files that are applied by
// POSTing them to /reconfigure. Until now only the *embedded* default.yaml
// was covered by a test, so a typo or a renamed converter in any of these
// surfaced as a 500 from /reconfigure - on the measurement machine, at the
// start of a multi-hour run.
//
// These configs are also where the evaluation's experimental design actually
// lives (which stages run, in what order, against which model), so the
// assertions below are not only "does it parse": they pin the properties a
// benchmark run depends on.
var shippedConfigs = []string{
	filepath.Join("..", "..", "default.json"),
	filepath.Join("..", "..", "scripts", "benchmark.json"),
	filepath.Join("..", "..", "scripts", "chatai.json"),
	filepath.Join("..", "..", "scripts", "summary-pipeline.json"),
}

func loadConfig(t *testing.T, path string) *ConverterOptions {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("%s not present: %v", path, err)
	}
	var opts ConverterOptions
	if err := json.Unmarshal(data, &opts); err != nil {
		t.Fatalf("%s is not a valid ConverterOptions document: %v", path, err)
	}
	return &opts
}

func TestShippedConfigsCompile(t *testing.T) {
	for _, path := range shippedConfigs {
		t.Run(filepath.Base(path), func(t *testing.T) {
			opts := loadConfig(t, path)
			if len(opts.Tasks) == 0 {
				t.Fatalf("%s declares no tasks", path)
			}
			if _, err := compilePipeline(opts.PipelineFile); err != nil {
				t.Fatalf("%s no longer compiles: %v", path, err)
			}
		})
	}
}

// Every config must reach a validation stage. A pipeline that translates and
// builds but never tests reports a success that checked nothing, which is the
// one failure mode that silently corrupts a whole benchmark.
func TestShippedConfigsValidateTheirOutput(t *testing.T) {
	testers := map[string]bool{"testRouter": true, "goTester": true, "flociTester": true}
	for _, path := range shippedConfigs {
		t.Run(filepath.Base(path), func(t *testing.T) {
			opts := loadConfig(t, path)
			for _, task := range opts.Tasks {
				if testers[task.Task] {
					return
				}
			}
			t.Errorf("%s never runs a test stage; it would report successes that validated nothing", path)
		})
	}
}

// benchmark.json is the configuration the thesis run uses, so the properties
// the evaluation depends on are asserted rather than trusted to review.
func TestBenchmarkConfigIsFitForTheEvaluationRun(t *testing.T) {
	path := filepath.Join("..", "..", "scripts", "benchmark.json")
	opts := loadConfig(t, path)

	byID := map[string]ConversionTaskStub{}
	for _, task := range opts.Tasks {
		byID[task.ID] = task
	}

	// The feature vector is what makes the run log usable as training data
	// ([I1]/[I4]). Recorded by the pyScan stage, so it has to be in the graph
	// - and required, or a job silently lands in the corpus without one.
	root, ok := byID["root"]
	if !ok {
		t.Fatal("no root task")
	}
	if root.Task != "pyScan" {
		t.Errorf("root task is %q, want pyScan: without it the run records no feature vectors and [I4] has no features to join", root.Task)
	}
	if required, _ := root.TaskArgs["required"].(bool); !required {
		t.Error("pyScan must be required in the benchmark config, or a job without a feature vector is recorded as if it were usable")
	}

	// 40 of the 95 evaluation_set functions declare setup/sideEffects and can
	// only be validated through the Floci route. goTester would ignore those
	// assertions with a warning and pass.
	foundRouter := false
	for _, task := range opts.Tasks {
		if task.Task == "testRouter" {
			foundRouter = true
		}
		if task.Task == "goTester" {
			t.Errorf("task %q names goTester directly; the benchmark must route through testRouter so AWS functions reach the Floci harness", task.ID)
		}
	}
	if !foundRouter {
		t.Error("benchmark config does not use testRouter")
	}

	// The energy coefficients in evaluation/energy.config.json were derived
	// for one specific model. Costing a different model's tokens with them
	// would put every energy figure in the thesis on the wrong constants.
	const costedModel = "devstral-2-123b-instruct-2512"
	if got, _ := opts.Options["model_name"].(string); got != costedModel {
		t.Errorf("model_name = %q, want %q - evaluation/energy.config.json's coefficients are derived for that model; change both together or not at all", got, costedModel)
	}
}
