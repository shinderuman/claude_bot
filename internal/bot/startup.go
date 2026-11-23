package bot

import (
	"claude_bot/internal/discovery"
	"context"
	"log"
	"time"
)

func (b *Bot) executeStartupTasks(ctx context.Context) {
	instanceID, totalInstances, err := discovery.GetMyPosition(b.config.BotUsername)
	if err != nil {
		log.Fatalf("[Startup] Critical Error: Failed to get cluster position for delay: %v. Cannot proceed safely.", err)
	}

	log.Printf("[Startup] Cluster Size: %d instances. Position: %d", totalInstances, instanceID)

	lightTasks, heavyTasks := b.prepareStartupTasks()

	lightDelay := time.Duration(instanceID) * StartupInitSlotDuration
	log.Printf("[Startup] 競合回避のため、Lightタスクの開始を %v 待機します (Instance: %d)", lightDelay, instanceID)
	go runWithDelay(ctx, lightDelay, lightTasks)

	heavyDelay := time.Duration(instanceID) * StartupMaintenanceSlotDuration
	log.Printf("[Startup] 競合回避のため、Heavyタスクの開始を %v 待機します (Instance: %d)", heavyDelay, instanceID)
	go runWithDelay(ctx, heavyDelay, heavyTasks)
}

func (b *Bot) prepareStartupTasks() ([]func(context.Context), []func(context.Context)) {
	lightTasks := []func(context.Context){
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
			b.startFactMaintenanceLoop(ctx)
		},
	}

	heavyTasks := []func(context.Context){}

	if b.config.RunMaintenanceOnStartup {
		heavyTasks = append(heavyTasks, func(ctx context.Context) {
			log.Println("[Startup] メンテナンスモードが有効です。ファクトメンテナンスを実行します...")
			if b.factService != nil {
				b.factService.PerformMaintenance(ctx)
			}
		})
	}

	return lightTasks, heavyTasks
}

func runWithDelay(ctx context.Context, delay time.Duration, tasks []func(context.Context)) {
	if delay > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}

	for _, task := range tasks {
		if ctx.Err() != nil {
			return
		}
		task(ctx)
	}
}
