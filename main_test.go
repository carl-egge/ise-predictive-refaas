package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"golang.org/x/exp/maps"
)

func TestPipelineReader(t *testing.T) {
	reader := bytes.NewReader([]byte(defaultPipelineFile))
	pipeline, err := PipelineReader(reader)
	assert.NoError(t, err)
	assert.NotNil(t, pipeline)

	assert.IsTypef(t, pipeline.FirstTask.Execute, &LLMConverter{}, "FirstTask should be a valid type")
}

func TestJsonPipelineReader(t *testing.T) {
	fs, err := os.OpenFile("test/pipeline_config.json", os.O_RDONLY, 0666)
	assert.NoError(t, err)
	defer fs.Close()

	pipeline, err := PipelineReader(fs)
	assert.NoError(t, err)
	assert.NotNil(t, pipeline)

	assert.IsTypef(t, pipeline.FirstTask.Execute, &LLMConverter{}, "FirstTask should be a valid type")

	//Unwrap graph, we assume Acyclic
	tasks := make([]ConversionTask, 0)
	next := []*ConversionTask{pipeline.FirstTask}
	for len(next) > 0 {
		process := slices.Clone(next)
		next = []*ConversionTask{}
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
		c, ok := task.Execute.(*LLMConverter)
		if ok {
			keys := maps.Keys(c.args)
			assert.True(t, slices.Contains(keys, "model_name"))
			t.Logf("%v", keys)
		}
		t.Logf("%v/%v", task.RetryCount, task.MaxRetryCount)
		assert.GreaterOrEqual(t, task.MaxRetryCount, 1)
	}

}

func TestFullConversion(t *testing.T) {
	reader := bytes.NewReader([]byte(defaultPipelineFile))
	pipeline, err := PipelineReader(reader)
	assert.NoError(t, err)
	assert.NotNil(t, pipeline)

	log.SetLevel(log.DebugLevel)
	cc, err := MakeCodeConverter(&ConverterOptions{
		CompiledPipeline: pipeline,
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

func testCompileByResponseLog(t *testing.T, test_case string) {
	tc, err := os.OpenFile(test_case, os.O_RDONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}

	defer tc.Close()
	test_bytes, err := io.ReadAll(tc)
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

	original := DeploymentPackage{
		TestFiles:  mk,
		BuildFiles: make(map[string]string),
		BuildCmd:   make([]string, 0),
	}

	reader := GoJsonOllamaReader{}

	dp, err := reader.makeDeploymentFile(string(test_bytes), &original)
	if err != nil {
		t.Fatal(err)
	}
	assert.NotNil(t, dp)

	task := ConversionTask{
		ID:            "test",
		Execute:       makeGolangBuilder(nil),
		CanApply:      nil,
		RetryCount:    0,
		MaxRetryCount: 1,
		RetryDelay:    0,
		Next:          nil,
		OnFailure:     nil,
		Validation: makeGoPackageTester(map[string]interface{}{
			"strategy": "json",
		}),
	}
	pipeline := NewPipeline(&task)
	runner := &PipelineRunner{
		Context:  context.Background(),
		pipeline: nil,
		client:   nil,
	}

	req := MakeConversionRequest(dp)
	req.WorkingPackage = req.SourcePackage

	err = pipeline.Execute(runner, req)
	if err != nil {
		t.Fatal(err)
	}

	assert.Nil(t, req.err)
}

func read(fname string) (string, error) {
	tc, err := os.OpenFile(fname, os.O_RDONLY, 0644)
	if err != nil {
		return "", err
	}

	defer tc.Close()
	test_bytes, err := io.ReadAll(tc)
	if err != nil {
		return "", err
	}
	return string(test_bytes), nil
}

func TestMakeAwareSimilarityValidation(t *testing.T) {
	jsonValidator := MakeAwareSimilarityValidation(0.8)

	result := jsonValidator.validate("{\"response\":{\"statusCode\":200,\"headers\":null,\"multiValueHeaders\":null,\"body\":\"{\\\"result\\\":20}\"}}",
		"{\"statusCode\":200,\"headers\":null,\"multiValueHeaders\":null,\"body\":{\"result\":20}}")

	assert.True(t, result)
}

func TestValidationFromFolder(t *testing.T) {
	log.SetLevel(log.DebugLevel)
	dir := "/Users/b/projects/sustian/functionset_aws/manual_set"
	validateFromFolder(t, dir)
}

func TestValidationFromFolders(t *testing.T) {
	dirs := []string{
		//"/Users/b/projects/sustian/functionset_aws/manual_set",
		//"/Users/b/projects/sustian/functionset_aws/advanced_deepseek-r1_32b",
		"/Users/b/projects/sustian/functionset_aws/advanced_gemma3_27b",
		"/Users/b/projects/sustian/functionset_aws/advanced_qwen2.5-coder_32b",
		"/Users/b/projects/sustian/functionset_aws/advanced_qwq",
		"/Users/b/projects/sustian/functionset_aws/simple_deepseek-r1_32b",
		"/Users/b/projects/sustian/functionset_aws/simple_gemma3_27b",
		"/Users/b/projects/sustian/functionset_aws/simple_qwen2.5-coder_32b",
		"/Users/b/projects/sustian/functionset_aws/simple_qwq",
	}
	for _, dir := range dirs {
		validateFromFolder(t, dir)
	}
}

func validateFromFolder(t *testing.T, dir string) {
	files, err := ListZipFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	experiment := filepath.Base(dir)
	for _, file := range files {
		t.Run(fmt.Sprintf("%s_%s", experiment, file), func(t *testing.T) {
			cc, err := MakeCodeConverter(&ConverterOptions{
				Args: map[string]any{
					"OLLAMA_API_URL": OLLAMA_API_URL,
				},
			})
			if err != nil {
				t.Fatal(err)
			}

			dp, err := cc.ReadDeploymentPackageFromFile(path.Join(dir, file))
			assert.Nil(t, err)

			dp.BuildCmd = []string{
				"go mod init example",
				"go mod tidy",
				"go build .",
			}
			dp.Suffix = "go"

			task := ConversionTask{
				ID:            "test",
				Execute:       makeGolangBuilder(nil),
				CanApply:      nil,
				RetryCount:    0,
				MaxRetryCount: 1,
				RetryDelay:    0,
				Next:          nil,
				OnFailure:     nil,
				Validation: makeGoPackageTester(map[string]interface{}{
					"strategy": "json",
				}),
			}
			pipeline := NewPipeline(&task)

			req := MakeConversionRequest(dp)
			req.WorkingPackage = req.SourcePackage

			err = pipeline.Execute(cc, req)
			t.Logf("%s\n%+v", file, req.Metrics.TestCases)
			assert.Nil(t, err)

		})
	}
}

func ListZipFiles(dir string) ([]string, error) {
	var zipFiles []string

	// Read directory contents
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	// Iterate over files and filter ZIP files
	for _, file := range files {
		if !file.IsDir() && filepath.Ext(file.Name()) == ".zip" {
			zipFiles = append(zipFiles, file.Name())
		}
	}

	return zipFiles, nil
}

func TestDeepSeekReader(t *testing.T) {
	dsor := GoDeepSeekOllamaReader{}
	tf, err := os.OpenFile("test/deepseek.txt", os.O_RDONLY, 0644)
	defer tf.Close()
	assert.Nil(t, err)

	deepseek_response, err := io.ReadAll(tf)
	assert.Nil(t, err)

	src := &DeploymentPackage{
		TestFiles:  make(map[string]string),
		BuildCmd:   []string{"go", "build"},
		BuildFiles: make(map[string]string),
	}
	result, err := dsor.makeDeploymentFile(string(deepseek_response), src)
	assert.Nil(t, err)
	assert.NotNil(t, result)

	assert.Contains(t, result.RootFile, "handle")
}
