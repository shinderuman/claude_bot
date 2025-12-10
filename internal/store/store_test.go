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

	AddMessage(conversation, "user", "Hello", []string{"msg1"})

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
	if len(msg.StatusIDs) != 1 || msg.StatusIDs[0] != "msg1" {
		t.Errorf("IDs = %v, want %v", msg.StatusIDs, []string{"msg1"})
	}

	AddMessage(conversation, "assistant", "Hi there", []string{"msg2", "msg3"})
	if len(conversation.Messages) != 2 {
		t.Errorf("メッセージ数 = %d, want 2", len(conversation.Messages))
	}
	msg2 := conversation.Messages[1]
	if len(msg2.StatusIDs) != 2 || msg2.StatusIDs[0] != "msg2" || msg2.StatusIDs[1] != "msg3" {
		t.Errorf("IDs = %v, want %v", msg2.StatusIDs, []string{"msg2", "msg3"})
	}
}

func TestConversationHistory_GetOrCreateConversation(t *testing.T) {
	history := &ConversationHistory{
		Sessions: make(map[string]*model.Session),
	}
	userID := "test_user"
	session := history.GetOrCreateSession(userID)

	conv := model.Conversation{
		RootStatusID: "root1",
		CreatedAt:    time.Now(),
		Messages:     []model.Message{},
	}

	// Add initial message
	AddMessage(&conv, "user", "Hello", []string{"id1"})

	// Initialize session with this conversation
	session.Conversations = []model.Conversation{conv}

	// Test case 1: Find conversation by RootStatusID
	foundConv := history.GetOrCreateConversation(session, "root1")
	if foundConv.RootStatusID != "root1" {
		t.Errorf("expected foundConv.RootStatusID to be 'root1', got %s", foundConv.RootStatusID)
	}
	// Verify it points to the element in the slice
	if foundConv != &session.Conversations[0] {
		t.Errorf("expected foundConv to point to session.Conversations[0]")
	}

	// Test case 2: Find conversation by Message ID
	// Add a message with specific IDs TO THE FOUND CONVERSATION (in session)
	AddMessage(foundConv, "assistant", "Response", []string{"id2", "id3"})

	foundConvByID := history.GetOrCreateConversation(session, "id2")
	if foundConvByID != foundConv {
		t.Errorf("expected to find existing conversation by Message ID 'id2'")
	}

	foundConvByID2 := history.GetOrCreateConversation(session, "id3")
	if foundConvByID2 != foundConv {
		t.Errorf("expected to find existing conversation by Message ID 'id3'")
	}

	// Test case 3: Create new conversation
	newConv := history.GetOrCreateConversation(session, "new_root")
	if newConv == foundConv {
		t.Errorf("expected to create a new conversation for 'new_root', got same old one")
	}
	if newConv.RootStatusID != "new_root" {
		t.Errorf("new conversation RootStatusID = %q, want %q", newConv.RootStatusID, "new_root")
	}
	if len(session.Conversations) != 2 {
		t.Errorf("expected 2 conversations in session, got %d", len(session.Conversations))
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
