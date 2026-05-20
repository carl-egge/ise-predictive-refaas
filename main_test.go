package refaas_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"testing"
	"time"

	"github.com/carl-egge/ise-predictive-refaas/internal/builder"
	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/carl-egge/ise-predictive-refaas/internal/pipeline"
	"github.com/carl-egge/ise-predictive-refaas/internal/translator"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"golang.org/x/exp/maps"
)

func TestPipelineReader(t *testing.T) {
	reader := bytes.NewReader([]byte(pipeline.DefaultPipelineYAML))
	pipe, err := pipeline.PipelineReader(reader)
	assert.NoError(t, err)
	assert.NotNil(t, pipe)

	assert.IsType(t, &translator.LLMConverter{}, pipe.FirstTask.Execute, "FirstTask should be a valid type")
}

func TestJsonPipelineReader(t *testing.T) {
	fs, err := os.OpenFile("test/pipeline_config.json", os.O_RDONLY, 0666)
	assert.NoError(t, err)
	defer fs.Close()

	pipe, err := pipeline.PipelineReader(fs)
	assert.NoError(t, err)
	assert.NotNil(t, pipe)

	assert.IsType(t, &translator.LLMConverter{}, pipe.FirstTask.Execute, "FirstTask should be a valid type")

	tasks := make([]pipeline.ConversionTask, 0)
	next := []*pipeline.ConversionTask{pipe.FirstTask}
	for len(next) > 0 {
		process := slices.Clone(next)
		next = []*pipeline.ConversionTask{}
		for _, task := range process {
			tasks = append(tasks, *task)
			if task.Next != nil {
				next = append(next, task.Next...)
			}
			if task.OnFailure != nil {
				next = append(next, task.OnFailure)
			}
		}
	}

	for _, task := range tasks {
		c, ok := task.Execute.(*translator.LLMConverter)
		if ok {
			keys := maps.Keys(c.Args())
			assert.True(t, slices.Contains(keys, "model_name"))
			t.Logf("%v", keys)
		}
		t.Logf("%v/%v", task.RetryCount, task.MaxRetryCount)
		assert.GreaterOrEqual(t, task.MaxRetryCount, 1)
	}

}

func TestFullConversion(t *testing.T) {
	reader := bytes.NewReader([]byte(pipeline.DefaultPipelineYAML))
	pipe, err := pipeline.PipelineReader(reader)
	assert.NoError(t, err)
	assert.NotNil(t, pipe)

	log.SetLevel(log.DebugLevel)
	cc, err := pipeline.MakeCodeConverter(&pipeline.ConverterOptions{
		CompiledPipeline: pipe,
		Args: map[string]any{
			"OLLAMA_API_URL": "http://swkgpu1.informatik.uni-hamburg.de:11434",
		},
	})

	assert.Nil(t, err)

	req, err := cc.ConvertFromFileBest("test/f5.zip")
	assert.NoError(t, err)
	assert.NotNil(t, req)
	if req != nil {
		assert.Greater(t, len(req.Metrics.TestCases), 0)
		assert.Greater(t, req.Metrics.BuildTime, time.Duration(0))
		assert.Greater(t, req.Metrics.TestTime, time.Duration(0))

		t.Logf("%v", req.Metrics)
	} else {
		t.FailNow()
	}

}

func TestLogCase(t *testing.T) {
	entries, err := os.ReadDir("chatlogs")
	if err != nil {
		t.Fatal(err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			testCompileByResponseLog(t, path.Join("chatlogs", entry.Name()))
		})
	}
}

func testCompileByResponseLog(t *testing.T, testCase string) {
	tc, err := os.OpenFile(testCase, os.O_RDONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer tc.Close()

	testBytes, err := io.ReadAll(tc)
	if err != nil {
		t.Fatal(err)
	}

	mk := make(map[string]string)
	if body, err := read("test/f1.json"); err == nil {
		mk["f1"] = body
	} else {
		t.Fatal(fmt.Errorf("failed to read %+v", err))
	}

	if body, err := read("test/f2.json"); err == nil {
		mk["f2"] = body
	} else {
		t.Fatal(fmt.Errorf("failed to read %+v", err))
	}

	original := domain.DeploymentPackage{
		TestFiles:  mk,
		BuildFiles: make(map[string]string),
		BuildCmd:   make([]string, 0),
	}

	reader := translator.GoJsonOllamaReader{}

	dp, err := reader.MakeDeploymentFile(string(testBytes), &original)
	if err != nil {
		t.Fatal(err)
	}
	assert.NotNil(t, dp)

	task := pipeline.ConversionTask{
		ID:            "test",
		Execute:       builder.NewGolangBuilder(nil),
		CanApply:      nil,
		RetryCount:    0,
		MaxRetryCount: 1,
		RetryDelay:    0,
		Next:          nil,
		OnFailure:     nil,
		Validation: builder.NewGoPackageTester(map[string]interface{}{
			"strategy": "json",
		}),
	}
	pipe := pipeline.NewPipeline(&task)
	runner := pipeline.NewRunner(context.Background(), pipe, nil)

	req := pipeline.MakeConversionRequest(dp)
	req.WorkingPackage = req.SourcePackage

	err = pipe.Execute(runner, req)
	if err != nil {
		t.Fatal(err)
	}

	assert.Empty(t, req.Errors())
}

func read(fname string) (string, error) {
	tc, err := os.OpenFile(fname, os.O_RDONLY, 0644)
	if err != nil {
		return "", err
	}
	defer tc.Close()

	testBytes, err := io.ReadAll(tc)
	if err != nil {
		return "", err
	}
	return string(testBytes), nil
}

func TestMakeAwareSimilarityValidation(t *testing.T) {
	result := builder.ValidateAwareSimilarity(0.8, "{\"response\":{\"statusCode\":200,\"headers\":null,\"multiValueHeaders\":null,\"body\":\"{\\\"result\\\":20}\"}}",
		"{\"statusCode\":200,\"headers\":null,\"multiValueHeaders\":null,\"body\":{\"result\":20}}")

	assert.True(t, result)
}
