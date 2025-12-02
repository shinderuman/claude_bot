package bot

import (
	"context"
	"log"
	"strings"

	"claude_bot/internal/llm"
	"claude_bot/internal/model"
	"claude_bot/internal/store"
)

// Conversation compression and summarization

func (b *Bot) compressHistoryIfNeeded(ctx context.Context, session *model.Session) {
	for i := range session.Conversations {
		b.compressConversationIfNeeded(ctx, session, &session.Conversations[i])
	}

	b.compressOldConversations(ctx, session)
}

func (b *Bot) compressConversationIfNeeded(ctx context.Context, session *model.Session, conversation *model.Conversation) {
	if len(conversation.Messages) <= b.config.ConversationMessageCompressThreshold {
		return
	}

	compressCount := len(conversation.Messages) - b.config.ConversationMessageKeepCount
	messagesToCompress := conversation.Messages[:compressCount]

	summary := b.generateSummary(ctx, messagesToCompress, "")
	if summary == "" {
		log.Printf("会話内要約生成エラー: 応答が空です")
		return
	}

	conversation.Messages = conversation.Messages[compressCount:]
	if session.Summary == "" {
		session.Summary = summary
	} else {
		session.Summary = session.Summary + "\n\n" + summary
	}

	log.Printf("会話内圧縮完了: %d件のメッセージを削除、%d件を保持", compressCount, len(conversation.Messages))
}

func (b *Bot) compressOldConversations(ctx context.Context, session *model.Session) {
	oldConversations := store.FindOldConversations(b.config, session)
	if len(oldConversations) == 0 {
		return
	}

	var allMessages []model.Message
	for _, conv := range oldConversations {
		allMessages = append(allMessages, conv.Messages...)
	}

	summary := b.generateSummary(ctx, allMessages, session.Summary)
	if summary == "" {
		log.Printf("要約生成エラー: 応答が空です")
		return
	}

	store.UpdateSessionWithSummary(session, summary, oldConversations)
	log.Printf("履歴圧縮完了: %d件の会話を要約に移行", len(oldConversations))
}

func (b *Bot) generateSummary(ctx context.Context, messages []model.Message, existingSummary string) string {
	formattedMessages := formatMessagesForSummary(messages)
	summaryPrompt := llm.BuildSummaryPrompt(formattedMessages, existingSummary)
	summaryMessages := []model.Message{{Role: "user", Content: summaryPrompt}}
	return b.llmClient.CallClaudeAPIForSummary(ctx, summaryMessages, existingSummary)
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
