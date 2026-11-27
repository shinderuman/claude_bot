package bot

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"claude_bot/internal/mastodon"
	"claude_bot/internal/model"
	"claude_bot/internal/prompts"
)

func (b *Bot) startFactMaintenanceLoop(ctx context.Context) {
	if b.factStore == nil {
		return
	}

	interval := time.Duration(b.config.FactMaintenanceIntervalHours) * time.Hour
	b.runInWindowedLoop(ctx, interval, "ファクトメンテナンス", func(ctx context.Context) {
		if err := b.factService.PerformMaintenance(ctx); err != nil {
			log.Printf("ファクトメンテナンス失敗: %v", err)
		}

		if b.factCollector != nil {
			b.factCollector.DiscoverAndCollectPeerFacts(ctx)
		}
	}, windowJitter, true)
}

func (b *Bot) startAutoPostLoop(ctx context.Context) {
	if b.config.AutoPostIntervalHours <= 0 {
		return
	}

	interval := time.Duration(b.config.AutoPostIntervalHours) * time.Hour
	b.runInWindowedLoop(ctx, interval, "自動投稿", b.executeAutoPost, windowJitter, false)
}

func windowJitter(interval time.Duration) time.Duration {
	minutes := int64(interval / time.Minute)
	if minutes <= 0 {
		return 0
	}
	return time.Duration(time.Now().UnixNano()%minutes) * time.Minute
}

type jitterFunc func(interval time.Duration) time.Duration

func (b *Bot) runInWindowedLoop(ctx context.Context, interval time.Duration, taskName string, task func(context.Context), getJitter jitterFunc, notifySlack bool) {
	if interval <= 0 {
		return
	}
	log.Printf("%s実行間隔: %s", taskName, interval)
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
				log.Printf("%sのパニックから復帰: %v", taskName, r)
			}
		}()

		jitter := getJitter(interval)
		if jitter > 0 {
			execTime := time.Now().Add(jitter)
			if loc, err := time.LoadLocation(b.config.Timezone); err == nil {
				execTime = execTime.In(loc)
			}

			displayTime := execTime.Format(DateTimeFormat)
			msg := fmt.Sprintf("📅 %s: %s に実行予定です",
				taskName,
				displayTime,
			)
			log.Printf("%s実行予定: %s", taskName, displayTime)
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
	facts, err := b.factStore.GetRandomGeneralFactBundle(ctx, AutoPostFactCount)
	if err != nil {
		log.Printf("自動投稿用ファクト取得失敗: %v", err)
		return
	}
	if len(facts) == 0 {
		return
	}

	prompt := prompts.BuildAutoPostPrompt(facts)
	systemPrompt := prompts.BuildSystemPrompt(b.config, "", "", "", true, b.config.CharacterPriority)

	response := b.llmClient.GenerateText(ctx, []model.Message{{Role: model.RoleUser, Content: prompt}}, systemPrompt, int64(b.config.MaxPostChars), nil, b.config.LLMTemperature, "自動投稿文生成")

	if response != "" {
		status, err := b.mastodonClient.PostStatus(ctx, response, b.config.AutoPostVisibility)
		if err != nil {
			log.Printf("自動投稿失敗: %s、%v", autoPostExcerpt(response), err)
			return
		}

		postedText := b.mastodonClient.StripHTML(status.Content)
		postedText = strings.TrimSuffix(strings.TrimSpace(postedText), strings.TrimSpace(mastodon.BotTag))
		log.Printf("自動投稿: %s", autoPostExcerpt(postedText))

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

func autoPostExcerpt(text string) string {
	runes := []rune(strings.Join(strings.Fields(text), " "))
	if len(runes) > LogContentMaxChars {
		return string(runes[:LogContentMaxChars]) + "…"
	}
	return string(runes)
}
