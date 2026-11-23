package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"claude_bot/internal/model"
)

type MemoryFactStore struct {
	mu    sync.RWMutex
	facts []model.Fact
}

func NewMemoryFactStore() *MemoryFactStore {
	return &MemoryFactStore{
		facts: []model.Fact{},
	}
}

func (s *MemoryFactStore) Add(ctx context.Context, fact model.Fact) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, existing := range s.facts {
		if existing.Target == fact.Target && existing.Key == fact.Key {
			val1 := fmt.Sprintf("%v", existing.Value)
			val2 := fmt.Sprintf("%v", fact.Value)

			if val1 == val2 {
				s.facts[i].Author = fact.Author
				s.facts[i].Timestamp = time.Now()
				if fact.SourceType != "" {
					s.facts[i].SourceType = fact.SourceType
				}
				if fact.SourceURL != "" {
					s.facts[i].SourceURL = fact.SourceURL
				}
				return nil
			}
		}
	}

	if fact.Timestamp.IsZero() {
		fact.Timestamp = time.Now()
	}
	s.facts = append(s.facts, fact)
	return nil
}

func (s *MemoryFactStore) GetByTarget(ctx context.Context, target string) ([]model.Fact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []model.Fact
	for _, fact := range s.facts {
		if fact.Target == target {
			results = append(results, fact)
		}
	}
	return results, nil
}

func (s *MemoryFactStore) GetRecent(ctx context.Context, limit int) ([]model.Fact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	facts := make([]model.Fact, len(s.facts))
	copy(facts, s.facts)

	sort.Slice(facts, func(i, j int) bool {
		return facts[i].Timestamp.After(facts[j].Timestamp)
	})

	if len(facts) <= limit {
		return facts, nil
	}
	return facts[:limit], nil
}

func (s *MemoryFactStore) SearchFuzzy(ctx context.Context, targets []string, keys []string) ([]model.Fact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []model.Fact
	for _, fact := range s.facts {
		targetMatch := false
		for _, t := range targets {
			if fact.Target == t || fact.TargetUserName == t {
				targetMatch = true
				break
			}
			if len(t) >= MinTargetUserNameFuzzyLength {
				if strings.HasPrefix(fact.TargetUserName, t) || strings.HasSuffix(fact.TargetUserName, t) {
					targetMatch = true
					break
				}
			}
		}
		if !targetMatch {
			continue
		}

		for _, key := range keys {
			if strings.Contains(fact.Key, key) || strings.Contains(key, fact.Key) {
				results = append(results, fact)
				break
			}
			if strings.HasPrefix(fact.Key, model.SystemFactKeyPrefix) {
				valStr := fmt.Sprintf("%v", fact.Value)
				if strings.Contains(valStr, key) {
					results = append(results, fact)
					break
				}
			}
		}
	}
	return results, nil
}

func (s *MemoryFactStore) Remove(ctx context.Context, target string, filter func(model.Fact) bool) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	newFacts := make([]model.Fact, 0, len(s.facts))
	deletedCount := 0

	for _, fact := range s.facts {
		if fact.Target == target && filter(fact) {
			deletedCount++
			continue
		}
		newFacts = append(newFacts, fact)
	}
	s.facts = newFacts
	return deletedCount, nil
}

func (s *MemoryFactStore) Replace(ctx context.Context, target string, remove []model.Fact, add []model.Fact) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var afterRemove []model.Fact
	toRemoveMap := make(map[string]bool)
	for _, f := range remove {
		key := fmt.Sprintf("%s:%s:%s", f.Target, f.Key, fmt.Sprint(f.Value))
		toRemoveMap[key] = true
	}

	for _, f := range s.facts {
		if f.Target == target {
			key := fmt.Sprintf("%s:%s:%s", f.Target, f.Key, fmt.Sprint(f.Value))
			if toRemoveMap[key] {
				continue
			}
		}
		afterRemove = append(afterRemove, f)
	}

	s.facts = afterRemove

	for _, f := range add {
		if f.Timestamp.IsZero() {
			f.Timestamp = time.Now()
		}

		s.facts = append(s.facts, f)
	}
	return nil
}

func (s *MemoryFactStore) GetAllFacts(ctx context.Context) ([]model.Fact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	results := make([]model.Fact, len(s.facts))
	copy(results, s.facts)
	return results, nil
}

func (s *MemoryFactStore) EnforceMaxFacts(ctx context.Context, maxFacts int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.facts) <= maxFacts {
		return 0, nil
	}

	sort.Slice(s.facts, func(i, j int) bool {
		return s.facts[i].Timestamp.After(s.facts[j].Timestamp)
	})

	deletedCount := len(s.facts) - maxFacts
	s.facts = s.facts[:maxFacts]

	return deletedCount, nil
}

func (s *MemoryFactStore) Close() error {
	return nil
}
