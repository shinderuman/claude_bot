package bot

import (
	"context"
	"log"
	"math/rand"
	"time"
)

// executeStartupTasks executes necessary background tasks at startup with a random delay
// to avoid race conditions (especially for facts.json) when multiple bots are started.
//
// Targeted tasks:
// 1. Load Bot Profile (LoadBotProfile)
// 2. Discover Peers (DiscoverAndCollectPeerFacts)
// 3. Start Maintenance Loop (startFactMaintenanceLoop)
func (b *Bot) executeStartupTasks(ctx context.Context) {
	// Task definitions
	tasks := []func(context.Context){
		func(ctx context.Context) {
			if b.factService != nil {
				log.Println("起動時自己プロファイル読み込みを開始します...")
				if err := b.factService.LoadBotProfile(ctx); err != nil {
					log.Printf("起動時自己プロファイル読み込みエラー: %v", err)
				}
			}
		},
		func(ctx context.Context) {
			if b.factCollector != nil {
				log.Println("起動時Peer探索を開始します...")
				b.factCollector.DiscoverAndCollectPeerFacts(ctx)
			}
		},
		func(ctx context.Context) {
			// This starts a loop in a separate goroutine, so it returns immediately
			b.startFactMaintenanceLoop(ctx)
		},
	}

	// Calculate random delay (0 to StartupMaxDelay)
	// Using generic random seed logic for compatibility
	rand.Seed(time.Now().UnixNano())
	maxDelay := StartupMaxDelay

	go func() {
		runWithRandomDelay(ctx, maxDelay, tasks)
	}()
}

// runWithRandomDelay waits for a random duration up to maxDelay and then executes tasks sequentially.
// Since tasks are executed sequentially, if a task blocks, subsequent tasks will wait.
// However, startFactMaintenanceLoop launches its own goroutine, so it won't block.
func runWithRandomDelay(ctx context.Context, maxDelay time.Duration, tasks []func(context.Context)) {
	// Calculate delay
	delay := time.Duration(rand.Int63n(int64(maxDelay)))
	log.Printf("競合回避のため、バックグラウンド処理の開始を %v 遅延させます...", delay)

	select {
	case <-ctx.Done():
		return
	case <-time.After(delay):
	}

	// Execute tasks sequentially
	for _, task := range tasks {
		// Check context before each task
		if ctx.Err() != nil {
			return
		}
		task(ctx)
	}
}
