package bot

import (
	"claude_bot/internal/llm"
	"claude_bot/internal/model"
	"claude_bot/internal/store"
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"claude_bot/internal/image"

	gomastodon "github.com/mattn/go-mastodon"
)

const (
	// TempImageFilenameSVG is the format for temporary SVG files
	TempImageFilenameSVG = "%s/generated_image_%d.svg"
	// TempImageFilenamePNG is the format for temporary PNG files
	TempImageFilenamePNG = "%s/generated_image_%d.png"
)

// handleChatResponse handles the normal chat response flow
func (b *Bot) handleChatResponse(ctx context.Context, session *model.Session, conversation *model.Conversation, notification *gomastodon.Notification, userMessage string, images []model.Image, statusID, mention, visibility string) bool {
	displayName := notification.Account.DisplayName
	if displayName == "" {
		displayName = notification.Account.Username
	}

	relevantFacts := b.factService.QueryRelevantFacts(ctx, notification.Account.Acct, displayName, userMessage)

	var botProfile string
	if b.config.BotProfileFile != "" {
		if content, err := os.ReadFile(b.config.BotProfileFile); err == nil {
			botProfile = string(content)
		}
	}

	response := b.llmClient.GenerateResponse(ctx, session, conversation, relevantFacts, botProfile, images)

	if response == "" {
		store.RollbackLastMessages(conversation, RollbackCountSmall) // ユーザー発言を取り消し
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.ResponseGeneration)
		return false
	}

	// 投稿
	postedStatuses, err := b.mastodonClient.PostResponseWithSplit(ctx, statusID, mention, response, visibility)
	if err != nil {
		log.Printf("応答の投稿に失敗: %v", err)
		store.RollbackLastMessages(conversation, RollbackCountMedium)
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.ResponsePost)
		return false
	}

	// IDリストの作成
	var postedIDs []string
	for _, s := range postedStatuses {
		postedIDs = append(postedIDs, string(s.ID))
	}

	// 自分の投稿から事実を抽出（学習）
	if len(postedStatuses) > 0 {
		status := postedStatuses[0]
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

	// 履歴に追加
	store.AddMessage(conversation, "assistant", response, postedIDs)

	session.LastUpdated = time.Now()
	return true
}

// handleImageGeneration handles image generation requests
func (b *Bot) handleImageGeneration(ctx context.Context, session *model.Session, conversation *model.Conversation, imagePrompt, statusID, mention, visibility string) bool {
	// SVG生成
	svg, err := b.imageGenerator.GenerateSVG(ctx, imagePrompt)
	if err != nil {
		log.Printf("画像生成エラー: %v", err)
		store.RollbackLastMessages(conversation, RollbackCountSmall)
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.ImageGeneration)
		return false
	}

	// 一時ファイルに保存
	tmpSvgFilename := fmt.Sprintf(TempImageFilenameSVG, os.TempDir(), time.Now().Unix())
	if err := os.WriteFile(tmpSvgFilename, []byte(svg), 0644); err != nil {
		log.Printf("SVG保存エラー: %v", err)
		store.RollbackLastMessages(conversation, RollbackCountSmall)
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.Internal)
		return false
	}
	defer os.Remove(tmpSvgFilename) //nolint:errcheck

	tmpPngFilename := fmt.Sprintf(TempImageFilenamePNG, os.TempDir(), time.Now().Unix())
	if err := image.ConvertSVGToPNG(tmpSvgFilename, tmpPngFilename); err != nil {
		log.Printf("PNG変換エラー: %v", err)
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.ImageGeneration)
		return false
	} else {
		defer os.Remove(tmpPngFilename) //nolint:errcheck // クリーンアップ
	}

	// 画像を添付して返信
	// メッセージを生成
	replyPrompt := llm.BuildImageGenerationReplyPrompt(imagePrompt, b.config.CharacterPrompt)
	replyMessages := []model.Message{{Role: "user", Content: replyPrompt}}
	response := b.llmClient.GenerateText(ctx, replyMessages, "", b.config.MaxResponseTokens, nil, b.config.LLMTemperature)

	if response == "" {
		response = llm.Messages.Success.ImageGeneration
	}

	// 投稿
	postedID, err := b.mastodonClient.PostResponseWithMedia(ctx, statusID, mention, response, visibility, tmpPngFilename)
	if err != nil {
		log.Printf("メディア投稿エラー: %v", err)
		store.RollbackLastMessages(conversation, RollbackCountMedium)
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.ImagePost)
		return false
	}

	// 成功したら履歴に追加
	store.AddMessage(conversation, "assistant", response, []string{postedID})

	session.LastUpdated = time.Now()
	return true
}

// classifyIntent classifies the user's intent using LLM
func (b *Bot) classifyIntent(ctx context.Context, message string) (model.IntentType, string, []string, string) {
	// JSTの現在時刻を取得（タイムゾーンロード失敗時はUTC）
	now := time.Now()
	if loc, err := time.LoadLocation(b.config.Timezone); err == nil {
		now = now.In(loc)
	}

	prompt := llm.BuildIntentClassificationPrompt(message, now)
	// システムプロンプトはシンプルに
	systemPrompt := llm.Messages.System.IntentClassification

	response := b.llmClient.GenerateText(ctx, []model.Message{{Role: "user", Content: prompt}}, systemPrompt, b.config.MaxResponseTokens, nil, 0.0)
	if response == "" {
		return model.IntentChat, "", nil, ""
	}

	jsonStr := llm.ExtractJSON(response)
	var result struct {
		Intent       string   `json:"intent"`
		ImagePrompt  string   `json:"image_prompt"`
		AnalysisURLs []string `json:"analysis_urls"`
		TargetDate   string   `json:"target_date"`
	}

	if err := llm.UnmarshalWithRepair(jsonStr, &result, "意図判定"); err != nil {
		log.Printf("意図判定JSONパースエラー: %v\nJSON: %s", err, jsonStr)
		return model.IntentChat, "", nil, ""
	}

	return model.IntentType(result.Intent), result.ImagePrompt, result.AnalysisURLs, result.TargetDate
}

// handleFollowRequest handles the follow request logic
func (b *Bot) handleFollowRequest(ctx context.Context, conversation *model.Conversation, notification *gomastodon.Notification, statusID, mention, visibility string) bool {
	targetAccountID := string(notification.Account.ID)
	targetAcct := notification.Account.Acct

	log.Printf("フォローリクエスト受信: %s (ID: %s)", targetAcct, targetAccountID)

	// 既にフォロー済みかチェック
	isFollowing, err := b.mastodonClient.IsFollowing(ctx, targetAccountID)
	if err != nil {
		log.Printf("フォロー状態確認エラー: %v", err)
		isFollowing = false
	}

	var replyMessage string
	if isFollowing {
		log.Printf("既にフォロー済みです: %s", targetAcct)
		replyMessage = b.generateFollowReply(ctx, targetAcct, llm.Templates.FollowResponseAlready, llm.Messages.Success.FollowAlready)
	} else {
		// まだフォローしていない場合、フォローを実行
		err := b.mastodonClient.FollowAccount(ctx, targetAccountID)
		if err != nil {
			log.Printf("フォロー失敗: %v", err)
			b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.FollowFail)
			return false
		}
		replyMessage = b.generateFollowReply(ctx, targetAcct, llm.Templates.FollowResponse, llm.Messages.Success.FollowSuccess)
	}

	// 投稿
	postedStatuses, err := b.mastodonClient.PostResponseWithSplit(ctx, statusID, mention, replyMessage, visibility)
	if err != nil {
		log.Printf("フォロー完了返信エラー: %v", err)
	} else {
		// IDリストの作成
		var postedIDs []string
		for _, s := range postedStatuses {
			postedIDs = append(postedIDs, string(s.ID))
		}
		// 成功したら履歴に追加
		store.AddMessage(conversation, "assistant", replyMessage, postedIDs)
	}

	return true
}

func (b *Bot) generateFollowReply(ctx context.Context, targetAcct, template, fallbackFormat string) string {
	if b.config.CharacterPrompt == "" {
		return fmt.Sprintf(fallbackFormat, targetAcct)
	}

	replyPrompt := fmt.Sprintf(template, b.config.CharacterPrompt, targetAcct)
	replyMessages := []model.Message{{Role: "user", Content: replyPrompt}}

	generatedReply := b.llmClient.GenerateText(ctx, replyMessages, "", b.config.MaxResponseTokens, nil, b.config.LLMTemperature)
	if generatedReply != "" {
		return generatedReply
	}

	return fmt.Sprintf(fallbackFormat, targetAcct)
}

// handleAssistantRequest handles the assistant analysis request
func (b *Bot) handleAssistantRequest(ctx context.Context, session *model.Session, conversation *model.Conversation, startID, endID, userMessage, statusID, mention, visibility string) bool {

	// 1. URLからアカウント情報を特定するためにまず開始ステータスを取得
	targetStatus, err := b.mastodonClient.GetStatus(ctx, startID)
	if err != nil {
		log.Printf("開始ステータス取得失敗: %v", err)
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.UserPostNotFound)
		return false
	}

	targetAccountID := string(targetStatus.Account.ID)

	// 2. 発言範囲の取得
	statuses, err := b.mastodonClient.GetStatusesByRange(ctx, targetAccountID, startID, endID)
	if err != nil {
		log.Printf("発言範囲取得失敗: %v", err)
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.AnalysisDataFetch)
		return false
	}

	if len(statuses) == 0 {
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.AnalysisNoData)
		return true
	}

	// 3. LLMによる分析
	prompt := llm.BuildAssistantAnalysisPrompt(statuses, userMessage)
	systemPrompt := llm.BuildSystemPrompt(b.config, "", "", "", true)

	// 分析には長文の可能性があるため、サマリー用のトークン数を使用
	response := b.llmClient.GenerateText(ctx, []model.Message{{Role: "user", Content: prompt}}, systemPrompt, b.config.MaxSummaryTokens, nil, 0.0)

	if response == "" {
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.AnalysisGeneration)
		return false
	}

	// 4. Mastodonに投稿 (分割投稿対応、全StatusID取得)
	postedStatuses, err := b.mastodonClient.PostResponseWithSplit(ctx, statusID, mention, response, visibility)
	if err != nil {
		log.Printf("応答の投稿に失敗しました: %v", err)
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.AnalysisPost)
		return false
	}

	// IDリストの作成
	var postedIDs []string
	for _, s := range postedStatuses {
		postedIDs = append(postedIDs, string(s.ID))
	}

	// 5. 会話履歴にアシスタントの発言（全ID）を追加
	store.AddMessage(conversation, "assistant", response, postedIDs)

	session.LastUpdated = time.Now()
	return true
}

// handleDailySummaryRequest handles the daily summary request
func (b *Bot) handleDailySummaryRequest(ctx context.Context, session *model.Session, conversation *model.Conversation, notification *gomastodon.Notification, targetDate, userMessage, statusID, mention, visibility string) bool {
	// リクエスト送信者のアカウントIDを取得
	accountID := string(notification.Account.ID)

	log.Printf("Daily Summary Request: targetDate=%s", targetDate)

	// JSTのタイムゾーンを取得
	loc, err := time.LoadLocation(b.config.Timezone)
	if err != nil {
		log.Printf("タイムゾーン読み込み失敗 (%s): %v", b.config.Timezone, err)
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.TimeZone)
		return false
	}

	// 対象日を計算
	now := time.Now().In(loc)
	var targetDay time.Time

	// targetDateはLLMによって既にYYYY-MM-DD形式に変換されている前提
	parsedDate, err := time.Parse(DateFormatYMD, targetDate)
	if err != nil {
		log.Printf("日付パース失敗: %s", targetDate)
		b.postErrorMessage(ctx, statusID, mention, visibility, fmt.Sprintf(llm.Messages.Error.DateParse, targetDate))
		return true
	}
	targetDay = parsedDate.In(loc)

	// 3日前より過去かどうかチェック
	// 今日の0時を基準にする
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	daysDiff := todayStart.Sub(time.Date(targetDay.Year(), targetDay.Month(), targetDay.Day(), 0, 0, 0, 0, loc)).Hours() / 24

	if daysDiff > DailySummaryDaysLimit {
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.DateLimit)
		return true
	}

	startTime := time.Date(targetDay.Year(), targetDay.Month(), targetDay.Day(), 0, 0, 0, 0, loc)
	endTime := startTime.Add(24 * time.Hour)
	targetDateStr := targetDay.Format(DateFormatYMDSlash)

	// 発言を取得
	statuses, err := b.mastodonClient.GetStatusesByDateRange(ctx, accountID, startTime, endTime)
	if err != nil {
		log.Printf("発言取得失敗: %v", err)
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.DataFetch)
		return false
	}

	if len(statuses) == 0 {
		b.postErrorMessage(ctx, statusID, mention, visibility, fmt.Sprintf(llm.Messages.Error.NoStatus, targetDay.Month(), targetDay.Day()))
		return true
	}

	// LLMによるまとめ
	prompt := llm.BuildDailySummaryPrompt(statuses, targetDateStr, userMessage, loc)
	systemPrompt := llm.BuildSystemPrompt(b.config, "", "", "", true)

	response := b.llmClient.GenerateText(ctx, []model.Message{{Role: "user", Content: prompt}}, systemPrompt, b.config.MaxSummaryTokens, nil, 0.0)

	if response == "" {
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.SummaryGeneration)
		return false
	}

	// 投稿
	postedStatuses, err := b.mastodonClient.PostResponseWithSplit(ctx, statusID, mention, response, visibility)
	if err != nil {
		log.Printf("まとめ結果投稿エラー: %v", err)
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.SummaryPost)
		return false
	}

	// IDリストの作成
	var postedIDs []string
	for _, s := range postedStatuses {
		postedIDs = append(postedIDs, string(s.ID))
	}

	// 履歴に追加
	store.AddMessage(conversation, "assistant", response, postedIDs)

	session.LastUpdated = time.Now()
	return true
}
