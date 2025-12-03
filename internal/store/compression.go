package store

import (
	"context"
	"log"
	"strings"

	"claude_bot/internal/config"
	"claude_bot/internal/llm"
	"claude_bot/internal/model"
)

// Conversation compression and summarization

func (h *ConversationHistory) CompressHistoryIfNeeded(ctx context.Context, session *model.Session, cfg *config.Config, llmClient *llm.Client) {
	for i := range session.Conversations {
		h.compressConversationIfNeeded(ctx, session, &session.Conversations[i], cfg, llmClient)
	}

	h.compressOldConversations(ctx, session, cfg, llmClient)
}

func (h *ConversationHistory) compressConversationIfNeeded(ctx context.Context, session *model.Session, conversation *model.Conversation, cfg *config.Config, llmClient *llm.Client) {
	if len(conversation.Messages) <= cfg.ConversationMessageCompressThreshold {
		return
	}

	compressCount := len(conversation.Messages) - cfg.ConversationMessageKeepCount
	messagesToCompress := conversation.Messages[:compressCount]

	summary := h.generateSummary(ctx, messagesToCompress, session.Summary, llmClient)
	if summary == "" {
		log.Printf("会話内要約生成エラー: 応答が空です")
		return
	}

	conversation.Messages = conversation.Messages[compressCount:]
	session.Summary = summary

	log.Printf("会話内圧縮完了: %d件のメッセージを削除、%d件を保持", compressCount, len(conversation.Messages))
}

func (h *ConversationHistory) compressOldConversations(ctx context.Context, session *model.Session, cfg *config.Config, llmClient *llm.Client) {
	oldConversations := FindOldConversations(cfg, session)
	if len(oldConversations) == 0 {
		return
	}

	var allMessages []model.Message
	for _, conv := range oldConversations {
		allMessages = append(allMessages, conv.Messages...)
	}

	summary := h.generateSummary(ctx, allMessages, session.Summary, llmClient)
	if summary == "" {
		log.Printf("要約生成エラー: 応答が空です")
		return
	}

	UpdateSessionWithSummary(session, summary, oldConversations)
	log.Printf("履歴圧縮完了: %d件の会話を要約に移行", len(oldConversations))
}

func (h *ConversationHistory) generateSummary(ctx context.Context, messages []model.Message, existingSummary string, llmClient *llm.Client) string {
	formattedMessages := formatMessagesForSummary(messages)
	summaryPrompt := llm.BuildSummaryPrompt(formattedMessages, existingSummary)
	summaryMessages := []model.Message{{Role: "user", Content: summaryPrompt}}
	return llmClient.CallClaudeAPIForSummary(ctx, summaryMessages, existingSummary)
}

func formatMessagesForSummary(messages []model.Message) string {
	var builder strings.Builder
	for _, msg := range messages {
		role := "ユーザー"
		if msg.Role == "assistant" {
			role = "アシスタント"
		}
		builder.WriteString(role)
		builder.WriteString(": ")
		builder.WriteString(msg.Content)
		builder.WriteString("\n")
	}
	return builder.String()
}
