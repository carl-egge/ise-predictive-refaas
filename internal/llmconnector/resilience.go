package llmconnector

import (
	"context"
	"net/http"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// maxLLMAttempts bounds connector-level retries for transient API failures
// (connection errors, 429s, 5xx). These retries are deliberately separate
// from task-level retries: an infrastructure blip must not consume a task's
// retry budget or trigger an LLM recovery prompt (see TODO.md [F2]).
const maxLLMAttempts = 3

// retryBaseDelay is the first backoff step; it doubles per attempt. A
// package variable so tests can shrink it.
var retryBaseDelay = time.Second

// transientHTTPStatus reports whether an HTTP status is worth retrying:
// rate limits and server-side errors, never client errors.
func transientHTTPStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

// sleepBackoff waits retryBaseDelay << attempt, aborting early when ctx ends
// (e.g. the job was cancelled via /stop while backing off).
func sleepBackoff(ctx context.Context, attempt int) error {
	timer := time.NewTimer(retryBaseDelay << attempt)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// callThrottle implements the global minimum delay between LLM calls
// (TODO.md [F5]): a single shared limiter across all connectors and jobs so
// batch experiments with many retries stay under provider rate limits.
// Slots are reserved under the lock, so even concurrent callers space out.
var callThrottle = struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
}{}

// ConfigureThrottle sets the global minimum interval between LLM calls from
// the connector args (key LLM_CALL_INTERVAL; a duration string like "2s" or
// "500ms", or a bare JSON number meaning seconds). Zero or empty disables
// throttling. Called by the pipeline whenever a Runner is built or
// reconfigured, so the env value is re-read like every other Args default.
func ConfigureThrottle(connectorArgs map[string]interface{}) {
	interval := time.Duration(0)
	switch v := connectorArgs["LLM_CALL_INTERVAL"].(type) {
	case string:
		if v != "" {
			if d, err := time.ParseDuration(v); err != nil {
				log.Warnf("ignoring invalid LLM_CALL_INTERVAL %q: %v", v, err)
			} else {
				interval = d
			}
		}
	case float64: // JSON number in a /reconfigure body
		interval = time.Duration(v * float64(time.Second))
	case int:
		interval = time.Duration(v) * time.Second
	}
	if interval < 0 {
		interval = 0
	}

	callThrottle.mu.Lock()
	callThrottle.interval = interval
	callThrottle.mu.Unlock()

	if interval > 0 {
		log.Infof("LLM call throttle enabled: minimum %s between calls", interval)
	}
}

// waitForCallSlot reserves the next call slot and blocks until it arrives.
// A no-op when throttling is disabled; aborts early when ctx is cancelled.
func waitForCallSlot(ctx context.Context) error {
	callThrottle.mu.Lock()
	if callThrottle.interval <= 0 {
		callThrottle.mu.Unlock()
		return nil
	}
	now := time.Now()
	slot := callThrottle.next
	if slot.Before(now) {
		slot = now
	}
	callThrottle.next = slot.Add(callThrottle.interval)
	callThrottle.mu.Unlock()

	wait := time.Until(slot)
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
