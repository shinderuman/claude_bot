package bot

import (
	"claude_bot/internal/config"
	"claude_bot/internal/slack"
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestBot() *Bot {
	return &Bot{
		config:      &config.Config{Timezone: "UTC"},
		slackClient: slack.NewClient("", "", "", ""),
	}
}

func TestBot_runInWindowedLoop_Basic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var executionCount int32
	interval := 50 * time.Millisecond

	task := func(ctx context.Context) {
		atomic.AddInt32(&executionCount, 1)
	}

	b := newTestBot()

	mockJitterZero := func(time.Duration) time.Duration { return 0 }

	b.runInWindowedLoop(ctx, interval, "BasicTest", task, mockJitterZero)

	time.Sleep(120 * time.Millisecond)
	cancel()

	count := atomic.LoadInt32(&executionCount)
	if count < 3 {
		t.Errorf("Expected at least 3 executions (Immediate + 2 Ticks), got %d", count)
	}
}

func TestBot_runInWindowedLoop_Cancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	var executionCount int32
	interval := 50 * time.Millisecond

	task := func(ctx context.Context) {
		atomic.AddInt32(&executionCount, 1)
	}

	b := newTestBot()
	mockJitterZero := func(time.Duration) time.Duration { return 0 }

	b.runInWindowedLoop(ctx, interval, "CancelTest", task, mockJitterZero)

	time.Sleep(10 * time.Millisecond)
	cancel()

	time.Sleep(100 * time.Millisecond)

	initialCount := atomic.LoadInt32(&executionCount)

	time.Sleep(100 * time.Millisecond)
	finalCount := atomic.LoadInt32(&executionCount)

	if finalCount != initialCount {
		t.Errorf("Loop continued after cancellation. Init: %d, Final: %d", initialCount, finalCount)
	}
}

func TestBot_runInWindowedLoop_Overrun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var executionCount int32
	interval := 50 * time.Millisecond

	task := func(ctx context.Context) {
		atomic.AddInt32(&executionCount, 1)
		time.Sleep(90 * time.Millisecond)
	}

	b := newTestBot()
	mockJitterZero := func(time.Duration) time.Duration { return 0 }
	b.runInWindowedLoop(ctx, interval, "OverrunTest", task, mockJitterZero)

	time.Sleep(220 * time.Millisecond)
	cancel()

	count := atomic.LoadInt32(&executionCount)
	if count < 4 {
		t.Errorf("Expected at least 4 overlapping executions, got %d", count)
	}
}

func TestBot_runInWindowedLoop_PanicRecovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var executionCount int32
	interval := 50 * time.Millisecond

	wg := sync.WaitGroup{}
	wg.Add(3)

	task := func(ctx context.Context) {
		current := atomic.AddInt32(&executionCount, 1)
		wg.Done()

		if current == 2 {
			panic("simulated panic")
		}
	}

	b := newTestBot()
	mockJitterZero := func(time.Duration) time.Duration { return 0 }

	b.runInWindowedLoop(ctx, interval, "PanicTest", task, mockJitterZero)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Errorf("Timed out waiting for executions. Count: %d", atomic.LoadInt32(&executionCount))
	}

	count := atomic.LoadInt32(&executionCount)
	if count < 3 {
		t.Errorf("Loop died after panic? Count: %d", count)
	}
}

func TestBot_runInWindowedLoop_CancelDuringRandomWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	var executionCount int32
	interval := 100 * time.Millisecond

	mockJitterLarge := func(time.Duration) time.Duration { return 90 * time.Millisecond }

	task := func(ctx context.Context) {
		atomic.AddInt32(&executionCount, 1)
	}

	b := newTestBot()
	b.runInWindowedLoop(ctx, interval, "RandomWaitCancel", task, mockJitterLarge)

	time.Sleep(40 * time.Millisecond)
	cancel()

	time.Sleep(100 * time.Millisecond)

	count := atomic.LoadInt32(&executionCount)
	if count > 0 {
		t.Errorf("Task ran despite cancellation during random wait! Count: %d", count)
	}
}
