package store

import (
	"testing"
	"time"

	"claude_bot/internal/model"
)

func TestConversationHistory_GetOrCreateSession(t *testing.T) {
	history := &ConversationHistory{
		Sessions: make(map[string]*model.Session),
	}

	userID := "test_user"

	session1 := history.GetOrCreateSession(userID)
	if session1 == nil {
		t.Fatal("GetOrCreateSession() returned nil")
	}

	if len(session1.Conversations) != 0 {
		t.Errorf("新規セッションの会話数 = %d, want 0", len(session1.Conversations))
	}

	session2 := history.GetOrCreateSession(userID)
	if session1 != session2 {
		t.Error("同じユーザーIDで異なるセッションが返された")
	}

	otherUserID := "other_user"
	session3 := history.GetOrCreateSession(otherUserID)
	if session1 == session3 {
		t.Error("異なるユーザーIDで同じセッションが返された")
	}
}

func TestConversation_AddMessage(t *testing.T) {
	conversation := &model.Conversation{
		RootStatusID: "test123",
		CreatedAt:    time.Now(),
		Messages:     []model.Message{},
	}

	AddMessage(conversation, "user", "Hello")

	if len(conversation.Messages) != 1 {
		t.Errorf("メッセージ数 = %d, want 1", len(conversation.Messages))
	}

	msg := conversation.Messages[0]
	if msg.Role != "user" {
		t.Errorf("Role = %q, want %q", msg.Role, "user")
	}
	if msg.Content != "Hello" {
		t.Errorf("Content = %q, want %q", msg.Content, "Hello")
	}

	AddMessage(conversation, "assistant", "Hi there")
	if len(conversation.Messages) != 2 {
		t.Errorf("メッセージ数 = %d, want 2", len(conversation.Messages))
	}
}

func TestConversation_RollbackLastMessages(t *testing.T) {
	conversation := &model.Conversation{
		RootStatusID: "test123",
		CreatedAt:    time.Now(),
		Messages: []model.Message{
			{Role: "user", Content: "message1"},
			{Role: "assistant", Content: "response1"},
			{Role: "user", Content: "message2"},
			{Role: "assistant", Content: "response2"},
		},
	}

	RollbackLastMessages(conversation, 2)

	if len(conversation.Messages) != 2 {
		t.Errorf("RollbackLastMessages() length = %d, want 2", len(conversation.Messages))
	}

	if conversation.Messages[1].Content != "response1" {
		t.Errorf("RollbackLastMessages() last message = %q, want %q", conversation.Messages[1].Content, "response1")
	}
}

func TestConversation_RollbackEmptyConversation(t *testing.T) {
	conversation := &model.Conversation{
		RootStatusID: "test123",
		CreatedAt:    time.Now(),
		Messages:     []model.Message{},
	}

	RollbackLastMessages(conversation, 1)

	if len(conversation.Messages) != 0 {
		t.Errorf("RollbackLastMessages(1) on empty conversation length = %d, want 0", len(conversation.Messages))
	}
}
