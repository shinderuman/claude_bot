package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"claude_bot/internal/config"
	"claude_bot/internal/model"
	"claude_bot/internal/util"
)

type FactStore struct {
	keyUpdateMu   sync.Mutex
	storage       FactStorage
	saveFilePath  string
	backupMu      sync.Mutex
	backupRunning bool
	backupPending bool
}

func InitializeFactStore(cfg *config.Config) (*FactStore, error) {
	path, err := util.DataFilePath(cfg.FactStoreFileName)
	if err != nil {
		return nil, err
	}
	storage, err := NewRedisFactStore(cfg.RedisURL, cfg.RedisFactsKey)
	if err != nil {
		return nil, fmt.Errorf("Redis初期化エラー: %w", err)
	}
	log.Printf("Redisファクトストア初期化: キー接頭辞 %s", cfg.RedisFactsKey)
	return NewFactStore(storage, path), nil
}

func NewFactStore(storage FactStorage, filePath string) *FactStore {
	return &FactStore{
		storage:      storage,
		saveFilePath: filePath,
	}
}

func (s *FactStore) AddFact(fact model.Fact) error {
	if err := s.storage.Add(context.Background(), fact); err != nil {
		return err
	}
	s.requestBackup()
	return nil
}

func (s *FactStore) requestBackup() {
	if s.saveFilePath == "" {
		return
	}
	s.backupMu.Lock()
	defer s.backupMu.Unlock()

	s.backupPending = true
	if !s.backupRunning {
		s.backupRunning = true
		go s.backupLoop()
	}
}

func (s *FactStore) backupLoop() {
	for {
		s.backupMu.Lock()
		if !s.backupPending {
			s.backupRunning = false
			s.backupMu.Unlock()
			return
		}
		s.backupPending = false
		s.backupMu.Unlock()
		s.writeBackup()
	}
}

func (s *FactStore) writeBackup() {
	facts, err := s.storage.GetAllFacts(context.Background())
	if err != nil {
		log.Printf("バックアップ用ファクト取得失敗: %v", err)
		return
	}

	if facts == nil {
		facts = []model.Fact{}
	}
	data, err := json.MarshalIndent(facts, "", "  ")
	if err != nil {
		log.Printf("ファクトバックアップ変換失敗: %v", err)
		return
	}

	if err := util.WriteFileAtomically(s.saveFilePath, data); err != nil {
		log.Printf("バックアップ保存失敗: %v", err)
	}
}

func (s *FactStore) GetFactsByTarget(ctx context.Context, target string) ([]model.Fact, error) {
	return s.storage.GetByTarget(ctx, target)
}

func (s *FactStore) GetAllFacts(ctx context.Context) ([]model.Fact, error) {
	return s.storage.GetAllFacts(ctx)
}

func (s *FactStore) ReplaceFactForKey(ctx context.Context, replacement model.Fact) error {
	s.keyUpdateMu.Lock()
	defer s.keyUpdateMu.Unlock()

	facts, err := s.storage.GetByTarget(ctx, replacement.Target)
	if err != nil {
		return err
	}
	var old []model.Fact
	for _, fact := range facts {
		if fact.Key == replacement.Key {
			old = append(old, fact)
		}
	}
	return s.ReplaceFacts(ctx, replacement.Target, old, []model.Fact{replacement})
}

func (s *FactStore) Close() error {
	return s.storage.Close()
}
