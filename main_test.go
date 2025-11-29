package main

import (
	"strings"
	"testing"
	"time"
)

func TestRemoveMentions(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "メンションを削除",
			input: "@user1 Hello World",
			want:  "Hello World",
		},
		{
			name:  "複数のメンションを削除",
			input: "@user1 @user2 Hello World",
			want:  "Hello World",
		},
		{
			name:  "メンションのみ",
			input: "@user1 @user2",
			want:  "",
		},
		{
			name:  "メンションなし",
			input: "Hello World",
			want:  "Hello World",
		},
		{
			name:  "空文字列",
			input: "",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := removeMentions(tt.input)
			if got != tt.want {
				t.Errorf("removeMentions() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestConversationHistory_GetOrCreateSession(t *testing.T) {
	history := &ConversationHistory{
		sessions: make(map[string]*Session),
	}

	userID := "test_user"

	session1 := history.getOrCreateSession(userID)
	if session1 == nil {
		t.Fatal("getOrCreateSession() returned nil")
	}

	if len(session1.Messages) != 0 {
		t.Errorf("新規セッションのメッセージ数 = %d, want 0", len(session1.Messages))
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

func TestSession_AddMessage(t *testing.T) {
	session := &Session{
		Messages:    []Message{},
		LastUpdated: time.Now().Add(-1 * time.Hour),
	}

	beforeTime := time.Now()
	session.addMessage("user", "Hello")
	afterTime := time.Now()

	if len(session.Messages) != 1 {
		t.Errorf("メッセージ数 = %d, want 1", len(session.Messages))
	}

	msg := session.Messages[0]
	if msg.Role != "user" {
		t.Errorf("Role = %q, want %q", msg.Role, "user")
	}
	if msg.Content != "Hello" {
		t.Errorf("Content = %q, want %q", msg.Content, "Hello")
	}

	if session.LastUpdated.Before(beforeTime) || session.LastUpdated.After(afterTime) {
		t.Error("LastUpdatedが更新されていない")
	}

	session.addMessage("assistant", "Hi there")
	if len(session.Messages) != 2 {
		t.Errorf("メッセージ数 = %d, want 2", len(session.Messages))
	}
}

func TestBuildMessages(t *testing.T) {
	session := &Session{
		Messages: []Message{
			{Role: "user", Content: "msg1"},
			{Role: "assistant", Content: "msg2"},
			{Role: "user", Content: "msg3"},
			{Role: "assistant", Content: "msg4"},
		},
		DetailedStart: 0,
	}

	messages := buildMessages(session)
	if len(messages) != 4 {
		t.Errorf("メッセージ数 = %d, want 4", len(messages))
	}

	session.DetailedStart = 2
	messages = buildMessages(session)
	if len(messages) != 2 {
		t.Errorf("DetailedStart=2の場合のメッセージ数 = %d, want 2", len(messages))
	}
	if messages[0].Content != "msg3" {
		t.Errorf("最初のメッセージ = %q, want %q", messages[0].Content, "msg3")
	}

	session.DetailedStart = -1
	messages = buildMessages(session)
	if len(messages) != 4 {
		t.Errorf("DetailedStart=-1の場合のメッセージ数 = %d, want 4", len(messages))
	}

	session.DetailedStart = 100
	messages = buildMessages(session)
	if len(messages) != 4 {
		t.Errorf("DetailedStart=100の場合のメッセージ数 = %d, want 4", len(messages))
	}
}

func TestBuildSystemPrompt(t *testing.T) {
	config := &Config{
		CharacterPrompt: "テストプロンプト",
	}

	session := &Session{
		Summaries: []string{},
	}

	prompt := buildSystemPrompt(config, session, true)
	expected := "IMPORTANT: Always respond in Japanese (日本語で回答してください / 请用日语回答).\n\nテストプロンプト"
	if prompt != expected {
		t.Errorf("要約なしの場合 = %q, want %q", prompt, expected)
	}

	session.Summaries = []string{"過去の会話内容"}
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
