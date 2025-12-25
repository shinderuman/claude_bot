package store

import (
	"claude_bot/internal/model"
	"context"
	"log"
)

// SearchFuzzy はファクトの曖昧検索を行います
func (s *FactStore) SearchFuzzy(targets []string, keys []string) []model.Fact {
	results, err := s.storage.SearchFuzzy(context.Background(), targets, keys)
	if err != nil {
		log.Printf("SearchFuzzy error: %v", err)
		return []model.Fact{}
	}
	return results
}

// GetRecentFacts は最新のファクトを取得します
func (s *FactStore) GetRecentFacts(limit int) []model.Fact {
	results, err := s.storage.GetRecent(context.Background(), limit)
	if err != nil {
		log.Printf("GetRecentFacts error: %v", err)
		return []model.Fact{}
	}
	return results
}

func (s *FactStore) GetRandomGeneralFactBundle(count int) ([]model.Fact, error) {
	// Legacy implementation:
	// facts := s.GetFactsByTarget(model.GeneralTarget)
	// shuffle and return count.

	// We can use GetFactsByTarget
	facts := s.GetFactsByTarget(model.GeneralTarget)
	if len(facts) <= count {
		return facts, nil
	}
	// Returning requested count.
	return facts[:count], nil
}

func (s *FactStore) GetAllTargets() []string {
	allFacts := s.GetAllFacts()
	targetsMap := make(map[string]bool)
	for _, f := range allFacts {
		targetsMap[f.Target] = true
	}
	targets := make([]string, 0, len(targetsMap))
	for t := range targetsMap {
		targets = append(targets, t)
	}
	return targets
}
