package main

import (
	"strings"
	"testing"
	"time"
)

func TestConversationHistory_GetOrCreateSession(t *testing.T) {
	history := &ConversationHistory{
		sessions: make(map[string]*Session),
	}

	userID := "test_user"

	session1 := history.getOrCreateSession(userID)
	if session1 == nil {
		t.Fatal("getOrCreateSession() returned nil")
	}

	if len(session1.Conversations) != 0 {
		t.Errorf("新規セッションの会話数 = %d, want 0", len(session1.Conversations))
	}

	session2 := history.getOrCreateSession(userID)
	if session1 != session2 {
		t.Error("同じユーザーIDで異なるセッションが返された")
	}

	otherUserID := "other_user"
	session3 := history.getOrCreateSession(otherUserID)
	if session1 == session3 {
		t.Error("異なるユーザーIDで同じセッションが返された")
	}
}

func TestConversation_AddMessage(t *testing.T) {
	conversation := &Conversation{
		RootStatusID: "test123",
		CreatedAt:    time.Now(),
		Messages:     []Message{},
	}

	conversation.addMessage("user", "Hello")

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

	conversation.addMessage("assistant", "Hi there")
	if len(conversation.Messages) != 2 {
		t.Errorf("メッセージ数 = %d, want 2", len(conversation.Messages))
	}
}

func TestBuildSystemPrompt(t *testing.T) {
	config := &Config{
		CharacterPrompt: "テストプロンプト",
	}

	session := &Session{
		Conversations: []Conversation{},
		Summary:       "",
		LastUpdated:   time.Now(),
	}

	prompt := buildSystemPrompt(config, session, true)
	expected := "IMPORTANT: Always respond in Japanese (日本語で回答してください / 请用日语回答).\n\nテストプロンプト"
	if prompt != expected {
		t.Errorf("要約なしの場合 = %q, want %q", prompt, expected)
	}

	session.Summary = "過去の会話内容"
	prompt = buildSystemPrompt(config, session, true)
	expected = "IMPORTANT: Always respond in Japanese (日本語で回答してください / 请用日语回答).\n\nテストプロンプト\n\n【過去の会話要約】\n過去の会話内容\n\n"
	if prompt != expected {
		t.Errorf("要約ありの場合 = %q, want %q", prompt, expected)
	}
}

func TestSplitResponse(t *testing.T) {
	mention := "@user "

	tests := []struct {
		name     string
		response string
		want     int
	}{
		{
			name:     "短い応答",
			response: "こんにちは",
			want:     1,
		},
		{
			name:     "480文字以内",
			response: strings.Repeat("あ", 470),
			want:     1,
		},
		{
			name:     "改行で分割可能",
			response: strings.Repeat("あ", 470) + "\n" + strings.Repeat("い", 100),
			want:     2,
		},
		{
			name:     "改行なしで長い",
			response: strings.Repeat("あ", 1000),
			want:     3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parts := splitResponse(tt.response, mention)
			if len(parts) != tt.want {
				t.Errorf("splitResponse() = %d parts, want %d parts", len(parts), tt.want)
			}

			for i, part := range parts {
				contentLen := len([]rune(mention + part))
				if contentLen > 500 {
					t.Errorf("part %d length = %d, exceeds 500 characters", i, contentLen)
				}
			}
		})
	}
}

func TestFindLastNewline(t *testing.T) {
	tests := []struct {
		name  string
		runes []rune
		start int
		end   int
		want  int
	}{
		{
			name:  "改行あり",
			runes: []rune("あいう\nえお"),
			start: 0,
			end:   5,
			want:  3,
		},
		{
			name:  "改行なし",
			runes: []rune("あいうえお"),
			start: 0,
			end:   5,
			want:  -1,
		},
		{
			name:  "複数改行",
			runes: []rune("あ\nい\nう"),
			start: 0,
			end:   5,
			want:  3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findLastNewline(tt.runes, tt.start, tt.end)
			if got != tt.want {
				t.Errorf("findLastNewline() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestConversation_RollbackLastMessage(t *testing.T) {
	conversation := &Conversation{
		RootStatusID: "test123",
		CreatedAt:    time.Now(),
		Messages: []Message{
			{Role: "user", Content: "message1"},
			{Role: "assistant", Content: "response1"},
			{Role: "user", Content: "message2"},
		},
	}

	conversation.rollbackLastMessages(1)

	if len(conversation.Messages) != 2 {
		t.Errorf("rollbackLastMessages(1) length = %d, want 2", len(conversation.Messages))
	}

	if conversation.Messages[1].Content != "response1" {
		t.Errorf("rollbackLastMessages(1) last message = %q, want %q", conversation.Messages[1].Content, "response1")
	}
}

func TestConversation_RollbackLastMessages(t *testing.T) {
	conversation := &Conversation{
		RootStatusID: "test123",
		CreatedAt:    time.Now(),
		Messages: []Message{
			{Role: "user", Content: "message1"},
			{Role: "assistant", Content: "response1"},
			{Role: "user", Content: "message2"},
			{Role: "assistant", Content: "response2"},
		},
	}

	conversation.rollbackLastMessages(2)

	if len(conversation.Messages) != 2 {
		t.Errorf("rollbackLastMessages() length = %d, want 2", len(conversation.Messages))
	}

	if conversation.Messages[1].Content != "response1" {
		t.Errorf("rollbackLastMessages() last message = %q, want %q", conversation.Messages[1].Content, "response1")
	}
}

func TestConversation_RollbackEmptyConversation(t *testing.T) {
	conversation := &Conversation{
		RootStatusID: "test123",
		CreatedAt:    time.Now(),
		Messages:     []Message{},
	}

	conversation.rollbackLastMessages(1)

	if len(conversation.Messages) != 0 {
		t.Errorf("rollbackLastMessages(1) on empty conversation length = %d, want 0", len(conversation.Messages))
	}
}
