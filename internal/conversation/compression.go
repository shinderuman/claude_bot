package conversation

import (
	"context"
	"log"
	"strings"

	"claude_bot/internal/config"
	"claude_bot/internal/model"
	"claude_bot/internal/prompts"
)

type summaryGenerator interface {
	GenerateSummary(context.Context, []model.Message, string) string
}

type summaryFactExtractor interface {
	ExtractAndSaveFactsFromSummary(ctx context.Context, summary string, baseFact model.Fact)
}

func CompressHistoryIfNeeded(ctx context.Context, session *model.Session, userID string, cfg *config.Config, summarizer summaryGenerator, factExtractor summaryFactExtractor) {
	for i := range session.Conversations {
		compressConversationIfNeeded(ctx, session, &session.Conversations[i], userID, cfg, summarizer, factExtractor)
	}

	compressOldConversations(ctx, session, userID, cfg, summarizer, factExtractor)
}

func compressConversationIfNeeded(ctx context.Context, session *model.Session, conversation *model.Conversation, userID string, cfg *config.Config, summarizer summaryGenerator, factExtractor summaryFactExtractor) {
	if len(conversation.Messages) <= cfg.ConversationMessageCompressThreshold {
		return
	}

	compressCount := len(conversation.Messages) - max(0, cfg.ConversationMessageKeepCount)
	if compressCount <= 0 {
		return
	}
	messagesToCompress := conversation.Messages[:compressCount]

	summary := generateSummary(ctx, messagesToCompress, session.Summary, summarizer)
	if summary == "" {
		log.Printf("会話内要約生成エラー: 応答が空です")
		return
	}

	conversation.Messages = conversation.Messages[compressCount:]
	session.Summary = summary

	if factExtractor != nil {
		baseFact := model.Fact{Author: userID}
		go factExtractor.ExtractAndSaveFactsFromSummary(ctx, summary, baseFact)
	}

	log.Printf("会話内圧縮完了: %d件のメッセージを削除、%d件を保持", compressCount, len(conversation.Messages))
}

func compressOldConversations(ctx context.Context, session *model.Session, userID string, cfg *config.Config, summarizer summaryGenerator, factExtractor summaryFactExtractor) {
	oldConversations := findOldConversations(cfg, session)
	if len(oldConversations) == 0 {
		return
	}

	var allMessages []model.Message
	for _, conv := range oldConversations {
		allMessages = append(allMessages, conv.Messages...)
	}

	summary := generateSummary(ctx, allMessages, session.Summary, summarizer)
	if summary == "" {
		log.Printf("要約生成エラー: 応答が空です")
		return
	}

	updateSessionWithSummary(session, summary, oldConversations)

	if factExtractor != nil {
		baseFact := model.Fact{Author: userID}
		go factExtractor.ExtractAndSaveFactsFromSummary(ctx, summary, baseFact)
	}

	log.Printf("履歴圧縮完了: %d件の会話を要約に移行", len(oldConversations))
}

func generateSummary(ctx context.Context, messages []model.Message, existingSummary string, summarizer summaryGenerator) string {
	formattedMessages := formatMessagesForSummary(messages)
	summaryPrompt := prompts.BuildSummaryPrompt(formattedMessages, existingSummary)
	summaryMessages := []model.Message{{Role: model.RoleUser, Content: summaryPrompt}}
	return summarizer.GenerateSummary(ctx, summaryMessages, existingSummary)
}

func formatMessagesForSummary(messages []model.Message) string {
	var builder strings.Builder
	for _, msg := range messages {
		role := "ユーザー"
		if msg.Role == model.RoleAssistant {
			role = "アシスタント"
		}
		builder.WriteString(role)
		builder.WriteString(": ")
		builder.WriteString(msg.Content)
		builder.WriteString("\n")
	}
	return builder.String()
}
