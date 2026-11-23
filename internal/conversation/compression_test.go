package conversation

import (
	"context"
	"strings"
	"testing"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/model"
)

type testSummarizer func(messages []model.Message, existing string) string

func (summarize testSummarizer) GenerateSummary(_ context.Context, messages []model.Message, existing string) string {
	return summarize(messages, existing)
}

func TestCompressionPreservesMostRecentExpiredConversation(t *testing.T) {
	now := time.Now()
	conversations := []model.Conversation{
		{RootStatusID: "keep", CreatedAt: now.Add(-8 * time.Hour), LastUpdated: now.Add(-2 * time.Hour), Messages: []model.Message{{Content: "recent conversation"}}},
		{RootStatusID: "archive", CreatedAt: now.Add(-8 * time.Hour), LastUpdated: now.Add(-4 * time.Hour), Messages: []model.Message{{Content: "older conversation"}}},
	}
	cfg := &config.Config{ConversationRetentionHours: 1, ConversationMinKeepCount: 1, ConversationMessageCompressThreshold: 10}

	for _, answer := range []string{"", "updated summary"} {
		session := &model.Session{Summary: "previous summary", Conversations: conversations}
		calls := 0
		summarizer := testSummarizer(func(messages []model.Message, existing string) string {
			calls++
			if existing != "previous summary" || len(messages) != 1 ||
				!strings.Contains(messages[0].Content, "older conversation") ||
				strings.Contains(messages[0].Content, "recent conversation") {
				t.Fatalf("summarization input = %+v, %q", messages, existing)
			}
			return answer
		})
		CompressHistoryIfNeeded(t.Context(), session, "user", cfg, summarizer, nil)
		if calls != 1 {
			t.Fatalf("summary generation calls = %d, want 1", calls)
		}
		if answer == "" {
			if session.Summary != "previous summary" || len(session.Conversations) != 2 {
				t.Fatalf("failed summary changed the session: %+v", session)
			}
		} else if session.Summary != answer || len(session.Conversations) != 1 || session.Conversations[0].RootStatusID != "keep" {
			t.Fatalf("unexpected session after compression: %+v", session)
		}
	}
}
