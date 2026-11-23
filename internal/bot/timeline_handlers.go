package bot

import (
	"context"
	"fmt"
	"log"
	"time"

	chat "claude_bot/internal/conversation"
	"claude_bot/internal/llm"
	"claude_bot/internal/model"
	"claude_bot/internal/prompts"

	gomastodon "github.com/mattn/go-mastodon"
)

func (b *Bot) handleAssistantRequest(ctx context.Context, session *model.Session, conversation *model.Conversation, startID, endID, userMessage, statusID, mention, visibility string) bool {

	targetStatus, err := b.mastodonClient.GetStatus(ctx, startID)
	if err != nil {
		log.Printf("分析の開始投稿取得失敗: %v", err)
		b.postErrorMessage(ctx, statusID, mention, visibility, prompts.Messages.Error.UserPostNotFound)
		return false
	}

	targetAccountID := string(targetStatus.Account.ID)

	statuses, err := b.mastodonClient.GetStatusesByRange(ctx, targetAccountID, startID, endID)
	if err != nil {
		log.Printf("投稿範囲取得失敗: %v", err)
		b.postErrorMessage(ctx, statusID, mention, visibility, prompts.Messages.Error.AnalysisDataFetch)
		return false
	}

	if len(statuses) == 0 {
		b.postErrorMessage(ctx, statusID, mention, visibility, prompts.Messages.Error.AnalysisNoData)
		return true
	}

	prompt := prompts.BuildAssistantAnalysisPrompt(statuses, userMessage)
	systemPrompt := prompts.BuildSystemPrompt(b.config, "", "", "", true, b.config.CharacterPriority)

	response := b.llmClient.GenerateText(ctx, []model.Message{{Role: model.RoleUser, Content: prompt}}, systemPrompt, b.config.MaxSummaryTokens, nil, llm.TemperatureSystem)

	if response == "" {
		b.postErrorMessage(ctx, statusID, mention, visibility, prompts.Messages.Error.AnalysisGeneration)
		return false
	}

	postedStatuses, err := b.mastodonClient.PostResponseWithSplit(ctx, statusID, mention, response, visibility)
	if err != nil {
		log.Printf("分析結果の返信失敗: %v", err)
		b.postErrorMessage(ctx, statusID, mention, visibility, prompts.Messages.Error.AnalysisPost)
		return false
	}

	var postedIDs []string
	for _, s := range postedStatuses {
		postedIDs = append(postedIDs, string(s.ID))
	}

	chat.AddMessage(conversation, model.RoleAssistant, response, postedIDs)

	session.LastUpdated = time.Now()
	return true
}

func (b *Bot) handleDailySummaryRequest(ctx context.Context, session *model.Session, conversation *model.Conversation, notification *gomastodon.Notification, targetDate, userMessage, statusID, mention, visibility string) bool {
	accountID := string(notification.Account.ID)

	log.Printf("日次まとめ依頼受信: %s", targetDate)

	loc, err := time.LoadLocation(b.config.Timezone)
	if err != nil {
		log.Printf("タイムゾーン読込失敗: %s、%v", b.config.Timezone, err)
		b.postErrorMessage(ctx, statusID, mention, visibility, prompts.Messages.Error.TimeZone)
		return false
	}

	now := time.Now().In(loc)
	targetDay, err := time.ParseInLocation(DateFormatYMD, targetDate, loc)
	if err != nil {
		log.Printf("日付解析失敗: %s", targetDate)
		b.postErrorMessage(ctx, statusID, mention, visibility, fmt.Sprintf(prompts.Messages.Error.DateParse, targetDate))
		return true
	}
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	if targetDay.Before(todayStart.AddDate(0, 0, -DailySummaryDaysLimit)) {
		b.postErrorMessage(ctx, statusID, mention, visibility, prompts.Messages.Error.DateLimit)
		return true
	}

	endTime := targetDay.AddDate(0, 0, 1)
	targetDateStr := targetDay.Format(DateFormatYMDSlash)

	statuses, err := b.mastodonClient.GetStatusesByDateRange(ctx, accountID, targetDay, endTime)
	if err != nil {
		log.Printf("投稿取得失敗: %v", err)
		b.postErrorMessage(ctx, statusID, mention, visibility, prompts.Messages.Error.DataFetch)
		return false
	}

	if len(statuses) == 0 {
		b.postErrorMessage(ctx, statusID, mention, visibility, fmt.Sprintf(prompts.Messages.Error.NoStatus, targetDay.Month(), targetDay.Day()))
		return true
	}

	prompt := prompts.BuildDailySummaryPrompt(statuses, targetDateStr, userMessage, loc)
	systemPrompt := prompts.BuildSystemPrompt(b.config, "", "", "", true, b.config.CharacterPriority)

	response := b.llmClient.GenerateText(ctx, []model.Message{{Role: model.RoleUser, Content: prompt}}, systemPrompt, b.config.MaxSummaryTokens, nil, llm.TemperatureSystem)

	if response == "" {
		b.postErrorMessage(ctx, statusID, mention, visibility, prompts.Messages.Error.SummaryGeneration)
		return false
	}

	postedStatuses, err := b.mastodonClient.PostResponseWithSplit(ctx, statusID, mention, response, visibility)
	if err != nil {
		log.Printf("日次まとめの返信失敗: %v", err)
		b.postErrorMessage(ctx, statusID, mention, visibility, prompts.Messages.Error.SummaryPost)
		return false
	}

	var postedIDs []string
	for _, s := range postedStatuses {
		postedIDs = append(postedIDs, string(s.ID))
	}

	chat.AddMessage(conversation, model.RoleAssistant, response, postedIDs)

	session.LastUpdated = time.Now()
	return true
}
