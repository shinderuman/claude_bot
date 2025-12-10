package store

import (
	"encoding/json"
	"log"
	"os"
	"sync"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/model"
	"claude_bot/internal/utils"
)

const (
	// Conversation
	MinMessagesForIdleCheck = 4
)

type ConversationHistory struct {
	mu           sync.RWMutex
	Sessions     map[string]*model.Session
	saveFilePath string
}

func InitializeHistory(cfg *config.Config) *ConversationHistory {
	sessionsPath := utils.GetFilePath(cfg.SessionFileName)

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

	// 0644: User(RW), Group(R), Other(R)
	return os.WriteFile(h.saveFilePath, data, 0644)

}

func (h *ConversationHistory) GetOrCreateConversation(session *model.Session, rootStatusID string) *model.Conversation {
	// 1. RootStatusIDで検索
	for i := range session.Conversations {
		if session.Conversations[i].RootStatusID == rootStatusID {
			return &session.Conversations[i]
		}
	}

	// 2. メッセージ内のStatusIDで検索（会話のどこかに含まれる投稿へのリプライの場合）
	for i := range session.Conversations {
		for _, msg := range session.Conversations[i].Messages {
			for _, id := range msg.StatusIDs {
				if id == rootStatusID {
					// ヒットした場合、この会話を継続として扱う
					return &session.Conversations[i]
				}
			}
		}
	}

	// 新規会話作成
	newConv := model.Conversation{
		RootStatusID: rootStatusID,
		CreatedAt:    time.Now(),
		LastUpdated:  time.Now(),
		Messages:     []model.Message{},
	}
	session.Conversations = append(session.Conversations, newConv)
	return &session.Conversations[len(session.Conversations)-1]
}

func AddMessage(c *model.Conversation, role, content string, statusIDs []string) {
	c.Messages = append(c.Messages, model.Message{
		Role:      role,
		Content:   content,
		StatusIDs: statusIDs,
	})
	c.LastUpdated = time.Now()
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

	retentionThreshold := time.Now().Add(-time.Duration(config.ConversationRetentionHours) * time.Hour)
	idleThreshold := time.Now().Add(-time.Duration(config.ConversationIdleHours) * time.Hour)

	var oldConvs []model.Conversation

	for _, conv := range session.Conversations {
		// 最終更新日時を使用
		lastUpdated := conv.LastUpdated

		// 保持期間切れチェック (絶対的な寿命)
		isExpired := conv.CreatedAt.Before(retentionThreshold)

		// アイドル状態チェック (最後の更新からの経過時間)
		// メッセージ数が一定以上ある場合のみアイドル判定を行う（短い会話は即サマリしない）
		isIdle := false
		if len(conv.Messages) >= MinMessagesForIdleCheck { // 最低2往復程度
			isIdle = lastUpdated.Before(idleThreshold)
		}

		if isExpired || isIdle {
			oldConvs = append(oldConvs, conv)
		}
	}

	return oldConvs
}

func UpdateSessionWithSummary(session *model.Session, summary string, oldConversations []model.Conversation) {
	session.Summary = summary

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
