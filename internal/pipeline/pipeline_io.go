package pipeline

import (
	"fmt"
	"io"
	"maps"
	"time"

	"gopkg.in/yaml.v3"
)

// PipelineFile models the YAML structure used to describe pipelines. It is
// used standalone for on-disk pipeline files (e.g. default.yaml) and embedded
// directly into ConverterOptions so the /reconfigure JSON body carries
// options/tasks at the top level instead of under a nested "pipeline" key.
type PipelineFile struct {
	// Options holds pipeline-wide default params (model_name, temperature,
	// strategy, etc.). It is merged into every task's params at compile
	// time, before that task's own TaskArgs are applied on top.
	Options map[string]interface{} `json:"options" yaml:"options"`
	Tasks   []ConversionTaskStub   `json:"tasks" yaml:"tasks"`
}

// ConversionTaskStub is an intermediate representation used when assembling
// ConversionTask instances from YAML.
type ConversionTaskStub struct {
	ID   string `json:"id" yaml:"id"`
	Task string `json:"task" yaml:"task"`
	task Converter
	// TaskArgs overrides PipelineFile.Options for this task only. The merged
	// result (Options + TaskArgs) is what the task's converter — and, for
	// LLM tasks, the connector's Prepare — actually receives as its params.
	TaskArgs      map[string]interface{} `json:"task_args" yaml:"task_args"`
	CanApply      string                 `json:"canApply" yaml:"canApply"`
	canApply      Converter
	Validation    string `json:"validation" yaml:"validation"`
	validator     Converter
	Recovery      string `json:"recovery" yaml:"recovery"`
	onFailure     *ConversionTask
	MaxRetryCount int           `json:"maxRetryCount" yaml:"maxRetryCount"`
	RetryDelay    time.Duration `json:"retryDelay" yaml:"retryDelay"`
	Next          []string      `json:"next" yaml:"next"`
	next          []*ConversionTask
}

// canConvert reports whether the stub has been fully resolved into concrete
// converter instances and links.
func (c *ConversionTaskStub) canConvert() bool {
	if c.task == nil {
		return false
	}
	if c.CanApply != "" && c.canApply == nil {
		return false
	}

	if c.Validation != "" && c.validator == nil {
		return false
	}

	if c.Recovery != "" && c.onFailure == nil {
		return false
	}

	if len(c.Next) > 0 {
		return false
	}

	return true
}

// asConversionTask converts a resolved stub into a ConversionTask.
func (c *ConversionTaskStub) asConversionTask() ConversionTask {
	if !c.canConvert() {
		panic(fmt.Errorf("can not convert task '%s'", c.ID))
	}
	return ConversionTask{
		ID:            c.ID,
		Execute:       c.task,
		CanApply:      c.canApply,
		Validation:    c.validator,
		OnFailure:     c.onFailure,
		RetryCount:    0,
		MaxRetryCount: c.MaxRetryCount,
		RetryDelay:    c.RetryDelay,
		Next:          c.next,
	}
}

// PipelineReader parses a YAML pipeline description from file and compiles it
// into an executable Pipeline.
func PipelineReader(file io.Reader) (*Pipeline, error) {
	var fileContent PipelineFile
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(data, &fileContent); err != nil {
		return nil, err
	}

	return compilePipeline(fileContent)
}

func compilePipeline(fileContent PipelineFile) (*Pipeline, error) {
	pipelineMapping := make(map[string]ConversionTask)
	uncompletedTasks := make([]ConversionTaskStub, 0)
	for _, task := range fileContent.Tasks {
		// taskParams is this task's Execute converter's config: pipeline-wide
		// Options first, then this task's own TaskArgs override on top.
		taskParams := make(map[string]interface{})
		maps.Copy(taskParams, fileContent.Options)
		if task.TaskArgs != nil {
			maps.Copy(taskParams, task.TaskArgs)
		}
		taskImpl, err := MakeConverter(task.Task, taskParams)
		if err != nil {
			return nil, err
		}
		task.task = taskImpl

		// canApply/validation intentionally only see the pipeline-wide
		// Options, not this task's TaskArgs.
		apply, err := MakeConverter(task.CanApply, fileContent.Options)
		if err != nil {
			return nil, err
		}
		task.canApply = apply

		validation, err := MakeConverter(task.Validation, fileContent.Options)
		if err != nil {
			return nil, err
		}
		task.validator = validation

		if task.canConvert() {
			pipelineMapping[task.ID] = task.asConversionTask()
		} else {
			uncompletedTasks = append(uncompletedTasks, task)
		}
	}

	for len(uncompletedTasks) > 0 {
		remainingUncompletedTasks := make([]ConversionTaskStub, 0)

		for _, task := range uncompletedTasks {
			remaining := make([]string, 0)
			for _, next := range task.Next {
				if nextTask, ok := pipelineMapping[next]; ok {
					task.next = append(task.next, &nextTask)
				} else {
					remaining = append(remaining, next)
				}
			}

			if task.Recovery != "" {
				if onFailure, ok := pipelineMapping[task.Recovery]; ok {
					task.onFailure = &onFailure
				}
			}
			task.Next = remaining
			if task.canConvert() {
				pipelineMapping[task.ID] = task.asConversionTask()
			} else {
				remainingUncompletedTasks = append(remainingUncompletedTasks, task)
			}
		}
		uncompletedTasks = remainingUncompletedTasks
	}
	if root, ok := pipelineMapping["root"]; ok {
		return NewPipeline(&root), nil
	}
	return nil, fmt.Errorf("no root converter found")
}
