package store

import (
	"encoding/json"
	"log"
	"os"
	"sync"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/model"
)

type ConversationHistory struct {
	mu           sync.RWMutex
	Sessions     map[string]*model.Session
	saveFilePath string
}

func InitializeHistory() *ConversationHistory {
	sessionsPath := getFilePath("sessions.json")

	history := &ConversationHistory{
		Sessions:     make(map[string]*model.Session),
		saveFilePath: sessionsPath,
	}

	if err := history.load(); err != nil {
		log.Printf("履歴読み込みエラー（新規作成します）: %v", err)
	} else {
		log.Printf("履歴読み込み成功: %d件のセッション (ファイル: %s)", len(history.Sessions), sessionsPath)
	}

	return history
}

func (h *ConversationHistory) GetOrCreateSession(userID string) *model.Session {
	h.mu.Lock()
	defer h.mu.Unlock()

	if session, exists := h.Sessions[userID]; exists {
		session.LastUpdated = time.Now()
		return session
	}

	session := createNewSession()
	h.Sessions[userID] = session
	return session
}

func createNewSession() *model.Session {
	return &model.Session{
		Conversations: []model.Conversation{},
		Summary:       "",
		LastUpdated:   time.Now(),
	}
}

func (h *ConversationHistory) load() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	data, err := os.ReadFile(h.saveFilePath)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, &h.Sessions)
}

func (h *ConversationHistory) Save() error {
	h.mu.RLock()
	defer h.mu.RUnlock()

	data, err := json.MarshalIndent(h.Sessions, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(h.saveFilePath, data, 0644)
}

func (h *ConversationHistory) GetOrCreateConversation(session *model.Session, rootStatusID string) *model.Conversation {
	for i := range session.Conversations {
		if session.Conversations[i].RootStatusID == rootStatusID {
			return &session.Conversations[i]
		}
	}

	newConv := model.Conversation{
		RootStatusID: rootStatusID,
		CreatedAt:    time.Now(),
		Messages:     []model.Message{},
	}
	session.Conversations = append(session.Conversations, newConv)
	return &session.Conversations[len(session.Conversations)-1]
}

func AddMessage(c *model.Conversation, role, content string) {
	c.Messages = append(c.Messages, model.Message{
		Role:    role,
		Content: content,
	})
}

func RollbackLastMessages(c *model.Conversation, count int) {
	if len(c.Messages) >= count {
		c.Messages = c.Messages[:len(c.Messages)-count]
	}
}

func FindOldConversations(config *config.Config, session *model.Session) []model.Conversation {
	if len(session.Conversations) <= config.ConversationMinKeepCount {
		return nil
	}

	threshold := time.Now().Add(-time.Duration(config.ConversationRetentionHours) * time.Hour)
	var oldConvs []model.Conversation

	for _, conv := range session.Conversations {
		if conv.CreatedAt.Before(threshold) {
			oldConvs = append(oldConvs, conv)
		}
	}

	return oldConvs
}

func UpdateSessionWithSummary(session *model.Session, summary string, oldConversations []model.Conversation) {
	if session.Summary == "" {
		session.Summary = summary
	} else {
		session.Summary = session.Summary + "\n\n" + summary
	}

	// Remove old conversations
	// This is a bit tricky because we need to remove specific conversations from the slice.
	// A simple way is to rebuild the slice.
	var keepConvs []model.Conversation
	oldMap := make(map[string]bool)
	for _, c := range oldConversations {
		oldMap[c.RootStatusID] = true
	}

	for _, c := range session.Conversations {
		if !oldMap[c.RootStatusID] {
			keepConvs = append(keepConvs, c)
		}
	}
	session.Conversations = keepConvs
}
