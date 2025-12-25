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

// MemoryFactStore is an in-memory implementation of FactStorage for testing
type MemoryFactStore struct {
	mu    sync.RWMutex
	facts []model.Fact
}

// NewMemoryFactStore creates a new MemoryFactStore
func NewMemoryFactStore() *MemoryFactStore {
	return &MemoryFactStore{
		facts: []model.Fact{},
	}
}

// Add adds a new fact or updates an existing one
func (s *MemoryFactStore) Add(ctx context.Context, fact model.Fact) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Logic matches FactStore.AddFactWithSource
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

// GetByTarget returns all facts for a specific target
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

// GetRecent returns the most recent n facts
func (s *MemoryFactStore) GetRecent(ctx context.Context, limit int) ([]model.Fact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Create a copy to sort
	facts := make([]model.Fact, len(s.facts))
	copy(facts, s.facts)

	// Sort by timestamp descending
	sort.Slice(facts, func(i, j int) bool {
		return facts[i].Timestamp.After(facts[j].Timestamp)
	})

	if len(facts) <= limit {
		return facts, nil
	}
	return facts[:limit], nil
}

// SearchFuzzy searches facts based on targets and keys
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
			if strings.HasPrefix(fact.Key, "system:") {
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

// Remove removes facts based on a filter function
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

// Replace replaces specific facts for a target atomically
func (s *MemoryFactStore) Replace(ctx context.Context, target string, remove []model.Fact, add []model.Fact) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. Remove
	var afterRemove []model.Fact
	// Map to verify equality for removal
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

	// 2. Add
	for _, f := range add {
		if f.Timestamp.IsZero() {
			f.Timestamp = time.Now()
		}
		// Basic append, ignoring deduplication logic.

		s.facts = append(s.facts, f)
	}
	return nil
}

// GetAllFacts returns all facts
func (s *MemoryFactStore) GetAllFacts(ctx context.Context) ([]model.Fact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	results := make([]model.Fact, len(s.facts))
	copy(results, s.facts)
	return results, nil
}

// EnforceMaxFacts keeps only the most recent maxFacts facts, removing older ones
func (s *MemoryFactStore) EnforceMaxFacts(ctx context.Context, maxFacts int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.facts) <= maxFacts {
		return 0, nil
	}

	// Sort by timestamp descending (newest first)
	// We want to keep the first maxFacts.
	sort.Slice(s.facts, func(i, j int) bool {
		return s.facts[i].Timestamp.After(s.facts[j].Timestamp)
	})

	deletedCount := len(s.facts) - maxFacts
	s.facts = s.facts[:maxFacts]

	return deletedCount, nil
}

// Close no-op
func (s *MemoryFactStore) Close() error {
	return nil
}
