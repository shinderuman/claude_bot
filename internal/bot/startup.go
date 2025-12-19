package bot

import (
	"claude_bot/internal/discovery"
	"context"
	"log"
	"time"
)

// executeStartupTasks executes necessary background tasks at startup
// to avoid race conditions and ensure data consistency.
//
// Targeted tasks:
// 1. Lightweight: Reload, Load Profile, Discovery (Staggered by 1m)
// 2. Heavy: Compression, Maintenance Loop (Staggered by 10m)
func (b *Bot) executeStartupTasks(ctx context.Context) {
	// Record start time to identify "new" facts collected during the delay
	bootTime := time.Now()

	// Calculate delay based on cluster position (Deterministic Slot)
	instanceID, totalInstances, err := discovery.GetMyPosition(b.config.BotUsername)
	if err != nil {
		// クラスタ位置特定に失敗した場合は、起動順序が保証できないため起動を中止する
		log.Fatalf("[Startup] Critical Error: Failed to get cluster position for delay: %v. Cannot proceed safely.", err)
	}

	log.Printf("[Startup] Cluster Size: %d instances. Position: %d", totalInstances, instanceID)

	// 1. Lightweight Tasks
	lightTasks := []func(context.Context){
		func(ctx context.Context) {
			if b.factStore != nil {
				log.Println("遅延待機終了: 最新のファクトデータをディスクから再読み込みします...")
				if err := b.factStore.Reload(bootTime); err != nil {
					log.Printf("ファクトデータの再読み込みエラー: %v", err)
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
			if b.factStore != nil {
				log.Println("起動時ファクトクリーンアップ（物理整理）を実行します...")
				deleted := b.factStore.PerformMaintenance(b.config.FactRetentionDays, b.config.MaxFacts)
				log.Printf("起動時クリーンアップ完了: %d件削除", deleted)
			}
		},
		func(ctx context.Context) {
			if b.factService != nil {
				log.Println("起動時自己プロファイル読み込みを開始します...")
				if err := b.factService.LoadBotProfile(ctx); err != nil {
					log.Printf("起動時自己プロファイル読み込みエラー: %v", err)
				}
			}
		},
	}

	// 2. Heavy Tasks
	heavyTasks := []func(context.Context){
		func(ctx context.Context) {
			if b.factService != nil {
				log.Println("起動時ファクトアーカイブ（抽出・圧縮）を実行します...")
				if err := b.factService.PerformMaintenance(ctx); err != nil {
					log.Printf("起動時アーカイブエラー: %v", err)
				}
				log.Println("起動時アーカイブ完了")
			}
		},
		func(ctx context.Context) {
			b.startFactMaintenanceLoop(ctx)
		},
	}

	// Schedule Light Tasks
	lightDelay := time.Duration(instanceID) * StartupInitSlotDuration
	log.Printf("[Startup] Light Tasks Scheduled: Instance %d, Delay %v", instanceID, lightDelay)
	go runWithDelay(ctx, lightDelay, lightTasks)

	// Schedule Heavy Tasks
	heavyDelay := time.Duration(instanceID) * StartupMaintenanceSlotDuration
	log.Printf("[Startup] Heavy Tasks Scheduled: Instance %d, Delay %v", instanceID, heavyDelay)
	go runWithDelay(ctx, heavyDelay, heavyTasks)
}

// runWithDelay waits for the specified duration and then executes tasks sequentially.
func runWithDelay(ctx context.Context, delay time.Duration, tasks []func(context.Context)) {
	if delay > 0 {
		log.Printf("競合回避のため、バックグラウンド処理の開始を %v 待機します...", delay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	} else {
		log.Printf("待機時間なしでバックグラウンド処理を開始します...")
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
