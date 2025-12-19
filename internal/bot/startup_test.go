package bot

import (
	"context"
	"testing"
	"time"
)

func TestRunWithDelay_Execution(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	executedCount := 0
	tasks := []func(context.Context){
		func(ctx context.Context) { executedCount++ },
		func(ctx context.Context) { executedCount++ },
		func(ctx context.Context) { executedCount++ },
	}

	// Use a very small delay for testing
	delay := 10 * time.Millisecond

	runWithDelay(ctx, delay, tasks)

	if executedCount != 3 {
		t.Errorf("Expected 3 tasks to be executed, got %d", executedCount)
	}
}

func TestRunWithDelay_Cancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	executedCount := 0
	tasks := []func(context.Context){
		func(ctx context.Context) { executedCount++ },
	}

	// Use a large delay
	delay := 500 * time.Millisecond

	// Run in goroutine because it blocks
	done := make(chan struct{})
	go func() {
		runWithDelay(ctx, delay, tasks)
		close(done)
	}()

	// Cancel immediately
	cancel()

	// Wait for return
	select {
	case <-done:
		// success
	case <-time.After(1 * time.Second):
		t.Fatal("runWithDelay did not return after cancellation")
	}

	// Should not have executed tasks
	if executedCount != 0 {
		t.Errorf("Expected 0 tasks to be executed due to cancellation, got %d", executedCount)
	}
}

func TestRunWithDelay_ContextCancelBetweenTasks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	executedCount := 0
	tasks := []func(context.Context){
		func(ctx context.Context) {
			executedCount++
			// Cancel context after first task
			cancel()
		},
		func(ctx context.Context) { executedCount++ },
	}

	// Small delay
	delay := 1 * time.Millisecond

	runWithDelay(ctx, delay, tasks)

	if executedCount != 1 {
		t.Errorf("Expected only 1 task to be executed, got %d", executedCount)
	}
}
