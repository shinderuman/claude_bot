package store

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/gofrs/flock"

	"claude_bot/internal/config"
	"claude_bot/internal/model"
	"claude_bot/internal/slack"
	"claude_bot/internal/util"
)

const MinTargetUserNameFuzzyLength = 5

type FactStore struct {
	mu           sync.RWMutex
	fileLock     *flock.Flock
	Facts        []model.Fact
	saveFilePath string
	lastModTime  time.Time
	slackClient  *slack.Client
}

func InitializeFactStore(cfg *config.Config, slackClient *slack.Client) *FactStore {
	factsPath := util.GetFilePath(cfg.FactStoreFileName)
	return NewFactStore(factsPath, slackClient)
}

// NewFactStore creates a new FactStore with a custom file path
func NewFactStore(filePath string, slackClient *slack.Client) *FactStore {
	store := &FactStore{
		fileLock:     flock.New(filePath + ".lock"),
		Facts:        []model.Fact{},
		saveFilePath: filePath,
		slackClient:  slackClient,
	}

	if err := store.load(); err != nil {
		log.Printf("事実データ読み込みエラー（新規作成します）: %v", err)
	} else {
		// 起動時に古いデータを削除
		deleted := store.Cleanup(30 * 24 * time.Hour)
		log.Printf("事実データ読み込み成功: %d件 (削除: %d件, ファイル: %s)", len(store.Facts), deleted, filePath)
	}

	return store
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
	s.mu.Lock()
	defer s.mu.Unlock()

	// メモリ上での重複チェック（完全一致のみ排除）
	// ディスクとのマージはSave時に行われるため、メモリ内の重複だけ防ぐ
	for i, existing := range s.Facts {
		// TargetとKeyが一致し、かつValueも一致する場合のみ更新（Timestamp更新）
		if existing.Target == fact.Target && existing.Key == fact.Key {
			// Valueの比較
			val1 := fmt.Sprintf("%v", existing.Value)
			val2 := fmt.Sprintf("%v", fact.Value)

			if val1 == val2 {
				// 完全一致なら更新扱いで維持（新しいメタデータを反映）
				s.Facts[i].Author = fact.Author
				s.Facts[i].Timestamp = time.Now()
				if fact.SourceType != "" {
					s.Facts[i].SourceType = fact.SourceType
				}
				if fact.SourceURL != "" {
					s.Facts[i].SourceURL = fact.SourceURL
				}
				return
			}
		}
	}

	// 新規追加（Valueが違うなら別ファクトとして追記）
	if fact.Timestamp.IsZero() {
		fact.Timestamp = time.Now()
	}
	s.Facts = append(s.Facts, fact)
}

// GetFactsByTarget gets all facts for a specific target
func (s *FactStore) GetFactsByTarget(target string) []model.Fact {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []model.Fact
	for _, fact := range s.Facts {
		if fact.Target == target {
			results = append(results, fact)
		}
	}
	return results
}

// GetAllFacts returns a copy of all facts in the store (thread-safe)
func (s *FactStore) GetAllFacts() []model.Fact {
	s.mu.RLock()
	defer s.mu.RUnlock()

	results := make([]model.Fact, len(s.Facts))
	copy(results, s.Facts)
	return results
}

// RemoveFactsByKey removes all facts matching the target and key
func (s *FactStore) RemoveFactsByKey(target, key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	var newFacts []model.Fact
	count := 0
	for _, fact := range s.Facts {
		if fact.Target == target && fact.Key == key {
			count++
			continue
		}
		newFacts = append(newFacts, fact)
	}
	s.Facts = newFacts
	return count
}
