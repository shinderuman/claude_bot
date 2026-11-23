package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"claude_bot/internal/model"
)

func (s *FactStore) Cleanup(retention time.Duration) int {
	ctx := context.Background()
	threshold := time.Now().Add(-retention)

	allFacts := s.GetAllFacts()
	targets := make(map[string]bool)
	for _, f := range allFacts {
		targets[f.Target] = true
	}

	deletedTotal := 0
	for target := range targets {
		count, err := s.storage.Remove(ctx, target, func(f model.Fact) bool {
			return f.Timestamp.Before(threshold)
		})
		if err != nil {
			log.Printf("Error cleaning up target %s: %v", target, err)
			continue
		}
		deletedTotal += count
	}

	return deletedTotal
}

func (s *FactStore) PerformMaintenance(retentionDays, maxFacts int) int {
	deleted := s.Cleanup(time.Duration(retentionDays) * 24 * time.Hour)

	if maxFacts > 0 {
		removedCount, err := s.storage.EnforceMaxFacts(context.Background(), maxFacts)
		if err != nil {
			log.Printf("Max Facts Enforcement failed: %v", err)
		} else if removedCount > 0 {
			log.Printf("Max Facts Enforcement: Removed %d old facts from Redis", removedCount)
			deleted += removedCount
		}
	}

	if deleted > 0 {
		log.Printf("ファクトメンテナンス完了: %d件削除", deleted)
		go s.saveAsync()
	}

	return deleted
}

func (s *FactStore) ReplaceFacts(target string, factsToRemove, factsToAdd []model.Fact) error {
	err := s.storage.Replace(context.Background(), target, factsToRemove, factsToAdd)
	if err == nil {
		go s.saveAsync()
	}
	return err
}

func (s *FactStore) RemoveFacts(ctx context.Context, target string, shouldRemove func(model.Fact) bool) (int, error) {

	facts, err := s.storage.GetByTarget(ctx, target)
	if err != nil {
		return 0, fmt.Errorf("failed to get facts for target %s: %w", target, err)
	}

	var toRemove []model.Fact
	for _, fact := range facts {
		if shouldRemove(fact) {
			toRemove = append(toRemove, fact)
		}
	}

	if len(toRemove) == 0 {
		return 0, nil
	}

	for _, fact := range toRemove {
		jsonBytes, _ := json.Marshal(fact)
		log.Printf("🗑️ ファクト削除: %s", string(jsonBytes))

		jsonIndentBytes, _ := json.MarshalIndent(fact, "", "    ")
		msg := fmt.Sprintf("🗑️ ファクトを削除しました (Target: %s)\n```\n%s\n```", target, string(jsonIndentBytes))
		s.slackClient.PostMessageAsync(ctx, msg)
	}

	err = s.storage.Replace(ctx, target, toRemove, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to remove facts: %w", err)
	}

	go s.saveAsync()

	return len(toRemove), nil
}
