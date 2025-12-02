package store

import (
	"encoding/json"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"claude_bot/internal/model"
)

type FactStore struct {
	mu           sync.RWMutex
	Facts        []model.Fact
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

// Upsert は既存のメソッド(後方互換性のため)
func (s *FactStore) Upsert(target, targetUserName, author, authorUserName, key string, value interface{}) {
	s.UpsertWithSource(model.Fact{
		Target:         target,
		TargetUserName: targetUserName,
		Author:         author,
		AuthorUserName: authorUserName,
		Key:            key,
		Value:          value,
		Timestamp:      time.Now(),
		SourceType:     "mention", // デフォルトはメンション
	})
}

// UpsertWithSource はソース情報を含むFactを保存します
func (s *FactStore) UpsertWithSource(fact model.Fact) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 既存の事実を検索して更新
	for i, existing := range s.Facts {
		if existing.Target == fact.Target && existing.Key == fact.Key {
			s.Facts[i].Value = fact.Value
			s.Facts[i].Author = fact.Author
			s.Facts[i].AuthorUserName = fact.AuthorUserName
			if fact.TargetUserName != "" {
				s.Facts[i].TargetUserName = fact.TargetUserName
			}
			s.Facts[i].Timestamp = time.Now()
			// ソース情報も更新
			if fact.SourceType != "" {
				s.Facts[i].SourceType = fact.SourceType
			}
			if fact.SourceURL != "" {
				s.Facts[i].SourceURL = fact.SourceURL
			}
			if fact.PostAuthor != "" {
				s.Facts[i].PostAuthor = fact.PostAuthor
			}
			if fact.PostAuthorUserName != "" {
				s.Facts[i].PostAuthorUserName = fact.PostAuthorUserName
			}
			return
		}
	}

	// 新規追加
	if fact.Timestamp.IsZero() {
		fact.Timestamp = time.Now()
	}
	s.Facts = append(s.Facts, fact)
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
