package bot

import (
	"context"
	"log"
	"time"
)

func (b *Bot) executeStartupTasks(ctx context.Context, instanceID int) {
	lightTasks, heavyTasks := b.prepareStartupTasks()

	lightDelay := time.Duration(instanceID) * StartupInitSlotDuration
	if lightDelay > 0 {
		log.Printf("起動時初期処理まで: %s", lightDelay)
	}
	go runWithDelay(ctx, lightDelay, lightTasks)

	if len(heavyTasks) > 0 {
		heavyDelay := time.Duration(instanceID) * StartupMaintenanceSlotDuration
		if heavyDelay > 0 {
			log.Printf("起動時メンテナンスまで: %s", heavyDelay)
		}
		go runWithDelay(ctx, heavyDelay, heavyTasks)
	}
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
				expired, overLimit, err := b.factStore.PerformMaintenance(ctx, b.config.FactRetentionDays, b.config.MaxFacts)
				if err != nil {
					log.Printf("起動時ファクト整理失敗: 期限切れ%d件・上限超過%d件削除、%v", expired, overLimit, err)
					return
				}
				log.Printf("起動時ファクト整理: 期限切れ%d件・上限超過%d件削除", expired, overLimit)
			}
		},
		func(ctx context.Context) {
			b.startFactMaintenanceLoop(ctx)
		},
	}

	heavyTasks := []func(context.Context){}

	if b.config.EnableFactStore && b.config.RunMaintenanceOnStartup {
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
