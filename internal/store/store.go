package store

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/model"
)

type FactStore struct {
	mu           sync.RWMutex
	Facts        []model.Fact
	saveFilePath string
}

type ConversationHistory struct {
	mu           sync.RWMutex
	Sessions     map[string]*model.Session
	saveFilePath string
}

func InitializeFactStore() *FactStore {
	factsPath := getFilePath("facts.json")

	store := &FactStore{
		Facts:        []model.Fact{},
		saveFilePath: factsPath,
	}

	if err := store.load(); err != nil {
		log.Printf("事実データ読み込みエラー（新規作成します）: %v", err)
	} else {
		// 起動時に古いデータを削除
		deleted := store.Cleanup(30 * 24 * time.Hour)
		log.Printf("事実データ読み込み成功: %d件 (削除: %d件, ファイル: %s)", len(store.Facts), deleted, factsPath)
	}

	return store
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

func (s *FactStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.saveFilePath)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(data, &s.Facts); err != nil {
		return err
	}

	// データ移行: Targetが空の場合はAuthorをTargetとする
	migrated := false
	for i := range s.Facts {
		if s.Facts[i].Target == "" {
			s.Facts[i].Target = s.Facts[i].Author
			migrated = true
		}
	}

	if migrated {
		log.Println("事実データの移行完了: Targetフィールドを補完しました")
		// 保存して永続化
		go s.Save()
	}

	return nil
}

func (s *FactStore) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := json.MarshalIndent(s.Facts, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(s.saveFilePath, data, 0644)
}

func (s *FactStore) Upsert(target, targetUserName, author, authorUserName, key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 既存の事実を検索して更新
	for i, fact := range s.Facts {
		if fact.Target == target && fact.Key == key {
			s.Facts[i].Value = value
			s.Facts[i].Author = author // 情報提供者を更新
			s.Facts[i].AuthorUserName = authorUserName
			if targetUserName != "" {
				s.Facts[i].TargetUserName = targetUserName
			}
			s.Facts[i].Timestamp = time.Now()
			return
		}
	}

	// 新規追加
	s.Facts = append(s.Facts, model.Fact{
		Target:         target,
		TargetUserName: targetUserName,
		Author:         author,
		AuthorUserName: authorUserName,
		Key:            key,
		Value:          value,
		Timestamp:      time.Now(),
	})
}

func (s *FactStore) Cleanup(retention time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	threshold := time.Now().Add(-retention)
	var activeFacts []model.Fact
	deletedCount := 0

	for _, fact := range s.Facts {
		if fact.Timestamp.After(threshold) {
			activeFacts = append(activeFacts, fact)
		} else {
			deletedCount++
		}
	}

	if deletedCount > 0 {
		s.Facts = activeFacts
		// 非同期で保存
		go func() {
			s.mu.RLock()
			defer s.mu.RUnlock()
			data, _ := json.MarshalIndent(s.Facts, "", "  ")
			os.WriteFile(s.saveFilePath, data, 0644)
		}()
	}

	return deletedCount
}

func (s *FactStore) SearchFuzzy(targets []string, keys []string) []model.Fact {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []model.Fact
	for _, fact := range s.Facts {
		// Targetの一致確認
		targetMatch := false
		for _, t := range targets {
			if fact.Target == t {
				targetMatch = true
				break
			}
		}
		if !targetMatch {
			continue
		}

		// Keyの部分一致確認
		for _, key := range keys {
			if strings.Contains(fact.Key, key) || strings.Contains(key, fact.Key) {
				results = append(results, fact)
				break
			}
		}
	}
	return results
}

func RunPeriodicCleanup(store *FactStore) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		deleted := store.Cleanup(30 * 24 * time.Hour)
		if deleted > 0 {
			log.Printf("定期クリーンアップ完了: %d件の古い事実を削除しました", deleted)
		}
	}
}

// ConversationHistory methods

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

func CompressHistoryIfNeeded(ctx interface{}, config *config.Config, session *model.Session) {
	// Note: ctx interface{} is a placeholder because we need context for LLM calls.
	// We might need to inject a dependency here or move this logic to bot/llm package.
	// For now, let's keep the structure but we'll need to refactor this logic out or inject LLM client.
	// Actually, compression requires LLM, so this logic belongs in `bot` or `llm` package, not pure `store`.
	// I will move the *logic* that calls LLM to `bot` package, and keep only data manipulation here.
	// So I will remove CompressHistoryIfNeeded from here and put it in `bot`.
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

// Helper
func getFilePath(filename string) string {
	// 作業ディレクトリを優先
	localPath := filepath.Join(".", filename)
	if _, err := os.Stat(localPath); err == nil {
		return localPath
	}

	// 実行ファイルディレクトリを fallback
	exePath, err := os.Executable()
	if err != nil {
		log.Fatal("実行ファイルパス取得エラー: ", err)
	}
	exeDir := filepath.Dir(exePath)
	return filepath.Join(exeDir, filename)
}
