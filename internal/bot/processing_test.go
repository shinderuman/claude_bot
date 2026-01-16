package bot

import (
	"context"
	"testing"

	"claude_bot/internal/config"
	"claude_bot/internal/model"

	gomastodon "github.com/mattn/go-mastodon"
)

func TestPrepareConversation(t *testing.T) {
	// Setup
	cfg := &config.Config{BotUsername: "bot"}

	b := &Bot{
		config:         cfg,
		mastodonClient: nil,
	}

	// Case 1: Simple message, no parent, no URLs
	ctx := context.Background()
	conv := &model.Conversation{Messages: []model.Message{}}
	notif := &gomastodon.Notification{
		Status: &gomastodon.Status{
			Content:     "Hello",
			InReplyToID: nil,
		},
	}
	userMsg := "Hello"
	statusID := "123"

	resultMsg := b.prepareConversation(ctx, conv, notif, userMsg, statusID)

	if resultMsg != "Hello" {
		t.Errorf("Expected 'Hello', got %q", resultMsg)
	}
	if len(conv.Messages) != 1 {
		t.Errorf("Expected 1 message added, got %d", len(conv.Messages))
	}
	if conv.Messages[0].Content != "Hello" {
		t.Errorf("Message content mismatch")
	}
}
