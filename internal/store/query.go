package store

import (
	"claude_bot/internal/model"
	"context"
)

func (s *FactStore) SearchFuzzy(ctx context.Context, targets, keys []string) ([]model.Fact, error) {
	return s.storage.SearchFuzzy(ctx, targets, keys)
}

func (s *FactStore) GetRecentFacts(ctx context.Context, limit int) ([]model.Fact, error) {
	return s.storage.GetRecent(ctx, limit)
}

func (s *FactStore) GetRandomGeneralFactBundle(ctx context.Context, count int) ([]model.Fact, error) {
	facts, err := s.GetFactsByTarget(ctx, model.GeneralTarget)
	if err != nil {
		return nil, err
	}
	return facts[:min(count, len(facts))], nil
}

func (s *FactStore) GetAllTargets(ctx context.Context) ([]string, error) {
	allFacts, err := s.GetAllFacts(ctx)
	if err != nil {
		return nil, err
	}
	targetsMap := make(map[string]bool)
	for _, f := range allFacts {
		targetsMap[f.Target] = true
	}
	targets := make([]string, 0, len(targetsMap))
	for t := range targetsMap {
		targets = append(targets, t)
	}
	return targets, nil
}
