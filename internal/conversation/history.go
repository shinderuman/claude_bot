package conversation

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"slices"
	"sort"
	"sync"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/model"
	"claude_bot/internal/util"
)

const (
	MinMessagesForIdleCheck = 4
)

type History struct {
	mu           sync.RWMutex
	sessions     map[string]*model.Session
	saveFilePath string
	gateMu       sync.Mutex
	gates        map[string]*sessionGate
}

type sessionGate struct {
	mu    sync.Mutex
	users int
}

func Open(cfg *config.Config) (*History, error) {
	sessionsPath, err := util.DataFilePath(cfg.SessionFileName)
	if err != nil {
		return nil, err
	}
	history := &History{
		sessions:     make(map[string]*model.Session),
		saveFilePath: sessionsPath,
	}
	if err := history.load(); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return history, nil
		}
		return nil, fmt.Errorf("会話履歴を読み込めません (%s): %w", sessionsPath, err)
	}
	log.Printf("履歴読み込み成功: %d件のセッション (ファイル: %s)", len(history.sessions), sessionsPath)
	return history, nil
}

func (h *History) lockSession(userID string) func() {
	h.gateMu.Lock()
	if h.gates == nil {
		h.gates = make(map[string]*sessionGate)
	}
	gate := h.gates[userID]
	if gate == nil {
		gate = &sessionGate{}
		h.gates[userID] = gate
	}
	gate.users++
	h.gateMu.Unlock()

	gate.mu.Lock()
	return func() {
		gate.mu.Unlock()
		h.gateMu.Lock()
		defer h.gateMu.Unlock()
		gate.users--
		if gate.users == 0 {
			delete(h.gates, userID)
		}
	}
}

func (h *History) UpdateSession(userID string, update func(*model.Session) bool) error {
	unlock := h.lockSession(userID)
	defer unlock()

	h.mu.RLock()
	session := cloneSession(h.sessions[userID])
	h.mu.RUnlock()
	session.LastUpdated = time.Now()
	if !update(session) {
		return nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessions == nil {
		h.sessions = make(map[string]*model.Session)
	}
	h.sessions[userID] = cloneSession(session)
	data, err := json.MarshalIndent(h.sessions, "", "  ")
	if err != nil {
		return err
	}
	return util.WriteFileAtomically(h.saveFilePath, data)
}

func cloneSession(src *model.Session) *model.Session {
	if src == nil {
		return createNewSession()
	}
	session := *src
	session.Conversations = slices.Clone(src.Conversations)
	for i := range session.Conversations {
		conversation := &session.Conversations[i]
		conversation.Messages = slices.Clone(conversation.Messages)
		for j := range conversation.Messages {
			conversation.Messages[j].StatusIDs = slices.Clone(conversation.Messages[j].StatusIDs)
		}
	}
	return &session
}

func createNewSession() *model.Session {
	return &model.Session{
		Conversations: []model.Conversation{},
		Summary:       "",
		LastUpdated:   time.Now(),
	}
}

func (h *History) load() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	data, err := os.ReadFile(h.saveFilePath)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, &h.sessions)
}

func (h *History) GetStats() (int, int64) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	count := len(h.sessions)
	var size int64
	if stat, err := os.Stat(h.saveFilePath); err == nil {
		size = stat.Size()
	}

	return count, size
}

func GetOrCreateConversation(session *model.Session, rootStatusID string) *model.Conversation {
	for i := range session.Conversations {
		if session.Conversations[i].RootStatusID == rootStatusID {
			return &session.Conversations[i]
		}
	}

	for i := range session.Conversations {
		for _, msg := range session.Conversations[i].Messages {
			for _, id := range msg.StatusIDs {
				if id == rootStatusID {
					return &session.Conversations[i]
				}
			}
		}
	}

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
		StatusIDs: slices.Clone(statusIDs),
	})
	c.LastUpdated = time.Now()
}

func findOldConversations(cfg *config.Config, session *model.Session) []model.Conversation {
	compressLimit := len(session.Conversations) - max(0, cfg.ConversationMinKeepCount)
	if compressLimit <= 0 {
		return nil
	}

	now := time.Now()
	retentionThreshold := now.Add(-time.Duration(cfg.ConversationRetentionHours) * time.Hour)
	idleThreshold := now.Add(-time.Duration(cfg.ConversationIdleHours) * time.Hour)

	var oldConvs []model.Conversation

	for _, conv := range session.Conversations {
		lastUpdated := conv.LastUpdated

		isExpired := conv.CreatedAt.Before(retentionThreshold)

		isIdle := false
		if len(conv.Messages) >= MinMessagesForIdleCheck {
			isIdle = lastUpdated.Before(idleThreshold)
		}

		if isExpired || isIdle {
			oldConvs = append(oldConvs, conv)
		}
	}
	if len(oldConvs) > compressLimit {
		sort.SliceStable(oldConvs, func(i, j int) bool {
			return oldConvs[i].LastUpdated.Before(oldConvs[j].LastUpdated)
		})
		return oldConvs[:compressLimit]
	}

	return oldConvs
}

func updateSessionWithSummary(session *model.Session, summary string, oldConversations []model.Conversation) {
	session.Summary = summary

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
