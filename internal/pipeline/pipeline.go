package pipeline

import (
	"fmt"
	"runtime/debug"
	"time"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	log "github.com/sirupsen/logrus"
)

// ConversionTask represents a step in the pipeline, including retry behavior
// and recovery links.
type ConversionTask struct {
	ID            string
	Execute       Converter         // Task execution function
	CanApply      Converter         // Checks preconditions
	RetryCount    int               // Retry attempts
	MaxRetryCount int               // Max retries
	RetryDelay    time.Duration     // Delay between retries
	Next          []*ConversionTask // Next tasks (normal execution flow)
	OnFailure     *ConversionTask   // Recovery task if this task fails
	Validation    Converter
}

// Pipeline represents a sequence of ConversionTask steps with a defined root.
type Pipeline struct {
	FirstTask *ConversionTask
}

// NewPipeline initializes a new pipeline with firstTask as the root node.
func NewPipeline(firstTask *ConversionTask) *Pipeline {
	return &Pipeline{FirstTask: firstTask}
}

// Execute runs the pipeline against the provided ConversionRequest, measuring
// timings and recovering from panics into an error result.
func (p *Pipeline) Execute(runner *Runner, req *domain.ConversionRequest) (out error) {
	if err := p.reset(); err != nil {
		return err
	}
	if req.Metrics != nil {
		req.Metrics.StartTime = time.Now()
		defer func() {
			req.Metrics.EndTime = time.Now()
			req.Metrics.TotalTime = req.Metrics.EndTime.Sub(req.Metrics.StartTime)
		}()
	}
	defer func() {
		if err := recover(); err != nil {
			log.Errorf("pipeline execution panic: %v", err)
			out = fmt.Errorf("%v\n%s", err, string(debug.Stack()))
		}
	}()
	out = p.executeTask(runner, req, p.FirstTask)
	return out
}

func (p *Pipeline) reset() error {
	return p.resetTask(p.FirstTask)
}

func (p *Pipeline) resetTask(task *ConversionTask) error {
	if task == nil {
		return nil
	}

	task.RetryCount = 0

	if task.OnFailure != nil {
		if err := p.resetTask(task.OnFailure); err != nil {
			return err
		}
	}

	for _, next := range task.Next {
		if err := p.resetTask(next); err != nil {
			return err
		}
	}

	return nil
}

// executeTask runs an individual task with retry logic and failure handling.
func (p *Pipeline) executeTask(runner *Runner, req *domain.ConversionRequest, task *ConversionTask) error {
	if task == nil {
		log.Debugf("Task is nil. Skipping")
		return nil
	}
	log.Debugf("starting %s", task.ID)
	if req.Metrics != nil {
		req.Metrics.Tasks += 1
	}

	if task.CanApply != nil {
		if applyErr := task.CanApply.Apply(runner, req); applyErr != nil {
			log.Errorf("failed to apply task %s: %s", task.ID, applyErr)
			return fmt.Errorf("task %s precondition failed - %v", task.ID, applyErr)
		}
	}

	var err error
	var workingPackage *domain.DeploymentPackage
	if task.Execute != nil {
		log.Debugf("Running task %s with (%d - %d) executions", task.ID, task.RetryCount, task.MaxRetryCount)
		for ; task.RetryCount < task.MaxRetryCount; task.RetryCount++ {
			if req.WorkingPackage != nil {
				workingPackage = req.WorkingPackage.Copy()
			}
			err = task.Execute.Apply(runner, req)
			if err == nil {
				log.Debugf("task %s executed successfully", task.ID)
				break
			}
			log.Debugf("task %s retry (%d) failed - %s", task.ID, task.RetryCount, err)
			if task.RetryCount+1 < task.MaxRetryCount {
				log.Errorf("task %s retrying...", task.ID)

				if task.OnFailure != nil {
					req.AddError(err)
					log.Debugf("attempting to recover task %s before retrying", task.ID)
					err = p.executeTask(runner, req, task.OnFailure)
					if err == nil {
						log.Debugf("Retrying failed task %s after recovery", task.ID)
						continue
					}
					log.Debugf("Recovery failed.")
					break
				}
				time.Sleep(task.RetryDelay)
			}
			if req.WorkingPackage != nil && task.CanApply != nil {
				if err := task.CanApply.Apply(runner, req); err != nil {
					log.Errorf("the task corrupted the working package, recovering latest version.")
					if workingPackage != nil {
						req.WorkingPackage = workingPackage
					}
				}
			} else if req.WorkingPackage == nil && workingPackage != nil {
				log.Debugf("the task corrupted the working package, recovering latest version.")
				req.WorkingPackage = workingPackage
			}
		}

		if err != nil {
			log.Debugf("task %s failed. %+v", task.ID, err)
			req.AddError(err)
			return err
		}
	} else {
		log.Debugf("task is not an executable task. Skipping")
	}

	if task.Validation != nil {
		log.Debugf("performing validation task %s", task.ID)
		err = task.Validation.Apply(runner, req)
		if err != nil {
			log.Debugf("task validation for %s failed.", task.ID)
			req.AddError(err)
			if task.RetryCount < task.MaxRetryCount {
				task.RetryCount++
				return p.executeTask(runner, req, task)
			}
			return err
		}
	}
	log.Debugf("task %s executed successfully", task.ID)
	for _, next := range task.Next {
		if err := p.executeTask(runner, req, next); err != nil {
			req.AddError(err)
			return err
		}
	}

	return nil
}
