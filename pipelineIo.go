// Package main provides pipeline YAML parsing and conversion helpers
// used to compile a human-friendly pipeline description into an
// executable `Pipeline`.
package main

import (
	"fmt"
	"io"
	"maps"
	"time"

	"gopkg.in/yaml.v3"
)

// PipelineFile models the YAML structure used to describe pipelines.
type PipelineFile struct {
	DefaultOptions map[string]interface{} `json:"options" yaml:"options"`
	Tasks          []ConversionTaskStub   `json:"tasks" yaml:"tasks"`
}

// ConversionTaskStub is an intermediate representation used when
// assembling `ConversionTask` instances from YAML.
type ConversionTaskStub struct {
	ID            string `json:"id" yaml:"id"`
	Task          string `json:"task" yaml:"task"`
	task          Converter
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

// canConvert reports whether the stub has been fully resolved into
// concrete converter instances and links.
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

// asConversionTask converts a resolved stub into a `ConversionTask`.
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

// MakeConverter looks up a converter factory by `key` and builds a
// `Converter` using `args`.
func MakeConverter(key string, args map[string]interface{}) (Converter, error) {
	if key == "" {
		return nil, nil
	}
	if _, ok := ConverterFactories[key]; ok {
		return ConverterFactories[key](args), nil
	}
	return nil, fmt.Errorf("no converter found for key: %s", key)
}

// PipelineReader parses a YAML pipeline description from `file` and
// compiles it into an executable `Pipeline`.
func PipelineReader(file io.Reader) (*Pipeline, error) {
	var fileContent PipelineFile
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	err = yaml.Unmarshal(data, &fileContent)
	if err != nil {
		return nil, err
	}

	return compilePipeline(fileContent)
}

func compilePipeline(fileContent PipelineFile) (*Pipeline, error) {
	pipelineMapping := make(map[string]ConversionTask)
	uncompletedTasks := make([]ConversionTaskStub, 0)
	for _, task := range fileContent.Tasks {
		args := make(map[string]interface{})
		maps.Copy(args, fileContent.DefaultOptions)
		if task.TaskArgs != nil {
			maps.Copy(args, task.TaskArgs)
		}
		_task, err := MakeConverter(task.Task, args)
		if err != nil {
			return nil, err
		}
		task.task = _task

		_apply, err := MakeConverter(task.CanApply, fileContent.DefaultOptions)
		if err != nil {
			return nil, err
		}
		task.canApply = _apply

		_validation, err := MakeConverter(task.Validation, fileContent.DefaultOptions)
		if err != nil {
			return nil, err
		}
		task.validator = _validation

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
	} else {
		return nil, fmt.Errorf("no root converter found")
	}
}
