package bot

import (
	"claude_bot/internal/config"
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

	delay := 500 * time.Millisecond

	done := make(chan struct{})
	go func() {
		runWithDelay(ctx, delay, tasks)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("runWithDelay did not return after cancellation")
	}

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
			cancel()
		},
		func(ctx context.Context) { executedCount++ },
	}

	delay := 1 * time.Millisecond

	runWithDelay(ctx, delay, tasks)

	if executedCount != 1 {
		t.Errorf("Expected only 1 task to be executed, got %d", executedCount)
	}
}

func TestPrepareStartupTasks_Maintenance(t *testing.T) {
	b1 := &Bot{
		config: &config.Config{
			EnableFactStore:         true,
			RunMaintenanceOnStartup: false,
		},
	}
	_, heavyTasks1 := b1.prepareStartupTasks()
	if len(heavyTasks1) != 0 {
		t.Errorf("Expected 0 heavy tasks when maintenance is disabled, got %d", len(heavyTasks1))
	}

	b2 := &Bot{
		config: &config.Config{
			EnableFactStore:         true,
			RunMaintenanceOnStartup: true,
		},
	}
	_, heavyTasks2 := b2.prepareStartupTasks()
	if len(heavyTasks2) != 1 {
		t.Errorf("Expected 1 heavy task when maintenance is enabled, got %d", len(heavyTasks2))
	}
	b2.config.EnableFactStore = false
	if _, tasks := b2.prepareStartupTasks(); len(tasks) != 0 {
		t.Errorf("scheduled %d heavy tasks with the fact store disabled", len(tasks))
	}
}
