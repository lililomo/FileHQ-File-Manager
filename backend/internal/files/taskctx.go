package files

import (
	"context"
	"time"
)

// Reporter receives progress from long operations run as tasks (Step 11).
type Reporter interface {
	Add(delta int64)
	SetTotal(total int64)
	Log(level, format string, args ...any)
}

// TaskContext switches an operation into task mode: no synchronous
// limits or walk budgets, locks are awaited instead of failing fast, no
// undo token, and progress goes to Reporter.
type TaskContext struct {
	Reporter Reporter
	// ItemDelay slows per-item work (development demos only).
	ItemDelay time.Duration
}

type taskCtxKey struct{}

// WithTask marks ctx as running inside a task.
func WithTask(ctx context.Context, tc *TaskContext) context.Context {
	return context.WithValue(ctx, taskCtxKey{}, tc)
}

func taskOf(ctx context.Context) *TaskContext {
	tc, _ := ctx.Value(taskCtxKey{}).(*TaskContext)
	return tc
}

func unbounded(ctx context.Context) bool { return taskOf(ctx) != nil }

func reporterOf(ctx context.Context) Reporter {
	if tc := taskOf(ctx); tc != nil {
		return tc.Reporter
	}
	return nil
}

// itemPause applies the development item delay and checks ctx.
func itemPause(ctx context.Context) error {
	tc := taskOf(ctx)
	if tc == nil || tc.ItemDelay <= 0 {
		return ctx.Err()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(tc.ItemDelay):
		return nil
	}
}
