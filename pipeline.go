package main

import (
	"fmt"
	"runtime/debug"
	"time"

	log "github.com/sirupsen/logrus"
)

// NewPipeline initializes a new pipeline
func NewPipeline(firstTask *ConversionTask) *Pipeline {
	return &Pipeline{FirstTask: firstTask}
}

// Execute runs the pipeline
func (p *Pipeline) Execute(runner *PipelineRunner, req *ConversionRequest) (out error) {
	err := p.reset()
	if err != nil {
		return err
	}
	req.Metrics.StartTime = time.Now()
	defer func() {
		req.Metrics.EndTime = time.Now()
		req.Metrics.TotalTime = req.Metrics.EndTime.Sub(req.Metrics.StartTime)
	}()
	defer func() {
		if err := recover(); err != nil {
			log.Errorf("pipline execution panic: %v", err)
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
		err := p.resetTask(task.OnFailure)
		if err != nil {
			return err
		}
	}

	for _, next := range task.Next {
		err := p.resetTask(next)
		if err != nil {
			return err
		}
	}

	return nil

}

// executeTask runs an individual task with retry logic and failure handling
func (p *Pipeline) executeTask(runner *PipelineRunner, req *ConversionRequest, task *ConversionTask) error {
	if task == nil {
		log.Debugf("Task is nil. Skipping")
		return nil
	}
	log.Debugf("starting %s", task.ID)
	req.Metrics.Tasks += 1

	if task.CanApply != nil {
		if applyErr := task.CanApply.Apply(runner, req); applyErr != nil {
			log.Errorf("failed to apply task %s: %s", task.ID, applyErr)
			return fmt.Errorf("task %s precondition failed - %v", task.ID, applyErr)
		}
	}

	var err error
	var workingPackage *DeploymentPackage = nil
	if task.Execute != nil {
		log.Debugf("Running task %s with (%d - %d) executions", task.ID, task.RetryCount, task.MaxRetryCount)
		for ; task.RetryCount < task.MaxRetryCount; task.RetryCount++ {
			if req.WorkingPackage != nil {
				workingPackage = req.WorkingPackage.copy()
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
					req.err = append(req.err, err)
					log.Debugf("atempting to recover task %s before retrying", task.ID)
					err = p.executeTask(runner, req, task.OnFailure)
					if err == nil {
						// Continue to next retry attempt of TaskB without exceeding max retries
						log.Debugf("Retrying failed task %s after recovery", task.ID)
						continue
					} else {
						log.Debugf("Recovery failed.")
						break
					}
				}
				time.Sleep(task.RetryDelay)
			}
			//recover working package
			if req.WorkingPackage != nil && task.CanApply != nil {
				err := task.CanApply.Apply(runner, req)
				if err != nil {
					log.Errorf("the task coruppted the working package, recovering latest version.")
					if workingPackage != nil {
						req.WorkingPackage = workingPackage
					}
				}
			} else if req.WorkingPackage == nil && workingPackage != nil {
				log.Debugf("the task coruppted the working package, recovering latest version.")
				req.WorkingPackage = workingPackage
			}
		}

		if err != nil {
			log.Debugf("task %s failed. %+v", task.ID, err)
			req.err = append(req.err, err)
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
			req.err = append(req.err, err)
			if task.RetryCount < task.MaxRetryCount {
				task.RetryCount++
				return p.executeTask(runner, req, task)
			} else {
				return err
			}
		}
	}
	log.Debugf("task %s executed successfully", task.ID)
	// Execute next tasks
	for _, next := range task.Next {
		if err := p.executeTask(runner, req, next); err != nil {
			req.err = append(req.err, err)
			return err
		}
	}

	return nil
}
