package pipeline

import (
	"errors"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	log "github.com/sirupsen/logrus"
)

// Stagnation-guard thresholds ([C5]): the number of consecutive times a task
// must fail with byte-identical error text before executeTask flags it as
// stagnant for the recovery prompt (2nd occurrence) or gives up on it
// entirely instead of invoking recovery again (3rd occurrence). Small,
// literal constants rather than task_args-configurable knobs: the behavior
// they encode ("the same fix attempt twice in a row is a bad sign, three
// times in a row means stop") is a property of LLM repair loops in general,
// not something a specific pipeline config should need to tune per task.
const (
	stagnationFlagThreshold  = 2
	stagnationAbortThreshold = 3
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
	if err := runner.Err(); err != nil {
		// runner's context was cancelled (e.g. via /stop/{uuid}): abort
		// immediately rather than starting another task, retry, or recovery.
		log.Debugf("aborting task (%s): %v", task.ID, err)
		return err
	}
	log.Debugf("starting task (%s)", task.ID)
	// Track which task is running so LLM calls and chatlogs can be
	// attributed to their stage (see Metrics.PerTask).
	req.CurrentTask = task.ID
	if req.Metrics != nil {
		req.Metrics.Tasks += 1
	}

	if task.CanApply != nil {
		if applyErr := task.CanApply.Apply(runner, req); applyErr != nil {
			log.Errorf("failed to apply task (%s): %s", task.ID, applyErr)
			return fmt.Errorf("task (%s) precondition failed - %v", task.ID, applyErr)
		}
	}

	var err error
	var workingPackage *domain.DeploymentPackage
	if task.Execute != nil {

		// Loop for retry attempts, executing the task and handling errors as needed.
		// MaxRetryCount is the max number of executions (not extra retries).
		log.Debugf("running task (%s) with (%d / %d) executions", task.ID, task.RetryCount+1, task.MaxRetryCount)
		for ; task.RetryCount < task.MaxRetryCount; task.RetryCount++ {
			if req.WorkingPackage != nil {
				workingPackage = req.WorkingPackage.Copy()
			}
			// Set fresh on every iteration (including after a recovery
			// sub-call recurses into executeTask and mutates req for its own
			// task), so it's always correct at the point Execute.Apply reads
			// it - no restore logic needed, unlike CurrentTask.
			req.CurrentAttempt = task.RetryCount + 1
			attemptStart := time.Now()
			err = task.Execute.Apply(runner, req)
			if req.Metrics != nil {
				req.Metrics.RecordTaskAttempt(task.ID, time.Since(attemptStart), err == nil)
			}
			if err == nil {
				log.Debugf("task (%s) executed successfully", task.ID)
				break
			}
			if cancelErr := runner.Err(); cancelErr != nil {
				// Don't retry or invoke recovery once cancelled - that would
				// spend more build/test/LLM resources on a stopped job.
				log.Debugf("task (%s) aborted: %v", task.ID, cancelErr)
				req.AddError(err)
				return cancelErr
			}
			log.Debugf("task (%s) retry (%d) failed - %s", task.ID, task.RetryCount, err)
			if task.RetryCount+1 < task.MaxRetryCount {
				log.Errorf("task (%s) retrying...", task.ID)

				var llmErr domain.LLMError
				if task.OnFailure != nil && errors.As(err, &llmErr) {
					// An LLM/infrastructure failure (API outage, rate limit,
					// truncation) has no code defect a recovery prompt could
					// fix - don't spend recovery tokens on it, just retry.
					log.Debugf("skipping recovery for task (%s): %v", task.ID, err)
					time.Sleep(task.RetryDelay)
				} else if task.OnFailure != nil {
					originalErr := err
					req.AddError(originalErr)

					// Stagnation guard ([C5]): a recovery task that is
					// itself succeeding (producing buildable code, say) but
					// not fixing whatever makes THIS task keep failing looks
					// identical to genuine progress from the retry loop's
					// point of view - it only sees "recovery returned nil,
					// continue". Comparing this failure's text against the
					// last one is what actually detects that nothing
					// changed.
					repeats := req.RecordFailure(task.ID, originalErr.Error())
					if repeats >= stagnationAbortThreshold {
						log.Warnf("task (%s) failed with the same error %d times in a row; the recovery task isn't changing the outcome, aborting instead of spending another attempt on it", task.ID, repeats)
						err = fmt.Errorf("repair loop for task (%s) made no progress after %d identical failures in a row, aborting: %w", task.ID, repeats, originalErr)
						break
					}
					if req.Metadata == nil {
						req.Metadata = make(map[string]string)
					}
					if repeats >= stagnationFlagThreshold {
						// One nudge before giving up: tell the recovery
						// prompt (via {{ .stagnant }}) that its last attempt
						// didn't change the outcome, so it has a chance to
						// try something other than a small variation of the
						// same fix before the abort threshold above fires.
						log.Debugf("task (%s) repeated the same failure; flagging stagnation for the recovery prompt", task.ID)
						req.Metadata["stagnant"] = "true"
					} else {
						delete(req.Metadata, "stagnant")
					}

					log.Debugf("attempting to recover task (%s) before retrying", task.ID)
					recoveryErr := p.executeTask(runner, req, task.OnFailure)
					// the recovery task overwrote CurrentTask; restore it for
					// this task's remaining attempts/validation
					req.CurrentTask = task.ID
					if recoveryErr == nil {
						log.Debugf("Retrying failed task (%s) after recovery", task.ID)
						continue
					}
					log.Debugf("Recovery failed.")
					// Join instead of letting recoveryErr replace originalErr:
					// otherwise LastError() (the fixer/align prompt's
					// {{ .issue }}) only ever sees "recovery also failed"
					// and never the code defect that triggered recovery in
					// the first place. errors.As still finds typed errors
					// (LLMError/TestingError/CompilationError) on either
					// side, since Join's Unwrap() []error is traversed by
					// errors.As/Is.
					err = errors.Join(originalErr, fmt.Errorf("recovery task (%s) also failed: %w", task.OnFailure.ID, recoveryErr))
					break
				} else {
					time.Sleep(task.RetryDelay)
				}
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
			log.Debugf("task (%s) failed. %+v", task.ID, err)
			req.AddError(err)
			return err
		}
	} else {
		log.Debugf("task (%s) is not an executable task. Skipping", task.ID)
	}
	// Perform validation if defined
	if task.Validation != nil {
		log.Debugf("performing validation task (%s)", task.ID)
		err = task.Validation.Apply(runner, req)
		if err != nil {
			log.Debugf("task validation for (%s) failed.", task.ID)
			req.AddError(err)
			if cancelErr := runner.Err(); cancelErr != nil {
				return cancelErr
			}
			if task.RetryCount < task.MaxRetryCount {
				task.RetryCount++
				return p.executeTask(runner, req, task)
			}
			return err
		}
		log.Debugf("task (%s) validated successfully", task.ID)
	}
	// Execute next tasks in the pipeline
	for _, next := range task.Next {
		if err := p.executeTask(runner, req, next); err != nil {
			req.AddError(err)
			return err
		}
	}

	return nil
}
