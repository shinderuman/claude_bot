package bot

import (
	"context"
	"log"
	"time"
)

func (b *Bot) executeStartupTasks(ctx context.Context, instanceID int) {
	lightTasks, heavyTasks := b.prepareStartupTasks()

	lightDelay := time.Duration(instanceID) * StartupInitSlotDuration
	go runWithDelay(ctx, lightDelay, lightTasks)

	heavyDelay := time.Duration(instanceID) * StartupMaintenanceSlotDuration
	go runWithDelay(ctx, heavyDelay, heavyTasks)
}

func (b *Bot) prepareStartupTasks() ([]func(context.Context), []func(context.Context)) {
	lightTasks := []func(context.Context){
		func(ctx context.Context) {
			if b.factCollector != nil {
				b.factCollector.DiscoverAndCollectPeerFacts(ctx)
			}
		},
		func(ctx context.Context) {
			if b.factStore != nil {
				deleted, err := b.factStore.PerformMaintenance(ctx, b.config.FactRetentionDays, b.config.MaxFacts)
				if err != nil {
					log.Printf("起動時ファクト整理失敗: %d件削除済み、%v", deleted, err)
					return
				}
				log.Printf("起動時ファクト整理完了: %d件削除", deleted)
			}
		},
		func(ctx context.Context) {
			b.startFactMaintenanceLoop(ctx)
		},
	}

	heavyTasks := []func(context.Context){}

	if b.config.RunMaintenanceOnStartup {
		heavyTasks = append(heavyTasks, func(ctx context.Context) {
			if b.factService != nil {
				if err := b.factService.PerformMaintenance(ctx); err != nil {
					log.Printf("起動時ファクトメンテナンス失敗: %v", err)
				}
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
