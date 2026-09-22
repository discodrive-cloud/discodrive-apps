package engine

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Activity is an in-memory session snapshot, independent of optional diagnostic logs.
// Counts describe operations, not unique files: a retry can process a node again.
type Activity struct {
	Phase     string          `json:"phase"`
	Path      string          `json:"path"`
	Completed int64           `json:"completed"`
	Errors    []ActivityError `json:"errors"`
}
type ActivityError struct {
	Path    string    `json:"path"`
	Message string    `json:"message"`
	Time    time.Time `json:"time"`
}
type activityTracker struct {
	mu       sync.Mutex
	snapshot Activity
}

func (e *Engine) Activity() Activity {
	e.activity.mu.Lock()
	defer e.activity.mu.Unlock()
	out := e.activity.snapshot
	out.Errors = append([]ActivityError{}, out.Errors...)
	return out
}
func (e *Engine) activityBegin(phase, path string) {
	e.activity.mu.Lock()
	defer e.activity.mu.Unlock()
	e.activity.snapshot.Phase = phase
	e.activity.snapshot.Path = path
}
func (e *Engine) activityEnd(err error) {
	e.activity.mu.Lock()
	defer e.activity.mu.Unlock()
	a := &e.activity.snapshot
	if a.Phase == "" {
		return
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		// Bound memory and collapse repeated failures for the same path.
		recent := a.Errors[:0]
		for _, entry := range a.Errors {
			if entry.Path != a.Path {
				recent = append(recent, entry)
			}
		}
		a.Errors = append(recent, ActivityError{Path: a.Path, Message: err.Error(), Time: time.Now()})
		if len(a.Errors) > 20 {
			a.Errors = append([]ActivityError{}, a.Errors[len(a.Errors)-20:]...)
		}
	} else if err == nil && a.Path != "" {
		a.Completed++
	}
	a.Phase = ""
	a.Path = ""
}
