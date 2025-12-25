package store

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/model"
	"claude_bot/internal/slack"
	"claude_bot/internal/util"
)

type FactStore struct {
	storage      FactStorage
	slackClient  *slack.Client
	saveFilePath string
	mu           sync.Mutex
}

func InitializeFactStore(cfg *config.Config, slackClient *slack.Client) *FactStore {

	storage, err := NewRedisFactStore(cfg.RedisURL, cfg.RedisFactsKey)
	if err != nil {
		log.Fatalf("Redis初期化エラー: %v", err)
	}

	log.Printf("Redis FactStore initialized (URL: %s, KeyPrefix: %s)", cfg.RedisURL, cfg.RedisFactsKey)

	filePath := cfg.FactStoreFileName

	finalPath := filePath
	if !filepath.IsAbs(finalPath) {
		finalPath = util.GetFilePath(finalPath)
	}

	return NewFactStore(storage, slackClient, finalPath)
}

// NewFactStore creates a new FactStore
func NewFactStore(storage FactStorage, slackClient *slack.Client, filePath string) *FactStore {
	return &FactStore{
		storage:      storage,
		slackClient:  slackClient,
		saveFilePath: filePath,
	}
}

// AddFact は引数からFact構造体を生成して追加するヘルパーメソッドです
func (s *FactStore) AddFact(target, targetUserName, author, authorUserName, key string, value interface{}) {
	s.AddFactWithSource(model.Fact{
		Target:         target,
		TargetUserName: targetUserName,
		Author:         author,
		AuthorUserName: authorUserName,
		Key:            key,
		Value:          value,
		Timestamp:      time.Now(),
		SourceType:     model.SourceTypeMention, // デフォルトはメンション
	})
}

// AddFactWithSource はソース情報を含むFactを保存します
func (s *FactStore) AddFactWithSource(fact model.Fact) {
	err := s.storage.Add(context.Background(), fact)
	if err != nil {
		log.Printf("Error adding fact: %v", err)
	} else {
		go s.saveAsync()
	}
}

func (s *FactStore) saveAsync() {
	s.mu.Lock()
	defer s.mu.Unlock()

	facts, err := s.storage.GetAllFacts(context.Background())
	if err != nil {
		log.Printf("Backup failed: could not fetch facts: %v", err)
		return
	}

	// Serialize
	data, err := json.MarshalIndent(facts, "", "  ")
	if err != nil {
		log.Printf("Backup failed: marshal error: %v", err)
		return
	}

	// Write Atomically
	tmpFile := s.saveFilePath + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		log.Printf("Backup failed: write error: %v", err)
		return
	}

	if err := os.Rename(tmpFile, s.saveFilePath); err != nil {
		log.Printf("Backup failed: rename error: %v", err)
	}
}

// GetFactsByTarget gets all facts for a specific target
func (s *FactStore) GetFactsByTarget(target string) []model.Fact {
	facts, err := s.storage.GetByTarget(context.Background(), target)
	if err != nil {
		log.Printf("Error getting facts by target %s: %v", target, err)
		return []model.Fact{}
	}
	return facts
}

// GetAllFacts returns a copy of all facts in the store (thread-safe)
func (s *FactStore) GetAllFacts() []model.Fact {
	facts, err := s.storage.GetAllFacts(context.Background())
	if err != nil {
		log.Printf("Error getting all facts: %v", err)
		return []model.Fact{}
	}
	return facts
}

// RemoveFactsByKey removes all facts matching the target and key
func (s *FactStore) RemoveFactsByKey(target, key string) int {
	count, err := s.storage.Remove(context.Background(), target, func(f model.Fact) bool {
		return f.Key == key
	})
	if err != nil {
		log.Printf("Error removing facts by key %s %s: %v", target, key, err)
		return 0
	}
	if count > 0 {
		go s.saveAsync()
	}
	return count
}

// Methods for maintenance.go and query.go support
func (s *FactStore) GetStorage() FactStorage {
	return s.storage
}
