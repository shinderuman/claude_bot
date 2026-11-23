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
	TempImageFilenameSVG = "%s/generated_image_%d.svg"
	TempImageFilenamePNG = "%s/generated_image_%d.png"
)

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
		store.RollbackLastMessages(conversation, RollbackCountSmall)
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.ResponseGeneration)
		return false
	}

	postedStatuses, err := b.mastodonClient.PostResponseWithSplit(ctx, statusID, mention, response, visibility)
	if err != nil {
		log.Printf("応答の投稿に失敗: %v", err)
		store.RollbackLastMessages(conversation, RollbackCountMedium)
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.ResponsePost)
		return false
	}

	var postedIDs []string
	for _, s := range postedStatuses {
		postedIDs = append(postedIDs, string(s.ID))
	}

	if len(postedStatuses) > 0 {
		status := postedStatuses[0]
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

	store.AddMessage(conversation, model.RoleAssistant, response, postedIDs)

	session.LastUpdated = time.Now()
	return true
}

func (b *Bot) handleImageGeneration(ctx context.Context, session *model.Session, conversation *model.Conversation, imagePrompt, statusID, mention, visibility string) bool {
	svg, err := b.imageGenerator.GenerateSVG(ctx, imagePrompt)
	if err != nil {
		log.Printf("画像生成エラー: %v", err)
		store.RollbackLastMessages(conversation, RollbackCountSmall)
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.ImageGeneration)
		return false
	}

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
		defer os.Remove(tmpPngFilename) //nolint:errcheck
	}

	replyPrompt := llm.BuildImageGenerationReplyPrompt(imagePrompt, b.config.CharacterPrompt)
	replyMessages := []model.Message{{Role: model.RoleUser, Content: replyPrompt}}
	response := b.llmClient.GenerateText(ctx, replyMessages, "", b.config.MaxResponseTokens, nil, b.config.LLMTemperature)

	if response == "" {
		response = llm.Messages.Success.ImageGeneration
	}

	postedID, err := b.mastodonClient.PostResponseWithMedia(ctx, statusID, mention, response, visibility, tmpPngFilename)
	if err != nil {
		log.Printf("メディア投稿エラー: %v", err)
		store.RollbackLastMessages(conversation, RollbackCountMedium)
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.ImagePost)
		return false
	}

	store.AddMessage(conversation, model.RoleAssistant, response, []string{postedID})

	session.LastUpdated = time.Now()
	return true
}

func (b *Bot) classifyIntent(ctx context.Context, message string, urlCandidates []string) (model.IntentType, string, []string, string) {
	now := time.Now()
	if loc, err := time.LoadLocation(b.config.Timezone); err == nil {
		now = now.In(loc)
	}

	prompt := llm.BuildIntentClassificationPrompt(message, now, urlCandidates...)
	systemPrompt := llm.Messages.System.IntentClassification

	response := b.llmClient.GenerateText(ctx, []model.Message{{Role: model.RoleUser, Content: prompt}}, systemPrompt, b.config.MaxResponseTokens, nil, llm.TemperatureSystem)
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

func (b *Bot) handleFollowRequest(ctx context.Context, conversation *model.Conversation, notification *gomastodon.Notification, statusID, mention, visibility string) bool {
	targetAccountID := string(notification.Account.ID)
	targetAcct := notification.Account.Acct

	log.Printf("フォローリクエスト受信: %s (ID: %s)", targetAcct, targetAccountID)

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
		err := b.mastodonClient.FollowAccount(ctx, targetAccountID)
		if err != nil {
			log.Printf("フォロー失敗: %v", err)
			b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.FollowFail)
			return false
		}
		replyMessage = b.generateFollowReply(ctx, targetAcct, llm.Templates.FollowResponse, llm.Messages.Success.FollowSuccess)
	}

	postedStatuses, err := b.mastodonClient.PostResponseWithSplit(ctx, statusID, mention, replyMessage, visibility)
	if err != nil {
		log.Printf("フォロー完了返信エラー: %v", err)
	} else {
		var postedIDs []string
		for _, s := range postedStatuses {
			postedIDs = append(postedIDs, string(s.ID))
		}
		store.AddMessage(conversation, model.RoleAssistant, replyMessage, postedIDs)
	}

	return true
}

func (b *Bot) generateFollowReply(ctx context.Context, targetAcct, template, fallbackFormat string) string {
	if b.config.CharacterPrompt == "" {
		return fmt.Sprintf(fallbackFormat, targetAcct)
	}

	replyPrompt := fmt.Sprintf(template, b.config.CharacterPrompt, targetAcct)
	replyMessages := []model.Message{{Role: model.RoleUser, Content: replyPrompt}}

	generatedReply := b.llmClient.GenerateText(ctx, replyMessages, "", b.config.MaxResponseTokens, nil, b.config.LLMTemperature)
	if generatedReply != "" {
		return generatedReply
	}

	return fmt.Sprintf(fallbackFormat, targetAcct)
}

func (b *Bot) handleAssistantRequest(ctx context.Context, session *model.Session, conversation *model.Conversation, startID, endID, userMessage, statusID, mention, visibility string) bool {

	targetStatus, err := b.mastodonClient.GetStatus(ctx, startID)
	if err != nil {
		log.Printf("開始ステータス取得失敗: %v", err)
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.UserPostNotFound)
		return false
	}

	targetAccountID := string(targetStatus.Account.ID)

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

	prompt := llm.BuildAssistantAnalysisPrompt(statuses, userMessage)
	systemPrompt := llm.BuildSystemPrompt(b.config, "", "", "", true, b.config.CharacterPriority)

	response := b.llmClient.GenerateText(ctx, []model.Message{{Role: model.RoleUser, Content: prompt}}, systemPrompt, b.config.MaxSummaryTokens, nil, llm.TemperatureSystem)

	if response == "" {
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.AnalysisGeneration)
		return false
	}

	postedStatuses, err := b.mastodonClient.PostResponseWithSplit(ctx, statusID, mention, response, visibility)
	if err != nil {
		log.Printf("応答の投稿に失敗しました: %v", err)
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.AnalysisPost)
		return false
	}

	var postedIDs []string
	for _, s := range postedStatuses {
		postedIDs = append(postedIDs, string(s.ID))
	}

	store.AddMessage(conversation, model.RoleAssistant, response, postedIDs)

	session.LastUpdated = time.Now()
	return true
}

func (b *Bot) handleDailySummaryRequest(ctx context.Context, session *model.Session, conversation *model.Conversation, notification *gomastodon.Notification, targetDate, userMessage, statusID, mention, visibility string) bool {
	accountID := string(notification.Account.ID)

	log.Printf("Daily Summary Request: targetDate=%s", targetDate)

	loc, err := time.LoadLocation(b.config.Timezone)
	if err != nil {
		log.Printf("タイムゾーン読み込み失敗 (%s): %v", b.config.Timezone, err)
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.TimeZone)
		return false
	}

	now := time.Now().In(loc)
	targetDay, err := time.ParseInLocation(DateFormatYMD, targetDate, loc)
	if err != nil {
		log.Printf("日付パース失敗: %s", targetDate)
		b.postErrorMessage(ctx, statusID, mention, visibility, fmt.Sprintf(llm.Messages.Error.DateParse, targetDate))
		return true
	}
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	if targetDay.Before(todayStart.AddDate(0, 0, -DailySummaryDaysLimit)) {
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.DateLimit)
		return true
	}

	endTime := targetDay.AddDate(0, 0, 1)
	targetDateStr := targetDay.Format(DateFormatYMDSlash)

	statuses, err := b.mastodonClient.GetStatusesByDateRange(ctx, accountID, targetDay, endTime)
	if err != nil {
		log.Printf("発言取得失敗: %v", err)
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.DataFetch)
		return false
	}

	if len(statuses) == 0 {
		b.postErrorMessage(ctx, statusID, mention, visibility, fmt.Sprintf(llm.Messages.Error.NoStatus, targetDay.Month(), targetDay.Day()))
		return true
	}

	prompt := llm.BuildDailySummaryPrompt(statuses, targetDateStr, userMessage, loc)
	systemPrompt := llm.BuildSystemPrompt(b.config, "", "", "", true, b.config.CharacterPriority)

	response := b.llmClient.GenerateText(ctx, []model.Message{{Role: model.RoleUser, Content: prompt}}, systemPrompt, b.config.MaxSummaryTokens, nil, llm.TemperatureSystem)

	if response == "" {
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.SummaryGeneration)
		return false
	}

	postedStatuses, err := b.mastodonClient.PostResponseWithSplit(ctx, statusID, mention, response, visibility)
	if err != nil {
		log.Printf("まとめ結果投稿エラー: %v", err)
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.SummaryPost)
		return false
	}

	var postedIDs []string
	for _, s := range postedStatuses {
		postedIDs = append(postedIDs, string(s.ID))
	}

	store.AddMessage(conversation, model.RoleAssistant, response, postedIDs)

	session.LastUpdated = time.Now()
	return true
}
