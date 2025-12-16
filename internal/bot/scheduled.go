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

	// 定期的にメンテナンスを実行
	ticker := time.NewTicker(FactMaintenanceInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			log.Println("定期ファクトメンテナンスを実行中...")
			if err := b.factService.PerformMaintenance(ctx); err != nil {
				log.Printf("ファクトメンテナンスエラー: %v", err)
			}
		}
	}
}

func (b *Bot) startAutoPostLoop(ctx context.Context) {
	if b.config.AutoPostIntervalHours <= 0 {
		return
	}

	log.Printf("自動投稿ループを開始しました (間隔: %d時間 + ランダム遅延)", b.config.AutoPostIntervalHours)

	// 起動時間を基準にする
	windowStart := time.Now()

	for {
		// インターバル（時間）を分に変換して、その範囲内でランダムな時間を決定
		// 例: 1時間なら0-59分、2時間なら0-119分
		intervalMinutes := b.config.AutoPostIntervalHours * 60
		randomMinutes := time.Duration(time.Now().UnixNano() % int64(intervalMinutes))
		if randomMinutes < 0 {
			randomMinutes = -randomMinutes // 念のため
		}

		// ウィンドウ開始からrandomMinutes後に実行
		scheduledTime := windowStart.Add(randomMinutes * time.Minute)

		// もし計算した時間が既に過ぎている場合は、すぐに実行するか、次のウィンドウに回すか...
		// ここでは「現在時刻より後」になるように調整（ウィンドウ内でまだ時間が残っていれば）
		if scheduledTime.Before(time.Now()) {
			// ウィンドウ後半などで起動した場合など。すぐに実行。
			scheduledTime = time.Now().Add(1 * time.Minute)
		}

		log.Printf("次回の自動投稿予定: %s", scheduledTime.Format(DateTimeFormat))

		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(scheduledTime)):
			b.executeAutoPost(ctx)
		}

		// 現在のウィンドウが終わる（＝次のウィンドウ開始）まで待機
		windowEnd := windowStart.Add(time.Duration(b.config.AutoPostIntervalHours) * time.Hour)
		if time.Now().Before(windowEnd) {
			log.Printf("次のウィンドウ開始(%s)まで待機します", windowEnd.Format(DateTimeFormat))
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Until(windowEnd)):
				// 待機完了
			}
		}

		// 次のウィンドウへ
		windowStart = windowEnd
	}
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
	response := b.llmClient.GenerateText(ctx, []model.Message{{Role: "user", Content: prompt}}, systemPrompt, int64(b.config.MaxPostChars), nil)

	if response != "" {
		// #botタグを追加（AI生成コンテンツであることを明示）
		response = response + AutoPostHashTag

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
