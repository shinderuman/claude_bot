package bot

import (
	"claude_bot/internal/llm"
	"claude_bot/internal/model"
	"context"
	"fmt"
	"log"
	"time"
)

func (b *Bot) startFactMaintenanceLoop(ctx context.Context) {
	if b.factStore == nil {
		return
	}

	jitterFunc := func(interval time.Duration) time.Duration {
		minutes := int64(interval.Minutes())
		if minutes > 0 {
			return time.Duration(time.Now().UnixNano()%minutes) * time.Minute
		}
		return 0
	}
	interval := time.Duration(b.config.FactMaintenanceIntervalHours) * time.Hour
	b.runInWindowedLoop(ctx, interval, "ファクトメンテナンス", func(ctx context.Context) {
		log.Println("ファクトメンテナンスを実行中...")
		if err := b.factService.PerformMaintenance(ctx); err != nil {
			log.Printf("ファクトメンテナンスエラー: %v", err)
		}

		if b.factCollector != nil {
			log.Println("Peer探索を実行中...")
			b.factCollector.DiscoverAndCollectPeerFacts(ctx)
		}
	}, jitterFunc, true)
}

func (b *Bot) startAutoPostLoop(ctx context.Context) {
	if b.config.AutoPostIntervalHours <= 0 {
		return
	}

	interval := time.Duration(b.config.AutoPostIntervalHours) * time.Hour
	jitterFunc := func(interval time.Duration) time.Duration {
		minutes := int64(interval.Minutes())
		if minutes > 0 {
			return time.Duration(time.Now().UnixNano()%minutes) * time.Minute
		}
		return 0
	}

	b.runInWindowedLoop(ctx, interval, "自動投稿", b.executeAutoPost, jitterFunc, false)
}

type jitterFunc func(interval time.Duration) time.Duration

func (b *Bot) runInWindowedLoop(ctx context.Context, interval time.Duration, taskName string, task func(context.Context), getJitter jitterFunc, notifySlack bool) {
	log.Printf("%sループを開始しました (間隔: %v)", taskName, interval)

	b.scheduleDelayedTask(ctx, interval, taskName, task, getJitter, notifySlack)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				b.scheduleDelayedTask(ctx, interval, taskName, task, getJitter, notifySlack)
			}
		}
	}()
}

func (b *Bot) scheduleDelayedTask(ctx context.Context, interval time.Duration, taskName string, task func(context.Context), getJitter jitterFunc, notifySlack bool) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("CRITICAL: %sタスク実行中にパニック発生 (回復済み): %v", taskName, r)
			}
		}()

		jitter := getJitter(interval)
		if jitter > 0 {
			execTime := time.Now().Add(jitter)
			if loc, err := time.LoadLocation(b.config.Timezone); err == nil {
				execTime = execTime.In(loc)
			}

			msg := fmt.Sprintf("📅 %s: %s に実行予定です",
				taskName,
				execTime.Format(DateTimeFormat),
			)
			log.Println(msg)
			if notifySlack {
				_ = b.slackClient.PostMessage(ctx, msg)
			}

			select {
			case <-ctx.Done():
				return
			case <-time.After(jitter):
			}
		}

		if ctx.Err() != nil {
			return
		}

		task(ctx)
	}()
}

func (b *Bot) executeAutoPost(ctx context.Context) {
	facts, err := b.factStore.GetRandomGeneralFactBundle(AutoPostFactCount)
	if err != nil || len(facts) == 0 {
		return
	}

	prompt := llm.BuildAutoPostPrompt(facts)
	systemPrompt := llm.BuildSystemPrompt(b.config, "", "", "", true, b.config.CharacterPriority)

	response := b.llmClient.GenerateText(ctx, []model.Message{{Role: model.RoleUser, Content: prompt}}, systemPrompt, int64(b.config.MaxPostChars), nil, b.config.LLMTemperature)

	if response != "" {
		log.Printf("自動投稿を実行します: %s...", string([]rune(response))[:min(LogContentMaxChars, len([]rune(response)))])
		status, err := b.mastodonClient.PostStatus(ctx, response, b.config.AutoPostVisibility)
		if err != nil {
			log.Printf("自動投稿エラー: %v", err)
			return
		}

		displayName := status.Account.DisplayName
		if displayName == "" {
			displayName = status.Account.Username
		}
		baseFact := model.Fact{
			SourceID:           string(status.ID),
			Author:             status.Account.Acct,
			AuthorUserName:     displayName,
			SourceType:         model.SourceTypeSelf,
			SourceURL:          string(status.URL),
			PostAuthor:         status.Account.Acct,
			PostAuthorUserName: displayName,
			IsTrusted:          false,
		}
		go b.factService.ExtractAndSaveFacts(ctx, response, baseFact)
	}
}
