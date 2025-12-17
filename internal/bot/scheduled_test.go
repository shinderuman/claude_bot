package bot

import (
	"context"
	"testing"
	"time"
)

func TestBot_runInWindowedLoop_Basic(t *testing.T) {
	// Setup
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	executionCount := 0
	// 50ms interval.
	interval := 50 * time.Millisecond

	task := func(ctx context.Context) {
		executionCount++
	}

	b := &Bot{}

	// Mock jitter: usually 0 for predictable timing
	mockJitterZero := func(time.Duration) time.Duration { return 0 }

	// Run
	b.runInWindowedLoop(ctx, interval, "BasicTest", task, mockJitterZero)

	// Wait for approx 3 intervals (150ms) + buffer
	time.Sleep(180 * time.Millisecond)
	cancel()

	// Verify
	if executionCount < 3 {
		t.Errorf("Expected at least 3 executions, got %d", executionCount)
	}
}

func TestBot_runInWindowedLoop_Cancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	executionCount := 0
	interval := 50 * time.Millisecond // fast interval

	task := func(ctx context.Context) {
		executionCount++
	}

	b := &Bot{}
	mockJitterZero := func(time.Duration) time.Duration { return 0 }
	b.runInWindowedLoop(ctx, interval, "CancelTest", task, mockJitterZero)

	// Cancel immediately (or very shortly)
	time.Sleep(10 * time.Millisecond)
	cancel()

	// Wait a bit to ensure it doesn't run anymore
	time.Sleep(100 * time.Millisecond)

	// Should run at most once (if generic ticker fired immediately) or 0
	// In our logic, it waits `randomMinutes` first. Since interval is small, random is 0.
	// But it calls `windowStart.Add(...)`.
	// If we cancel before the loop logic proceeds, it returns.

	// Just ensure it stopped. count shouldn't increase after cancellation.
	initialCount := executionCount
	time.Sleep(100 * time.Millisecond)
	finalCount := executionCount

	if finalCount != initialCount {
		t.Errorf("Loop continued after cancellation. Init: %d, Final: %d", initialCount, finalCount)
	}
}

func TestBot_runInWindowedLoop_Overrun(t *testing.T) {
	// Task takes longer than interval
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	executionCount := 0
	interval := 50 * time.Millisecond

	task := func(ctx context.Context) {
		executionCount++
		// Sleep longer than interval to force overrun
		time.Sleep(100 * time.Millisecond)
	}

	b := &Bot{}
	mockJitterZero := func(time.Duration) time.Duration { return 0 }
	b.runInWindowedLoop(ctx, interval, "OverrunTest", task, mockJitterZero)

	// Wait for enough time for 2 executions (2 * 100ms = 200ms)
	// Interval is 50ms.
	// T0: Start task (takes 100ms). Ends at T100.
	// Window1 (T50) passed. Window2 (T100) starts.
	// Logic: windowStart adds 50ms -> T50. Now(T100) > T50. Proceed immediately.

	time.Sleep(250 * time.Millisecond)
	cancel()

	// Should have executed roughly 2 times.
	if executionCount < 2 {
		t.Errorf("Expected at least 2 executions (catch-up), got %d", executionCount)
	}
}

func TestBot_runInWindowedLoop_PanicRecovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	executionCount := 0
	interval := 50 * time.Millisecond

	task := func(ctx context.Context) {
		executionCount++
		if executionCount == 2 {
			panic("simulated panic")
		}
	}

	b := &Bot{}
	mockJitterZero := func(time.Duration) time.Duration { return 0 }

	// Run
	b.runInWindowedLoop(ctx, interval, "PanicTest", task, mockJitterZero)

	// Allow enough time for 3 or 4 executions
	// 1st: OK
	// 2nd: Panic (Should recover)
	// 3rd: OK (Should run even after panic)
	time.Sleep(200 * time.Millisecond)

	if executionCount < 3 {
		t.Errorf("Loop died after panic? Count: %d (expected >= 3)", executionCount)
	}
}

func TestBot_runInWindowedLoop_CancelDuringRandomWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	executionCount := 0
	interval := 100 * time.Millisecond

	// Force a large jitter: 90ms.
	// Scheduled time = windowStart + 90ms.
	// If we cancel at 40ms, the task should NEVER run.
	mockJitterLarge := func(time.Duration) time.Duration { return 90 * time.Millisecond }

	task := func(ctx context.Context) {
		executionCount++
	}

	b := &Bot{}
	b.runInWindowedLoop(ctx, interval, "RandomWaitCancel", task, mockJitterLarge)

	time.Sleep(40 * time.Millisecond)
	cancel()

	// Wait past the scheduled time (90ms)
	time.Sleep(100 * time.Millisecond)

	if executionCount > 0 {
		t.Errorf("Task ran despite cancellation during random wait! Count: %d", executionCount)
	}
}
