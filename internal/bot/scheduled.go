package bot

import (
	"claude_bot/internal/llm"
	"claude_bot/internal/model"
	"context"
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

		// Peer探索とプロファイル収集（メンテナンスタイミングで実行）
		if b.factCollector != nil {
			log.Println("Peer探索を実行中...")
			b.factCollector.DiscoverAndCollectPeerFacts(ctx)
		}
	}, jitterFunc)
}

func (b *Bot) startAutoPostLoop(ctx context.Context) {
	if b.config.AutoPostIntervalHours <= 0 {
		return
	}

	interval := time.Duration(b.config.AutoPostIntervalHours) * time.Hour
	// Default jitter: Random within interval
	jitterFunc := func(interval time.Duration) time.Duration {
		minutes := int64(interval.Minutes())
		if minutes > 0 {
			return time.Duration(time.Now().UnixNano()%minutes) * time.Minute
		}
		return 0
	}

	b.runInWindowedLoop(ctx, interval, "自動投稿", b.executeAutoPost, jitterFunc)
}

type jitterFunc func(interval time.Duration) time.Duration

func (b *Bot) runInWindowedLoop(ctx context.Context, interval time.Duration, taskName string, task func(context.Context), getJitter jitterFunc) {
	log.Printf("%sループを開始しました (間隔: %v)", taskName, interval)

	// 起動時間を基準にする
	windowStart := time.Now()

	go func() {
		for {
			// Panic Recovery for the loop
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("CRITICAL: %sループ内でパニックが発生しました (回復): %v", taskName, r)
					}
				}()

				// Jitter計算 (テスト時は固定値を返すことで制御可能)
				offset := getJitter(interval)

				// ウィンドウ開始からoffset後に実行
				scheduledTime := windowStart.Add(offset)

				log.Printf("次回の%s予定: %s", taskName, scheduledTime.Format(DateTimeFormat))

				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Until(scheduledTime)):
					// 実行
					task(ctx)
				}
			}()

			// コンテキストチェック（パニック回復後もここを通るため）
			if ctx.Err() != nil {
				return
			}

			// 次のウィンドウへ
			windowStart = windowStart.Add(interval)

			// Catch-up: もし現在時刻がすでに次のウィンドウを超えていても、
			// 待機ロジック(Before check)がfalseになるだけなので即座に次へ進む

			if time.Now().Before(windowStart) {
				log.Printf("次の%sウィンドウ開始(%s)まで待機します", taskName, windowStart.Format(DateTimeFormat))
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Until(windowStart)):
					// 待機完了
				}
			}
		}
	}()
}

func (b *Bot) executeAutoPost(ctx context.Context) {
	// ランダムな一般知識のバンドルを取得
	facts, err := b.factStore.GetRandomGeneralFactBundle(AutoPostFactCount)
	if err != nil || len(facts) == 0 {
		return
	}

	// プロンプト作成
	prompt := llm.BuildAutoPostPrompt(facts)
	// システムプロンプトはキャラクター設定のみを使用（要約などは不要）
	// AutoPostの場合はMaxPostChars制限を適用
	systemPrompt := llm.BuildSystemPrompt(b.config, "", "", "", true)

	// 画像なしで呼び出し
	response := b.llmClient.GenerateText(ctx, []model.Message{{Role: model.RoleUser, Content: prompt}}, systemPrompt, int64(b.config.MaxPostChars), nil, b.config.LLMTemperature)

	if response != "" {
		// 公開投稿として送信
		log.Printf("自動投稿を実行します: %s...", string([]rune(response))[:min(LogContentMaxChars, len([]rune(response)))])
		status, err := b.mastodonClient.PostStatus(ctx, response, b.config.AutoPostVisibility)
		if err != nil {
			log.Printf("自動投稿エラー: %v", err)
			return
		}

		// 自分の投稿から事実を抽出（学習）
		displayName := status.Account.DisplayName
		if displayName == "" {
			displayName = status.Account.Username
		}
		go b.factService.ExtractAndSaveFacts(
			ctx,
			string(status.ID),
			status.Account.Acct,
			displayName,
			response,
			model.SourceTypeSelf,
			string(status.URL),
			status.Account.Acct,
			displayName,
		)
	}
}
