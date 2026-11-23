package store

import (
	"claude_bot/internal/model"
	"context"
	"log"
)

func (s *FactStore) SearchFuzzy(targets []string, keys []string) []model.Fact {
	results, err := s.storage.SearchFuzzy(context.Background(), targets, keys)
	if err != nil {
		log.Printf("SearchFuzzy error: %v", err)
		return []model.Fact{}
	}
	return results
}

func (s *FactStore) GetRecentFacts(limit int) []model.Fact {
	results, err := s.storage.GetRecent(context.Background(), limit)
	if err != nil {
		log.Printf("GetRecentFacts error: %v", err)
		return []model.Fact{}
	}
	return results
}

func (s *FactStore) GetRandomGeneralFactBundle(count int) ([]model.Fact, error) {

	facts := s.GetFactsByTarget(model.GeneralTarget)
	if len(facts) <= count {
		return facts, nil
	}
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
