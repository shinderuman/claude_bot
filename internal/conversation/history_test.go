package conversation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/model"
)

func TestConversationHistorySerializesSameUserAndPersistsAllUpdates(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sessions.json")
	history := &History{saveFilePath: file}
	const updates = 12
	var workers sync.WaitGroup
	for i := range updates {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for _, user := range []string{"first", "second"} {
				if err := history.UpdateSession(user, func(session *model.Session) bool {
					conversation := GetOrCreateConversation(session, "root")
					AddMessage(conversation, model.RoleUser, "message", []string{fmt.Sprint(i)})
					return true
				}); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	workers.Wait()
	if err := history.UpdateSession("first", func(session *model.Session) bool {
		GetOrCreateConversation(session, "root").Messages[0].StatusIDs[0] = "discarded"
		return false
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var sessions map[string]*model.Session
	if err := json.Unmarshal(data, &sessions); err != nil {
		t.Fatal(err)
	}
	for _, user := range []string{"first", "second"} {
		session := sessions[user]
		if session == nil || len(session.Conversations) != 1 || len(session.Conversations[0].Messages) != updates {
			t.Fatalf("%s: unexpected session: %+v", user, session)
		}
	}
	if got := history.sessions["first"].Conversations[0].Messages[0].StatusIDs[0]; got == "discarded" {
		t.Fatal("uncommitted change appeared in the live session")
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
	session := createNewSession()

	conv := model.Conversation{
		RootStatusID: "root1",
		CreatedAt:    time.Now(),
		Messages:     []model.Message{},
	}

	AddMessage(&conv, "user", "Hello", []string{"id1"})

	session.Conversations = []model.Conversation{conv}

	foundConv := GetOrCreateConversation(session, "root1")
	if foundConv.RootStatusID != "root1" {
		t.Errorf("expected foundConv.RootStatusID to be 'root1', got %s", foundConv.RootStatusID)
	}
	if foundConv != &session.Conversations[0] {
		t.Errorf("expected foundConv to point to session.Conversations[0]")
	}

	AddMessage(foundConv, "assistant", "Response", []string{"id2", "id3"})

	foundConvByID := GetOrCreateConversation(session, "id2")
	if foundConvByID != foundConv {
		t.Errorf("expected to find existing conversation by Message ID 'id2'")
	}

	foundConvByID2 := GetOrCreateConversation(session, "id3")
	if foundConvByID2 != foundConv {
		t.Errorf("expected to find existing conversation by Message ID 'id3'")
	}

	newConv := GetOrCreateConversation(session, "new_root")
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

func TestOpenCreatesHistoryOnlyWhenFileIsAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	cfg := &config.Config{SessionFileName: path}
	if history, err := Open(cfg); err != nil || history == nil {
		t.Fatalf("Open for a missing history = %v, %v", history, err)
	}

	corrupt := []byte(`{"user":`)
	if err := os.WriteFile(path, corrupt, 0600); err != nil {
		t.Fatal(err)
	}
	if history, err := Open(cfg); err == nil || history != nil {
		t.Fatalf("Open for an invalid history = %v, %v", history, err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(corrupt) {
		t.Fatalf("history after failed open = %q, %v", got, err)
	}
}
