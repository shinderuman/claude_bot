package store

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"claude_bot/internal/model"
)

func (s *FactStore) Cleanup(ctx context.Context, retention time.Duration) (int, error) {
	threshold := time.Now().Add(-retention)

	allFacts, err := s.GetAllFacts(ctx)
	if err != nil {
		return 0, err
	}
	targets := make(map[string]bool)
	for _, f := range allFacts {
		targets[f.Target] = true
	}

	deletedTotal := 0
	var failures []error
	for target := range targets {
		count, err := s.storage.Remove(ctx, target, func(f model.Fact) bool {
			return f.Timestamp.Before(threshold)
		})
		if err != nil {
			failures = append(failures, fmt.Errorf("ファクトの期限切れ削除 (%s): %w", target, err))
			continue
		}
		deletedTotal += count
	}

	return deletedTotal, errors.Join(failures...)
}

func (s *FactStore) PerformMaintenance(ctx context.Context, retentionDays, maxFacts int) (int, error) {
	deleted, err := s.Cleanup(ctx, time.Duration(retentionDays)*24*time.Hour)
	if err == nil && maxFacts > 0 {
		var removedCount int
		removedCount, err = s.storage.EnforceMaxFacts(ctx, maxFacts)
		if err == nil {
			deleted += removedCount
		}
	}

	if deleted > 0 {
		s.requestBackup()
		if err == nil {
			log.Printf("ファクトメンテナンス完了: %d件削除", deleted)
		}
	}
	return deleted, err
}

func (s *FactStore) ReplaceFacts(ctx context.Context, target string, factsToRemove, factsToAdd []model.Fact) error {
	err := s.storage.Replace(ctx, target, factsToRemove, factsToAdd)
	if err == nil {
		s.requestBackup()
	}
	return err
}
